package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
)

// audioBytes is a plausible first block for each container crier recognises.
// Only the header matters: nothing here decodes the audio.
var audioBytes = map[string][]byte{
	"id3":  append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), make([]byte, 8)...),
	"sync": append([]byte{0xFF, 0xFB, 0x90, 0x00}, make([]byte, 12)...),
	"m4a":  append([]byte("\x00\x00\x00\x20ftypM4A "), make([]byte, 8)...),
	"isom": append([]byte("\x00\x00\x00\x18ftypisom"), make([]byte, 8)...),
	"ogg":  append([]byte("OggS\x00\x02\x00\x00"), make([]byte, 12)...),
	"wav":  append([]byte("RIFF\x24\x08\x00\x00WAVEfmt "), make([]byte, 4)...),
}

// writeAudio puts one of those blocks on disk under the given name.
func writeAudio(t *testing.T, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// musicArtifact is the jingle the publisher tests attach.
func musicArtifact(t *testing.T) string {
	t.Helper()
	return writeAudio(t, "jingle.mp3", audioBytes["id3"])
}

func TestSniffAudioRecognisesTheFourContainers(t *testing.T) {
	tests := []struct {
		key         string
		wantFormat  string
		contentType string
	}{
		{"id3", "mp3", "audio/mpeg"},
		{"sync", "mp3", "audio/mpeg"},
		{"m4a", "m4a", "audio/mp4"},
		{"isom", "m4a", "audio/mp4"},
		{"ogg", "ogg", "audio/ogg"},
		{"wav", "wav", "audio/wav"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			// The extension deliberately disagrees with the bytes: the bytes win.
			path := writeAudio(t, "track.bin", audioBytes[tt.key])
			got, err := SniffAudio(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Format != tt.wantFormat || got.ContentType != tt.contentType {
				t.Errorf("format=%q type=%q, want %q and %q",
					got.Format, got.ContentType, tt.wantFormat, tt.contentType)
			}
			if got.Name != "track.bin" || !got.Attached() {
				t.Errorf("name=%q attached=%v", got.Name, got.Attached())
			}
			if got.Size != int64(len(audioBytes[tt.key])) {
				t.Errorf("size = %d, want %d", got.Size, len(audioBytes[tt.key]))
			}
		})
	}
}

func TestSniffAudioRefusesWhatIsNotAudio(t *testing.T) {
	tests := map[string]string{
		"a png":        "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
		"plain text":   "this is not a track at all",
		"empty":        "",
		"an mp4 video": "\x00\x00\x00\x18ftypqt  \x00\x00\x00\x00",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeAudio(t, "track.mp3", []byte(body))
			_, err := SniffAudio(path)
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !strings.Contains(err.Error(), "mp3, m4a, ogg and wav") {
				t.Errorf("error does not say what is accepted: %v", err)
			}
		})
	}
}

func TestSniffAudioReportsAMissingFile(t *testing.T) {
	_, err := SniffAudio(filepath.Join(t.TempDir(), "nowhere.mp3"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "reading the audio file") {
		t.Errorf("error = %v", err)
	}
}

func TestSniffAudioReportsADirectory(t *testing.T) {
	_, err := SniffAudio(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error = %v", err)
	}
}

// TestMusicForNamesTheKeyItCameFrom: the error has to point at the line the
// operator wrote, which is the platform's own key when it has one.
func TestMusicForNamesTheKeyItCameFrom(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere.mp3")

	shared := config.Defaults()
	shared.Publish.MusicFile = missing
	if _, err := MusicFor(&shared, "discord"); err == nil ||
		!strings.Contains(err.Error(), "publish.music-file") {
		t.Errorf("error = %v", err)
	}

	own := config.Defaults()
	own.Publish.MusicFile = missing
	own.Publish.Discord.Music.File = missing
	if _, err := MusicFor(&own, "discord"); err == nil ||
		!strings.Contains(err.Error(), "publish.discord.music-file") {
		t.Errorf("error = %v", err)
	}
}

// TestMusicForIsSilentWhereItCannotWork: a global music file with Instagram
// enabled is not an error, and Instagram gets no audio.
func TestMusicForIsSilentWhereItCannotWork(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.MusicFile = musicArtifact(t)

	got, err := MusicFor(&cfg, "instagram")
	if err != nil {
		t.Fatal(err)
	}
	if got.Attached() {
		t.Errorf("instagram was given %q", got.Path)
	}

	dc, err := MusicFor(&cfg, "discord")
	if err != nil {
		t.Fatal(err)
	}
	if !dc.Attached() || dc.Format != "mp3" {
		t.Errorf("discord got %+v", dc)
	}
}

// TestCheckMusicOneRowPerKey: the report has a row per line the operator
// wrote, so a broken override says which line to change.
func TestCheckMusicOneRowPerKey(t *testing.T) {
	shared := musicArtifact(t)
	own := writeAudio(t, "telegram.ogg", audioBytes["ogg"])

	cfg := config.Defaults()
	cfg.Publish.MusicFile = shared
	cfg.Publish.Telegram.Music.File = own
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Discord.Enabled = true
	cfg.Publish.Instagram.Enabled = true

	got := CheckMusic(&cfg)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want the shared file and telegram's own: %+v", len(got), got)
	}
	if got[0].Key != "publish.music-file" || got[0].Audio.Format != "mp3" {
		t.Errorf("shared row = %+v", got[0])
	}
	// Telegram named its own, so the shared file reaches Discord alone.
	// Instagram is enabled and cannot carry audio, so it appears nowhere.
	if len(got[0].Platforms) != 1 || got[0].Platforms[0] != "discord" {
		t.Errorf("the shared file reaches %v, want discord alone", got[0].Platforms)
	}
	if got[1].Key != "publish.telegram.music-file" || got[1].Audio.Format != "ogg" {
		t.Errorf("telegram row = %+v", got[1])
	}
	if !got[1].Reaches() || !strings.Contains(got[1].Describe(), "telegram") {
		t.Errorf("telegram row = %+v, %q", got[1], got[1].Describe())
	}
}

// TestCheckMusicSaysWhenNothingCarriesIt: the file is fine and the run around
// it will post no audio at all, which is the finding worth reporting.
func TestCheckMusicSaysWhenNothingCarriesIt(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.MusicFile = musicArtifact(t)
	cfg.Publish.X.Enabled = true

	got := CheckMusic(&cfg)
	if len(got) != 1 {
		t.Fatalf("rows = %+v", got)
	}
	if got[0].Err != nil || got[0].Reaches() {
		t.Errorf("row = %+v", got[0])
	}
	if !strings.Contains(got[0].Describe(), "no enabled platform can carry it") {
		t.Errorf("describe = %q", got[0].Describe())
	}
}

func TestCheckMusicReportsAFileItCannotRead(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.MusicFile = filepath.Join(t.TempDir(), "nowhere.mp3")
	cfg.Publish.Discord.Enabled = true

	got := CheckMusic(&cfg)
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("rows = %+v", got)
	}
	if got[0].Describe() != "" {
		t.Errorf("a broken file should describe nothing, got %q", got[0].Describe())
	}
}

func TestCheckMusicIsEmptyWhenNoneIsConfigured(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Discord.Enabled = true
	if got := CheckMusic(&cfg); len(got) != 0 {
		t.Errorf("rows = %+v, want none", got)
	}
}

func TestMusicForWithNothingConfigured(t *testing.T) {
	cfg := config.Defaults()
	got, err := MusicFor(&cfg, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if got.Attached() {
		t.Errorf("got %+v, want nothing", got)
	}
}
