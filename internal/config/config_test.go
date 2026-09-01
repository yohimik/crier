package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	dispat "github.com/yohimik/dispat/pkg/config"
)

// --- anti-drift ------------------------------------------------------------

func TestRegistryAndBindingsCoverTheSameKeys(t *testing.T) {
	var cfg Config
	bound := Bindings(&cfg)

	declared := map[string]bool{}
	for _, d := range registry {
		if declared[d.Key] {
			t.Errorf("duplicate descriptor for %q", d.Key)
		}
		declared[d.Key] = true
	}

	for key := range declared {
		if _, ok := bound[key]; !ok {
			t.Errorf("key %q is declared in the registry but bound to no field", key)
		}
	}
	for key := range bound {
		if !declared[key] {
			t.Errorf("key %q is bound to a field but declared in no descriptor", key)
		}
	}
}

func TestValuesCoverTheSameKeys(t *testing.T) {
	cfg := Defaults()
	values := Values(&cfg)
	for _, d := range registry {
		if _, ok := values[d.Key]; !ok {
			t.Errorf("key %q cannot be read back", d.Key)
		}
	}
	if len(values) != len(registry) {
		t.Errorf("Values has %d keys, registry has %d", len(values), len(registry))
	}
}

func TestFlagAndEnvNamesAreUnique(t *testing.T) {
	flags := map[string]string{}
	envs := map[string]string{}
	for _, d := range registry {
		if prev, dup := flags[d.FlagName()]; dup {
			t.Errorf("keys %q and %q share flag --%s", prev, d.Key, d.FlagName())
		}
		flags[d.FlagName()] = d.Key
		if prev, dup := envs[d.EnvName()]; dup {
			t.Errorf("keys %q and %q share env %s", prev, d.Key, d.EnvName())
		}
		envs[d.EnvName()] = d.Key
		if strings.ContainsAny(d.Key, "_ ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Errorf("key %q should be lower kebab-case with dots", d.Key)
		}
		if d.Usage == "" {
			t.Errorf("key %q has no usage text", d.Key)
		}
	}
	for alias, key := range Aliases {
		if _, ok := flags[alias]; ok {
			t.Errorf("alias --%s collides with a real key flag", alias)
		}
		if _, ok := Descriptors()[key]; !ok {
			t.Errorf("alias --%s points at unknown key %q", alias, key)
		}
	}
	if _, ok := flags[ConfigFlag]; ok {
		t.Errorf("a key claims the reserved --%s flag", ConfigFlag)
	}
}

func TestEnvBindingAcceptsEveryKey(t *testing.T) {
	// dispat refuses a binding where two keys derive one variable name.
	if _, err := EnvBinding([]string{}).Overrides(context.Background()); err != nil {
		t.Fatalf("env binding rejected: %v", err)
	}
}

func TestDefaultsRoundTrip(t *testing.T) {
	cfg := Defaults()
	values := Values(&cfg)
	for _, d := range registry {
		got := values[d.Key]
		want := zeroFor(d.Kind)
		if d.Default != "" {
			want = parseAs(t, d.Kind, d.Default)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("default for %s = %#v, want %#v", d.Key, got, want)
		}
	}
}

func TestEveryKeyIsDecodableFromTheEnvironment(t *testing.T) {
	var environ []string
	want := map[string]any{}
	for _, d := range registry {
		v := sampleFor(d.Kind)
		environ = append(environ, d.EnvName()+"="+v)
		want[d.Key] = parseAs(t, d.Kind, v)
	}

	res, err := Load(context.Background(), Options{Environ: environ, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Values(&res.Config)
	for key, exp := range want {
		if !reflect.DeepEqual(got[key], exp) {
			t.Errorf("%s = %#v, want %#v", key, got[key], exp)
		}
	}
}

func zeroFor(k Kind) any {
	switch k {
	case KindInt:
		return 0
	case KindBool:
		return false
	case KindStrings:
		return []string(nil)
	default:
		return ""
	}
}

func parseAs(t *testing.T, k Kind, s string) any {
	t.Helper()
	switch k {
	case KindInt:
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("default %q is not an int: %v", s, err)
		}
		return n
	case KindBool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			t.Fatalf("default %q is not a bool: %v", s, err)
		}
		return b
	case KindStrings:
		return dispat.SplitList(s)
	default:
		return s
	}
}

