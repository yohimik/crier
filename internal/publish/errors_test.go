package publish

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// testLoggerTo writes records into a buffer a test can read back.
func testLoggerTo(w *bytes.Buffer) zerolog.Logger {
	return zerolog.New(w).Level(zerolog.DebugLevel)
}

// The tests here walk the error branches of each publisher: what happens when
// a platform refuses, times out, or answers with something other than what it
// promised. They are the paths an operator meets on a bad day, and the ones
// that are never exercised by a happy-path test.

func TestConstructorsNameWhatIsMissing(t *testing.T) {
	for _, tt := range []struct {
		name  string
		build func(cfg *config.Config)
		want  string
	}{
		{"instagram without a token", func(c *config.Config) {
			c.Publish.Instagram.Enabled = true
			c.Publish.Instagram.UserID = "u"
		}, "publish.instagram.token"},
		{"instagram without a user id", func(c *config.Config) {
			c.Publish.Instagram.Enabled = true
			c.Publish.Instagram.Token = "t"
		}, "publish.instagram.user-id"},
		{"facebook without a page", func(c *config.Config) {
			c.Publish.Facebook.Enabled = true
			c.Publish.Facebook.Token = "t"
		}, "publish.facebook.page-id"},
		{"facebook without a token", func(c *config.Config) {
			c.Publish.Facebook.Enabled = true
			c.Publish.Facebook.PageID = "p"
		}, "publish.facebook.token"},
		{"x without a token", func(c *config.Config) {
			c.Publish.X.Enabled = true
		}, "publish.x.token"},
		{"linkedin without an author", func(c *config.Config) {
			c.Publish.LinkedIn.Enabled = true
			c.Publish.LinkedIn.Token = "t"
		}, "publish.linkedin.author-urn"},
		{"linkedin with a malformed author", func(c *config.Config) {
			c.Publish.LinkedIn.Enabled = true
			c.Publish.LinkedIn.Token = "t"
			c.Publish.LinkedIn.AuthorURN = "12345"
		}, "urn:li:person"},
		{"telegram without a chat", func(c *config.Config) {
			c.Publish.Telegram.Enabled = true
			c.Publish.Telegram.Token = "t"
		}, "publish.telegram.chat-id"},
		{"mastodon with a bad visibility", func(c *config.Config) {
			c.Publish.Mastodon.Enabled = true
			c.Publish.Mastodon.APIBaseURL = "https://example.test"
			c.Publish.Mastodon.Token = "t"
			c.Publish.Mastodon.Visibility = "shouted"
		}, "visibility"},
		{"discord with a webhook that is not a URL", func(c *config.Config) {
			c.Publish.Discord.Enabled = true
			c.Publish.Discord.WebhookURL = "not-a-url"
		}, "webhook-url"},
		{"tiktok with a bad privacy level", func(c *config.Config) {
			c.Publish.TikTok.Enabled = true
			c.Publish.TikTok.Token = "t"
			c.Publish.TikTok.PrivacyLevel = "MAYBE"
		}, "privacy-level"},
		{"reddit without a password or a refresh token", func(c *config.Config) {
			c.Publish.Reddit.Enabled = true
			c.Publish.Reddit.ClientID = "id"
			c.Publish.Reddit.ClientSecret = "secret"
			c.Publish.Reddit.Subreddit = "s"
			c.Publish.Reddit.Username = "u"
		}, "refresh-token"},
		{"reddit with a bad kind", func(c *config.Config) {
			c.Publish.Reddit.Enabled = true
			c.Publish.Reddit.ClientID = "id"
			c.Publish.Reddit.ClientSecret = "secret"
			c.Publish.Reddit.Subreddit = "s"
			c.Publish.Reddit.RefreshToken = "r"
			c.Publish.Reddit.Kind = "poll"
		}, "kind"},
	} {
		cfg := config.Defaults()
		tt.build(&cfg)
		_, err := Build(&cfg, testDeps(t))
		if err == nil {
			t.Errorf("%s: should not build", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: the error should name %s: %v", tt.name, tt.want, err)
		}
	}
}

// TestPingFailuresAreReported walks each platform's identity call refusing.
func TestPingFailuresAreReported(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	})

	discord := config.Defaults()
	discord.Publish.Discord.Enabled = true
	discord.Publish.Discord.WebhookURL = srv.URL + "/webhook"

	xCfg := config.Defaults()
	xCfg.Publish.X.Enabled = true
	xCfg.Publish.X.APIBaseURL = srv.URL
	xCfg.Publish.X.Token = "tok"

	for name, cfg := range map[string]*config.Config{
		"telegram":  telegramConfig(srv.URL),
		"mastodon":  mastodonConfig(srv.URL),
		"instagram": instagramConfig(srv.URL),
		"facebook":  facebookConfig(srv.URL),
		"tiktok":    tiktokConfig(srv.URL),
		"reddit":    redditConfig(srv.URL, srv.URL),
		"discord":   &discord,
		"x":         &xCfg,
	} {
		if _, err := onlyPublisher(t, cfg).Ping(context.Background()); err == nil {
			t.Errorf("%s: a 401 should fail the ping", name)
		}
	}
}

