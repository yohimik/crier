package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/render"
	"github.com/yohimik/crier/internal/template"
)

// pagedTemplate overflows a 200x200 page on purpose: five blocks of 120 CSS
// pixels flow onto five pages, one apiece.
const pagedTemplate = `<html><body>
<style>
 body { margin: 0; background: #fff; font-family: Go }
 .b { height: 120px; background: #ccddff; margin: 0 0 40px 0; break-inside: avoid }
 @page { margin: 20px }
</style>
<div class="b">1</div><div class="b">2</div><div class="b">3</div>
<div class="b">4</div><div class="b">5</div>
</body></html>`

// pagedPipeline builds a pipeline over a template written to a temporary
// directory, with the page ceiling set by the caller.
func pagedPipeline(t *testing.T, html string, tune func(*config.Config)) (*Pipeline, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "card.html")
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Render.HermeticFonts = true
	cfg.Render.Template = path
	cfg.Render.Width, cfg.Render.Height = 200, 200
	if tune != nil {
		tune(&cfg)
	}
	p, err := NewPipeline(PipelineOptions{
		Config: &cfg,
		Logger: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
		Client: httpx.New(httpx.Options{Logger: zerolog.Nop()}),
		Stdin:  strings.NewReader(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Cleanup(context.Background()) })
	return p, &cfg
}

// TestOverflowBecomesPages is the whole of pagination: content that does not
// fit is no longer refused, it is the rest of the post.
func TestOverflowBecomesPages(t *testing.T) {
	p, _ := pagedPipeline(t, pagedTemplate, nil)
	arts, err := p.Render(context.Background(), BaseVariant(p.cfg), nil, []config.Format{config.PNG})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts.Pages) != 5 {
		t.Fatalf("laid out into %d pages, want 5", len(arts.Pages))
	}
	// Every page is a real file, and no two pages are the same file: a
	// carousel of five copies of page one is the failure this catches.
	seen := map[string]bool{}
	for i, page := range arts.Pages {
		art, ok := page.Images[config.PNG]
		if !ok {
			t.Fatalf("page %d was not encoded as PNG", i+1)
		}
		if art.Size <= 0 {
			t.Errorf("page %d is empty", i+1)
		}
		if seen[art.Path] {
			t.Errorf("page %d reuses %s", i+1, art.Path)
		}
		seen[art.Path] = true
		if art.Width != 200 || art.Height != 200 {
			t.Errorf("page %d is %dx%d, want 200x200", i+1, art.Width, art.Height)
		}
	}
}

// TestOnePageKeepsItsName: a document that fits keeps the file name it always
// had, so nothing downstream has to learn about pagination to keep working.
func TestOnePageKeepsItsName(t *testing.T) {
	const short = `<html><body><style>body{margin:0;background:#fff}</style>` +
		`<div style="width:100px;height:100px;background:#c33"></div></body></html>`
	p, _ := pagedPipeline(t, short, nil)
	v := BaseVariant(p.cfg)
	arts, err := p.Render(context.Background(), v, nil, []config.Format{config.PNG})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts.Pages) != 1 {
		t.Fatalf("laid out into %d pages, want 1", len(arts.Pages))
	}
	name := filepath.Base(arts.Pages[0].Images[config.PNG].Path)
	if !strings.HasPrefix(name, v.Key()) || strings.Contains(name, "-p0") {
		t.Errorf("a single page is called %q; it should keep the unnumbered name", name)
	}
}

