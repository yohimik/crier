package render

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
)

// TestFitGoldens pins what cover and contain actually produce.
//
// The master is deliberately asymmetric — a wide card with content near its
// edges — because a square one would look the same however it was fitted, and
// the whole question is what happens to the parts that do not survive.
func TestFitGoldens(t *testing.T) {
	update := os.Getenv(UpdateGoldenEnv) != ""
	dir := filepath.Join("testdata", "golden")
	fonts := hermeticFonts(t)

	const masterHTML = `<div style="width:320px;height:120px;position:relative;` +
		`background:linear-gradient(90deg,#12203a,#4a2f6f);font-family:Go;color:#fff">` +
		`<div style="position:absolute;left:8px;top:6px;font-size:20px">LEFT</div>` +
		`<div style="position:absolute;right:8px;top:6px;font-size:20px">RIGHT</div>` +
		`<div style="position:absolute;left:120px;top:44px;font-size:28px">MIDDLE</div>` +
		`<div style="position:absolute;left:8px;bottom:6px;font-size:20px">BOTTOM</div>` +
		`</div>`

	master, err := RenderOne(context.Background(), Options{
		HTML:       "<html><body style='margin:0'>" + masterHTML + "</body></html>",
		Width:      320,
		Height:     120,
		Background: color.White,
		Fonts:      fonts,
		Logger:     zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.WarnLevel),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name       string
		w, h       int
		fit        config.Fit
		background color.Color
	}{
		// A wide card into a tall frame: cover keeps the middle and loses the
		// left and right thirds entirely.
		{"fit_cover_portrait", 120, 200, config.FitCover, color.White},
		// The same frame with contain: nothing is lost and the bars show.
		{"fit_contain_portrait", 120, 200, config.FitContain, color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 255}},
		// And into a square, which crops far less.
		{"fit_cover_square", 160, 160, config.FitCover, color.White},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := FitImage(master, tc.w, tc.h, tc.fit, tc.background)
			if got.Bounds().Dx() != tc.w || got.Bounds().Dy() != tc.h {
				t.Fatalf("bounds = %v, want %dx%d", got.Bounds(), tc.w, tc.h)
			}
			path := filepath.Join(dir, tc.name+".png")
			if update {
				writeGolden(t, path, got)
				return
			}
			want := readGolden(t, path)
			if err := compareImages(want, got); err != nil {
				t.Fatalf("%s does not match its golden: %v\n"+
					"if the change is intended, regenerate with %s=1 go test ./internal/render",
					tc.name, err, UpdateGoldenEnv)
			}
		})
	}
}
