package raster

import (
	"testing"

	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/css/parser"
)

// TestFadeToTransparentKeepsItsColour is the regression test.
//
// CSS interpolates gradient stops in premultiplied space. Interpolating
// straight instead drags "transparent" — which is rgba(0,0,0,0), black — into
// the mix, so linear-gradient(red, transparent) runs through a muddy grey at
// half alpha instead of staying red. That gradient is the commonest overlay
// there is: it is how a caption is made readable over a photo.
func TestFadeToTransparentKeepsItsColour(t *testing.T) {
	stops := makeStops(
		[]backend.Fl{0, 1},
		[]parser.RGBA{{R: 1, G: 0, B: 0, A: 1}, {R: 0, G: 0, B: 0, A: 0}},
	)
	s := &gradientShader{stops: stops}

	r, g, b, a := s.sample(0.5)
	if a < 120 || a > 135 {
		t.Errorf("alpha at the midpoint = %d, want about half", a)
	}
	if r < 250 {
		t.Errorf("red at the midpoint = %d, want it still fully red", r)
	}
	if g != 0 || b != 0 {
		t.Errorf("the midpoint picked up green %d and blue %d", g, b)
	}

	// The ends are exact and unchanged.
	if r, g, b, a := s.sample(0); r != 255 || g != 0 || b != 0 || a != 255 {
		t.Errorf("the start = %d,%d,%d,%d", r, g, b, a)
	}
	if _, _, _, a := s.sample(1); a != 0 {
		t.Errorf("the end alpha = %d", a)
	}
}

// TestOpaqueGradientIsUnchanged: premultiplying must not move a fade between
// two opaque colours, which is every gradient in the examples.
func TestOpaqueGradientIsUnchanged(t *testing.T) {
	s := &gradientShader{stops: makeStops(
		[]backend.Fl{0, 1},
		[]parser.RGBA{{R: 1, G: 0, B: 0, A: 1}, {R: 0, G: 0, B: 1, A: 1}},
	)}
	r, g, b, a := s.sample(0.5)
	if a != 255 {
		t.Errorf("alpha = %d, want opaque throughout", a)
	}
	// Half way between red and blue, in each channel.
	if r < 126 || r > 129 || b < 126 || b > 129 || g != 0 {
		t.Errorf("the midpoint = %d,%d,%d", r, g, b)
	}
}

// TestFadeBetweenTwoTranslucentColours checks the general case rather than the
// fade-to-nothing one.
func TestFadeBetweenTwoTranslucentColours(t *testing.T) {
	s := &gradientShader{stops: makeStops(
		[]backend.Fl{0, 1},
		[]parser.RGBA{{R: 1, G: 0, B: 0, A: 1}, {R: 0, G: 0, B: 1, A: 0.5}},
	)}
	r, g, b, a := s.sample(0.5)
	// Premultiplied midpoint: (0.5, 0, 0.25) at alpha 0.75, which unpremultiplies
	// to (0.667, 0, 0.333).
	if a < 188 || a > 194 {
		t.Errorf("alpha = %d, want about three quarters", a)
	}
	if r < 165 || r > 176 {
		t.Errorf("red = %d, want about two thirds", r)
	}
	if b < 80 || b > 91 {
		t.Errorf("blue = %d, want about one third", b)
	}
	if g != 0 {
		t.Errorf("green = %d", g)
	}
}

// TestUnpremultiplyEdges covers the cases the fade never reaches.
func TestUnpremultiplyEdges(t *testing.T) {
	if r, g, b, a := unpremultiply(0, 0, 0, 0); r|g|b|a != 0 {
		t.Errorf("zero alpha = %d,%d,%d,%d", r, g, b, a)
	}
	// An alpha over one is clamped rather than used as a divisor.
	if _, _, _, a := unpremultiply(1, 1, 1, 2); a != 255 {
		t.Errorf("alpha = %d", a)
	}
}
