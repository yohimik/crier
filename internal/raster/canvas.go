// Package raster is a raster implementation of webrender's backend.Page, so
// an HTML document can be laid out by webrender and drawn straight into an
// image instead of into a PDF.
//
// The whole package works in device pixels with y growing downwards, which is
// the space webrender's document.Page.Paint hands over. It implements
// backend.Page rather than backend.Document on purpose: Page.Paint is the
// entry point that stays in CSS pixels, while Document.Write is PDF-specific
// (it scales by 0.75 and flips the y axis).
package raster

import (
	"image"
	"sync"

	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/css/parser"
	"github.com/benoitkugler/webrender/matrix"
	"github.com/rs/zerolog"
	"github.com/tdewolff/canvas"
)

var (
	_ backend.Canvas       = (*Canvas)(nil)
	_ backend.Page         = (*Canvas)(nil)
	_ backend.GraphicState = (*Canvas)(nil)
)

// paint is a fill or stroke colour, with its alpha kept apart the way the
// backend interface keeps them: SetColorRgba sets both, SetAlpha only the
// second.
type paint struct {
	r, g, b uint8
	alpha   float32
	pattern *patternPaint
}

func (p paint) shader() shader {
	if p.pattern != nil {
		return p.pattern
	}
	return solidShader{r: p.r, g: p.g, b: p.b, a: 255}
}

// state is the graphic state OnNewStack saves and restores.
type state struct {
	ctm        matrix.Transform
	clip       *clip
	fill       paint
	stroke     paint
	lineWidth  float64
	dashes     []float64
	dashOffset float64
	strokeOpts backend.StrokeOptions
	blend      string
	textPaint  backend.PaintOp
}

// shared holds what every canvas of one rendering has in common: the caches
// and the logger. Groups share it with the page they came from, so a font
// parsed for the page is not parsed again for a group.
type shared struct {
	log    zerolog.Logger
	fonts  map[backend.Font]*fontEntry
	images map[int]image.Image
	warned map[string]bool
	mu     sync.Mutex
}

func (s *shared) warnOnce(key string, msg string) {
	s.mu.Lock()
	seen := s.warned[key]
	s.warned[key] = true
	s.mu.Unlock()
	if !seen {
		s.log.Warn().Msg(msg)
	}
}

// Canvas draws webrender's graphic operations onto an RGBA image.
//
// It is also the backend.Page and the backend.GraphicState: the interface
// splits them for backends where a state is a separate object, and here they
// are the same thing, exactly as in webrender's own PDF backend.
type Canvas struct {
	dst *image.RGBA
	sh  *shared

	bbox  [4]backend.Fl // left, top, right, bottom, in the space of the caller
	st    state
	stack []state

	// path is accumulated in device coordinates: every point goes through the
	// CTM as it is appended, which an affine transform lets us do without
	// distorting the Béziers.
	path *canvas.Path
}

// NewCanvas builds a canvas drawing onto dst, which must be premultiplied
// RGBA. dst is not cleared, so a caller that wants a background paints it
// first.
func NewCanvas(dst *image.RGBA, log zerolog.Logger) *Canvas {
	return &Canvas{
		dst: dst,
		sh: &shared{
			log:    log,
			fonts:  map[backend.Font]*fontEntry{},
			images: map[int]image.Image{},
			warned: map[string]bool{},
		},
		bbox: [4]backend.Fl{
			backend.Fl(dst.Bounds().Min.X), backend.Fl(dst.Bounds().Min.Y),
			backend.Fl(dst.Bounds().Max.X), backend.Fl(dst.Bounds().Max.Y),
		},
		st: state{
			ctm:       matrix.Identity(),
			clip:      newRectClip(dst.Bounds()),
			fill:      paint{alpha: 1},
			stroke:    paint{alpha: 1},
			lineWidth: 1,
			textPaint: backend.FillNonZero,
		},
	}
}

// Image is the canvas's destination.
func (c *Canvas) Image() *image.RGBA { return c.dst }

// State returns the canvas itself: a state is not a separate object here.
func (c *Canvas) State() backend.GraphicState { return c }

// --- backend.Page link and box methods -------------------------------------
//
// A raster image has nowhere to put a link or a media box, so these are all
// no-ops. They exist because backend.Page requires them.

func (c *Canvas) AddInternalLink(_, _, _, _ backend.Fl, _ string)   {}
func (c *Canvas) AddExternalLink(_, _, _, _ backend.Fl, _ string)   {}
func (c *Canvas) AddFileAnnotation(_, _, _, _ backend.Fl, _ string) {}
func (c *Canvas) SetMediaBox(_, _, _, _ backend.Fl)                 {}
func (c *Canvas) SetTrimBox(_, _, _, _ backend.Fl)                  {}
func (c *Canvas) SetBleedBox(_, _, _, _ backend.Fl)                 {}

// --- bounding box ----------------------------------------------------------

// GetBoundingBox returns the rectangle the canvas was created for.
func (c *Canvas) GetBoundingBox() (left, top, right, bottom backend.Fl) {
	return c.bbox[0], c.bbox[1], c.bbox[2], c.bbox[3]
}

