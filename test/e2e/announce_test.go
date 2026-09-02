//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
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

	// The card paginates, so a changelog is no longer cut to fit one thumbnail:
	// every entry is carried and the pages carry on.
	features := doc.Sections[1]
	if len(features.Items) != 5 {
		t.Errorf("features shows %d items, want all five", len(features.Items))
	}
	if features.Items[0] != "add streaming" || features.Items[4] != "add tracing" {
		t.Errorf("features = %v, want them in history order", features.Items)
	}
	if features.More != 0 {
		t.Errorf("more = %d, want nothing left over", features.More)
	}
	if doc.Sections[2].More != 0 {
		t.Errorf("fixes.more = %d", doc.Sections[2].More)
	}
}

// TestAnnounceNotesStillHasACeiling: pagination removed the reason to cut a
// changelog to three lines, not the reason to have a ceiling at all. A release
// past it would push the render past render.pages-max, which refuses it
// outright rather than truncating.
func TestAnnounceNotesStillHasACeiling(t *testing.T) {
	requireSh(t)

	var many []string
	for i := 1; i <= 25; i++ {
		many = append(many, fmt.Sprintf("feature %d", i))
	}
	out, stderr, code := runScript(t, "notes.sh", []string{
		"DISPAT_NEW_VERSION=1.2.3",
		"DISPAT_FEATURES=" + strings.Join(many, "\n"),
	})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var doc notesDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got := len(doc.Sections[0].Items); got != 20 {
		t.Errorf("shows %d items, want the ceiling of 20", got)
	}
	if doc.Sections[0].More != 5 {
		t.Errorf("more = %d, want the five past the ceiling", doc.Sections[0].More)
	}
}

