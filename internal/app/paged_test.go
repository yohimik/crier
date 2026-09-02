package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/render"
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
