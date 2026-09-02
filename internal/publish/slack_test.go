package publish

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

func slackConfig(api string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.Slack.Enabled = true
	cfg.Publish.Slack.APIBaseURL = api
	cfg.Publish.Slack.Token = "xoxb-test-token"
	cfg.Publish.Slack.Channel = "C0123ABCD"
	return &cfg
}

// fakeSlack is the three-step external upload: hand out a URL, take the bytes,
// then be told what the file is for. The upload host is separate, because the
// URL Slack hands out is not on the API host and carries no token.
func fakeSlack(t *testing.T, rec *recorder) (api string, uploads *recorder) {
	t.Helper()
	uploads = newRecorder()
	store := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		uploads.record(r)
		_, _ = w.Write([]byte("OK - 8 bytes"))
	})
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/files.getUploadURLExternal"):
			_, _ = w.Write([]byte(`{"ok":true,"upload_url":"` + store.URL + `/upload?t=abc","file_id":"F0999"}`))
		case strings.HasSuffix(r.URL.Path, "/files.completeUploadExternal"):
			_, _ = w.Write([]byte(`{"ok":true,"files":[{"id":"F0999","title":"card.jpg"}]}`))
		case strings.HasSuffix(r.URL.Path, "/auth.test"):
			_, _ = w.Write([]byte(`{"ok":true,"url":"https://acme.slack.com/","team":"Acme",` +
				`"user":"crier","team_id":"T1","user_id":"U1","bot_id":"B1"}`))
		default:
			http.NotFound(w, r)
		}
	})
	return srv.URL, uploads
}

// TestSlackRunsTheThreeStepUpload is the whole flow, and the linkage between
// its steps: the file id the first call hands out has to be the one the third
// call names, or the post shares a file nobody uploaded.
func TestSlackRunsTheThreeStepUpload(t *testing.T) {
	rec := newRecorder()
	api, uploads := fakeSlack(t, rec)

	art := imageArtifact(t)
	res, err := onlyPublisher(t, slackConfig(api)).Publish(context.Background(), Input{
		Artifact: art, Caption: "a card from crier",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "F0999" || res.Extra["channel"] != "C0123ABCD" {
		t.Errorf("result = %+v", res)
	}

	paths := rec.paths()
	want := []string{"POST /files.getUploadURLExternal", "POST /files.completeUploadExternal"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("paths = %v, want %v", paths, want)
	}

	// Step 1 declares the name and the exact length; Slack sizes the slot from
	// it and refuses a body that disagrees.
	first := rec.all()[0]
	values, err := url.ParseQuery(first.Body)
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("filename") == "" {
		t.Errorf("no filename: %q", first.Body)
	}
	if values.Get("length") != "8" {
		t.Errorf("length = %q, want the artifact's own size", values.Get("length"))
	}
	if got := first.Header.Get("Authorization"); got != "Bearer xoxb-test-token" {
		t.Errorf("authorization = %q", got)
	}

	// Step 2 puts the bytes at the URL Slack named, raw and unauthenticated:
	// the URL is the credential, and a token sent there is a request rejected
	// by a host that never wanted one.
	if len(uploads.all()) != 1 {
		t.Fatalf("uploaded %d times", len(uploads.all()))
	}
	up := uploads.all()[0]
	if up.Body != "JPEGDATA" {
		t.Errorf("the upload body = %q, want the file's own bytes", up.Body)
	}
	if got := up.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("content type = %q", got)
	}
	if up.Header.Get("Authorization") != "" {
		t.Error("the upload carried a token; the presigned URL is the credential")
	}
	if !strings.Contains(up.Query, "t=abc") {
		t.Errorf("the query Slack put on the upload URL was dropped: %q", up.Query)
	}

	// Step 3 names the file, the channel and the caption.
	complete, err := url.ParseQuery(rec.all()[1].Body)
	if err != nil {
		t.Fatal(err)
	}
	var files []map[string]string
	if err := json.Unmarshal([]byte(complete.Get("files")), &files); err != nil {
		t.Fatalf("files is not a JSON array: %q", complete.Get("files"))
	}
	if len(files) != 1 || files[0]["id"] != "F0999" {
		t.Errorf("files = %v, want the id from step one", files)
	}
	if complete.Get("channel_id") != "C0123ABCD" {
		t.Errorf("channel_id = %q", complete.Get("channel_id"))
	}
	if complete.Get("initial_comment") != "a card from crier" {
		t.Errorf("initial_comment = %q", complete.Get("initial_comment"))
	}
}

// TestSlackWithoutACaption: a file with no comment is a valid post, and the
// argument should be absent rather than empty.
func TestSlackWithoutACaption(t *testing.T) {
	rec := newRecorder()
	api, _ := fakeSlack(t, rec)
	if _, err := onlyPublisher(t, slackConfig(api)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)}); err != nil {
		t.Fatal(err)
	}
	complete, _ := url.ParseQuery(rec.all()[1].Body)
	if _, ok := complete["initial_comment"]; ok {
		t.Errorf("an empty caption was sent: %q", rec.all()[1].Body)
	}
}

// TestSlackAnswersOkFalseWithTwoHundred is the trap: Slack reports an
// application error inside a 200, so the status code alone says nothing.
func TestSlackAnswersOkFalseWithTwoHundred(t *testing.T) {
	for _, tt := range []struct {
		slackError string
		says       string
	}{
		{"not_in_channel", "invite it"},
		{"invalid_auth", "refused the token"},
		{"missing_scope", "files:write"},
		{"channel_not_found", "C0123ABCD"},
		{"ratelimited", "slack said ratelimited"},
		{"", "no reason given"},
	} {
		srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":false,"error":"` + tt.slackError + `"}`))
		})
		_, err := onlyPublisher(t, slackConfig(srv.URL)).Publish(context.Background(),
			Input{Artifact: imageArtifact(t)})
		if err == nil {
			t.Errorf("%q: a 200 with ok:false is a failure", tt.slackError)
			continue
		}
		if !strings.Contains(err.Error(), tt.says) {
			t.Errorf("%q: the error should say %q: %v", tt.slackError, tt.says, err)
		}
	}
}

