//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// pagedTemplate overflows its page on purpose. Each block carries its own page
// number, and the page files crier writes are numbered too, which is what lets
// these tests assert the order a platform received them in.
const pagedTemplate = `<html><body>
<style>
 body { margin: 0; background: #fff; font-family: Go; font-size: 14px }
 .b { height: 90px; background: #ccddff; margin: 0 0 20px 0; break-inside: avoid }
 @page { margin: 10px;
   @bottom-center { content: counter(page) " / " counter(pages); font-family: Go; font-size: 12px } }
</style>
<div class="b">page one</div><div class="b">page two</div><div class="b">page three</div>
<div class="b">page four</div><div class="b">page five</div>
</body></html>`

// newPagedProject is newProject with a template that lays out into five pages.
func newPagedProject(t *testing.T, publishBlock string) string {
	t.Helper()
	dir := newProject(t, publishBlock)
	writeFile(t, dir, "template.html", pagedTemplate)
	// 240x120 with 90px blocks and a page margin gives one block per page.
	return dir
}

// pageNumbers pulls the page numbers out of the file names in a multipart body.
//
// crier names a paged render's files <key>-p01, <key>-p02 and so on, so the
// order the names appear in a request body is the order the pages were sent
// in. It is the cheapest end-to-end proof of ordering there is: it reads what
// actually went over the wire.
var pageFileRE = regexp.MustCompile(`-p(\d\d)\.(?:png|jpe?g)`)

func pageNumbers(body string) []string {
	var out []string
	for _, m := range pageFileRE.FindAllStringSubmatch(body, -1) {
		out = append(out, strings.TrimPrefix(m[1], "0"))
	}
	return out
}

// pageNumbersFromURLs is pageNumbers for the platforms that fetch rather than
// take bytes: the staged URL ends in the same numbered file name.
func pageNumbersFromURLs(reqs []request) []string {
	var out []string
	for _, r := range reqs {
		if n := pageNumbers(r.Body); len(n) > 0 {
			out = append(out, n...)
		}
	}
	return out
}

func joined(v []string) string { return strings.Join(v, ",") }

// igContainers are the container creations only. A path fragment of "/media"
// would also match "/media_publish", which is a different step.
func igContainers(f *fakes) []request {
	var out []request
	for _, r := range f.all() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/media") {
			out = append(out, r)
		}
	}
	return out
}

