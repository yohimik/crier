package raster

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"

	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/css/parser"
	"github.com/benoitkugler/webrender/matrix"
	"github.com/rs/zerolog"
	"github.com/tdewolff/canvas"
)

func newTestCanvas(w, h int) (*Canvas, *image.RGBA) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	return NewCanvas(img, zerolog.Nop()), img
}

func rgba(r, g, b, a float32) parser.RGBA { return parser.RGBA{R: r, G: g, B: b, A: a} }

var (
	red   = color.RGBA{R: 255, A: 255}
	green = color.RGBA{G: 255, A: 255}
	blue  = color.RGBA{B: 255, A: 255}
)

// --- state stack -----------------------------------------------------------

func TestStateStackSavesAndRestores(t *testing.T) {
	c, _ := newTestCanvas(4, 4)
	c.SetColorRgba(rgba(1, 0, 0, 1), false)
	c.SetLineWidth(3)
	c.Transform(matrix.Translation(10, 10))

	before := c.st
	c.OnNewStack(func() {
		c.SetColorRgba(rgba(0, 1, 0, 1), false)
		c.SetLineWidth(9)
		c.Transform(matrix.Scaling(2, 2))
		if c.st.lineWidth != 9 {
			t.Error("the inner state should have taken effect")
		}
	})
	if c.st.lineWidth != before.lineWidth || c.st.fill != before.fill || c.st.ctm != before.ctm {
		t.Errorf("state not restored: %+v vs %+v", c.st, before)
	}
}

func TestNestedStacks(t *testing.T) {
	c, _ := newTestCanvas(4, 4)
	c.SetLineWidth(1)
	c.OnNewStack(func() {
		c.SetLineWidth(2)
		c.OnNewStack(func() {
			c.SetLineWidth(3)
		})
		if c.st.lineWidth != 2 {
			t.Errorf("inner restore = %v", c.st.lineWidth)
		}
	})
	if c.st.lineWidth != 1 {
		t.Errorf("outer restore = %v", c.st.lineWidth)
	}
}

// --- transforms ------------------------------------------------------------

func TestTransformOrderMatchesTheInterface(t *testing.T) {
	c, _ := newTestCanvas(4, 4)
	// Transform applies mt before the existing transformation: a point is
	// scaled first, then translated.
	c.Transform(matrix.Translation(10, 20))
	c.Transform(matrix.Scaling(2, 3))

	x, y := c.st.ctm.Apply(1, 1)
	if x != 12 || y != 23 {
		t.Fatalf("point = (%v,%v), want (12,23)", x, y)
	}
	if got := c.GetTransform(); got != c.st.ctm {
		t.Error("GetTransform should return the CTM")
	}
}

func TestPathPointsGoThroughTheTransform(t *testing.T) {
	c, _ := newTestCanvas(100, 100)
	c.Transform(matrix.Translation(10, 5))
	c.MoveTo(0, 0)
	c.LineTo(10, 0)
	b := c.path.Bounds()
	if b.X0 != 10 || b.Y0 != 5 || b.X1 != 20 {
		t.Errorf("bounds = %+v, want the translated points", b)
	}
}

func TestRectangleUnderRotationIsNotAxisAligned(t *testing.T) {
	c, _ := newTestCanvas(100, 100)
	c.Transform(matrix.Rotation(math.Pi / 4))
	c.Rectangle(0, 0, 10, 10)
	if _, ok := deviceRect(c.path); ok {
		t.Error("a rotated rectangle must not take the axis-aligned fast path")
	}
}

// --- fills -----------------------------------------------------------------

func TestFillRectangle(t *testing.T) {
	c, img := newTestCanvas(20, 20)
	c.SetColorRgba(rgba(1, 0, 0, 1), false)
	c.Rectangle(5, 5, 10, 10)
	c.Paint(backend.FillNonZero)

	if got := img.RGBAAt(10, 10); got != red {
		t.Errorf("inside = %v", got)
	}
	if got := img.RGBAAt(2, 2); got != (color.RGBA{}) {
		t.Errorf("outside = %v", got)
	}
	if c.path != nil {
		t.Error("Paint must clear the path")
	}
}

func TestFillHonoursAlpha(t *testing.T) {
	c, img := newTestCanvas(4, 4)
	c.SetColorRgba(rgba(1, 0, 0, 0.5), false)
	c.Rectangle(0, 0, 4, 4)
	c.Paint(backend.FillNonZero)
	got := img.RGBAAt(2, 2)
	if got.A < 120 || got.A > 136 {
		t.Errorf("alpha = %d, want about half", got.A)
	}
	if got.R != got.A {
		t.Errorf("pixel %v is not premultiplied", got)
	}
}

func TestSetAlphaOnlyChangesAlpha(t *testing.T) {
	c, img := newTestCanvas(4, 4)
	c.SetColorRgba(rgba(0, 0, 1, 1), false)
	c.SetAlpha(0.25, false)
	c.Rectangle(0, 0, 4, 4)
	c.Paint(backend.FillNonZero)
	got := img.RGBAAt(2, 2)
	if got.A < 56 || got.A > 72 {
		t.Errorf("alpha = %d, want about a quarter", got.A)
	}
	if got.B != got.A {
		t.Errorf("pixel %v is not premultiplied blue", got)
	}
}

// TestEvenOddVersusNonZero draws a five pointed star, whose centre is filled
// under the non-zero rule and hollow under even-odd.
func TestEvenOddVersusNonZero(t *testing.T) {
	star := func(c *Canvas) {
		const cx, cy, r = 50.0, 50.0, 40.0
		for i := 0; i < 5; i++ {
			angle := -math.Pi/2 + float64(i)*4*math.Pi/5
			x := cx + r*math.Cos(angle)
			y := cy + r*math.Sin(angle)
			if i == 0 {
				c.MoveTo(backend.Fl(x), backend.Fl(y))
			} else {
				c.LineTo(backend.Fl(x), backend.Fl(y))
			}
		}
		c.ClosePath()
	}

	cNZ, imgNZ := newTestCanvas(100, 100)
	cNZ.SetColorRgba(rgba(0, 0, 0, 1), false)
	star(cNZ)
	cNZ.Paint(backend.FillNonZero)

	cEO, imgEO := newTestCanvas(100, 100)
	cEO.SetColorRgba(rgba(0, 0, 0, 1), false)
	star(cEO)
	cEO.Paint(backend.FillEvenOdd)

	if imgNZ.RGBAAt(50, 50).A == 0 {
		t.Error("non-zero should fill the middle of a star")
	}
	if imgEO.RGBAAt(50, 50).A != 0 {
		t.Error("even-odd should leave the middle of a star hollow")
	}
	// The points of the star are filled either way.
	if imgNZ.RGBAAt(50, 20).A == 0 || imgEO.RGBAAt(50, 20).A == 0 {
		t.Error("a point of the star should be filled under both rules")
	}
}

