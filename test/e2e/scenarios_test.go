//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exit codes, repeated here rather than imported: an end-to-end test asserts
// the contract as a caller sees it, and a caller does not import internal/app.
const (
	exitOK      = 0
	exitConfig  = 1
	exitUsage   = 2
	exitRender  = 3
	exitPartial = 4
	exitPublish = 5
	exitStaging = 6
)

const baseTemplate = `<!doctype html><html><head><style>
body { margin:0; font-family:"Go"; background:#fff; color:#111 }
.card { width:100%; height:100%; padding:20px; box-sizing:border-box }
h1 { font-size:32px; margin:0 }
</style></head><body><div class="card">
{{ block "headline" . }}<h1>{{ .title }}</h1>{{ end }}
{{ block "extra" . }}{{ end }}
</div></body></html>`

// newProject writes a project directory: a template, a data file, and a
// crier.yaml holding the given publish block.
func newProject(t *testing.T, publishBlock string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "template.html", baseTemplate)
	writeFile(t, dir, "data.yaml", "title: end to end\nversion: 9.9.9\n")
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  level: debug",
		"render:",
		"  template: template.html",
		"  data: data.yaml",
		"  width: 240",
		"  height: 120",
		"  hermetic-fonts: true",
		"publish:",
		"  concurrency: 4",
		publishBlock,
	}, "\n"))
	return dir
}

func writeFile(t *testing.T, dir, name, body string) string {
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

func decodeImage(t *testing.T, path string) (image.Config, string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return cfg, format
}

// --- render ----------------------------------------------------------------

// TestSmokeRenderProducesAPNG is in the release smoke subset: it is the
// shortest path through the whole program — configuration, template, layout,
// rasteriser, encoder.
func TestSmokeRenderProducesAPNG(t *testing.T) {
	dir := newProject(t, "")
	out := filepath.Join(dir, "out.png")
	res := crier(t, dir, nil, "render", "--render-output", out)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != out {
		t.Errorf("stdout = %q", res.Stdout)
	}
	cfg, format := decodeImage(t, out)
	if format != "png" || cfg.Width != 240 || cfg.Height != 120 {
		t.Errorf("image = %s %dx%d", format, cfg.Width, cfg.Height)
	}
}

func TestRenderJPEGAndScale(t *testing.T) {
	dir := newProject(t, "")
	out := filepath.Join(dir, "out.jpg")
	res := crier(t, dir, nil, "render",
		"--render-format", "jpeg", "--render-scale", "2", "--render-output", out)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	cfg, format := decodeImage(t, out)
	if format != "jpeg" || cfg.Width != 480 || cfg.Height != 240 {
		t.Errorf("image = %s %dx%d, want a doubled jpeg", format, cfg.Width, cfg.Height)
	}
}

func TestRenderReadsDataFromStdin(t *testing.T) {
	dir := newProject(t, "")
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"render:",
		"  template: template.html",
		"  data: \"-\"",
		"  width: 200",
		"  height: 100",
		"  hermetic-fonts: true",
	}, "\n"))

	out := filepath.Join(dir, "out.png")
	cmd := crierCmd(t, dir, nil, "render", "--render-output", out)
	cmd.Stdin = strings.NewReader("title: from stdin\n")
	res := runCmd(t, cmd)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

func TestRenderFailsOnABrokenTemplate(t *testing.T) {
	dir := newProject(t, "")
	writeFile(t, dir, "template.html", "{{ .unclosed ")
	res := crier(t, dir, nil, "render")
	if res.Code != exitRender {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

func TestRenderFailsOnABrokenOverlay(t *testing.T) {
	dir := newProject(t, "")
	writeFile(t, dir, "bad-overlay.html", `{{ define "headline" }}oops`)
	res := crier(t, dir, nil, "render", "--render-overlays", "bad-overlay.html")
	if res.Code != exitRender {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "overlay") {
		t.Errorf("stderr = %s", res.Stderr)
	}
}

// --- configuration ---------------------------------------------------------

// TestBareCrierInAProjectDirectory is the flagship flow, end to end: cd into a
// project, run crier with no arguments at all, and the post goes out. It is
// the per-directory configuration and the default command together.
func TestBareCrierInAProjectDirectory(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableTwo(f))

	// From the project itself, and from a directory inside it.
	sub := filepath.Join(dir, "assets", "cards")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, from := range []string{dir, sub} {
		res := crier(t, from, nil)
		if res.Code != exitOK {
			t.Fatalf("from %s: code=%d stderr=%s", from, res.Code, res.Stderr)
		}
		if !strings.Contains(res.Stdout, "telegram") || !strings.Contains(res.Stdout, "discord") {
			t.Errorf("from %s: stdout = %s", from, res.Stdout)
		}
	}
	if _, ok := f.find("/sendPhoto"); !ok {
		t.Error("bare crier did not publish")
	}
}

// TestBareCrierTakesFlags checks a leading flag belongs to publish rather than
// being read as a command.
func TestBareCrierTakesFlags(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableTwo(f))
	res := crier(t, dir, nil, "--publish-dry-run", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, `"dryRun": true`) {
		t.Errorf("stdout = %s", res.Stdout)
	}
	if len(f.all()) != 0 {
		t.Errorf("a dry run made %d requests", len(f.all()))
	}
}

// --- U1-U6: the CLI fails closed ---------------------------------------------

// TestStrictCLIRefusesWhatItDoesNotKnow is U1 through U6 in one table: every
// way of typing something crier does not understand, and the code each gets.
//
// The rule is worth this much test because of what the alternative looks like:
// a mistyped --dry-run that publishes for real.
func TestStrictCLIRefusesWhatItDoesNotKnow(t *testing.T) {
	dir := newProject(t, "")
	for _, tt := range []struct {
		what string
		args []string
		code int
		says string
	}{
		{"U1 unknown subcommand", []string{"pubish"}, exitUsage, "unknown command"},
		{"U2 unknown top-level flag", []string{"--piblish"}, exitUsage, "not defined"},
		{"U3 unknown subcommand flag", []string{"render", "--nope"}, exitUsage, "not defined"},
		{"U4 unknown --set key", []string{"config", "--set", "render.widht=1"}, exitConfig, "unknown key"},
		{"U5 extra positional", []string{"render", "stray"}, exitUsage, "unexpected argument"},
		{"U6 mistyped flag is not routed", []string{"--dry-runn"}, exitUsage, "not defined"},
	} {
		res := crier(t, dir, nil, tt.args...)
		if res.Code != tt.code {
			t.Errorf("%s: code = %d, want %d (stderr: %s)", tt.what, res.Code, tt.code, res.Stderr)
		}
		if !strings.Contains(res.Stderr, tt.says) {
			t.Errorf("%s: stderr should contain %q: %s", tt.what, tt.says, res.Stderr)
		}
		if res.Stdout != "" {
			t.Errorf("%s: something ran anyway: %q", tt.what, res.Stdout)
		}
	}

	// U1 also owes the reader the list of what would have worked.
	res := crier(t, dir, nil, "pubish")
	for _, name := range []string{"publish", "render", "init", "ping", "platforms", "config", "self-update"} {
		if !strings.Contains(res.Stderr, name) {
			t.Errorf("%s is missing from the refusal:\n%s", name, res.Stderr)
		}
	}
}

