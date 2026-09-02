// Package render turns HTML into images: it drives webrender's layout and
// paints the result through the raster backend.
//
// It is the only place that knows webrender's API, which is what makes the
// pinned pre-1.0 dependency a local risk rather than one spread across the
// program.
package render

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"

	"github.com/benoitkugler/webrender/html/document"
	"github.com/benoitkugler/webrender/html/tree"
	"github.com/benoitkugler/webrender/utils"
	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/raster"
)

// MaxPixels caps a rendering, so a mistyped size fails with a message instead
// of exhausting memory. It is generous: 10000x10000 at scale 1.
const MaxPixels = 100 << 20

// Options describes one rendering.
type Options struct {
	// HTML is the document to lay out.
	HTML string
	// BaseURL resolves the document's relative references.
	BaseURL string
	// Width and Height are the page size in CSS pixels. When both are zero the
	// document's own page rule decides. They set the page size only: the page
	// margin stays the document's to choose, and defaults to zero.
	Width, Height int
	// Scale is the device pixel ratio: output pixels per CSS pixel.
	Scale float64
	// SuperSample renders at this multiple of Scale and shrinks the result. The
	// vector antialiasing is already good, so this is a knob rather than a
	// default.
	SuperSample int
	// MediaType is the CSS media type. Empty means "screen" — note that
	// webrender's own default is "print".
	MediaType string
	// Background is painted before anything else. A zero value leaves the page
	// transparent.
	Background color.Color
	// ExtraCSS is appended to the document as a stylesheet, before the page
	// size rule.
	ExtraCSS []string
	// Fonts is the font configuration. Required.
	Fonts *Fonts
	// Logger receives the render's own records and the backend's warnings.
	Logger zerolog.Logger
}

// Result is one rendered page.
type Result struct {
	Image *image.RGBA
	// Pages is how many pages the document laid out into. More than one means
	// the content overflowed the page box.
	Pages int
}

// Render lays the document out and paints every page.
func Render(ctx context.Context, o Options) ([]*image.RGBA, error) {
	if o.Fonts == nil {
		return nil, fmt.Errorf("render: no font configuration")
	}
	scale := o.Scale
	if scale <= 0 {
		scale = 1
	}
	super := o.SuperSample
	if super < 1 {
		super = 1
	}
	total := scale * float64(super)

	html, err := tree.NewHTML(utils.InputString(o.compose()), o.BaseURL, o.Fonts.Fetcher, o.mediaType())
	if err != nil {
		return nil, fmt.Errorf("parsing document: %w", err)
	}

	// The layout engine panics rather than erroring on inputs it cannot
	// handle — a malformed language tag reached one such index once — and a
	// panic here would take the whole program down over one document. Turned
	// into an error, it fails the render and names itself.
	var doc document.Document
	if err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("the layout engine crashed on this document: %v", r)
			}
		}()
		doc = document.Render(html, nil, false, o.Fonts.Config)
		return nil
	}(); err != nil {
		return nil, err
	}
	if len(doc.Pages) == 0 {
		return nil, fmt.Errorf("the document laid out into no pages")
	}

	out := make([]*image.RGBA, 0, len(doc.Pages))
	for i, page := range doc.Pages {
		// webrender's own layout is not cancellable, so the context is checked
		// between pages and between frames rather than inside one.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		w := int(float64(page.Width)*total + 0.5)
		h := int(float64(page.Height)*total + 0.5)
		if w <= 0 || h <= 0 {
			return nil, fmt.Errorf("page %d has no size (%gx%g CSS px)", i+1, page.Width, page.Height)
		}
		if w*h > MaxPixels {
			return nil, fmt.Errorf("page %d would be %dx%d pixels, which is more than crier will allocate", i+1, w, h)
		}

		img := image.NewRGBA(image.Rect(0, 0, w, h))
		if o.Background != nil {
			fill(img, o.Background)
		}
		canvas := raster.NewCanvas(img, o.Logger)
		page.Paint(canvas, o.Fonts.Config, 0, 0, float32(total), true)

		if super > 1 {
			img = raster.Downsample(img, super)
		}
		out = append(out, img)
	}
	o.Logger.Debug().Int("pages", len(out)).Float64("scale", total).Msg("rendered document")
	return out, nil
}

// RenderOne renders a document that must produce exactly one page.
//
// Anything crier publishes is one image, so more than one page means the
// content did not fit and the operator has to know rather than get the first
// page silently.
func RenderOne(ctx context.Context, o Options) (*image.RGBA, error) {
	pages, err := Render(ctx, o)
	if err != nil {
		return nil, err
	}
	if len(pages) != 1 {
		return nil, fmt.Errorf("the document laid out into %d pages; it has to fit on one "+
			"(reduce the content, or raise render.height)", len(pages))
	}
	return pages[0], nil
}

func (o Options) mediaType() string {
	if strings.TrimSpace(o.MediaType) == "" {
		return "screen"
	}
	return o.MediaType
}

// compose assembles the document crier actually lays out: a default page
// margin, the caller's HTML, the extra stylesheets, then the page size.
//
// The two halves of the page rule sit on opposite sides of the document, and
// which side each one lands on is the whole of the policy.
//
// The size goes last. It is the one declaration crier insists on, and putting
// it after the document's own styles is what makes --render-width win over a
// page rule the template happens to carry.
//
// The margin goes first, which makes it a default rather than a decree. A
// template that says nothing gets an edge-to-edge page, because that is what a
// social image is. A template that asks for a page margin gets one, and that
// margin is the only place a page's margin boxes have to draw in: the running
// header and footer of a paginated document live there.
func (o Options) compose() string {
	var b strings.Builder
	b.WriteString("<style>@page { margin: 0 }</style>\n")
	b.WriteString(o.HTML)
	if o.Fonts != nil && o.Fonts.ExtraCSS != "" {
		b.WriteString("\n<style>")
		b.WriteString(o.Fonts.ExtraCSS)
		b.WriteString("</style>")
	}
	for _, css := range o.ExtraCSS {
		if strings.TrimSpace(css) == "" {
			continue
		}
		b.WriteString("\n<style>")
		b.WriteString(css)
		b.WriteString("</style>")
	}
	if o.Width > 0 && o.Height > 0 {
		fmt.Fprintf(&b, "\n<style>@page { size: %dpx %dpx }</style>", o.Width, o.Height)
	}
	return b.String()
}

// chan8 narrows one channel of a color.Color, whose components are 16 bit
// values scaled to [0,0xffff], into the 8 bit one an RGBA image holds.
func chan8(v uint32) uint8 {
	return uint8((v >> 8) & 0xff) //nolint:gosec // masked to eight bits
}

// fill paints a solid colour over the whole image.
func fill(img *image.RGBA, c color.Color) {
	r, g, b, a := c.RGBA()
	px := [4]uint8{chan8(r), chan8(g), chan8(b), chan8(a)}
	pix := img.Pix
	for i := 0; i+4 <= len(pix); i += 4 {
		copy(pix[i:i+4], px[:])
	}
}
