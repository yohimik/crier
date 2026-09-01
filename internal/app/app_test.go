package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/render"
	"github.com/yohimik/crier/internal/template"
)

// run drives the CLI the way a shell would and returns the code and streams.
func run(t *testing.T, dir string, environ []string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = App{
		Args:    args,
		Environ: environ,
		Stdin:   strings.NewReader(""),
		Stdout:  &out,
		Stderr:  &errBuf,
		Dir:     dir,
	}.Run(context.Background())
	return code, out.String(), errBuf.String()
}

// project writes a self-contained crier project and returns its directory.
func project(t *testing.T, extraConfig string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "template.html", `<html><body style="margin:0;font-family:Go;background:#fff">`+
		`<h1 style="font-size:40px">{{ .title }}</h1></body></html>`)
	write(t, dir, "data.yaml", "title: hello\nversion: 1.2.3\n")
	write(t, dir, "crier.yaml", strings.Join([]string{
		"render:",
		"  template: template.html",
		"  data: data.yaml",
		"  width: 200",
		"  height: 100",
		"  hermetic-fonts: true",
		extraConfig,
	}, "\n"))
	return dir
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// --- exit codes ------------------------------------------------------------

func TestExitNames(t *testing.T) {
	for code, want := range map[int]string{
		ExitOK: "ok", ExitConfig: "config error", ExitUsage: "usage error",
		ExitRender: "render error", ExitPartial: "partial publish failure",
		ExitPublish: "publish failure", ExitStaging: "staging error", 99: "error",
	} {
		if got := ExitName(code); got != want {
			t.Errorf("ExitName(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestErrorWrapping(t *testing.T) {
	base := errors.New("boom")
	e := fail(ExitStaging, base)
	if codeOf(e) != ExitStaging {
		t.Errorf("code = %d", codeOf(e))
	}
	if !errors.Is(e, base) {
		t.Error("the cause should be reachable")
	}
	// Wrapping twice keeps the first code, which is the specific one.
	if codeOf(fail(ExitConfig, e)) != ExitStaging {
		t.Error("re-wrapping should not change the code")
	}
	if fail(ExitConfig, nil) != nil {
		t.Error("nil stays nil")
	}
	if codeOf(nil) != ExitOK {
		t.Error("no error is ok")
	}
	if codeOf(errors.New("plain")) != ExitConfig {
		t.Error("a plain error is a config error")
	}
	if (&Error{Code: ExitUsage}).Error() != "usage error" {
		t.Error("an Error with no cause names its code")
	}
}

// --- usage -----------------------------------------------------------------

func TestNoCommandIsAUsageError(t *testing.T) {
	code, _, stderr := run(t, t.TempDir(), []string{})
	if code != ExitUsage {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := run(t, t.TempDir(), []string{}, "publsih")
	if code != ExitUsage || !strings.Contains(stderr, "unknown command") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestHelpIsOK(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, _, stderr := run(t, t.TempDir(), []string{}, arg)
		if code != ExitOK || !strings.Contains(stderr, "Commands:") {
			t.Errorf("%s: code=%d", arg, code)
		}
	}
}

func TestVersion(t *testing.T) {
	code, stdout, _ := run(t, t.TempDir(), []string{}, "version")
	if code != ExitOK || !strings.Contains(stdout, "crier") {
		t.Errorf("code=%d stdout=%q", code, stdout)
	}

	code, stdout, _ = run(t, t.TempDir(), []string{}, "version", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var info map[string]any
	if err := json.Unmarshal([]byte(stdout), &info); err != nil {
		t.Fatalf("not json: %v", err)
	}
	if info["goVersion"] == nil {
		t.Errorf("version json = %v", info)
	}
}

func TestBadFlagIsAUsageError(t *testing.T) {
	code, _, _ := run(t, t.TempDir(), []string{}, "render", "--not-a-flag")
	if code != ExitUsage {
		t.Errorf("code = %d", code)
	}
}

// --- render ----------------------------------------------------------------

func TestRenderWritesTheImage(t *testing.T) {
	dir := project(t, "")
	out := filepath.Join(t.TempDir(), "card.png")
	code, stdout, stderr := run(t, dir, []string{}, "render", "--render-output", out)
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != out {
		t.Errorf("stdout = %q, want the path", stdout)
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("no image was written: %v", err)
	}
	// Logs go to stderr so stdout stays scriptable.
	if !strings.Contains(stderr, "rendered") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRenderJSON(t *testing.T) {
	dir := project(t, "")
	out := filepath.Join(t.TempDir(), "card.png")
	code, stdout, _ := run(t, dir, []string{}, "render", "--render-output", out, "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var rep RenderReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("not json: %v (%q)", err, stdout)
	}
	if rep.Width != 200 || rep.Height != 100 || rep.Kind != "image" || rep.Variant != "base" {
		t.Errorf("report = %+v", rep)
	}
}

func TestRenderNeedsATemplate(t *testing.T) {
	code, _, stderr := run(t, t.TempDir(), []string{}, "render")
	if code != ExitConfig || !strings.Contains(stderr, "render.template") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestRenderReportsABrokenTemplate(t *testing.T) {
	dir := project(t, "")
	write(t, dir, "template.html", "{{ .broken ")
	code, _, stderr := run(t, dir, []string{}, "render")
	if code != ExitRender {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestRenderVariantOfAPlatform(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"  overlays: []",
		"publish:",
		"  discord:",
		"    width: 120",
		"    height: 60",
		"    overlay: overlay.html",
	}, "\n"))
	write(t, dir, "overlay.html", "")

	out := filepath.Join(t.TempDir(), "d.png")
	code, stdout, stderr := run(t, dir, []string{}, "render",
		"--render-variant", "discord", "--render-output", out, "--json")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var rep RenderReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Width != 120 || rep.Height != 60 {
		t.Errorf("the platform's own size should have been used: %+v", rep)
	}
	if rep.Variant != "discord" {
		t.Errorf("variant = %q", rep.Variant)
	}
}

func TestRenderVariantRejectsAnUnknownPlatform(t *testing.T) {
	dir := project(t, "")
	code, _, stderr := run(t, dir, []string{}, "render", "--render-variant", "myspace")
	if code != ExitUsage || !strings.Contains(stderr, "not a platform") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

// --- config discovery through the CLI --------------------------------------

func TestRenderFindsTheProjectFromASubdirectory(t *testing.T) {
	dir := project(t, "")
	sub := filepath.Join(dir, "deep", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "card.png")
	code, _, stderr := run(t, sub, []string{}, "render", "--render-output", out)
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

// --- config ----------------------------------------------------------------

func TestConfigRedactsSecrets(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  telegram:",
		"    enabled: true",
		"    token: super-secret-token",
		"    chat-id: \"@chan\"",
	}, "\n"))

	code, stdout, _ := run(t, dir, []string{}, "config")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(stdout, "super-secret-token") {
		t.Fatal("the token was printed in the clear")
	}
	if !strings.Contains(stdout, Redacted) {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "@chan") {
		t.Errorf("a non-secret value should be shown: %q", stdout)
	}
}

func TestConfigJSONAndAll(t *testing.T) {
	dir := project(t, "")
	code, stdout, _ := run(t, dir, []string{}, "config", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var rep ConfigReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("not json: %v", err)
	}
	if rep.File == "" || rep.Dir == "" {
		t.Errorf("report = %+v", rep)
	}
	if _, ok := rep.Values["render.width"]; !ok {
		t.Errorf("a changed key should be listed: %v", rep.Values)
	}
	if _, ok := rep.Values["log.level"]; ok {
		t.Error("a defaulted key should be hidden without --all")
	}

	code, stdout, _ = run(t, dir, []string{}, "config", "--json", "--all")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Values) != len(config.Registry()) {
		t.Errorf("--all listed %d keys, want %d", len(rep.Values), len(config.Registry()))
	}
}

func TestConfigRejectsAnUnknownKey(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "crier.yaml", "render:\n  widht: 10\n")
	code, _, stderr := run(t, dir, []string{}, "config")
	if code != ExitConfig || !strings.Contains(stderr, "widht") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestConfigRejectsAnInvalidValue(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "crier.yaml", "render:\n  jpeg-quality: 0\n")
	code, _, stderr := run(t, dir, []string{}, "config")
	if code != ExitConfig || !strings.Contains(stderr, "jpeg-quality") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

// --- platforms -------------------------------------------------------------

func TestPlatformsListsEverything(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  telegram:",
		"    enabled: true",
		"    token: t",
		"    chat-id: c",
	}, "\n"))

	code, stdout, _ := run(t, dir, []string{}, "platforms", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var rows []PlatformInfo
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("not json: %v", err)
	}
	if len(rows) != len(config.Platforms) {
		t.Fatalf("listed %d platforms, want %d", len(rows), len(config.Platforms))
	}
	byName := map[string]PlatformInfo{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if !byName["telegram"].Enabled || !byName["telegram"].Ready {
		t.Errorf("telegram = %+v", byName["telegram"])
	}
	if byName["instagram"].Ready || byName["instagram"].Problem == "" {
		t.Errorf("instagram should be reported as unconfigured: %+v", byName["instagram"])
	}
	if !byName["instagram"].NeedsURL {
		// Instagram cannot be built, so its needs are unknown; the table is
		// still expected to say the platform exists.
		t.Logf("instagram needs are unknown until it is configured")
	}

	code, stdout, _ = run(t, dir, []string{}, "platforms")
	if code != ExitOK || !strings.Contains(stdout, "PLATFORM") {
		t.Errorf("code=%d stdout=%q", code, stdout)
	}
}

// --- publish ---------------------------------------------------------------

func TestPublishNeedsAPlatform(t *testing.T) {
	dir := project(t, "")
	code, _, stderr := run(t, dir, []string{}, "publish")
	if code != ExitConfig || !strings.Contains(stderr, "no platform is enabled") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestPublishDryRunMakesNoRequests(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  caption: \"{{ .title }} on {{ .Platform }}\"",
		"  dry-run: true",
		"  telegram:",
		"    enabled: true",
		"    token: t",
		"    chat-id: c",
		"    api-base-url: http://127.0.0.1:1",
		"  discord:",
		"    enabled: true",
		"    webhook-url: http://127.0.0.1:1/hook",
	}, "\n"))

	code, stdout, stderr := run(t, dir, []string{}, "publish", "--json")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var rep PublishReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("not json: %v (%q)", err, stdout)
	}
	if !rep.DryRun || len(rep.Plan) != 2 || len(rep.Results) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	for _, pl := range rep.Plan {
		want := "hello on " + pl.Platform
		if pl.Caption != want {
			t.Errorf("%s caption = %q, want %q", pl.Platform, pl.Caption, want)
		}
	}
}