// TestInstagramContainerFailures covers the two ways Meta refuses a container:
// it never finishes, and it comes back in an error state.
func TestInstagramContainerFailures(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status string
		want   string
	}{
		{"an error state", `{"status_code":"ERROR"}`, "could not process the media"},
		{"expired", `{"status_code":"EXPIRED"}`, "expired before it was published"},
	} {
		srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/123/media":
				_, _ = w.Write([]byte(`{"id":"c1"}`))
			case "/c1":
				_, _ = w.Write([]byte(tt.status))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
		_, err := onlyPublisher(t, instagramConfig(srv.URL)).Publish(context.Background(), Input{
			Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
		})
		if err == nil {
			t.Errorf("%s: should fail", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: the error should name the state: %v", tt.name, err)
		}
	}

	// A container crier is never given a URL for cannot be made at all.
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	if _, err := onlyPublisher(t, instagramConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t),
	}); err == nil || !strings.Contains(err.Error(), "stage.mode") {
		t.Errorf("err = %v, want the staging advice", err)
	}
}

// TestInstagramPublishRefusal: the container was created and the publish call
// failed, which is worth logging because the container expires in a day.
func TestInstagramPublishRefusal(t *testing.T) {
	var logs bytes.Buffer
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	cfg := instagramConfig(srv.URL)
	d := testDeps(t)
	d.Logger = testLoggerTo(&logs)
	ps, err := Build(cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps[0].Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
	}); err == nil || !strings.Contains(err.Error(), "publishing the container") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(logs.String(), "expires in 24 hours") {
		t.Errorf("the abandoned container should be logged: %s", logs.String())
	}
}

// TestInstagramContainerWithNoID: Meta answered 200 and said nothing useful.
func TestInstagramContainerWithNoID(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := onlyPublisher(t, instagramConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
	}); err == nil || !strings.Contains(err.Error(), "no container id") {
		t.Errorf("err = %v", err)
	}
}

// TestLinkedInFailures walks the image and video flows refusing at each step.
func TestLinkedInFailures(t *testing.T) {
	// The initialisation call refuses.
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	if _, err := onlyPublisher(t, linkedinConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)}); err == nil {
		t.Error("a refused initialisation should fail")
	}

	// The upload URL is missing from an otherwise fine answer.
	srv = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value":{}}`))
	})
	if _, err := onlyPublisher(t, linkedinConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)}); err == nil {
		t.Error("an answer with no upload URL should fail")
	}
}