// TestSetReachesAnyKey is the other half of U4: the escape hatch works.
func TestSetReachesAnyKey(t *testing.T) {
	dir := newProject(t, "")
	out := filepath.Join(dir, "set.png")
	res := crier(t, dir, nil, "render", "--set", "render.width=321", "--set", "render.output="+out)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	cfg, _ := decodeImage(t, out)
	if cfg.Width != 321 {
		t.Errorf("width = %d, want --set to have won", cfg.Width)
	}
}

func TestUnknownBareWordIsAUsageError(t *testing.T) {
	dir := newProject(t, "")
	res := crier(t, dir, nil, "publsih")
	if res.Code != exitUsage || !strings.Contains(res.Stderr, "unknown command") {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

func TestConfigDiscoveryPicksTheProjectYouAreIn(t *testing.T) {
	f := newFakes(t)
	root := t.TempDir()

	// Two projects side by side, with different templates and different
	// platforms enabled.
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, a, "template.html", `<html><body style="margin:0"><div style="width:100px;height:50px;background:#ff0000"></div></body></html>`)
	writeFile(t, b, "template.html", `<html><body style="margin:0"><div style="width:100px;height:50px;background:#0000ff"></div></body></html>`)
	writeFile(t, a, "crier.yaml", strings.Join([]string{
		"render:", "  template: template.html", "  width: 100", "  height: 50",
		"  hermetic-fonts: true",
		"publish:", "  dry-run: true", "  telegram:", "    enabled: true",
		"    token: t", "    chat-id: c", "    api-base-url: " + f.URL,
	}, "\n"))
	writeFile(t, b, "crier.yaml", strings.Join([]string{
		"render:", "  template: template.html", "  width: 100", "  height: 50",
		"  hermetic-fonts: true",
		"publish:", "  dry-run: true", "  discord:", "    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
	}, "\n"))

	for _, tc := range []struct {
		dir  string
		want string
	}{{a, "telegram"}, {b, "discord"}} {
		res := crier(t, tc.dir, nil, "publish", "--json")
		if res.Code != exitOK {
			t.Fatalf("%s: code=%d stderr=%s", tc.dir, res.Code, res.Stderr)
		}
		var rep struct {
			Plan []struct{ Platform string } `json:"plan"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
			t.Fatalf("%s: %v (%q)", tc.dir, err, res.Stdout)
		}
		if len(rep.Plan) != 1 || rep.Plan[0].Platform != tc.want {
			t.Errorf("in %s the plan was %+v, want %s", tc.dir, rep.Plan, tc.want)
		}
	}

	// And from a subdirectory of a, it is still a's project.
	deep := filepath.Join(a, "assets", "cards")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	res := crier(t, deep, nil, "publish", "--json")
	if res.Code != exitOK || !strings.Contains(res.Stdout, "telegram") {
		t.Errorf("from a subdirectory: code=%d stdout=%s", res.Code, res.Stdout)
	}
}

func TestEnvironmentOverridesTheFile(t *testing.T) {
	dir := newProject(t, "")
	out := filepath.Join(dir, "out.png")
	res := crier(t, dir, []string{"CRIER_RENDER_WIDTH=333"}, "render", "--render-output", out, "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	cfg, _ := decodeImage(t, out)
	if cfg.Width != 333 {
		t.Errorf("width = %d, want the environment to win over the file", cfg.Width)
	}
}

// TestSmokeFlagsOverrideTheEnvironment is in the release smoke subset: it is
// the precedence rule, and it exercises all three layers at once — the file
// sets the width, the environment overrides it, and the flag overrides that.
func TestSmokeFlagsOverrideTheEnvironment(t *testing.T) {
	dir := newProject(t, "")
	out := filepath.Join(dir, "out.png")
	res := crier(t, dir, []string{"CRIER_RENDER_WIDTH=333"},
		"render", "--render-width", "444", "--render-output", out)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	cfg, _ := decodeImage(t, out)
	if cfg.Width != 444 {
		t.Errorf("width = %d, want the flag to win", cfg.Width)
	}
}

func TestEveryKeyCanBeSetFromTheEnvironment(t *testing.T) {
	dir := newProject(t, "")
	// A representative sample from each group; the exhaustive check is the
	// unit-level anti-drift test.
	env := []string{
		"CRIER_LOG_FORMAT=json",
		"CRIER_RENDER_JPEG_QUALITY=71",
		"CRIER_HTTP_RETRY_MAX=5",
		"CRIER_STAGE_MODE=url",
		"CRIER_STAGE_URL=https://cdn.example/x.jpg",
		"CRIER_PUBLISH_CONCURRENCY=2",
		"CRIER_PUBLISH_REDDIT_SUBREDDIT=golang",
	}
	res := crier(t, dir, env, "config", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	var rep struct {
		Values map[string]any `json:"values"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"log.format": "json", "render.jpeg-quality": float64(71),
		"http.retry-max": float64(5), "stage.mode": "url",
		"publish.concurrency": float64(2), "publish.reddit.subreddit": "golang",
	} {
		if rep.Values[key] != want {
			t.Errorf("%s = %v, want %v", key, rep.Values[key], want)
		}
	}
}

func TestUnknownConfigKeyIsAConfigError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "crier.yaml", "render:\n  widht: 10\n")
	res := crier(t, dir, nil, "config")
	if res.Code != exitConfig || !strings.Contains(res.Stderr, "widht") {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

func TestConfigRefsAreFollowed(t *testing.T) {
	dir := newProject(t, "")
	writeFile(t, dir, "shared.yaml", "level: warn\nformat: json\n")
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  $ref: shared.yaml",
		"render:",
		"  template: template.html",
		"  hermetic-fonts: true",
	}, "\n"))
	res := crier(t, dir, nil, "config", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, `"log.level": "warn"`) {
		t.Errorf("stdout = %s", res.Stdout)
	}
}

func TestSecretsAreRedacted(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, "  telegram:\n    enabled: true\n    token: secret-token-value\n"+
		"    chat-id: c\n    api-base-url: "+f.URL+"\n")
	res := crier(t, dir, nil, "config")
	if res.Code != exitOK {
		t.Fatalf("code=%d", res.Code)
	}
	if strings.Contains(res.Stdout, "secret-token-value") {
		t.Fatal("the token was printed in the clear")
	}
}

func TestUsageErrorForAnUnknownFlag(t *testing.T) {
	dir := newProject(t, "")
	res := crier(t, dir, nil, "render", "--nope")
	if res.Code != exitUsage {
		t.Errorf("code = %d", res.Code)
	}
}

// --- publishing ------------------------------------------------------------

// enableAll turns on every platform against the fakes.
func enableAll(f *fakes, extra string) string {
	var b strings.Builder
	b.WriteString(f.platformConfig())
	lines := strings.Split(b.String(), "\n")
	// Insert "enabled: true" after each platform header.
	var out []string
	for _, line := range lines {
		out = append(out, line)
		if strings.HasPrefix(line, "  ") && strings.HasSuffix(line, ":") &&
			!strings.HasPrefix(line, "    ") {
			out = append(out, "    enabled: true")
		}
	}
	return strings.Join(out, "\n") + extra
}

