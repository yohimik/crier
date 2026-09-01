package config

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnvDataSourceIsNotAPath is the trap render.data's Path flag sets: the
// value is anchored against the config file's directory, and joining
// `env:CARD_` onto it would make a path that cannot exist and a failure naming
// a file nobody wrote.
func TestEnvDataSourceIsNotAPath(t *testing.T) {
	path := writeConfig(t, "render:\n  template: t.html\n  data: env:CARD_\n")
	res, err := Load(context.Background(), Options{Path: path, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Render.Data != "env:CARD_" {
		t.Errorf("render.data = %q, want the source left alone", res.Config.Render.Data)
	}
	// The template beside it is still anchored, so the exemption is narrow.
	if !filepath.IsAbs(res.Config.Render.Template) {
		t.Errorf("render.template = %q, want it anchored", res.Config.Render.Template)
	}
}

// TestEnvDataPrefixIsRequired: `env:` on its own would match the whole
// environment, which is never what somebody meant.
func TestEnvDataPrefixIsRequired(t *testing.T) {
	cfg := Defaults()
	cfg.Render.Template = "t.html"
	cfg.Render.Data = "env:"

	err := Validate(&cfg)
	if err == nil {
		t.Fatal("an empty prefix should be a config error")
	}
	if !strings.Contains(err.Error(), "render.data") {
		t.Errorf("the error should name the key: %v", err)
	}
	if !strings.Contains(err.Error(), "env:CARD_") {
		t.Errorf("the error should show what one looks like: %v", err)
	}

	// The three real forms all validate.
	for _, source := range []string{"env:CARD_", "data.yaml", "-", ""} {
		cfg.Render.Data = source
		if err := Validate(&cfg); err != nil {
			t.Errorf("render.data = %q: %v", source, err)
		}
	}
}

// TestEnvDataSourceSettableThreeWays: it is a value like any other.
func TestEnvDataSourceSettableThreeWays(t *testing.T) {
	dir := t.TempDir()
	res, err := Load(context.Background(), Options{
		Dir:     dir,
		Environ: []string{"CRIER_RENDER_DATA=env:FROM_ENV_"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Render.Data != "env:FROM_ENV_" {
		t.Errorf("= %q", res.Config.Render.Data)
	}
}
