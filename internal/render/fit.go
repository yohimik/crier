package render

import (
	"image"
	"image/color"
	"image/draw"

	"fmt"
	"strconv"

	xdraw "golang.org/x/image/draw"

	"github.com/yohimik/crier/internal/config"
)

// FitGeometry is where the scaled source lands inside the frame.
//
// It is computed on its own so the arithmetic can be tested without drawing
// anything, and so the ffmpeg filter for a clip and the resampler for a still
// are working from the same numbers.
type FitGeometry struct {
	// Dst is the frame the result occupies: (0,0)-(width,height).
	Dst image.Rectangle
	// Draw is where the scaled source is placed inside Dst. For cover it is
	// larger than Dst on one axis and is clipped; for contain it is smaller
	// and the remainder is background.
	Draw image.Rectangle
	// Letterboxed says the background will show, which is the only case that
	// needs a fill.
	Letterboxed bool
}

// FitRect works out the geometry for one mode.
//
// The rounding rule is worth stating: the scaled size is rounded to whole
// pixels and then centred with integer division, so an odd remainder puts the
// extra pixel on the right or the bottom. That matches what ffmpeg's pad
// filter does with (ow-iw)/2, which is what keeps a clip and a still of the
// same card looking the same.
func FitRect(srcW, srcH, dstW, dstH int, fit config.Fit) FitGeometry {
	dst := image.Rect(0, 0, dstW, dstH)
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return FitGeometry{Dst: dst, Draw: dst}
	}

	switch fit {
	case config.FitStretch:
		return FitGeometry{Dst: dst, Draw: dst}

	case config.FitCover, config.FitContain:
		// One scale factor for both axes: the larger of the two ratios fills
		// the frame, the smaller fits inside it.
		sx := float64(dstW) / float64(srcW)
		sy := float64(dstH) / float64(srcH)
		scale := sx
		if fit == config.FitCover {
			if sy > scale {
				scale = sy
			}
		} else if sy < scale {
			scale = sy
		}

		w := int(float64(srcW)*scale + 0.5)
		h := int(float64(srcH)*scale + 0.5)
		// Rounding can leave a scaled edge a pixel short of the frame it was
		// meant to fill, which would show as a one-pixel seam. Cover promises
		// no background, so it is grown rather than trusted.
		if fit == config.FitCover {
			if w < dstW {
				w = dstW
			}
			if h < dstH {
				h = dstH
			}
		}
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}

		x := (dstW - w) / 2
		y := (dstH - h) / 2
		return FitGeometry{
			Dst:         dst,
			Draw:        image.Rect(x, y, x+w, y+h),
			Letterboxed: fit == config.FitContain && (w < dstW || h < dstH),
		}

	default:
		return FitGeometry{Dst: dst, Draw: dst}
	}
}

// FitImage resamples an image into a frame.
//
// CatmullRom because this is the last resampling the picture gets before
// somebody looks at it: a story cropped out of a square card is a big
// reduction, and a cheaper kernel shows it in the text.
//
// The background is painted first and always, not only when letterboxing:
// a PNG with transparency going to a platform that flattens onto its own
// colour would otherwise arrive with that platform's idea of white.
func FitImage(src *image.RGBA, dstW, dstH int, fit config.Fit, background color.Color) *image.RGBA {
	if src == nil {
		return nil
	}
	b := src.Bounds()
	geom := FitRect(b.Dx(), b.Dy(), dstW, dstH, fit)
	if geom.Dst.Empty() {
		return src
	}

	out := image.NewRGBA(geom.Dst)
	if background == nil {
		background = color.White
	}
	draw.Draw(out, out.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)

	// Over, so a source with alpha composites onto the background rather than
	// replacing it — which is what flattening means.
	xdraw.CatmullRom.Scale(out, geom.Draw, src, b, xdraw.Over, nil)
	return out
}

// FitFilter is the ffmpeg filter chain that gives a clip the same shape a
// still gets.
//
// It returns the expression rather than a -vf pair because ffmpeg honours only
// the last -vf it is given, and a GIF already spends one on its palette: the
// caller composes.
//
// The filters are ffmpeg's own idioms rather than anything clever, so the
// output matches what somebody would write by hand and what the documentation
// describes: increase-then-crop fills, decrease-then-pad letterboxes.
func FitFilter(dstW, dstH int, fit config.Fit, background string) string {
	if fit == config.FitNone || dstW <= 0 || dstH <= 0 {
		return ""
	}
	w, h := strconv.Itoa(dstW), strconv.Itoa(dstH)
	switch fit {
	case config.FitCover:
		return "scale=" + w + ":" + h + ":force_original_aspect_ratio=increase," +
			"crop=" + w + ":" + h
	case config.FitContain:
		return "scale=" + w + ":" + h + ":force_original_aspect_ratio=decrease," +
			"pad=" + w + ":" + h + ":(ow-iw)/2:(oh-ih)/2:" + ffmpegColor(background)
	case config.FitStretch:
		return "scale=" + w + ":" + h
	default:
		return ""
	}
}

// ffmpegColor spells a colour the way ffmpeg's pad filter wants it: 0xRRGGBB
// rather than #rrggbb, since # starts a comment in a filter graph read from a
// file and is awkward in a shell besides.
func ffmpegColor(hex string) string {
	c, err := config.ParseColor(hex)
	if err != nil {
		return "white"
	}
	return fmt.Sprintf("0x%02x%02x%02x", c.R, c.G, c.B)
}
