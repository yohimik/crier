package raster

import (
	"image"
	"math"

	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/matrix"
)

// NewGroup creates an off-screen layer to draw into.
//
// The layer is the same size as the page and starts fully transparent, and it
// inherits the current transform so that what is drawn into it lands where it
// would have landed on the page. It starts unclipped on purpose: the clip in
// force when the group is drawn back is applied then, which is what the PDF
// XObject it stands in for does.
func (c *Canvas) NewGroup(x, y, width, height backend.Fl) backend.Canvas {
	child := &Canvas{
		dst:  image.NewRGBA(c.dst.Bounds()),
		sh:   c.sh,
		bbox: [4]backend.Fl{x, y, x + width, y + height},
		st: state{
			ctm:        c.st.ctm,
			clip:       newRectClip(c.dst.Bounds()),
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
