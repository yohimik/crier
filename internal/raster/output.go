package raster

import (
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"

	xdraw "golang.org/x/image/draw"
)

// EncodePNG writes the image as PNG, keeping the alpha channel.
func EncodePNG(w io.Writer, img image.Image) error {
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	return enc.Encode(w, img)
}

// EncodeJPEG writes the image as JPEG, flattening it onto bg first.
//
// JPEG has no alpha, and a page rendered with a transparent background would
// otherwise come out with black where nothing was drawn. Instagram only takes
// JPEG, so this is the path every Instagram post goes through.
func EncodeJPEG(w io.Writer, img image.Image, bg color.Color, quality int) error {
	if quality <= 0 || quality > 100 {
		quality = jpeg.DefaultQuality
	}
	return jpeg.Encode(w, Flatten(img, bg), &jpeg.Options{Quality: quality})
}

// Flatten composites an image onto a solid background and returns an opaque
// result. An image that is already opaque is returned unchanged.
func Flatten(img image.Image, bg color.Color) image.Image {
	if bg == nil {
		bg = color.White
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, image.NewUniform(bg), image.Point{}, draw.Src)
	draw.Draw(out, b, img, b.Min, draw.Over)
	return out
}

// Downsample scales an image down by an integer factor, which is how
// supersampled rendering is brought back to its nominal size.
func Downsample(img *image.RGBA, factor int) *image.RGBA {
	if factor <= 1 {
		return img
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx()/factor, b.Dy()/factor))
	if out.Bounds().Empty() {
		return img
	}
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, b, xdraw.Src, nil)
	return out
}