func TestPaintWithNoOperationJustClearsThePath(t *testing.T) {
	c, img := newTestCanvas(10, 10)
	c.SetColorRgba(rgba(1, 0, 0, 1), false)
	c.Rectangle(0, 0, 10, 10)
	c.Paint(0)
	if img.RGBAAt(5, 5).A != 0 {
		t.Error("nothing should have been drawn")
	}
	if c.path != nil {
		t.Error("the path should be cleared")
	}
}

func TestPaintWithNoPathIsHarmless(t *testing.T) {
	c, _ := newTestCanvas(10, 10)
	c.Paint(backend.FillNonZero)
	c.ClosePath()
	c.Clip(false)
}

// --- strokes ---------------------------------------------------------------

func TestStrokeDrawsOnTheOutline(t *testing.T) {
	c, img := newTestCanvas(40, 40)
	c.SetColorRgba(rgba(0, 0, 1, 1), true)
	c.SetLineWidth(4)
	c.Rectangle(10, 10, 20, 20)
	c.Paint(backend.Stroke)

	if img.RGBAAt(10, 20).A == 0 {
		t.Error("the stroke should cover the edge")
	}
	if img.RGBAAt(20, 20).A != 0 {
		t.Error("a stroke must not fill the inside")
	}
}

func TestStrokeWidthScalesWithTheTransform(t *testing.T) {
	c, _ := newTestCanvas(40, 40)
	c.SetLineWidth(2)
	if got := c.strokeWidth(); got != 2 {
		t.Errorf("width = %v", got)
	}
	c.Transform(matrix.Scaling(3, 3))
	if got := c.strokeWidth(); math.Abs(got-6) > 1e-6 {
		t.Errorf("scaled width = %v, want 6", got)
	}
}

func TestHairlineStrokeStaysVisible(t *testing.T) {
	c, img := newTestCanvas(20, 20)
	c.SetColorRgba(rgba(0, 0, 0, 1), true)
	c.SetLineWidth(0.05)
	c.MoveTo(2, 10)
	c.LineTo(18, 10)
	c.Paint(backend.Stroke)
	if img.RGBAAt(10, 10).A == 0 {
		t.Error("a sub-pixel stroke should still be drawn")
	}
}

func TestZeroWidthStrokeDrawsNothing(t *testing.T) {
	c, img := newTestCanvas(20, 20)
	c.SetColorRgba(rgba(0, 0, 0, 1), true)
	c.SetLineWidth(0)
	c.MoveTo(2, 10)
	c.LineTo(18, 10)
	c.Paint(backend.Stroke)
	if img.RGBAAt(10, 10).A != 0 {
		t.Error("a zero width stroke draws nothing")
	}
}

func TestDashesLeaveGaps(t *testing.T) {
	c, img := newTestCanvas(60, 10)
	c.SetColorRgba(rgba(0, 0, 0, 1), true)
	c.SetLineWidth(4)
	c.SetDash([]backend.Fl{6, 6}, 0)
	c.MoveTo(0, 5)
	c.LineTo(60, 5)
	c.Paint(backend.Stroke)

	on, off := 0, 0
	for x := 0; x < 60; x++ {
		if img.RGBAAt(x, 5).A > 128 {
			on++
		} else {
			off++
		}
	}
	if on == 0 || off == 0 {
		t.Fatalf("dashes produced %d on and %d off pixels", on, off)
	}
}

func TestSetDashRejectsDegeneratePatterns(t *testing.T) {
	c, _ := newTestCanvas(10, 10)
	c.SetDash([]backend.Fl{0, 0}, 1)
	if c.st.dashes != nil {
		t.Error("an all-zero pattern should disable dashing rather than loop")
	}
	c.SetDash([]backend.Fl{5, 5}, 2)
	if len(c.st.dashes) != 2 || c.st.dashOffset != 2 {
		t.Errorf("dashes = %v offset = %v", c.st.dashes, c.st.dashOffset)
	}
	c.SetDash(nil, 0)
	if c.st.dashes != nil {
		t.Error("an empty pattern disables dashing")
	}
	c.SetDash([]backend.Fl{-1, 4}, 0)
	if c.st.dashes[0] != 0 {
		t.Errorf("a negative dash should be clamped, got %v", c.st.dashes)
	}
}

func TestStrokeOptionsAreRecorded(t *testing.T) {
	c, _ := newTestCanvas(10, 10)
	opts := backend.StrokeOptions{LineCap: backend.RoundCap, LineJoin: backend.Bevel, MiterLimit: 8}
	c.SetStrokeOptions(opts)
	if c.st.strokeOpts != opts {
		t.Errorf("options = %+v", c.st.strokeOpts)
	}
	if capper(backend.RoundCap) != canvas.RoundCap || capper(backend.SquareCap) != canvas.SquareCap ||
		capper(backend.ButtCap) != canvas.ButtCap {
		t.Error("cap mapping")
	}
	if joiner(backend.StrokeOptions{LineJoin: backend.Round}) != canvas.RoundJoin {
		t.Error("round join mapping")
	}
	if joiner(backend.StrokeOptions{LineJoin: backend.Bevel}) != canvas.BevelJoin {
		t.Error("bevel join mapping")
	}
	if j, ok := joiner(backend.StrokeOptions{LineJoin: backend.Miter}).(canvas.MiterJoiner); !ok || j.Limit != 4 {
		t.Errorf("miter join mapping = %#v", j)
	}
}

// --- clipping --------------------------------------------------------------

