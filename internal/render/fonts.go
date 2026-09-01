package render

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	fc "github.com/benoitkugler/textprocessing/fontconfig"
	"github.com/benoitkugler/textprocessing/pango/fcfonts"
	"github.com/benoitkugler/webrender/text"
	"github.com/benoitkugler/webrender/utils"
	"github.com/rs/zerolog"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

// FontScheme is the URL scheme the hermetic fonts are served under. It is a
// scheme of crier's own so that it can never collide with a real resource the
// document asks for.
const FontScheme = "crier-font:"

// HermeticFamily is the family name a hermetic template asks for.
const HermeticFamily = "Go"

// HermeticMonoFamily is the monospace family a hermetic template asks for.
const HermeticMonoFamily = "Go Mono"

// embeddedFonts are the faces compiled into the binary.
//
// They exist so a golden test renders the same pixels on every machine: the
// fonts installed on a developer's laptop, on a CI runner and in a container
// are three different sets, and text laid out with a substituted face is a
// different image every time.
var embeddedFonts = map[string][]byte{
	"goregular": goregular.TTF,
	"gobold":    gobold.TTF,
	"goitalic":  goitalic.TTF,
	"gomono":    gomono.TTF,
}

// hermeticCSS is the stylesheet that registers the embedded faces. It is
// appended to the document rather than loaded as a separate sheet, because
// only the document's own stylesheets are processed with the font
// configuration that @font-face has to reach.
const hermeticCSS = `
@font-face { font-family: "Go"; font-weight: 400; font-style: normal; src: url("crier-font:goregular"); }
@font-face { font-family: "Go"; font-weight: 700; font-style: normal; src: url("crier-font:gobold"); }
@font-face { font-family: "Go"; font-weight: 400; font-style: italic; src: url("crier-font:goitalic"); }
@font-face { font-family: "Go Mono"; font-weight: 400; font-style: normal; src: url("crier-font:gomono"); }
`

// Fonts is the font configuration one rendering uses.
type Fonts struct {
	Config text.FontConfiguration
	// ExtraCSS is appended to the document, and carries the @font-face rules
	// the hermetic mode needs.
	ExtraCSS string
	// Fetcher wraps the caller's fetcher so the embedded faces resolve.
	Fetcher utils.UrlFetcher
}

// FontOptions configures NewFonts.
type FontOptions struct {
	// Hermetic ignores the system's fonts entirely and uses only the embedded
	// ones. It is what makes a golden image reproducible.
	Hermetic bool
	// Dirs are extra directories scanned for fonts.
	Dirs []string
	// CacheDir holds the scanned font index. Empty uses the user cache
	// directory; a first scan takes seconds and every later one milliseconds.
	CacheDir string
	// Fetcher is the caller's resource fetcher, wrapped rather than replaced.
	Fetcher utils.UrlFetcher
	// Logger records the scan.
	Logger zerolog.Logger
}

// NewFonts builds the font configuration.
//
// It has to be the Pango engine: webrender's glyph emission only produces
// glyphs for a Pango layout, and a document laid out with the go-text engine
// reaches a backend with every text run empty. There is a golden test that
// fails on blank text precisely so that swapping the engine cannot pass
// silently.
func NewFonts(o FontOptions) (*Fonts, error) {
	fetcher := o.Fetcher
	if fetcher == nil {
		fetcher = utils.DefaultUrlFetcher
	}

	// Hermetic means "none of this machine's fonts", not "no fonts at all": a
	// project that ships its own faces still gets them, and still renders the
	// same pixels anywhere, because the faces travel with the project.
	var (
		database fc.Fontset
		err      error
	)
	// bundled says the embedded faces have to be registered: always in
	// hermetic mode, and also when the system scan produced nothing, so a
	// machine whose fonts could not be read still renders words rather than
	// blank boxes.
	bundled := o.Hermetic
	if !o.Hermetic {
		database, err = systemFontset(o)
		if err != nil {
			return nil, err
		}
		if len(database) == 0 {
			bundled = true
		}
	}
	extra, err := scanDirs(o.Dirs, o.Logger)
	if err != nil {
		return nil, err
	}
	database = append(database, extra...)

	fontmap := fcfonts.NewFontMap(fc.Standard.Copy(), database)
	cfg := text.NewFontConfigurationPango(fontmap)

	out := &Fonts{Config: cfg, Fetcher: wrapFetcher(fetcher)}
	if bundled {
		out.ExtraCSS = hermeticCSS
	}
	return out, nil
}

// systemFontset scans the machine's fonts, using a cache so only the first run
// pays for the walk.
//
// The outer recover is a last-resort net, not the strategy. The strategy is in
// scanFontFiles, which scans one file at a time so that a font the parser
// crashes on costs that font rather than every font on the machine. This
// catches whatever a per-file recover could not: a crash in the directory walk
// itself, or in the cache.
func systemFontset(o FontOptions) (fc.Fontset, error) {
	return safeScan(o.Logger, func() (fc.Fontset, error) { return scanSystemFonts(o) })
}

