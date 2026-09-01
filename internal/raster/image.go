package raster

import (
	"bytes"
	"image"
	"image/color"
	"io"
	"strings"

	// Decoders registered for image.Decode's sniffing fallback.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/matrix"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
	_ "golang.org/x/image/webp" // webp is not in the standard library
)

// MaxImagePixels bounds a decoded image, so a hostile or mistaken resource
// cannot make the renderer allocate without limit.
const MaxImagePixels = 64 << 20

// MaxImageBytes bounds an encoded image resource.
//
// It is a separate number from MaxImagePixels because they measure different
// things, and using the pixel budget as a byte cap silently truncated any
// resource over 64MB into an undecodable prefix. 64MB of compressed image is
// far more than a card needs and far less than a machine minds.
const MaxImageBytes = 64 << 20

// DrawRasterImage draws an image into the rectangle (0,0)-(width,height) of
// the current user space.
//
// That rectangle is what webrender means by "at the current point with these
// dimensions": the PDF backend places the image the same way, with the y flip
// its own space needs and this one does not.
func (c *Canvas) DrawRasterImage(img backend.RasterImage, width, height backend.Fl) {
	if width <= 0 || height <= 0 {
		return
	}
	src := c.decode(img)
	if src == nil {
		return
	}
	sr := src.Bounds()
	if sr.Empty() {
		return
	}

	// src pixel -> user space -> device space.
	m := matrix.Mul(c.st.ctm, matrix.Scaling(width/backend.Fl(sr.Dx()), height/backend.Fl(sr.Dy())))
	// The source rectangle may not start at the origin, so undo its offset.
	m = matrix.Mul(m, matrix.Translation(backend.Fl(-sr.Min.X), backend.Fl(-sr.Min.Y)))
	aff := f64.Aff3{
		float64(m.A), float64(m.C), float64(m.E),
		float64(m.B), float64(m.D), float64(m.F),
	}

	area := c.drawArea()
	if area.Empty() {
		return
	}

	scaler := xdraw.Interpolator(xdraw.CatmullRom)
	if img.Rendering == "pixelated" || img.Rendering == "crisp-edges" {
		scaler = xdraw.NearestNeighbor
	}

	// The simple path writes straight into the page. It is only correct when
	// nothing else has to be applied on top: a constant alpha or a blend mode
	// has to go through a layer, because x/image/draw knows neither.
	if c.st.fill.alpha >= 1 && c.st.blend == "" {
		opts := &xdraw.Options{}
		if !c.st.clip.isRect() {
			opts.DstMask = c.st.clip.mask
		}
		dst := c.dst.SubImage(area).(*image.RGBA)
		scaler.Transform(dst, aff, src, sr, xdraw.Over, opts)
		return
	}

	layer := image.NewRGBA(area)
	scaler.Transform(layer, aff, src, sr, xdraw.Src, nil)
	compose(c.dst, area, nil, &layerShader{layer: layer}, c.st.fill.alpha, c.st.clip, c.st.blend)
}

// decode turns an encoded resource into an image, caching it by the id
// webrender assigns, because a repeated background image is the same bytes
// every time.
func (c *Canvas) decode(img backend.RasterImage) image.Image {
	if cached, ok := c.sh.images[img.ID]; ok {
		return cached
	}
	// One byte past the cap, so a resource that reaches it is recognised as
	// truncated here rather than as a corrupt image further down.
	data, err := io.ReadAll(io.LimitReader(img.Content, MaxImageBytes+1))
	if err != nil {
		c.sh.warnOnce("imgread", "could not read an image resource: "+err.Error())
		c.sh.images[img.ID] = nil
		return nil
	}
	if len(data) > MaxImageBytes {
		c.sh.log.Warn().Int("bytes", len(data)).Str("mime", img.MimeType).
			Msg("image resource is too large to read; it will not be drawn")
		c.sh.images[img.ID] = nil
		return nil
	}

	// The size is checked from the header, before anything is decoded.
	//
	// Checking it afterwards is checking it too late: a two kilobyte PNG can
	// declare itself 30000 by 30000, and decoding that allocates three and a
	// half gigabytes before there is anything to measure. A document crier
	// renders can name any URL, so the header is not something to trust.
	if cfg, _, cfgErr := image.DecodeConfig(bytes.NewReader(data)); cfgErr == nil {
		if pixels := int64(cfg.Width) * int64(cfg.Height); pixels > MaxImagePixels {
			c.sh.log.Warn().Int("width", cfg.Width).Int("height", cfg.Height).
				Msg("image is too large to draw")
			c.sh.images[img.ID] = nil
			return nil
		}
	}

	decoded, err := decodeImage(data, img.MimeType)
	if err != nil {
		c.sh.log.Warn().Err(err).Str("mime", img.MimeType).Msg("could not decode an image; it will not be drawn")
		c.sh.images[img.ID] = nil
		return nil
	}
	// And again from what actually came out, for a format whose header the
	// standard library cannot read on its own.
	if b := decoded.Bounds(); int64(b.Dx())*int64(b.Dy()) > MaxImagePixels {
		c.sh.log.Warn().Int("width", b.Dx()).Int("height", b.Dy()).Msg("image is too large to draw")
		c.sh.images[img.ID] = nil
		return nil
	}
	c.sh.images[img.ID] = decoded
	return decoded
}

// decodeImage decodes by MIME type where one is given, and falls back to
// sniffing, because a data: URL or a mislabelled server is common enough that
// trusting the header alone loses images.
func decodeImage(data []byte, mime string) (image.Image, error) {
	switch normaliseMIME(mime) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		// The registered decoders sniff anyway, and sniffing is right more
		// often than a Content-Type is.
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func normaliseMIME(m string) string {
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = m[:i]
	}
	return strings.ToLower(strings.TrimSpace(m))
}

// layerShader reads a premultiplied layer back as straight colours, which is
// what the compositor wants.
type layerShader struct{ layer *image.RGBA }

func (s *layerShader) colorAt(x, y int) (uint8, uint8, uint8, uint8) {
	if !(image.Point{X: x, Y: y}).In(s.layer.Bounds()) {
		return 0, 0, 0, 0
	}
	i := s.layer.PixOffset(x, y)
	p := s.layer.Pix[i : i+4 : i+4]
	a := p[3]
	if a == 0 {
		return 0, 0, 0, 0
	}
	if a == 255 {
		return p[0], p[1], p[2], 255
	}
	return unpremul(p[0], a), unpremul(p[1], a), unpremul(p[2], a), a
}

func unpremul(v, a uint8) uint8 {
	n := uint32(v) * 255 / uint32(a)
	if n > 255 {
		n = 255
	}
	return uint8(n)
}

var _ color.Color = color.RGBA{}