// SetBoundingBox records a new rectangle. It does not resize the image: the
// destination is allocated once, and webrender only uses the box to ask how
// large a group is.
func (c *Canvas) SetBoundingBox(left, top, right, bottom backend.Fl) {
	c.bbox = [4]backend.Fl{left, top, right, bottom}
}

// --- graphic stack ---------------------------------------------------------

// OnNewStack saves the graphic state, runs task, and restores it.
//
// The path is deliberately not saved: PDF's q/Q do not save the current path
// either, and webrender is written against those semantics.
func (c *Canvas) OnNewStack(task func()) {
	c.stack = append(c.stack, c.st)
	task()
	c.st = c.stack[len(c.stack)-1]
	c.stack = c.stack[:len(c.stack)-1]
}

// --- transform -------------------------------------------------------------

// GetTransform returns the current transformation matrix.
func (c *Canvas) GetTransform() matrix.Transform { return c.st.ctm }

// Transform applies mt before the existing transformation, which is the order
// the interface documents and the order the PDF backend uses.
func (c *Canvas) Transform(mt matrix.Transform) { c.st.ctm.RightMultBy(mt) }

// --- paint settings --------------------------------------------------------

// SetColorRgba sets the colour and, from its alpha channel, the alpha.
func (c *Canvas) SetColorRgba(col parser.RGBA, stroke bool) {
	p := &c.st.fill
	if stroke {
		p = &c.st.stroke
	}
	p.r, p.g, p.b = clamp8(col.R), clamp8(col.G), clamp8(col.B)
	p.alpha = clampUnit(col.A)
	p.pattern = nil
}

// SetAlpha sets only the alpha.
func (c *Canvas) SetAlpha(alpha backend.Fl, stroke bool) {
	if stroke {
		c.st.stroke.alpha = clampUnit(alpha)
		return
	}
	c.st.fill.alpha = clampUnit(alpha)
}

func clampUnit(v backend.Fl) float32 {
	switch {
	case v <= 0:
		return 0
	case v >= 1:
		return 1
	default:
		return float32(v)
	}
}

// SetBlendingMode records a CSS blend mode.
func (c *Canvas) SetBlendingMode(mode string) {
	if isNonSeparableBlend(mode) {
		c.sh.warnOnce("blend:"+mode,
			"blend mode "+mode+" mixes colour channels and is not supported; drawing normally instead")
		c.st.blend = ""
		return
	}
	c.st.blend = mode
}

// SetLineWidth sets the stroke width, in user units.
func (c *Canvas) SetLineWidth(width backend.Fl) { c.st.lineWidth = float64(width) }

// SetDash sets the dash pattern, in user units.
func (c *Canvas) SetDash(dashes []backend.Fl, offset backend.Fl) {
	if len(dashes) == 0 {
		c.st.dashes, c.st.dashOffset = nil, 0
		return
	}
	out := make([]float64, 0, len(dashes))
	allZero := true
	for _, d := range dashes {
		if d < 0 {
			d = 0
		}
		if d > 0 {
			allZero = false
		}
		out = append(out, float64(d))
	}
	if allZero {
		// An all-zero pattern would make the dasher loop forever.
		c.st.dashes, c.st.dashOffset = nil, 0
		return
	}
	c.st.dashes, c.st.dashOffset = out, float64(offset)
}

// SetStrokeOptions sets the cap, join and miter limit.
func (c *Canvas) SetStrokeOptions(o backend.StrokeOptions) { c.st.strokeOpts = o }

// SetTextPaint says whether glyphs are filled, stroked, both or neither.
func (c *Canvas) SetTextPaint(op backend.PaintOp) { c.st.textPaint = op }

// --- path construction -----------------------------------------------------

func (c *Canvas) ensurePath() *canvas.Path {
	if c.path == nil {
		c.path = &canvas.Path{}
	}
	return c.path
}

func (c *Canvas) dev(x, y backend.Fl) (float64, float64) {
	dx, dy := c.st.ctm.Apply(x, y)
	return float64(dx), float64(dy)
}

// MoveTo starts a new subpath.
func (c *Canvas) MoveTo(x, y backend.Fl) {
	px, py := c.dev(x, y)
	c.ensurePath().MoveTo(px, py)
}

// LineTo adds a straight segment.
func (c *Canvas) LineTo(x, y backend.Fl) {
	px, py := c.dev(x, y)
	p := c.ensurePath()
	if p.Empty() {
		p.MoveTo(px, py)
		return
	}
	p.LineTo(px, py)
}

// CubicTo adds a cubic Bézier. An affine transform maps a Bézier onto a
// Bézier, so transforming the four points is exact.
func (c *Canvas) CubicTo(x1, y1, x2, y2, x3, y3 backend.Fl) {
	p := c.ensurePath()
	px1, py1 := c.dev(x1, y1)
	px2, py2 := c.dev(x2, y2)
	px3, py3 := c.dev(x3, y3)
	if p.Empty() {
		p.MoveTo(px1, py1)
	}
	p.CubeTo(px1, py1, px2, py2, px3, py3)
}

