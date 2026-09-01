package publish

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// TestInstagramStorySendsNoCaption: the Stories API has no caption — Meta
// ignores the parameter — so crier omits it rather than posting text into a
// void, and warns, because a silently dropped caption reads as a crier bug.
func TestInstagramStorySendsNoCaption(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case "/123/media_publish":
			_, _ = w.Write([]byte(`{"id":"p1"}`))
		case "/p1":
			_, _ = w.Write([]byte(`{"permalink":"https://www.instagram.com/stories/x/1/"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	cfg := instagramConfig(srv.URL)
	cfg.Publish.Instagram.Story = true
	p := onlyPublisher(t, cfg)
	if _, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg", Caption: "release notes",
	}); err != nil {
		t.Fatal(err)
	}

	for _, req := range rec.all() {
		if req.Path != "/123/media" {
			continue
		}
		form, err := url.ParseQuery(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got := form.Get("caption"); got != "" {
			t.Errorf("a story container carried caption %q; the Stories API has none", got)
		}
		if form.Get("media_type") != "STORIES" {
			t.Errorf("media_type = %q, want STORIES", form.Get("media_type"))
		}
		return
	}
	t.Fatal("no container request was recorded")
}
