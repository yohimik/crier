package raster

import (
	"bytes"
	"image"
	"math"

	"github.com/benoitkugler/textlayout/fonts"
	"github.com/benoitkugler/textlayout/fonts/truetype"
	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/matrix"
	"github.com/benoitkugler/webrender/text"
	drawtext "github.com/benoitkugler/webrender/text/draw"
	"github.com/tdewolff/canvas"
)

// fontEntry is one registered font: the metadata webrender writes into, and
// the parsed face crier reads glyph outlines out of.
type fontEntry struct {
	chars   *backend.FontChars
	face    *truetype.Font
	upem    float64
	origin  text.FontOrigin
	outline map[backend.GID]*canvas.Path // glyph outlines, in font units
}

// AddFont registers a font and returns the metadata block webrender fills in.
//
// webrender calls this once per text run and writes into the returned maps, so
// the very same pointer has to come back every time — handing out a fresh
// FontChars would throw away the extents the previous run recorded, and the
// extents are what the emoji path and the PDF-style metrics depend on.
func (c *Canvas) AddFont(font backend.Font, content []byte) *backend.FontChars {
	if e, ok := c.sh.fonts[font]; ok {
		return e.chars
	}
	e := &fontEntry{
		chars: &backend.FontChars{
			Cmap:    map[backend.GID][]rune{},
			Extents: map[backend.GID]backend.GlyphExtents{},
		},
		origin:  font.Origin(),
		upem:    1000,
		outline: map[backend.GID]*canvas.Path{},
	}
	if len(content) > 0 {
		if face, err := truetype.Parse(bytes.NewReader(content)); err == nil {
			e.face = face
			if u := face.Upem(); u > 0 {
				e.upem = float64(u)
			}
		} else {
			c.sh.warnOnce("font:"+e.origin.File,
				"could not parse font "+e.origin.File+": "+err.Error()+"; its text will not be drawn")
		}
	}
	c.sh.fonts[font] = e
	return e.chars
}

// DrawText draws the glyphs webrender laid out.
//
// The geometry, which is the whole of the difficulty:
//
//   - td.Matrix() maps text space to user space. It is y-up (its d is -1) and
//     its unit is one CSS pixel; the font size is NOT in it.
//   - a glyph outline is in font units, so it is scaled by FontSize/upem.
//   - XAdvance is the pen position of the glyph, already accumulated by the
//     layout, in thousandths of the font size. It must not be accumulated
//     again. Offset is that glyph's own extra shift, in the same units.
//   - Rise is a vertical shift in pango units (1/1024 px), positive downwards,
//     so it enters the y-up text space negated.
func (c *Canvas) DrawText(texts []backend.TextDrawing) {
	if c.st.textPaint == 0 {
		return // invisible text: still advances the layout, draws nothing
	}
	area := c.drawArea()
	if area.Empty() {
		return
	}

	for _, td := range texts {
		base := matrix.Mul(c.st.ctm, td.Matrix())
		fontSize := float64(td.FontSize)
		if fontSize == 0 {
			continue
		}
		for _, run := range td.Runs {
			entry, ok := c.sh.fonts[run.Font]
			if !ok || entry.face == nil {
				continue
			}
			k := fontSize / entry.upem
			for _, g := range run.Glyphs {
				if g.Glyph == backend.GID(fonts.EmptyGlyph) {
					continue
				}
				penX := float64(g.XAdvance+g.Offset) * fontSize / 1000
				riseY := -float64(g.Rise) / 1024

				glyphMat := matrix.Mul(base, matrix.New(
					float32(k), 0, 0, float32(k),
					float32(penX), float32(riseY),
				))
				c.drawGlyph(entry, g.Glyph, glyphMat, area)

				// Colour bitmap glyphs (emoji) are not outlines; webrender
				// knows how to turn them into a raster image on this canvas.
				drawtext.DrawEmoji(run.Font, g.Glyph, entry.chars.Extents[g.Glyph],
					td.FontSize, td.X, td.Y, g.XAdvance, c)
			}
		}
	}
}

