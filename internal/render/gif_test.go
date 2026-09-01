package render

import (
	"strings"
	"testing"
)

// TestGIFArgsUseTheSinglePassPalette is the one thing that makes a rendered
// gradient survive a 256-colour palette.
//
// ffmpeg's default palette is a fixed table, and a card with a gradient
// background comes out of it in visible bands. palettegen derives the palette
// from this clip's own frames; the split is what lets one command do both.
func TestGIFArgsUseTheSinglePassPalette(t *testing.T) {
	args := FFmpegArgs(VideoOptions{
		Output: "out.gif", Format: "gif", Width: 640, Height: 480, FPS: 12,
		Frames: 24,
	})
	line := strings.Join(args, " ")

	if !strings.Contains(line, "palettegen") || !strings.Contains(line, "paletteuse") {
		t.Errorf("no palette filter: %s", line)
	}
	if !strings.Contains(line, "split[s0][s1]") {
		t.Errorf("the filter has to split the stream: %s", line)
	}
	if !strings.Contains(line, "-loop 0") {
		t.Errorf("a GIF should loop forever: %s", line)
	}
	if !strings.Contains(line, "-r 12") {
		t.Errorf("the frame rate did not reach ffmpeg: %s", line)
	}
	if args[len(args)-1] != "out.gif" {
		t.Errorf("the output has to come last: %v", args)
	}

	// None of the video codec arguments belong on a GIF.
	for _, unwanted := range []string{"libx264", "yuv420p", "faststart", "-c:a"} {
		if strings.Contains(line, unwanted) {
			t.Errorf("%s should not be in a GIF command: %s", unwanted, line)
		}
	}
}

// TestGIFDropsAudio: a GIF carries none, and passing an audio input to a
// filter graph that has no audio branch makes ffmpeg fail rather than ignore
// it.
func TestGIFDropsAudio(t *testing.T) {
	args := FFmpegArgs(VideoOptions{
		Output: "out.gif", Format: "gif", Width: 8, Height: 8, Audio: "track.mp3",
	})
	if strings.Contains(strings.Join(args, " "), "track.mp3") {
		t.Errorf("audio reached a GIF encode: %v", args)
	}
}

func TestVideoArgsAreUnchangedByTheGIFSupport(t *testing.T) {
	args := FFmpegArgs(VideoOptions{Output: "out.mp4", Width: 640, Height: 480, FPS: 30})
	line := strings.Join(args, " ")
	if !strings.Contains(line, "libx264") || !strings.Contains(line, "+faststart") {
		t.Errorf("the mp4 default changed: %s", line)
	}
	if strings.Contains(line, "palettegen") {
		t.Errorf("a palette filter reached an mp4: %s", line)
	}
}

func TestIsGIFAndExt(t *testing.T) {
	for _, yes := range []string{"gif", "GIF", " gif "} {
		if !IsGIF(yes) {
			t.Errorf("IsGIF(%q) = false", yes)
		}
		if VideoExt(yes) != ".gif" {
			t.Errorf("VideoExt(%q) = %q", yes, VideoExt(yes))
		}
	}
	for _, no := range []string{"", "mp4", "webm"} {
		if IsGIF(no) {
			t.Errorf("IsGIF(%q) = true", no)
		}
		if VideoExt(no) != ".mp4" {
			t.Errorf("VideoExt(%q) = %q", no, VideoExt(no))
		}
	}
}
