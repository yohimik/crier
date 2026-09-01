package render

import (
	"errors"
	"testing"

	fc "github.com/benoitkugler/textprocessing/fontconfig"
	"github.com/rs/zerolog"
)

// TestSystemFontScanSurvivesAPanickingFont is a regression test.
//
// The font parser underneath panics rather than erroring on a file it cannot
// read — a nil dereference reached from a directory walk — and one such file
// anywhere on the machine took crier down while it was rendering something
// that never needed that font. A machine's font collection is not something
// crier controls, so a crash there cannot be a crash here.
func TestSystemFontScanSurvivesAPanickingFont(t *testing.T) {
	set, err := safeScan(zerolog.Nop(), func() (fc.Fontset, error) {
		var broken *struct{ n int }
		_ = broken.n // the exact shape of the upstream crash: a nil dereference
		return nil, nil
	})
	if err != nil || set != nil {
		t.Fatalf("safeScan = %v, %v; want an empty fontset and no error", set, err)
	}

	// A real error still comes back as an error: only a crash is swallowed.
	want := errors.New("no font directories")
	if _, err := safeScan(zerolog.Nop(), func() (fc.Fontset, error) { return nil, want }); !errors.Is(err, want) {
		t.Errorf("safeScan swallowed a real error: %v", err)
	}
}

// TestBundledFontsWhenTheSystemHasNone checks the other half of the fallback:
// an empty system fontset registers the embedded faces, so text still draws.
func TestBundledFontsWhenTheSystemHasNone(t *testing.T) {
	fonts, err := NewFonts(FontOptions{Hermetic: true, Logger: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	if fonts.ExtraCSS == "" {
		t.Error("no bundled font CSS, so nothing would have a face to draw with")
	}
}
