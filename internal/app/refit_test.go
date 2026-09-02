package app

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
)

// TestFitVariantsGroupByTheFrameAlone: in the modes that render nothing,
// overlays and render sizes have no meaning, so grouping by them would split
// platforms that are going to receive identical bytes. The frame still has
// meaning, because it is about the file rather than about the drawing.
func TestFitVariantsGroupByTheFrameAlone(t *testing.T) {
	cfg := config.Defaults()
	// Two platforms that want nothing done to the file, and that disagree
	// about overlays and sizes, which do not apply here.
	cfg.Publish.Discord.Layout.Overlay = []string{"a.html"}
	cfg.Publish.Discord.Layout.Width = 800
	cfg.Publish.Slack.Layout.Overlay = []string{"b.html"}
	// One that wants a story frame.
	cfg.Publish.Instagram.Layout.Width = 1080
	cfg.Publish.Instagram.Layout.Height = 1920
	cfg.Publish.Instagram.Layout.Fit = "contain"
	cfg.Publish.Instagram.Layout.FitBackground = "#04140c"
	// And one that wants the same frame, so the two share the work.
	cfg.Publish.Telegram.Layout.Width = 1080
	cfg.Publish.Telegram.Layout.Height = 1920
	cfg.Publish.Telegram.Layout.Fit = "contain"
	cfg.Publish.Telegram.Layout.FitBackground = "#04140c"

	got := FitVariants(&cfg, []string{"discord", "slack", "instagram", "telegram"})
	if len(got) != 2 {
		t.Fatalf("made %d variants, want the unfitted pair and the fitted pair: %+v", len(got), got)
	}
	if got[0].Fits() {
		t.Errorf("the first variant should ask for nothing: %+v", got[0])
	}
	if len(got[0].Platforms) != 2 {
		t.Errorf("discord and slack should share the file as it arrived: %v", got[0].Platforms)
	}
	if !got[1].Fits() || got[1].FitWidth != 1080 || got[1].FitHeight != 1920 {
		t.Errorf("the fitted variant = %+v", got[1])
	}
	if len(got[1].Platforms) != 2 {
		t.Errorf("two platforms asked for the same frame and should share it: %v", got[1].Platforms)
	}
}

// TestFitVariantsWithNoFitAnywhere is the ordinary publish-only run: one
// variant, every platform on it, exactly as before this existed.
func TestFitVariantsWithNoFitAnywhere(t *testing.T) {
	cfg := config.Defaults()
	got := FitVariants(&cfg, []string{"discord", "telegram", "slack"})
	if len(got) != 1 || len(got[0].Platforms) != 3 || got[0].Fits() {
		t.Fatalf("variants = %+v", got)
	}
}

// TestPublishInputFitsAnImage: the same rule as the clip, on the path that
// needs no ffmpeg. A file handed to crier is passed through, unless the
// platform asked for a frame.
func TestPublishInputFitsAnImage(t *testing.T) {
	dir := t.TempDir()
	square := filepath.Join(dir, "square.png")
	writePNG(t, square, 100, 100)

	cfg := config.Defaults()
	cfg.Publish.Input = square
	cfg.Render.HermeticFonts = true
	p := &Pipeline{cfg: &cfg, log: zerolog.New(zerolog.NewTestWriter(t)), dir: dir}

	fitted := Variant{
		Fit: config.FitContain, FitWidth: 200, FitHeight: 400,
		FitBackground: "#04140c", Platforms: []string{"instagram"},
	}
	arts, err := p.Render(context.Background(), fitted, nil, []config.Format{config.PNG})
	if err != nil {
		t.Fatal(err)
	}
	art := arts.First().Images[config.PNG]
	if art.Width != 200 || art.Height != 400 {
		t.Errorf("the fitted image is %dx%d, want 200x400", art.Width, art.Height)
	}
	if art.Path == square {
		t.Error("the input file was published unchanged despite the fit")
	}

	// The same input with no frame asked for is passed through as it arrived.
	plain, err := p.Render(context.Background(), Variant{Platforms: []string{"discord"}},
		nil, []config.Format{config.PNG})
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.First().Images[config.PNG]; got.Path != square {
		t.Errorf("published %s, want the file as it arrived", got.Path)
	}
}

// TestPublishInputRefusesAClipItCannotReshape: a fit on a clip needs ffmpeg,
// and saying so before the upload beats a story with black bars.
func TestPublishInputRefusesAClipItCannotReshape(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(clip, []byte("\x00\x00\x00\x20ftypisomand some bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Publish.Input = clip
	cfg.Render.Video.FFmpegBin = filepath.Join(dir, "no-such-ffmpeg")
	p := &Pipeline{cfg: &cfg, log: zerolog.New(zerolog.NewTestWriter(t)), dir: dir}

	_, err := p.Render(context.Background(), Variant{
		Fit: config.FitContain, FitWidth: 1080, FitHeight: 1920,
		FitBackground: "#04140c", Platforms: []string{"instagram"},
	}, nil, []config.Format{config.JPEG})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "only ffmpeg can reshape") {
		t.Errorf("err = %v", err)
	}
	if code := codeOf(err); code != ExitConfig {
		t.Errorf("exit code = %d, want the config one", code)
	}
}

// TestPublishInputLeavesAClipAloneWithoutAFit is the rule that has not
// changed: no frame asked for, no re-encode, the bytes as they arrived.
func TestPublishInputLeavesAClipAloneWithoutAFit(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(clip, []byte("\x00\x00\x00\x20ftypisomand some bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Publish.Input = clip
	// An ffmpeg that is not there: nothing should reach for it.
	cfg.Render.Video.FFmpegBin = filepath.Join(dir, "no-such-ffmpeg")
	p := &Pipeline{cfg: &cfg, log: zerolog.New(zerolog.NewTestWriter(t)), dir: dir}

	arts, err := p.Render(context.Background(), Variant{Platforms: []string{"discord"}},
		nil, []config.Format{config.JPEG})
	if err != nil {
		t.Fatal(err)
	}
	if arts.Video == nil || arts.Video.Path != clip {
		t.Errorf("video = %+v, want the file as it arrived", arts.Video)
	}
}

// writePNG writes a solid image of the given size.
func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+4 <= len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 200, 40, 60, 255
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