// TestSmokePublishToEveryPlatform is in the release smoke subset: nine
// publishers fanned out against fakes, which is the one test that touches
// every platform's request shape.
func TestSmokePublishToEveryPlatform(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableAll(f, "\n  caption: \"{{ .title }} {{ .version }} via {{ .Platform }}\"\n")+
		"\nstage:\n  mode: url\n  url: "+f.URL+"/staged/image.jpg\n")

	res := crier(t, dir, nil, "publish", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	var rep struct {
		Results []struct {
			Platform string `json:"platform"`
			OK       bool   `json:"ok"`
			ID       string `json:"id"`
			Error    string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatalf("%v\n%s", err, res.Stdout)
	}
	if len(rep.Results) != 9 {
		t.Fatalf("published to %d platforms, want 9: %+v", len(rep.Results), rep.Results)
	}
	for _, r := range rep.Results {
		if !r.OK {
			t.Errorf("%s failed: %s", r.Platform, r.Error)
		}
	}

	// Each platform's own contract was exercised.
	for _, fragment := range []string{
		"/sendPhoto", "/discord/webhook", "/mastodon/api/v1/statuses",
		"/x/2/tweets", "/instagram/ig-user/media_publish", "/facebook/fb-page/photos",
		"/tiktok/v2/post/publish/content/init/", "/linkedin/rest/posts",
		"/reddit/api/submit",
	} {
		if _, ok := f.find(fragment); !ok {
			t.Errorf("no request reached %s", fragment)
		}
	}

	// The caption template was rendered per platform.
	tg, _ := f.find("/sendPhoto")
	if !strings.Contains(tg.Body, "end to end 9.9.9 via telegram") {
		t.Errorf("telegram caption not resolved: %q", tg.Body)
	}
	dc, _ := f.find("/discord/webhook")
	if !strings.Contains(dc.Body, "via discord") {
		t.Errorf("discord caption not resolved: %q", dc.Body)
	}

	// Reddit's mandatory descriptive User-Agent went out.
	rd, _ := f.find("/reddit/api/submit")
	if !strings.Contains(rd.Header.Get("User-Agent"), "com.yohimik.crier") {
		t.Errorf("reddit user agent = %q", rd.Header.Get("User-Agent"))
	}
	// LinkedIn's two mandatory headers went out.
	li, _ := f.find("/linkedin/rest/posts")
	if li.Header.Get("LinkedIn-Version") == "" || li.Header.Get("X-Restli-Protocol-Version") != "2.0.0" {
		t.Errorf("linkedin headers = %v", li.Header)
	}
}

func TestPerPlatformCaptionOverrideFromTheEnvironment(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableTwo(f)+"\n  caption: \"shared for {{ .Platform }}\"\n")

	res := crier(t, dir, []string{
		`CRIER_PUBLISH_DISCORD_CAPTION=only discord, {{ .version }}`,
	}, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	tg, _ := f.find("/sendPhoto")
	if !strings.Contains(tg.Body, "shared for telegram") {
		t.Errorf("telegram body = %q", tg.Body)
	}
	dc, _ := f.find("/discord/webhook")
	if !strings.Contains(dc.Body, "only discord, 9.9.9") {
		t.Errorf("discord body = %q", dc.Body)
	}
}

// enableTwo turns on telegram and discord only.
func enableTwo(f *fakes) string {
	return strings.Join([]string{
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
	}, "\n")
}

func TestDryRunMakesNoRequests(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableTwo(f)+"\n  dry-run: true\n")
	res := crier(t, dir, nil, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if len(f.all()) != 0 {
		t.Fatalf("a dry run made %d requests", len(f.all()))
	}
	if !strings.Contains(res.Stdout, "telegram") || !strings.Contains(res.Stdout, "discord") {
		t.Errorf("stdout = %s", res.Stdout)
	}
}

func TestPartialFailureIsExitFour(t *testing.T) {
	f := newFakes(t)
	// Discord points at a path the fakes do not serve, so it 404s.
	dir := newProject(t, strings.Join([]string{
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: t",
		"    chat-id: c",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/nowhere",
	}, "\n"))
	res := crier(t, dir, nil, "publish")
	if res.Code != exitPartial {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "failed") || !strings.Contains(res.Stdout, "ok") {
		t.Errorf("stdout should show both outcomes:\n%s", res.Stdout)
	}
}

func TestTotalFailureIsExitFive(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/nowhere",
	}, "\n"))
	res := crier(t, dir, nil, "publish")
	if res.Code != exitPublish {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

func TestInstagramWithoutStagingIsAConfigError(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: t",
		"    user-id: u",
	}, "\n"))
	res := crier(t, dir, nil, "publish")
	// Nothing can produce a URL, so this is refused before anything is
	// rendered rather than failing at the platform.
	if res.Code != exitConfig {
		t.Fatalf("code=%d stderr=%s stdout=%s", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stderr, "stage.mode") {
		t.Errorf("the failure should name stage.mode:\n%s", res.Stderr)
	}
}

func TestNoPlatformEnabledIsAConfigError(t *testing.T) {
	dir := newProject(t, "")
	res := crier(t, dir, nil, "publish")
	if res.Code != exitConfig {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

func TestMisconfiguredPlatformIsAConfigError(t *testing.T) {
	dir := newProject(t, "  telegram:\n    enabled: true\n")
	res := crier(t, dir, nil, "publish")
	if res.Code != exitConfig || !strings.Contains(res.Stderr, "publish.telegram.token") {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

// --- staging ---------------------------------------------------------------

func TestStageServerServesTheImageToTheFetcher(t *testing.T) {
	f := newFakes(t)
	// Instagram fetches the image URL itself; the stage server is what it
	// fetches from, so the run only succeeds if the server really served it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	dir := newProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: t",
		"    user-id: ig-user",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
	}, "\n")+"\nstage:\n  mode: server\n  server:\n    listen: "+addr+
		"\n    public-url: http://"+addr+"\n")

	res := crier(t, dir, nil, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	container, ok := f.find("/instagram/ig-user/media")
	if !ok {
		t.Fatal("no container was created")
	}
	if !strings.Contains(container.Body, "image_url=http") {
		t.Errorf("container body = %q", container.Body)
	}

	// And the port is free again once crier has exited.
	ln2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the stage server did not release its port: %v", err)
	}
	_ = ln2.Close()
}

func TestStageServerWithATunnel(t *testing.T) {
	f := newFakes(t)
	self := selfPath(t)

	dir := newProject(t, strings.Join([]string{
		"  tiktok:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/tiktok",
		"    token: t",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
	}, "\n")+strings.Join([]string{
		"",
		"stage:",
		"  mode: server",
		"  server:",
		"    listen: 127.0.0.1:0",
		"    tunnel:",
		"      mode: custom",
		"      bin: " + self,
		"      url-pattern: 'url=(https://\\S+)'",
		"      startup-timeout: 10s",
	}, "\n"))

	res := crier(t, dir, []string{
		helperEnv + "=tunnel",
		helperURLEnv + "=https://tunnel.example",
	}, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	init, ok := f.find("/tiktok/v2/post/publish/content/init/")
	if !ok {
		t.Fatal("tiktok was not called")
	}
	if !strings.Contains(init.Body, "https://tunnel.example/") {
		t.Errorf("the tunnel's URL should be what tiktok is told to pull: %q", init.Body)
	}
	if !strings.Contains(res.Stderr, "the tunnel is up") {
		t.Errorf("stderr = %s", res.Stderr)
	}
}

func TestStageServerTunnelFailureIsExitSix(t *testing.T) {
	f := newFakes(t)
	self := selfPath(t)
	dir := newProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: t",
		"    user-id: ig-user",
	}, "\n")+strings.Join([]string{
		"",
		"stage:",
		"  mode: server",
		"  server:",
		"    listen: 127.0.0.1:0",
		"    tunnel:",
		"      mode: custom",
		"      bin: " + self,
		"      url-pattern: 'url=(https://\\S+)'",
		"      startup-timeout: 5s",
	}, "\n"))

	res := crier(t, dir, []string{
		helperEnv + "=tunnel",
		helperFailEnv + "=the tunnel refused to start",
	}, "publish")
	if res.Code != exitStaging {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "the tunnel refused to start") {
		t.Errorf("the helper's own message should surface: %s", res.Stderr)
	}
}

