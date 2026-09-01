package raster

import (
	"image"
	"math"

	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/css/parser"
	"github.com/benoitkugler/webrender/matrix"
)

// DrawGradient fills the rectangle (0,0)-(width,height) of the current user
// space with a linear or radial gradient.
//
// webrender resolves a one-stop gradient into a plain rectangle fill before it
// gets here, so only the two real kinds arrive.
func (c *Canvas) DrawGradient(g backend.GradientLayout, width, height backend.Fl) {
	if width <= 0 || height <= 0 || len(g.Colors) == 0 {
		return
	}
	// The gradient is laid out in a space scaled vertically by ScaleY, which is
	// how an elliptical radial gradient is expressed as a circular one.
	scaleY := float64(g.ScaleY)
	if scaleY == 0 {
		scaleY = 1
	}
	toDevice := matrix.Mul(c.st.ctm, matrix.Scaling(1, backend.Fl(scaleY)))
	inv := toDevice
	if err := inv.Invert(); err != nil {
		return
	}

	sh := newGradientShader(g, inv)
	if sh == nil {
		return
	}

	// The painted area is the rectangle webrender asked for, mapped into
	// device space and clipped.
	c.MoveTo(0, 0)
	c.LineTo(width, 0)
	c.LineTo(width, height)
	c.LineTo(0, height)
	c.ClosePath()
	path := c.path
	c.path, c.hasPoint = nil, false

	area := c.drawArea()
	if area.Empty() {
		return
	}
	mask := rasterize(path, area, false)
	if mask == nil {
		return
	}
	compose(c.dst, mask.Bounds(), mask, sh, c.st.fill.alpha, c.st.clip, c.st.blend)
}

// gradientShader evaluates a gradient per device pixel.
type gradientShader struct {
	inv    matrix.Transform // device -> gradient space
	radial bool

	// linear: the gradient axis, precomputed for a dot product.
	x0, y0     float64
	dx, dy     float64
	lenSquared float64

	// radial: the two circles.
	cx0, cy0, r0 float64
	cdx, cdy, dr float64
	a            float64

	stops []gradientStop
}

type gradientStop struct {
	pos            float64
	r, g, b, alpha float64
}

func newGradientShader(g backend.GradientLayout, inv matrix.Transform) *gradientShader {
	s := &gradientShader{inv: inv}
	s.stops = makeStops(g.Positions, g.Colors)
	if len(s.stops) == 0 {
		return nil
	}
	switch g.Kind {
	case "linear":
		s.x0, s.y0 = float64(g.Coords[0]), float64(g.Coords[1])
		s.dx, s.dy = float64(g.Coords[2])-s.x0, float64(g.Coords[3])-s.y0
		s.lenSquared = s.dx*s.dx + s.dy*s.dy
		if s.lenSquared == 0 {
			return nil
		}
	case "radial":
		s.radial = true
		s.cx0, s.cy0, s.r0 = float64(g.Coords[0]), float64(g.Coords[1]), float64(g.Coords[2])
		s.cdx = float64(g.Coords[3]) - s.cx0
		s.cdy = float64(g.Coords[4]) - s.cy0
		s.dr = float64(g.Coords[5]) - s.r0
		s.a = s.cdx*s.cdx + s.cdy*s.cdy - s.dr*s.dr
	default:
		return nil
	}
	return s
}

// makeStops turns the parallel position and colour slices into stops in [0,1].
//
// The rescaling is the part that matters. webrender hands over a repeating
// gradient with the repetitions already written out as extra stops, and those
// stops run past 1 and below 0 — they are normalised against the *original*
// colour run, while Coords spans the whole expanded range. Reading them as if
// they were already [0,1] renders a repeating gradient as a single band, which
// is what this used to do.
func makeStops(positions []backend.Fl, colors []parser.RGBA) []gradientStop {
	n := len(colors)
	if len(positions) < n {
		n = len(positions)
	}
	if n == 0 {
		if len(colors) == 0 {
			return nil
		}
		c := colors[0]
		return []gradientStop{{pos: 0, r: float64(c.R), g: float64(c.G), b: float64(c.B), alpha: float64(c.A)}}
	}
	out := make([]gradientStop, 0, n)
	prev := math.Inf(-1)
	for i := 0; i < n; i++ {
		p := float64(positions[i])
		if p < prev {
			p = prev // stop positions are non-decreasing by definition
		}
		prev = p
		c := colors[i]
		out = append(out, gradientStop{pos: p, r: float64(c.R), g: float64(c.G), b: float64(c.B), alpha: float64(c.A)})
	}
	return rescale(out)
}

