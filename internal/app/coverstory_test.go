package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/render"
	"github.com/yohimik/crier/internal/stage"
)

func TestCoverStoryUsesOneStillForSixteenSecondsInAVerticalFrame(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffmpeg is a POSIX shell script")
	}
	dir := t.TempDir()
	cover := filepath.Join(dir, "cover.png")
	writePNG(t, cover, 10, 10)
	captured := filepath.Join(dir, "frames.rgba")
	ffmpeg := filepath.Join(dir, "ffmpeg")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"${1-}\" = -version ]; then exit 0; fi\nout=\nfor out do :; done\ncat > %q\nprintf '\\000\\000\\000\\040ftypisom' > \"$out\"\n", captured)
	if err := os.WriteFile(ffmpeg, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Render.Video.FFmpegBin = ffmpeg
	cfg.Render.Video.FPS = 30
	cfg.Render.Video.Audio = filepath.Join(dir, "music.mp3")
	p := &Pipeline{cfg: &cfg, log: zerolog.Nop(), dir: dir, audio: cfg.Render.Video.Audio}
	story, err := p.CoverStory(context.Background(), render.Artifact{Path: cover, Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	if story.Video == nil || story.Video.Width != 1080 || story.Video.Height != 1920 {
		t.Fatalf("story video = %+v, want a 1080x1920 MP4", story.Video)
	}
	info, err := os.Stat(captured)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(10 * 10 * 4 * 30 * 16); info.Size() != want {
		t.Fatalf("streamed %d frame bytes, want %d (16 seconds at 30 fps)", info.Size(), want)
	}
}

func TestCoverStoryFailureDoesNotStopIndependentPublishers(t *testing.T) {
	dir := t.TempDir()
	cover := filepath.Join(dir, "cover.png")
	writePNG(t, cover, 10, 10)
	cfg := config.Defaults()
	cfg.Render.Video.FFmpegBin = filepath.Join(dir, "missing-ffmpeg")
	pipeline := &Pipeline{cfg: &cfg, log: zerolog.Nop(), dir: dir, audio: "music.mp3"}
	story := &coverStoryPublisher{instagram: stubPub{name: "instagram"}, pipeline: pipeline}
	feed := stubPub{name: "discord"}
	input := publish.Input{Artifact: render.Artifact{Path: cover, Kind: render.KindImage}}
	report := publish.RunAll(context.Background(), []publish.Job{
		{Publisher: story, Posts: []publish.Input{input}},
		{Publisher: feed, Posts: []publish.Input{input}},
	}, 2, zerolog.Nop())
	if report.Failed() != 1 || report.Succeeded() != 1 {
		t.Fatalf("outcomes = %+v, want failed story and successful independent feed", report.Outcomes)
	}
}

type failingStoryStager struct{}

func (failingStoryStager) Name() string { return "failing" }
func (failingStoryStager) Stage(context.Context, stage.Asset) (*stage.Object, error) {
	return nil, fmt.Errorf("stage unavailable")
}
func (failingStoryStager) Close(context.Context) error { return nil }

func TestCoverStoryStagingFailureDoesNotStopIndependentPublishers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffmpeg is a POSIX shell script")
	}
	dir := t.TempDir()
	cover := filepath.Join(dir, "cover.png")
	writePNG(t, cover, 2, 2)
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nif [ \"${1-}\" = -version ]; then exit 0; fi\nout=\nfor out do :; done\ncat >/dev/null\nprintf '\\000\\000\\000\\040ftypisom' > \"$out\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Render.Video.FFmpegBin = ffmpeg
	pipeline := &Pipeline{cfg: &cfg, log: zerolog.Nop(), dir: dir, audio: "music.mp3"}
	story := &coverStoryPublisher{instagram: stubPub{name: "instagram"}, pipeline: pipeline, stager: failingStoryStager{}}
	input := publish.Input{Artifact: render.Artifact{Path: cover, Kind: render.KindImage}}
	report := publish.RunAll(context.Background(), []publish.Job{
		{Publisher: story, Posts: []publish.Input{input}},
		{Publisher: stubPub{name: "linkedin"}, Posts: []publish.Input{input}},
	}, 2, zerolog.Nop())
	if report.Failed() != 1 || report.Succeeded() != 1 {
		t.Fatalf("outcomes = %+v, want failed story staging and successful independent feed", report.Outcomes)
	}
}

func TestPingCoverStoryAudioChecksEveryPoolEntry(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.mp3")
	two := filepath.Join(dir, "two.mp3")
	if err := os.WriteFile(one, []byte("ID3music"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, []byte("not audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Publish.Instagram.Enabled = true
	cfg.Publish.Instagram.CoverStory = true
	cfg.Render.Video.AudioPool = []string{one, two}
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg.Render.Video.FFmpegBin = ffmpeg
	rows := pingCoverStory(context.Background(), &cfg, zerolog.Nop())
	if len(rows) != 3 || !rows[0].OK || !rows[1].OK || rows[2].OK || rows[2].Error == "" {
		t.Fatalf("rows = %+v, want every pool entry checked independently", rows)
	}
}