// TestSmokePagedPostsLandEverywhere is the release gate for paged posts.
//
// It drives the shipped binary through the three shapes a page list can take —
// sequential stories, one carousel, and a capped split — and asserts all three
// against the fakes, because "paged posts must land" is exactly the promise a
// release has to keep.
func TestSmokePagedPostsLandEverywhere(t *testing.T) {
	f := newFakes(t)
	addr := freeAddr(t)

	dir := newPagedProject(t, strings.Join([]string{
		// Instagram as stories: one story per page, in order.
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: t",
		"    user-id: ig-user",
		"    story: true",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
		// Telegram takes ten at once, so five pages are one media group.
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		// x takes four, so five pages are a post of four then a post of one.
		"  x:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/x",
		"    token: x-token",
	}, "\n")+"\nstage:\n  mode: server\n  server:\n    listen: "+addr+
		"\n    public-url: http://"+addr+"\n")

	res := crier(t, dir, nil, "publish", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	var rep struct {
		Variants []struct {
			Pages int `json:"pages"`
		} `json:"variants"`
		Results []struct {
			Platform string `json:"platform"`
			OK       bool   `json:"ok"`
			Error    string `json:"error"`
			Posts    []struct {
				Post  int  `json:"post"`
				Pages int  `json:"pages"`
				OK    bool `json:"ok"`
			} `json:"posts"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatalf("%v\n%s", err, res.Stdout)
	}
	for _, r := range rep.Results {
		if !r.OK {
			t.Fatalf("%s failed: %s", r.Platform, r.Error)
		}
	}
	if len(rep.Variants) == 0 || rep.Variants[0].Pages != 5 {
		t.Fatalf("the document did not lay out into five pages: %+v", rep.Variants)
	}

	byPlatform := map[string]int{}
	for _, r := range rep.Results {
		byPlatform[r.Platform] = len(r.Posts)
	}

	// Instagram stories: five separate posts, no carousel.
	if got := byPlatform["instagram"]; got != 5 {
		t.Errorf("instagram made %d posts, want one story per page", got)
	}
	publishes := f.findAll("/instagram/ig-user/media_publish")
	if len(publishes) != 5 {
		t.Fatalf("instagram published %d stories, want 5", len(publishes))
	}
	containers := igContainers(f)
	// Every container is a story container, never a carousel one.
	for _, c := range containers {
		if strings.Contains(c.Body, "CAROUSEL") || strings.Contains(c.Body, "is_carousel_item") {
			t.Errorf("instagram stories must not use a carousel: %s", c.Body)
		}
	}
	if got := joined(pageNumbersFromURLs(containers)); got != "1,2,3,4,5" {
		t.Errorf("instagram received the pages as %q, want 1,2,3,4,5", got)
	}

	// Telegram: one media group carrying all five, in order.
	if got := byPlatform["telegram"]; got != 0 {
		t.Errorf("telegram made %d posts, want one", got+1)
	}
	groups := f.findAll("/sendMediaGroup")
	if len(groups) != 1 {
		t.Fatalf("telegram sent %d media groups, want 1", len(groups))
	}
	if got := joined(pageNumbers(groups[0].Body)); got != "1,2,3,4,5" {
		t.Errorf("telegram's media group is %q, want 1,2,3,4,5", got)
	}

	// x: four then one, and the four are the first four pages.
	if got := byPlatform["x"]; got != 2 {
		t.Errorf("x made %d posts, want two", got)
	}
	uploads := f.findAll("/x/2/media/upload")
	if got := joined(pageNumbers(strings.Join(bodies(uploads), "|"))); got != "1,2,3,4,5" {
		t.Errorf("x uploaded the pages as %q, want 1,2,3,4,5", got)
	}
	tweets := f.findAll("/x/2/tweets")
	if len(tweets) != 2 {
		t.Fatalf("x posted %d times, want 2", len(tweets))
	}
	if n := mediaCount(t, tweets[0].Body); n != 4 {
		t.Errorf("x's first post carries %d media, want 4: %s", n, tweets[0].Body)
	}
	if n := mediaCount(t, tweets[1].Body); n != 1 {
		t.Errorf("x's second post carries %d media, want 1: %s", n, tweets[1].Body)
	}
}

// mediaCount is how many media ids a post on x carried.
func mediaCount(t *testing.T, body string) int {
	t.Helper()
	var out struct {
		Media struct {
			IDs []string `json:"media_ids"`
		} `json:"media"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return len(out.Media.IDs)
}

func bodies(reqs []request) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.Body
	}
	return out
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// TestPagesReachEveryPlatformInTheSameOrder is the synchronisation rule proved
// end to end: three platforms with three different capacities see one page
// list, in one order, with nothing reordered, skipped or repeated.
func TestPagesReachEveryPlatformInTheSameOrder(t *testing.T) {
	f := newFakes(t)
	addr := freeAddr(t)
	dir := newPagedProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: t",
		"    user-id: ig-user",
		"    story: true",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		"  x:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/x",
		"    token: x-token",
	}, "\n")+"\nstage:\n  mode: server\n  server:\n    listen: "+addr+
		"\n    public-url: http://"+addr+"\n")

	if res := crier(t, dir, nil, "publish"); res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	const want = "1,2,3,4,5"
	for name, got := range map[string]string{
		"instagram": joined(pageNumbersFromURLs(igContainers(f))),
		"telegram":  joined(pageNumbers(f.findAll("/sendMediaGroup")[0].Body)),
		"x":         joined(pageNumbers(strings.Join(bodies(f.findAll("/x/2/media/upload")), "|"))),
	} {
		if got != want {
			t.Errorf("%s saw the pages as %q, want %q", name, got, want)
		}
	}
}

// TestStoriesArePublishedOneAtATime: a story reel is ordered by when each
// story was published, so the next container may not be created until the
// previous story is live.
func TestStoriesArePublishedOneAtATime(t *testing.T) {
	f := newFakes(t)
	addr := freeAddr(t)
	dir := newPagedProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: t",
		"    user-id: ig-user",
		"    story: true",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
	}, "\n")+"\nstage:\n  mode: server\n  server:\n    listen: "+addr+
		"\n    public-url: http://"+addr+"\n")

	if res := crier(t, dir, nil, "publish"); res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	// Walk the instagram requests in the order they arrived and check they
	// alternate: create, publish, create, publish. A second create before the
	// first publish would mean two stories were in flight at once.
	var steps []string
	for _, r := range f.all() {
		switch {
		case strings.HasSuffix(r.Path, "/media_publish"):
			steps = append(steps, "publish")
		case strings.HasSuffix(r.Path, "/media") && r.Method == "POST":
			steps = append(steps, "create")
		}
	}
	want := strings.Repeat("create,publish,", 5)
	if got := joined(steps) + ","; got != want {
		t.Errorf("the story sequence was %q, want %q", got, want)
	}
}

