//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPhotoFanoutAndMusicCoverStoryLandFromOneCommand drives the released
// interface once. The platform servers and ffmpeg are local fakes, but the
// crier process, render, pagination, staging, fan-out and request encoders are
// the same ones a real publish uses.
func TestPhotoFanoutAndMusicCoverStoryLandFromOneCommand(t *testing.T) {
	f := newFakes(t)
	addr := freeAddr(t)
	dir := newPagedProject(t, strings.Join([]string{
		"  caption: \"release pages\"",
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: ig-token",
		"    user-id: ig-user",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
		"  linkedin:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/linkedin",
		"    token: li-token",
		"    author-urn: urn:li:person:e2e",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
	}, "\n")+"\nstage:\n  mode: server\n  server:\n    listen: "+addr+
		"\n    public-url: http://"+addr+"\n")
	writeFile(t, dir, "template.html", `<html><body><style>
body { margin: 0; background: white; font-family: Go }
.b { height: 90px; margin-bottom: 20px; break-inside: avoid }
@page { margin: 10px }
</style><div class="b">page one</div><div class="b">page two</div></body></html>`)
	writeFile(t, dir, "music.mp3", "ID3\x04\x00\x00\x00\x00\x00\x00music")

	res := crier(t, dir, []string{helperEnv + "=ffmpeg"}, "publish", "--json",
		"--render-video-audio", "music.mp3",
		"--render-video-ffmpeg-bin", selfPath(t),
		"--publish-instagram-cover-story",
		"--publish-discord-mention-everyone")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s stdout=%s", res.Code, res.Stderr, res.Stdout)
	}
	var report struct {
		Results []struct {
			Platform string `json:"platform"`
			OK       bool   `json:"ok"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &report); err != nil {
		t.Fatalf("report is not JSON: %v\n%s", err, res.Stdout)
	}
	if len(report.Results) != 4 {
		t.Fatalf("reported %d outcomes, want the three photo destinations and cover Story: %+v", len(report.Results), report.Results)
	}
	got := map[string]bool{}
	for _, outcome := range report.Results {
		got[outcome.Platform] = outcome.OK
	}
	for _, name := range []string{"instagram", "instagram-cover-story", "linkedin", "discord"} {
		if !got[name] {
			t.Errorf("%s did not publish successfully: %+v", name, report.Results)
		}
	}

	containers := igContainers(f)
	if len(containers) != 4 {
		t.Fatalf("Instagram created %d containers, want two feed children, one carousel parent, and one Story", len(containers))
	}
	var feedPages []string
	stories := 0
	for _, request := range containers {
		switch {
		case strings.Contains(request.Body, "media_type=STORIES"):
			stories++
			if !strings.Contains(request.Body, "video_url=") {
				t.Errorf("cover Story did not carry its staged MP4 URL: %q", request.Body)
			}
			if strings.Contains(request.Body, "caption=") {
				t.Errorf("cover Story carried a caption: %q", request.Body)
			}
		case strings.Contains(request.Body, "is_carousel_item=true"):
			feedPages = append(feedPages, pageNumbers(request.Body)...)
		}
	}
	if stories != 1 || joined(feedPages) != "1,2" {
		t.Errorf("Instagram stories=%d feed pages=%q, want one Story and pages 1,2", stories, joined(feedPages))
	}
	if f.count("/instagram/ig-user/media_publish") != 2 {
		t.Errorf("Instagram did not publish both the feed carousel and cover Story")
	}

	discord, ok := f.find("/discord/webhook")
	if !ok {
		t.Fatal("Discord was not called")
	}
	if f.count("/discord/webhook") != 1 {
		t.Errorf("Discord must receive exactly one message")
	}
	if gotPages := joined(pageNumbers(discord.Body)); gotPages != "1,2" {
		t.Errorf("Discord pages = %q, want 1,2", gotPages)
	}
	for _, want := range []string{"release pages", `"allowed_mentions":{"parse":["everyone"]}`} {
		if !strings.Contains(discord.Body, want) {
			t.Errorf("Discord multipart body is missing %q", want)
		}
	}

	if f.count("/linkedin/rest/posts") != 1 {
		t.Errorf("LinkedIn did not receive its photo carousel")
	}
}