// ClosePath closes the current subpath.
func (c *Canvas) ClosePath() {
	if c.path == nil || c.path.Empty() {
		return
	}
	c.path.Close()
}

// Rectangle adds an axis-aligned rectangle, in user space. Under a rotation it
// stops being axis-aligned, which is why it goes through the same transform as
// every other point.
func (c *Canvas) Rectangle(x, y, width, height backend.Fl) {
	p := c.ensurePath()
	x0, y0 := c.dev(x, y)
	x1, y1 := c.dev(x+width, y)
	x2, y2 := c.dev(x+width, y+height)
	x3, y3 := c.dev(x, y+height)
	p.MoveTo(x0, y0)
	p.LineTo(x1, y1)
	p.LineTo(x2, y2)
	p.LineTo(x3, y3)
	p.Close()
}

// --- painting --------------------------------------------------------------

// Paint fills and/or strokes the current path and clears it.
func (c *Canvas) Paint(op backend.PaintOp) {
	path := c.path
	c.path = nil
	if path == nil || path.Empty() {
		return
	}
	area := c.drawArea()
	if area.Empty() {
		return
	}

	if op&(backend.FillEvenOdd|backend.FillNonZero) != 0 {
		mask := rasterize(path, area, op&backend.FillEvenOdd != 0)
		if mask != nil {
			compose(c.dst, mask.Bounds(), mask, c.st.fill.shader(), c.st.fill.alpha, c.st.clip, c.st.blend)
		}
	}
	if op&backend.Stroke != 0 {
		outline := strokeOutline(path, c.strokeWidth(), c.scaledDashes(), c.st.dashOffset*c.scale(), c.st.strokeOpts)
		if outline != nil {
			mask := rasterize(outline, area, false)
			if mask != nil {
				compose(c.dst, mask.Bounds(), mask, c.st.stroke.shader(), c.st.stroke.alpha, c.st.clip, c.st.blend)
			}
		}
	}
}

// drawArea is the rectangle a drawing operation may touch.
func (c *Canvas) drawArea() image.Rectangle {
	return c.dst.Bounds().Intersect(c.st.clip.bounds())
}

// scale is the average linear scale of the CTM, which is what a line width in
// user units has to be multiplied by. Under a shear the stroke is only an
// approximation, which is what every scanline rasterizer does.
func (c *Canvas) scale() float64 {
	det := float64(c.st.ctm.Determinant())
	if det < 0 {
		det = -det
	}
	if det == 0 {
		return 0
	}
	return sqrt(det)
}

func (c *Canvas) strokeWidth() float64 {
	w := c.st.lineWidth * c.scale()
	if w > 0 && w < 0.8 {
		// A hairline still has to be visible; this is what every renderer does
		// with a sub-pixel stroke.
		w = 0.8
	}
	return w
}

func (c *Canvas) scaledDashes() []float64 {
	if len(c.st.dashes) == 0 {
		return nil
	}
	s := c.scale()
	out := make([]float64, len(c.st.dashes))
	for i, d := range c.st.dashes {
		out[i] = d * s
	}
	return out
}

// --- clipping --------------------------------------------------------------

// Clip narrows the clip region to the current path and clears the path.
func (c *Canvas) Clip(evenOdd bool) {
	path := c.path
	c.path = nil
	if path == nil || path.Empty() {
		return
	}
	if r, ok := deviceRect(path); ok {
		c.st.clip = c.st.clip.intersectRect(r)
		return
	}
	mask := rasterize(path, c.drawArea(), evenOdd)
	c.st.clip = c.st.clip.intersectMask(mask)
}

// SetAlphaMask multiplies the luminosity of a drawn group into the clip, which
// is how SVG masks and CSS masks are expressed.
func (c *Canvas) SetAlphaMask(mask backend.Canvas) {
	layer, ok := mask.(*Canvas)
	if !ok {
		c.sh.warnOnce("alphamask:foreign", "alpha mask from a foreign canvas ignored")
		return
	}
	c.st.clip = c.st.clip.intersectMask(luminosityMask(layer.dst, c.drawArea()))
}

// luminosityMask turns a premultiplied layer into the alpha mask a luminosity
// mask means: the perceived brightness of what was drawn, already multiplied
// by its own alpha because the layer is premultiplied.
func luminosityMask(layer *image.RGBA, area image.Rectangle) *image.Alpha {
	r := layer.Bounds().Intersect(area)
	if r.Empty() {
		return nil
	}
	out := image.NewAlpha(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := layer.PixOffset(x, y)
			p := layer.Pix[i : i+4 : i+4]
			lum := (2126*uint32(p[0]) + 7152*uint32(p[1]) + 722*uint32(p[2])) / 10000
			if lum > 255 {
				lum = 255
			}
			out.SetAlpha(x, y, colorAlpha(uint8(lum)))
		}
	}
	return out
}
