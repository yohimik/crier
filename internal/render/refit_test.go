package render

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
)

// TestRefitArgsCopiesTheAudio is the whole shape of the command: the filter
// that does the fitting, the video codec, and an audio stream that is carried
// over rather than encoded again.
//
// Re-encoding the audio would cost a generation of quality to produce the same
// soundtrack, and on the announcement clip the soundtrack is the point.
func TestRefitArgsCopiesTheAudio(t *testing.T) {
	filter := FitFilter(1080, 1920, config.FitContain, "#04140c")
	args := RefitArgs(RefitOptions{
		Input: "in.mp4", Output: "out.mp4", Filter: filter,
		Width: 1080, Height: 1920,
	})
	line := strings.Join(args, " ")

	for _, want := range []string{
		"-i in.mp4",
		"-vf " + filter,
		"-c:v libx264",
		"-c:a copy",
		"out.mp4",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in: %s", want, line)
		}
	}
	if strings.Contains(line, "-c:a aac") {
		t.Errorf("the audio is re-encoded: %s", line)
	}
	// The pad colour is the platform's, spelled the way a filter graph wants
	// it. A missing one is how a letterbox comes out black.
	if !strings.Contains(line, "0x04140c") {
		t.Errorf("the letterbox is not the configured colour: %s", line)
	}
	if args[len(args)-1] != "out.mp4" {
		t.Errorf("the output has to come last: %s", line)
	}
}

func TestRefitVideoNeedsAFilterAndAnOutput(t *testing.T) {
	log := zerolog.New(zerolog.NewTestWriter(t))
	if _, err := RefitVideo(context.Background(), RefitOptions{
		Input: "in.mp4", Output: "out.mp4", Logger: log,
	}); err == nil || !strings.Contains(err.Error(), "no filter") {
		t.Errorf("err = %v", err)
	}
	if _, err := RefitVideo(context.Background(), RefitOptions{
		Input: "in.mp4", Filter: "scale=2:2", Logger: log,
	}); err == nil || !strings.Contains(err.Error(), "no output") {
		t.Errorf("err = %v", err)
	}
}

// TestRefitArgsPresetIsHonoured: the codec preset is the run's, so a clip
// refitted for a platform is encoded the same way a rendered one would be.
func TestRefitArgsPresetIsHonoured(t *testing.T) {
	line := strings.Join(RefitArgs(RefitOptions{
		Input: "in.mp4", Output: "out.mp4", Filter: "scale=2:2", Preset: "h265",
	}), " ")
	if !strings.Contains(line, "-c:v libx265") {
		t.Errorf("preset ignored: %s", line)
	}
}