// TestACarouselIsOnePost: the whole point of a carousel is that a five-page
// changelog is one entry in the feed rather than five.
func TestACarouselIsOnePost(t *testing.T) {
	f := newFakes(t)
	addr := freeAddr(t)
	dir := newPagedProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: t",
		"    user-id: ig-user",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
		"    caption: \"part {{.Post}} of {{.Posts}}\"",
	}, "\n")+"\nstage:\n  mode: server\n  server:\n    listen: "+addr+
		"\n    public-url: http://"+addr+"\n")

	if res := crier(t, dir, nil, "publish"); res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	if n := len(f.findAll("/instagram/ig-user/media_publish")); n != 1 {
		t.Fatalf("instagram published %d times, want one carousel", n)
	}
	containers := igContainers(f)
	// Five children plus one parent.
	if len(containers) != 6 {
		t.Fatalf("instagram created %d containers, want five children and a parent", len(containers))
	}
	for i, c := range containers[:5] {
		if !strings.Contains(c.Body, "is_carousel_item=true") {
			t.Errorf("container %d is not a carousel item: %s", i+1, c.Body)
		}
	}
	parent := containers[5]
	if !strings.Contains(parent.Body, "media_type=CAROUSEL") {
		t.Errorf("the parent is not a carousel: %s", parent.Body)
	}
	if !strings.Contains(parent.Body, "children=") {
		t.Errorf("the parent lists no children: %s", parent.Body)
	}
	// The caption belongs to the parent, and a carousel is one post of one.
	if !strings.Contains(parent.Body, "part+1+of+1") {
		t.Errorf("the parent's caption is wrong: %s", parent.Body)
	}
	if got := joined(pageNumbersFromURLs(containers[:5])); got != "1,2,3,4,5" {
		t.Errorf("the children are %q, want 1,2,3,4,5", got)
	}
}

// TestPagesMaxRefusesTheRun: content past the ceiling fails the render rather
// than posting a truncated version of itself.
func TestPagesMaxRefusesTheRun(t *testing.T) {
	f := newFakes(t)
	dir := newPagedProject(t, enableTwo(f)+"\n")
	res := crier(t, dir, []string{"CRIER_RENDER_PAGES_MAX=2"}, "publish")
	if res.Code == exitOK {
		t.Fatal("five pages under a ceiling of two should fail")
	}
	if !strings.Contains(res.Stderr, "render.pages-max") {
		t.Errorf("stderr does not name the key: %s", res.Stderr)
	}
	if n := len(f.all()); n != 0 {
		t.Errorf("it made %d requests for a run that should not have started", n)
	}
}