func TestClipOnlyEverNarrows(t *testing.T) {
	c, img := newTestCanvas(40, 40)
	c.Rectangle(0, 0, 20, 40)
	c.Clip(false)
	c.Rectangle(10, 0, 30, 40)
	c.Clip(false)

	c.SetColorRgba(rgba(1, 0, 0, 1), false)
	c.Rectangle(0, 0, 40, 40)
	c.Paint(backend.FillNonZero)

	if img.RGBAAt(15, 20) != red {
		t.Error("the overlap should be painted")
	}
	for _, p := range []image.Point{{X: 5, Y: 20}, {X: 25, Y: 20}} {
		if img.RGBAAt(p.X, p.Y).A != 0 {
			t.Errorf("pixel %v is outside both clips and should be untouched", p)
		}
	}
}

func TestClipIsRestoredByTheStack(t *testing.T) {
	c, img := newTestCanvas(20, 20)
	c.OnNewStack(func() {
		c.Rectangle(0, 0, 5, 5)
		c.Clip(false)
	})
	c.SetColorRgba(rgba(0, 1, 0, 1), false)
	c.Rectangle(0, 0, 20, 20)
	c.Paint(backend.FillNonZero)
	if img.RGBAAt(15, 15) != green {
		t.Error("the clip should have been restored")
	}
}

func TestAxisAlignedClipTakesTheRectanglePath(t *testing.T) {
	c, _ := newTestCanvas(40, 40)
	c.Rectangle(5, 5, 10, 10)
	c.Clip(false)
	if !c.st.clip.isRect() {
		t.Error("a whole-pixel rectangle clip should need no mask")
	}
	if got := c.st.clip.bounds(); got != image.Rect(5, 5, 15, 15) {
		t.Errorf("clip rect = %v", got)
	}
}

func TestNonRectangularClipUsesAMask(t *testing.T) {
	c, img := newTestCanvas(40, 40)
	c.MoveTo(20, 0)
	c.LineTo(40, 40)
	c.LineTo(0, 40)
	c.ClosePath()
	c.Clip(false)
	if c.st.clip.isRect() {
		t.Fatal("a triangle clip needs a mask")
	}
	c.SetColorRgba(rgba(0, 0, 1, 1), false)
	c.Rectangle(0, 0, 40, 40)
	c.Paint(backend.FillNonZero)

	if img.RGBAAt(20, 35) != blue {
		t.Error("inside the triangle should be painted")
	}
	if img.RGBAAt(2, 2).A != 0 {
		t.Error("outside the triangle should be untouched")
	}
}

func TestClipMaskCombinesCoverage(t *testing.T) {
	base := &clip{rect: image.Rect(0, 0, 10, 10)}
	m := image.NewAlpha(image.Rect(0, 0, 10, 10))
	m.SetAlpha(5, 5, color.Alpha{A: 128})
	narrowed := base.intersectMask(m)
	if got := narrowed.alphaAt(5, 5); got != 128 {
		t.Errorf("alpha = %d", got)
	}
	if got := narrowed.alphaAt(1, 1); got != 0 {
		t.Errorf("alpha outside the mask = %d", got)
	}

	again := narrowed.intersectMask(m)
	if got := again.alphaAt(5, 5); got != 64 {
		t.Errorf("intersecting twice should multiply, got %d", got)
	}
	if empty := base.intersectMask(nil); !empty.bounds().Empty() {
		t.Error("a nil mask clips everything away")
	}
}

func TestNilClipIsUnclipped(t *testing.T) {
	var c *clip
	if c.alphaAt(3, 3) != 255 {
		t.Error("a nil clip should be fully open")
	}
	if !c.isRect() {
		t.Error("a nil clip has no mask")
	}
	if !c.bounds().Empty() {
		t.Error("a nil clip has no bounds")
	}
	if got := c.intersectRect(image.Rect(0, 0, 2, 2)); got.rect != image.Rect(0, 0, 2, 2) {
		t.Errorf("rect = %v", got.rect)
	}
}

// --- geometry helpers ------------------------------------------------------

func TestDeviceRectRecognition(t *testing.T) {
	rect := &canvas.Path{}
	rect.MoveTo(1, 2)
	rect.LineTo(11, 2)
	rect.LineTo(11, 12)
	rect.LineTo(1, 12)
	rect.Close()
	got, ok := deviceRect(rect)
	if !ok || got != image.Rect(1, 2, 11, 12) {
		t.Errorf("deviceRect = %v, %v", got, ok)
	}

	frac := &canvas.Path{}
	frac.MoveTo(1.5, 2)
	frac.LineTo(11, 2)
	frac.LineTo(11, 12)
	frac.LineTo(1.5, 12)
	frac.Close()
	if _, ok := deviceRect(frac); ok {
		t.Error("a rectangle off the pixel grid needs a mask")
	}

	tri := &canvas.Path{}
	tri.MoveTo(0, 0)
	tri.LineTo(10, 0)
	tri.LineTo(5, 10)
	tri.Close()
	if _, ok := deviceRect(tri); ok {
		t.Error("a triangle is not a rectangle")
	}

	curve := &canvas.Path{}
	curve.MoveTo(0, 0)
	curve.CubeTo(1, 1, 2, 2, 3, 3)
	if _, ok := deviceRect(curve); ok {
		t.Error("a curve is not a rectangle")
	}

	two := &canvas.Path{}
	two.MoveTo(0, 0)
	two.LineTo(1, 0)
	two.MoveTo(5, 5)
	two.LineTo(6, 5)
	if _, ok := deviceRect(two); ok {
		t.Error("two subpaths are not a rectangle")
	}

	if _, ok := deviceRect(nil); ok {
		t.Error("nil is not a rectangle")
	}
}

func TestRasterizeSkipsEmptyPaths(t *testing.T) {
	if got := rasterize(nil, image.Rect(0, 0, 10, 10), false); got != nil {
		t.Error("nil path")
	}
	if got := rasterize(&canvas.Path{}, image.Rect(0, 0, 10, 10), false); got != nil {
		t.Error("empty path")
	}
	p := &canvas.Path{}
	p.MoveTo(100, 100)
	p.LineTo(110, 110)
	p.Close()
	if got := rasterize(p, image.Rect(0, 0, 10, 10), false); got != nil {
		t.Error("a path outside the clip covers nothing")
	}
}

func TestNaNAndInfinityDoNotPanic(t *testing.T) {
	c, _ := newTestCanvas(10, 10)
	nan := backend.Fl(math.NaN())
	inf := backend.Fl(math.Inf(1))
	c.MoveTo(nan, nan)
	c.LineTo(inf, 1)
	c.CubicTo(nan, inf, 1, 2, 3, nan)
	c.Rectangle(nan, 1, inf, 2)
	c.ClosePath()
	c.Paint(backend.FillNonZero | backend.Stroke)

	c.MoveTo(nan, 0)
	c.LineTo(1, inf)
	c.Clip(true)
}

