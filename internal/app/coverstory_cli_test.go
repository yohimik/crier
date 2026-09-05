package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestPublishDryRunPlansPhotoFanoutAndCoverStory is the command-level contract:
// one invocation keeps the two-page photo publication for every enabled
// destination and adds one distinct Instagram music Story.
func TestPublishDryRunPlansPhotoFanoutAndCoverStory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ffmpeg is a POSIX shell script")
	}
	dir := t.TempDir()
	write(t, dir, "template.html", `<html><body><style>
body { margin: 0; background: white; font-family: Go }
.page { height: 160px; break-after: page }
</style><div class="page">one</div><div class="page">two</div></body></html>`)
	write(t, dir, "music.mp3", jingleBytes)
	ffmpeg := write(t, dir, "ffmpeg", "#!/bin/sh\nif [ \"${1-}\" = -version ]; then exit 0; fi\nout=\nfor out do :; done\ncat >/dev/null\nprintf '\\000\\000\\000\\040ftypisom' >\"$out\"\n")
	if err := os.Chmod(ffmpeg, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "crier.yaml", fmt.Sprintf(`render:
  template: template.html
  width: 200
  height: 200
  hermetic-fonts: true
  video:
    audio: music.mp3
    ffmpeg-bin: %s
publish:
  dry-run: true
  instagram:
    enabled: true
    token: ig-token
    user-id: ig-user
    caption: feed words
    cover-story: true
  linkedin:
    enabled: true
    token: li-token
    author-urn: urn:li:person:test
  discord:
    enabled: true
    webhook-url: https://discord.example/webhook
stage:
  mode: url
  url: https://media.example/photo.jpg
`, filepath.ToSlash(ffmpeg)))

	code, stdout, stderr := run(t, dir, nil, "publish", "--json")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var report PublishReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("publish report is not JSON: %v\n%s", err, stdout)
	}
	if !report.DryRun || len(report.Results) != 0 {
		t.Fatalf("dry-run report = %+v", report)
	}
	if len(report.Plan) != 4 {
		t.Fatalf("planned %d publications, want three photo destinations and one cover Story: %+v", len(report.Plan), report.Plan)
	}

	var got []string
	for _, plan := range report.Plan {
		got = append(got, plan.Platform+":"+plan.Variant+fmt.Sprintf(":%d", plan.Files))
		if plan.Variant == "instagram-cover-story" && plan.Caption != "" {
			t.Errorf("cover Story caption = %q, want none because Instagram Stories carry no caption", plan.Caption)
		}
		if plan.Platform == "instagram" && plan.Variant != "instagram-cover-story" && plan.Caption != "feed words" {
			t.Errorf("Instagram feed caption = %q, want feed words", plan.Caption)
		}
	}
	sort.Strings(got)
	want := []string{
		"discord:instagram-discord-linkedin:2",
		"instagram:instagram-cover-story:1",
		"instagram:instagram-discord-linkedin:2",
		"linkedin:instagram-discord-linkedin:2",
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plan =\n%s\nwant =\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
