package raster

import (
	"image"
	"math"

	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/matrix"
)

// groupPad is how many device pixels a group's box is grown by.
//
// Antialiasing writes up to a pixel outside a shape's exact bounds, and a box
// webrender computed from the CSS box may not account for a stroke's outer
// half. Two pixels is cheap insurance against a clipped edge.
const groupPad = 2

// NewGroup creates an off-screen layer to draw into.
//
// The layer covers the box webrender asked for, intersected with the page, and
// starts fully transparent. It inherits the current transform so that what is
// drawn into it lands where it would have landed on the page — which is what
// lets the layer keep device coordinates, so nothing downstream has to know it
// is smaller than the page.
//
// Smaller than the page matters: a full-page RGBA is 33MB at 1080 square and
// scale 2, one per opacity group, pattern and mask — and one per group per
// frame of a video. Most groups cover a caption or a badge.
//
// It starts unclipped on purpose: the clip in force when the group is drawn
// back is applied then, which is what the PDF XObject it stands in for does.
func (c *Canvas) NewGroup(x, y, width, height backend.Fl) backend.Canvas {
	bounds := c.groupBounds(x, y, width, height)
	child := &Canvas{
		dst:  image.NewRGBA(bounds),
		sh:   c.sh,
		bbox: [4]backend.Fl{x, y, x + width, y + height},
		st: state{
			ctm: c.st.ctm,
			// The layer's own extent, which is what "unclipped" means for a
			// layer that is not the whole page.
			clip:       newRectClip(bounds),
			fill:       c.st.fill,
			stroke:     c.st.stroke,
			lineWidth:  c.st.lineWidth,
			dashes:     c.st.dashes,
			dashOffset: c.st.dashOffset,
			strokeOpts: c.st.strokeOpts,
			textPaint:  c.st.textPaint,
		},
	}
	return child
}

// groupBounds is the device rectangle a group needs, or the whole page when
// the requested box cannot be used.
//
// The box arrives in user space, so it goes through the current transform; a
// rotation or a shear makes the four corners something other than a rectangle,
// and their bounding box is the right answer either way.
func (c *Canvas) groupBounds(x, y, width, height backend.Fl) image.Rectangle {
	page := c.dst.Bounds()
	if width <= 0 || height <= 0 {
		return page
	}
	xs := [4]float64{}
	ys := [4]float64{}
	corners := [4][2]backend.Fl{{x, y}, {x + width, y}, {x + width, y + height}, {x, y + height}}
	for i, pt := range corners {
		dx, dy := c.dev(pt[0], pt[1])
		if math.IsNaN(dx) || math.IsNaN(dy) || math.IsInf(dx, 0) || math.IsInf(dy, 0) {
			return page
		}
		xs[i], ys[i] = dx, dy
	}
	minX, maxX := xs[0], xs[0]
	minY, maxY := ys[0], ys[0]
	for i := 1; i < 4; i++ {
		minX, maxX = math.Min(minX, xs[i]), math.Max(maxX, xs[i])
		minY, maxY = math.Min(minY, ys[i]), math.Max(maxY, ys[i])
	}
	box := image.Rect(
		int(math.Floor(minX))-groupPad, int(math.Floor(minY))-groupPad,
		int(math.Ceil(maxX))+groupPad, int(math.Ceil(maxY))+groupPad,
	).Intersect(page)
	if box.Empty() {
		// A box entirely off the page is more likely a box crier misread than
		// a group with nothing in it, and the page costs memory rather than
		// correctness.
		return page
	}
	return box
}

// DrawWithOpacity composites a layer back onto this canvas.
func (c *Canvas) DrawWithOpacity(opacity backend.Fl, group backend.Canvas) {
	layer, ok := group.(*Canvas)
	if !ok {
		c.sh.warnOnce("group:foreign", "a group from a foreign canvas was ignored")
		return
	}
	area := c.drawArea().Intersect(layer.dst.Bounds())
	if area.Empty() {
		return
	}
	compose(c.dst, area, nil, &layerShader{layer: layer.dst}, clampUnit(opacity), c.st.clip, c.st.blend)
}

// patternPaint is a tiled layer used as a fill or stroke colour, which is how
// a repeating background image is painted.
type patternPaint struct {
	layer *image.RGBA
	// toPattern maps a device point into the pattern's own space.
	toPattern matrix.Transform
	// toDevice maps a point of one tile back into the layer's device space.
	toDevice matrix.Transform
	// stepX and stepY are the tile size, in pattern space.
	stepX, stepY float64
}

func (p *patternPaint) colorAt(x, y int) (uint8, uint8, uint8, uint8) {
	ux, uy := p.toPattern.Apply(backend.Fl(float64(x)+0.5), backend.Fl(float64(y)+0.5))
	tx, ty := float64(ux), float64(uy)
	if p.stepX > 0 {
		tx -= p.stepX * math.Floor(tx/p.stepX)
	}
	if p.stepY > 0 {
		ty -= p.stepY * math.Floor(ty/p.stepY)
	}
	dx, dy := p.toDevice.Apply(backend.Fl(tx), backend.Fl(ty))
	sx, sy := int(math.Floor(float64(dx))), int(math.Floor(float64(dy)))
	shader := layerShader{layer: p.layer}
	return shader.colorAt(sx, sy)
}

// SetColorPattern makes subsequent fills or strokes paint with a tiled layer.
//
// The layer was drawn with this canvas's transform, so a point is looked up by
// going device -> pattern space (undoing both the transform and mat), taking
// the remainder against the tile size, and going back to the layer's device
// space with the transform alone.
func (c *Canvas) SetColorPattern(pattern backend.Canvas, contentWidth, contentHeight backend.Fl, mat matrix.Transform, stroke bool) {
	layer, ok := pattern.(*Canvas)
	if !ok {
		c.sh.warnOnce("pattern:foreign", "a pattern from a foreign canvas was ignored")
		return
	}
	left, top, right, bottom := layer.GetBoundingBox()
	stepX, stepY := float64(right-left), float64(bottom-top)
	if stepX <= 0 {
		stepX = float64(contentWidth)
	}
	if stepY <= 0 {
		stepY = float64(contentHeight)
	}

	toPattern := matrix.Mul(c.st.ctm, mat)
	if err := toPattern.Invert(); err != nil {
		c.sh.warnOnce("pattern:singular", "a pattern with a singular transform was ignored")
		return
	}
	p := &patternPaint{
		layer:     layer.dst,
		toPattern: toPattern,
		toDevice:  c.st.ctm,
		stepX:     stepX,
		stepY:     stepY,
	}
	if stroke {
		c.st.stroke.pattern = p
		return
	}
	c.st.fill.pattern = p
}
