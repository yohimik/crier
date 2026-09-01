package render

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

// The overflow recipes are the ones docs/templates.md tells template authors to
// use, and they are only worth documenting if they demonstrably work. Each
// case here renders a string that cannot fit and asserts nothing was drawn
// outside the card.

const overflowCardCSS = `width:280px;height:80px;margin:10px;font-size:24px;color:#000;background:#eef;box-sizing:border-box`

// inkOutside counts pixels outside the card that are not the page background.
func inkOutside(img *image.RGBA, card image.Rectangle, bg color.RGBA) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if (image.Point{X: x, Y: y}).In(card) {
				continue
			}
			if img.RGBAAt(x, y) != bg {
				n++
			}
		}
	}
	return n
}

func renderOverflow(t *testing.T, style, text string) *image.RGBA {
	t.Helper()
	return render(t, Options{
		HTML: `<html><head><style>body{margin:0;background:#fff;font-family:Go}</style></head>` +
			`<body><div style="` + overflowCardCSS + `;` + style + `">` + text + `</div></body></html>`,
		Width:      300,
		Height:     100,
		Background: color.White,
	})
}

func TestTextOverflowRecipesKeepTextInsideTheCard(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	card := image.Rect(10, 10, 290, 90)

	for _, tt := range []struct {
		name  string
		style string
		text  string
	}{
		{
			name:  "single line ellipsis",
			style: "white-space:nowrap;overflow:hidden;text-overflow:ellipsis",
			text:  overflowText,
		},
		{
			name:  "clip",
			style: "overflow:hidden",
			text:  overflowText,
		},
		{
			name:  "break long words",
			style: "overflow:hidden;overflow-wrap:break-word",
			text:  overflowText,
		},
		{
			name:  "line clamp",
			style: "overflow:hidden;overflow-wrap:break-word;max-lines:2;continue:discard;block-ellipsis:auto",
			text:  overflowWords,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			img := renderOverflow(t, tt.style, tt.text)
			if n := inkOutside(img, card, white); n != 0 {
				t.Errorf("%d pixels were drawn outside the card; the recipe does not contain the text", n)
			}
			// And something was drawn inside, so the test is not passing by
			// rendering nothing at all.
			if countNonBackground(img, white) < 100 {
				t.Error("almost nothing was drawn; the case is not exercising anything")
			}
		})
	}
}

// TestTextOverflowWithoutARecipeSpills is the control: the recipes are needed
// because without them the text really does leave the box.
func TestTextOverflowWithoutARecipeSpills(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	card := image.Rect(10, 10, 290, 90)
	img := renderOverflow(t, "", overflowText)
	if inkOutside(img, card, white) == 0 {
		t.Fatal("the control case did not overflow, so it proves nothing about the recipes")
	}
}

// TestLineClampStopsAtTheLineLimit checks that max-lines really stops the text
// where it says, rather than only letting overflow:hidden cut a third line in
// half.
func TestLineClampStopsAtTheLineLimit(t *testing.T) {
	clamped := renderOverflow(t,
		"overflow:hidden;max-lines:2;continue:discard;block-ellipsis:auto", overflowWords)
	plain := renderOverflow(t, "overflow:hidden", overflowWords)

	// The card runs from y=10 to y=90; a third line of 24px text starts around
	// y=72, so that band tells the two apart.
	band := image.Rect(10, 72, 290, 90)
	if inkIn(plain, band) == 0 {
		t.Fatal("the control case has no third line, so the comparison proves nothing")
	}
	if n := inkIn(clamped, band); n != 0 {
		t.Errorf("%d pixels of a third line survived the clamp", n)
	}
}

// inkIn counts pixels inside a rectangle that are neither the page nor the
// card background.
func inkIn(img *image.RGBA, r image.Rectangle) int {
	page := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	card := color.RGBA{R: 0xee, G: 0xee, B: 0xff, A: 255}
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c != page && c != card {
				n++
			}
		}
	}
	return n
}

// TestWebkitLineClampIsNotSupported records what does not work, so the
// documentation's "not supported" list cannot go stale quietly.
func TestWebkitLineClampIsNotSupported(t *testing.T) {
	var log strings.Builder
	CaptureLogs(testLogger(&log))
	defer CaptureLogs(nopLogger())

	renderOverflow(t, "display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical", overflowWords)
	if !strings.Contains(log.String(), "-webkit-line-clamp") {
		t.Fatalf("webrender no longer warns about -webkit-line-clamp; if it now supports it, "+
			"docs/templates.md has to say so. Log was:\n%s", log.String())
	}
}