// safeScan is the last-resort net around the whole scan, turning both a panic
// and a failure into an empty fontset.
//
// A failure is swallowed for the same reason a panic is: the machine's fonts
// are not crier's to control, and a machine with none — a scratch container, a
// minimal CI image — is a machine crier is meant to work on. It ships its own
// faces so that it can. Refusing to render there would contradict the whole
// point of a static binary with nothing to install alongside it.
//
// Only the system scan is treated this way. A font directory the configuration
// named still fails loudly: that one is a promise the operator made.
func safeScan(log zerolog.Logger, scan func() (fc.Fontset, error)) (set fc.Fontset, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Warn().Interface("panic", r).
				Msg("the system font scan crashed outside any single file; continuing with the bundled fonts only")
			set, err = nil, nil
		}
	}()
	set, err = scan()
	if err != nil {
		log.Warn().Err(err).
			Msg("this machine has no readable system fonts; continuing with the bundled fonts only")
		return nil, nil
	}
	return set, nil
}

// scanSystemFonts is systemFontset without the recover, so the recover has
// something to wrap.
func scanSystemFonts(o FontOptions) (fc.Fontset, error) {
	// fontconfig reports a missing font directory with a bare log.Println, and
	// crier's rule is that everything goes through zerolog. The capture is
	// scoped to the scan, which is the only part of crier that provokes it.
	restore := captureStdlib(o.Logger, "fontconfig", zerolog.DebugLevel)
	defer restore()

	cacheDir := o.CacheDir
	if cacheDir == "" {
		base, err := os.UserCacheDir()
		if err == nil {
			cacheDir = filepath.Join(base, "crier")
		}
	}

	cacheFile := ""
	if cacheDir != "" {
		cacheFile = filepath.Join(cacheDir, "fonts.cache")
		if set, err := fc.LoadFontsetFile(cacheFile); err == nil && len(set) > 0 {
			o.Logger.Debug().Str("cache", cacheFile).Int("fonts", len(set)).Msg("loaded the font cache")
			return set, nil
		}
		if mkErr := os.MkdirAll(cacheDir, 0o755); mkErr != nil {
			cacheFile = ""
		}
	}

	o.Logger.Debug().Msg("scanning system fonts; the first run is the slow one")
	dirs, err := fc.DefaultFontDirs()
	if err != nil {
		return nil, fmt.Errorf("scanning system fonts: %w", err)
	}

	set, stats := scanFontFiles(fc.Standard.Copy(), dirs, o.Logger)
	stats.report(o.Logger)
	if len(set) == 0 {
		return nil, fmt.Errorf("scanning system fonts: none of the %d files under %s could be read",
			stats.Files, strings.Join(dirs, ", "))
	}
	if cacheFile != "" && stats.cacheable() {
		writeFontCache(cacheFile, set, o.Logger)
	}
	return set, nil
}

// writeFontCache saves the scan so the next run is instant.
//
// It is written to a temporary file and renamed, so a crash halfway through
// leaves the previous cache rather than a truncated one that would be loaded
// and believed. A cache that cannot be written is not an error: it costs the
// next run a scan and nothing else.
func writeFontCache(path string, set fc.Fontset, log zerolog.Logger) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "fonts.cache-*")
	if err != nil {
		log.Debug().Err(err).Str("cache", path).Msg("could not write the font cache")
		return
	}
	name := tmp.Name()
	if err := set.Serialize(tmp); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		log.Debug().Err(err).Str("cache", path).Msg("could not write the font cache")
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		log.Debug().Err(err).Str("cache", path).Msg("could not write the font cache")
		return
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		log.Debug().Err(err).Str("cache", path).Msg("could not write the font cache")
		return
	}
	log.Debug().Str("cache", path).Int("fonts", len(set)).Msg("wrote the font cache")
}

// scanDirs reads the font directories the configuration named.
//
// A directory that cannot be read is a mistake worth reporting: a project that
// says where its fonts are and then renders with substitutes has produced the
// wrong image quietly.
func scanDirs(dirs []string, log zerolog.Logger) (fc.Fontset, error) {
	var out fc.Fontset
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		set, err := fc.Standard.ScanFontDirectories(dir)
		if err != nil {
			return nil, fmt.Errorf("scanning the font directory %s: %w", dir, err)
		}
		if len(set) == 0 {
			log.Warn().Str("dir", dir).Msg("a font directory held no fonts")
		}
		log.Debug().Str("dir", dir).Int("fonts", len(set)).Msg("scanned a font directory")
		out = append(out, set...)
	}
	return out, nil
}

// wrapFetcher answers the crier-font scheme and passes everything else on.
func wrapFetcher(next utils.UrlFetcher) utils.UrlFetcher {
	return func(url string) (utils.RemoteRessource, error) {
		name, ok := strings.CutPrefix(url, FontScheme)
		if !ok {
			return next(url)
		}
		data, known := embeddedFonts[name]
		if !known {
			return utils.RemoteRessource{}, fmt.Errorf("unknown embedded font %q", name)
		}
		return utils.RemoteRessource{
			Content:  bytes.NewReader(data),
			MimeType: "font/ttf",
			Filename: name + ".ttf",
		}, nil
	}
}

var _ io.Reader = (*bytes.Reader)(nil)