// drawGlyph rasterizes one glyph outline under the given transform.
func (c *Canvas) drawGlyph(e *fontEntry, gid backend.GID, mat matrix.Transform, area image.Rectangle) {
	outline := e.glyphOutline(gid)
	if outline == nil || outline.Empty() {
		return
	}
	path := transformPath(outline, mat)
	if op := c.st.textPaint; op&(backend.FillEvenOdd|backend.FillNonZero) != 0 {
		if mask := rasterize(path, area, op&backend.FillEvenOdd != 0); mask != nil {
			compose(c.dst, mask.Bounds(), mask, c.st.fill.shader(), c.st.fill.alpha, c.st.clip, c.st.blend)
		}
	}
	if c.st.textPaint&backend.Stroke != 0 {
		if o := strokeOutline(path, c.strokeWidth(), c.scaledDashes(), c.st.dashOffset*c.scale(), c.st.strokeOpts); o != nil {
			if mask := rasterize(o, area, false); mask != nil {
				compose(c.dst, mask.Bounds(), mask, c.st.stroke.shader(), c.st.stroke.alpha, c.st.clip, c.st.blend)
			}
		}
	}
}

// glyphOutline returns the glyph's path in font units, y-up, caching it.
func (e *fontEntry) glyphOutline(gid backend.GID) *canvas.Path {
	if p, ok := e.outline[gid]; ok {
		return p
	}
	var p *canvas.Path
	data := e.face.GlyphData(fonts.GID(gid), 0, 0)
	if outline, ok := data.(fonts.GlyphOutline); ok {
		p = segmentsToPath(outline.Segments)
	}
	e.outline[gid] = p
	return p
}

// segmentsToPath converts font-unit segments into a path. The font's y axis
// grows upwards; the flip happens later, in the text matrix.
func segmentsToPath(segs []fonts.Segment) *canvas.Path {
	if len(segs) == 0 {
		return nil
	}
	p := &canvas.Path{}
	started := false
	for _, s := range segs {
		a := s.Args
		switch s.Op {
		case fonts.SegmentOpMoveTo:
			if started {
				p.Close()
			}
			p.MoveTo(float64(a[0].X), float64(a[0].Y))
			started = true
		case fonts.SegmentOpLineTo:
			if !started {
				p.MoveTo(float64(a[0].X), float64(a[0].Y))
				started = true
				continue
			}
			p.LineTo(float64(a[0].X), float64(a[0].Y))
		case fonts.SegmentOpQuadTo:
			if !started {
				p.MoveTo(float64(a[0].X), float64(a[0].Y))
				started = true
			}
			p.QuadTo(float64(a[0].X), float64(a[0].Y), float64(a[1].X), float64(a[1].Y))
		case fonts.SegmentOpCubeTo:
			if !started {
				p.MoveTo(float64(a[0].X), float64(a[0].Y))
				started = true
			}
			p.CubeTo(float64(a[0].X), float64(a[0].Y), float64(a[1].X), float64(a[1].Y), float64(a[2].X), float64(a[2].Y))
		}
	}
	if started {
		p.Close()
	}
	return p
}

// transformPath maps a path through an affine transform, which is exact for
// the Béziers a glyph outline is made of.
//
// The copy is not optional. Path.Transform rewrites its receiver in place, and
// the outline it is handed comes out of the glyph cache: transforming that one
// would move the cached glyph itself, so the second "i" of "italic" would be
// drawn wherever the first one had been placed, plus the first placement
// again — off the page, in practice, leaving a gap.
func transformPath(p *canvas.Path, m matrix.Transform) *canvas.Path {
	return p.Copy().Transform(canvas.Matrix{
		{float64(m.A), float64(m.C), float64(m.E)},
		{float64(m.B), float64(m.D), float64(m.F)},
	})
}

func sqrt(v float64) float64 { return math.Sqrt(v) }
