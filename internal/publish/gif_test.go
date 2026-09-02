package publish

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// gifArtifact is an animation on disk, with the magic bytes a fake can check.
func gifArtifact(t *testing.T, size int) render.Artifact {
	t.Helper()
	a := artifact(t, render.KindGIF, "", "GIF89a"+strings.Repeat("\x00", size))
	a.ContentType = render.GIFContentType
	return a
}

// TestGIFSupportMatrix is the routing table as a test: seven platforms take an
// animation and five do not, and a configuration that mixes them has to fail
// before anything is rendered rather than at the API.
func TestGIFSupportMatrix(t *testing.T) {
	const at = "https://example.test"
	byName := map[string]*config.Config{
		"telegram":  telegramConfig(at),
		"mastodon":  mastodonConfig(at),
		"instagram": instagramConfig(at),
		"facebook":  facebookConfig(at),
		"tiktok":    tiktokConfig(at),
		"linkedin":  linkedinConfig(at),
		"reddit":    redditConfig(at, at),
		"slack":     slackConfig(at),
		"vk":        vkConfig(at, vkCommunity),
		"threads":   threadsConfig(at),
	}
	discord := config.Defaults()
	discord.Publish.Discord.Enabled = true
	discord.Publish.Discord.WebhookURL = at + "/webhook"
	byName["discord"] = &discord

	xCfg := config.Defaults()
	xCfg.Publish.X.Enabled = true
	xCfg.Publish.X.APIBaseURL = at
	xCfg.Publish.X.Token = "tok"
	byName["x"] = &xCfg

	for name, want := range map[string]bool{
		"telegram": true, "discord": true, "mastodon": true, "x": true, "reddit": true,
		"slack":     true,
		"vk":        true,
		"instagram": false, "facebook": false, "tiktok": false, "linkedin": false,
		"threads": false,
	} {
		p := onlyPublisher(t, byName[name])
		if got := p.Needs().Accepts(render.KindGIF); got != want {
			t.Errorf("%s accepts a GIF = %t, want %t", name, got, want)
		}
		// Every platform still takes a video, GIF support or not.
		if !p.Needs().Accepts(render.KindVideo) {
			t.Errorf("%s stopped accepting video", name)
		}
	}
}

// TestTelegramSendsAnAnimation is the switch that matters: sendVideo with a
// GIF is accepted and then shown as a still.
func TestTelegramSendsAnAnimation(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9,"chat":{"id":1}}}`))
	})

	p := onlyPublisher(t, telegramConfig(srv.URL))
	if _, err := p.Publish(context.Background(), Input{Artifact: gifArtifact(t, 100)}); err != nil {
		t.Fatal(err)
	}
	req := rec.all()[0]
	if !strings.HasSuffix(req.Path, "/sendAnimation") {
		t.Fatalf("path = %q, want sendAnimation", req.Path)
	}
	if !strings.Contains(req.Body, `name="animation"`) {
		t.Errorf("the part should be named animation: %q", req.Body)
	}
	if !strings.Contains(req.Body, "GIF89a") {
		t.Errorf("the animation itself did not go out")
	}
}

// TestXUsesTheGIFCategory: a GIF uploaded as tweet_video comes out as a silent
// video rather than an animation.
func TestXUsesTheGIFCategory(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		switch {
		case strings.Contains(req.Body, "INIT"):
			_, _ = w.Write([]byte(`{"data":{"id":"xm-9"}}`))
		case strings.Contains(req.Body, "APPEND"):
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(req.Body, "FINALIZE"):
			_, _ = w.Write([]byte(`{"data":{"id":"xm-9","processing_info":{"state":"succeeded"}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"id":"t-9"}}`))
		}
	})

	cfg := config.Defaults()
	cfg.Publish.X.Enabled = true
	cfg.Publish.X.APIBaseURL = srv.URL
	cfg.Publish.X.Token = "tok"

	if _, err := onlyPublisher(t, &cfg).Publish(context.Background(),
		Input{Artifact: gifArtifact(t, 1000), Caption: "look"}); err != nil {
		t.Fatal(err)
	}
	init := rec.all()[0]
	if !strings.Contains(init.Body, "media_category=tweet_gif") {
		t.Errorf("INIT body = %q, want tweet_gif", init.Body)
	}
	if !strings.Contains(init.Body, "image%2Fgif") {
		t.Errorf("the media type did not go out: %q", init.Body)
	}
}

// TestXRefusesAnOversizedGIF: X's animation limit is a thirty-fourth of its
// video limit, and finding that out after uploading 20MB is a slow no.
func TestXRefusesAnOversizedGIF(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("nothing should have been uploaded")
		w.WriteHeader(http.StatusInternalServerError)
	})
	cfg := config.Defaults()
	cfg.Publish.X.Enabled = true
	cfg.Publish.X.APIBaseURL = srv.URL
	cfg.Publish.X.Token = "tok"

	big := gifArtifact(t, 8)
	big.Size = XGIFLimit + 1

	_, err := onlyPublisher(t, &cfg).Publish(context.Background(), Input{Artifact: big})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want a size refusal", err)
	}
	// The video limit is not what a GIF is measured against.
	if !strings.Contains(err.Error(), "15.0MB") {
		t.Errorf("the message should name the GIF limit: %v", err)
	}
}

// TestRedditSubmitsAGIFAsAnImage: the asset store is told image/gif and keeps
// the file animated; `videogif` is for an MP4 standing in for one.
func TestRedditSubmitsAGIFAsAnImage(t *testing.T) {
	rec := newRecorder()
	api, auth, _ := fakeReddit(t, rec)
	cfg := redditConfig(api, auth)

	if _, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: gifArtifact(t, 50), Caption: "an animation"}); err != nil {
		t.Fatal(err)
	}
	lease, ok := findRequest(rec, "/api/media/asset.json")
	if !ok {
		t.Fatal("no lease was taken")
	}
	if !strings.Contains(lease.Body, "image%2Fgif") {
		t.Errorf("the lease mime type = %q, want image/gif", lease.Body)
	}
	submit, ok := findRequest(rec, "/api/submit")
	if !ok {
		t.Fatal("nothing was submitted")
	}
	if !strings.Contains(submit.Body, "kind=image") {
		t.Errorf("submit body = %q, want kind=image", submit.Body)
	}
}

// findRequest is the first recorded request whose path contains fragment.
func findRequest(r *recorder, fragment string) (recorded, bool) {
	for _, req := range r.all() {
		if strings.Contains(req.Path, fragment) {
			return req, true
		}
	}
	return recorded{}, false
}