func TestFiniteAndNearInt(t *testing.T) {
	if finite(math.NaN()) || finite(math.Inf(-1)) || !finite(1.5) {
		t.Error("finite")
	}
	if !nearInt(3.00001) || nearInt(3.4) || nearInt(math.NaN()) {
		t.Error("nearInt")
	}
}

func TestStrokeOutlineGuards(t *testing.T) {
	if got := strokeOutline(nil, 1, nil, 0, backend.StrokeOptions{}); got != nil {
		t.Error("nil path")
	}
	p := &canvas.Path{}
	p.MoveTo(0, 0)
	p.LineTo(10, 0)
	if got := strokeOutline(p, 0, nil, 0, backend.StrokeOptions{}); got != nil {
		t.Error("zero width")
	}
	if got := strokeOutline(p, 2, []float64{0}, 0, backend.StrokeOptions{}); got != nil {
		t.Error("a dash pattern that removes everything leaves no outline")
	}
}

// --- compositing -----------------------------------------------------------

func TestComposeOverBuildsUp(t *testing.T) {
	c, img := newTestCanvas(4, 4)
	c.SetColorRgba(rgba(1, 0, 0, 1), false)
	c.Rectangle(0, 0, 4, 4)
	c.Paint(backend.FillNonZero)
	c.SetColorRgba(rgba(0, 0, 1, 0.5), false)
	c.Rectangle(0, 0, 4, 4)
	c.Paint(backend.FillNonZero)

	got := img.RGBAAt(2, 2)
	if got.A != 255 {
		t.Errorf("alpha = %d, want opaque", got.A)
	}
	if got.R < 100 || got.R > 140 || got.B < 115 || got.B > 140 {
		t.Errorf("pixel = %v, want half red and half blue", got)
	}
}

func TestBlendModes(t *testing.T) {
	for _, mode := range []string{
		"multiply", "screen", "overlay", "darken", "lighten",
		"color-dodge", "color-burn", "hard-light", "soft-light",
		"difference", "exclusion",
	} {
		if separableBlend(mode) == nil {
			t.Errorf("blend mode %q should be supported", mode)
		}
	}
	for _, mode := range []string{"", "normal", "source-over", "nonsense"} {
		if separableBlend(mode) != nil {
			t.Errorf("mode %q should be plain source-over", mode)
		}
	}
	for _, mode := range []string{"hue", "saturation", "color", "luminosity"} {
		if !isNonSeparableBlend(mode) {
			t.Errorf("mode %q is non-separable", mode)
		}
	}
}

func TestMultiplyBlendDarkens(t *testing.T) {
	c, img := newTestCanvas(4, 4)
	c.SetColorRgba(rgba(1, 1, 1, 1), false) // white backdrop
	c.Rectangle(0, 0, 4, 4)
	c.Paint(backend.FillNonZero)

	c.SetBlendingMode("multiply")
	c.SetColorRgba(rgba(0.5, 0.5, 0.5, 1), false)
	c.Rectangle(0, 0, 4, 4)
	c.Paint(backend.FillNonZero)

	got := img.RGBAAt(2, 2)
	if got.R > 140 || got.R < 115 {
		t.Errorf("white multiplied by grey = %v, want grey", got)
	}
}

func TestNonSeparableBlendFallsBackToNormal(t *testing.T) {
	c, _ := newTestCanvas(4, 4)
	c.SetBlendingMode("luminosity")
	if c.st.blend != "" {
		t.Errorf("blend = %q, want the unsupported mode dropped", c.st.blend)
	}
	c.SetBlendingMode("multiply")
	if c.st.blend != "multiply" {
		t.Errorf("blend = %q", c.st.blend)
	}
}

func TestBlendFunctionsAtTheEdges(t *testing.T) {
	if colorDodge(0, 0.5) != 0 {
		t.Error("dodge of a black backdrop stays black")
	}
	if colorDodge(0.5, 1) != 1 {
		t.Error("dodge with a white source is white")
	}
	if colorBurn(1, 0.5) != 1 {
		t.Error("burn of a white backdrop stays white")
	}
	if colorBurn(0.5, 0) != 0 {
		t.Error("burn with a black source is black")
	}
	if got := softLight(0.1, 0.9); got <= 0.1 {
		t.Errorf("soft light with a bright source should lighten, got %v", got)
	}
	if got := softLight(0.5, 0.1); got >= 0.5 {
		t.Errorf("soft light with a dark source should darken, got %v", got)
	}
	if hardLight(0.5, 0.25) != 0.25 {
		t.Errorf("hard light = %v", hardLight(0.5, 0.25))
	}
	if min32(1, 2) != 1 || max32(1, 2) != 2 || abs32(-3) != 3 || abs32(3) != 3 {
		t.Error("helpers")
	}
}