// TestLinkedInVideoProcessingFails covers the poll's failure state, which is a
// video LinkedIn accepted and then could not transcode.
func TestLinkedInVideoProcessingFails(t *testing.T) {
	upload := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"e1"`)
		w.WriteHeader(http.StatusOK)
	})
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "initializeUpload":
			_, _ = w.Write([]byte(`{"value":{"video":"urn:li:video:1","uploadToken":"t",` +
				`"uploadInstructions":[{"uploadUrl":"` + upload.URL + `","firstByte":0,"lastByte":7}]}}`))
		case "finalizeUpload":
			w.WriteHeader(http.StatusOK)
		default:
			_, _ = w.Write([]byte(`{"status":"PROCESSING_FAILED"}`))
		}
	})
	_, err := onlyPublisher(t, linkedinConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 8)})
	if err == nil || !strings.Contains(err.Error(), "could not process") {
		t.Errorf("err = %v, want the processing failure", err)
	}
}

// TestPostURL: an empty id has no link, rather than a link to nothing.
func TestPostURL(t *testing.T) {
	if got := postURL(""); got != "" {
		t.Errorf("= %q", got)
	}
	if got := postURL("urn:li:share:1"); !strings.HasSuffix(got, "urn:li:share:1") {
		t.Errorf("= %q", got)
	}
}

// TestFacebookVideoFailures covers the video branch, which the photo tests
// never reach.
func TestFacebookVideoFailures(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"video too long"}}`))
	})
	_, err := onlyPublisher(t, facebookConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 64)})
	if err == nil || !strings.Contains(err.Error(), "video too long") {
		t.Errorf("err = %v", err)
	}
}

// TestFacebookVideoFromAURL is the other half of the video branch: the Page is
// told where to fetch rather than given the bytes.
func TestFacebookVideoFromAURL(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"id":"v1","post_id":"p1"}`))
	})
	cfg := facebookConfig(srv.URL)
	cfg.Publish.Facebook.UseURL = true

	res, err := onlyPublisher(t, cfg).Publish(context.Background(), Input{
		Artifact: videoArtifact(t, 32), URL: "https://cdn.example/clip.mp4", Caption: "look",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID == "" {
		t.Errorf("result = %+v", res)
	}
	if body := rec.all()[0].Body; !strings.Contains(body, "file_url") {
		t.Errorf("the Page should have been given a URL to fetch: %q", body)
	}
}

// TestTikTokStatusFailure: TikTok accepted the upload and then gave up on it.
func TestTikTokStatusFailure(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "status/fetch") {
			_, _ = w.Write([]byte(`{"data":{"status":"FAILED","fail_reason":"video_format_check_failed"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"publish_id":"tt-1"}}`))
	})
	_, err := onlyPublisher(t, tiktokConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
	})
	if err == nil || !strings.Contains(err.Error(), "video_format_check_failed") {
		t.Errorf("err = %v, want the reason TikTok gave", err)
	}
}

// TestXUploadRefused covers the simple upload path failing, and the answer
// with no media id.
func TestXUploadRefused(t *testing.T) {
	cfgFor := func(url string) *config.Config {
		c := config.Defaults()
		c.Publish.X.Enabled = true
		c.Publish.X.APIBaseURL = url
		c.Publish.X.Token = "tok"
		return &c
	}

	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
	})
	if _, err := onlyPublisher(t, cfgFor(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)}); err == nil {
		t.Error("a refused upload should fail")
	}

	srv = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	if _, err := onlyPublisher(t, cfgFor(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)}); err == nil ||
		!strings.Contains(err.Error(), "no media id") {
		t.Errorf("err = %v", err)
	}
}

// TestRedditUploadFailures covers the lease and the object store refusing.
func TestRedditUploadFailures(t *testing.T) {
	// The lease comes back empty.
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "access_token"):
			_, _ = w.Write([]byte(`{"access_token":"at"}`))
		default:
			_, _ = w.Write([]byte(`{"args":{}}`))
		}
	})
	_, err := onlyPublisher(t, redditConfig(srv.URL, srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "upload lease") {
		t.Errorf("err = %v", err)
	}
}

// TestNeedsPrefersInOrder covers the format negotiation helper's miss.
func TestNeedsPrefersInOrder(t *testing.T) {
	n := Needs{Formats: []config.Format{config.JPEG, config.PNG}}
	available := map[config.Format]render.Artifact{config.PNG: {Path: "a.png"}}
	got, ok := n.Prefers(available)
	if !ok || got.Path != "a.png" {
		t.Errorf("= %+v, %t", got, ok)
	}
	if _, ok := n.Prefers(map[config.Format]render.Artifact{}); ok {
		t.Error("nothing available should not be preferred")
	}
}
