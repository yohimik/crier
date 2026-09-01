package render

import (
	"os"
	"path/filepath"
	"strings"

	fc "github.com/benoitkugler/textprocessing/fontconfig"
	"github.com/rs/zerolog"
)

// scanStats is what one pass over a machine's font files came to.
type scanStats struct {
	// Files is how many candidate font files were opened.
	Files int
	// Faces is how many faces came out of them.
	Faces int
	// Skipped are the files that could not be read, by path.
	Skipped []string
}

// skipRatio is how much of a machine's font collection may fail before the
// result is treated as a bad scan rather than as a few bad files.
//
// A couple of unreadable files is normal — macOS ships one that crashes the
// parser — and caching around them is right. A quarter of them failing is not a
// font problem: it is a disk, a permission or a version problem, and pinning
// that into the cache would make a transient failure permanent.
const skipRatio = 4

// scanFontFiles walks the font directories and scans each file on its own.
//
// This is the whole point of the function. The library's own
// ScanFontDirectories walks and parses in one pass, and the parser panics
// rather than erroring on a file it cannot make sense of — so a single bad file
// takes the entire scan with it. On macOS that is not hypothetical:
// /System/Library/Fonts/Supplemental/NISC18030.ttf ships with the operating
// system and panics inside LoadSummary, which meant every Mac silently lost all
// two and a half thousand of its faces and rendered with the bundled Go faces
// instead.
//
// Scanning file by file inside a recover costs one deferred call per file and
// buys the other 2,610 faces.
func scanFontFiles(config *fc.Config, dirs []string, log zerolog.Logger) (fc.Fontset, scanStats) {
	var (
		out   fc.Fontset
		stats scanStats
	)
	for _, dir := range dirs {
		// A directory that cannot be walked is skipped rather than fatal: these
		// are the machine's directories, not the operator's, and one of them
		// being unreadable is not a reason to render without fonts.
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() || !fontFileName(info.Name()) {
				return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
			}
			stats.Files++
			set, ok := scanOne(config, path, log)
			if !ok {
				stats.Skipped = append(stats.Skipped, path)
				return nil
			}
			stats.Faces += len(set)
			out = append(out, set...)
			return nil
		})
	}
	return out, stats
}

// scanOne reads one font file, surviving both a refusal and a crash.
func scanOne(config *fc.Config, path string, log zerolog.Logger) (set fc.Fontset, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Debug().Str("file", path).Interface("panic", r).
				Msg("the font parser crashed on this file; skipping it")
			set, ok = nil, false
		}
	}()
	set, err := config.ScanFontFile(path)
	if err != nil {
		log.Debug().Str("file", path).Err(err).Msg("not a readable font file; skipping it")
		return nil, false
	}
	return set, true
}

// fontFileName mirrors the library's own filter, which is unexported: hidden
// files, and the metric and encoding sidecars that live beside a font without
// being one.
func fontFileName(name string) bool {
	if name == "" || name[0] == '.' {
		return false
	}
	for _, suffix := range []string{".enc.gz", ".afm", ".pfm", ".dir", ".scale", ".alias"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
}

// report logs what a scan came to: one summary line, and the skipped paths at
// debug level so a machine that loses a font can be told which one.
func (s scanStats) report(log zerolog.Logger) {
	if len(s.Skipped) == 0 {
		log.Debug().Int("files", s.Files).Int("fonts", s.Faces).Msg("scanned system fonts")
		return
	}
	log.Warn().
		Int("files", s.Files).
		Int("fonts", s.Faces).
		Int("skipped", len(s.Skipped)).
		Msg("some font files could not be read and were skipped; the rest are available")
	for _, path := range s.Skipped {
		log.Debug().Str("file", path).Msg("skipped a font file")
	}
}

// cacheable reports whether a scan's result is worth keeping.
//
// The survivors are cached, skips and all: on macOS there is always at least
// one bad file, and rescanning several hundred fonts on every render to avoid
// caching around it would be a poor trade. A scan that lost a large fraction of
// the collection is not cached, because that is the shape of a transient
// failure rather than of a couple of broken files, and a cache is exactly the
// wrong place to make a transient failure permanent.
//
// Either way the cache is a snapshot: deleting the cache directory rescans.
func (s scanStats) cacheable() bool {
	if s.Faces == 0 {
		return false
	}
	return len(s.Skipped)*skipRatio < s.Files
}
