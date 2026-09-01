package config

import (
	"strings"
	"testing"
)

func TestParseFit(t *testing.T) {
	for in, want := range map[string]Fit{
		"":         FitNone,
		"none":     FitNone,
		"cover":    FitCover,
		"CONTAIN":  FitContain,
		"  cover ": FitCover,
		"stretch":  FitStretch,
	} {
		got, ok := ParseFit(in)
		if !ok || got != want {
			t.Errorf("ParseFit(%q) = %q, %t; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"crop", "fill", "scale", "cover ratio"} {
		if _, ok := ParseFit(in); ok {
			t.Errorf("ParseFit(%q) was accepted", in)
		}
	}
	if names := FitNames(); strings.Join(names, ",") != "none,cover,contain,stretch" {
		t.Errorf("FitNames() = %v", names)
	}
}

// TestFitNeedsAFrame is the validation the plan asks for: a fit with no
// dimensions has nothing to scale towards, and the run would quietly send the
// master render — which is the surprise the setting exists to remove.
func TestFitNeedsAFrame(t *testing.T) {
	for _, fit := range []string{"cover", "contain", "stretch"} {
		cfg := Defaults()
		cfg.Render.Template = "t.html"
		cfg.Publish.Instagram.Layout.Fit = fit

		err := Validate(&cfg)
		if err == nil {
			t.Errorf("%s with no dimensions should be refused", fit)
			continue
		}
		if !strings.Contains(err.Error(), "publish.instagram.fit") {
			t.Errorf("%s: the error should name the platform's key: %v", fit, err)
		}
		if !strings.Contains(err.Error(), "publish.instagram.width") {
			t.Errorf("%s: the error should say what to set: %v", fit, err)
		}
		if !strings.Contains(err.Error(), "target size, not the render size") {
			t.Errorf("%s: the error should explain what the dimensions mean: %v", fit, err)
		}

		// With a frame it is fine.
		cfg.Publish.Instagram.Layout.Width = 1080
		cfg.Publish.Instagram.Layout.Height = 1920
		if err := Validate(&cfg); err != nil {
			t.Errorf("%s with a frame: %v", fit, err)
		}

		// Half a frame is still not a frame.
		cfg.Publish.Instagram.Layout.Height = 0
		if err := Validate(&cfg); err == nil {
			t.Errorf("%s with only a width should be refused", fit)
		}
	}

	// none needs nothing, which is what keeps every existing config working.
	cfg := Defaults()
	cfg.Render.Template = "t.html"
	cfg.Publish.Telegram.Layout.Fit = "none"
	if err := Validate(&cfg); err != nil {
		t.Errorf("none with no dimensions: %v", err)
	}
}

func TestFitModeIsChecked(t *testing.T) {
	cfg := Defaults()
	cfg.Render.Template = "t.html"
	cfg.Publish.Discord.Layout.Fit = "crop"

	err := Validate(&cfg)
	if err == nil {
		t.Fatal("an unknown mode should be refused")
	}
	if !strings.Contains(err.Error(), "publish.discord.fit") {
		t.Errorf("the error should name the key: %v", err)
	}
	for _, name := range FitNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error should list %q: %v", name, err)
		}
	}
}

func TestFitBackgroundIsAColour(t *testing.T) {
	cfg := Defaults()
	cfg.Render.Template = "t.html"
	cfg.Publish.X.Layout.FitBackground = "octarine"

	err := Validate(&cfg)
	if err == nil {
		t.Fatal("a colour that is not one should be refused")
	}
	if !strings.Contains(err.Error(), "publish.x.fit-background") {
		t.Errorf("the error should name the key: %v", err)
	}

	for _, ok := range []string{"#000", "#fff", "#112233", "#11223344", ""} {
		cfg.Publish.X.Layout.FitBackground = ok
		if err := Validate(&cfg); err != nil {
			t.Errorf("%q should be a colour: %v", ok, err)
		}
	}
}

// TestEveryPlatformHasFitKeys: a platform silently missing one would be a hole
// nobody notices until somebody tries to use it.
func TestEveryPlatformHasFitKeys(t *testing.T) {
	byKey := Descriptors()
	for _, name := range Platforms {
		for _, suffix := range []string{"fit", "fit-background"} {
			key := "publish." + name + "." + suffix
			d, ok := byKey[key]
			if !ok {
				t.Errorf("missing descriptor %q", key)
				continue
			}
			if d.Default == "" {
				t.Errorf("%s has no default", key)
			}
		}
	}
	// A custom platform gets them too, from its own leaf list.
	var haveFit, haveBackground bool
	for _, d := range CustomLeaves {
		switch d.Key {
		case "fit":
			haveFit = true
		case "fit-background":
			haveBackground = true
		}
	}
	if !haveFit || !haveBackground {
		t.Error("a custom platform is missing a fit key")
	}
}

// TestCustomPlatformFitIsValidated: the custom entries go through the same
// check, since LayoutOf reaches them too.
func TestCustomPlatformFitIsValidated(t *testing.T) {
	cfg := Defaults()
	cfg.Render.Template = "t.html"
	cfg.Publish.Custom = map[string]*Custom{"hook": {
		Enabled: true, Command: "true", Kinds: []string{"image"}, Format: "png", Timeout: "1s",
		Layout: Layout{Fit: "cover"},
	}}
	err := Validate(&cfg)
	if err == nil || !strings.Contains(err.Error(), "publish.hook.fit") {
		t.Fatalf("err = %v, want the custom platform named", err)
	}

	cfg.Publish.Custom["hook"].Layout.Width = 800
	cfg.Publish.Custom["hook"].Layout.Height = 600
	if err := Validate(&cfg); err != nil {
		t.Errorf("with a frame: %v", err)
	}
}
