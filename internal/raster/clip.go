package raster

import "image"

// clip is the region drawing is allowed to touch.
//
// It is immutable and shared by pointer between saved states, so pushing and
// popping the graphic stack costs a pointer assignment. Narrowing it always
// builds a new one, which is what makes that safe.
//
// A nil mask means the clip is exactly rect, with hard edges. That is the
// common case — the page clip and most overflow clips are whole-pixel
// rectangles — and it costs no memory and no per-pixel lookup.
type clip struct {
	rect image.Rectangle
	mask *image.Alpha // nil: opaque throughout rect
}

// newRectClip is the unclipped state for a canvas of the given size.
func newRectClip(r image.Rectangle) *clip { return &clip{rect: r} }

// alphaAt is the clip's coverage at a device pixel, 0 to 255.
func (c *clip) alphaAt(x, y int) uint8 {
	if c == nil {
		return 255
	}
	if !(image.Point{X: x, Y: y}).In(c.rect) {
		return 0
	}
	if c.mask == nil {
		return 255
	}
	return c.mask.AlphaAt(x, y).A
}

// bounds is the rectangle outside which the clip is certainly zero.
func (c *clip) bounds() image.Rectangle {
	if c == nil {
		return image.Rectangle{}
	}
	return c.rect
}

// intersectRect narrows the clip to a rectangle.
func (c *clip) intersectRect(r image.Rectangle) *clip {
	if c == nil {
		return &clip{rect: r}
	}
	return &clip{rect: c.rect.Intersect(r), mask: c.mask}
}

// intersectMask narrows the clip by an alpha mask, multiplying the two
// coverages. A nil mask clips everything away.
func (c *clip) intersectMask(m *image.Alpha) *clip {
	if m == nil {
		return &clip{}
	}
	base := m.Bounds()
	if c != nil {
		base = base.Intersect(c.rect)
	}
	if base.Empty() {
		return &clip{}
	}
	out := image.NewAlpha(base)
	for y := base.Min.Y; y < base.Max.Y; y++ {
		for x := base.Min.X; x < base.Max.X; x++ {
			a := uint32(m.AlphaAt(x, y).A)
			if a != 0 {
				a = a * uint32(c.alphaAt(x, y)) / 255
			}
			out.SetAlpha(x, y, colorAlpha(uint8(a)))
		}
	}
	return &clip{rect: base, mask: out}
}

// isRect reports whether the clip has no mask, so a caller can take a faster
// path.
func (c *clip) isRect() bool { return c == nil || c.mask == nil }
