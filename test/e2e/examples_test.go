//go:build e2e

package e2e

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

// exampleGallery is what the README's gallery links to. Each entry names the
// size its preview is committed at, so a template that stops rendering — or
// starts rendering at a different size — fails here rather than leaving a
// stale thumbnail in the README.
var exampleGallery = []struct {
	dir           string
	width, height int
}{
	{"square-1080", 1080, 1080},
	{"story-1080x1920", 1080, 1920},
	{"business-promo", 1080, 1080},
	{"video-game-release", 1920, 1080},
	{"social-quote", 1200, 675},
	{"release-changelog", 1080, 1080},
	{"event-invite", 1080, 1920},
}

func TestEveryExampleStillRenders(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	for _, ex := range exampleGallery {
		t.Run(ex.dir, func(t *testing.T) {
			cfg := filepath.Join(root, "examples", ex.dir, "crier.yaml")
			if _, err := os.Stat(cfg); err != nil {
				t.Fatalf("the gallery lists %s but it has no crier.yaml: %v", ex.dir, err)
			}
			out := filepath.Join(t.TempDir(), "out.png")

			res := crier(t, root, nil, "render", "--config", cfg, "--render-output", out)
			if res.Code != exitOK {
				t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
			}
			cfgImg, format := decodeImage(t, out)
			if format != "png" {
				t.Errorf("format = %s", format)
			}
			if cfgImg.Width != ex.width || cfgImg.Height != ex.height {
				t.Errorf("rendered %dx%d, but the gallery says %dx%d",
					cfgImg.Width, cfgImg.Height, ex.width, ex.height)
			}

			// The committed preview is what the README shows, so it has to
			// exist and match the size too.
			preview := filepath.Join(root, "examples", ex.dir, "preview.png")
			previewCfg, _ := decodeImage(t, preview)
			if previewCfg.Width != ex.width || previewCfg.Height != ex.height {
				t.Errorf("the committed preview is %dx%d, want %dx%d; regenerate it with\n"+
					"  crier render --config examples/%s/crier.yaml",
					previewCfg.Width, previewCfg.Height, ex.width, ex.height, ex.dir)
			}
		})
	}
}

// TestOverlayVariantOfAnExample checks the one gallery entry that ships a
// per-platform overlay really produces a different shape.
func TestOverlayVariantOfAnExample(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "examples", "video-game-release", "crier.yaml")
	out := filepath.Join(t.TempDir(), "story.png")

	res := crier(t, root, nil, "render", "--config", cfg,
		"--render-variant", "instagram", "--render-output", out)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	got, _ := decodeImage(t, out)
	if got.Width != 1080 || got.Height != 1920 {
		t.Fatalf("the story variant is %dx%d, want 1080x1920", got.Width, got.Height)
	}
}

// TestSeededExampleIsReproducible checks the pinned seed in the quote example
// really pins the layout it picks, which is what makes its committed preview
// meaningful.
func TestSeededExampleIsReproducible(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "examples", "social-quote", "crier.yaml")

	render := func(seed string) image.Config {
		out := filepath.Join(t.TempDir(), "q.png")
		args := []string{"render", "--config", cfg, "--render-output", out}
		if seed != "" {
			args = append(args, "--render-seed", seed)
		}
		res := crier(t, root, nil, args...)
		if res.Code != exitOK {
			t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
		}
		cfgImg, _ := decodeImage(t, out)
		return cfgImg
	}

	a := render("")
	b := render("")
	if a != b {
		t.Errorf("the pinned seed gave two different results: %v then %v", a, b)
	}
}
