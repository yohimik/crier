package app

import (
	"testing"

	"github.com/yohimik/crier/internal/config"
)

// TestFittedVariantRendersAtTheMasterSize is the decision the feature turns
// on: a story is the square card resampled, not the card reflowed into a tall
// box. Reflowing would move the text; resampling keeps the design.
func TestFittedVariantRendersAtTheMasterSize(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Width, cfg.Render.Height = 1080, 1080
	cfg.Publish.Instagram.Enabled = true
	cfg.Publish.Instagram.Layout.Width = 1080
	cfg.Publish.Instagram.Layout.Height = 1920
	cfg.Publish.Instagram.Layout.Fit = "cover"

	vs := Variants(&cfg, []string{"instagram"})
	if len(vs) != 1 {
		t.Fatalf("variants = %+v", vs)
	}
	v := vs[0]

	if v.Width != 1080 || v.Height != 1080 {
		t.Errorf("rendered at %dx%d, want the master size", v.Width, v.Height)
	}
	if v.FitWidth != 1080 || v.FitHeight != 1920 {
		t.Errorf("frame = %dx%d, want the platform's", v.FitWidth, v.FitHeight)
	}
	if v.Fit != config.FitCover || !v.Fits() {
		t.Errorf("fit = %q", v.Fit)
	}
	if v.OutWidth() != 1080 || v.OutHeight() != 1920 {
		t.Errorf("output = %dx%d, want the frame", v.OutWidth(), v.OutHeight())
	}
}

// TestUnfittedVariantIsUnchanged: every configuration that predates this
// keeps rendering exactly as it did, with the platform's dimensions as the
// render size.
func TestUnfittedVariantIsUnchanged(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Width, cfg.Render.Height = 1080, 1080
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Telegram.Layout.Width = 1200
	cfg.Publish.Telegram.Layout.Height = 630

	v := Variants(&cfg, []string{"telegram"})[0]
	if v.Width != 1200 || v.Height != 630 {
		t.Errorf("rendered at %dx%d, want the platform's dimensions", v.Width, v.Height)
	}
	if v.Fits() {
		t.Error("no fit was configured")
	}
	if v.OutWidth() != 1200 || v.OutHeight() != 630 {
		t.Errorf("output = %dx%d", v.OutWidth(), v.OutHeight())
	}
}

// TestFitWithoutDimensionsDoesNotFit: validation refuses this, so the grouping
// only has to not make it worse — the platform renders as it always did.
func TestFitWithoutDimensionsDoesNotFit(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Width, cfg.Render.Height = 1080, 1080
	cfg.Publish.Discord.Enabled = true
	cfg.Publish.Discord.Layout.Fit = "cover"

	v := Variants(&cfg, []string{"discord"})[0]
	if v.Fits() {
		t.Error("a fit with no frame cannot fit")
	}
	if v.Width != 1080 || v.Height != 1080 {
		t.Errorf("= %dx%d", v.Width, v.Height)
	}
}

// TestFitIsPartOfTheVariantKey: two platforms agreeing about the layout and
// disagreeing about the frame are two pictures, however much work they share.
func TestFitIsPartOfTheVariantKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Width, cfg.Render.Height = 1080, 1080
	for _, p := range []*config.Layout{
		&cfg.Publish.Instagram.Layout, &cfg.Publish.Facebook.Layout,
	} {
		p.Width, p.Height = 1080, 1920
	}
	cfg.Publish.Instagram.Layout.Fit = "cover"
	cfg.Publish.Facebook.Layout.Fit = "contain"

	vs := Variants(&cfg, []string{"instagram", "facebook"})
	if len(vs) != 2 {
		t.Fatalf("variants = %d, want one per fit: %+v", len(vs), vs)
	}
	if vs[0].Key() == vs[1].Key() {
		t.Error("two different fits share a key and would share a file")
	}

	// The background is part of it too: two letterboxes in different colours
	// are two files.
	cfg.Publish.Facebook.Layout.Fit = "cover"
	cfg.Publish.Facebook.Layout.FitBackground = "#000000"
	cfg.Publish.Instagram.Layout.FitBackground = "#ffffff"
	vs = Variants(&cfg, []string{"instagram", "facebook"})
	if len(vs) != 2 {
		t.Errorf("variants = %d, want one per background", len(vs))
	}
}

// TestIdenticalFitsShareOneRender is the other half of the key: the whole
// point of grouping is that two platforms wanting the same picture get one.
func TestIdenticalFitsShareOneRender(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Width, cfg.Render.Height = 1080, 1080
	for _, p := range []*config.Layout{
		&cfg.Publish.Instagram.Layout, &cfg.Publish.Facebook.Layout,
	} {
		p.Width, p.Height = 1080, 1920
		p.Fit = "cover"
	}

	vs := Variants(&cfg, []string{"instagram", "facebook"})
	if len(vs) != 1 {
		t.Fatalf("variants = %d, want them shared: %+v", len(vs), vs)
	}
	if len(vs[0].Platforms) != 2 {
		t.Errorf("platforms = %v", vs[0].Platforms)
	}
}

// TestFittedAndUnfittedTogether is the motivating configuration: a story for
// Instagram and the card as it is for Telegram.
func TestFittedAndUnfittedTogether(t *testing.T) {
	cfg := config.Defaults()
	cfg.Render.Width, cfg.Render.Height = 1080, 1080
	cfg.Publish.Instagram.Layout.Width = 1080
	cfg.Publish.Instagram.Layout.Height = 1920
	cfg.Publish.Instagram.Layout.Fit = "cover"

	vs := Variants(&cfg, []string{"instagram", "telegram"})
	if len(vs) != 2 {
		t.Fatalf("variants = %d: %+v", len(vs), vs)
	}
	byPlatform := map[string]Variant{}
	for _, v := range vs {
		byPlatform[v.Platforms[0]] = v
	}
	if got := byPlatform["instagram"]; got.OutWidth() != 1080 || got.OutHeight() != 1920 {
		t.Errorf("instagram gets %dx%d", got.OutWidth(), got.OutHeight())
	}
	if got := byPlatform["telegram"]; got.OutWidth() != 1080 || got.OutHeight() != 1080 {
		t.Errorf("telegram gets %dx%d, want the master", got.OutWidth(), got.OutHeight())
	}
}
