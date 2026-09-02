package publish

import (
	"context"
	"net/http"
	"testing"
)

// TestInstagramRetriesTheNotReadyPublish: error 9007 means the publish did
// not happen, so it is the one refusal that is safe to ask again — bounded by
// the poll budget. Seen on a real release: the carousel published and the
// story seconds behind it was told to wait a moment.
func TestInstagramRetriesTheNotReadyPublish(t *testing.T) {
	rejections := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case "/123/media_publish":
			if rejections < 2 {
				rejections++
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"Media ID is not available","type":"OAuthException","code":9007,"error_subcode":2207027,"is_transient":false}}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"p1"}`))
		case "/p1":
			_, _ = w.Write([]byte(`{"permalink":"https://www.instagram.com/stories/x/1/"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	p := onlyPublisher(t, instagramConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rejections != 2 {
		t.Errorf("publish was rejected %d times before succeeding, want 2", rejections)
	}
	if res.ID != "p1" {
		t.Errorf("result = %+v", res)
	}
}

// TestInstagramRetriesTheVanishedContainer: rc.5's costume for the same
// race — the container this process created "does not exist" at the publish
// endpoint for a moment. Nothing was published from a container that does
// not exist, so asking again is safe.
func TestInstagramRetriesTheVanishedContainer(t *testing.T) {
	rejections := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case "/123/media_publish":
			if rejections == 0 {
				rejections++
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"The requested resource does not exist","type":"OAuthException","code":24,"error_subcode":2207006,"is_transient":false}}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"p1"}`))
		case "/p1":
			_, _ = w.Write([]byte(`{"permalink":"https://www.instagram.com/stories/x/1/"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	p := onlyPublisher(t, instagramConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rejections != 1 || res.ID != "p1" {
		t.Errorf("rejections=%d res=%+v", rejections, res)
	}
}

// TestInstagramDoesNotRetryOtherPublishFailures: everything that is not the
// not-ready refusal keeps the old rule — publishing may have happened, so it
// is never repeated.
func TestInstagramDoesNotRetryOtherPublishFailures(t *testing.T) {
	attempts := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case "/123/media_publish":
			attempts++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"something else","code":100}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	p := onlyPublisher(t, instagramConfig(srv.URL))
	if _, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
	}); err == nil {
		t.Fatal("expected the publish to fail")
	}
	if attempts != 1 {
		t.Errorf("media_publish was attempted %d times, want exactly 1", attempts)
	}
}

// TestInstagramWaitsOutATransientStatusRead is rc.14's lesson. Meta answered
// the container status GET with 403 "Application request limit reached",
// is_transient true, and the whole announcement died over a read that
// creates nothing. An error Meta itself marks transient keeps the poll
// waiting; a permanent one still fails at once.
func TestInstagramWaitsOutATransientStatusRead(t *testing.T) {
	refusals := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			if refusals < 2 {
				refusals++
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"message":"Application request limit reached","type":"OAuthException","is_transient":true,"code":4,"error_subcode":1349210}}`))
				return
			}
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case "/123/media_publish":
			_, _ = w.Write([]byte(`{"id":"p1"}`))
		case "/p1":
			_, _ = w.Write([]byte(`{"permalink":"https://www.instagram.com/p/x/"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	p := onlyPublisher(t, instagramConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
	})
	if err != nil {
		t.Fatalf("a transient refusal of the status read should be waited out: %v", err)
	}
	if refusals != 2 {
		t.Fatalf("the poll saw %d transient refusals, want both", refusals)
	}
	if res.ID != "p1" {
		t.Errorf("result = %+v", res)
	}
}

// And the other half: a permanent refusal of the read still fails at once.
func TestInstagramFailsAPermanentStatusRead(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported get request","type":"GraphMethodException","code":100,"is_transient":false}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	p := onlyPublisher(t, instagramConfig(srv.URL))
	if _, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg",
	}); err == nil {
		t.Fatal("a permanent refusal has to fail, not spin the poll")
	}
}
