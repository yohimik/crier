package render

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
)

// master builds an asymmetric test image: a 100×50 landscape whose quadrants
// are different colours, so a crop can be told from a squash.
func master(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.RGBA
			switch {
			case x < w/2 && y < h/2:
				c = color.RGBA{R: 255, A: 255}
			case x >= w/2 && y < h/2:
				c = color.RGBA{G: 255, A: 255}
			case x < w/2:
				c = color.RGBA{B: 255, A: 255}
			default:
				c = color.RGBA{R: 255, G: 255, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// TestFitRectCoverFillsAndCentresTheCrop is the story case: a square card into
// a tall frame keeps the middle and loses the top and bottom equally.
func TestFitRectCoverFillsAndCentresTheCrop(t *testing.T) {
	// 1080 square into a 1080×1920 story: scale by 1920/1080, so the width
	// overflows to 1920 and 420 is cropped from each side.
	g := FitRect(1080, 1080, 1080, 1920, config.FitCover)
	if g.Dst != image.Rect(0, 0, 1080, 1920) {
		t.Errorf("dst = %v", g.Dst)
	}
	if g.Draw.Dy() != 1920 {
		t.Errorf("draw height = %d, want the frame filled", g.Draw.Dy())
	}
	if g.Draw.Dx() != 1920 {
		t.Errorf("draw width = %d, want the source scaled to 1920", g.Draw.Dx())
	}
	if g.Draw.Min.X != -420 || g.Draw.Max.X != 1500 {
		t.Errorf("draw = %v, want the overflow split evenly", g.Draw)
	}
	if g.Letterboxed {
		t.Error("cover never letterboxes")
	}

	// The other way: a wide master into a square frame crops left and right.
	g = FitRect(200, 100, 100, 100, config.FitCover)
	if g.Draw != image.Rect(-50, 0, 150, 100) {
		t.Errorf("draw = %v", g.Draw)
	}

	// Cover always covers, even when rounding would leave a seam.
	g = FitRect(1000, 333, 1080, 360, config.FitCover)
	if g.Draw.Dx() < 1080 || g.Draw.Dy() < 360 {
		t.Errorf("draw = %v, want it to reach every edge", g.Draw)
	}
}

// TestFitRectContainLetterboxes is the other half: nothing is lost and the
// remainder is background.
func TestFitRectContainLetterboxes(t *testing.T) {
	// 200×100 into 100×100: scaled to 100×50, centred vertically with 25 above
	// and 25 below.
	g := FitRect(200, 100, 100, 100, config.FitContain)
	if g.Draw != image.Rect(0, 25, 100, 75) {
		t.Errorf("draw = %v", g.Draw)
	}
	if !g.Letterboxed {
		t.Error("this one letterboxes")
	}

	// 100×200 into 100×100: bars left and right.
	g = FitRect(100, 200, 100, 100, config.FitContain)
	if g.Draw != image.Rect(25, 0, 75, 100) {
		t.Errorf("draw = %v", g.Draw)
	}

	// The same aspect fits exactly and needs no bars at all.
	g = FitRect(200, 100, 400, 200, config.FitContain)
	if g.Draw != image.Rect(0, 0, 400, 200) {
		t.Errorf("draw = %v", g.Draw)
	}
	if g.Letterboxed {
		t.Error("an exact fit has no background to show")
	}
}

// TestFitRectOddRemainderMatchesFFmpeg: integer division puts the extra pixel
// on the right and the bottom, which is what (ow-iw)/2 does — and is why a
// clip and a still of the same card line up.
func TestFitRectOddRemainderMatchesFFmpeg(t *testing.T) {
	// 100×100 into 101×100 contain: scaled to 100×100, one pixel spare.
	g := FitRect(100, 100, 101, 100, config.FitContain)
	if g.Draw.Min.X != 0 {
		t.Errorf("draw = %v, want the odd pixel on the right", g.Draw)
	}
	if got := g.Dst.Dx() - g.Draw.Max.X; got != 1 {
		t.Errorf("right margin = %d, want the remainder", got)
	}
}

func TestFitRectStretchIgnoresAspect(t *testing.T) {
	g := FitRect(200, 100, 100, 400, config.FitStretch)
	if g.Draw != image.Rect(0, 0, 100, 400) {
		t.Errorf("draw = %v, want the whole frame", g.Draw)
	}
	if g.Letterboxed {
		t.Error("stretch leaves nothing over")
	}
}

func TestFitRectDegenerateInputs(t *testing.T) {
	for _, tt := range []struct{ sw, sh, dw, dh int }{
		{0, 100, 50, 50}, {100, 0, 50, 50}, {100, 100, 0, 50}, {100, 100, 50, 0},
	} {
		g := FitRect(tt.sw, tt.sh, tt.dw, tt.dh, config.FitCover)
		if g.Draw != g.Dst {
			t.Errorf("%v: draw = %v, want it to fall back to the frame", tt, g.Draw)
		}
	}
	// A mode nobody declared behaves as none rather than as nothing.
	g := FitRect(100, 100, 50, 50, config.Fit("nonsense"))
	if g.Draw != g.Dst {
		t.Errorf("draw = %v", g.Draw)
	}
}

// TestFitImageCoverKeepsTheMiddle checks the pixels rather than the geometry:
// a wide master cover-fitted into a square keeps its middle columns.
func TestFitImageCoverKeepsTheMiddle(t *testing.T) {
	src := master(200, 100)
	out := FitImage(src, 100, 100, config.FitCover, color.Black)
	if out.Bounds() != image.Rect(0, 0, 100, 100) {
		t.Fatalf("bounds = %v", out.Bounds())
	}
	// The source's own quadrant boundary sits at x=100 of 200, which after a
	// centred crop lands in the middle of the output.
	left := out.RGBAAt(10, 10)
	right := out.RGBAAt(90, 10)
	if left.R == 0 {
		t.Errorf("top left = %v, want the red quadrant", left)
	}
	if right.G == 0 {
		t.Errorf("top right = %v, want the green quadrant", right)
	}
	// No background anywhere: cover fills.
	for _, p := range []image.Point{{X: 0, Y: 0}, {X: 99, Y: 0}, {X: 0, Y: 99}, {X: 99, Y: 99}} {
		if c := out.RGBAAt(p.X, p.Y); c.R == 0 && c.G == 0 && c.B == 0 {
			t.Errorf("corner %v is background; cover should have filled it", p)
		}
	}
}

// TestFitImageContainPaintsExactBackground is the pixel-level check the plan
// asked for: the bars are the colour that was configured, exactly.
func TestFitImageContainPaintsExactBackground(t *testing.T) {
	src := master(200, 100)
	bg := color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 255}
	out := FitImage(src, 100, 100, config.FitContain, bg)

	// The image occupies y=25..74; everything above and below is background.
	for _, y := range []int{0, 12, 24, 75, 88, 99} {
		got := out.RGBAAt(50, y)
		if got != bg {
			t.Errorf("row %d = %v, want the background %v", y, got, bg)
		}
	}
	// And the middle is the picture, not the background.
	if got := out.RGBAAt(50, 50); got == bg {
		t.Error("the picture is missing from the middle of the letterbox")
	}
}

// TestFitImageFlattensTransparency: a platform that composites onto its own
// colour would otherwise decide what a transparent card looks like.
func TestFitImageFlattensTransparency(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	// Left half fully transparent, right half opaque red.
	for y := 0; y < 10; y++ {
		for x := 5; x < 10; x++ {
			src.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	bg := color.RGBA{R: 0, G: 0, B: 255, A: 255}
	out := FitImage(src, 10, 10, config.FitStretch, bg)

	if got := out.RGBAAt(1, 5); got.A != 255 {
		t.Errorf("transparent pixel = %v, want it flattened opaque", got)
	}
	if got := out.RGBAAt(1, 5); got.B < 200 {
		t.Errorf("transparent pixel = %v, want the background showing through", got)
	}
	if got := out.RGBAAt(8, 5); got.R < 200 {
		t.Errorf("opaque pixel = %v, want the source kept", got)
	}
}

func TestFitImageNilAndNoop(t *testing.T) {
	if FitImage(nil, 10, 10, config.FitCover, color.White) != nil {
		t.Error("a nil source has nothing to fit")
	}
	src := master(20, 10)
	if got := FitImage(src, 0, 0, config.FitCover, color.White); got != src {
		t.Error("an empty frame should leave the source alone")
	}
	// A nil background is white rather than a panic.
	out := FitImage(master(20, 10), 10, 10, config.FitContain, nil)
	if got := out.RGBAAt(5, 0); got.R != 255 || got.G != 255 || got.B != 255 {
		t.Errorf("default background = %v, want white", got)
	}
}

// --- the video side -----------------------------------------------------------

// TestFitFilterUsesFFmpegsOwnIdioms: the expressions are the ones the
// documentation describes, so what crier builds is what somebody would write.
func TestFitFilterUsesFFmpegsOwnIdioms(t *testing.T) {
	cover := FitFilter(1080, 1920, config.FitCover, "#ffffff")
	if cover != "scale=1080:1920:force_original_aspect_ratio=increase,crop=1080:1920" {
		t.Errorf("cover = %q", cover)
	}

	contain := FitFilter(1080, 1920, config.FitContain, "#112233")
	want := "scale=1080:1920:force_original_aspect_ratio=decrease," +
		"pad=1080:1920:(ow-iw)/2:(oh-ih)/2:0x112233"
	if contain != want {
		t.Errorf("contain = %q, want %q", contain, want)
	}

	if got := FitFilter(640, 480, config.FitStretch, ""); got != "scale=640:480" {
		t.Errorf("stretch = %q", got)
	}

	// Nothing to do.
	if got := FitFilter(1080, 1920, config.FitNone, ""); got != "" {
		t.Errorf("none = %q", got)
	}
	if got := FitFilter(0, 1920, config.FitCover, ""); got != "" {
		t.Errorf("no frame = %q", got)
	}
}

// TestFitFilterColourIsFFmpegSpelling: # starts a comment in a filter graph,
// so the colour goes in as 0xRRGGBB.
func TestFitFilterColourIsFFmpegSpelling(t *testing.T) {
	for hex, want := range map[string]string{
		"#000":     "0x000000",
		"#fff":     "0xffffff",
		"#112233":  "0x112233",
		"":         "0xffffff", // the documented default
		"nonsense": "white",    // unreadable falls back rather than emitting rubbish
	} {
		got := FitFilter(10, 10, config.FitContain, hex)
		if !strings.HasSuffix(got, ":"+want) {
			t.Errorf("background %q produced %q, want it to end in %q", hex, got, want)
		}
		if strings.Contains(got, "#") {
			t.Errorf("background %q left a # in the filter graph: %q", hex, got)
		}
	}
}

// TestFitFilterComposesWithThePalette is the trap: ffmpeg honours only the
// last -vf, and a GIF already spends one on its palette. A second flag would
// have silently thrown the palette away and banded every gradient.
func TestFitFilterComposesWithThePalette(t *testing.T) {
	args := FFmpegArgs(VideoOptions{
		Output: "out.gif", Format: "gif", Width: 1080, Height: 1080,
		FitFilter: FitFilter(1080, 1920, config.FitCover, ""),
	})
	var filters []string
	for i, a := range args {
		if a == "-vf" {
			filters = append(filters, args[i+1])
		}
	}
	if len(filters) != 1 {
		t.Fatalf("%d -vf flags, want exactly one: %v", len(filters), args)
	}
	if !strings.Contains(filters[0], "crop=1080:1920") {
		t.Errorf("the fit is missing: %q", filters[0])
	}
	if !strings.Contains(filters[0], "palettegen") || !strings.Contains(filters[0], "paletteuse") {
		t.Errorf("the palette is missing: %q", filters[0])
	}
	// The fit runs first: scaling after the palette was measured would measure
	// the wrong pixels.
	if strings.Index(filters[0], "crop=") > strings.Index(filters[0], "palettegen") {
		t.Errorf("the fit should come before the palette: %q", filters[0])
	}
}

// TestFitFilterComposesWithTheEvenSizeScale is the same trap on the mp4 side.
func TestFitFilterComposesWithTheEvenSizeScale(t *testing.T) {
	args := FFmpegArgs(VideoOptions{
		Output: "out.mp4", Width: 101, Height: 101,
		FitFilter: FitFilter(640, 480, config.FitStretch, ""),
	})
	var filters []string
	for i, a := range args {
		if a == "-vf" {
			filters = append(filters, args[i+1])
		}
	}
	if len(filters) != 1 {
		t.Fatalf("%d -vf flags, want one: %v", len(filters), args)
	}
	if !strings.Contains(filters[0], "scale=640:480") {
		t.Errorf("the fit is missing: %q", filters[0])
	}

	// With no fit and even dimensions there is no filter at all.
	plain := FFmpegArgs(VideoOptions{Output: "out.mp4", Width: 640, Height: 480})
	for _, a := range plain {
		if a == "-vf" {
			t.Errorf("an unfitted even-sized clip should need no filter: %v", plain)
		}
	}
}

func TestChainFilters(t *testing.T) {
	if got := chainFilters("a", "b"); got != "a,b" {
		t.Errorf("= %q", got)
	}
	if got := chainFilters("", "b"); got != "b" {
		t.Errorf("= %q", got)
	}
	if got := chainFilters("a", "  "); got != "a" {
		t.Errorf("= %q", got)
	}
	if got := chainFilters("", ""); got != "" {
		t.Errorf("= %q", got)
	}
}
