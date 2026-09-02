package render

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// The video tests run a fake ffmpeg, which is this test binary re-invoked. It
// counts the bytes it is fed and writes them next to the output file, so the
// tests can assert the exact rawvideo stream without needing a real encoder.
const (
	fakeFFmpegEnv  = "CRIER_FAKE_FFMPEG"
	fakeFFmpegFail = "CRIER_FAKE_FFMPEG_FAIL"
)

// fakeFFmpegMain is called from TestMain when the helper environment is set.
func fakeFFmpegMain() {
	if msg := os.Getenv(fakeFFmpegFail); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
	n, err := io.Copy(io.Discard, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(2)
	}
	out := os.Args[len(os.Args)-1]
	if err := os.WriteFile(out, []byte("fake mp4"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(3)
	}
	if err := os.WriteFile(out+".bytes", []byte(strconv.FormatInt(n, 10)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(4)
	}
	fmt.Fprintln(os.Stderr, "fake ffmpeg wrote", n, "bytes")
	os.Exit(0)
}

func fakeFFmpegEnviron(extra ...string) []string {
	return append(append(os.Environ(), fakeFFmpegEnv+"=1"), extra...)
}

// videoOptions builds options that run the fake encoder.
func videoOptions(t *testing.T, out string) VideoOptions {
	t.Helper()
	return VideoOptions{
		Output: out,
		Frames: 3,
		FPS:    10,
		Width:  4,
		Height: 2,
		Bin:    os.Args[0],
		Logger: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
	}
}

func solidFrame(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+4 <= len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

func TestEncodeVideoStreamsEveryFrame(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "clip.mp4")
	o := videoOptions(t, out)
	o.Env = fakeFFmpegEnviron()

	var asked []int
	art, err := EncodeVideo(context.Background(), o, func(_ context.Context, i int) (*image.RGBA, error) {
		asked = append(asked, i)
		return solidFrame(o.Width, o.Height, color.RGBA{R: uint8(i), A: 255}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 3 || asked[0] != 0 || asked[2] != 2 {
		t.Errorf("frames asked for: %v", asked)
	}
	if art.Kind != KindVideo || art.ContentType != VideoContentType {
		t.Errorf("artifact = %+v", art)
	}
	if art.Width != 4 || art.Height != 2 || art.Size == 0 {
		t.Errorf("artifact = %+v", art)
	}

	counted, err := os.ReadFile(out + ".bytes")
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(3 * 4 * 2 * 4) // frames * w * h * RGBA
	if string(counted) != want {
		t.Errorf("ffmpeg was fed %s bytes, want %s", counted, want)
	}
}

func TestEncodeVideoReportsAFailingEncoder(t *testing.T) {
	dir := t.TempDir()
	o := videoOptions(t, filepath.Join(dir, "clip.mp4"))
	o.Env = fakeFFmpegEnviron(fakeFFmpegFail + "=codec not found")

	_, err := EncodeVideo(context.Background(), o, func(context.Context, int) (*image.RGBA, error) {
		return solidFrame(o.Width, o.Height, color.RGBA{A: 255}), nil
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "codec not found") {
		t.Errorf("the encoder's own message should surface, got %v", err)
	}
}

func TestEncodeVideoPropagatesFrameErrors(t *testing.T) {
	dir := t.TempDir()
	o := videoOptions(t, filepath.Join(dir, "clip.mp4"))
	o.Env = fakeFFmpegEnviron()
	boom := errors.New("template blew up")

	_, err := EncodeVideo(context.Background(), o, func(_ context.Context, i int) (*image.RGBA, error) {
		if i == 1 {
			return nil, boom
		}
		return solidFrame(o.Width, o.Height, color.RGBA{A: 255}), nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "frame 1") {
		t.Errorf("the error should name the frame, got %v", err)
	}
}

func TestEncodeVideoRejectsAWronglySizedFrame(t *testing.T) {
	dir := t.TempDir()
	o := videoOptions(t, filepath.Join(dir, "clip.mp4"))
	o.Env = fakeFFmpegEnviron()

	_, err := EncodeVideo(context.Background(), o, func(_ context.Context, i int) (*image.RGBA, error) {
		if i == 1 {
			return solidFrame(o.Width+1, o.Height, color.RGBA{A: 255}), nil
		}
		return solidFrame(o.Width, o.Height, color.RGBA{A: 255}), nil
	})
	if err == nil || !strings.Contains(err.Error(), "same size") {
		t.Fatalf("err = %v", err)
	}
}

func TestEncodeVideoRejectsANilFrame(t *testing.T) {
	dir := t.TempDir()
	o := videoOptions(t, filepath.Join(dir, "clip.mp4"))
	o.Env = fakeFFmpegEnviron()
	_, err := EncodeVideo(context.Background(), o, func(context.Context, int) (*image.RGBA, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "no image") {
		t.Fatalf("err = %v", err)
	}
}

func TestEncodeVideoStopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	o := videoOptions(t, filepath.Join(dir, "clip.mp4"))
	o.Frames = 100
	o.Env = fakeFFmpegEnviron()
	ctx, cancel := context.WithCancel(context.Background())

	_, err := EncodeVideo(ctx, o, func(_ context.Context, i int) (*image.RGBA, error) {
		if i == 2 {
			cancel()
		}
		return solidFrame(o.Width, o.Height, color.RGBA{A: 255}), nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestEncodeVideoArgumentChecks(t *testing.T) {
	dir := t.TempDir()
	frame := func(context.Context, int) (*image.RGBA, error) { return solidFrame(2, 2, color.RGBA{A: 255}), nil }
	base := videoOptions(t, filepath.Join(dir, "c.mp4"))

	for _, tt := range []struct {
		name   string
		mutate func(*VideoOptions)
		want   string
	}{
		{"no frames", func(o *VideoOptions) { o.Frames = 0 }, "no frames"},
		{"no size", func(o *VideoOptions) { o.Width = 0 }, "frame size"},
		{"no output", func(o *VideoOptions) { o.Output = "" }, "no output path"},
		{"no ffmpeg", func(o *VideoOptions) { o.Bin = "crier-no-such-ffmpeg" }, "ffmpeg was not found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := base
			tt.mutate(&o)
			if _, err := EncodeVideo(context.Background(), o, frame); err == nil ||
				!strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCheckFFmpeg(t *testing.T) {
	if err := CheckFFmpeg("crier-no-such-ffmpeg"); !errors.Is(err, ErrFFmpegMissing) {
		t.Errorf("err = %v", err)
	}
	if err := CheckFFmpeg(os.Args[0]); err != nil {
		t.Errorf("an existing binary should pass: %v", err)
	}
}

func TestFFmpegArgs(t *testing.T) {
	got := FFmpegArgs(VideoOptions{Output: "out.mp4", Width: 100, Height: 50, FPS: 25})
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"-f rawvideo", "-pix_fmt rgba", "-s 100x50", "-r 25", "-i -",
		"-c:v libx264", "-pix_fmt yuv420p", "-movflags +faststart",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if got[len(got)-1] != "out.mp4" {
		t.Errorf("the output must come last, got %q", got[len(got)-1])
	}
}

func TestFFmpegArgsOddSizeGetsScaled(t *testing.T) {
	joined := strings.Join(FFmpegArgs(VideoOptions{Output: "o.mp4", Width: 101, Height: 50}), " ")
	if !strings.Contains(joined, "scale=trunc(iw/2)*2") {
		t.Errorf("an odd dimension needs the scale filter: %q", joined)
	}
	joined = strings.Join(FFmpegArgs(VideoOptions{Output: "o.mp4", Width: 100, Height: 50}), " ")
	if strings.Contains(joined, "scale=trunc") {
		t.Errorf("an even size needs no filter: %q", joined)
	}
}

func TestFFmpegArgsAudio(t *testing.T) {
	joined := strings.Join(FFmpegArgs(VideoOptions{Output: "o.mp4", Width: 10, Height: 10, Audio: "a.mp3"}), " ")
	for _, want := range []string{"-i a.mp3", "-c:a aac", "-shortest"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
}

// TestFFmpegArgsAudioLoop: a slideshow can outlast its soundtrack, and
// -shortest would cut the video at the audio's end. The loop repeats the
// track and -shortest then ends the loop with the video instead — and
// -stream_loop has to stand before the input it applies to, or ffmpeg
// reads it as an option for the output.
func TestFFmpegArgsAudioLoop(t *testing.T) {
	joined := strings.Join(FFmpegArgs(VideoOptions{
		Output: "o.mp4", Width: 10, Height: 10, Audio: "a.mp3", AudioLoop: true,
	}), " ")
	if !strings.Contains(joined, "-stream_loop -1 -i a.mp3") {
		t.Errorf("the loop must precede its input: %q", joined)
	}
	if !strings.Contains(joined, "-shortest") {
		t.Errorf("the loop still ends with the video: %q", joined)
	}
	plain := strings.Join(FFmpegArgs(VideoOptions{
		Output: "o.mp4", Width: 10, Height: 10, Audio: "a.mp3",
	}), " ")
	if strings.Contains(plain, "-stream_loop") {
		t.Errorf("no loop unless asked: %q", plain)
	}
}

func TestFFmpegArgsPresets(t *testing.T) {
	for preset, want := range map[string]string{
		"h264": "libx264",
		"h265": "libx265",
		"vp9":  "libvpx-vp9",
	} {
		joined := strings.Join(FFmpegArgs(VideoOptions{Output: "o.mp4", Width: 10, Height: 10, Preset: preset}), " ")
		if !strings.Contains(joined, want) {
			t.Errorf("preset %q: missing %q in %q", preset, want, joined)
		}
	}
	joined := strings.Join(FFmpegArgs(VideoOptions{
		Output: "o.mp4", Width: 10, Height: 10, Preset: "none", ExtraArgs: []string{"-c:v", "mine"},
	}), " ")
	if strings.Contains(joined, "libx264") {
		t.Errorf("preset none should add no codec: %q", joined)
	}
	if !strings.Contains(joined, "-c:v mine") {
		t.Errorf("extra args should be kept: %q", joined)
	}
}

func TestFrameCount(t *testing.T) {
	for _, tt := range []struct {
		frames, fps int
		d           time.Duration
		want        int
	}{
		{0, 30, 3 * time.Second, 90},
		{7, 30, time.Hour, 7},
		{0, 0, time.Second, 0},
		{0, 30, 0, 0},
		{0, 30, time.Millisecond, 1},
	} {
		if got := FrameCount(tt.frames, tt.fps, tt.d); got != tt.want {
			t.Errorf("FrameCount(%d,%d,%v) = %d, want %d", tt.frames, tt.fps, tt.d, got, tt.want)
		}
	}
}

func TestFrameVars(t *testing.T) {
	v := FrameVars(0, 4, 10)
	if v["Frame"] != 0 || v["Frames"] != 4 || v["Time"] != 0.0 || v["Progress"] != 0.0 {
		t.Errorf("first frame = %v", v)
	}
	v = FrameVars(3, 4, 10)
	if v["Progress"] != 1.0 {
		t.Errorf("last frame progress = %v", v["Progress"])
	}
	if v["Time"] != 0.3 {
		t.Errorf("last frame time = %v", v["Time"])
	}
	v = FrameVars(0, 1, 0)
	if v["Progress"] != 0.0 || v["Time"] != 0.0 {
		t.Errorf("single frame = %v", v)
	}
}