func TestStageURLMode(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: t",
		"    user-id: ig-user",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
	}, "\n")+"\nstage:\n  mode: url\n  url: https://cdn.example/pre-hosted.jpg\n")

	res := crier(t, dir, nil, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	container, _ := f.find("/instagram/ig-user/media")
	if !strings.Contains(container.Body, "pre-hosted.jpg") {
		t.Errorf("container body = %q", container.Body)
	}
}

func TestStageURLModeWithoutAURLIsAConfigError(t *testing.T) {
	dir := newProject(t, "")
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"render:", "  template: template.html", "  hermetic-fonts: true",
		"stage:", "  mode: url",
	}, "\n"))
	res := crier(t, dir, nil, "config")
	if res.Code != exitConfig || !strings.Contains(res.Stderr, "stage.url") {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

// --- format negotiation ----------------------------------------------------

func TestInstagramForcesJPEGWhileOthersKeepPNG(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: t",
		"    user-id: ig-user",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: t",
		"    chat-id: c",
	}, "\n")+"\nstage:\n  mode: url\n  url: https://cdn.example/x.jpg\n")

	res := crier(t, dir, nil, "publish", "--render-format", "png", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	var rep struct {
		Variants []struct {
			Files []string `json:"files"`
		} `json:"variants"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatal(err)
	}
	var haveJPEG, havePNG bool
	for _, v := range rep.Variants {
		for _, file := range v.Files {
			switch filepath.Ext(file) {
			case ".jpg":
				haveJPEG = true
			case ".png":
				havePNG = true
			}
		}
	}
	if !haveJPEG || !havePNG {
		t.Errorf("both formats should have been encoded: %+v", rep.Variants)
	}
	// Telegram got the PNG it prefers... it prefers JPEG first, so what
	// matters is that Instagram got a JPEG URL and the run succeeded.
	if _, ok := f.find("/instagram/ig-user/media_publish"); !ok {
		t.Error("instagram did not publish")
	}
}

// --- variants --------------------------------------------------------------

func TestOverlaysProduceDifferentImagesPerPlatform(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		enableTwo(f),
		"  discord:",
		"    overlay: discord-overlay.html",
		"    width: 300",
		"    height: 150",
	}, "\n"))
	// The discord block above re-opens the key; write the config by hand
	// instead so the YAML stays valid.
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"render:",
		"  template: template.html",
		"  data: data.yaml",
		"  width: 240",
		"  height: 120",
		"  hermetic-fonts: true",
		"publish:",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: t",
		"    chat-id: c",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
		"    overlay: discord-overlay.html",
		"    width: 300",
		"    height: 150",
	}, "\n"))
	writeFile(t, dir, "discord-overlay.html", `{{ define "headline" }}<h1>discord only</h1>{{ end }}`)

	res := crier(t, dir, nil, "publish", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	var rep struct {
		Variants []struct {
			Name      string   `json:"name"`
			Platforms []string `json:"platforms"`
			Width     int      `json:"width"`
			Height    int      `json:"height"`
			Files     []string `json:"files"`
		} `json:"variants"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Variants) != 2 {
		t.Fatalf("variants = %d, want one per layout: %+v", len(rep.Variants), rep.Variants)
	}
	sizes := map[string][2]int{}
	for _, v := range rep.Variants {
		sizes[v.Name] = [2]int{v.Width, v.Height}
	}
	if sizes["telegram"] != [2]int{240, 120} {
		t.Errorf("telegram variant = %v", sizes["telegram"])
	}
	if sizes["discord"] != [2]int{300, 150} {
		t.Errorf("discord variant = %v", sizes["discord"])
	}

	// The two platforms really did receive different images.
	tg, ok := f.find("/sendPhoto")
	if !ok {
		t.Fatal("telegram was not called")
	}
	dc, ok := f.find("/discord/webhook")
	if !ok {
		t.Fatal("discord was not called")
	}
	tgCfg, err := imageInBody(tg.Body)
	if err != nil {
		t.Fatalf("telegram body: %v", err)
	}
	dcCfg, err := imageInBody(dc.Body)
	if err != nil {
		t.Fatalf("discord body: %v", err)
	}
	if tgCfg.Width != 240 || tgCfg.Height != 120 {
		t.Errorf("telegram received %dx%d", tgCfg.Width, tgCfg.Height)
	}
	if dcCfg.Width != 300 || dcCfg.Height != 150 {
		t.Errorf("discord received %dx%d", dcCfg.Width, dcCfg.Height)
	}
}

// imageInBody finds the encoded image inside a multipart body and reads its
// dimensions, which is how a test asserts what a platform actually received.
func imageInBody(body string) (image.Config, error) {
	for _, magic := range []string{"\x89PNG\r\n\x1a\n", "\xff\xd8\xff"} {
		if i := strings.Index(body, magic); i >= 0 {
			cfg, _, err := image.DecodeConfig(strings.NewReader(body[i:]))
			return cfg, err
		}
	}
	return image.Config{}, errors.New("no image found in the request body")
}

// --- video -----------------------------------------------------------------

func TestVideoIsRenderedAndPublished(t *testing.T) {
	f := newFakes(t)
	self := selfPath(t)
	dir := newProject(t, enableTwo(f))
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"render:",
		"  template: template.html",
		"  data: data.yaml",
		"  width: 80",
		"  height: 40",
		"  hermetic-fonts: true",
		"  video:",
		"    enabled: true",
		"    fps: 5",
		"    frames: 3",
		"    ffmpeg-bin: " + self,
		"publish:",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: t",
		"    chat-id: c",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
	}, "\n"))
	writeFile(t, dir, "template.html",
		`<html><body style="margin:0;background:#fff">`+
			`<div style="width:80px;height:40px;background:#00{{ printf "%02x" .Video.Frame }}00"></div>`+
			`</body></html>`)

	res := crier(t, dir, []string{helperEnv + "=ffmpeg"}, "publish", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if _, ok := f.find("/sendVideo"); !ok {
		t.Error("telegram should have been sent a video")
	}
	if !strings.Contains(res.Stderr, "encoded video") {
		t.Errorf("stderr = %s", res.Stderr)
	}
}

