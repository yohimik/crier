package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dispat "github.com/yohimik/dispat/pkg/config"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "crier.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const customFile = `publish:
  custom:
    webhook:
      enabled: true
      command: "curl -F file=@$CRIER_ARTIFACT https://example.test/hook"
      ping-command: "curl -sf https://example.test/me"
      caption: "from {{ .Platform }}"
      kinds: [image, video]
      format: jpeg
      needs-url: true
      timeout: 45s
      width: 800
      height: 600
      overlay: [wide.html]
      env:
        MY_TOKEN: abc
        lower_case_kept: yes
`

func TestCustomPlatformFromAFile(t *testing.T) {
	res, err := Load(context.Background(), Options{Path: writeConfig(t, customFile), Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	c := CustomOf(&res.Config.Publish, "webhook")
	if c == nil {
		t.Fatal("the platform was not created")
	}
	if !c.Enabled || !strings.Contains(c.Command, "curl") {
		t.Errorf("entry = %+v", c)
	}
	if c.Format != "jpeg" || c.Timeout != "45s" || !c.NeedsURL {
		t.Errorf("entry = %+v", c)
	}
	if len(c.Kinds) != 2 {
		t.Errorf("kinds = %v", c.Kinds)
	}
	if c.Layout.Width != 800 || c.Layout.Height != 600 {
		t.Errorf("layout = %+v", c.Layout)
	}
	// A path inside a custom entry is anchored to the config file, like every
	// other path a config file writes.
	if len(c.Layout.Overlay) != 1 || !filepath.IsAbs(c.Layout.Overlay[0]) {
		t.Errorf("overlay = %v, want it anchored to the config file", c.Layout.Overlay)
	}
	// The env keys keep their spelling: they become variables in a shell.
	if c.Env["MY_TOKEN"] != "abc" {
		t.Errorf("env = %v", c.Env)
	}
	if _, ok := c.Env["lower_case_kept"]; !ok {
		t.Errorf("env = %v, want the key as written", c.Env)
	}

	if err := Validate(&res.Config); err != nil {
		t.Fatalf("a complete entry should validate: %v", err)
	}
}

// TestCustomPlatformDefaults: an entry that says almost nothing still has the
// defaults the reference documents.
func TestCustomPlatformDefaults(t *testing.T) {
	res, err := Load(context.Background(), Options{
		Path:    writeConfig(t, "publish:\n  custom:\n    hook:\n      command: \"true\"\n"),
		Environ: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := CustomOf(&res.Config.Publish, "hook")
	if c.Format != "png" || c.Timeout != "2m" {
		t.Errorf("defaults did not apply: %+v", c)
	}
	if len(c.Kinds) != 1 || c.Kinds[0] != "image" {
		t.Errorf("kinds = %v", c.Kinds)
	}
	if c.Enabled {
		t.Error("nothing should be enabled by accident")
	}
}

// TestCustomPlatformFromTheEnvironment is the layer that took work: the
// environment binding is a closed list of keys, so the names have to be
// discovered before it is built.
func TestCustomPlatformFromTheEnvironment(t *testing.T) {
	env := []string{
		"CRIER_PUBLISH_CUSTOM_WEBHOOK_ENABLED=true",
		"CRIER_PUBLISH_CUSTOM_WEBHOOK_COMMAND=echo hi",
		"CRIER_PUBLISH_CUSTOM_WEBHOOK_PING_COMMAND=echo ok",
		"CRIER_PUBLISH_CUSTOM_WEBHOOK_TIMEOUT=10s",
	}
	// Named nowhere but the environment.
	res, err := Load(context.Background(), Options{Environ: env, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c := CustomOf(&res.Config.Publish, "webhook")
	if c == nil {
		t.Fatal("the environment did not introduce the platform")
	}
	if !c.Enabled || c.Command != "echo hi" || c.PingCommand != "echo ok" || c.Timeout != "10s" {
		t.Errorf("entry = %+v", c)
	}

	// And it overrides the file, like every other key.
	res, err = Load(context.Background(), Options{
		Path:    writeConfig(t, customFile),
		Environ: []string{"CRIER_PUBLISH_CUSTOM_WEBHOOK_TIMEOUT=5s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := CustomOf(&res.Config.Publish, "webhook").Timeout; got != "5s" {
		t.Errorf("timeout = %q, want the environment to win", got)
	}
}

// TestCustomPlatformFromSet is the flag layer, which cannot pre-register a
// flag for a name nobody has written down yet.
func TestCustomPlatformFromSet(t *testing.T) {
	res, err := Load(context.Background(), Options{
		Dir:     t.TempDir(),
		Environ: []string{},
		FlagOverrides: dispat.Overrides{
			"publish.custom.hook.enabled": "true",
			"publish.custom.hook.command": "echo from a flag",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := CustomOf(&res.Config.Publish, "hook")
	if c == nil || !c.Enabled || c.Command != "echo from a flag" {
		t.Fatalf("entry = %+v", c)
	}

	// And it outranks the file.
	res, err = Load(context.Background(), Options{
		Path:          writeConfig(t, customFile),
		Environ:       []string{"CRIER_PUBLISH_CUSTOM_WEBHOOK_TIMEOUT=5s"},
		FlagOverrides: dispat.Overrides{"publish.custom.webhook.timeout": "1s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := CustomOf(&res.Config.Publish, "webhook").Timeout; got != "1s" {
		t.Errorf("timeout = %q, want the flag to win", got)
	}
}

func TestCustomKeysAreCheckedNotGuessed(t *testing.T) {
	for _, key := range []string{
		"publish.custom.hook.command",
		"publish.custom.hook.env.MY_TOKEN",
		"publish.custom.my-hook.enabled",
	} {
		if err := CheckKey(key); err != nil {
			t.Errorf("CheckKey(%q) = %v, want it accepted", key, err)
		}
	}
	for _, key := range []string{
		"publish.custom.hook.commnad",
		"publish.custom.hook",
		"publish.custom.My_Hook.command",
		"publish.custom.telegram.command",
		"publish.custom.hook.env.",
	} {
		if err := CheckKey(key); err == nil {
			t.Errorf("CheckKey(%q) was accepted", key)
		}
	}
}

// TestCustomNameRules: a name has to survive the round trip through an
// environment variable, and must not shadow a built-in.
func TestCustomNameRules(t *testing.T) {
	for _, name := range []string{"hook", "my-hook", "hook2"} {
		if err := CheckCustomName(name); err != nil {
			t.Errorf("%q = %v", name, err)
		}
	}
	for _, name := range []string{"", "My-Hook", "my_hook", "my hook", "telegram", "hook.a"} {
		if err := CheckCustomName(name); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
}

func TestCustomNamesInEnv(t *testing.T) {
	got := CustomNamesInEnv([]string{
		"CRIER_PUBLISH_CUSTOM_WEBHOOK_COMMAND=x",
		"CRIER_PUBLISH_CUSTOM_MY_HOOK_PING_COMMAND=x",
		"CRIER_PUBLISH_CUSTOM_OTHER_ENV_TOKEN=x",
		"CRIER_PUBLISH_CUSTOM_NOLEAF=x",
		"CRIER_RENDER_WIDTH=100",
		"PATH=/bin",
	})
	want := []string{"my-hook", "other", "webhook"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("= %v, want %v", got, want)
	}

	// PING_COMMAND has to win over COMMAND, or the name would keep the "ping".
	if got := customNameOf("MY_HOOK_PING_COMMAND"); got != "my-hook" {
		t.Errorf("customNameOf = %q", got)
	}
}

func TestCustomEntryRefusesAnUnknownKey(t *testing.T) {
	_, err := Load(context.Background(), Options{
		Path:    writeConfig(t, "publish:\n  custom:\n    hook:\n      commnad: x\n"),
		Environ: []string{},
	})
	if err == nil || !strings.Contains(err.Error(), "commnad") {
		t.Fatalf("err = %v, want an unknown-key error naming it", err)
	}
	// The names are open; the keys under them are not. That is the whole rule.
	_, err = Load(context.Background(), Options{
		Path:    writeConfig(t, "publish:\n  custom:\n    Bad Name:\n      command: x\n"),
		Environ: []string{},
	})
	if err == nil {
		t.Fatal("a name that cannot be an environment variable should be refused")
	}
}

func TestCustomValidation(t *testing.T) {
	cfg := Defaults()
	cfg.Publish.Custom = map[string]*Custom{"hook": {
		Enabled: true, Kinds: []string{"audio"}, Format: "webp", Timeout: "nonsense",
	}}
	err := Validate(&cfg)
	if err == nil {
		t.Fatal("want errors")
	}
	for _, want := range []string{"command is required", "kinds", "format", "timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q: %v", want, err)
		}
	}

	// A disabled half-written entry is somebody's work in progress.
	cfg.Publish.Custom = map[string]*Custom{"hook": {Kinds: []string{"image"}, Format: "png", Timeout: "1s"}}
	if err := Validate(&cfg); err != nil {
		t.Errorf("a disabled entry with no command should be fine: %v", err)
	}
}

// TestCustomIsReadBack: `crier config` and the path anchoring both go through
// Values, so a key that cannot be read back is a key that cannot be shown.
func TestCustomIsReadBack(t *testing.T) {
	res, err := Load(context.Background(), Options{Path: writeConfig(t, customFile), Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	values := Values(&res.Config)
	if values["publish.custom.webhook.command"] == nil {
		t.Error("the command did not come back out")
	}
	if got := values["publish.custom.webhook.width"]; got != 800 {
		t.Errorf("width = %v", got)
	}
	for _, key := range CustomKeys("webhook") {
		if _, ok := values[key]; !ok {
			t.Errorf("%s is missing from the read-back", key)
		}
	}
}
