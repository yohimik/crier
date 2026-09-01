package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
	"github.com/yohimik/crier/internal/stage"
)

// failingStager refuses everything, which is how a bucket behaves when the
// credentials are wrong.
type failingStager struct{ err error }

func (failingStager) Name() string { return "failing" }
func (f failingStager) Stage(context.Context, stage.Asset) (*stage.Object, error) {
	return nil, f.err
}
func (failingStager) Close(context.Context) error { return nil }

// countingStager records what it was asked to stage.
type countingStager struct{ staged []string }

func (countingStager) Name() string { return "counting" }
func (c *countingStager) Stage(_ context.Context, a stage.Asset) (*stage.Object, error) {
	c.staged = append(c.staged, filepath.Base(a.Path))
	return &stage.Object{
		URL:    "https://staged.example/" + filepath.Base(a.Path),
		Remove: func(context.Context) error { return nil },
	}, nil
}
func (countingStager) Close(context.Context) error { return nil }

func stagePipeline(t *testing.T) *Pipeline {
	t.Helper()
	cfg := config.Defaults()
	cfg.Render.HermeticFonts = true
	p, err := NewPipeline(PipelineOptions{
		Config: &cfg,
		Logger: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
		Client: httpx.New(httpx.Options{Logger: zerolog.Nop()}),
		Stdin:  strings.NewReader(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Cleanup(context.Background()) })
	return p
}

// TestStageOnlyWhenSomethingNeedsIt: staging costs an upload, so a run whose
// platforms all take bytes should do none.
func TestStageOnlyWhenSomethingNeedsIt(t *testing.T) {
	p := stagePipeline(t)
	st := &countingStager{}
	arts := Artifacts{Images: map[config.Format]render.Artifact{
		config.PNG: {Path: "/tmp/card.png", ContentType: "image/png", Format: config.PNG},
	}}
	if err := p.Stage(context.Background(), st, &arts, false, false); err != nil {
		t.Fatal(err)
	}
	if len(st.staged) != 0 || arts.URL != "" {
		t.Errorf("it staged %v for a run that needed no URL", st.staged)
	}
}

// TestStagePosterAlongsideTheClip is Reddit's requirement: the video and the
// still both have to be reachable.
func TestStagePosterAlongsideTheClip(t *testing.T) {
	p := stagePipeline(t)
	st := &countingStager{}
	arts := Artifacts{
		Video:  &render.Artifact{Path: "/tmp/clip.mp4", ContentType: render.VideoContentType, Kind: render.KindVideo},
		Poster: &render.Artifact{Path: "/tmp/poster.jpg", ContentType: "image/jpeg", Kind: render.KindImage},
	}
	if err := p.Stage(context.Background(), st, &arts, true, true); err != nil {
		t.Fatal(err)
	}
	if len(st.staged) != 2 {
		t.Fatalf("staged %v, want the clip and its poster", st.staged)
	}
	if arts.URL == "" || arts.PosterURL == "" {
		t.Errorf("urls = %q and %q", arts.URL, arts.PosterURL)
	}

	// A poster nobody asked for is not staged.
	st2 := &countingStager{}
	arts2 := arts
	arts2.URL, arts2.PosterURL = "", ""
	if err := p.Stage(context.Background(), st2, &arts2, true, false); err != nil {
		t.Fatal(err)
	}
	if len(st2.staged) != 1 {
		t.Errorf("staged %v, want only the clip", st2.staged)
	}
}

// TestStageFailureIsAStagingError: a bucket that refuses is exit 6, not a
// publish failure, because nothing was ever sent to a platform.
func TestStageFailureIsAStagingError(t *testing.T) {
	p := stagePipeline(t)
	arts := Artifacts{Images: map[config.Format]render.Artifact{
		config.PNG: {Path: "/tmp/card.png", ContentType: "image/png", Format: config.PNG},
	}}
	err := p.Stage(context.Background(), failingStager{err: errors.New("access denied")},
		&arts, true, false)
	if err == nil || codeOf(err) != ExitStaging {
		t.Fatalf("err = %v, code = %d", err, codeOf(err))
	}

	// A poster that cannot be staged is the same failure, after the clip went up.
	both := Artifacts{
		Video:  &render.Artifact{Path: "/tmp/clip.mp4", ContentType: render.VideoContentType},
		Poster: &render.Artifact{Path: "/tmp/poster.jpg", ContentType: "image/jpeg"},
	}
	if err := p.Stage(context.Background(), &halfFailingStager{}, &both, true, true); err == nil ||
		codeOf(err) != ExitStaging {
		t.Errorf("err = %v", err)
	}

	// Nothing to stage at all is a staging error naming the gap.
	empty := Artifacts{Images: map[config.Format]render.Artifact{}}
	if err := p.Stage(context.Background(), &countingStager{}, &empty, true, false); err == nil {
		t.Error("there is nothing to stage; that should be an error")
	}
}

// halfFailingStager stages the first thing and refuses the second.
type halfFailingStager struct{ n int }

func (*halfFailingStager) Name() string { return "half" }
func (h *halfFailingStager) Stage(context.Context, stage.Asset) (*stage.Object, error) {
	h.n++
	if h.n == 1 {
		return &stage.Object{URL: "https://x/1", Remove: func(context.Context) error { return nil }}, nil
	}
	return nil, errors.New("the poster was refused")
}
func (*halfFailingStager) Close(context.Context) error { return nil }

// TestEnableOnlyLeavesTheRealConfigAlone is a regression test.
//
// The custom entries are pointers behind a map, so the shallow copy `crier
// platforms` makes shares them — mutating them to ask "is discord configured"
// would quietly disable every custom platform in the configuration the run
// then uses.
func TestEnableOnlyLeavesTheRealConfigAlone(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Custom = map[string]*config.Custom{
		"hook":  {Enabled: true, Command: "true"},
		"other": {Enabled: true, Command: "true"},
	}

	probe := cfg
	enableOnly(&probe, "discord")

	if !cfg.Publish.Custom["hook"].Enabled || !cfg.Publish.Custom["other"].Enabled {
		t.Error("the real configuration's custom platforms were disabled")
	}
	if !cfg.Publish.Telegram.Enabled {
		t.Error("the real configuration's telegram was disabled")
	}
	if probe.Publish.Custom["hook"].Enabled {
		t.Error("the probe should have only discord on")
	}
	if !probe.Publish.Discord.Enabled || probe.Publish.Telegram.Enabled {
		t.Error("the probe should have exactly one platform on")
	}

	// A custom platform can be the one turned on.
	probe2 := cfg
	enableOnly(&probe2, "hook")
	if !probe2.Publish.Custom["hook"].Enabled || probe2.Publish.Custom["other"].Enabled {
		t.Error("enabling one custom platform should leave the others off")
	}
	if probe2.Publish.Telegram.Enabled {
		t.Error("the built-ins should all be off")
	}
}

// TestPosterExtractionNeedsFFmpeg: without it the failure is a render error
// naming what is missing.
func TestPosterExtractionNeedsFFmpeg(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.HermeticFonts = true
	cfg.Render.Video.FFmpegBin = "crier-no-such-ffmpeg"
	p, err := NewPipeline(PipelineOptions{
		Config: &cfg,
		Logger: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
		Client: httpx.New(httpx.Options{Logger: zerolog.Nop()}),
		Stdin:  strings.NewReader(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Cleanup(context.Background()) })

	clip := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(clip, []byte("not really an mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PosterFor(context.Background(), render.Artifact{Path: clip}); err == nil ||
		codeOf(err) != ExitRender {
		t.Errorf("err = %v, code = %d", err, codeOf(err))
	}
}

// TestFramesEncodingNeedsFFmpeg and a frames directory that cannot be read.
func TestFramesEncodingNeedsFFmpeg(t *testing.T) {
	dir := t.TempDir()
	frames := filepath.Join(dir, "frames")
	if err := os.MkdirAll(frames, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBytes(t, frames, "a.png", samplePNG(t, 4, 4))

	cfg := config.Defaults()
	cfg.Render.HermeticFonts = true
	cfg.Render.Video.FramesInput = frames
	cfg.Render.Video.FFmpegBin = "crier-no-such-ffmpeg"

	p, err := NewPipeline(PipelineOptions{
		Config: &cfg,
		Logger: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
		Client: httpx.New(httpx.Options{Logger: zerolog.Nop()}),
		Stdin:  strings.NewReader(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Cleanup(context.Background()) })

	if _, err := p.Render(context.Background(), BaseVariant(&cfg), nil, nil); err == nil ||
		!strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("err = %v", err)
	}

	// A frames directory holding nothing is a config error naming the setting.
	cfg.Render.Video.FramesInput = filepath.Join(dir, "empty")
	if err := os.MkdirAll(cfg.Render.Video.FramesInput, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Render(context.Background(), BaseVariant(&cfg), nil, nil); err == nil ||
		!strings.Contains(err.Error(), "frames-input") {
		t.Errorf("err = %v", err)
	}
}