// TestGIFGoesOutAsAnAnimation is the E4 GIF path end to end: the same frame
// pipeline, ffmpeg's palette filter, and two platforms that take an animation
// — with Telegram's different method as the thing that would silently go wrong.
func TestGIFGoesOutAsAnAnimation(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, "")
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  level: debug",
		"render:",
		"  template: template.html",
		"  width: 80",
		"  height: 40",
		"  hermetic-fonts: true",
		"  video:",
		"    enabled: true",
		"    format: gif",
		"    frames: 3",
		"    fps: 10",
		"    ffmpeg-bin: " + selfPath(t),
		"publish:",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: t",
		"    chat-id: c",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
	}, "\n"))
	writeFile(t, dir, "template.html",
		`<html><body style="margin:0;background:#fff">`+
			`<div style="width:80px;height:40px;background:#00{{ printf "%02x" .Video.Frame }}00"></div>`+
			`</body></html>`)

	res := crier(t, dir, []string{helperEnv + "=ffmpeg"}, "publish", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}

	// Telegram takes an animation through a method of its own; sendVideo would
	// be accepted and then shown as a still.
	tg, ok := f.find("/sendAnimation")
	if !ok {
		t.Fatalf("telegram was not sent an animation; it got %d requests", len(f.all()))
	}
	if _, ok := f.find("/sendVideo"); ok {
		t.Error("a GIF went out through sendVideo")
	}
	if !strings.Contains(tg.Body, "GIF89a") {
		t.Errorf("what reached telegram is not a GIF: %.80q", tg.Body)
	}
	if !strings.Contains(tg.Body, `name="animation"`) {
		t.Errorf("the part is not an animation: %.200q", tg.Body)
	}

	dc, ok := f.find("/discord/webhook")
	if !ok {
		t.Fatal("discord was not called")
	}
	if !strings.Contains(dc.Body, "GIF89a") {
		t.Errorf("what reached discord is not a GIF: %.80q", dc.Body)
	}
}

// TestGIFToAPlatformThatCannotTakeOne fails before anything is rendered, which
// is the point of declaring what each platform accepts.
func TestGIFToAPlatformThatCannotTakeOne(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, "")
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"render:",
		"  template: template.html",
		"  hermetic-fonts: true",
		"  video:",
		"    enabled: true",
		"    format: gif",
		"    frames: 2",
		"    ffmpeg-bin: " + selfPath(t),
		"publish:",
		"  linkedin:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/linkedin",
		"    token: li",
		"    author-urn: \"urn:li:person:e2e\"",
	}, "\n"))

	res := crier(t, dir, []string{helperEnv + "=ffmpeg"}, "publish")
	if res.Code != exitConfig {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "linkedin") || !strings.Contains(res.Stderr, "GIF") {
		t.Errorf("the refusal should name the platform and the format: %s", res.Stderr)
	}
	if len(f.all()) != 0 {
		t.Errorf("it made %d requests before refusing", len(f.all()))
	}
}

func TestVideoWithoutFFmpegIsARenderError(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableTwo(f))
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"render:",
		"  template: template.html",
		"  hermetic-fonts: true",
		"  video:",
		"    enabled: true",
		"    frames: 2",
		"    ffmpeg-bin: crier-no-such-ffmpeg",
		"publish:",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
	}, "\n"))

	res := crier(t, dir, nil, "publish")
	if res.Code != exitRender {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "ffmpeg was not found") {
		t.Errorf("stderr = %s", res.Stderr)
	}
}