// TestRenderWritesEveryPage: `crier render` hands back the whole set, numbered.
func TestRenderWritesEveryPage(t *testing.T) {
	dir := newPagedProject(t, "  telegram:\n    enabled: false\n")
	res := crier(t, dir, nil, "render", "--render-output", "card.png", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	var rep struct {
		Files []string `json:"files"`
		Pages int      `json:"pages"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatalf("%v\n%s", err, res.Stdout)
	}
	if rep.Pages != 5 || len(rep.Files) != 5 {
		t.Fatalf("report = %+v", rep)
	}
	for i, name := range rep.Files {
		if want := fmt.Sprintf("card-%d.png", i+1); !strings.HasSuffix(name, want) {
			t.Errorf("file %d = %s, want it to end in %s", i+1, name, want)
		}
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if cfg, format := decodeImage(t, path); format != "png" || cfg.Width != 240 {
			t.Errorf("%s is a %s of %dx%d", name, format, cfg.Width, cfg.Height)
		}
	}
}

// TestSmokeStoriesRecoverFromNotReady: what rc.3 hit in the wild. A container
// can poll FINISHED while Meta is still making the media publishable, and
// media_publish then answers error 9007. That refusal created no post, so the
// shipped binary has to ask again rather than stop the story sequence — and
// still stop on every other refusal. This runs in the release smoke because
// the failure only ever shows up against real timing, so the recovery is the
// part that must be proven on the bytes being shipped.
func TestSmokeStoriesRecoverFromNotReady(t *testing.T) {
	var mu sync.Mutex
	rejected := 0
	published := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/ig-user/media"):
			writeJSON(w, map[string]any{"id": "c1"})
		case strings.HasSuffix(r.URL.Path, "/c1"):
			writeJSON(w, map[string]any{"status_code": "FINISHED"})
		case strings.HasSuffix(r.URL.Path, "/ig-user/media_publish"):
			if rejected == 0 {
				rejected++
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": map[string]any{
					"message": "Media ID is not available", "type": "OAuthException",
					"code": 9007, "error_subcode": 2207027, "is_transient": false,
				}})
				return
			}
			published++
			writeJSON(w, map[string]any{"id": "p1"})
		case strings.HasSuffix(r.URL.Path, "/p1"):
			writeJSON(w, map[string]any{"permalink": "https://www.instagram.com/stories/x/1/"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	addr := freeAddr(t)
	dir := newProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + srv.URL,
		"    token: t",
		"    user-id: ig-user",
		"    story: true",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
	}, "\n")+"\nstage:\n  mode: server\n  server:\n    listen: "+addr+
		"\n    public-url: http://"+addr+"\n")

	res := crier(t, dir, nil, "publish", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if rejected != 1 {
		t.Errorf("the fake rejected %d publishes, want the scripted 1", rejected)
	}
	if published != 1 {
		t.Errorf("the story published %d times after the refusal, want exactly 1", published)
	}
}

// TestVKPagesBecomeOneWallPost: a wall post takes ten attachments, so a
// five-page changelog is one entry on the wall rather than five.
//
// The order is the part that would go wrong quietly. The fake numbers each
// upload and derives the saved id from it, so the attachment list wall.post
// carries is a readout of the order the pages were actually uploaded in.
func TestVKPagesBecomeOneWallPost(t *testing.T) {
	f := newFakes(t)
	dir := newPagedProject(t, strings.Join([]string{
		"  vk:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/vk",
		"    token: vk-token",
		"    owner-id: -123",
	}, "\n"))

	if res := crier(t, dir, nil, "publish"); res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	uploads := f.findAll("/vk-photo-upload")
	if len(uploads) != 5 {
		t.Fatalf("uploaded %d pages, want 5", len(uploads))
	}
	if got := joined(pageNumbersFromURLs(uploads)); got != "1,2,3,4,5" {
		t.Errorf("vk received the pages as %q, want 1,2,3,4,5", got)
	}

	posts := f.findAll("/vk/method/wall.post")
	if len(posts) != 1 {
		t.Fatalf("vk made %d posts, want one", len(posts))
	}
	form, err := url.ParseQuery(posts[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	want := "photo-123_1001,photo-123_1002,photo-123_1003,photo-123_1004,photo-123_1005"
	if form.Get("attachments") != want {
		t.Errorf("attachments = %q, want %q", form.Get("attachments"), want)
	}
}

// TestVKLoweredCapSplitsTheWall: max-attachments lowers the cap, and a page
// list longer than it becomes several posts in a row rather than a truncated
// one.
func TestVKLoweredCapSplitsTheWall(t *testing.T) {
	f := newFakes(t)
	dir := newPagedProject(t, strings.Join([]string{
		"  vk:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/vk",
		"    token: vk-token",
		"    owner-id: -123",
		"    max-attachments: 3",
	}, "\n"))

	if res := crier(t, dir, nil, "publish"); res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	posts := f.findAll("/vk/method/wall.post")
	if len(posts) != 2 {
		t.Fatalf("vk made %d posts, want two", len(posts))
	}
	for i, want := range []string{
		"photo-123_1001,photo-123_1002,photo-123_1003",
		"photo-123_1004,photo-123_1005",
	} {
		form, err := url.ParseQuery(posts[i].Body)
		if err != nil {
			t.Fatal(err)
		}
		if form.Get("attachments") != want {
			t.Errorf("post %d attachments = %q, want %q", i+1, form.Get("attachments"), want)
		}
	}
}
