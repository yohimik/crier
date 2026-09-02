package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	dispat "github.com/yohimik/dispat/pkg/config"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDiscoveryWalksUpFromTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	project := mkdir(t, root, "project")
	deep := mkdir(t, project, "assets", "cards")
	writeFile(t, project, "crier.yaml", "log:\n  level: warn\n")

	for _, dir := range []string{project, filepath.Join(project, "assets"), deep} {
		res, err := Load(context.Background(), Options{Environ: []string{}, Dir: dir})
		if err != nil {
			t.Fatalf("from %s: %v", dir, err)
		}
		if res.Config.Log.Level != "warn" {
			t.Errorf("from %s: level = %q", dir, res.Config.Log.Level)
		}
		if filepath.Base(res.Dir) != "project" {
			t.Errorf("from %s: root = %q", dir, res.Dir)
		}
	}
}

func TestDiscoveryNearestConfigWins(t *testing.T) {
	root := t.TempDir()
	outer := mkdir(t, root, "outer")
	inner := mkdir(t, outer, "inner")
	writeFile(t, outer, "crier.yaml", "log:\n  level: error\n")
	writeFile(t, inner, "crier.yaml", "log:\n  level: debug\n")

	res, err := Load(context.Background(), Options{Environ: []string{}, Dir: inner})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "debug" {
		t.Errorf("level = %q, want the nearest config to win", res.Config.Log.Level)
	}
}

func TestDiscoveryTwoSiblingProjectsStayApart(t *testing.T) {
	root := t.TempDir()
	a := mkdir(t, root, "a")
	b := mkdir(t, root, "b")
	writeFile(t, a, "crier.yaml", "render:\n  template: a.html\npublish:\n  telegram:\n    enabled: true\n")
	writeFile(t, b, "crier.yaml", "render:\n  template: b.html\npublish:\n  discord:\n    enabled: true\n")

	resA, err := Load(context.Background(), Options{Environ: []string{}, Dir: a})
	if err != nil {
		t.Fatal(err)
	}
	resB, err := Load(context.Background(), Options{Environ: []string{}, Dir: b})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(resA.Config.Render.Template, "a.html") || !resA.Config.Publish.Telegram.Enabled {
		t.Errorf("project a got %+v", resA.Config.Render.Template)
	}
	if !strings.HasSuffix(resB.Config.Render.Template, "b.html") || !resB.Config.Publish.Discord.Enabled {
		t.Errorf("project b got %+v", resB.Config.Render.Template)
	}
	if resA.Config.Publish.Discord.Enabled || resB.Config.Publish.Telegram.Enabled {
		t.Error("one project's platform list leaked into the other")
	}
}

func TestExplicitPathBeatsDiscovery(t *testing.T) {
	root := t.TempDir()
	project := mkdir(t, root, "project")
	writeFile(t, project, "crier.yaml", "log:\n  level: warn\n")
	other := writeFile(t, root, "other.yaml", "log:\n  level: trace\n")

	res, err := Load(context.Background(), Options{Environ: []string{}, Dir: project, Path: other})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "trace" {
		t.Errorf("level = %q, want --config to win", res.Config.Log.Level)
	}

	res, err = Load(context.Background(), Options{Environ: []string{"CRIER_CONFIG=" + other}, Dir: project})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "trace" {
		t.Errorf("level = %q, want CRIER_CONFIG to win", res.Config.Log.Level)
	}
}

func TestDiscoveryDotPrefixedName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".crier.yaml", "log:\n  level: warn\n")
	res, err := Load(context.Background(), Options{Environ: []string{}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Log.Level != "warn" {
		t.Errorf("level = %q", res.Config.Log.Level)
	}
}

func TestDiscoveryBrokenConfigFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "crier.yaml", "log:\n  level: [oops\n")
	if _, err := Load(context.Background(), Options{Environ: []string{}, Dir: dir}); err == nil {
		t.Fatal("a broken config on the way up must fail, not be stepped over")
	}
}

// --- path anchoring --------------------------------------------------------

func TestFilePathsAnchorToTheConfigDirectory(t *testing.T) {
	root := t.TempDir()
	project := mkdir(t, root, "project")
	deep := mkdir(t, project, "sub")
	writeFile(t, project, "crier.yaml", strings.Join([]string{
		"render:",
		"  template: layouts/base.html",
		"  data: data.yaml",
		"  overlays:",
		"    - overlays/story.html",
		"  fonts-dir: fonts",
		"  output: out/card.png",
		"publish:",
		"  discord:",
		"    overlay: overlays/discord.html",
	}, "\n"))

	res, err := Load(context.Background(), Options{Environ: []string{}, Dir: deep})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Config
	want := func(rel string) string { return filepath.Join(project, rel) }
	if c.Render.Template != want("layouts/base.html") {
		t.Errorf("template = %q", c.Render.Template)
	}
	if c.Render.Data != want("data.yaml") {
		t.Errorf("data = %q", c.Render.Data)
	}
	if !reflect.DeepEqual(c.Render.Overlays, []string{want("overlays/story.html")}) {
		t.Errorf("overlays = %v", c.Render.Overlays)
	}
	if !reflect.DeepEqual(c.Render.FontsDir, []string{want("fonts")}) {
		t.Errorf("fonts-dir = %v", c.Render.FontsDir)
	}
	if c.Render.Output != want("out/card.png") {
		t.Errorf("output = %q", c.Render.Output)
	}
	if !reflect.DeepEqual(c.Publish.Discord.Overlay, []string{want("overlays/discord.html")}) {
		t.Errorf("discord overlay = %v", c.Publish.Discord.Overlay)
	}
}