func TestClamp8(t *testing.T) {
	for _, tt := range []struct {
		in   float32
		want uint8
	}{{-1, 0}, {0, 0}, {0.5, 128}, {1, 255}, {2, 255}} {
		if got := clamp8(tt.in); got != tt.want {
			t.Errorf("clamp8(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestComposeIsANoOpWithoutAlpha(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	compose(img, img.Bounds(), nil, solidShader{r: 255, a: 255}, 0, nil, "")
	if img.RGBAAt(1, 1).A != 0 {
		t.Error("zero alpha draws nothing")
	}
	compose(img, image.Rect(100, 100, 110, 110), nil, solidShader{r: 255, a: 255}, 1, nil, "")
	if img.RGBAAt(1, 1).A != 0 {
		t.Error("an area outside the image draws nothing")
	}
}

// --- groups and opacity ----------------------------------------------------

func TestDrawWithOpacity(t *testing.T) {
	c, img := newTestCanvas(20, 20)
	group := c.NewGroup(0, 0, 20, 20).(*Canvas)
	group.SetColorRgba(rgba(1, 0, 0, 1), false)
	group.Rectangle(0, 0, 20, 20)
	group.Paint(backend.FillNonZero)

	if img.RGBAAt(10, 10).A != 0 {
		t.Fatal("a group must not draw onto the page until it is composited")
	}
	c.DrawWithOpacity(0.5, group)
	got := img.RGBAAt(10, 10)
	if got.A < 120 || got.A > 136 {
		t.Errorf("alpha = %d, want about half", got.A)
	}
}

func TestGroupInheritsTheTransformButNotTheClip(t *testing.T) {
	c, _ := newTestCanvas(20, 20)
	c.Transform(matrix.Translation(3, 4))
	c.Rectangle(0, 0, 2, 2)
	c.Clip(false)

	group := c.NewGroup(0, 0, 20, 20).(*Canvas)
	if group.st.ctm != c.st.ctm {
		t.Error("a group inherits the transform")
	}
	if group.st.clip.bounds() != group.dst.Bounds() {
		t.Error("a group starts unclipped; the clip applies when it is drawn back")
	}
	if group.sh != c.sh {
		t.Error("a group shares the font and image caches")
	}
}

func TestGroupIsClippedWhenItIsDrawnBack(t *testing.T) {
	c, img := newTestCanvas(20, 20)
	group := c.NewGroup(0, 0, 20, 20).(*Canvas)
	group.SetColorRgba(rgba(0, 1, 0, 1), false)
	group.Rectangle(0, 0, 20, 20)
	group.Paint(backend.FillNonZero)

	c.Rectangle(0, 0, 10, 20)
	c.Clip(false)
	c.DrawWithOpacity(1, group)

	if img.RGBAAt(5, 5) != green {
		t.Error("inside the clip should be drawn")
	}
	if img.RGBAAt(15, 5).A != 0 {
		t.Error("outside the clip should not be")
	}
}

func TestGroupBoundingBox(t *testing.T) {
	c, _ := newTestCanvas(20, 20)
	g := c.NewGroup(1, 2, 3, 4)
	l, tp, r, b := g.GetBoundingBox()
	if l != 1 || tp != 2 || r != 4 || b != 6 {
		t.Errorf("bbox = %v %v %v %v", l, tp, r, b)
	}
	g.SetBoundingBox(0, 0, 9, 9)
	if l, tp, r, b = g.GetBoundingBox(); l != 0 || tp != 0 || r != 9 || b != 9 {
		t.Errorf("bbox = %v %v %v %v", l, tp, r, b)
	}
}

func TestAlphaMaskMultipliesIntoTheClip(t *testing.T) {
	c, img := newTestCanvas(20, 20)
	mask := c.NewGroup(0, 0, 20, 20).(*Canvas)
	mask.SetColorRgba(rgba(1, 1, 1, 1), false) // white: fully visible
	mask.Rectangle(0, 0, 10, 20)
	mask.Paint(backend.FillNonZero)

	c.SetAlphaMask(mask)
	c.SetColorRgba(rgba(1, 0, 0, 1), false)
	c.Rectangle(0, 0, 20, 20)
	c.Paint(backend.FillNonZero)

	if img.RGBAAt(5, 5) != red {
		t.Error("the lit half of the mask should show through")
	}
	if img.RGBAAt(15, 5).A != 0 {
		t.Error("the dark half of the mask should hide the paint")
	}
}

func TestLuminosityMask(t *testing.T) {
	layer := image.NewRGBA(image.Rect(0, 0, 2, 1))
	layer.SetRGBA(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	layer.SetRGBA(1, 0, color.RGBA{A: 255})
	m := luminosityMask(layer, layer.Bounds())
	if m.AlphaAt(0, 0).A < 250 {
		t.Errorf("white = %d, want fully lit", m.AlphaAt(0, 0).A)
	}
	if m.AlphaAt(1, 0).A != 0 {
		t.Errorf("black = %d, want dark", m.AlphaAt(1, 0).A)
	}
	if luminosityMask(layer, image.Rect(50, 50, 60, 60)) != nil {
		t.Error("an area outside the layer has no mask")
	}
}

func TestForeignCanvasesAreIgnored(t *testing.T) {
	c, _ := newTestCanvas(10, 10)
	c.SetAlphaMask(foreignCanvas{})
	c.DrawWithOpacity(1, foreignCanvas{})
	c.SetColorPattern(foreignCanvas{}, 1, 1, matrix.Identity(), false)
}

// --- patterns --------------------------------------------------------------

func TestColorPatternTiles(t *testing.T) {
	c, img := newTestCanvas(40, 10)
	pattern := c.NewGroup(0, 0, 10, 10).(*Canvas)
	pattern.SetColorRgba(rgba(0, 0, 1, 1), false)
	pattern.Rectangle(0, 0, 5, 10)
	pattern.Paint(backend.FillNonZero)

	c.SetColorPattern(pattern, 10, 10, matrix.Identity(), false)
	c.Rectangle(0, 0, 40, 10)
	c.Paint(backend.FillNonZero)

	for _, x := range []int{2, 12, 22, 32} {
		if img.RGBAAt(x, 5) != blue {
			t.Errorf("x=%d should be inside a tile, got %v", x, img.RGBAAt(x, 5))
		}
	}
	for _, x := range []int{7, 17, 27, 37} {
		if img.RGBAAt(x, 5).A != 0 {
			t.Errorf("x=%d should be in a gap, got %v", x, img.RGBAAt(x, 5))
		}
	}
}

func TestSingularPatternTransformIsIgnored(t *testing.T) {
	c, _ := newTestCanvas(10, 10)
	pattern := c.NewGroup(0, 0, 5, 5)
	c.SetColorPattern(pattern, 5, 5, matrix.Scaling(0, 0), false)
	if c.st.fill.pattern != nil {
		t.Error("a pattern that cannot be inverted should be dropped")
	}
}

func TestStrokePattern(t *testing.T) {
	c, _ := newTestCanvas(10, 10)
	pattern := c.NewGroup(0, 0, 5, 5)
	c.SetColorPattern(pattern, 5, 5, matrix.Identity(), true)
	if c.st.stroke.pattern == nil {
		t.Error("the stroke paint should hold the pattern")
	}
	if c.st.fill.pattern != nil {
		t.Error("the fill paint should be untouched")
	}
}

// --- images ----------------------------------------------------------------

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDrawRasterImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			src.SetRGBA(x, y, green)
		}
	}
	data := pngBytes(t, src)

	c, img := newTestCanvas(20, 20)
	c.Transform(matrix.Translation(5, 5))
	c.DrawRasterImage(backend.RasterImage{
		Content: bytes.NewReader(data), MimeType: "image/png", ID: 1,
	}, 10, 10)

	if got := img.RGBAAt(10, 10); got.G < 200 {
		t.Errorf("inside the image = %v, want green", got)
	}
	if got := img.RGBAAt(2, 2); got.A != 0 {
		t.Errorf("outside the image = %v", got)
	}
}

func TestDrawRasterImageIsCachedByID(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	src.SetRGBA(0, 0, red)
	data := pngBytes(t, src)

	c, _ := newTestCanvas(10, 10)
	img := backend.RasterImage{Content: bytes.NewReader(data), MimeType: "image/png", ID: 7}
	c.DrawRasterImage(img, 4, 4)
	// The reader is exhausted; a second draw has to come from the cache.
	c.DrawRasterImage(backend.RasterImage{Content: bytes.NewReader(nil), MimeType: "image/png", ID: 7}, 4, 4)
	if len(c.sh.images) != 1 {
		t.Errorf("cache holds %d entries", len(c.sh.images))
	}
}

func TestDrawRasterImageWithOpacityGoesThroughALayer(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	src.SetRGBA(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	data := pngBytes(t, src)

	c, img := newTestCanvas(10, 10)
	c.SetAlpha(0.5, false)
	c.DrawRasterImage(backend.RasterImage{Content: bytes.NewReader(data), MimeType: "image/png", ID: 3}, 10, 10)
	got := img.RGBAAt(5, 5)
	if got.A < 110 || got.A > 145 {
		t.Errorf("alpha = %d, want about half", got.A)
	}
}

func TestDrawRasterImageGuards(t *testing.T) {
	c, img := newTestCanvas(10, 10)
	c.DrawRasterImage(backend.RasterImage{Content: bytes.NewReader(nil), ID: 1}, 0, 10)
	c.DrawRasterImage(backend.RasterImage{Content: bytes.NewReader([]byte("not an image")), ID: 2}, 5, 5)
	if img.RGBAAt(1, 1).A != 0 {
		t.Error("a broken image should draw nothing")
	}
}

func TestNormaliseMIME(t *testing.T) {
	if got := normaliseMIME(" Image/PNG; charset=x "); got != "image/png" {
		t.Errorf("got %q", got)
	}
}

func TestUnpremul(t *testing.T) {
	if got := unpremul(128, 128); got != 255 {
		t.Errorf("got %d", got)
	}
	if got := unpremul(255, 128); got != 255 {
		t.Errorf("clamping: got %d", got)
	}
}

func TestLayerShaderOutsideBounds(t *testing.T) {
	l := &layerShader{layer: image.NewRGBA(image.Rect(0, 0, 2, 2))}
	if _, _, _, a := l.colorAt(9, 9); a != 0 {
		t.Error("outside the layer is transparent")
	}
	l.layer.SetRGBA(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	if r, _, _, a := l.colorAt(0, 0); r != 255 || a != 255 {
		t.Error("an opaque pixel comes back as itself")
	}
}

// --- gradients -------------------------------------------------------------

func linearLayout() backend.GradientLayout {
	return backend.GradientLayout{
		GradientKind: backend.GradientKind{Kind: "linear", Coords: [6]backend.Fl{0, 0, 100, 0}},
		Positions:    []backend.Fl{0, 1},
		Colors:       []parser.RGBA{rgba(1, 0, 0, 1), rgba(0, 0, 1, 1)},
		ScaleY:       1,
	}
}

func TestLinearGradientRunsAcross(t *testing.T) {
	c, img := newTestCanvas(100, 10)
	c.DrawGradient(linearLayout(), 100, 10)

	left := img.RGBAAt(2, 5)
	right := img.RGBAAt(97, 5)
	if left.R < 200 || left.B > 60 {
		t.Errorf("left = %v, want red", left)
	}
	if right.B < 200 || right.R > 60 {
		t.Errorf("right = %v, want blue", right)
	}
	mid := img.RGBAAt(50, 5)
	if mid.R < 80 || mid.R > 175 || mid.B < 80 || mid.B > 175 {
		t.Errorf("middle = %v, want a mix", mid)
	}
}

func TestRadialGradient(t *testing.T) {
	c, img := newTestCanvas(100, 100)
	c.DrawGradient(backend.GradientLayout{
		GradientKind: backend.GradientKind{Kind: "radial", Coords: [6]backend.Fl{50, 50, 0, 50, 50, 50}},
		Positions:    []backend.Fl{0, 1},
		Colors:       []parser.RGBA{rgba(1, 1, 1, 1), rgba(0, 0, 0, 1)},
		ScaleY:       1,
	}, 100, 100)

	if img.RGBAAt(50, 50).R < 200 {
		t.Errorf("centre = %v, want white", img.RGBAAt(50, 50))
	}
	if img.RGBAAt(50, 4).R > 80 {
		t.Errorf("edge = %v, want dark", img.RGBAAt(50, 4))
	}
}

func TestGradientShaderMath(t *testing.T) {
	inv := matrix.Identity()
	s := newGradientShader(linearLayout(), inv)
	if s == nil {
		t.Fatal("no shader")
	}
	if got, ok := s.param(0, 0); !ok || got != 0 {
		t.Errorf("start = %v %v", got, ok)
	}
	if got, ok := s.param(100, 0); !ok || got != 1 {
		t.Errorf("end = %v %v", got, ok)
	}
	if got, ok := s.param(50, 0); !ok || got != 0.5 {
		t.Errorf("middle = %v %v", got, ok)
	}
}

func TestGradientDegenerateCases(t *testing.T) {
	inv := matrix.Identity()
	// A linear gradient with no length has no direction.
	zero := linearLayout()
	zero.Coords = [6]backend.Fl{5, 5, 5, 5}
	if newGradientShader(zero, inv) != nil {
		t.Error("a zero-length linear gradient should be refused")
	}
	// An unknown kind is refused.
	odd := linearLayout()
	odd.Kind = "conic"
	if newGradientShader(odd, inv) != nil {
		t.Error("an unknown gradient kind should be refused")
	}
	// Two concentric circles of the same radius cover nothing.
	flat := backend.GradientLayout{
		GradientKind: backend.GradientKind{Kind: "radial", Coords: [6]backend.Fl{0, 0, 10, 0, 0, 10}},
		Positions:    []backend.Fl{0, 1},
		Colors:       []parser.RGBA{rgba(1, 0, 0, 1), rgba(0, 1, 0, 1)},
		ScaleY:       1,
	}
	s := newGradientShader(flat, inv)
	if s == nil {
		t.Fatal("no shader")
	}
	if _, ok := s.param(0, 0); ok {
		t.Error("the centre of a degenerate radial gradient is not covered")
	}
	if _, ok := s.param(15, 0); ok {
		t.Error("two identical circles describe no gradient anywhere")
	}
}

// TestRepeatingGradientDrawsEveryBand is the regression test for reading
// webrender's expanded stop list as if it were already normalised. A repeating
// gradient arrives with every repetition written out as extra stops, running
// past 1 and below 0; scaling them wrongly rendered the whole thing as one
// band, so a scanline pattern came out as a single stripe.
func TestRepeatingGradientDrawsEveryBand(t *testing.T) {
	// Four cycles of black then white across 80 pixels, the way webrender
	// hands one over: positions normalised against the first cycle only.
	var (
		positions []backend.Fl
		colors    []parser.RGBA
	)
	for i := 0; i < 4; i++ {
		base := backend.Fl(i)
		positions = append(positions, base, base+0.5, base+0.5, base+1)
		colors = append(colors,
			rgba(0, 0, 0, 1), rgba(0, 0, 0, 1),
			rgba(1, 1, 1, 1), rgba(1, 1, 1, 1))
	}

	c, img := newTestCanvas(80, 8)
	c.DrawGradient(backend.GradientLayout{
		GradientKind: backend.GradientKind{Kind: "linear", Coords: [6]backend.Fl{0, 0, 80, 0}},
		Positions:    positions,
		Colors:       colors,
		ScaleY:       1,
		Reapeating:   true,
	}, 80, 8)

	// Count the black-to-white transitions along a row: four cycles means
	// four of them.
	transitions := 0
	prev := img.RGBAAt(0, 4).R > 128
	for x := 1; x < 80; x++ {
		now := img.RGBAAt(x, 4).R > 128
		if now != prev {
			transitions++
		}
		prev = now
	}
	if transitions < 6 {
		t.Fatalf("only %d colour changes across the gradient; the repetitions were collapsed", transitions)
	}
}

func TestGradientStopsAreRescaled(t *testing.T) {
	stops := rescale([]gradientStop{{pos: -1}, {pos: 0}, {pos: 3}})
	if stops[0].pos != 0 || stops[2].pos != 1 {
		t.Errorf("stops = %v, want them mapped onto [0,1]", stops)
	}
	if stops[1].pos < 0.24 || stops[1].pos > 0.26 {
		t.Errorf("middle stop = %v, want a quarter of the way", stops[1].pos)
	}
	// Degenerate inputs are left alone rather than dividing by zero.
	if got := rescale([]gradientStop{{pos: 2}}); got[0].pos != 2 {
		t.Errorf("a single stop should be untouched: %v", got)
	}
	if got := rescale([]gradientStop{{pos: 2}, {pos: 2}}); got[0].pos != 2 {
		t.Errorf("a zero span should be untouched: %v", got)
	}
}

func TestGradientClamps(t *testing.T) {
	s := newGradientShader(linearLayout(), matrix.Identity())
	r, _, _, _ := s.sample(-5)
	if r != 255 {
		t.Errorf("before the first stop = %d, want the first colour", r)
	}
	_, _, b, _ := s.sample(5)
	if b != 255 {
		t.Errorf("after the last stop = %d, want the last colour", b)
	}
	if _, _, _, a := s.sample(math.NaN()); a != 0 {
		t.Error("a NaN parameter covers nothing")
	}
}

func TestGradientStopsWithDuplicatePositions(t *testing.T) {
	l := linearLayout()
	l.Positions = []backend.Fl{0, 0.5, 0.5, 1}
	l.Colors = []parser.RGBA{rgba(1, 0, 0, 1), rgba(1, 0, 0, 1), rgba(0, 0, 1, 1), rgba(0, 0, 1, 1)}
	s := newGradientShader(l, matrix.Identity())
	r, _, b, _ := s.sample(0.5)
	if r != 0 || b != 255 {
		t.Errorf("a hard stop should switch colour: %d/%d", r, b)
	}
}

func TestMakeStopsHandlesMismatchedSlices(t *testing.T) {
	if got := makeStops(nil, nil); got != nil {
		t.Error("nothing in, nothing out")
	}
	got := makeStops(nil, []parser.RGBA{rgba(1, 0, 0, 1)})
	if len(got) != 1 || got[0].pos != 0 {
		t.Errorf("a colour with no position sits at 0: %v", got)
	}
	got = makeStops([]backend.Fl{0.5, 0.2}, []parser.RGBA{rgba(1, 0, 0, 1), rgba(0, 1, 0, 1)})
	if got[1].pos < got[0].pos {
		t.Errorf("stop positions must not go backwards: %v", got)
	}
}

func TestDrawGradientGuards(t *testing.T) {
	c, img := newTestCanvas(10, 10)
	c.DrawGradient(linearLayout(), 0, 10)
	c.DrawGradient(backend.GradientLayout{ScaleY: 1}, 10, 10)
	c.Transform(matrix.Scaling(0, 0))
	c.DrawGradient(linearLayout(), 10, 10)
	if img.RGBAAt(5, 5).A != 0 {
		t.Error("nothing should have been drawn")
	}
}

func TestGradientScaleY(t *testing.T) {
	l := linearLayout()
	l.ScaleY = 0 // treated as 1 rather than making the transform singular
	c, img := newTestCanvas(100, 10)
	c.DrawGradient(l, 100, 10)
	if img.RGBAAt(2, 5).A == 0 {
		t.Error("a zero ScaleY should be read as 1")
	}
}

// --- text ------------------------------------------------------------------

func TestAddFontReturnsTheSamePointer(t *testing.T) {
	c, _ := newTestCanvas(10, 10)
	f := fakeFont{}
	first := c.AddFont(f, nil)
	second := c.AddFont(f, nil)
	if first != second {
		t.Fatal("webrender writes into the FontChars it is given, so the same pointer has to come back")
	}
	if first.Cmap == nil || first.Extents == nil {
		t.Error("the maps have to be allocated for webrender to write into")
	}
}

func TestAddFontWithBrokenContent(t *testing.T) {
	c, _ := newTestCanvas(10, 10)
	chars := c.AddFont(fakeFont{}, []byte("not a font"))
	if chars == nil {
		t.Fatal("a font that will not parse still needs its metadata block")
	}
	if c.sh.fonts[fakeFont{}].face != nil {
		t.Error("no face should have been recorded")
	}
}

func TestDrawTextWithoutAFontDrawsNothing(t *testing.T) {
	c, img := newTestCanvas(20, 20)
	c.DrawText([]backend.TextDrawing{{
		FontSize: 10, ScaleX: 1, X: 0, Y: 10,
		Runs: []backend.TextRun{{Font: fakeFont{}, Glyphs: []backend.TextGlyph{{Glyph: 1}}}},
	}})
	if img.RGBAAt(5, 5).A != 0 {
		t.Error("nothing should have been drawn")
	}
}

func TestInvisibleTextPaintDrawsNothing(t *testing.T) {
	c, img := newTestCanvas(20, 20)
	c.SetTextPaint(0)
	c.DrawText([]backend.TextDrawing{{FontSize: 10, ScaleX: 1}})
	if img.RGBAAt(5, 5).A != 0 {
		t.Error("invisible text draws nothing")
	}
	c.SetTextPaint(backend.FillNonZero)
	if c.st.textPaint != backend.FillNonZero {
		t.Error("SetTextPaint")
	}
}

// TestGlyphPenPositions checks the arithmetic that places a glyph, against
// hand-computed values. XAdvance is the pen position the layout already
// accumulated; accumulating it again is the classic way to get text that
// spreads out quadratically.
func TestGlyphPenPositions(t *testing.T) {
	td := backend.TextDrawing{FontSize: 20, ScaleX: 1, X: 100, Y: 200}
	base := matrix.Mul(matrix.Identity(), td.Matrix())

	for _, tt := range []struct {
		glyph backend.TextGlyph
		wantX float64
		wantY float64
	}{
		{backend.TextGlyph{XAdvance: 0, Offset: 0}, 100, 200},
		{backend.TextGlyph{XAdvance: 500, Offset: 0}, 110, 200},   // half an em
		{backend.TextGlyph{XAdvance: 500, Offset: 250}, 115, 200}, // plus a quarter
		// Rise carries pango's glyph y-offset, which grows downwards; a
		// superscript arrives as a negative rise and so moves up.
		{backend.TextGlyph{XAdvance: 1000, Rise: 1024}, 120, 201},
		{backend.TextGlyph{XAdvance: 1000, Rise: -1024}, 120, 199},
	} {
		penX := float64(tt.glyph.XAdvance+tt.glyph.Offset) * float64(td.FontSize) / 1000
		riseY := -float64(tt.glyph.Rise) / 1024
		m := matrix.Mul(base, matrix.New(1, 0, 0, 1, float32(penX), float32(riseY)))
		gotX, gotY := m.Apply(0, 0)
		if math.Abs(float64(gotX)-tt.wantX) > 1e-4 || math.Abs(float64(gotY)-tt.wantY) > 1e-4 {
			t.Errorf("glyph %+v placed at (%v,%v), want (%v,%v)", tt.glyph, gotX, gotY, tt.wantX, tt.wantY)
		}
	}
}

func TestTextMatrixIsYUp(t *testing.T) {
	td := backend.TextDrawing{FontSize: 10, ScaleX: 1, X: 0, Y: 100}
	m := td.Matrix()
	// A glyph point one unit above the baseline in text space is one device
	// pixel higher on the page, which is a smaller y.
	_, y := m.Apply(0, 1)
	if y != 99 {
		t.Errorf("y = %v, want the text space to be y-up", y)
	}
}

// TestTransformPathDoesNotMutateItsInput guards the glyph cache: outlines are
// cached in font units and transformed per glyph, and canvas transforms its
// receiver in place, so a missing copy makes every repeated letter land in the
// wrong place.
func TestTransformPathDoesNotMutateItsInput(t *testing.T) {
	p := &canvas.Path{}
	p.MoveTo(0, 0)
	p.LineTo(10, 0)
	p.LineTo(10, 10)
	p.Close()
	before := p.String()

	got := transformPath(p, matrix.Translation(100, 100))
	if p.String() != before {
		t.Fatalf("the source path was modified: %q, was %q", p.String(), before)
	}
	if got.String() == before {
		t.Fatal("the returned path was not transformed")
	}
	// Transforming the same source twice has to give the same answer.
	again := transformPath(p, matrix.Translation(100, 100))
	if again.String() != got.String() {
		t.Fatalf("second transform = %q, want %q", again.String(), got.String())
	}
}

func TestSegmentsToPath(t *testing.T) {
	if segmentsToPath(nil) != nil {
		t.Error("no segments, no path")
	}
}

// --- page methods ----------------------------------------------------------

func TestPageMethodsAreHarmless(t *testing.T) {
	c, _ := newTestCanvas(4, 4)
	c.AddInternalLink(0, 0, 1, 1, "a")
	c.AddExternalLink(0, 0, 1, 1, "https://x.example")
	c.AddFileAnnotation(0, 0, 1, 1, "f")
	c.SetMediaBox(0, 0, 1, 1)
	c.SetTrimBox(0, 0, 1, 1)
	c.SetBleedBox(0, 0, 1, 1)
	if c.State() != backend.GraphicState(c) {
		t.Error("State should be the canvas itself")
	}
	if c.Image() == nil {
		t.Error("Image")
	}
}

func TestWarnOnceOnlyWarnsOnce(t *testing.T) {
	var buf bytes.Buffer
	c := NewCanvas(image.NewRGBA(image.Rect(0, 0, 1, 1)), zerolog.New(&buf))
	c.sh.warnOnce("k", "first")
	c.sh.warnOnce("k", "second")
	if bytes.Count(buf.Bytes(), []byte("first")) != 1 {
		t.Errorf("log = %q", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("second")) {
		t.Errorf("the second warning should be suppressed: %q", buf.String())
	}
}

func TestClampUnit(t *testing.T) {
	for _, tt := range []struct {
		in   backend.Fl
		want float32
	}{{-1, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {2, 1}} {
		if got := clampUnit(tt.in); got != tt.want {
			t.Errorf("clampUnit(%v) = %v", tt.in, got)
		}
	}
}