// rescale maps the stop positions onto [0,1], which is the range the parameter
// computed from Coords lives in.
func rescale(stops []gradientStop) []gradientStop {
	if len(stops) < 2 {
		return stops
	}
	lo, hi := stops[0].pos, stops[len(stops)-1].pos
	span := hi - lo
	if span <= 0 {
		return stops
	}
	for i := range stops {
		stops[i].pos = (stops[i].pos - lo) / span
	}
	return stops
}

func (s *gradientShader) colorAt(x, y int) (uint8, uint8, uint8, uint8) {
	// Sample at the pixel centre.
	gx, gy := s.inv.Apply(backend.Fl(float64(x)+0.5), backend.Fl(float64(y)+0.5))
	t, ok := s.param(float64(gx), float64(gy))
	if !ok {
		return 0, 0, 0, 0
	}
	return s.sample(t)
}

// param is the gradient parameter at a point in gradient space, and whether
// the point is covered at all (a radial gradient can leave points uncovered).
func (s *gradientShader) param(x, y float64) (float64, bool) {
	if !s.radial {
		return ((x-s.x0)*s.dx + (y-s.y0)*s.dy) / s.lenSquared, true
	}
	// Two-circle radial gradient: find the largest t with a non-negative
	// radius, which is what the PDF and Canvas specifications both say.
	px, py := x-s.cx0, y-s.cy0
	b := px*s.cdx + py*s.cdy + s.r0*s.dr
	cc := px*px + py*py - s.r0*s.r0
	if s.a == 0 {
		if b == 0 {
			return 0, false
		}
		t := cc / (2 * b)
		if s.r0+t*s.dr < 0 {
			return 0, false
		}
		return t, true
	}
	disc := b*b - s.a*cc
	if disc < 0 {
		return 0, false
	}
	root := math.Sqrt(disc)
	t1 := (b + root) / s.a
	t2 := (b - root) / s.a
	if t1 < t2 {
		t1, t2 = t2, t1
	}
	if s.r0+t1*s.dr >= 0 {
		return t1, true
	}
	if s.r0+t2*s.dr >= 0 {
		return t2, true
	}
	return 0, false
}

// sample interpolates the stop colours at t, clamping outside the range.
//
// There is deliberately no wrapping here: a repeating gradient arrives with
// its repetitions already expanded into the stop list, covering the whole
// painting area, so wrapping would fold it back on itself.
func (s *gradientShader) sample(t float64) (uint8, uint8, uint8, uint8) {
	if math.IsNaN(t) || math.IsInf(t, 0) {
		return 0, 0, 0, 0
	}
	first, last := s.stops[0], s.stops[len(s.stops)-1]
	if t <= first.pos {
		return toRGBA(first.r, first.g, first.b, first.alpha)
	}
	if t >= last.pos {
		return toRGBA(last.r, last.g, last.b, last.alpha)
	}
	// The segment is the one ending at the first stop strictly past t. Taking
	// the first stop at or past t instead would read a hard stop — two stops at
	// the same position — as the colour before the break rather than after it.
	for i := 1; i < len(s.stops); i++ {
		a, b := s.stops[i-1], s.stops[i]
		if b.pos <= t {
			continue
		}
		span := b.pos - a.pos
		if span <= 0 {
			return toRGBA(b.r, b.g, b.b, b.alpha)
		}
		f := (t - a.pos) / span
		if f < 0 {
			f = 0
		}
		return toRGBA(
			a.r+(b.r-a.r)*f,
			a.g+(b.g-a.g)*f,
			a.b+(b.b-a.b)*f,
			a.alpha+(b.alpha-a.alpha)*f,
		)
	}
	return toRGBA(last.r, last.g, last.b, last.alpha)
}

func toRGBA(r, g, b, a float64) (uint8, uint8, uint8, uint8) {
	return clamp8(float32(r)), clamp8(float32(g)), clamp8(float32(b)), clamp8(float32(a))
}

var _ image.Image = (*image.RGBA)(nil)