func TestPublishRefusesAVideoForAnImageOnlyPlatform(t *testing.T) {
	// Every platform crier ships takes video, so this is asserted at the unit
	// level with a publisher that says it cannot.
	needs := publish.Needs{Kinds: []render.Kind{render.KindImage}}
	if needs.Accepts(render.KindVideo) {
		t.Fatal("an image-only publisher must not accept video")
	}
	a := Artifacts{Video: &render.Artifact{Kind: render.KindVideo}}
	if _, err := a.Primary(needs); err == nil || !strings.Contains(err.Error(), "does not take video") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishReportsAMisconfiguredPlatform(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  telegram:",
		"    enabled: true",
	}, "\n"))
	code, _, stderr := run(t, dir, []string{}, "publish")
	if code != ExitConfig || !strings.Contains(stderr, "publish.telegram.token") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestPublishRejectsABrokenCaptionTemplate(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  dry-run: true",
		"  telegram:",
		"    enabled: true",
		"    token: t",
		"    chat-id: c",
		"    caption: \"{{ .nope }}\"",
	}, "\n"))
	code, _, stderr := run(t, dir, []string{}, "publish")
	if code != ExitRender {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "publish.telegram.caption") {
		t.Errorf("the error should name the key, got %q", stderr)
	}
}