func sampleFor(k Kind) string {
	switch k {
	case KindInt:
		return "7"
	case KindBool:
		return "true"
	case KindStrings:
		return "a,b"
	case KindDuration:
		return "3s"
	case KindFloat:
		return "1.5"
	default:
		return "sample"
	}
}

// --- loading ---------------------------------------------------------------

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPrecedenceFileEnvFlags(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "crier.yaml", strings.Join([]string{
		"log:",
		"  level: debug",
		"  format: json",
		"render:",
		"  width: 100",
		"  height: 200",
	}, "\n"))

	res, err := Load(context.Background(), Options{
		Path:          path,
		Environ:       []string{"CRIER_LOG_LEVEL=warn", "CRIER_RENDER_WIDTH=300"},
		FlagOverrides: dispat.Overrides{"log.level": "error"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Config.Log.Level; got != "error" {
		t.Errorf("log.level = %q, want the flag value", got)
	}
	if got := res.Config.Log.Format; got != "json" {
		t.Errorf("log.format = %q, want the file value", got)
	}
	if got := res.Config.Render.Width; got != 300 {
		t.Errorf("render.width = %d, want the env value", got)
	}
	if got := res.Config.Render.Height; got != 200 {
		t.Errorf("render.height = %d, want the file value", got)
	}
	if got := res.Config.Render.Format; got != "png" {
		t.Errorf("render.format = %q, want the default", got)
	}
	if res.File != path {
		t.Errorf("File = %q, want %q", res.File, path)
	}
}

func TestLoadDiscoversDefaultFileName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "crier.yaml", "log:\n  level: trace\n")
	res, err := Load(context.Background(), Options{Environ: []string{}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "trace" {
		t.Errorf("level = %q", res.Config.Log.Level)
	}
}

func TestLoadWithoutAnyFile(t *testing.T) {
	res, err := Load(context.Background(), Options{Environ: []string{}, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.File != "" {
		t.Errorf("File = %q, want empty", res.File)
	}
	if res.Config.Log.Level != "info" {
		t.Errorf("level = %q, want the default", res.Config.Log.Level)
	}
}

func TestLoadMissingExplicitFileIsAnError(t *testing.T) {
	_, err := Load(context.Background(), Options{Path: filepath.Join(t.TempDir(), "nope.yaml"), Environ: []string{}})
	if err == nil {
		t.Fatal("expected an error for a missing explicit config file")
	}
}

func TestLoadUnknownKeyIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "crier.yaml", "render:\n  widht: 10\n")
	_, err := Load(context.Background(), Options{Path: path, Environ: []string{}})
	if err == nil {
		t.Fatal("expected an unknown key error")
	}
	if !strings.Contains(err.Error(), "widht") {
		t.Errorf("error should name the key, got %v", err)
	}
}

func TestLoadFollowsRefs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yaml", "level: debug\nformat: json\n")
	path := writeFile(t, dir, "crier.yaml", "log:\n  $ref: base.yaml\n")
	res, err := Load(context.Background(), Options{Path: path, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "debug" || res.Config.Log.Format != "json" {
		t.Errorf("ref not applied: %+v", res.Config.Log)
	}
	if len(res.Files) < 2 {
		t.Errorf("Files = %v, want the referenced file too", res.Files)
	}
}

func TestLoadUsesConfigEnvVar(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "elsewhere.yaml", "log:\n  level: warn\n")
	res, err := Load(context.Background(), Options{Environ: []string{"CRIER_CONFIG=" + path}, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "warn" {
		t.Errorf("level = %q", res.Config.Log.Level)
	}
}

func TestLoadTOMLAndJSON(t *testing.T) {
	dir := t.TempDir()
	tomlPath := writeFile(t, dir, "crier.toml", "[log]\nlevel = \"warn\"\n")
	res, err := Load(context.Background(), Options{Path: tomlPath, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "warn" {
		t.Errorf("toml level = %q", res.Config.Log.Level)
	}

	jsonPath := writeFile(t, dir, "crier.json", `{"log":{"level":"error"}}`)
	res, err = Load(context.Background(), Options{Path: jsonPath, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "error" {
		t.Errorf("json level = %q", res.Config.Log.Level)
	}
}

func TestLoadCaseInsensitiveKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "crier.yaml", "Log:\n  Level: warn\n")
	res, err := Load(context.Background(), Options{Path: path, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "warn" {
		t.Errorf("level = %q", res.Config.Log.Level)
	}
}

func TestLoadListsFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "crier.yaml", "render:\n  fonts-dir:\n    - /a\n    - /b\n")
	res, err := Load(context.Background(), Options{Path: path, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Config.Render.FontsDir, []string{"/a", "/b"}) {
		t.Errorf("fonts-dir = %v", res.Config.Render.FontsDir)
	}

	res, err = Load(context.Background(), Options{
		Path:    path,
		Environ: []string{"CRIER_RENDER_FONTS_DIR=/c,/d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Config.Render.FontsDir, []string{"/c", "/d"}) {
		t.Errorf("fonts-dir from env = %v", res.Config.Render.FontsDir)
	}
}

// --- flags -----------------------------------------------------------------

func newFlagSet(t *testing.T) (*flagSetHarness, *Flags) {
	t.Helper()
	h := &flagSetHarness{}
	h.fs = newSilentFlagSet()
	f := RegisterFlags(h.fs)
	return h, f
}

func TestFlagsOnlyReportWhatWasTyped(t *testing.T) {
	h, f := newFlagSet(t)
	if err := h.fs.Parse([]string{"--log-level", "warn", "--publish-dry-run"}); err != nil {
		t.Fatal(err)
	}
	got := f.Overrides()
	want := dispat.Overrides{"log.level": "warn", "publish.dry-run": "true"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Overrides() = %#v, want %#v", got, want)
	}
}

func TestFlagsEmptyWhenNothingTyped(t *testing.T) {
	h, f := newFlagSet(t)
	if err := h.fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if got := f.Overrides(); got != nil {
		t.Errorf("Overrides() = %#v, want nil", got)
	}
}

func TestFlagAliases(t *testing.T) {
	h, f := newFlagSet(t)
	if err := h.fs.Parse([]string{"--dry-run", "--width", "500"}); err != nil {
		t.Fatal(err)
	}
	got := f.Overrides()
	if got["publish.dry-run"] != "true" || got["render.width"] != "500" {
		t.Errorf("aliases did not resolve: %#v", got)
	}
}

func TestFlagExactBeatsAlias(t *testing.T) {
	h, f := newFlagSet(t)
	if err := h.fs.Parse([]string{"--width", "500", "--render-width", "900"}); err != nil {
		t.Fatal(err)
	}
	if got := f.Overrides()["render.width"]; got != "900" {
		t.Errorf("render.width = %v, want the exact flag to win", got)
	}
}

func TestFlagConfigPath(t *testing.T) {
	h, f := newFlagSet(t)
	if err := h.fs.Parse([]string{"--config", "x.yaml"}); err != nil {
		t.Fatal(err)
	}
	if f.ConfigPath() != "x.yaml" {
		t.Errorf("ConfigPath = %q", f.ConfigPath())
	}
	if _, ok := f.Overrides()["config"]; ok {
		t.Error("--config must not become a config key override")
	}
}

func TestFlagsCoverEveryKey(t *testing.T) {
	h, _ := newFlagSet(t)
	for _, d := range registry {
		if h.fs.Lookup(d.FlagName()) == nil {
			t.Errorf("no flag for key %q", d.Key)
		}
	}
}

func TestFlagRoundTripThroughLoad(t *testing.T) {
	h, f := newFlagSet(t)
	args := make([]string, 0, len(registry)*2)
	for _, d := range registry {
		if d.Kind == KindBool {
			args = append(args, "--"+d.FlagName()+"=true")
			continue
		}
		args = append(args, "--"+d.FlagName(), sampleFor(d.Kind))
	}
	if err := h.fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	res, err := Load(context.Background(), Options{
		Environ:       []string{},
		Dir:           t.TempDir(),
		FlagOverrides: f.Overrides(),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := Values(&res.Config)
	for _, d := range registry {
		want := parseAs(t, d.Kind, sampleFor(d.Kind))
		if d.Kind == KindBool {
			want = true
		}
		if !reflect.DeepEqual(got[d.Key], want) {
			t.Errorf("%s = %#v, want %#v", d.Key, got[d.Key], want)
		}
	}
}

// --- validation ------------------------------------------------------------

func TestValidateDefaultsAreValid(t *testing.T) {
	cfg := Defaults()
	if err := Validate(&cfg); err != nil {
		t.Fatalf("the shipped defaults do not validate: %v", err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	cfg := Defaults()
	cfg.Log.Level = "loud"
	cfg.Log.Format = "xml"
	cfg.Render.Width = MaxDimension + 1
	cfg.Render.Scale = "9"
	cfg.Render.Format = "bmp"
	cfg.Render.JPEGQuality = 0
	cfg.Render.MediaType = "braille"
	cfg.Render.Background = "blue"
	cfg.Render.SuperSample = 0
	cfg.HTTP.Timeout = "soon"
	cfg.HTTP.RetryMax = -1
	cfg.Publish.Concurrency = 0

	err := Validate(&cfg)
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	for _, want := range []string{
		"log.level", "log.format", "render.width", "render.scale", "render.format",
		"render.jpeg-quality", "render.media-type", "render.background",
		"render.supersample", "http.timeout", "http.retry-max", "publish.concurrency",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
}

func TestValidateStageModes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   []string
	}{
		{"url without url", func(c *Config) { c.Stage.Mode = "url" }, []string{"stage.url"}},
		{"url not absolute", func(c *Config) { c.Stage.Mode = "url"; c.Stage.URL = "relative" }, []string{"stage.url"}},
		{"s3 missing everything", func(c *Config) { c.Stage.Mode = "s3" },
			[]string{"stage.s3.endpoint", "stage.s3.bucket", "stage.s3.access-key", "stage.s3.secret-key"}},
		{"s3 no presign needs base url", func(c *Config) {
			c.Stage.Mode = "s3"
			c.Stage.S3.Endpoint, c.Stage.S3.Bucket = "e", "b"
			c.Stage.S3.AccessKey, c.Stage.S3.SecretKey = "a", "s"
			c.Stage.S3.Presign = false
		}, []string{"stage.s3.public-base-url"}},
		{"server without public url", func(c *Config) { c.Stage.Mode = "server" },
			[]string{"stage.server.public-url"}},
		{"server tunnel and public url", func(c *Config) {
			c.Stage.Mode = "server"
			c.Stage.Server.PublicURL = "https://x.example"
			c.Stage.Server.Tunnel.Mode = "ngrok"
		}, []string{"stage.server.public-url"}},
		{"custom tunnel needs bin and pattern", func(c *Config) {
			c.Stage.Mode = "server"
			c.Stage.Server.Tunnel.Mode = "custom"
		}, []string{"stage.server.tunnel.bin", "stage.server.tunnel.url-pattern"}},
		{"bad tunnel mode", func(c *Config) {
			c.Stage.Mode = "server"
			c.Stage.Server.Tunnel.Mode = "wormhole"
		}, []string{"stage.server.tunnel.mode"}},
		{"bad url pattern", func(c *Config) {
			c.Stage.Mode = "server"
			c.Stage.Server.PublicURL = "https://x.example"
			c.Stage.Server.Tunnel.URLPattern = "(unclosed"
		}, []string{"stage.server.tunnel.url-pattern"}},
		{"url pattern needs one group", func(c *Config) {
			c.Stage.Mode = "server"
			c.Stage.Server.PublicURL = "https://x.example"
			c.Stage.Server.Tunnel.URLPattern = "https://\\S+"
		}, []string{"one capture group"}},
		{"unknown mode", func(c *Config) { c.Stage.Mode = "carrier-pigeon" }, []string{"stage.mode"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			tt.mutate(&cfg)
			err := Validate(&cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in: %v", want, err)
				}
			}
		})
	}
}

func TestValidateStageServerHappyPaths(t *testing.T) {
	cfg := Defaults()
	cfg.Stage.Mode = "server"
	cfg.Stage.Server.PublicURL = "https://x.example"
	if err := Validate(&cfg); err != nil {
		t.Fatalf("public url mode should be valid: %v", err)
	}

	cfg = Defaults()
	cfg.Stage.Mode = "server"
	cfg.Stage.Server.Tunnel.Mode = "ngrok"
	if err := Validate(&cfg); err != nil {
		t.Fatalf("ngrok mode should be valid: %v", err)
	}
}

func TestErrorMessagesNameAllThreeLayers(t *testing.T) {
	err := invalid("render.jpeg-quality", "0", "want 1 to 100")
	msg := err.Error()
	for _, want := range []string{"render.jpeg-quality", "CRIER_RENDER_JPEG_QUALITY", "--render-jpeg-quality"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in %q", want, msg)
		}
	}
	msg = missing("stage.url", "because").Error()
	for _, want := range []string{"stage.url", "CRIER_STAGE_URL", "--stage-url"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in %q", want, msg)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want Format
		bad  bool
	}{
		{"png", PNG, false},
		{"PNG", PNG, false},
		{"jpg", JPEG, false},
		{"jpeg", JPEG, false},
		{"gif", "", true},
	} {
		got, err := ParseFormat(tt.in)
		if (err != nil) != tt.bad {
			t.Errorf("ParseFormat(%q) err = %v", tt.in, err)
			continue
		}
		if err == nil && got != tt.want {
			t.Errorf("ParseFormat(%q) = %q", tt.in, got)
		}
	}
	if PNG.ContentType() != "image/png" || JPEG.ContentType() != "image/jpeg" {
		t.Error("wrong content types")
	}
	if PNG.Ext() != ".png" || JPEG.Ext() != ".jpg" {
		t.Error("wrong extensions")
	}
}

func TestParseColor(t *testing.T) {
	for _, tt := range []struct {
		in         string
		r, g, b, a uint8
		bad        bool
	}{
		{in: "", r: 255, g: 255, b: 255, a: 255},
		{in: "#fff", r: 255, g: 255, b: 255, a: 255},
		{in: "#000000", a: 255},
		{in: "#12345678", r: 0x12, g: 0x34, b: 0x56, a: 0x78},
		{in: "blue", bad: true},
		{in: "#12345", bad: true},
		{in: "#gggggg", bad: true},
	} {
		got, err := ParseColor(tt.in)
		if (err != nil) != tt.bad {
			t.Errorf("ParseColor(%q) err = %v", tt.in, err)
			continue
		}
		if err != nil {
			continue
		}
		if got.R != tt.r || got.G != tt.g || got.B != tt.b || got.A != tt.a {
			t.Errorf("ParseColor(%q) = %v", tt.in, got)
		}
	}
}

func TestDurationAndFloatHelpers(t *testing.T) {
	if Duration("1s").String() != "1s" {
		t.Error("Duration")
	}
	if Duration("nope") != 0 {
		t.Error("Duration fallback")
	}
	if Float("2.5", 1) != 2.5 {
		t.Error("Float")
	}
	if Float("nope", 1) != 1 {
		t.Error("Float fallback")
	}
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{
		KindString: "string", KindInt: "int", KindBool: "bool",
		KindStrings: "list", KindDuration: "duration", KindFloat: "float",
		Kind(99): "unknown",
	} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestRegistryIsCopied(t *testing.T) {
	r := Registry()
	if len(r) == 0 {
		t.Fatal("empty registry")
	}
	r[0].Key = "mutated"
	if Registry()[0].Key == "mutated" {
		t.Error("Registry returned the backing array")
	}
	keys := Keys()
	if !sort.StringsAreSorted(keys) {
		t.Error("Keys must be sorted")
	}
	if len(keys) != len(registry) {
		t.Errorf("Keys has %d entries, want %d", len(keys), len(registry))
	}
}

func TestNestReportsDeepUnknownKeys(t *testing.T) {
	var cfg Config
	err := dispat.DecodeObject(map[string]any{
		"publish": map[string]any{
			"instagram": map[string]any{"nope": 1},
		},
	}, "", Fields(&cfg))
	if err == nil || !strings.Contains(err.Error(), "publish.instagram.nope") {
		t.Fatalf("want a full-path unknown key error, got %v", err)
	}
}

func TestFieldsDecodeNestedObject(t *testing.T) {
	var cfg Config
	err := dispat.DecodeObject(map[string]any{
		"stage": map[string]any{
			"server": map[string]any{
				"tunnel": map[string]any{"mode": "zrok", "args": []any{"-x", "-y"}},
			},
		},
	}, "", Fields(&cfg))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Stage.Server.Tunnel.Mode != "zrok" {
		t.Errorf("mode = %q", cfg.Stage.Server.Tunnel.Mode)
	}
	if !reflect.DeepEqual(cfg.Stage.Server.Tunnel.Args, []string{"-x", "-y"}) {
		t.Errorf("args = %v", cfg.Stage.Server.Tunnel.Args)
	}
}

func TestApplyDefaultsIsIdempotent(t *testing.T) {
	a := Defaults()
	b := Defaults()
	ApplyDefaults(&b)
	if !reflect.DeepEqual(Values(&a), Values(&b)) {
		t.Error("applying defaults twice changed the result")
	}
}

func ExampleFlagName() {
	fmt.Println(FlagName("publish.instagram.api-base-url"))
	fmt.Println(EnvName("publish.instagram.api-base-url"))
	// Output:
	// publish-instagram-api-base-url
	// CRIER_PUBLISH_INSTAGRAM_API_BASE_URL
}
