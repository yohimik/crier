package render

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

// pagedHTML is a document that overflows its page on purpose, with a running
// header and footer in the page margin boxes and a page counter in the footer.
//
// It is the shape the announce card takes once a changelog is longer than one
// page, reduced to the parts under test.
const pagedHTML = `<html><body>
<style>
 body { margin: 0; font-family: Go; background: #fff; font-size: 20px }
 .card { height: 150px; background: #ccddff; margin: 0 0 10px 0; break-inside: avoid }
 @page {
   size: 300px 300px;
   margin: 40px;
   @top-center { content: "crier"; font-family: Go; font-size: 16px; color: #cc0000 }
   @bottom-center { content: counter(page) " / " counter(pages); font-family: Go; font-size: 16px; color: #00aa00 }
 }
</style>
<div class="card">one</div><div class="card">two</div><div class="card">three</div>
</body></html>`

// inkNear counts pixels close to want, which is how a test looks for one
// specific piece of coloured text rather than for "something was drawn".
func inkNear(img *image.RGBA, want color.RGBA) int {
	n := 0
	b := img.Bounds()
	near := func(a, c uint8) bool {
		if a > c {
			return a-c < 60
		}
		return c-a < 60
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := img.RGBAAt(x, y)
			if near(p.R, want.R) && near(p.G, want.G) && near(p.B, want.B) {
				n++
			}
		}
	}
	return n
}

func renderPages(t *testing.T, o Options) []*image.RGBA {
	t.Helper()
	if o.Fonts == nil {
		o.Fonts = hermeticFonts(t)
	}
	if o.Background == nil {
		o.Background = color.White
	}
	o.Logger = zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.WarnLevel)
	pages, err := Render(context.Background(), o)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return pages
}

// TestPageMarginIsTheTemplatesToChoose is the regression test for a bug found
// while verifying that page margin boxes work at all.
//
// crier appended `@page { size: WxH; margin: 0 }` after the document's own
// styles. The size belongs there. The margin did not: it overruled any margin
// the template asked for, which left the margin boxes with no room and drew the
// header and footer half off the page. A paginated template could not have
// worked.
func TestPageMarginIsTheTemplatesToChoose(t *testing.T) {
	red := color.RGBA{R: 0xcc, A: 0xff}
	green := color.RGBA{G: 0xaa, A: 0xff}

	// The size arrives as Width and Height, the way the pipeline passes it, so
	// the injected rule and the template's margin have to coexist.
	pages := renderPages(t, Options{HTML: pagedHTML, Width: 300, Height: 300})
	if len(pages) != 3 {
		t.Fatalf("laid out into %d pages, want 3", len(pages))
	}

	// The control is the old behaviour, reproduced by overruling the margin
	// after the document the way the injected rule used to. Comparing against
	// it rather than against a fixed pixel count is what keeps this test about
	// the bug instead of about the width of the word "crier".
	squashed := renderPages(t, Options{
		HTML: pagedHTML, Width: 300, Height: 300,
		ExtraCSS: []string{"@page { margin: 0 }"},
	})

	for i, p := range pages {
		// The header sits inside the top margin and the footer inside the
		// bottom one. With no margin to sit in, both were clipped to a sliver.
		if got, bad := inkNear(p, red), inkNear(squashed[i], red); got <= bad {
			t.Errorf("page %d draws %d header pixels with a page margin and %d without; "+
				"the margin box is being squashed", i+1, got, bad)
		}
		if got, bad := inkNear(p, green), inkNear(squashed[i], green); got <= bad {
			t.Errorf("page %d draws %d footer pixels with a page margin and %d without; "+
				"the margin box is being squashed", i+1, got, bad)
		}
	}
}

// TestPageCountersCountThePages asserts the footer says a different thing on
// every page, which is what proves counter(page) advances rather than being
// painted once and repeated.
func TestPageCountersCountThePages(t *testing.T) {
	green := color.RGBA{G: 0xaa, A: 0xff}
	pages := renderPages(t, Options{HTML: pagedHTML, Width: 300, Height: 300})
	if len(pages) != 3 {
		t.Fatalf("laid out into %d pages, want 3", len(pages))
	}
	seen := map[int]int{}
	for i, p := range pages {
		n := inkNear(p, green)
		if prev, dup := seen[n]; dup {
			t.Errorf("pages %d and %d have identically shaped footers (%d pixels); "+
				"the page counter is not advancing", prev+1, i+1, n)
		}
		seen[n] = i
	}
}

// TestPageMarginDefaultsToZero keeps the other half of the policy honest: a
// template that says nothing about the page margin still gets an edge-to-edge
// page, because that is what a social image is.
func TestPageMarginDefaultsToZero(t *testing.T) {
	const html = `<html><body><style>body{margin:0}</style>` +
		`<div style="width:100px;height:100px;background:#ff0000"></div></body></html>`
	pages := renderPages(t, Options{HTML: html, Width: 100, Height: 100})
	if len(pages) != 1 {
		t.Fatalf("laid out into %d pages, want 1", len(pages))
	}
	// The box fills the page only if the page margin is zero.
	if c := pages[0].RGBAAt(99, 99); c.R < 200 || c.G > 60 {
		t.Errorf("the bottom right pixel is %v, want red; the page has a margin it "+
			"was not asked for", c)
	}
}

// TestPagedGolden pins the whole three-page flow, margin boxes and all, so a
// change in webrender's paged layout is caught as a picture rather than as a
// pixel count.
func TestPagedGolden(t *testing.T) {
	update := os.Getenv(UpdateGoldenEnv) != ""
	dir := filepath.Join("testdata", "golden")
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pages := renderPages(t, Options{HTML: pagedHTML, Width: 300, Height: 300})
	if len(pages) != 3 {
		t.Fatalf("laid out into %d pages, want 3", len(pages))
	}
	for i, img := range pages {
		name := fileNameForPage(i)
		path := filepath.Join(dir, name+".png")
		if update {
			writeGolden(t, path, img)
			continue
		}
		want := readGolden(t, path)
		if err := compareImages(want, img); err != nil {
			t.Fatalf("%s does not match its golden: %v\n"+
				"if the change is intended, regenerate with %s=1 go test ./internal/render",
				name, err, UpdateGoldenEnv)
		}
	}
}

func fileNameForPage(i int) string {
	return "paged_margin_boxes_" + string(rune('1'+i))
}