func TestFlagAndEnvPathsAreLeftRelativeToTheWorkingDirectory(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, "crier.yaml", "render:\n  template: from-file.html\n  data: from-file.yaml\n")

	res, err := Load(context.Background(), Options{
		Environ:       []string{"CRIER_RENDER_DATA=from-env.yaml"},
		Dir:           project,
		FlagOverrides: dispat.Overrides{"render.template": "from-flag.html"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Render.Template != "from-flag.html" {
		t.Errorf("template = %q, want the flag value left alone", res.Config.Render.Template)
	}
	if res.Config.Render.Data != "from-env.yaml" {
		t.Errorf("data = %q, want the env value left alone", res.Config.Render.Data)
	}
}

func TestAbsoluteAndStdinPathsAreNotAnchored(t *testing.T) {
	project := t.TempDir()
	abs := filepath.Join(project, "abs.html")
	writeFile(t, project, "crier.yaml", "render:\n  template: "+abs+"\n  data: \"-\"\n")

	res, err := Load(context.Background(), Options{Environ: []string{}, Dir: project})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Render.Template != abs {
		t.Errorf("template = %q, want it untouched", res.Config.Render.Template)
	}
	if res.Config.Render.Data != "-" {
		t.Errorf("data = %q, want stdin untouched", res.Config.Render.Data)
	}
}

func TestSettingsHave(t *testing.T) {
	settings := map[string]any{
		"Render": map[string]any{"Template": "x", "empty": nil},
		"flat":   "v",
	}
	for _, tt := range []struct {
		key  string
		want bool
	}{
		{"render.template", true},
		{"RENDER.TEMPLATE", true},
		{"render.data", false},
		{"render.empty", false},
		{"flat", true},
		{"flat.deeper", false},
		{"nope.at.all", false},
	} {
		if got := settingsHave(settings, tt.key); got != tt.want {
			t.Errorf("settingsHave(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestAnchorOne(t *testing.T) {
	dir := string(filepath.Separator) + filepath.Join("base", "dir")
	if got := anchorOne(dir, ""); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := anchorOne(dir, "-"); got != "-" {
		t.Errorf("stdin = %q", got)
	}
	if got := anchorOne(dir, "rel.html"); got != filepath.Join(dir, "rel.html") {
		t.Errorf("relative = %q", got)
	}
	abs := filepath.Join(dir, "x")
	if got := anchorOne(dir, abs); got != abs {
		t.Errorf("absolute = %q", got)
	}
}

func TestLayoutOf(t *testing.T) {
	cfg := Defaults()
	for _, name := range Platforms {
		if LayoutOf(&cfg.Publish, name) == nil {
			t.Errorf("no layout for platform %q", name)
		}
	}
	if LayoutOf(&cfg.Publish, "myspace") != nil {
		t.Error("unknown platform should have no layout")
	}
}

func TestPerPlatformLayoutKeysExist(t *testing.T) {
	byKey := Descriptors()
	for _, name := range Platforms {
		for _, suffix := range []string{"overlay", "width", "height", "enabled"} {
			key := "publish." + name + "." + suffix
			if _, ok := byKey[key]; !ok {
				t.Errorf("missing descriptor %q", key)
			}
		}
	}
}

func TestEveryPlatformHasACaptionishKey(t *testing.T) {
	// E5: every platform must have at least one key holding its post text, or
	// the per-platform caption cannot be set at all.
	byKey := Descriptors()
	captionish := map[string][]string{
		"instagram": {"caption"},
		"facebook":  {"caption"},
		"tiktok":    {"caption", "title"},
		"telegram":  {"caption"},
		"x":         {"caption"},
		"slack":     {"caption"},
		"mastodon":  {"caption", "alt-text"},
		"discord":   {"caption"},
		"linkedin":  {"caption"},
		"reddit":    {"caption", "title"},
		"vk":        {"caption"},
	}
	for _, name := range Platforms {
		keys, ok := captionish[name]
		if !ok {
			t.Errorf("platform %q has no declared caption keys", name)
			continue
		}
		for _, suffix := range keys {
			key := "publish." + name + "." + suffix
			if _, ok := byKey[key]; !ok {
				t.Errorf("missing descriptor %q", key)
			}
		}
	}
	if _, ok := byKey["publish.caption"]; !ok {
		t.Error("missing the global publish.caption fallback")
	}
}

func TestVideoValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"fps", func(c *Config) { c.Render.Video.FPS = 0 }, "render.video.fps"},
		{"frames", func(c *Config) { c.Render.Video.Frames = -1 }, "render.video.frames"},
		{"preset", func(c *Config) { c.Render.Video.CodecPreset = "av1" }, "render.video.codec-preset"},
		{"duration", func(c *Config) { c.Render.Video.Duration = "soon" }, "render.video.duration"},
		{"bin", func(c *Config) { c.Render.Video.Enabled = true; c.Render.Video.FFmpegBin = "" }, "render.video.ffmpeg-bin"},
		{"kind", func(c *Config) { c.Publish.Reddit.Kind = "poll" }, "publish.reddit.kind"},
		{"width", func(c *Config) { c.Publish.Discord.Width = -1 }, "publish.discord.width"},
		{"height", func(c *Config) { c.Publish.Discord.Height = MaxDimension + 1 }, "publish.discord.height"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			tt.mutate(&cfg)
			err := Validate(&cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("want an error naming %q, got %v", tt.want, err)
			}
		})
	}
}

func TestVideoDefaultsAreUsable(t *testing.T) {
	cfg := Defaults()
	cfg.Render.Video.Enabled = true
	if err := Validate(&cfg); err != nil {
		t.Fatalf("video defaults do not validate: %v", err)
	}
	if cfg.Render.Video.FPS != 30 || cfg.Render.Video.CodecPreset != "h264" {
		t.Errorf("unexpected video defaults: %+v", cfg.Render.Video)
	}
}
