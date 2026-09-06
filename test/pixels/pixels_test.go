//go:build pixels

// Package pixels compares two actual CLI artifacts with the raster golden
// tolerance. It never rebuilds either CLI or updates golden images.
package pixels

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestArtifactPixels(t *testing.T) {
	reference, tested, output := os.Getenv("CRIER_PIXEL_REFERENCE"), os.Getenv("CRIER_PIXEL_BINARY"), os.Getenv("CRIER_PIXEL_OUTPUT")
	if reference == "" || tested == "" || output == "" {
		t.Fatal("CRIER_PIXEL_REFERENCE, CRIER_PIXEL_BINARY and CRIER_PIXEL_OUTPUT must name the compared artifacts and evidence directory")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate fixtures")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(source)))
	fixtures := []struct{ name, config, variant, pool, data string }{}
	for _, name := range []string{"square-1080", "story-1080x1920", "business-promo", "video-game-release", "social-quote", "release-changelog", "event-invite"} {
		fixtures = append(fixtures, struct{ name, config, variant, pool, data string }{name: name, config: "examples/" + name + "/crier.yaml"})
	}
	fixtures = append(fixtures, struct{ name, config, variant, pool, data string }{name: "video-game-story", config: "examples/video-game-release/crier.yaml", variant: "instagram"})
	fixtures = append(fixtures, struct{ name, config, variant, pool, data string }{name: "gradient-only", config: "examples/video-game-release/crier.yaml", pool: filepath.Join(root, "test/pixels/testdata/repeating-gradient.html"), data: "{}"})
	notes := exec.Command("sh", "announce/notes.sh")
	notes.Dir = root
	notes.Env = append(os.Environ(), "DISPAT_NEW_VERSION=1.2.3", "DISPAT_FEATURES=Render one release with both compilers\nKeep the same fonts and fixtures\nPublish release media together", "DISPAT_FIXES=Close the listener after use")
	data, err := notes.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{"template.html", "template-b.html"} {
		fixtures = append(fixtures, struct{ name, config, variant, pool, data string }{name: layout, config: "announce/crier.yaml", pool: filepath.Join(root, "announce", layout), data: string(data)})
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			var dirs []string
			for i, bin := range []string{reference, tested} {
				dir := filepath.Join(output, fixture.name, fmt.Sprint(i))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				dirs = append(dirs, dir)
				args := []string{"render", "--config", fixture.config, "--render-seed", "12345", "--render-format", "png", "--render-output", filepath.Join(dir, "page.png")}
				if fixture.variant != "" {
					args = append(args, "--render-variant", fixture.variant)
				}
				if fixture.pool != "" {
					args = append(args, "--render-pool", fixture.pool, "--render-data", "-")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Dir, cmd.Stdin = root, strings.NewReader(fixture.data)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s render failed: %v\n%s", bin, err, out)
				}
			}
			want, err := filepath.Glob(filepath.Join(dirs[0], "*.png"))
			if err != nil || len(want) == 0 {
				t.Fatalf("no reference PNGs: %v", err)
			}
			got, err := filepath.Glob(filepath.Join(dirs[1], "*.png"))
			if err != nil || len(got) != len(want) {
				t.Fatalf("page counts differ: want=%d got=%d: %v", len(want), len(got), err)
			}
			for _, path := range want {
				compare(t, path, filepath.Join(dirs[1], filepath.Base(path)))
			}
		})
	}
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	im, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func compare(t *testing.T, wantPath, gotPath string) {
	t.Helper()
	want, got := readPNG(t, wantPath), readPNG(t, gotPath)
	if want.Bounds() != got.Bounds() {
		t.Fatalf("dimensions differ: %v vs %v", want.Bounds(), got.Bounds())
	}
	changed, loose, worst := 0, 0, uint32(0)
	var samples [][3]int
	for y := want.Bounds().Min.Y; y < want.Bounds().Max.Y; y++ {
		for x := want.Bounds().Min.X; x < want.Bounds().Max.X; x++ {
			ar, ag, ab, aa := want.At(x, y).RGBA()
			br, bg, bb, ba := got.At(x, y).RGBA()
			a, b := [4]uint32{ar, ag, ab, aa}, [4]uint32{br, bg, bb, ba}
			delta := uint32(0)
			for i := range a {
				v, w := a[i]>>8, b[i]>>8
				delta = max(delta, max(v, w)-min(v, w))
			}
			worst = max(worst, delta)
			if delta != 0 {
				changed++
				if len(samples) < 16 {
					samples = append(samples, [3]int{x, y, int(delta)})
				}
			}
			if delta > 2 {
				loose++
			}
		}
	}
	total := want.Bounds().Dx() * want.Bounds().Dy()
	t.Logf("%s: changed=%d/%d loose=%d max_channel_delta=%d", filepath.Base(wantPath), changed, total, loose, worst)
	if len(samples) != 0 {
		t.Logf("first changed pixels [x,y,delta]: %v", samples)
	}
	// Same thresholds as internal/render/golden_test.go; never relax these
	// because a compiler disagrees. Preserve both images for diagnosis.
	if worst > 8 || float64(loose)/float64(total) > 0.001 {
		t.Errorf("raster golden tolerance exceeded; evidence: %s and %s", wantPath, gotPath)
	}
}