// TestPagesMaxRefusesARunawayDocument: the ceiling exists so a template with a
// loop that never ends fails here rather than at the platform, which would
// have taken the first ten and said nothing about the rest.
func TestPagesMaxRefusesARunawayDocument(t *testing.T) {
	p, _ := pagedPipeline(t, pagedTemplate, func(c *config.Config) { c.Render.PagesMax = 3 })
	_, err := p.Render(context.Background(), BaseVariant(p.cfg), nil, []config.Format{config.PNG})
	if err == nil {
		t.Fatal("five pages under a ceiling of three should be refused")
	}
	if codeOf(err) != ExitRender {
		t.Errorf("code = %d, want ExitRender", codeOf(err))
	}
	for _, want := range []string{"5 pages", "render.pages-max is 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// TestPagesMaxAtTheCeilingIsAllowed: the boundary is inclusive, so a document
// that lays out into exactly the ceiling is a post rather than an error.
func TestPagesMaxAtTheCeilingIsAllowed(t *testing.T) {
	p, _ := pagedPipeline(t, pagedTemplate, func(c *config.Config) { c.Render.PagesMax = 5 })
	arts, err := p.Render(context.Background(), BaseVariant(p.cfg), nil, []config.Format{config.PNG})
	if err != nil {
		t.Fatalf("exactly at the ceiling should pass: %v", err)
	}
	if len(arts.Pages) != 5 {
		t.Errorf("pages = %d", len(arts.Pages))
	}
}

// TestFitAppliesToEveryPage: a carousel has to be a set of pictures the same
// shape, so the platform's frame is applied per page rather than to the first.
func TestFitAppliesToEveryPage(t *testing.T) {
	p, _ := pagedPipeline(t, pagedTemplate, nil)
	v := BaseVariant(p.cfg)
	v.Fit, v.FitWidth, v.FitHeight = config.FitCover, 300, 150
	v.FitBackground = "#ffffff"

	arts, err := p.Render(context.Background(), v, nil, []config.Format{config.PNG})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts.Pages) != 5 {
		t.Fatalf("pages = %d", len(arts.Pages))
	}
	for i, page := range arts.Pages {
		art := page.Images[config.PNG]
		if art.Width != 300 || art.Height != 150 {
			t.Errorf("page %d is %dx%d, want the platform's 300x150 frame",
				i+1, art.Width, art.Height)
		}
	}
}

// TestSequenceIsEveryPageInOrder: one ordered page list per run is what makes
// every platform show the same thing. Sequence is where that order comes from.
func TestSequenceIsEveryPageInOrder(t *testing.T) {
	arts := Artifacts{Pages: []Page{
		{Images: map[config.Format]render.Artifact{config.PNG: {Path: "/1.png", Kind: render.KindImage}}},
		{Images: map[config.Format]render.Artifact{config.PNG: {Path: "/2.png", Kind: render.KindImage}}},
		{Images: map[config.Format]render.Artifact{config.PNG: {Path: "/3.png", Kind: render.KindImage}}},
	}}
	needs := publishNeedsPNG()
	got, err := arts.Sequence(needs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/1.png", "/2.png", "/3.png"}
	if len(got) != len(want) {
		t.Fatalf("got %d artifacts, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Path != want[i] {
			t.Errorf("artifact %d = %s, want %s", i, got[i].Path, want[i])
		}
	}
	// Primary stays the first page, which is what the single-file paths mean
	// when they ask for "the" artifact.
	first, err := arts.Primary(needs)
	if err != nil || first.Path != "/1.png" {
		t.Errorf("primary = %v, %v", first, err)
	}
}

// TestSequenceRefusesAMixedPageList: a carousel of an image and a clip is a
// post some platforms take and others silently truncate, so it is refused.
func TestSequenceRefusesAMixedPageList(t *testing.T) {
	arts := Artifacts{Pages: []Page{
		{Images: map[config.Format]render.Artifact{config.PNG: {Path: "/1.png", Kind: render.KindImage}}},
		{Images: map[config.Format]render.Artifact{config.PNG: {Path: "/2.mp4", Kind: render.KindVideo}}},
	}}
	_, err := arts.Sequence(publishNeedsPNG())
	if err == nil || !strings.Contains(err.Error(), "page 2") {
		t.Fatalf("err = %v, want a refusal naming page 2", err)
	}
}

// TestURLsFollowThePages: a platform that fetches by URL fetches every page,
// so every page needs an address of its own.
func TestStageGivesEveryPageAURL(t *testing.T) {
	p, _ := pagedPipeline(t, pagedTemplate, nil)
	arts, err := p.Render(context.Background(), BaseVariant(p.cfg), nil, []config.Format{config.JPEG})
	if err != nil {
		t.Fatal(err)
	}
	st := &countingStager{}
	if err := p.Stage(context.Background(), st, &arts, true, false); err != nil {
		t.Fatal(err)
	}
	if len(st.staged) != 5 {
		t.Fatalf("staged %d files, want one per page: %v", len(st.staged), st.staged)
	}
	urls := arts.URLs()
	if len(urls) != 5 {
		t.Fatalf("urls = %v", urls)
	}
	seen := map[string]bool{}
	for i, u := range urls {
		if u == "" {
			t.Errorf("page %d was not staged", i+1)
		}
		if seen[u] {
			t.Errorf("page %d shares a URL with an earlier page: %s", i+1, u)
		}
		seen[u] = true
	}
	if arts.URL() != urls[0] {
		t.Errorf("URL() = %q, want the first page's %q", arts.URL(), urls[0])
	}
}

// publishNeedsPNG is a publisher's needs reduced to what these tests exercise.
func publishNeedsPNG() publish.Needs {
	return publish.Needs{
		Formats: []config.Format{config.PNG},
		Kinds:   []render.Kind{render.KindImage},
	}
}

// stubPub is a publisher with the needs a test wants and nothing else.
type stubPub struct {
	name string
	need publish.Needs
}

func (s stubPub) Name() string { return s.name }
func (s stubPub) Needs() publish.Needs {
	if len(s.need.Formats) == 0 {
		s.need.Formats = []config.Format{config.PNG}
	}
	if len(s.need.Kinds) == 0 {
		s.need.Kinds = []render.Kind{render.KindImage}
	}
	return s.need
}
func (stubPub) Publish(context.Context, publish.Input) (publish.Result, error) {
	return publish.Result{}, nil
}
func (s stubPub) Ping(context.Context) (publish.Identity, error) {
	return publish.Identity{ID: s.name}, nil
}

func fivePages() Artifacts {
	var pages []Page
	for i := 1; i <= 5; i++ {
		pages = append(pages, Page{
			Images: map[config.Format]render.Artifact{
				config.PNG: {Path: fmt.Sprintf("/p%d.png", i), Kind: render.KindImage},
			},
			URL: fmt.Sprintf("https://staged/%d.png", i),
		})
	}
	return Artifacts{Pages: pages}
}

// TestEveryPlatformGetsTheSamePagesInTheSameOrder is the synchronisation rule.
// A carousel at one platform and a run of single posts at another have to tell
// the same story in the same sequence.
func TestEveryPlatformGetsTheSamePagesInTheSameOrder(t *testing.T) {
	cfg := config.Defaults()
	arts := fivePages()
	eng := template.New()

	for _, tc := range []struct {
		name     string
		capacity int
		want     [][]string
	}{
		{"a carousel platform", 10, [][]string{{"/p1.png", "/p2.png", "/p3.png", "/p4.png", "/p5.png"}}},
		{"a four-cap platform", 4, [][]string{
			{"/p1.png", "/p2.png", "/p3.png", "/p4.png"}, {"/p5.png"},
		}},
		{"a one-at-a-time platform", 1, [][]string{
			{"/p1.png"}, {"/p2.png"}, {"/p3.png"}, {"/p4.png"}, {"/p5.png"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pub := stubPub{name: "x", need: publish.Needs{MaxAttachments: tc.capacity}}
			posts, err := PostsFor(eng, &cfg, pub, arts, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(posts) != len(tc.want) {
				t.Fatalf("got %d posts, want %d", len(posts), len(tc.want))
			}
			var flat []string
			for i, in := range posts {
				var got []string
				for _, a := range in.Sequence() {
					got = append(got, a.Path)
				}
				flat = append(flat, got...)
				if strings.Join(got, ",") != strings.Join(tc.want[i], ",") {
					t.Errorf("post %d = %v, want %v", i+1, got, tc.want[i])
				}
				if in.Post != i+1 || in.Posts != len(tc.want) {
					t.Errorf("post %d says it is %d of %d", i+1, in.Post, in.Posts)
				}
				if in.Pages != 5 {
					t.Errorf("post %d says the run has %d pages", i+1, in.Pages)
				}
			}
			// The flattened sequence is the run's page list, unchanged: nothing
			// reordered, skipped or merged.
			if strings.Join(flat, ",") != "/p1.png,/p2.png,/p3.png,/p4.png,/p5.png" {
				t.Errorf("the pages came out as %v", flat)
			}
		})
	}
}

// TestPostsCarryEachBatchesOwnURLs: a platform that fetches must be given the
// addresses of the files in the post it was handed.
func TestPostsCarryEachBatchesOwnURLs(t *testing.T) {
	cfg := config.Defaults()
	pub := stubPub{name: "x", need: publish.Needs{URL: true, MaxAttachments: 2}}
	posts, err := PostsFor(template.New(), &cfg, pub, fivePages(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 3 {
		t.Fatalf("posts = %d", len(posts))
	}
	if posts[1].URL != "https://staged/3.png" {
		t.Errorf("post 2's first url = %q", posts[1].URL)
	}
	want := []string{"https://staged/3.png", "https://staged/4.png"}
	if strings.Join(posts[1].SequenceURLs(), ",") != strings.Join(want, ",") {
		t.Errorf("post 2 urls = %v, want %v", posts[1].SequenceURLs(), want)
	}
}

// TestMaxAttachmentsOnlyLowersTheCap: asking a platform for more than it takes
// would only be refused by the platform, which is a worse way to find out.
func TestMaxAttachmentsOnlyLowersTheCap(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.X.Layout.MaxAttachments = 2
	pub := stubPub{name: "x", need: publish.Needs{MaxAttachments: 4}}
	posts, err := PostsFor(template.New(), &cfg, pub, fivePages(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 3 {
		t.Errorf("posts = %d, want the configured cap of two per post", len(posts))
	}

	cfg.Publish.X.Layout.MaxAttachments = 40
	posts, err = PostsFor(template.New(), &cfg, pub, fivePages(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 {
		t.Errorf("posts = %d, want the platform's own cap of four to win", len(posts))
	}
}

// TestCaptionsCountThePosts is what lets one line of configuration write
// "2 of 3" without the operator writing a caption per post by hand.
func TestCaptionsCountThePosts(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Caption = "part {{.Post}}/{{.Posts}} from page {{.Page}} of {{.Pages}} on {{.Platform}}"
	pub := stubPub{name: "x", need: publish.Needs{MaxAttachments: 2}}
	posts, err := PostsFor(template.New(), &cfg, pub, fivePages(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"part 1/3 from page 1 of 5 on x",
		"part 2/3 from page 3 of 5 on x",
		"part 3/3 from page 5 of 5 on x",
	}
	for i, in := range posts {
		if in.Caption != want[i] {
			t.Errorf("caption %d = %q, want %q", i+1, in.Caption, want[i])
		}
	}
}

// TestAnUnpagedRunStillReadsAsOneOfOne, so a caption that mentions the numbers
// is safe to write whether or not anything ever paginates.
func TestAnUnpagedRunStillReadsAsOneOfOne(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Caption = "{{.Post}} of {{.Posts}}"
	arts := Artifacts{Pages: []Page{{Images: map[config.Format]render.Artifact{
		config.PNG: {Path: "/only.png", Kind: render.KindImage},
	}}}}
	posts, err := PostsFor(template.New(), &cfg, stubPub{name: "x"}, arts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Caption != "1 of 1" {
		t.Errorf("posts = %+v", posts)
	}
}

// TestAClipIsOnePostHoweverManyPagesTheStillsWouldHaveBeen.
func TestAClipIsOnePost(t *testing.T) {
	cfg := config.Defaults()
	arts := Artifacts{
		Video: &render.Artifact{Path: "/clip.mp4", Kind: render.KindVideo},
		Pages: []Page{{URL: "https://staged/clip.mp4"}},
	}
	pub := stubPub{name: "x", need: publish.Needs{
		URL:            true,
		MaxAttachments: 10,
		Kinds:          []render.Kind{render.KindImage, render.KindVideo},
	}}
	posts, err := PostsFor(template.New(), &cfg, pub, arts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].Files() != 1 {
		t.Fatalf("posts = %+v", posts)
	}
	if posts[0].URL != "https://staged/clip.mp4" {
		t.Errorf("url = %q", posts[0].URL)
	}
}