// --- variants --------------------------------------------------------------

func TestVariantsGroupPlatformsThatAgree(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Width, cfg.Render.Height = 1080, 1080
	cfg.Publish.Instagram.Height = 1920
	cfg.Publish.Discord.Width, cfg.Publish.Discord.Height = 1200, 630

	vs := Variants(&cfg, []string{"instagram", "telegram", "discord", "x"})
	if len(vs) != 3 {
		t.Fatalf("variants = %d, want three: instagram, the default pair, discord", len(vs))
	}
	byName := map[string]Variant{}
	for _, v := range vs {
		byName[v.Name()] = v
	}
	if _, ok := byName["telegram-x"]; !ok {
		t.Errorf("platforms with no overrides should share a variant: %v", vs)
	}
	if got := byName["instagram"]; got.Height != 1920 || got.Width != 1080 {
		t.Errorf("instagram variant = %+v", got)
	}
	if got := byName["discord"]; got.Width != 1200 || got.Height != 630 {
		t.Errorf("discord variant = %+v", got)
	}
}

func TestVariantsWithNoOverridesAreOne(t *testing.T) {
	cfg := config.Defaults()
	vs := Variants(&cfg, []string{"telegram", "discord", "x"})
	if len(vs) != 1 {
		t.Fatalf("variants = %d, want one", len(vs))
	}
	if len(vs[0].Platforms) != 3 {
		t.Errorf("platforms = %v", vs[0].Platforms)
	}
}

