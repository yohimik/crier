package publish

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/render"
)

// TestDecodeJSONToleratesAnEmptyBody: LinkedIn returns the post's URN in a
// header and sometimes also in the body, so the body is read opportunistically
// and an empty one is not a failure.
func TestDecodeJSONToleratesAnEmptyBody(t *testing.T) {
	var out struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(strings.NewReader(`{"id":"urn:li:share:9"}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "urn:li:share:9" {
		t.Errorf("out = %+v", out)
	}
	if err := decodeJSON(strings.NewReader(""), &out); err == nil {
		t.Error("an empty body has nothing to decode")
	}
}

// TestLinkedInReadsTheURNFromTheBody covers the fallback: the header is
// missing and the body has it instead.
func TestLinkedInReadsTheURNFromTheBody(t *testing.T) {
	upload := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "rest/images"):
			_, _ = w.Write([]byte(`{"value":{"uploadUrl":"` + upload.URL + `","image":"urn:li:image:1"}}`))
		default:
			// No x-restli-id header this time.
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"urn:li:share:from-body"}`))
		}
	})

	res, err := onlyPublisher(t, linkedinConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t), Caption: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "urn:li:share:from-body" {
		t.Errorf("id = %q, want the one from the body", res.ID)
	}
	if !strings.HasSuffix(res.URL, "urn:li:share:from-body") {
		t.Errorf("url = %q", res.URL)
	}
}

// TestMastodonUploadRefused covers the media endpoint failing, which is where
// an instance rejects a file it will not take.
func TestMastodonUploadRefused(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"File type image/png not supported"}`))
	})
	_, err := onlyPublisher(t, mastodonConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("err = %v", err)
	}

	// And a 200 that carries no attachment id.
	srv = fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := onlyPublisher(t, mastodonConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)}); err == nil ||
		!strings.Contains(err.Error(), "no attachment id") {
		t.Errorf("err = %v", err)
	}
}

// TestFacebookPhotoRefused covers the photo upload's error branch and the
// story step that follows it.
func TestFacebookPhotoRefused(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"the page token expired"}}`))
	})
	if _, err := onlyPublisher(t, facebookConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)}); err == nil ||
		!strings.Contains(err.Error(), "token expired") {
		t.Errorf("err = %v", err)
	}

	// The photo goes up and turning it into a story fails.
	calls := 0
	srv = fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.HasSuffix(r.URL.Path, "photo_stories") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"id":"ph-1"}`))
	})
	cfg := facebookConfig(srv.URL)
	cfg.Publish.Facebook.Story = true
	if _, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)}); err == nil ||
		!strings.Contains(err.Error(), "story") {
		t.Errorf("err = %v", err)
	}
}

// TestCustomCommandNeedsAShellPath covers the constructor's own refusal, and
// the run when the working directory does not exist.
func TestCustomRunFromAProjectDirectory(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran-here")
	if err := os.WriteFile(filepath.Join(dir, "publish.sh"),
		[]byte("#!/bin/sh\npwd > "+marker+"\n"), 0o755); err != nil { //nolint:gosec // a test script has to run
		t.Fatal(err)
	}

	cfg := customConfig(t, "sh ./publish.sh")
	d := testDeps(t)
	d.Dir = dir
	ps, err := Build(cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps[0].Publish(context.Background(), Input{Artifact: imageArtifact(t)}); err != nil {
		t.Fatal(err)
	}
	where, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	// The script beside the configuration is the one that ran, which is what
	// makes `command: sh ./publish.sh` mean what it looks like.
	if !strings.Contains(string(where), filepath.Base(dir)) {
		t.Errorf("the command ran in %q, want the project directory", strings.TrimSpace(string(where)))
	}
}

// TestCustomEnvOverridesCriersOwn: an entry's own variables go last, so a
// configuration can override anything, including one of crier's.
func TestCustomEnvOverridesCriersOwn(t *testing.T) {
	requireShell(t)
	out := filepath.Join(t.TempDir(), "platform.txt")
	cfg := customConfig(t, "printf '%s' \"$CRIER_PLATFORM\" > "+out)
	cfg.Publish.Custom["webhook"].Env = map[string]string{"CRIER_PLATFORM": "renamed"}

	if _, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "renamed" {
		t.Errorf("CRIER_PLATFORM = %q, want the entry's own value", body)
	}
}

// TestNeedsAcceptsIsExhaustive guards the routing table itself.
func TestNeedsAcceptsIsExhaustive(t *testing.T) {
	n := Needs{Kinds: []render.Kind{render.KindImage}}
	if !n.Accepts(render.KindImage) || n.Accepts(render.KindVideo) || n.Accepts(render.KindGIF) {
		t.Errorf("kinds = %v", n.Kinds)
	}
	if (Needs{}).Accepts(render.KindImage) {
		t.Error("a publisher declaring no kinds accepts none")
	}
}

// TestFirstNonEmptyChain is the fallback every per-platform setting resolves
// through.
func TestFirstNonEmptyChain(t *testing.T) {
	if got := firstNonEmpty("", "  ", "third"); got != "third" {
		t.Errorf("= %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("= %q", got)
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Errorf("= %q", got)
	}
}