// TestTheThreeConfigFormatsAgree is C7: the same configuration written three
// ways has to mean the same thing.
//
// Formats are the loader's business rather than crier's, which is exactly why
// this is worth asserting from the outside: nothing in crier chooses a parser,
// so nothing in crier would notice if one of the three stopped working.
func TestTheThreeConfigFormatsAgree(t *testing.T) {
	files := map[string]string{
		"crier.yaml": `render:
  template: template.html
  data: data.yaml
  width: 640
  height: 360
  scale: 2
  hermetic-fonts: true
  video:
    ffmpeg-args: ["-preset", "fast"]
log:
  level: debug
publish:
  caption: "hello {{ .title }}"
  concurrency: 3
  telegram:
    enabled: true
    chat-id: "@c"
    token: tok
`,
		"crier.json": `{
  "render": {
    "template": "template.html",
    "data": "data.yaml",
    "width": 640,
    "height": 360,
    "scale": 2,
    "hermetic-fonts": true,
    "video": { "ffmpeg-args": ["-preset", "fast"] }
  },
  "log": { "level": "debug" },
  "publish": {
    "caption": "hello {{ .title }}",
    "concurrency": 3,
    "telegram": { "enabled": true, "chat-id": "@c", "token": "tok" }
  }
}
`,
		"crier.toml": `[render]
template = "template.html"
data = "data.yaml"
width = 640
height = 360
scale = 2
hermetic-fonts = true

[render.video]
ffmpeg-args = ["-preset", "fast"]

[log]
level = "debug"

[publish]
caption = "hello {{ .title }}"
concurrency = 3

[publish.telegram]
enabled = true
chat-id = "@c"
token = "tok"
`,
	}

	var first string
	for _, name := range []string{"crier.yaml", "crier.json", "crier.toml"} {
		dir := t.TempDir()
		writeFile(t, dir, "template.html", baseTemplate)
		writeFile(t, dir, "data.yaml", "title: formats\n")
		writeFile(t, dir, name, files[name])

		res := crier(t, dir, nil, "config", "--json", "--all")
		if res.Code != exitOK {
			t.Fatalf("%s: code=%d stderr=%s", name, res.Code, res.Stderr)
		}
		var rep struct {
			File   string         `json:"file"`
			Values map[string]any `json:"values"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasSuffix(rep.File, name) {
			t.Errorf("%s: the file it found is %q", name, rep.File)
		}

		// The values are compared rather than the whole report: the report
		// carries the file name, which is the one thing that has to differ.
		//
		// Paths are anchored against the config file's own directory, which
		// is a different temporary directory each time round, so the two
		// path-typed keys are compared by their base name.
		for _, key := range []string{"render.template", "render.data"} {
			v, _ := rep.Values[key].(string)
			rep.Values[key] = filepath.Base(v)
		}
		normalised, err := json.Marshal(rep.Values)
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = string(normalised)
			continue
		}
		if string(normalised) != first {
			t.Errorf("%s resolves differently:\n got: %s\nwant: %s", name, normalised, first)
		}
	}
}

// --- ping ------------------------------------------------------------------

// TestPingChecksEveryEnabledPlatform is the safe setup check: nine identity
// endpoints, no post anywhere.
func TestPingChecksEveryEnabledPlatform(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, enableAll(f, ""))

	res := crier(t, dir, nil, "ping", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	var rep struct {
		Results []struct {
			Target  string `json:"target"`
			OK      bool   `json:"ok"`
			Account string `json:"account"`
			Error   string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatalf("%v\n%s", err, res.Stdout)
	}
	if len(rep.Results) != 9 {
		t.Fatalf("checked %d targets, want 9: %+v", len(rep.Results), rep.Results)
	}
	for _, r := range rep.Results {
		if !r.OK {
			t.Errorf("%s failed: %s", r.Target, r.Error)
		}
		if r.Account == "" {
			t.Errorf("%s reported no account", r.Target)
		}
	}

	// The read-only endpoint of each platform was the one that got hit.
	for _, fragment := range []string{
		"/getMe", "/mastodon/api/v1/accounts/verify_credentials", "/x/2/users/me",
		"/instagram/ig-user", "/facebook/fb-page",
		"/tiktok/v2/post/publish/creator_info/query/",
		"/linkedin/v2/userinfo", "/reddit/api/v1/me",
	} {
		if _, ok := f.find(fragment); !ok {
			t.Errorf("ping did not reach %s", fragment)
		}
	}

	// And nothing that posts was touched. This is the property that makes ping
	// safe to run against a live account.
	for _, fragment := range []string{
		"/sendPhoto", "/sendVideo", "/x/2/tweets", "/mastodon/api/v1/statuses",
		"/instagram/ig-user/media_publish", "/facebook/fb-page/photos",
		"/linkedin/rest/posts", "/reddit/api/submit",
	} {
		if _, ok := f.find(fragment); ok {
			t.Errorf("ping posted something: %s was called", fragment)
		}
	}
}

// TestPingWithOneBadTokenIsExitFour checks the partial case reports which
// platform is broken rather than only that something is.
func TestPingWithOneBadTokenIsExitFour(t *testing.T) {
	f := newFakes(t)
	dir := newProject(t, strings.ReplaceAll(enableAll(f, ""), "token: x-token", "token: bad-token"))

	res := crier(t, dir, nil, "ping")
	if res.Code != exitPartial {
		t.Fatalf("code=%d stderr=%s stdout=%s", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "x") || !strings.Contains(res.Stdout, "failed") {
		t.Errorf("the failing platform should be named in the table:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "x") {
		t.Errorf("the failure should be logged: %s", res.Stderr)
	}
	// The other eight still reported.
	if strings.Count(res.Stdout, "ok") < 8 {
		t.Errorf("the other platforms should still have been checked:\n%s", res.Stdout)
	}
}

func TestPingWithNoPlatformIsAConfigError(t *testing.T) {
	dir := newProject(t, "")
	res := crier(t, dir, nil, "ping")
	if res.Code != exitConfig || !strings.Contains(res.Stderr, "no platform is enabled") {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

// --- entry modes -------------------------------------------------------------

// makeImage writes a real PNG or JPEG, because the pipeline sniffs the bytes.
func makeImage(t *testing.T, path string, w, h int, asJPEG bool) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 180, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if asJPEG {
		err = jpeg.Encode(f, img, nil)
	} else {
		err = png.Encode(f, img)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// TestPublishOnlyUsesAnExistingFile is entry mode 3: a poster somebody drew
// elsewhere is a perfectly good thing to publish, and crier should not need a
// template to do it.
func TestPublishOnlyUsesAnExistingFile(t *testing.T) {
	f := newFakes(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "card.png")
	makeImage(t, input, 64, 32, false)

	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  level: debug",
		"publish:",
		"  input: card.png",
		"  caption: \"a file that already existed\"",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: t",
		"    chat-id: c",
	}, "\n"))

	// No template, no data file, no render. Bare crier.
	res := crier(t, dir, nil)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	tg, ok := f.find("/sendPhoto")
	if !ok {
		t.Fatal("telegram was not called")
	}
	got, err := imageInBody(tg.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 64 || got.Height != 32 {
		t.Errorf("telegram received %dx%d, want the input untouched", got.Width, got.Height)
	}
	if !strings.Contains(tg.Body, "a file that already existed") {
		t.Error("the caption did not go out")
	}
	// Nothing was laid out.
	if strings.Contains(res.Stderr, "rendered document") {
		t.Errorf("something was rendered: %s", res.Stderr)
	}
}

// TestPublishOnlyTranscodesForInstagram: Instagram will not fetch a PNG, so a
// PNG input has to become a JPEG on the way. The fake asserts what was staged.
func TestPublishOnlyTranscodesForInstagram(t *testing.T) {
	f := newFakes(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "card.png")
	makeImage(t, input, 48, 48, false)

	// A local staging server, so what Instagram's fetcher receives is a real
	// file crier produced rather than a URL nobody follows.
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  level: debug",
		"stage:",
		"  mode: server",
		"  server:",
		"    listen: 127.0.0.1:0",
		"    public-url: http://127.0.0.1:0",
		"publish:",
		"  input: card.png",
		"  instagram:",
		"    enabled: true",
		"    api-base-url: " + f.URL + "/instagram",
		"    token: ig",
		"    user-id: ig-user",
		"    poll-interval: 1ms",
		"    poll-timeout: 5s",
	}, "\n"))

	res := crier(t, dir, nil, "publish", "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	var rep struct {
		Variants []struct {
			Files []string `json:"files"`
			URL   string   `json:"url"`
		} `json:"variants"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Variants) != 1 {
		t.Fatalf("variants = %+v", rep.Variants)
	}
	// Both the original and the transcode exist; the staged one is the JPEG.
	var sawJPEG bool
	for _, file := range rep.Variants[0].Files {
		if strings.HasSuffix(file, ".jpg") || strings.HasSuffix(file, ".jpeg") {
			sawJPEG = true
		}
	}
	if !sawJPEG {
		t.Errorf("no JPEG was produced for Instagram: %v", rep.Variants[0].Files)
	}
	if !strings.Contains(res.Stderr, "transcoded the input") {
		t.Errorf("the transcode was not reported: %s", res.Stderr)
	}
	if !strings.Contains(rep.Variants[0].URL, ".jpg") {
		t.Errorf("Instagram was given %q, and it will not fetch a PNG", rep.Variants[0].URL)
	}
}

// TestPublishOnlyWithAVideo passes the clip through untouched: re-encoding
// somebody's video to satisfy a format preference would be a surprising thing
// for a publish command to do.
func TestPublishOnlyWithAVideo(t *testing.T) {
	f := newFakes(t)
	dir := t.TempDir()
	// A minimal MP4 header is enough: crier sniffs the bytes and uploads them.
	if err := os.WriteFile(filepath.Join(dir, "clip.mp4"),
		append([]byte{0, 0, 0, 0x18}, "ftypmp42\x00\x00\x00\x00moov"...), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"publish:",
		"  input: clip.mp4",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: t",
		"    chat-id: c",
	}, "\n"))

	res := crier(t, dir, nil, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if _, ok := f.find("/sendVideo"); !ok {
		t.Error("an MP4 input should go out as a video")
	}
}

// TestEncodeOnlyFromFrames is entry mode 4: frames made anywhere else become a
// crier post through the same ffmpeg pipeline.
func TestEncodeOnlyFromFrames(t *testing.T) {
	f := newFakes(t)
	dir := t.TempDir()
	frames := filepath.Join(dir, "frames")
	if err := os.MkdirAll(frames, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		makeImage(t, filepath.Join(frames, fmt.Sprintf("f-%04d.png", i)), 32, 16, false)
	}

	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  level: debug",
		"render:",
		"  video:",
		"    format: gif",
		"    fps: 8",
		"    frames-input: frames",
		"    ffmpeg-bin: " + selfPath(t),
		"publish:",
		"  discord:",
		"    enabled: true",
		"    webhook-url: " + f.URL + "/discord/webhook",
	}, "\n"))

	res := crier(t, dir, []string{helperEnv + "=ffmpeg"}, "publish")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "encoding frames from disk") {
		t.Errorf("stderr = %s", res.Stderr)
	}
	dc, ok := f.find("/discord/webhook")
	if !ok {
		t.Fatal("discord was not called")
	}
	if !strings.Contains(dc.Body, "GIF89a") {
		t.Errorf("what reached discord is not a GIF: %.80q", dc.Body)
	}

	// And `crier render` in the same directory encodes without publishing.
	out := filepath.Join(dir, "out.gif")
	res = crier(t, dir, []string{helperEnv + "=ffmpeg"}, "render", "--render-output", out)
	if res.Code != exitOK {
		t.Fatalf("render: code=%d stderr=%s", res.Code, res.Stderr)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "GIF89a") {
		t.Errorf("crier render wrote %.20q", body)
	}
}

