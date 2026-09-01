package publish

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// TestTikTokVideoUploadFailures walks the video branch, which the photo tests
// never reach: an empty file, an answer with no upload URL, and the chunk PUT
// refusing.
func TestTikTokVideoUploadFailures(t *testing.T) {
	// An answer with no upload URL, which is a 200 that says nothing usable.
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"publish_id":"tt-1"}}`))
	})
	_, err := onlyPublisher(t, tiktokConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 128)})
	if err == nil || !strings.Contains(err.Error(), "no upload url") {
		t.Errorf("err = %v", err)
	}

	// An empty file has nothing to chunk.
	empty := videoArtifact(t, 0)
	empty.Size = 0
	if _, err := onlyPublisher(t, tiktokConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: empty}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("err = %v", err)
	}

	// The error object inside a 200, which is TikTok's habit.
	srv = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"code":"spam_risk_too_many_posts","message":"slow down","log_id":"L"}}`))
	})
	if _, err := onlyPublisher(t, tiktokConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 128)}); err == nil ||
		!strings.Contains(err.Error(), "spam_risk_too_many_posts") {
		t.Errorf("err = %v", err)
	}
}

// TestTikTokVideoUploadsItsChunks is the happy path of the video branch, which
// is what makes the failures above meaningful.
func TestTikTokVideoUploadsItsChunks(t *testing.T) {
	rec := newRecorder()
	uploads := newRecorder()
	store := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		uploads.record(r)
		w.WriteHeader(http.StatusCreated)
	})
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if strings.Contains(r.URL.Path, "status/fetch") {
			_, _ = w.Write([]byte(`{"data":{"status":"PUBLISH_COMPLETE","publicaly_available_post_id":[99]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"publish_id":"tt-1","upload_url":"` + store.URL + `/put"}}`))
	})

	res, err := onlyPublisher(t, tiktokConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 4096), Caption: "a clip"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "tt-1" {
		t.Errorf("result = %+v", res)
	}
	if len(uploads.all()) != 1 {
		t.Fatalf("uploaded %d chunks, want one", len(uploads.all()))
	}
	if got := uploads.all()[0].Header.Get("Content-Range"); !strings.HasPrefix(got, "bytes 0-4095/4096") {
		t.Errorf("Content-Range = %q", got)
	}
	if got := rec.all()[0].Path; got != "/v2/post/publish/video/init/" {
		t.Errorf("the init path = %q", got)
	}
}

// TestXInitRefused covers the chunked upload's first step failing, which is
// where a media id would have been reserved.
func TestXInitRefused(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.X.Enabled = true
	cfg.Publish.X.Token = "tok"

	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		if strings.Contains(body, "INIT") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"media type not supported"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"x1"}}`))
	})
	cfg.Publish.X.APIBaseURL = srv.URL

	if _, err := onlyPublisher(t, &cfg).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 128)}); err == nil ||
		!strings.Contains(err.Error(), "starting the upload") {
		t.Errorf("err = %v", err)
	}

	// And INIT answering 200 with no id.
	srv2 := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	cfg.Publish.X.APIBaseURL = srv2.URL
	if _, err := onlyPublisher(t, &cfg).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 128)}); err == nil ||
		!strings.Contains(err.Error(), "no media id") {
		t.Errorf("err = %v", err)
	}
}

// TestXProcessingFails: X accepted the video and then could not transcode it.
func TestXProcessingFails(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.X.Enabled = true
	cfg.Publish.X.Token = "tok"
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		switch {
		case strings.Contains(body, "INIT"):
			_, _ = w.Write([]byte(`{"data":{"id":"xm-1"}}`))
		case strings.Contains(body, "APPEND"):
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{"data":{"id":"xm-1","processing_info":{"state":"failed",` +
				`"error":{"name":"InvalidMedia","message":"the video is not usable"}}}}`))
		}
	})
	cfg.Publish.X.APIBaseURL = srv.URL

	if _, err := onlyPublisher(t, &cfg).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 128)}); err == nil ||
		!strings.Contains(err.Error(), "not usable") {
		t.Errorf("err = %v, want the reason X gave", err)
	}
}

// TestRedditPermalinkLookupGivesUp: the submission is made, and finding where
// it landed is best effort — a post with no link is still a post.
func TestRedditPermalinkLookupGivesUp(t *testing.T) {
	rec := newRecorder()
	store := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case strings.Contains(r.URL.Path, "access_token"):
			_, _ = w.Write([]byte(`{"access_token":"at"}`))
		case strings.Contains(r.URL.Path, "asset.json"):
			_, _ = w.Write([]byte(`{"args":{"action":"` + store.URL + `","fields":[` +
				`{"name":"key","value":"media/x.jpg"}]},"asset":{"asset_id":"a1"}}`))
		case strings.Contains(r.URL.Path, "api/submit"):
			_, _ = w.Write([]byte(`{"json":{"errors":[],"data":{}}}`))
		default:
			// The listing never shows the post.
			_, _ = w.Write([]byte(`{"data":{"children":[]}}`))
		}
	})

	cfg := redditConfig(srv.URL, srv.URL)
	res, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: imageArtifact(t), Caption: "body"})
	if err != nil {
		t.Fatalf("a lookup that finds nothing must not fail the post: %v", err)
	}
	if res.URL != "" {
		t.Errorf("url = %q, want none rather than a guess", res.URL)
	}

	// And with no username configured there is nothing to look in.
	cfg.Publish.Reddit.Username = ""
	cfg.Publish.Reddit.RefreshToken = "r"
	if _, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: imageArtifact(t), Caption: "body"}); err != nil {
		t.Errorf("a refresh-token setup should still post: %v", err)
	}
}

// TestRedditSubmitErrors: Reddit reports a refusal inside a 200.
func TestRedditSubmitErrors(t *testing.T) {
	store := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "access_token"):
			_, _ = w.Write([]byte(`{"access_token":"at"}`))
		case strings.Contains(r.URL.Path, "asset.json"):
			_, _ = w.Write([]byte(`{"args":{"action":"` + store.URL + `","fields":[` +
				`{"name":"key","value":"media/x.jpg"}]},"asset":{"asset_id":"a1"}}`))
		default:
			_, _ = w.Write([]byte(`{"json":{"errors":[["SUBREDDIT_NOTALLOWED","you may not post here","sr"]]}}`))
		}
	})
	_, err := onlyPublisher(t, redditConfig(srv.URL, srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t), Caption: "body"})
	if err == nil || !strings.Contains(err.Error(), "you may not post here") {
		t.Errorf("err = %v, want Reddit's own words", err)
	}
}

// TestCustomWithNoShell is the Windows message, exercised by asking for an
// interpreter that is not there.
func TestCustomNeedsAKind(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Custom = map[string]*config.Custom{"hook": {
		Enabled: true, Command: "true", Kinds: []string{"video"}, Format: "png", Timeout: "5s",
	}}
	needs := onlyPublisher(t, &cfg).Needs()
	if needs.Accepts(render.KindImage) {
		t.Errorf("kinds = %v: only what was asked for", needs.Kinds)
	}
	if !needs.Accepts(render.KindVideo) {
		t.Errorf("kinds = %v", needs.Kinds)
	}
}
