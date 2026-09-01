package render

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

// Golden images are compared with a tolerance rather than byte for byte.
//
// The rasterizer works in float32, and float32 does not associate the same way
// on every architecture: an arm64 machine with fused multiply-add and an amd64
// one without produce coverage values a step apart on the antialiased edge of a
// curve. A byte-exact golden would fail on one of them for a reason that is not
// a bug, so the rule is: no channel may differ by more than MaxChannelDelta
// anywhere, and no more than MaxLooseFraction of the pixels may differ by more
// than LooseChannelDelta.
const (
	// MaxChannelDelta is the largest per-channel difference allowed anywhere.
	MaxChannelDelta = 8
	// LooseChannelDelta is the difference above which a pixel counts as loose.
	LooseChannelDelta = 2
	// MaxLooseFraction is how many pixels may be loose, as a fraction.
	MaxLooseFraction = 0.001
)

// UpdateGoldenEnv regenerates the golden files when it is set.
const UpdateGoldenEnv = "CRIER_UPDATE_GOLDEN"

// overflowText is one very long unbreakable word plus more, which is the
// shape that defeats a naive layout.
const overflowText = "Supercalifragilisticexpialidocious antidisestablishmentarianism " +
	"pneumonoultramicroscopicsilicovolcanoconiosis and then some more words"

// overflowWords is ordinary prose, which is what a line clamp is for.
const overflowWords = "This is an ordinary sentence made of ordinary words that simply " +
	"goes on for far too long to fit inside the card it was given."

type goldenCase struct {
	name          string
	html          string
	width, height int
}

// The scenes cover one drawing primitive each, so a failure names the part of
// the backend that changed rather than "the page looks different".
func goldenCases() []goldenCase {
	const head = `<style>body{margin:0;font-family:Go;background:#fff}</style>`
	return []goldenCase{
		{
			name:  "solid_card",
			width: 200, height: 120,
			html: head + `<div style="margin:20px;width:160px;height:80px;background:#3355cc;border-radius:12px"></div>`,
		},
		{
			name:  "dashed_border",
			width: 200, height: 120,
			html: head + `<div style="margin:20px;width:150px;height:70px;border:6px dashed #cc3355"></div>`,
		},
		{
			name:  "linear_gradient",
			width: 200, height: 120,
			html: head + `<div style="width:200px;height:120px;background:linear-gradient(90deg,#ff0000,#0000ff)"></div>`,
		},
		{
			name:  "radial_gradient",
			width: 200, height: 120,
			html: head + `<div style="width:200px;height:120px;background:radial-gradient(circle at 50% 50%,#ffffff,#000000)"></div>`,
		},
		{
			// The commonest overlay there is: a fade to transparent over a
			// photo, so a caption stays readable. CSS interpolates gradient
			// stops premultiplied; interpolating straight drags
			// "transparent" — which is rgba(0,0,0,0) — into the mix and runs
			// the fade through a muddy grey.
			// Two stops differing in both colour and alpha, which is where
			// straight and premultiplied interpolation actually part company.
			// (A plain fade to `transparent` does not: webrender resolves it
			// to the carried colour at zero alpha, so both agree.)
			name:  "gradient_premultiplied",
			width: 200, height: 120,
			html: head + `<div style="width:200px;height:120px;background:#ffffff">` +
				`<div style="width:200px;height:120px;` +
				`background:linear-gradient(90deg,rgba(255,0,0,1),rgba(0,0,255,0.2))"></div></div>`,
		},
		{
			name:  "text_basic",
			width: 320, height: 90,
			html: head + `<div style="padding:16px;font-size:28px;color:#111">Crier renders text</div>`,
		},
		{
			name:  "text_decoration",
			width: 320, height: 120,
			html: head + `<div style="padding:16px;font-size:24px;color:#114">` +
				`<span style="text-decoration:underline">under</span> ` +
				`<b>bold</b> <i>italic</i> <span style="font-family:'Go Mono'">mono</span></div>`,
		},
		{
			name:  "opacity_stack",
			width: 200, height: 120,
			html: head + `<div style="position:relative;width:200px;height:120px">` +
				`<div style="position:absolute;left:20px;top:20px;width:100px;height:80px;background:#ff0000;opacity:0.6"></div>` +
				`<div style="position:absolute;left:70px;top:30px;width:100px;height:80px;background:#0000ff;opacity:0.6"></div>` +
				`</div>`,
		},
		{
			name:  "story_1080x1920",
			width: 1080, height: 1920,
			html: head + `<div style="width:1080px;height:1920px;` +
				`background:linear-gradient(160deg,#12203a,#4a2f6f);color:#fff;` +
				`display:flex;align-items:center;justify-content:center">` +
				`<div style="font-size:96px;text-align:center">crier<br>story</div></div>`,
		},
		// The overflow cases carry a string that cannot fit, and their goldens
		// are what proves each CSS recipe in docs/templates.md still works.
		{
			name:  "text_overflow_ellipsis",
			width: 300, height: 100,
			html: head + `<div style="width:280px;height:80px;margin:10px;font-size:24px;` +
				`background:#eef;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">` +
				overflowText + `</div>`,
		},
		{
			name:  "text_overflow_clip",
			width: 300, height: 100,
			html: head + `<div style="width:280px;height:80px;margin:10px;font-size:24px;` +
				`background:#eef;overflow:hidden">` + overflowText + `</div>`,
		},
		{
			name:  "text_overflow_wrap",
			width: 300, height: 100,
			html: head + `<div style="width:280px;height:80px;margin:10px;font-size:24px;` +
				`background:#eef;overflow:hidden;overflow-wrap:break-word">` + overflowText + `</div>`,
		},
		{
			name:  "text_overflow_clamp",
			width: 300, height: 100,
			html: head + `<div style="width:280px;height:80px;margin:10px;font-size:24px;` +
				`background:#eef;overflow:hidden;overflow-wrap:break-word;` +
				`max-lines:2;continue:discard;block-ellipsis:auto">` + overflowWords + `</div>`,
		},
		{
			name:  "post_1080x1080",
			width: 1080, height: 1080,
			html: head + `<div style="width:1080px;height:1080px;background:#f5f0e8;` +
				`padding:80px;box-sizing:border-box;color:#222">` +
				`<div style="font-size:72px">A square post</div>` +
				`<div style="margin-top:24px;font-size:36px;color:#666">rendered by crier</div></div>`,
		},
	}
}

