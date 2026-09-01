package app

import (
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/render"
)

// TestPrimaryAsksAboutTheArtifactsOwnKind is a regression test.
//
// Primary checked KindVideo whatever the clip actually was, so a GIF — which
// lives in the same field — passed the check for a platform that takes video
// and not animations, and would have been uploaded to Instagram as if it were
// an MP4.
func TestPrimaryAsksAboutTheArtifactsOwnKind(t *testing.T) {
	gif := render.Artifact{Kind: render.KindGIF, ContentType: render.GIFContentType, Path: "clip.gif"}
	video := render.Artifact{Kind: render.KindVideo, ContentType: render.VideoContentType, Path: "clip.mp4"}

	videoOnly := publish.Needs{Kinds: []render.Kind{render.KindImage, render.KindVideo}}
	both := publish.Needs{Kinds: []render.Kind{render.KindImage, render.KindVideo, render.KindGIF}}

	if _, err := (Artifacts{Video: &gif}).Primary(videoOnly); err == nil {
		t.Error("a GIF was accepted by a platform that only takes video")
	} else if !strings.Contains(err.Error(), "gif") {
		t.Errorf("the refusal should name the kind: %v", err)
	}
	if _, err := (Artifacts{Video: &gif}).Primary(both); err != nil {
		t.Errorf("a platform that takes GIFs should get one: %v", err)
	}
	if _, err := (Artifacts{Video: &video}).Primary(videoOnly); err != nil {
		t.Errorf("a video should still work: %v", err)
	}
}

func TestDescribeKind(t *testing.T) {
	for kind, want := range map[render.Kind]string{
		render.KindGIF:   "an animated GIF",
		render.KindVideo: "a video",
		render.KindImage: "an image",
	} {
		if got := describeKind(kind); got != want {
			t.Errorf("describeKind(%s) = %q, want %q", kind, got, want)
		}
	}
}

// TestPublishInputKindIsCheckedEarly: a file crier was handed has whatever
// kind its bytes say, and a platform that cannot take it should be told before
// anything is staged.
func TestPublishInputKindIsCheckedEarly(t *testing.T) {
	dir := t.TempDir()
	gif := writeBytes(t, dir, "clip.gif", []byte("GIF89a"+strings.Repeat("\x00", 16)))
	write(t, dir, "crier.yaml", strings.Join([]string{
		"publish:",
		"  input: " + gif,
		"  linkedin:",
		"    enabled: true",
		"    token: li",
		"    author-urn: \"urn:li:person:x\"",
	}, "\n"))

	code, _, stderr := run(t, dir, []string{}, "publish")
	if code != ExitConfig {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "linkedin") || !strings.Contains(stderr, "animated GIF") {
		t.Errorf("the refusal should name the platform and the kind: %q", stderr)
	}

	// The same file at a platform that takes one is fine as far as the check
	// goes; a dry run stops before the network.
	write(t, dir, "crier.yaml", strings.Join([]string{
		"publish:",
		"  input: " + gif,
		"  dry-run: true",
		"  discord:",
		"    enabled: true",
		"    webhook-url: https://example.test/hook",
	}, "\n"))
	if code, _, stderr := run(t, dir, []string{}, "publish"); code != ExitOK {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

// A guard on the layout table: a custom platform has to be reachable through
// the same helper the built-ins are, or its overlay and size would be ignored.
func TestLayoutOfKnowsCustomPlatforms(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Custom = map[string]*config.Custom{
		"hook": {Layout: config.Layout{Width: 640, Height: 480}},
	}
	l := config.LayoutOf(&cfg.Publish, "hook")
	if l == nil || l.Width != 640 || l.Height != 480 {
		t.Fatalf("layout = %+v", l)
	}
	if config.LayoutOf(&cfg.Publish, "nobody") != nil {
		t.Error("an unknown name should have no layout")
	}
}
