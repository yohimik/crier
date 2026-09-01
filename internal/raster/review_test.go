package raster

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/benoitkugler/webrender/backend"
	"github.com/benoitkugler/webrender/matrix"
	"github.com/rs/zerolog"
)

// hugeHeaderPNG is a small file that claims to be enormous.
//
// It is 30000 by 30000 in the header — three and a half gigabytes once
// decoded — with a truncated image body, which is exactly the shape of a
// decompression bomb: cheap to send, ruinous to decode.
func hugeHeaderPNG(t *testing.T) []byte {
	t.Helper()
	// A real one-pixel PNG, with its IHDR width and height rewritten.
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	// IHDR starts at byte 8: length(4) type(4) then width(4) height(4). The
	// chunk's CRC covers the type and the data, so it has to be recomputed —
	// a real bomb is a valid file, which is the whole reason it gets as far as
	// the decoder.
	const at = 8 + 4 + 4
	for i, v := range []byte{0x00, 0x00, 0x75, 0x30} { // 30000
		data[at+i] = v
		data[at+4+i] = v
	}
	const ihdrLen = 13
	crcOver := data[8+4 : 8+4+4+ihdrLen] // the type and the data
	binary.BigEndian.PutUint32(data[8+4+4+ihdrLen:], crc32.ChecksumIEEE(crcOver))
	return data
}

// TestOversizedImageIsRefusedBeforeItIsDecoded is the regression test.
//
// The pixel budget was checked after decoding, so a two kilobyte PNG claiming
// 30000 by 30000 allocated three and a half gigabytes first — and a document
// crier renders can name any URL. The same constant was also used as a byte
// cap on the reader, silently truncating any resource over 64MB into something
// undecodable.
func TestOversizedImageIsRefusedBeforeItIsDecoded(t *testing.T) {
	c, _ := newTestCanvas(20, 20)
	data := hugeHeaderPNG(t)

	// A guard on the fixture: this really does declare more pixels than the cap.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the fixture is not a readable PNG header: %v", err)
	}
	if int64(cfg.Width)*int64(cfg.Height) <= MaxImagePixels {
		t.Fatalf("the fixture declares %dx%d, which is within the cap", cfg.Width, cfg.Height)
	}

	got := c.decode(backend.RasterImage{
		ID: 1, MimeType: "image/png", Content: bytes.NewReader(data),
	})
	if got != nil {
		t.Error("an image over the pixel budget was decoded and kept")
	}

	// And a resource just under the byte cap still decodes, so the reader's
	// bound is a bound rather than a truncation.
	var small bytes.Buffer
	if err := png.Encode(&small, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	if got := c.decode(backend.RasterImage{
		ID: 9, MimeType: "image/png", Content: bytes.NewReader(small.Bytes()),
	}); got == nil {
		t.Error("an ordinary image stopped decoding")
	}
}

// TestOversizedResourceIsRefused covers the reader's own bound: a resource
// past the byte cap used to be truncated into a corrupt image rather than
// refused.
func TestOversizedResourceIsRefused(t *testing.T) {
	c, _ := newTestCanvas(20, 20)
	huge := bytes.NewReader(make([]byte, MaxImageBytes+1))
	if got := c.decode(backend.RasterImage{ID: 2, MimeType: "image/png", Content: huge}); got != nil {
		t.Error("a resource past the byte cap should not be drawn")
	}
}

// TestGroupCoversItsBoxRatherThanThePage is the memory fix: a group used to
// allocate a full page each — 33MB at 1080 square and scale 2, per opacity
// stack, per frame of a video.
func TestGroupCoversItsBoxRatherThanThePage(t *testing.T) {
	c, _ := newTestCanvas(200, 200)
	group := c.NewGroup(10, 20, 30, 40).(*Canvas)

	b := group.dst.Bounds()
	if b == c.dst.Bounds() {
		t.Fatal("the group is still the size of the page")
	}
	// The box, grown by the antialiasing pad, in device coordinates.
	if b.Min.X > 10-groupPad || b.Min.Y > 20-groupPad {
		t.Errorf("the group starts at %v, inside the box it was given", b.Min)
	}
	if b.Max.X < 40+groupPad || b.Max.Y < 60+groupPad {
		t.Errorf("the group ends at %v, inside the box it was given", b.Max)
	}
	// Device coordinates are kept, so nothing downstream has to know.
	if b.Min.X < 0 || b.Min.Y < 0 {
		t.Errorf("the group escaped the page: %v", b)
	}

	// What is drawn into it still lands where it would have on the page.
	group.SetColorRgba(rgba(0, 1, 0, 1), false)
	group.Rectangle(12, 22, 10, 10)
	group.Paint(backend.FillNonZero)
	if group.dst.RGBAAt(15, 25).A == 0 {
		t.Error("a fill inside the box did not land in the layer")
	}
}

