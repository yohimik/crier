package raster

import (
	"image"
	"image/color"
	"math"
)

// shader produces the straight (non-premultiplied) colour a paint has at a
// device pixel. A solid colour ignores the coordinates; a gradient and a
// pattern do not.
type shader interface {
	colorAt(x, y int) (r, g, b, a uint8)
}

// solidShader is a single colour.
type solidShader struct{ r, g, b, a uint8 }

func (s solidShader) colorAt(int, int) (uint8, uint8, uint8, uint8) { return s.r, s.g, s.b, s.a }

func colorAlpha(a uint8) color.Alpha { return color.Alpha{A: a} }

// compose is the one place pixels are written.
//
// dst is premultiplied. mask is the shape's antialiased coverage (nil means
// "the whole area"), cl narrows it further, alpha is the graphic state's
// constant alpha, and blend is the CSS mix-blend-mode name.
//
// The arithmetic is the CSS compositing formula, in float32:
//
//	Cr = (1-ab)*Cs + ab*B(Cb,Cs)     the blended source colour
//	Co = as*Cr + (1-as)*Cb*ab        premultiplied result
//	ao = as + ab*(1-as)
func compose(dst *image.RGBA, area image.Rectangle, mask *image.Alpha, sh shader, alpha float32, cl *clip, blend string) {
	if alpha <= 0 {
		return
	}
	if alpha > 1 {
		alpha = 1
	}
	r := area.Intersect(dst.Bounds())
	if mask != nil {
		r = r.Intersect(mask.Bounds())
	}
	r = r.Intersect(cl.bounds())
	if r.Empty() {
		return
	}
	blendFn := separableBlend(blend)

	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cov := uint32(255)
			if mask != nil {
				cov = uint32(mask.AlphaAt(x, y).A)
				if cov == 0 {
					continue
				}
			}
			if !cl.isRect() {
				cov = cov * uint32(cl.alphaAt(x, y)) / 255
				if cov == 0 {
					continue
				}
			}
			sr, sg, sb, sa := sh.colorAt(x, y)
			if sa == 0 {
				continue
			}
			as := float32(sa) / 255 * float32(cov) / 255 * alpha
			if as <= 0 {
				continue
			}
			i := dst.PixOffset(x, y)
			pix := dst.Pix[i : i+4 : i+4]
			ab := float32(pix[3]) / 255

			// Straight backdrop colour; a fully transparent backdrop has no
			// colour of its own, which is what makes the blend fall back to
			// the source.
			var cbr, cbg, cbb float32
			if ab > 0 {
				cbr = float32(pix[0]) / 255 / ab
				cbg = float32(pix[1]) / 255 / ab
				cbb = float32(pix[2]) / 255 / ab
			}
			csr, csg, csb := float32(sr)/255, float32(sg)/255, float32(sb)/255

			crr, crg, crb := csr, csg, csb
			if blendFn != nil && ab > 0 {
				crr = (1-ab)*csr + ab*blendFn(cbr, csr)
				crg = (1-ab)*csg + ab*blendFn(cbg, csg)
				crb = (1-ab)*csb + ab*blendFn(cbb, csb)
			}

			inv := 1 - as
			ao := as + ab*inv
			or := as*crr + float32(pix[0])/255*inv
			og := as*crg + float32(pix[1])/255*inv
			ob := as*crb + float32(pix[2])/255*inv

			pix[0] = clamp8(or)
			pix[1] = clamp8(og)
			pix[2] = clamp8(ob)
			pix[3] = clamp8(ao)
		}
	}
}

func clamp8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}

// separableBlend returns the per-channel blend function for a CSS blend mode,
// or nil for plain source-over.
//
// The four non-separable modes — hue, saturation, color, luminosity — mix the
// channels together and cannot be expressed this way; drawNonSeparable in
// canvas.go logs them once and falls back to normal.
func separableBlend(mode string) func(cb, cs float32) float32 {
	switch mode {
	case "", "normal", "source-over":
		return nil
	case "multiply":
		return func(cb, cs float32) float32 { return cb * cs }
	case "screen":
		return func(cb, cs float32) float32 { return cb + cs - cb*cs }
	case "overlay":
		return func(cb, cs float32) float32 { return hardLight(cs, cb) }
	case "darken":
		return func(cb, cs float32) float32 { return min32(cb, cs) }
	case "lighten":
		return func(cb, cs float32) float32 { return max32(cb, cs) }
	case "color-dodge":
		return colorDodge
	case "color-burn":
		return colorBurn
	case "hard-light":
		return func(cb, cs float32) float32 { return hardLight(cb, cs) }
	case "soft-light":
		return softLight
	case "difference":
		return func(cb, cs float32) float32 { return abs32(cb - cs) }
	case "exclusion":
		return func(cb, cs float32) float32 { return cb + cs - 2*cb*cs }
	default:
		return nil
	}
}

// isNonSeparableBlend reports the four modes that mix channels and are not
// supported.
func isNonSeparableBlend(mode string) bool {
	switch mode {
	case "hue", "saturation", "color", "luminosity":
		return true
	}
	return false
}

func hardLight(cb, cs float32) float32 {
	if cs <= 0.5 {
		return cb * 2 * cs
	}
	return screen(cb, 2*cs-1)
}

func screen(cb, cs float32) float32 { return cb + cs - cb*cs }

func colorDodge(cb, cs float32) float32 {
	switch {
	case cb <= 0:
		return 0
	case cs >= 1:
		return 1
	default:
		return min32(1, cb/(1-cs))
	}
}

func colorBurn(cb, cs float32) float32 {
	switch {
	case cb >= 1:
		return 1
	case cs <= 0:
		return 0
	default:
		return 1 - min32(1, (1-cb)/cs)
	}
}

func softLight(cb, cs float32) float32 {
	if cs <= 0.5 {
		return cb - (1-2*cs)*cb*(1-cb)
	}
	var d float32
	if cb <= 0.25 {
		d = ((16*cb-12)*cb + 4) * cb
	} else {
		d = float32(math.Sqrt(float64(cb)))
	}
	return cb + (2*cs-1)*(d-cb)
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func abs32(a float32) float32 {
	if a < 0 {
		return -a
	}
	return a
}