func TestGoldens(t *testing.T) {
	update := os.Getenv(UpdateGoldenEnv) != ""
	dir := filepath.Join("testdata", "golden")
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fonts := hermeticFonts(t)

	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			img, err := RenderOne(context.Background(), Options{
				HTML:       "<html><body>" + tc.html + "</body></html>",
				Width:      tc.width,
				Height:     tc.height,
				Background: color.White,
				Fonts:      fonts,
				Logger:     zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.WarnLevel),
			})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, tc.name+".png")
			if update {
				writeGolden(t, path, img)
				return
			}
			want := readGolden(t, path)
			if err := compareImages(want, img); err != nil {
				t.Fatalf("%s does not match its golden: %v\n"+
					"if the change is intended, regenerate with %s=1 go test ./internal/render",
					tc.name, err, UpdateGoldenEnv)
			}
		})
	}
}

func writeGolden(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}

func readGolden(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%v (regenerate with %s=1)", err, UpdateGoldenEnv)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// compareImages applies the tolerance rule.
func compareImages(want, got image.Image) error {
	wb, gb := want.Bounds(), got.Bounds()
	if wb.Dx() != gb.Dx() || wb.Dy() != gb.Dy() {
		return fmt.Errorf("size %dx%d, want %dx%d", gb.Dx(), gb.Dy(), wb.Dx(), wb.Dy())
	}
	total := wb.Dx() * wb.Dy()
	loose, worst := 0, 0
	var worstAt image.Point
	for y := 0; y < wb.Dy(); y++ {
		for x := 0; x < wb.Dx(); x++ {
			wr, wg, wbl, wa := want.At(wb.Min.X+x, wb.Min.Y+y).RGBA()
			gr, gg, gbl, ga := got.At(gb.Min.X+x, gb.Min.Y+y).RGBA()
			d := maxDelta(
				int(wr>>8)-int(gr>>8), int(wg>>8)-int(gg>>8),
				int(wbl>>8)-int(gbl>>8), int(wa>>8)-int(ga>>8),
			)
			if d > worst {
				worst, worstAt = d, image.Pt(x, y)
			}
			if d > LooseChannelDelta {
				loose++
			}
		}
	}
	if worst > MaxChannelDelta {
		return fmt.Errorf("channel differs by %d at %v, more than the %d allowed", worst, worstAt, MaxChannelDelta)
	}
	if frac := float64(loose) / float64(total); frac > MaxLooseFraction {
		return fmt.Errorf("%.4f%% of pixels differ by more than %d, more than the %.4f%% allowed",
			frac*100, LooseChannelDelta, MaxLooseFraction*100)
	}
	return nil
}

func maxDelta(ds ...int) int {
	m := 0
	for _, d := range ds {
		if d < 0 {
			d = -d
		}
		if d > m {
			m = d
		}
	}
	return m
}

func TestCompareImages(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 4, 4))
	b := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := compareImages(a, b); err != nil {
		t.Errorf("identical images: %v", err)
	}

	b.SetRGBA(0, 0, color.RGBA{R: MaxChannelDelta + 1, A: 0})
	if err := compareImages(a, b); err == nil {
		t.Error("a large single-pixel difference should fail")
	}

	c := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := compareImages(a, c); err == nil {
		t.Error("a size mismatch should fail")
	}

	// A small difference on a few pixels is within the noise budget for a big
	// image, and over it for a small one.
	big := image.NewRGBA(image.Rect(0, 0, 100, 100))
	big2 := image.NewRGBA(image.Rect(0, 0, 100, 100))
	big2.SetRGBA(0, 0, color.RGBA{R: 4})
	if err := compareImages(big, big2); err != nil {
		t.Errorf("one loose pixel in 10000 should pass: %v", err)
	}
	small := image.NewRGBA(image.Rect(0, 0, 4, 4))
	small2 := image.NewRGBA(image.Rect(0, 0, 4, 4))
	small2.SetRGBA(0, 0, color.RGBA{R: 4})
	if err := compareImages(small, small2); err == nil {
		t.Error("one loose pixel in 16 is over the budget")
	}
}
