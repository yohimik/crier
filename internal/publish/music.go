package publish

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
)

// AudioFile is the audio a post carries beside its pictures.
//
// It is a file the operator holds the rights to, and nothing more. No public
// API anywhere takes the id of a licensed track: the pickers Instagram,
// Facebook and TikTok show inside their own apps have no endpoint behind them,
// and neither the Meta Sound Collection nor TikTok's Commercial Music Library
// can be reached from a program. What can be done is to send a file, which is
// what this is.
type AudioFile struct {
	// Path is the file on this machine.
	Path string
	// Name is what the platform will show, which is the file's own name.
	Name string
	// Format is the container crier recognised: mp3, m4a, ogg or wav.
	Format string
	// ContentType is the media type that goes with that container.
	ContentType string
	// Size is the file's length in bytes.
	Size int64
}

// Attached reports whether there is any audio at all. The zero AudioFile is
// what a post with no music carries, so every publisher can ask this rather
// than compare against an empty string.
func (a AudioFile) Attached() bool { return a.Path != "" }

// AudioFormats are the containers crier recognises, in the order it tries
// them, with the media type each one is sent as.
//
// The list is short on purpose. These four cover what the three platforms play
// inline, and a file crier cannot name is refused rather than uploaded as
// something the platform will show as a grey box.
var AudioFormats = []struct {
	Name        string
	ContentType string
}{
	{"mp3", "audio/mpeg"},
	{"m4a", "audio/mp4"},
	{"ogg", "audio/ogg"},
	{"wav", "audio/wav"},
}

// SniffAudio reads a file's first bytes and says which audio container it is.
//
// The extension is not consulted. An operator who renamed a WAV to .mp3 would
// otherwise find out from the platform, after the upload, in the form of a
// track nobody can play.
func SniffAudio(path string) (AudioFile, error) {
	if strings.TrimSpace(path) == "" {
		return AudioFile{}, errors.New("no audio file")
	}
	st, err := os.Stat(path)
	if err != nil {
		return AudioFile{}, fmt.Errorf("reading the audio file: %w", err)
	}
	if st.IsDir() {
		return AudioFile{}, fmt.Errorf("%s is a directory, not an audio file", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return AudioFile{}, fmt.Errorf("reading the audio file: %w", err)
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, 16)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return AudioFile{}, fmt.Errorf("reading the audio file: %w", err)
	}
	head = head[:n]

	format, contentType := sniffAudioBytes(head)
	if format == "" {
		return AudioFile{}, fmt.Errorf(
			"%s does not begin like an audio file; crier recognises mp3, m4a, ogg and wav", path)
	}
	return AudioFile{
		Path:        path,
		Name:        filepath.Base(path),
		Format:      format,
		ContentType: contentType,
		Size:        st.Size(),
	}, nil
}

// sniffAudioBytes matches the magic bytes of the four containers.
func sniffAudioBytes(b []byte) (format, contentType string) {
	switch {
	case len(b) >= 3 && string(b[:3]) == "ID3":
		// An ID3v2 tag in front of the MPEG frames, which is how nearly every
		// mp3 in the wild starts.
		return "mp3", "audio/mpeg"
	case len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0:
		// A bare MPEG frame sync: eleven set bits.
		return "mp3", "audio/mpeg"
	case len(b) >= 4 && string(b[:4]) == "OggS":
		return "ogg", "audio/ogg"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE":
		return "wav", "audio/wav"
	case len(b) >= 12 && string(b[4:8]) == "ftyp" && isMP4Brand(string(b[8:12])):
		// An ISO base media file. crier cannot tell an audio-only one from a
		// video from the header alone, and does not try: the operator asked for
		// this file to be the music.
		return "m4a", "audio/mp4"
	default:
		return "", ""
	}
}

// isMP4Brand reports whether a major brand is one an audio file carries.
func isMP4Brand(brand string) bool {
	switch brand {
	case "M4A ", "M4B ", "mp41", "mp42", "isom", "iso2", "dash":
		return true
	default:
		return false
	}
}

// MusicFor is the audio one platform will attach, already checked.
//
// It is called while the publishers are built, which is before anything is
// rendered: an audio file that is missing or is not audio is a configuration
// mistake, and finding it out after the render and the upload would be a slow
// way to learn about a typo.
func MusicFor(cfg *config.Config, platform string) (AudioFile, error) {
	path := config.MusicFileFor(&cfg.Publish, platform)
	if strings.TrimSpace(path) == "" {
		return AudioFile{}, nil
	}
	audio, err := SniffAudio(path)
	if err != nil {
		return AudioFile{}, fmt.Errorf("%s: %w", musicKeyFor(&cfg.Publish, platform), err)
	}
	return audio, nil
}

// musicKeyFor names the key the value came from, so the error points at the
// line the operator has to change rather than at the shared one.
func musicKeyFor(p *config.Publish, platform string) string {
	if m := config.MusicOf(p, platform); m != nil && strings.TrimSpace(m.File) != "" {
		return "publish." + platform + ".music-file"
	}
	return "publish.music-file"
}

// Part is the audio as a multipart file part, under the field name the
// platform wants it in.
func (a AudioFile) Part(field string) httpx.Part {
	return httpx.FilePart(field, a.Path, a.ContentType)
}