// TestAnnounceNotesKeepsEveryEntry: the entry count no longer depends on how
// many sections there are, because the card is no longer one page.
func TestAnnounceNotesKeepsTheFullAllowanceForFewerSections(t *testing.T) {
	requireSh(t)

	out, stderr, code := runScript(t, "notes.sh", []string{
		"DISPAT_NEW_VERSION=1.2.3",
		"DISPAT_FEATURES=add streaming\nadd retries\nadd caching\nadd metrics",
		"DISPAT_FIXES=close a leak",
	})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var doc notesDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(doc.Sections) != 2 {
		t.Fatalf("sections = %+v, want features and fixes", doc.Sections)
	}
	if got := len(doc.Sections[0].Items); got != 4 {
		t.Errorf("features shows %d items, want all four", got)
	}
	if doc.Sections[0].More != 0 {
		t.Errorf("more = %d, want nothing left over", doc.Sections[0].More)
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
	// Bare since dispat 1.7: crier's assets carry the conventional name the
	// installer looks for, so the card teaches the short spelling.
	if byLabel["dispat"] != "dispat install yohimik/crier" {
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
//
// The card paginates, so this is also the proof that the release announces
// itself as a paged carousel: the feed pass builds children and a parent, and
// the story pass turns the same pages into a run of stories.
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

	// The feed pass comes first, because a story about a post that does not
	// exist yet is a story about nothing.
	var containers []url.Values
	for _, r := range requests {
		if !strings.HasSuffix(r.Path, "/media") {
			continue
		}
		v, err := url.ParseQuery(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		containers = append(containers, v)
	}

	// The changelog is longer than the cover page, so the card paginates and
	// the feed post is a carousel: a child per page, then a parent listing
	// them. Everything after that is the story pass.
	var children, parents, stories, videoStories []url.Values
	for _, v := range containers {
		switch {
		case v.Get("media_type") == "STORIES" && v.Get("video_url") != "":
			videoStories = append(videoStories, v)
		case v.Get("media_type") == "STORIES":
			stories = append(stories, v)
		case v.Get("media_type") == "CAROUSEL":
			parents = append(parents, v)
		case v.Get("is_carousel_item") == "true":
			children = append(children, v)
		default:
			t.Errorf("a container that is none of the kinds: %v", v)
		}
	}
	if len(children) < 2 {
		t.Fatalf("the feed post carried %d carousel children; the card should have paginated", len(children))
	}
	if len(parents) != 1 {
		t.Fatalf("created %d carousel parents, want one", len(parents))
	}
	if len(stories) != len(children) {
		t.Errorf("posted %d stories for %d pages; every page should get one",
			len(stories), len(children))
	}
	// The anthem: one video story, after everything else — the cover held for
	// sixteen seconds with the 1812 finale as its soundtrack. It renders only
	// where ffmpeg is installed, and the announce script says so and carries
	// on without it.
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		if len(videoStories) != 1 {
			t.Fatalf("posted %d video stories, want the one anthem", len(videoStories))
		}
		last, err := url.ParseQuery(requests[lastContainerIndex(requests)].Body)
		if err != nil {
			t.Fatal(err)
		}
		if last.Get("video_url") == "" {
			t.Error("the anthem story is not the last container; the fanfare comes after the news")
		}
	} else if len(videoStories) != 0 {
		t.Fatalf("a video story went out with no ffmpeg to make it")
	}
	// The parent lists exactly the children that were created, in order.
	wantChildren := make([]string, 0, len(children))
	for i := range children {
		wantChildren = append(wantChildren, "c-"+strconv.Itoa(childIndex(t, requests, i)))
	}
	if got := parents[0].Get("children"); got != strings.Join(wantChildren, ",") {
		t.Errorf("children = %q, want %q", got, strings.Join(wantChildren, ","))
	}

	feed := parents[0]
	story := stories[0]
	// A carousel child carries no caption: Instagram takes it on the parent.
	for i, c := range children {
		if c.Get("caption") != "" {
			t.Errorf("carousel child %d carried a caption", i+1)
		}
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

	// One publish for the carousel, and one per story.
	published := 0
	for _, r := range requests {
		if strings.HasSuffix(r.Path, "/media_publish") {
			published++
		}
	}
	if want := 1 + len(stories) + len(videoStories); published != want {
		t.Errorf("published %d times, want one carousel, %d stories and %d anthem",
			published, len(stories), len(videoStories))
	}
	if !strings.Contains(stderr, "posted the feed post") || !strings.Contains(stderr, "posted the stories") {
		t.Errorf("both passes should be logged: %s", stderr)
	}
	if len(videoStories) == 1 && !strings.Contains(stderr, "posted the anthem story") {
		t.Errorf("the anthem pass should be logged: %s", stderr)
	}
}

// childIndex is the request number of the i-th carousel child, which is what
// the fake named its container after.
func childIndex(t *testing.T, requests []recordedCall, i int) int {
	t.Helper()
	seen := 0
	for n, r := range requests {
		if !strings.HasSuffix(r.Path, "/media") {
			continue
		}
		v, err := url.ParseQuery(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if v.Get("is_carousel_item") != "true" {
			continue
		}
		if seen == i {
			return n + 1
		}
		seen++
	}
	t.Fatalf("no carousel child %d", i)
	return 0
}

// TestAnnounceRendersBothShapes checks the pictures rather than the calls: the
// feed pages are square cards and the story pages are them fitted into
// 1080x1920. Every page is checked, not just the first — a carousel whose
// second image is the wrong shape is a carousel Instagram crops.
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
			"--render-output", out, "--render-format", "png", "--json",
		}, tt.args...)
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.Stdin = strings.NewReader(notes)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v\n%s", tt.name, err, stderr.String())
		}
		var rep struct {
			Files []string `json:"files"`
			Pages int      `json:"pages"`
		}
		if err := json.Unmarshal([]byte(stdout.String()), &rep); err != nil {
			t.Fatalf("%s: %v\n%s", tt.name, err, stdout.String())
		}
		// The cover fills a page on its own, so any changelog at all puts the
		// entries on a second one.
		if rep.Pages < 2 {
			t.Errorf("%s laid out into %d pages; the changelog should follow the cover", tt.name, rep.Pages)
		}
		for i, f := range rep.Files {
			cfg, format := decodeImage(t, f)
			if format != "png" || cfg.Width != tt.w || cfg.Height != tt.h {
				t.Errorf("%s page %d is %s %dx%d, want %dx%d",
					tt.name, i+1, format, cfg.Width, cfg.Height, tt.w, tt.h)
			}
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

// TestSmokeHostileChangelog: real release notes carry what people wrote —
// an unbroken run of the widest glyph there is, one-character subjects, and
// more entries than any card should show. The card has to ellipsise the
// monsters, keep the pages within the cap, and post the lot.
func TestSmokeHostileChangelog(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()

	published := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ig-user/media"):
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case strings.HasSuffix(r.URL.Path, "/c1"):
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case strings.HasSuffix(r.URL.Path, "/ig-user/media_publish"):
			published++
			_, _ = w.Write([]byte(`{"id":"p1"}`))
		case strings.HasSuffix(r.URL.Path, "/p1"):
			_, _ = w.Write([]byte(`{"permalink":"https://www.instagram.com/p/x/"}`))
		case strings.HasPrefix(r.URL.Path, "/staged/"):
			_, _ = w.Write([]byte("JPEGDATA"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	long := strings.Repeat("W", 300)
	var features []string
	features = append(features, long, "w")
	for i := 0; i < 30; i++ {
		features = append(features, fmt.Sprintf("feature number %d, of a perfectly ordinary length", i))
	}

	_, stderr, code := runScript(t, "announce.sh", []string{
		"DISPAT_NEW_VERSION=9.9.9",
		"DISPAT_BREAKING_CHANGES=" + long,
		"DISPAT_FEATURES=" + strings.Join(features, "\n"),
		"DISPAT_FIXES=a\nb\nc",
		"CRIER_PUBLISH_INSTAGRAM_TOKEN=ig-token",
		"CRIER_PUBLISH_INSTAGRAM_USER_ID=ig-user",
		"CRIER_PUBLISH_INSTAGRAM_API_BASE_URL=" + srv.URL,
		"CRIER_PUBLISH_INSTAGRAM_POLL_INTERVAL=1ms",
		"CRIER_PUBLISH_INSTAGRAM_POLL_TIMEOUT=5s",
		"CRIER_STAGE_MODE=url",
		"CRIER_STAGE_URL=" + srv.URL + "/staged/card.jpg",
		"ANNOUNCE_CRIER_BIN=" + buildAnnounceBinary(t, dir),
	})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if published == 0 {
		t.Fatal("nothing was published")
	}
	if strings.Contains(stderr, "pages-max") || strings.Contains(stderr, "will not allocate") {
		t.Fatalf("the hostile changelog broke the render:\n%s", stderr)
	}
}

// lastContainerIndex finds the final container-creation call.
func lastContainerIndex(requests []recordedCall) int {
	last := -1
	for i, r := range requests {
		if strings.HasSuffix(r.Path, "/media") {
			last = i
		}
	}
	return last
}