// TestSlackCompleteFailureIsReported: the bytes went up and the share failed,
// which is a different sentence from the upload failing.
func TestSlackCompleteFailureIsReported(t *testing.T) {
	store := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/files.getUploadURLExternal") {
			_, _ = w.Write([]byte(`{"ok":true,"upload_url":"` + store.URL + `/u","file_id":"F1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":false,"error":"not_in_channel"}`))
	})
	_, err := onlyPublisher(t, slackConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "sharing the file") {
		t.Errorf("err = %v", err)
	}
}

// TestSlackUploadURLMissing: a 200 that says ok but names nowhere to upload.
func TestSlackUploadURLMissing(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	_, err := onlyPublisher(t, slackConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "no upload url") {
		t.Errorf("err = %v", err)
	}
}

// TestSlackUploadRefused: the presigned URL rejected the bytes.
func TestSlackUploadRefused(t *testing.T) {
	store := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"upload_url":"` + store.URL + `/u","file_id":"F1"}`))
	})
	_, err := onlyPublisher(t, slackConfig(srv.URL)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "uploading the file") {
		t.Errorf("err = %v", err)
	}
}

// TestSlackPingReadsAuthTest: auth.test needs no scope, which is what makes it
// the right check — it separates a token Slack never heard of from one that
// merely cannot do what crier wants.
func TestSlackPingReadsAuthTest(t *testing.T) {
	rec := newRecorder()
	api, _ := fakeSlack(t, rec)

	id, err := onlyPublisher(t, slackConfig(api)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "U1" {
		t.Errorf("id = %q, want the user id", id.ID)
	}
	if id.Name != "crier in Acme" {
		t.Errorf("name = %q, want the user and the workspace", id.Name)
	}
	// auth.test says nothing about channel membership, which is the other half
	// of a working setup — the note has to admit that.
	if !strings.Contains(id.Note, "C0123ABCD") || !strings.Contains(id.Note, "cannot confirm") {
		t.Errorf("note = %q", id.Note)
	}
	if got := rec.paths(); len(got) != 1 || got[0] != "POST /auth.test" {
		t.Errorf("requests = %v", got)
	}
	// Nothing was posted.
	for _, req := range rec.all() {
		if strings.Contains(req.Path, "Upload") {
			t.Errorf("ping touched the upload flow: %s", req.Path)
		}
	}
}

// TestSlackPingRejectsABadToken is the scenario the command exists for.
func TestSlackPingRejectsABadToken(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	})
	_, err := onlyPublisher(t, slackConfig(srv.URL)).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refused the token") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "publish.slack.token") {
		t.Errorf("the error should name the key to fix: %v", err)
	}
}

// TestSlackPingNotesAUserToken: a user token works for auth.test and is not
// what crier's instructions describe, so it is worth saying.
func TestSlackPingNotesAUserToken(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"team":"Acme","user":"someone","team_id":"T1","user_id":"U9"}`))
	})
	id, err := onlyPublisher(t, slackConfig(srv.URL)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(id.Note, "user token") {
		t.Errorf("note = %q", id.Note)
	}
}

// TestSlackNeedsAndConstructor: what Slack declares, and what it refuses to be
// built without.
func TestSlackNeedsAndConstructor(t *testing.T) {
	needs := onlyPublisher(t, slackConfig("https://slack.example")).Needs()
	if needs.URL {
		t.Error("slack takes the bytes; it needs no staging")
	}
	for _, kind := range []render.Kind{render.KindImage, render.KindVideo, render.KindGIF} {
		if !needs.Accepts(kind) {
			t.Errorf("slack should accept %s", kind)
		}
	}
	if len(needs.Formats) == 0 || needs.Formats[0] != config.JPEG {
		t.Errorf("formats = %v", needs.Formats)
	}

	for _, tt := range []struct {
		name  string
		build func(c *config.Config)
		want  string
	}{
		{"no token", func(c *config.Config) { c.Publish.Slack.Token = "" }, "publish.slack.token"},
		{"no channel", func(c *config.Config) { c.Publish.Slack.Channel = "" }, "publish.slack.channel"},
		{"no base url", func(c *config.Config) { c.Publish.Slack.APIBaseURL = "" }, "publish.slack.api-base-url"},
	} {
		cfg := slackConfig("https://slack.example")
		tt.build(cfg)
		_, err := Build(cfg, testDeps(t))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want it to name %s", tt.name, err, tt.want)
		}
	}
}

// TestSlackIsAPeer: it takes part in the fan-out like the other ten.
func TestSlackIsAPeer(t *testing.T) {
	cfg := slackConfig("https://slack.example")
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Telegram.Token = "t"
	cfg.Publish.Telegram.ChatID = "c"

	enabled := Enabled(cfg)
	if len(enabled) != 2 {
		t.Fatalf("enabled = %v", enabled)
	}
	built, err := Build(cfg, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range built {
		if p.Name() == "slack" {
			found = true
		}
	}
	if !found {
		t.Errorf("built = %v, want slack among them", built)
	}
	if len(Names()) != 13 {
		t.Errorf("crier knows %d platforms, want thirteen", len(Names()))
	}
}
