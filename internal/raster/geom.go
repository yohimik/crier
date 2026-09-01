package raster

import (
	"image"
	"math"

	"github.com/benoitkugler/webrender/backend"
	"github.com/tdewolff/canvas"
	"golang.org/x/image/vector"
)

// flatTolerance is the largest deviation, in device pixels, a flattened Bézier
// may have from the curve it replaces. A tenth of a pixel is below what the
// antialiasing can show.
const flatTolerance = 0.1

// geometry seam
//
// Everything in this file is the one place the raster backend depends on an
// outside geometry library. tdewolff/canvas supplies the two operations
// x/image/vector has no answer for — stroking a path into its outline, and
// dashing one — plus the even-odd to non-zero conversion; x/image/vector
// supplies the scanline rasterizer. Keeping both behind these four functions is
// what makes swapping either of them a local change.

// rasterize turns a device-space path into an alpha coverage mask.
//
// The mask covers only the path's bounding box intersected with clipRect, so a
// small shape on a large page costs a small buffer. A nil result means the
// path covers nothing.
func rasterize(p *canvas.Path, clipRect image.Rectangle, evenOdd bool) *image.Alpha {
	if p == nil || p.Empty() {
		return nil
	}
	if evenOdd {
		p = settle(p)
		if p == nil || p.Empty() {
			return nil
		}
	}
	rect := pathRect(p).Intersect(clipRect)
	if rect.Empty() {
		return nil
	}
	ras := vector.NewRasterizer(rect.Dx(), rect.Dy())
	feed(ras, p, float32(-rect.Min.X), float32(-rect.Min.Y))
	mask := image.NewAlpha(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	ras.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
	// PixOffset is relative to Rect.Min, so moving the rectangle after the fact
	// puts the mask where it belongs without touching a pixel.
	mask.Rect = rect
	return mask
}

// pathRect is the pixel rectangle a path can touch, rounded outwards.
func pathRect(p *canvas.Path) image.Rectangle {
	b := p.Bounds()
	if !finite(b.X0) || !finite(b.Y0) || !finite(b.X1) || !finite(b.Y1) {
		return image.Rectangle{}
	}
	return image.Rect(
		int(math.Floor(b.X0)), int(math.Floor(b.Y0)),
		int(math.Ceil(b.X1))+1, int(math.Ceil(b.Y1))+1,
	)
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// feed walks a path into the rasterizer, flattening the curves and shifting
// the coordinates so the rasterizer's own space starts at its bounding box.
func feed(ras *vector.Rasterizer, p *canvas.Path, dx, dy float32) {
	flat := p.Flatten(flatTolerance)
	s := flat.Scanner()
	var startX, startY float32
	open := false
	for s.Scan() {
		switch s.Cmd() {
		case canvas.MoveToCmd:
			if open {
				ras.ClosePath()
			}
			end := s.End()
			startX, startY = float32(end.X)+dx, float32(end.Y)+dy
			ras.MoveTo(startX, startY)
			open = true
		case canvas.LineToCmd:
			end := s.End()
			ras.LineTo(float32(end.X)+dx, float32(end.Y)+dy)
		case canvas.CloseCmd:
			end := s.End()
			ras.LineTo(float32(end.X)+dx, float32(end.Y)+dy)
			ras.ClosePath()
			open = false
			ras.MoveTo(startX, startY)
		}
	}
	if open {
		ras.ClosePath()
	}
}

// settle rewrites an even-odd path as one that fills the same area under the
// non-zero rule, which is the only rule the scanline rasterizer knows.
//
// The conversion is a boolean operation over the subpaths, and a degenerate
// path can make it give up. Rather than take the whole render down, a failure
// falls back to the original path: a wrong fill rule on one shape is a much
// smaller loss than a blank page.
func settle(p *canvas.Path) (out *canvas.Path) {
	out = p
	defer func() {
		if r := recover(); r != nil {
			out = p
		}
	}()
	settled := p.Settle(canvas.EvenOdd)
	if settled == nil || settled.Empty() {
		return p
	}
	return settled
}

// strokeOutline converts a path into the outline of its stroke, ready to be
// filled with the non-zero rule.
//
// width is already in device units. Dashes are applied first, because a dash
// pattern describes the path being stroked and not the outline of the stroke.
func strokeOutline(p *canvas.Path, width float64, dashes []float64, dashOffset float64, opts backend.StrokeOptions) (out *canvas.Path) {
	if p == nil || p.Empty() {
		return nil
	}
	if width <= 0 {
		return nil
	}
	if len(dashes) > 0 {
		p = p.Dash(dashOffset, dashes...)
		if p == nil || p.Empty() {
			return nil
		}
	}
	out = nil
	defer func() {
		if r := recover(); r != nil {
			out = nil
		}
	}()
	return p.Stroke(width, capper(opts.LineCap), joiner(opts), flatTolerance)
}

func capper(c backend.StrokeCapMode) canvas.Capper {
	switch c {
	case backend.RoundCap:
		return canvas.RoundCap
	case backend.SquareCap:
		return canvas.SquareCap
	default:
		return canvas.ButtCap
	}
}

func joiner(o backend.StrokeOptions) canvas.Joiner {
	switch o.LineJoin {
	case backend.Round:
		return canvas.RoundJoin
	case backend.Bevel:
		return canvas.BevelJoin
	default:
		limit := float64(o.MiterLimit)
		if limit <= 0 {
			limit = 4
		}
		return canvas.MiterJoiner{GapJoiner: canvas.BevelJoin, Limit: limit}
	}
}

// deviceRect recognises the common case of an axis-aligned rectangle that
// lands on whole pixels, which is what a page clip and most overflow clips
// are. Such a clip needs no mask at all, only a rectangle.
func deviceRect(p *canvas.Path) (image.Rectangle, bool) {
	if p == nil {
		return image.Rectangle{}, false
	}
	pts := rectPoints(p)
	if pts == nil {
		return image.Rectangle{}, false
	}
	xs := map[float64]bool{}
	ys := map[float64]bool{}
	for _, pt := range pts {
		if !nearInt(pt.X) || !nearInt(pt.Y) {
			return image.Rectangle{}, false
		}
		xs[math.Round(pt.X)] = true
		ys[math.Round(pt.Y)] = true
	}
	if len(xs) != 2 || len(ys) != 2 {
		return image.Rectangle{}, false
	}
	b := p.Bounds()
	return image.Rect(
		int(math.Round(b.X0)), int(math.Round(b.Y0)),
		int(math.Round(b.X1)), int(math.Round(b.Y1)),
	), true
}

// rectPoints returns the four corners of a path that is a single closed
// quadrilateral of straight lines, and nil for anything else.
func rectPoints(p *canvas.Path) []canvas.Point {
	var pts []canvas.Point
	s := p.Scanner()
	moves := 0
	for s.Scan() {
		switch s.Cmd() {
		case canvas.MoveToCmd:
			moves++
			if moves > 1 {
				return nil
			}
			pts = append(pts, s.End())
		case canvas.LineToCmd, canvas.CloseCmd:
			pts = append(pts, s.End())
		default:
			return nil
		}
	}
	// A rectangle is 5 points when it is explicitly closed back onto its start,
	// and 4 when the close is implicit.
	if len(pts) == 5 && pts[0] == pts[4] {
		pts = pts[:4]
	}
	if len(pts) != 4 {
		return nil
	}
	return pts
}

func nearInt(v float64) bool {
	if !finite(v) {
		return false
	}
	return math.Abs(v-math.Round(v)) < 1e-4
}
