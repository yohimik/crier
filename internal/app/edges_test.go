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
	"github.com/yohimik/crier/internal/template"
)

func testPipeline(t *testing.T, cfg *config.Config) *Pipeline {
	t.Helper()
	p, err := NewPipeline(PipelineOptions{
		Config: cfg,
		Logger: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
		Client: httpx.New(httpx.Options{Logger: zerolog.Nop()}),
		Stdin:  strings.NewReader(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Cleanup(context.Background()) })
	return p
}

// TestPipelineTemplateIsTheOneChosen: with a pool, the run picks one and every
// variant and frame uses it, so what was picked has to be readable.
func TestPipelineTemplateIsTheOneChosen(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.html")
	b := filepath.Join(dir, "b.html")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("<html></html>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Defaults()
	cfg.Render.Pool = []string{a, b}
	cfg.Render.Seed = 7
	cfg.Render.HermeticFonts = true

	p := testPipeline(t, &cfg)
	chosen := p.Template()
	if chosen != a && chosen != b {
		t.Fatalf("Template() = %q, want one of the pool", chosen)
	}
	// The same seed picks the same one, which is the reproducibility promise.
	if again := testPipeline(t, &cfg).Template(); again != chosen {
		t.Errorf("a second run with the same seed chose %q, the first chose %q", again, chosen)
	}

	// And a single template is its own pool of one.
	single := config.Defaults()
	single.Render.Template = a
	single.Render.HermeticFonts = true
	if got := testPipeline(t, &single).Template(); got != a {
		t.Errorf("Template() = %q, want %q", got, a)
	}
}

// TestPipelineAudioIsTheOneChosen is the template pool's promise, for the
// soundtrack: a pool of tracks picks one per run, the same seed picks the same
// one, and a run without a pool keeps the file it was given.
func TestPipelineAudioIsTheOneChosen(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.mp3")
	two := filepath.Join(dir, "two.mp3")

	cfg := config.Defaults()
	cfg.Render.HermeticFonts = true
	cfg.Render.Video.AudioPool = []string{one, two}
	cfg.Render.Seed = 7

	chosen := testPipeline(t, &cfg).Audio()
	if chosen != one && chosen != two {
		t.Fatalf("Audio() = %q, want one of the pool", chosen)
	}
	// The same seed picks the same track, which is what lets one release be
	// re-run and sound the same.
	if again := testPipeline(t, &cfg).Audio(); again != chosen {
		t.Errorf("a second run with the same seed chose %q, the first chose %q", again, chosen)
	}

	// Every seed picks something in the pool, and both entries are reachable.
	seen := map[string]bool{}
	for seed := 1; seed <= 40; seed++ {
		c := cfg
		c.Render.Seed = seed
		got := testPipeline(t, &c).Audio()
		if got != one && got != two {
			t.Fatalf("seed %d chose %q, which is not in the pool", seed, got)
		}
		seen[got] = true
	}
	if len(seen) != 2 {
		t.Errorf("forty seeds only ever chose %v; a pool of two should reach both", seen)
	}

	// No pool at all leaves render.video.audio exactly as it was.
	plain := config.Defaults()
	plain.Render.HermeticFonts = true
	plain.Render.Video.Audio = one
	if got := testPipeline(t, &plain).Audio(); got != one {
		t.Errorf("Audio() = %q, want the configured file %q", got, one)
	}

	// Both set is not an error, and it is not a coin toss either: the pool
	// wins, exactly as render.pool wins over render.template.
	both := cfg
	both.Render.Video.Audio = filepath.Join(dir, "ignored.mp3")
	both.Render.Seed = 7
	if got := testPipeline(t, &both).Audio(); got != chosen {
		t.Errorf("Audio() = %q with both set, want the pool's pick %q", got, chosen)
	}
}

// TestPipelinePicksInAFixedOrder: the layout is drawn before the soundtrack,
// and both are drawn whether or not video is enabled. A pick that happened
// only on a video run would shift every draw after it, and announce.sh runs
// crier several times over one release with video on for one of them.
func TestPipelinePicksInAFixedOrder(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.html")
	b := filepath.Join(dir, "b.html")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("<html></html>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	base := config.Defaults()
	base.Render.HermeticFonts = true
	base.Render.Pool = []string{a, b}
	base.Render.Video.AudioPool = []string{
		filepath.Join(dir, "one.mp3"), filepath.Join(dir, "two.mp3"),
	}
	base.Render.Seed = 11

	still := testPipeline(t, &base)

	clip := base
	clip.Render.Video.Enabled = true
	clip.Render.Video.FFmpegBin = "crier-no-such-ffmpeg"
	moving := testPipeline(t, &clip)

	if still.Template() != moving.Template() {
		t.Errorf("the still run drew %q and the video run drew %q; one seed is one layout",
			still.Template(), moving.Template())
	}
	if still.Audio() != moving.Audio() {
		t.Errorf("the still run chose %q and the video run chose %q; one seed is one soundtrack",
			still.Audio(), moving.Audio())
	}
}

// TestPipelineDataReportsABadDocument: a data file that is not readable is a
// render error naming the file, not a nil map that renders an empty card.
func TestPipelineDataReportsABadDocument(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.HermeticFonts = true
	cfg.Render.Data = filepath.Join(t.TempDir(), "missing.yaml")

	if _, err := testPipeline(t, &cfg).Data(); err == nil {
		t.Fatal("a missing data file should fail")
	} else if codeOf(err) != ExitRender {
		t.Errorf("code = %d, want a render error", codeOf(err))
	}

	// No data file at all is fine: a template may need none.
	empty := config.Defaults()
	empty.Render.HermeticFonts = true
	if _, err := testPipeline(t, &empty).Data(); err != nil {
		t.Errorf("no data document should be fine: %v", err)
	}
}

// TestLayoutHelpersToleratePlatformsWithout: a platform with no layout takes
// the render defaults rather than zero.
func TestLayoutHelpersToleratePlatformsWithout(t *testing.T) {
	if overlayOf(nil) != nil {
		t.Error("no layout has no overlays")
	}
	if widthOf(nil) != 0 || heightOf(nil) != 0 {
		t.Error("no layout has no size")
	}
	l := &config.Layout{Overlay: []string{"o.html"}, Width: 8, Height: 9}
	if len(overlayOf(l)) != 1 || widthOf(l) != 8 || heightOf(l) != 9 {
		t.Errorf("layout = %+v", l)
	}
	if dimensionOr(0, 5) != 5 || dimensionOr(3, 5) != 3 {
		t.Error("an override wins and a zero falls back")
	}
}

// TestBaseURLResolution covers the three shapes render.base-url takes.
func TestBaseURLResolution(t *testing.T) {
	got, err := baseURL("")
	if err != nil || got != "" {
		t.Errorf("empty = %q, %v", got, err)
	}

	// An absolute URL is used as it is.
	if got, err := baseURL("https://cdn.example/assets/"); err != nil || got != "https://cdn.example/assets/" {
		t.Errorf("absolute = %q, %v", got, err)
	}

	// A directory becomes a file URL ending in a slash, which is what makes a
	// relative src in a stylesheet resolve against it.
	dir := t.TempDir()
	got, err = baseURL(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "file://") || !strings.HasSuffix(got, "/") {
		t.Errorf("a directory = %q", got)
	}

	// A path that is not there is a configuration mistake worth naming.
	if _, err := baseURL(filepath.Join(dir, "nope")); err == nil {
		t.Error("a missing base URL directory should fail")
	}
}

// TestPlaceOutputCopiesOutOfTheWorkingDirectory: the working directory is
// removed on cleanup, so a path pointing into it is not a useful answer.
func TestPlaceOutputCopiesOutOfTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	produced := filepath.Join(dir, "rendered.png")
	if err := os.WriteFile(produced, []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}

	// An explicit destination, including a directory that does not exist yet.
	want := filepath.Join(dir, "out", "card.png")
	got, err := placeOutput(want, produced, ".png")
	if err != nil || got != want {
		t.Fatalf("= %q, %v", got, err)
	}
	if body, err := os.ReadFile(want); err != nil || string(body) != "PNGDATA" {
		t.Errorf("the file did not arrive: %q, %v", body, err)
	}

	// Producing straight into the destination copies nothing.
	if got, err := placeOutput(produced, produced, ".png"); err != nil || got != produced {
		t.Errorf("= %q, %v", got, err)
	}

	// A source that is not there is an error rather than an empty file.
	if _, err := placeOutput(filepath.Join(dir, "x.png"), filepath.Join(dir, "gone.png"), ".png"); err == nil {
		t.Error("copying a missing file should fail")
	}

	// A destination that cannot be created is an error too.
	if _, err := placeOutput(filepath.Join(produced, "under-a-file.png"), produced, ".png"); err == nil {
		t.Error("a destination under a file should fail")
	}
}

// TestCaptionOfPlatformsWithoutOne: a platform crier does not know has no
// caption of its own and falls back to the shared one.
func TestCaptionOfPlatformsWithoutOne(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Caption = "shared"
	if got := captionOf(&cfg, "nobody"); got != "" {
		t.Errorf("= %q, want none", got)
	}
	cfg.Publish.Telegram.Caption = "telegram's own"
	if got := captionOf(&cfg, "telegram"); got != "telegram's own" {
		t.Errorf("= %q", got)
	}

	engine := template.New()
	out, err := CaptionFor(engine, &cfg, "discord", map[string]any{})
	if err != nil || out != "shared" {
		t.Errorf("= %q, %v; a platform with no caption takes the shared one", out, err)
	}
}

// TestPruneBackupIsSilent: housekeeping must never be the reason a command
// fails, so it reports nothing whatever it finds.
func TestPruneBackupIsSilent(t *testing.T) {
	pruneBackup() // no backup exists beside the test binary; must not panic
}

// TestReportTurnsErrorsIntoCodes covers the mapping every command exits
// through.
func TestReportTurnsErrorsIntoCodes(t *testing.T) {
	var out, errBuf strings.Builder
	a := App{Stdout: &out, Stderr: &errBuf}

	if code := a.report(nil); code != ExitOK {
		t.Errorf("nil = %d", code)
	}
	// An Error carrying ExitOK is a deliberate quiet success.
	if code := a.report(&Error{Code: ExitOK}); code != ExitOK {
		t.Errorf("a quiet success = %d", code)
	}
	if errBuf.Len() != 0 {
		t.Errorf("a success printed %q", errBuf.String())
	}

	if code := a.report(failf(ExitStaging, "the tunnel died")); code != ExitStaging {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(errBuf.String(), "the tunnel died") {
		t.Errorf("stderr = %q", errBuf.String())
	}
}