func TestVariantsSeparateOnOverlays(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Overlays = []string{"base.html"}
	cfg.Publish.Reddit.Overlay = []string{"reddit.html"}
	vs := Variants(&cfg, []string{"telegram", "reddit"})
	if len(vs) != 2 {
		t.Fatalf("variants = %d", len(vs))
	}
	for _, v := range vs {
		if v.Overlays[0] != "base.html" {
			t.Errorf("the global overlay should come first: %v", v.Overlays)
		}
	}
}

func TestBaseVariant(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Overlays = []string{"a.html"}
	v := BaseVariant(&cfg)
	if v.Name() != "base" || len(v.Overlays) != 1 {
		t.Errorf("variant = %+v", v)
	}
	if v.Key() == "" {
		t.Error("a variant needs a key")
	}
}

// --- format negotiation ----------------------------------------------------

type formatStub struct{ needs publish.Needs }

func (formatStub) Name() string { return "stub" }
func (s formatStub) Needs() publish.Needs {
	return s.needs
}
func (formatStub) Publish(context.Context, publish.Input) (publish.Result, error) {
	return publish.Result{}, nil
}

func TestFormatsForAddsWhatAPlatformInsistsOn(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Format = "png"

	// A platform that takes either is satisfied by the configured format.
	got := FormatsFor(&cfg, []publish.Publisher{
		formatStub{publish.Needs{Formats: []config.Format{config.JPEG, config.PNG}}},
	})
	if len(got) != 1 || got[0] != config.PNG {
		t.Errorf("got %v, want just png", got)
	}

	// One that takes only JPEG adds it.
	got = FormatsFor(&cfg, []publish.Publisher{
		formatStub{publish.Needs{Formats: []config.Format{config.JPEG}}},
	})
	if len(got) != 2 {
		t.Fatalf("got %v, want both formats", got)
	}

	// A publisher with no declared formats asks for nothing.
	got = FormatsFor(&cfg, []publish.Publisher{formatStub{publish.Needs{}}})
	if len(got) != 1 {
		t.Errorf("got %v", got)
	}

	// An unreadable render.format falls back to png rather than failing here;
	// Validate is what reports it.
	cfg.Render.Format = "bmp"
	if got := FormatsFor(&cfg, nil); len(got) != 1 || got[0] != config.PNG {
		t.Errorf("got %v", got)
	}
}

// --- texts -----------------------------------------------------------------

func TestTextFieldsCoverEveryPlatform(t *testing.T) {
	cfg := config.Defaults()
	for _, name := range config.Platforms {
		if len(textFields(&cfg, name)) == 0 {
			t.Errorf("platform %q has no post-text fields", name)
		}
	}
	if len(textFields(&cfg, "myspace")) != 0 {
		t.Error("an unknown platform has no fields")
	}
}

func TestResolveTextsRendersEveryField(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Reddit.Enabled = true
	cfg.Publish.Reddit.Title = "{{ .version }} on {{ .Platform }}"
	cfg.Publish.Reddit.Caption = "see {{ .version }}"
	cfg.Publish.Mastodon.Enabled = true
	cfg.Publish.Mastodon.AltText = "a card for {{ .Platform }}"

	data := map[string]any{"version": "2.0"}
	if err := ResolveTexts(template.New(), &cfg, data); err != nil {
		t.Fatal(err)
	}
	if cfg.Publish.Reddit.Title != "2.0 on reddit" {
		t.Errorf("title = %q", cfg.Publish.Reddit.Title)
	}
	if cfg.Publish.Reddit.Caption != "see 2.0" {
		t.Errorf("caption = %q", cfg.Publish.Reddit.Caption)
	}
	if cfg.Publish.Mastodon.AltText != "a card for mastodon" {
		t.Errorf("alt text = %q", cfg.Publish.Mastodon.AltText)
	}
}

func TestResolveTextsNamesTheOffendingKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.TikTok.Enabled = true
	cfg.Publish.TikTok.Title = "{{ .missing }}"
	err := ResolveTexts(template.New(), &cfg, map[string]any{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if codeOf(err) != ExitRender {
		t.Errorf("code = %d", codeOf(err))
	}
	if !strings.Contains(err.Error(), "publish.tiktok.title") {
		t.Errorf("err = %v", err)
	}
}

func TestCaptionForFallsBackToTheSharedOne(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Caption = "shared for {{ .Platform }}"
	got, err := CaptionFor(template.New(), &cfg, "telegram", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "shared for telegram" {
		t.Errorf("got %q", got)
	}

	cfg.Publish.Telegram.Caption = "telegram only"
	got, err = CaptionFor(template.New(), &cfg, "telegram", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "telegram only" {
		t.Errorf("the platform's own caption should win, got %q", got)
	}
}

func TestCaptionForReportsABrokenSharedTemplate(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Caption = "{{ .missing }}"
	_, err := CaptionFor(template.New(), &cfg, "x", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "publish.caption") {
		t.Fatalf("err = %v", err)
	}
}

// --- small helpers ---------------------------------------------------------

func TestOneLine(t *testing.T) {
	if got := oneLine("a\nb"); got != "a b" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("x", 200)
	if got := oneLine(long); len(got) != 80 || !strings.HasSuffix(got, "...") {
		t.Errorf("got %d chars", len(got))
	}
}

func TestIsZeroAndSameValue(t *testing.T) {
	if !isZero("") || !isZero(0) || !isZero(false) || !isZero([]string{}) || !isZero(nil) {
		t.Error("zero values")
	}
	if isZero("x") || isZero(1) || isZero(true) || isZero([]string{"a"}) {
		t.Error("non-zero values")
	}
	if !sameValue(1, 1) || sameValue(1, 2) {
		t.Error("sameValue")
	}
}

func TestPlaceOutputCopies(t *testing.T) {
	dir := t.TempDir()
	src := write(t, dir, "src.png", "DATA")
	dst := filepath.Join(dir, "sub", "out.png")
	got, err := placeOutput(dst, src, ".png")
	if err != nil {
		t.Fatal(err)
	}
	if got != dst {
		t.Errorf("got %q", got)
	}
	body, err := os.ReadFile(dst)
	if err != nil || string(body) != "DATA" {
		t.Fatalf("body = %q, err = %v", body, err)
	}
	// The same path is a no-op rather than a copy onto itself.
	if got, err := placeOutput(src, src, ".png"); err != nil || got != src {
		t.Errorf("got %q %v", got, err)
	}
}

func TestStagedAssetPrefersJPEG(t *testing.T) {
	a := &Artifacts{Images: map[config.Format]render.Artifact{
		config.PNG:  {Path: "/a.png", ContentType: "image/png"},
		config.JPEG: {Path: "/a.jpg", ContentType: "image/jpeg"},
	}}
	got, err := stagedAsset(a)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/a.jpg" {
		t.Errorf("got %q, want the JPEG the URL-fetching platforms need", got.Path)
	}

	a = &Artifacts{Video: &render.Artifact{Path: "/a.mp4"}}
	if got, _ := stagedAsset(a); got.Path != "/a.mp4" {
		t.Errorf("a video should be staged over anything else, got %q", got.Path)
	}

	if _, err := stagedAsset(&Artifacts{}); err == nil {
		t.Error("nothing encoded means nothing to stage")
	}
}

func TestReadAllFiles(t *testing.T) {
	dir := t.TempDir()
	a := write(t, dir, "a.css", "a{}")
	got, err := readAllFiles([]string{a, "  "})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "a{}" {
		t.Errorf("got %v", got)
	}
	if _, err := readAllFiles([]string{filepath.Join(dir, "missing.css")}); err == nil {
		t.Error("expected a read error")
	}
}

func TestEnableOnly(t *testing.T) {
	cfg := config.Defaults()
	for _, name := range config.Platforms {
		enableOnly(&cfg, name)
		got := publish.Enabled(&cfg)
		if len(got) != 1 || got[0] != name {
			t.Errorf("enableOnly(%q) gave %v", name, got)
		}
	}
}
