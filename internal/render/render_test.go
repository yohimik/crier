package render

import (
	"context"
	"image"
	"image/color"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	// The video tests run this same binary as a stand-in for ffmpeg.
	if os.Getenv(fakeFFmpegEnv) != "" {
		fakeFFmpegMain()
		return
	}
	// webrender logs to stdout by default; the bridge is what keeps that out of
	// crier's own output, and the tests exercise it too.
	CaptureLogs(zerolog.Nop())
	os.Exit(m.Run())
}

// testLogger and nopLogger let a test capture what webrender warns about.
func testLogger(w io.Writer) zerolog.Logger {
	return zerolog.New(w).Level(zerolog.WarnLevel)
}

func nopLogger() zerolog.Logger { return zerolog.Nop() }

func hermeticFonts(t *testing.T) *Fonts {
	t.Helper()
	f, err := NewFonts(FontOptions{Hermetic: true})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func render(t *testing.T, o Options) *image.RGBA {
	t.Helper()
	if o.Fonts == nil {
		o.Fonts = hermeticFonts(t)
	}
	o.Logger = zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel)
	img, err := RenderOne(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// countNonBackground is how the tests ask "did anything get drawn".
func countNonBackground(img *image.RGBA, bg color.RGBA) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y) != bg {
				n++
			}
		}
	}
	return n
}

func TestRenderSolidRectangle(t *testing.T) {
	img := render(t, Options{
		HTML:       `<html><body style="margin:0"><div style="width:50px;height:20px;background:#ff0000"></div></body></html>`,
		Width:      100,
		Height:     40,
		Background: color.White,
	})
	if got := img.Bounds(); got.Dx() != 100 || got.Dy() != 40 {
		t.Fatalf("bounds = %v", got)
	}
	if got := img.RGBAAt(10, 10); got != (color.RGBA{R: 255, A: 255}) {
		t.Errorf("inside the box = %v, want red", got)
	}
	if got := img.RGBAAt(80, 30); got != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Errorf("outside the box = %v, want white", got)
	}
}

func TestRenderScaleMultipliesTheOutput(t *testing.T) {
	img := render(t, Options{
		HTML:       `<html><body></body></html>`,
		Width:      100,
		Height:     40,
		Scale:      2,
		Background: color.White,
	})
	if got := img.Bounds(); got.Dx() != 200 || got.Dy() != 80 {
		t.Fatalf("bounds = %v, want the scaled size", got)
	}
}

func TestRenderSuperSampleKeepsTheNominalSize(t *testing.T) {
	img := render(t, Options{
		HTML:        `<html><body></body></html>`,
		Width:       100,
		Height:      40,
		SuperSample: 2,
		Background:  color.White,
	})
	if got := img.Bounds(); got.Dx() != 100 || got.Dy() != 40 {
		t.Fatalf("bounds = %v, want the nominal size", got)
	}
}

func TestInjectedPageRuleBeatsTheDocumentsOwn(t *testing.T) {
	img := render(t, Options{
		HTML:       `<html><head><style>@page { size: 300px 300px }</style></head><body></body></html>`,
		Width:      120,
		Height:     50,
		Background: color.White,
	})
	if got := img.Bounds(); got.Dx() != 120 || got.Dy() != 50 {
		t.Fatalf("bounds = %v, want crier's own page size to win", got)
	}
}

func TestDocumentPageRuleDecidesWhenNoSizeIsGiven(t *testing.T) {
	img := render(t, Options{
		HTML:       `<html><head><style>@page { size: 200px 60px; margin: 0 }</style></head><body></body></html>`,
		Background: color.White,
	})
	if got := img.Bounds(); got.Dx() != 200 || got.Dy() != 60 {
		t.Fatalf("bounds = %v, want the document's page size", got)
	}
}

// TestTextIsActuallyDrawn is the regression test for the trap this backend was
// written around: webrender only emits glyphs for a Pango layout, and a build
// wired to the go-text engine renders every page with the text missing and no
// error anywhere. A blank page here means the font engine was swapped.
func TestTextIsActuallyDrawn(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	withText := render(t, Options{
		HTML: `<html><body style="margin:0;font-family:Go;font-size:30px;color:#000">` +
			`Hello crier</body></html>`,
		Width:      300,
		Height:     60,
		Background: white,
	})
	drawn := countNonBackground(withText, white)
	if drawn == 0 {
		t.Fatal("no text was drawn: the Pango engine is not in use, or the hermetic fonts did not load")
	}
	if drawn < 100 {
		t.Fatalf("only %d pixels differ from the background; the text is not really there", drawn)
	}

	blank := render(t, Options{
		HTML:       `<html><body style="margin:0"></body></html>`,
		Width:      300,
		Height:     60,
		Background: white,
	})
	if countNonBackground(blank, white) != 0 {
		t.Fatal("an empty document should be blank")
	}
}

