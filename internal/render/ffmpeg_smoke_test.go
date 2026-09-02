//go:build ffmpeg

package render

import (
	"context"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
)

// TestRealFFmpeg runs the video path against a real encoder.
//
// Every other video test uses a fake ffmpeg, which proves crier feeds it the
// right bytes but not that a real encoder accepts them. This one is behind a
// build tag and skipped when ffmpeg is absent, so a laptop without it still
// runs the suite; CI installs ffmpeg and runs this leg.
func TestRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "clip.mp4")

	art, err := EncodeVideo(context.Background(), VideoOptions{
		Output: out,
		Frames: 10,
		FPS:    10,
		Width:  64,
		Height: 64,
		Logger: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
	}, func(_ context.Context, i int) (*image.RGBA, error) {
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		shade := uint8(i * 25)
		for p := 0; p+4 <= len(img.Pix); p += 4 {
			img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = shade, 0, 255-shade, 255
		}
		return img, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if art.Kind != KindVideo || art.ContentType != VideoContentType {
		t.Errorf("artifact = %+v", art)
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("no video was written: %v", err)
	}

	// ffprobe, when it is there, is what says the file is really H.264 in an
	// MP4 with the index at the front — the combination Instagram insists on.
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Log("ffprobe is not installed; the file was produced but not inspected")
		return
	}
	outBytes, err := exec.Command(probe, "-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height",
		"-of", "default=noprint_wrappers=1:nokey=1", out).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	got := string(outBytes)
	for _, want := range []string{"h264", "64"} {
		if !strings.Contains(got, want) {
			t.Errorf("ffprobe reported %q, missing %q", got, want)
		}
	}
}

// TestRealFFmpegRefitsAnExistingClip is defect three of rc.8, against a real
// encoder: a square clip published as a story came out with black bars,
// because publish-only mode passed the file through and Instagram padded it on
// its own servers.
//
// The two things worth proving are what that cost: the frame really is the
// platform's, and the soundtrack survived the re-encode. A fit that produced a
// silent story would trade one defect for a worse one.
func TestRealFFmpegRefitsAnExistingClip(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}

	dir := t.TempDir()
	square := filepath.Join(dir, "square.mp4")
	// A square clip with a real audio stream, which is what the announcement
	// anthem is.
	build := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=red:s=240x240:d=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", square)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the input clip: %v\n%s", err, out)
	}

	out := filepath.Join(dir, "story.mp4")
	art, err := RefitVideo(context.Background(), RefitOptions{
		Input:  square,
		Output: out,
		Filter: FitFilter(360, 640, config.FitContain, "#04140c"),
		Width:  360,
		Height: 640,
		Logger: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
	})
	if err != nil {
		t.Fatal(err)
	}
	if art.Width != 360 || art.Height != 640 || art.Kind != KindVideo {
		t.Errorf("artifact = %+v", art)
	}

	dims, err := exec.Command(probe, "-v", "error", "-select_streams", "v",
		"-show_entries", "stream=width,height", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(dims)); got != "360,640" {
		t.Errorf("the fitted clip is %s, want 360,640", got)
	}

	audio, err := exec.Command(probe, "-v", "error", "-select_streams", "a",
		"-show_entries", "stream=codec_name", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(audio)); got != "aac" {
		t.Errorf("the audio stream is %q, want the aac that was copied over", got)
	}
}
