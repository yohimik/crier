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
	"github.com/yohimik/crier/internal/render"
)

// VideoFile is the clip that opens a post, ahead of the pages.
//
// It is a file rather than anything the platform holds, and it is the only way
// audio reaches Instagram: Instagram takes no audio file and no track id, so a
// soundtrack has to arrive inside a video. That is what makes a lead video the
// other half of the music story rather than a feature of its own.
type VideoFile struct {
	// Path is the file on this machine.
	Path string
	// Name is what a platform will show, which is the file's own name.
	Name string
	// ContentType is the media type it is uploaded as.
	ContentType string
	// Size is the file's length in bytes.
	Size int64
}

// Attached reports whether there is a clip at all. The zero VideoFile is what
// an ordinary post carries.
func (v VideoFile) Attached() bool { return v.Path != "" }

// Part is the clip as a multipart file part, under the field name the platform
// wants it in.
func (v VideoFile) Part(field string) httpx.Part {
	return httpx.FilePart(field, v.Path, v.ContentType)
}

// SniffVideo reads a file's first bytes and checks it is a video crier can
// post.
//
// MP4 and nothing else. Instagram and Telegram both document MP4 for the
// shapes a lead video lands in, and the extension is not consulted for the
// same reason it is not consulted for audio: a mislabelled file is refused by
// the platform after the post rather than by crier before it.
func SniffVideo(path string) (VideoFile, error) {
	if strings.TrimSpace(path) == "" {
		return VideoFile{}, errors.New("no video file")
	}
	st, err := os.Stat(path)
	if err != nil {
		return VideoFile{}, fmt.Errorf("reading the video file: %w", err)
	}
	if st.IsDir() {
		return VideoFile{}, fmt.Errorf("%s is a directory, not a video file", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return VideoFile{}, fmt.Errorf("reading the video file: %w", err)
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, 12)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return VideoFile{}, fmt.Errorf("reading the video file: %w", err)
	}
	head = head[:n]

	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		// The reason first, then the path: a report column is narrow.
		return VideoFile{}, fmt.Errorf(
			"does not begin like an MP4; a lead video has to be one: %s", path)
	}
	return VideoFile{
		Path:        path,
		Name:        filepath.Base(path),
		ContentType: render.VideoContentType,
		Size:        st.Size(),
	}, nil
}

// LeadVideoFor is the clip one platform opens its post with, already checked.
//
// Like the audio, it is resolved while the publishers are built, which is
// before anything is rendered: a clip that is missing or is not an MP4 is a
// configuration mistake, and a render is a slow way to learn about a typo.
func LeadVideoFor(cfg *config.Config, platform string) (VideoFile, error) {
	path := config.LeadVideoFor(&cfg.Publish, platform)
	if strings.TrimSpace(path) == "" {
		return VideoFile{}, nil
	}
	video, err := SniffVideo(path)
	if err != nil {
		return VideoFile{}, fmt.Errorf("publish.%s.lead-video: %w", platform, err)
	}
	return video, nil
}

// LeadVideoCheck is one configured clip and what came of looking at it.
//
// It is the shape `crier ping` reports, matching MusicCheck so the two rows
// read the same way.
type LeadVideoCheck struct {
	// Key is the configuration key the path was written in.
	Key string
	// Platform is the platform that will post it.
	Platform string
	// Path is the file the key named.
	Path string
	// Video is what the file turned out to be, when it could be read.
	Video VideoFile
	// Enabled says whether that platform is turned on for this run.
	Enabled bool
	// Err is why the clip cannot be used.
	Err error
}

// CheckLeadVideos looks at every clip a configuration names.
func CheckLeadVideos(cfg *config.Config) []LeadVideoCheck {
	enabled := map[string]bool{}
	for _, name := range Enabled(cfg) {
		enabled[name] = true
	}

	var out []LeadVideoCheck
	for _, name := range config.LeadVideoPlatforms {
		path := config.LeadVideoFor(&cfg.Publish, name)
		if path == "" {
			continue
		}
		check := LeadVideoCheck{
			Key:      "publish." + name + ".lead-video",
			Platform: name,
			Path:     path,
			Enabled:  enabled[name],
		}
		check.Video, check.Err = SniffVideo(path)
		out = append(out, check)
	}
	return out
}

// Describe is the one-line summary of what the clip is, for a report.
func (c LeadVideoCheck) Describe() string {
	if c.Err != nil {
		return ""
	}
	out := "mp4, " + humanSize(c.Video.Size)
	if !c.Enabled {
		return out + "; " + c.Platform + " is not enabled"
	}
	return out + "; opens the " + c.Platform + " post"
}
