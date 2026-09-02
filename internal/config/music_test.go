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

func TestAutoAddMusicIsOffByDefault(t *testing.T) {
	cfg := Defaults()
	if cfg.Publish.TikTok.AutoAddMusic {
		t.Error("publish.tiktok.auto-add-music defaults to true")
	}
}