// TestRepeatedGlyphsAreAllDrawn is the regression test for a glyph cache that
// was transformed in place: the first "i" of a run appeared and every later one
// vanished, because the cached outline had already been moved.
func TestRepeatedGlyphsAreAllDrawn(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	ink := func(text string) int {
		return countNonBackground(render(t, Options{
			HTML: `<html><body style="margin:0;font-family:Go;font-size:40px;color:#000">` +
				text + `</body></html>`,
			Width:      400,
			Height:     60,
			Background: white,
		}), white)
	}
	one := ink("i")
	four := ink("iiii")
	if one == 0 {
		t.Fatal("nothing was drawn at all")
	}
	if float64(four) < 3.5*float64(one) {
		t.Fatalf("four letters drew %d pixels of ink and one drew %d; the repeats are missing", four, one)
	}
}

func TestTransparentBackgroundIsKept(t *testing.T) {
	img := render(t, Options{
		HTML:   `<html><body style="margin:0"></body></html>`,
		Width:  10,
		Height: 10,
	})
	if got := img.RGBAAt(5, 5); got.A != 0 {
		t.Errorf("pixel = %v, want fully transparent", got)
	}
}

func TestExtraCSSIsApplied(t *testing.T) {
	img := render(t, Options{
		HTML:       `<html><body style="margin:0"><div id="x" style="width:10px;height:10px"></div></body></html>`,
		ExtraCSS:   []string{`#x { background: #00ff00 }`},
		Width:      20,
		Height:     20,
		Background: color.White,
	})
	if got := img.RGBAAt(5, 5); got != (color.RGBA{G: 255, A: 255}) {
		t.Errorf("pixel = %v, want green", got)
	}
}

func TestRenderErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Render(ctx, Options{HTML: "<html></html>"}); err == nil {
		t.Error("expected an error with no font configuration")
	}

	fonts := hermeticFonts(t)
	if _, err := RenderOne(ctx, Options{HTML: "<html><body></body></html>", Fonts: fonts,
		Width: 20001, Height: 20001}); err == nil {
		t.Error("expected an error for an oversized page")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Render(cancelled, Options{HTML: "<html><body></body></html>", Fonts: fonts, Width: 10, Height: 10}); err == nil {
		t.Error("expected the cancelled context to be reported")
	}
}

func TestRenderOneRefusesMultiplePages(t *testing.T) {
	fonts := hermeticFonts(t)
	_, err := RenderOne(context.Background(), Options{
		HTML: `<html><body style="margin:0">` +
			`<div style="height:40px">a</div><div style="height:40px">b</div>` +
			`<div style="height:40px">c</div><div style="height:40px">d</div>` +
			`</body></html>`,
		Width:  50,
		Height: 50,
		Fonts:  fonts,
	})
	if err == nil || !strings.Contains(err.Error(), "pages") {
		t.Fatalf("err = %v", err)
	}
}

func TestMediaTypeDefaultsToScreen(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	img := render(t, Options{
		HTML: `<html><head><style>` +
			`@media screen { div { background: #0000ff } }` +
			`@media print { div { background: #ff0000 } }` +
			`</style></head><body style="margin:0"><div style="width:10px;height:10px"></div></body></html>`,
		Width:      20,
		Height:     20,
		Background: white,
	})
	if got := img.RGBAAt(5, 5); got != (color.RGBA{B: 255, A: 255}) {
		t.Errorf("pixel = %v, want the screen rule to apply", got)
	}
}

func TestComposeOrder(t *testing.T) {
	o := Options{
		HTML:     "<html><body>x</body></html>",
		ExtraCSS: []string{"a{}", "   "},
		Width:    10, Height: 20,
		Fonts: &Fonts{ExtraCSS: "@font-face{}"},
	}
	got := o.compose()
	iFont := strings.Index(got, "@font-face{}")
	iCSS := strings.Index(got, "a{}")
	iPage := strings.Index(got, "@page")
	if iFont < 0 || iCSS < 0 || iPage < 0 {
		t.Fatalf("compose = %q", got)
	}
	if !(iFont < iCSS && iCSS < iPage) {
		t.Errorf("wrong order: font=%d css=%d page=%d", iFont, iCSS, iPage)
	}
	if strings.Contains(got, "<style>   </style>") {
		t.Error("blank stylesheets should be skipped")
	}
	if !strings.Contains(got, "size: 10px 20px") {
		t.Errorf("page rule missing: %q", got)
	}
}

func TestFill(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	fill(img, color.RGBA{R: 1, G: 2, B: 3, A: 4})
	for _, p := range []image.Point{{}, {X: 1, Y: 1}} {
		if got := img.RGBAAt(p.X, p.Y); got != (color.RGBA{R: 1, G: 2, B: 3, A: 4}) {
			t.Errorf("pixel %v = %v", p, got)
		}
	}
}