// TestGroupFallsBackToThePageWhenTheBoxIsUnusable: a box that is degenerate,
// off the page, or not finite costs memory rather than correctness.
func TestGroupFallsBackToThePageWhenTheBoxIsUnusable(t *testing.T) {
	c, _ := newTestCanvas(50, 50)
	for _, tt := range []struct {
		name                string
		x, y, width, height backend.Fl
	}{
		{"zero width", 0, 0, 0, 10},
		{"negative height", 0, 0, 10, -1},
		{"entirely off the page", 900, 900, 10, 10},
	} {
		group := c.NewGroup(tt.x, tt.y, tt.width, tt.height).(*Canvas)
		if group.dst.Bounds() != c.dst.Bounds() {
			t.Errorf("%s: bounds = %v, want the whole page", tt.name, group.dst.Bounds())
		}
	}
}

// TestGroupUnderATransform: the box is in user space, so it goes through the
// transform before it means anything in pixels.
func TestGroupUnderATransform(t *testing.T) {
	c, _ := newTestCanvas(200, 200)
	c.Transform(matrix.Translation(100, 50))
	group := c.NewGroup(0, 0, 20, 20).(*Canvas)

	b := group.dst.Bounds()
	if b.Min.X > 100-groupPad || b.Max.X < 120+groupPad {
		t.Errorf("bounds = %v, want the box translated to x=100..120", b)
	}
	if b.Min.Y > 50-groupPad || b.Max.Y < 70+groupPad {
		t.Errorf("bounds = %v, want the box translated to y=50..70", b)
	}
}

// TestBowTieIsNotARectangle is the clip fix: four corners visited in a
// self-crossing order fill two triangles, not the rectangle their bounding box
// describes.
func TestBowTieIsNotARectangle(t *testing.T) {
	c, _ := newTestCanvas(20, 20)

	// A proper rectangle is recognised.
	c.MoveTo(2, 2)
	c.LineTo(10, 2)
	c.LineTo(10, 8)
	c.LineTo(2, 8)
	c.ClosePath()
	if _, ok := deviceRect(c.path); !ok {
		t.Error("a plain rectangle should be recognised")
	}

	// The same four corners, crossed.
	c.path, c.hasPoint = nil, false
	c.MoveTo(2, 2)
	c.LineTo(10, 8)
	c.LineTo(10, 2)
	c.LineTo(2, 8)
	c.ClosePath()
	if _, ok := deviceRect(c.path); ok {
		t.Error("a bow-tie was taken for a rectangle; its fill is two triangles")
	}
}

// TestUnknownBlendModeWarns: silently drawing normally is how somebody spends
// an hour wondering why their blend mode does nothing.
func TestUnknownBlendModeWarns(t *testing.T) {
	var logs bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	c := NewCanvas(img, zerolog.New(&logs).Level(zerolog.DebugLevel))

	c.SetBlendingMode("plusserious")
	if c.st.blend != "" {
		t.Errorf("blend = %q, want it dropped", c.st.blend)
	}
	if !strings.Contains(logs.String(), "plusserious") {
		t.Errorf("nothing warned about it: %s", logs.String())
	}

	// The ones it does know are kept, and warn about nothing.
	logs.Reset()
	for _, mode := range []string{"", "normal", "multiply", "screen", "overlay", "difference"} {
		c.SetBlendingMode(mode)
		if mode != "" && mode != "normal" && c.st.blend != mode {
			t.Errorf("%s was dropped", mode)
		}
	}
	if strings.Contains(logs.String(), "not one this renderer knows") {
		t.Errorf("a supported mode warned: %s", logs.String())
	}
}

// BenchmarkGroupAllocation is the measurement behind the group fix: a caption
// overlay on a story-sized page.
func BenchmarkGroupAllocation(b *testing.B) {
	c := NewCanvas(image.NewRGBA(image.Rect(0, 0, 1080, 1920)), zerolog.Nop())
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g := c.NewGroup(80, 1500, 920, 300).(*Canvas)
		_ = g.dst.Bounds()
	}
}
