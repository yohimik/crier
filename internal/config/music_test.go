package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMusicIsRefusedWhereItCannotWork is the whole point of the per-platform
// key existing everywhere: a track aimed at Instagram is answered with the
// reason rather than with an unknown-key error that reads like a typo.
func TestMusicIsRefusedWhereItCannotWork(t *testing.T) {
	for _, name := range Platforms {
		t.Run(name, func(t *testing.T) {
			cfg := Defaults()
			MusicOf(&cfg.Publish, name).File = "jingle.mp3"

			err := Validate(&cfg)
			if CanCarryMusic(name) {
				if err != nil {
					t.Fatalf("%s can carry audio, but the configuration was refused: %v", name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s cannot carry audio and the configuration was accepted", name)
			}
			msg := err.Error()
			for _, want := range []string{"publish." + name + ".music-file", name, "discord, slack, telegram"} {
				if !strings.Contains(msg, want) {
					t.Errorf("missing %q in:\n%s", want, msg)
				}
			}
		})
	}
}

// TestGlobalMusicIsNotRefusedAnywhere: the shared key means it for the
// platforms that can take it. Refusing a run because Instagram happens to be
// enabled would make the global key useless.
func TestGlobalMusicIsNotRefusedAnywhere(t *testing.T) {
	cfg := Defaults()
	cfg.Publish.MusicFile = "jingle.mp3"
	if err := Validate(&cfg); err != nil {
		t.Fatalf("a global music file was refused: %v", err)
	}
}

func TestMusicFileFor(t *testing.T) {
	cfg := Defaults()
	cfg.Publish.MusicFile = "shared.mp3"
	cfg.Publish.Telegram.Music.File = "telegram.ogg"

	tests := map[string]string{
		"telegram":  "telegram.ogg",
		"discord":   "shared.mp3",
		"slack":     "shared.mp3",
		"instagram": "",
		"x":         "",
		"nonesuch":  "",
	}
	for name, want := range tests {
		if got := MusicFileFor(&cfg.Publish, name); got != want {
			t.Errorf("MusicFileFor(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestMusicFileIsAnchoredToTheConfigFile: the audio is a path key like every
// other, so a project naming its jingle beside its config means the file
// beside its config, whatever directory crier was run from.
func TestMusicFileIsAnchoredToTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crier.yaml")
	body := "publish:\n  music-file: jingle.mp3\n  discord:\n    music-file: sub/other.ogg\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Load(context.Background(), Options{Path: path, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Config.Publish.MusicFile, filepath.Join(dir, "jingle.mp3"); got != want {
		t.Errorf("publish.music-file = %q, want %q", got, want)
	}
	if got, want := res.Config.Publish.Discord.Music.File, filepath.Join(dir, "sub", "other.ogg"); got != want {
		t.Errorf("publish.discord.music-file = %q, want %q", got, want)
	}
}

// TestLeadVideoIsRefusedWhereItCannotWork: a post is pictures or a video on
// eight of the ten, and the key says so where somebody would look for it.
func TestLeadVideoIsRefusedWhereItCannotWork(t *testing.T) {
	for _, name := range Platforms {
		t.Run(name, func(t *testing.T) {
			cfg := Defaults()
			LeadVideoOf(&cfg.Publish, name).File = "anthem.mp4"

			err := Validate(&cfg)
			if CanCarryLeadVideo(name) {
				if err != nil {
					t.Fatalf("%s takes mixed media, but the configuration was refused: %v", name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s cannot open a post with a clip and the configuration was accepted", name)
			}
			msg := err.Error()
			for _, want := range []string{"publish." + name + ".lead-video", "instagram and telegram"} {
				if !strings.Contains(msg, want) {
					t.Errorf("missing %q in:\n%s", want, msg)
				}
			}
		})
	}
}

func TestLeadVideoFor(t *testing.T) {
	cfg := Defaults()
	cfg.Publish.Instagram.LeadVideo.File = "anthem.mp4"

	tests := map[string]string{
		"instagram": "anthem.mp4",
		// There is no shared key, so telegram gets nothing from instagram's.
		"telegram": "",
		"discord":  "",
		"nonesuch": "",
	}
	for name, want := range tests {
		if got := LeadVideoFor(&cfg.Publish, name); got != want {
			t.Errorf("LeadVideoFor(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestLeadVideoIsAnchoredToTheConfigFile: a path key like every other, so a
// project naming its clip beside its config means the file beside its config.
func TestLeadVideoIsAnchoredToTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crier.yaml")
	body := "publish:\n  telegram:\n    lead-video: clips/anthem.mp4\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Load(context.Background(), Options{Path: path, Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Config.Publish.Telegram.LeadVideo.File, filepath.Join(dir, "clips", "anthem.mp4"); got != want {
		t.Errorf("publish.telegram.lead-video = %q, want %q", got, want)
	}
}

// TestMusicAndLeadVideoAreSeparateFields guards the one hazard of embedding
// two structs that both spell their field File: the two keys must not land in
// the same place.
func TestMusicAndLeadVideoAreSeparateFields(t *testing.T) {
	cfg := Defaults()
	cfg.Publish.Telegram.Music.File = "jingle.mp3"
	cfg.Publish.Telegram.LeadVideo.File = "anthem.mp4"

	if got := MusicFileFor(&cfg.Publish, "telegram"); got != "jingle.mp3" {
		t.Errorf("music = %q", got)
	}
	if got := LeadVideoFor(&cfg.Publish, "telegram"); got != "anthem.mp4" {
		t.Errorf("lead video = %q", got)
	}
}

func TestAutoAddMusicIsOffByDefault(t *testing.T) {
	cfg := Defaults()
	if cfg.Publish.TikTok.AutoAddMusic {
		t.Error("publish.tiktok.auto-add-music defaults to true")
	}
}
