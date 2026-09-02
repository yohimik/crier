package publish

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestMastodonRetriesTheUnprocessedAttachment: the instance refuses a status
// whose attachment has not finished processing with a 422 raised before any
// status exists, so asking again is safe.
func TestMastodonRetriesTheUnprocessedAttachment(t *testing.T) {
	rejections := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/v2/media"):
			_, _ = w.Write([]byte(`{"id":"m1","url":"x"}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/media/m1"):
			_, _ = w.Write([]byte(`{"id":"m1","url":"x"}`))
		case strings.HasSuffix(r.URL.Path, "/api/v1/statuses"):
			if rejections == 0 {
				rejections++
				w.WriteHeader(422)
				_, _ = w.Write([]byte(`{"error":"Cannot attach files that have not finished processing. Try again in a moment!"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"s1","url":"https://m.example/@u/s1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	res, err := onlyPublisher(t, mastodonConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t), Caption: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if rejections != 1 || res.ID != "s1" {
		t.Errorf("rejections=%d res=%+v", rejections, res)
	}
}

// TestXRetriesTheInvalidMediaRefusal: X answers a not-yet-consistent media id
// with the same 400 it uses for a wrong one; neither creates a tweet, so the
// bounded retry is safe and a permanent mistake still surfaces.
func TestXRetriesTheInvalidMediaRefusal(t *testing.T) {
	rejections := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "media/upload"):
			_, _ = w.Write([]byte(`{"data":{"id":"md1"}}`))
		case strings.HasSuffix(r.URL.Path, "/2/tweets"):
			if rejections == 0 {
				rejections++
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"errors":[{"message":"Your media IDs are invalid."}],"title":"Invalid Request"}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"id":"t1"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	res, err := onlyPublisher(t, xConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t), Caption: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if rejections != 1 || res.ID != "t1" {
		t.Errorf("rejections=%d res=%+v", rejections, res)
	}
}
