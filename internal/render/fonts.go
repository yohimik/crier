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

	var (
		database fc.Fontset
		err      error
	)
	if o.Hermetic {
		database = fc.Fontset{}
	} else {
		database, err = systemFontset(o)
		if err != nil {
			return nil, err
		}
	}

	fontmap := fcfonts.NewFontMap(fc.Standard.Copy(), database)
	cfg := text.NewFontConfigurationPango(fontmap)

	out := &Fonts{Config: cfg, Fetcher: wrapFetcher(fetcher)}
	if o.Hermetic {
		out.ExtraCSS = hermeticCSS
	}
	return out, nil
}

// systemFontset scans the machine's fonts, using a cache so only the first run
// pays for the walk.
func systemFontset(o FontOptions) (fc.Fontset, error) {
	cacheDir := o.CacheDir
	if cacheDir == "" {
		base, err := os.UserCacheDir()
		if err == nil {
			cacheDir = filepath.Join(base, "crier")
		}
	}

	var (
		set fc.Fontset
		err error
	)
	cacheFile := ""
	if cacheDir != "" {
		cacheFile = filepath.Join(cacheDir, "fonts.cache")
		if set, err = fc.LoadFontsetFile(cacheFile); err == nil {
			o.Logger.Debug().Str("cache", cacheFile).Int("fonts", len(set)).Msg("loaded the font cache")
		} else {
			set = nil
		}
	}
	if set == nil {
		if cacheDir != "" {
			if mkErr := os.MkdirAll(cacheDir, 0o755); mkErr != nil {
				cacheFile = ""
			}
		}
		o.Logger.Debug().Msg("scanning system fonts; the first run is the slow one")
		if cacheFile != "" {
			set, err = fc.ScanAndCache(cacheFile)
		} else {
			var dirs []string
			if dirs, err = fc.DefaultFontDirs(); err == nil {
				set, err = fc.Standard.ScanFontDirectories(dirs...)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("scanning system fonts: %w", err)
		}
		o.Logger.Debug().Int("fonts", len(set)).Msg("scanned system fonts")
	}

	for _, dir := range o.Dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		extra, scanErr := fc.Standard.ScanFontDirectories(dir)
		if scanErr != nil {
			o.Logger.Warn().Str("dir", dir).Err(scanErr).Msg("could not scan a font directory")
			continue
		}
		o.Logger.Debug().Str("dir", dir).Int("fonts", len(extra)).Msg("scanned an extra font directory")
		set = append(set, extra...)
	}
	return set, nil
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