// TestMixedModesAreAConfigError: two answers to "where does the artifact come
// from" is a configuration whose author believed two different things.
func TestMixedModesAreAConfigError(t *testing.T) {
	dir := t.TempDir()
	makeImage(t, filepath.Join(dir, "card.png"), 8, 8, false)
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"render:",
		"  video:",
		"    frames-input: frames",
		"publish:",
		"  input: card.png",
		"  discord:",
		"    enabled: true",
		"    webhook-url: http://127.0.0.1:1/webhook",
	}, "\n"))

	res := crier(t, dir, nil, "publish")
	if res.Code != exitConfig {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "publish.input") ||
		!strings.Contains(res.Stderr, "frames-input") {
		t.Errorf("the error should name both keys: %s", res.Stderr)
	}

	// And `crier render` with an input has nothing to render.
	writeFile(t, dir, "crier.yaml", "publish:\n  input: card.png\n")
	res = crier(t, dir, nil, "render")
	if res.Code != exitConfig || !strings.Contains(res.Stderr, "nothing for") {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

// --- custom platforms --------------------------------------------------------

// TestCustomPlatformIsAPeer is E10's whole claim: a shell script is a platform
// like any other. It fans out beside a built-in, gets the staged URL, the
// rendered caption and the artifact, and reports back what it published.
func TestCustomPlatformIsAPeer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script is sh")
	}
	f := newFakes(t)
	dir := t.TempDir()
	dump := filepath.Join(dir, "env.txt")

	writeFile(t, dir, "template.html", baseTemplate)
	writeFile(t, dir, "data.yaml", "title: custom\nversion: 4.5.6\n")
	writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  level: debug",
		"render:",
		"  template: template.html",
		"  data: data.yaml",
		"  width: 240",
		"  height: 120",
		"  hermetic-fonts: true",
		"stage:",
		"  mode: url",
		"  url: " + f.URL + "/staged/image.png",
		"publish:",
		"  caption: \"{{ .title }} {{ .version }} via {{ .Platform }}\"",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		"  custom:",
		"    webhook:",
		"      enabled: true",
		"      needs-url: true",
		"      command: >",
		"        env | grep '^CRIER_' | sort > " + dump + ";",
		"        cp \"$CRIER_ARTIFACT\" " + filepath.Join(dir, "received.png") + ";",
		"        echo id=hook-1 >> \"$CRIER_OUTPUT\";",
		"        echo link=https://example.test/hook-1 >> \"$CRIER_OUTPUT\"",
		"      env:",
		"        MY_TOKEN: from-config",
	}, "\n"))

	res := crier(t, dir, nil, "--json")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	var rep struct {
		Results []struct {
			Platform string `json:"platform"`
			OK       bool   `json:"ok"`
			ID       string `json:"id"`
			URL      string `json:"url"`
			Error    string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatalf("%v\n%s", err, res.Stdout)
	}
	if len(rep.Results) != 2 {
		t.Fatalf("results = %+v, want the built-in and the custom", rep.Results)
	}
	var hook *struct {
		Platform string `json:"platform"`
		OK       bool   `json:"ok"`
		ID       string `json:"id"`
		URL      string `json:"url"`
		Error    string `json:"error"`
	}
	for i := range rep.Results {
		if rep.Results[i].Platform == "webhook" {
			hook = &rep.Results[i]
		}
	}
	if hook == nil {
		t.Fatalf("the custom platform is missing: %+v", rep.Results)
	}
	if !hook.OK || hook.ID != "hook-1" || hook.URL != "https://example.test/hook-1" {
		t.Errorf("custom result = %+v", hook)
	}

	env := map[string]string{}
	body, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	// The script copied what it was given, which proves the path pointed at a
	// readable rendering rather than only at a plausible name. The artifact
	// itself is gone by now: crier cleans up after the run.
	cfg, format := decodeImage(t, filepath.Join(dir, "received.png"))
	if format != "png" || cfg.Width != 240 || cfg.Height != 120 {
		t.Errorf("the script received %s %dx%d", format, cfg.Width, cfg.Height)
	}
	if !strings.HasSuffix(env["CRIER_ARTIFACT"], ".png") {
		t.Errorf("CRIER_ARTIFACT = %q", env["CRIER_ARTIFACT"])
	}
	if env["CRIER_ARTIFACT_KIND"] != "image" || env["CRIER_ARTIFACT_FORMAT"] != "png" {
		t.Errorf("kind=%q format=%q", env["CRIER_ARTIFACT_KIND"], env["CRIER_ARTIFACT_FORMAT"])
	}
	if env["CRIER_CAPTION"] != "custom 4.5.6 via webhook" {
		t.Errorf("CRIER_CAPTION = %q", env["CRIER_CAPTION"])
	}
	if env["CRIER_URL"] != f.URL+"/staged/image.png" {
		t.Errorf("CRIER_URL = %q", env["CRIER_URL"])
	}
	if env["CRIER_PLATFORM"] != "webhook" {
		t.Errorf("CRIER_PLATFORM = %q", env["CRIER_PLATFORM"])
	}

	// The built-in beside it still posted, which is the "peer" half.
	if _, ok := f.find("/sendPhoto"); !ok {
		t.Error("the built-in platform did not publish")
	}
}

