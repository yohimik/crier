package config

import (
	"strings"
	"testing"
)

// TestOneDimensionAloneIsRefused is the regression test.
//
// crier emits an @page rule only when it has both a width and a height, so one
// on its own was silently ignored: the render came out at the engine's default
// size and nothing anywhere said why the width had not been honoured.
func TestOneDimensionAloneIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name          string
		width, height int
		says          string
	}{
		{"a width with no height", 1080, 0, "render.height"},
		{"a height with no width", 0, 1080, "render.width"},
	} {
		cfg := Defaults()
		cfg.Render.Template = "t.html"
		cfg.Render.Width, cfg.Render.Height = tt.width, tt.height

		err := Validate(&cfg)
		if err == nil {
			t.Errorf("%s: should be refused", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.says) {
			t.Errorf("%s: the error should name %s: %v", tt.name, tt.says, err)
		}
		if !strings.Contains(err.Error(), "would be ignored") {
			t.Errorf("%s: the error should say why: %v", tt.name, err)
		}
	}

	// Both, or neither, are fine.
	for _, size := range [][2]int{{1080, 1080}, {0, 0}} {
		cfg := Defaults()
		cfg.Render.Template = "t.html"
		cfg.Render.Width, cfg.Render.Height = size[0], size[1]
		if err := Validate(&cfg); err != nil {
			t.Errorf("%dx%d should be fine: %v", size[0], size[1], err)
		}
	}
}

// TestAPlatformMayOverrideOneDimension: a per-platform layout is different,
// because the other dimension falls back to the render default rather than
// being dropped.
func TestAPlatformMayOverrideOneDimension(t *testing.T) {
	cfg := Defaults()
	cfg.Render.Template = "t.html"
	cfg.Render.Width, cfg.Render.Height = 1080, 1080
	cfg.Publish.Instagram.Layout.Height = 1920

	if err := Validate(&cfg); err != nil {
		t.Errorf("a platform overriding one dimension should be fine: %v", err)
	}
}
