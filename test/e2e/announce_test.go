//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The announce tests drive announce/ the way a release does: the real scripts,
// the real binary, the real config, against a fake Graph API. The only thing
// standing in is the tunnel — a test has no public URL and does not need one,
// because stage.mode=url is the documented escape hatch these scripts already
// honour.

func requireSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the announce scripts are sh")
	}
}

// runScript runs one of the announce scripts from the repository root.
func runScript(t *testing.T, script string, env []string, args ...string) (string, string, int) {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{filepath.Join(root, "announce", script)}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !asExitError(err, &exitErr) {
			t.Fatalf("running %s: %v\n%s", script, err, stderr.String())
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// notesDoc is the shape announce/notes.sh writes.
type notesDoc struct {
	Version  string `json:"version"`
	Sections []struct {
		Label string   `json:"label"`
		Items []string `json:"items"`
		More  int      `json:"more"`
	} `json:"sections"`
	Install []struct {
		Label   string `json:"label"`
		Command string `json:"command"`
	} `json:"install"`
}

// TestAnnounceNotesBuildsTheCardsData covers the list splitting, the
// truncation and the sections that are left out — the parts that decide what a
// release card actually says.
func TestAnnounceNotesBuildsTheCardsData(t *testing.T) {
	requireSh(t)

	out, stderr, code := runScript(t, "notes.sh", []string{
		"DISPAT_NEW_VERSION=1.2.3",
		"DISPAT_BREAKING_CHANGES=drop the old API",
		"DISPAT_FEATURES=add streaming\nadd retries\nadd caching\nadd metrics\nadd tracing",
		"DISPAT_FIXES=close a leak\nfix a race",
	})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}

	var doc notesDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Version != "1.2.3" {
		t.Errorf("version = %q", doc.Version)
	}

	if len(doc.Sections) != 3 {
		t.Fatalf("sections = %+v, want breaking, features and fixes", doc.Sections)
	}
	// The changelog's order, not the environment's.
	for i, want := range []string{"BREAKING", "FEATURES", "FIXES"} {
		if doc.Sections[i].Label != want {
			t.Errorf("section %d = %q, want %q", i, doc.Sections[i].Label, want)
		}
	}

	features := doc.Sections[1]
	if len(features.Items) != 3 {
		t.Errorf("features shows %d items, want the first three", len(features.Items))
	}
	if features.Items[0] != "add streaming" || features.Items[2] != "add caching" {
		t.Errorf("features = %v, want them in history order", features.Items)
	}
	if features.More != 2 {
		t.Errorf("more = %d, want the two that did not fit", features.More)
	}
	// A section that fits entirely says nothing is left.
	if doc.Sections[2].More != 0 {
		t.Errorf("fixes.more = %d", doc.Sections[2].More)
	}
}

// TestAnnounceNotesOmitsEmptySections: a card with a FIXES heading and no
// fixes under it looks broken, so the heading is not drawn at all.
func TestAnnounceNotesOmitsEmptySections(t *testing.T) {
	requireSh(t)

	// dispat sets an empty group rather than leaving it unset, and a here-doc
	// leaves a trailing newline — both have to read as "nothing here".
	out, _, code := runScript(t, "notes.sh", []string{
		"DISPAT_NEW_VERSION=2.0.0",
		"DISPAT_BREAKING_CHANGES=",
		"DISPAT_FEATURES=only this one\n",
		"DISPAT_FIXES=   \n  ",
	})
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	var doc notesDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(doc.Sections) != 1 || doc.Sections[0].Label != "FEATURES" {
		t.Fatalf("sections = %+v, want only the one with entries", doc.Sections)
	}
	if len(doc.Sections[0].Items) != 1 {
		t.Errorf("items = %v, want the blank line dropped", doc.Sections[0].Items)
	}

	// A release with no notes at all is still a valid document.
	out, _, code = runScript(t, "notes.sh", []string{"DISPAT_NEW_VERSION=3.0.0"})
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(doc.Sections) != 0 {
		t.Errorf("sections = %+v, want none", doc.Sections)
	}
}

// TestAnnounceNotesEscapesAndPins checks the two things that would produce
// invalid JSON or a wrong command.
func TestAnnounceNotesEscapesAndPins(t *testing.T) {
	requireSh(t)

	out, _, code := runScript(t, "notes.sh", []string{
		"DISPAT_NEW_VERSION=1.0.0-rc.1",
		`DISPAT_FIXES=quote the "path" and the \backslash`,
	})
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	var doc notesDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("a subject with a quote in it broke the JSON: %v\n%s", err, out)
	}
	if got := doc.Sections[0].Items[0]; got != `quote the "path" and the \backslash` {
		t.Errorf("item = %q, want it unchanged after the round trip", got)
	}

	// All three install routes, each pinned to the version being announced.
	if len(doc.Install) != 3 {
		t.Fatalf("install = %+v, want the three routes", doc.Install)
	}
	byLabel := map[string]string{}
	for _, i := range doc.Install {
		byLabel[i.Label] = i.Command
	}
	if !strings.Contains(byLabel["curl"], "/v1.0.0-rc.1/install.sh") ||
		!strings.Contains(byLabel["curl"], "CRIER_VERSION=1.0.0-rc.1") {
		t.Errorf("curl = %q", byLabel["curl"])
	}
	if !strings.Contains(byLabel["go"], "@v1.0.0-rc.1") {
		t.Errorf("go = %q", byLabel["go"])
	}
	if !strings.Contains(byLabel["dispat"], "--asset 'crier-{os}-{arch}'") {
		t.Errorf("dispat = %q", byLabel["dispat"])
	}
}

