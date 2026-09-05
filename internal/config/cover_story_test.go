package config

import (
	"strings"
	"testing"
)

func TestInstagramCoverStoryValidation(t *testing.T) {
	cfg := Defaults()
	cfg.Publish.Instagram.CoverStory = true
	if err := Validate(&cfg); err != nil {
		t.Fatalf("a disabled Instagram ignores its cover-story configuration for single-platform replay: %v", err)
	}

	cfg.Publish.Instagram.Enabled = true
	if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "render.video.audio") {
		t.Fatalf("missing prerequisites: %v", err)
	}

	cfg.Render.Video.Audio = "anthem.mp3"
	if err := Validate(&cfg); err != nil {
		t.Fatalf("valid cover story: %v", err)
	}

	cfg.Publish.Instagram.Story = true
	if err := Validate(&cfg); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("legacy story combination: %v", err)
	}
}

func TestNewPublishFlagsBind(t *testing.T) {
	cfg := Defaults()
	b := Bindings(&cfg)
	if err := b["publish.instagram.cover-story"].Set("true", "test"); err != nil {
		t.Fatal(err)
	}
	if err := b["publish.discord.mention-everyone"].Set("true", "test"); err != nil {
		t.Fatal(err)
	}
	if !cfg.Publish.Instagram.CoverStory || !cfg.Publish.Discord.MentionEveryone {
		t.Fatalf("flags did not bind: %+v %+v", cfg.Publish.Instagram, cfg.Publish.Discord)
	}
}