// TestCustomPlatformFailureIsPartial: a script that exits non-zero fails its
// platform and no other, exactly like an API refusing a post.
func TestCustomPlatformFailureIsPartial(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script is sh")
	}
	f := newFakes(t)
	dir := newProject(t, strings.Join([]string{
		"  telegram:",
		"    enabled: true",
		"    api-base-url: " + f.URL,
		"    token: tg-token",
		"    chat-id: \"@crier\"",
		"  custom:",
		"    webhook:",
		"      enabled: true",
		"      command: \"echo 'the hook said no' >&2; exit 7\"",
	}, "\n"))

	res := crier(t, dir, nil, "publish")
	if res.Code != exitPartial {
		t.Fatalf("code=%d stderr=%s stdout=%s", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "webhook") || !strings.Contains(res.Stdout, "failed") {
		t.Errorf("the failing platform should be named:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "the hook said no") {
		t.Errorf("the script's own output should reach the error: %s", res.Stderr)
	}
	if _, ok := f.find("/sendPhoto"); !ok {
		t.Error("one platform's failure took the other down with it")
	}
}

// TestCustomPlatformPingAndSet covers the two things a custom platform needs
// that a built-in does not: a ping command of its own, and a way to set a key
// whose name crier could not know in advance.
func TestCustomPlatformPingAndSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script is sh")
	}
	dir := newProject(t, strings.Join([]string{
		"  custom:",
		"    webhook:",
		"      enabled: true",
		"      command: \"true\"",
		"      ping-command: \"echo id=acct-9 >> \\\"$CRIER_OUTPUT\\\"\"",
	}, "\n"))

	res := crier(t, dir, nil, "ping", "--json")
	if res.Code != exitOK {
		t.Fatalf("ping: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "acct-9") {
		t.Errorf("the ping-command's answer is missing:\n%s", res.Stdout)
	}

	// --set reaching a key whose middle segment is a name crier invented
	// nothing about.
	res = crier(t, dir, nil, "ping", "--json",
		"--set", `publish.custom.webhook.ping-command=echo id=from-a-flag >> "$CRIER_OUTPUT"`)
	if res.Code != exitOK {
		t.Fatalf("--set: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "from-a-flag") {
		t.Errorf("--set did not reach the custom key:\n%s", res.Stdout)
	}

	// And the environment introduces one all by itself.
	res = crier(t, t.TempDir(), []string{
		"CRIER_PUBLISH_CUSTOM_FROM_ENV_ENABLED=true",
		"CRIER_PUBLISH_CUSTOM_FROM_ENV_COMMAND=true",
		`CRIER_PUBLISH_CUSTOM_FROM_ENV_PING_COMMAND=echo id=env-acct >> "$CRIER_OUTPUT"`,
	}, "ping", "--json")
	if res.Code != exitOK {
		t.Fatalf("from the environment: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "env-acct") || !strings.Contains(res.Stdout, "from-env") {
		t.Errorf("a platform named only in the environment did not appear:\n%s", res.Stdout)
	}
}

// --- init ------------------------------------------------------------------

// TestInitThenPublish is the first five minutes with crier, end to end: run
// init, write the two files it names, and the bare command works. If that path
// breaks, nobody gets as far as the rest of the program.
func TestInitThenPublish(t *testing.T) {
	f := newFakes(t)
	dir := t.TempDir()

	res := crier(t, dir, nil, "init")
	if res.Code != exitOK {
		t.Fatalf("init: code=%d stderr=%s", res.Code, res.Stderr)
	}
	path := filepath.Join(dir, "crier.yaml")
	// Compared by suffix: on macOS the temporary directory is reached through
	// a symlink, and the absolute path init prints is the resolved one.
	if !strings.HasSuffix(strings.TrimSpace(res.Stdout), filepath.Join("001", "crier.yaml")) &&
		strings.TrimSpace(res.Stdout) != path {
		t.Fatalf("init stdout = %q, want an absolute path ending in crier.yaml", res.Stdout)
	}

	// The two files the starter names, at the names it chose. Nothing here
	// edits the generated config beyond enabling a platform, which is the
	// point: the starter's own defaults have to be workable.
	writeFile(t, dir, "template.html", baseTemplate)
	writeFile(t, dir, "data.yaml", "title: from the starter\n")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.ReplaceAll(string(body), "enabled: false", "enabled: true")
	cfg = strings.ReplaceAll(cfg, "@your_channel", "-100123")
	writeFile(t, dir, "crier.yaml", cfg+
		"\n    api-base-url: "+f.URL+"/telegram\n    token: test-token\n")

	// Bare crier, in the directory init was run in: no arguments, no flags.
	res = crier(t, dir, []string{"CRIER_LOG_LEVEL=debug"}, "--dry-run")
	if res.Code != exitOK {
		t.Fatalf("dry run: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if len(f.all()) != 0 {
		t.Errorf("a dry run made %d requests", len(f.all()))
	}

	res = crier(t, dir, nil)
	if res.Code != exitOK {
		t.Fatalf("publish: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if _, ok := f.find("/sendPhoto"); !ok {
		t.Error("nothing reached telegram")
	}
}

// TestInitFullLoadsBack checks the embedded generator against the loader from
// the outside: --full writes every key, and crier reads its own output.
func TestInitFullLoadsBack(t *testing.T) {
	for _, format := range []string{"yaml", "json", "toml"} {
		dir := t.TempDir()
		if res := crier(t, dir, nil, "init", "--full", "--format", format); res.Code != exitOK {
			t.Fatalf("%s: code=%d stderr=%s", format, res.Code, res.Stderr)
		}
		// --all, because config prints what differs from the defaults and
		// --full writes exactly the defaults: the empty diff is itself the
		// check that the generator round-trips.
		res := crier(t, dir, nil, "config", "--json", "--all")
		if res.Code != exitOK {
			t.Fatalf("%s: config code=%d stderr=%s", format, res.Code, res.Stderr)
		}
		var got struct {
			File   string         `json:"file"`
			Values map[string]any `json:"values"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if !strings.HasSuffix(got.File, "crier."+format) {
			t.Errorf("%s: the config it found is %q", format, got.File)
		}
		if got.Values["render.width"] == nil {
			t.Errorf("%s: nothing came back: %v", format, got.Values)
		}
		if got.Values["publish.telegram.token"] != "********" {
			t.Errorf("%s: the placeholder secret was not redacted", format)
		}
	}

	// The file init refuses to clobber.
	dir := t.TempDir()
	if res := crier(t, dir, nil, "init"); res.Code != exitOK {
		t.Fatal(res.Stderr)
	}
	res := crier(t, dir, nil, "init")
	if res.Code != exitConfig || !strings.Contains(res.Stderr, "--force") {
		t.Errorf("second init: code=%d stderr=%s", res.Code, res.Stderr)
	}
}

// --- misc ------------------------------------------------------------------

func TestPlatformsCommand(t *testing.T) {
	dir := newProject(t, "")
	res := crier(t, dir, nil, "platforms")
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	for _, name := range []string{
		"instagram", "facebook", "tiktok", "telegram",
		"mastodon", "discord", "linkedin", "reddit",
	} {
		if !strings.Contains(res.Stdout, name) {
			t.Errorf("%s is missing from the list:\n%s", name, res.Stdout)
		}
	}
}

// TestSmokeVersionFlag is in the release smoke subset, where it is the check
// that the ldflags stamping actually took: a release binary reporting the
// zero version built fine and is still wrong.
func TestSmokeVersionFlag(t *testing.T) {
	res := crier(t, t.TempDir(), nil, "--version")
	if res.Code != exitOK || !strings.Contains(res.Stdout, "crier") {
		t.Fatalf("code=%d stdout=%q", res.Code, res.Stdout)
	}
	for _, want := range []string{"commit ", "built ", "go1."} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("the version line has no %q: %q", want, res.Stdout)
		}
	}

	res = crier(t, t.TempDir(), nil, "--version", "--json")
	if res.Code != exitOK {
		t.Fatalf("--version --json: code=%d stderr=%s", res.Code, res.Stderr)
	}
	var info map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &info); err != nil {
		t.Fatalf("not json: %v\n%s", err, res.Stdout)
	}
	if info["version"] == nil || info["goVersion"] == nil {
		t.Errorf("version json = %v", info)
	}

	// The subcommand spelling is gone.
	if res := crier(t, t.TempDir(), nil, "version"); res.Code != exitUsage {
		t.Errorf("`crier version` = %d, want a usage error now that it is a flag", res.Code)
	}
}

func TestLogsGoToStderrAndResultsToStdout(t *testing.T) {
	dir := newProject(t, "")
	out := filepath.Join(dir, "o.png")
	res := crier(t, dir, []string{"CRIER_LOG_FORMAT=json"}, "render", "--render-output", out)
	if res.Code != exitOK {
		t.Fatalf("code=%d", res.Code)
	}
	if strings.Contains(res.Stdout, `"level"`) {
		t.Errorf("a log record reached stdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, `"level"`) {
		t.Errorf("no log records on stderr:\n%s", res.Stderr)
	}
	// webrender's own progress logging must not reach stdout either.
	if strings.Contains(res.Stdout, "webrender") {
		t.Errorf("webrender's logging reached stdout:\n%s", res.Stdout)
	}
}
