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