// TestAnnounceSkipsWithoutSecrets is the property that matters most: dispat's
// announce stage only warns, and a missing secret must never fail a release.
func TestAnnounceSkipsWithoutSecrets(t *testing.T) {
	requireSh(t)

	for _, tt := range []struct {
		name string
		env  []string
		says string
	}{
		{
			"nothing set at all",
			[]string{"DISPAT_NEW_VERSION=1.0.0",
				"CRIER_PUBLISH_INSTAGRAM_TOKEN=", "CRIER_PUBLISH_INSTAGRAM_USER_ID=", "NGROK_AUTHTOKEN="},
			"CRIER_PUBLISH_INSTAGRAM_TOKEN",
		},
		{
			"no tunnel token",
			[]string{"DISPAT_NEW_VERSION=1.0.0",
				"CRIER_PUBLISH_INSTAGRAM_TOKEN=t", "CRIER_PUBLISH_INSTAGRAM_USER_ID=u", "NGROK_AUTHTOKEN="},
			"NGROK_AUTHTOKEN",
		},
		{
			"no release to announce",
			[]string{"DISPAT_NEW_VERSION="},
			"no DISPAT_NEW_VERSION",
		},
	} {
		_, stderr, code := runScript(t, "announce.sh", tt.env)
		if code != 0 {
			t.Errorf("%s: exit %d — the announce stage must never fail a release", tt.name, code)
		}
		if !strings.Contains(stderr, tt.says) {
			t.Errorf("%s: it should say what is missing: %s", tt.name, stderr)
		}
	}

	// With the platform secrets but staging pointed elsewhere, the tunnel is
	// not wanted and its absence is not a reason to skip — but the missing
	// binary still is.
	_, stderr, code := runScript(t, "announce.sh", []string{
		"DISPAT_NEW_VERSION=1.0.0",
		"CRIER_PUBLISH_INSTAGRAM_TOKEN=t", "CRIER_PUBLISH_INSTAGRAM_USER_ID=u",
		"NGROK_AUTHTOKEN=",
		"CRIER_STAGE_MODE=url",
		"ANNOUNCE_CRIER_BIN=/nonexistent/crier",
	})
	if code != 0 {
		t.Errorf("exit %d", code)
	}
	if strings.Contains(stderr, "NGROK_AUTHTOKEN") {
		t.Errorf("the tunnel is not needed when staging elsewhere: %s", stderr)
	}
	if !strings.Contains(stderr, "no built binary") {
		t.Errorf("it should say the binary is missing: %s", stderr)
	}
}

// TestAnnouncePostsFeedThenStory is the whole thing end to end: the real
// scripts, the real binary, the real config, against a fake Graph API.
func TestAnnouncePostsFeedThenStory(t *testing.T) {
	requireSh(t)

	// The container calls are recorded in order: which came first is half of
	// what this test is checking.
	var requests []recordedCall
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body := readBody(r)
		requests = append(requests, recordedCall{Path: r.URL.Path, Body: body})
		switch {
		case strings.HasSuffix(r.URL.Path, "/media"):
			_, _ = w.Write([]byte(`{"id":"c-` + strconv.Itoa(len(requests)) + `"}`))
		case strings.HasSuffix(r.URL.Path, "/media_publish"):
			_, _ = w.Write([]byte(`{"id":"p-` + strconv.Itoa(len(requests)) + `"}`))
		case strings.HasPrefix(r.URL.Path, "/c-"):
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case strings.HasPrefix(r.URL.Path, "/p-"):
			_, _ = w.Write([]byte(`{"permalink":"https://www.instagram.com/p/Cx/"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	dir := t.TempDir()
	_, stderr, code := runScript(t, "announce.sh", []string{
		"DISPAT_NEW_VERSION=9.9.9",
		"DISPAT_BREAKING_CHANGES=publish is the default",
		"DISPAT_FEATURES=add slack\nfit the render\nread data from the environment\nand one more",
		"DISPAT_FIXES=close a leak",
		"CRIER_PUBLISH_INSTAGRAM_TOKEN=ig-token",
		"CRIER_PUBLISH_INSTAGRAM_USER_ID=ig-user",
		"CRIER_PUBLISH_INSTAGRAM_API_BASE_URL=" + srv.URL,
		"CRIER_PUBLISH_INSTAGRAM_POLL_INTERVAL=1ms",
		"CRIER_PUBLISH_INSTAGRAM_POLL_TIMEOUT=5s",
		// No tunnel: a test has no public URL and needs none. This is the
		// documented escape hatch, exercised here rather than only described.
		"CRIER_STAGE_MODE=url",
		"CRIER_STAGE_URL=" + srv.URL + "/staged/card.jpg",
		"ANNOUNCE_CRIER_BIN=" + buildAnnounceBinary(t, dir),
	})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}

	// Two containers, feed first and story second — the order matters, because
	// a story that goes out before the post it refers to is a story about
	// nothing.
	var containers []recordedCall
	for _, r := range requests {
		if strings.HasSuffix(r.Path, "/media") {
			containers = append(containers, r)
		}
	}
	if len(containers) != 2 {
		t.Fatalf("created %d containers, want the feed post and the story", len(containers))
	}

	feed, err := url.ParseQuery(containers[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	story, err := url.ParseQuery(containers[1].Body)
	if err != nil {
		t.Fatal(err)
	}
	if feed.Get("media_type") == "STORIES" {
		t.Error("the feed post went out as a story")
	}
	if story.Get("media_type") != "STORIES" {
		t.Errorf("the story's media_type = %q", story.Get("media_type"))
	}

	// The feed caption carries the version and an install line. The story
	// carries none at all: the Stories API has no caption field, so crier
	// omits it and the card itself is the whole message.
	if !strings.Contains(feed.Get("caption"), "9.9.9") {
		t.Errorf("the feed caption does not name the version: %q", feed.Get("caption"))
	}
	if !strings.Contains(feed.Get("caption"), "install.sh") {
		t.Errorf("the feed caption has no install line: %q", feed.Get("caption"))
	}
	if got := story.Get("caption"); got != "" {
		t.Errorf("the story container carried caption %q; the Stories API has none", got)
	}

	// And both published.
	published := 0
	for _, r := range requests {
		if strings.HasSuffix(r.Path, "/media_publish") {
			published++
		}
	}
	if published != 2 {
		t.Errorf("published %d times, want two", published)
	}
	if !strings.Contains(stderr, "posted the feed post") || !strings.Contains(stderr, "posted the story") {
		t.Errorf("both posts should be logged: %s", stderr)
	}
}

// TestAnnounceRendersBothShapes checks the pictures rather than the calls: the
// feed post is the square card and the story is it fitted into 1080x1920.
func TestAnnounceRendersBothShapes(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	bin := buildAnnounceBinary(t, dir)
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	notes, _, code := runScript(t, "notes.sh", []string{"DISPAT_NEW_VERSION=1.0.0", "DISPAT_FIXES=one fix"})
	if code != 0 {
		t.Fatal("notes.sh failed")
	}

	for _, tt := range []struct {
		name string
		args []string
		w, h int
	}{
		{"feed", nil, 1080, 1080},
		{"story", []string{
			"--publish-instagram-width", "1080", "--publish-instagram-height", "1920",
			"--publish-instagram-fit", "contain",
			"--publish-instagram-fit-background", "#04140c",
			"--render-variant", "instagram",
		}, 1080, 1920},
	} {
		out := filepath.Join(dir, tt.name+".png")
		args := append([]string{
			"render",
			"--config", filepath.Join(root, "announce", "crier.yaml"),
			"--render-data", "-",
			"--render-output", out, "--render-format", "png",
		}, tt.args...)
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.Stdin = strings.NewReader(notes)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v\n%s", tt.name, err, stderr.String())
		}
		cfg, format := decodeImage(t, out)
		if format != "png" || cfg.Width != tt.w || cfg.Height != tt.h {
			t.Errorf("%s is %s %dx%d, want %dx%d", tt.name, format, cfg.Width, cfg.Height, tt.w, tt.h)
		}
	}
}

type recordedCall struct {
	Path string
	Body string
}

func readBody(r *http.Request) string {
	buf := make([]byte, 1<<20)
	n, _ := r.Body.Read(buf)
	for {
		m, err := r.Body.Read(buf[n:])
		n += m
		if err != nil || n == len(buf) {
			break
		}
	}
	return string(buf[:n])
}

// buildAnnounceBinary builds the binary announce.sh would find in a release.
//
// The release runs the bytes it just built; a test builds the same source the
// same way rather than reaching for whatever is on PATH.
func buildAnnounceBinary(t *testing.T, dir string) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "crier-announce")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/crier")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building crier: %v\n%s", err, combined)
	}
	return out
}
