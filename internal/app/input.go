package app

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// Mode is where a run enters the pipeline.
//
// The four steps — template, render, encode, publish — are not one road with
// one entrance. A poster somebody drew in Figma is a perfectly good thing to
// publish, and frames a simulation wrote are a perfectly good thing to encode.
// Naming the modes is what keeps those from being second-class.
type Mode int

const (
	// ModeFull is template → render → encode → stage → publish.
	ModeFull Mode = iota
	// ModePublishInput publishes a file that already exists.
	ModePublishInput
	// ModeEncodeFrames encodes images that already exist into a clip.
	ModeEncodeFrames
)

// String names the mode as the configuration key that selected it.
func (m Mode) String() string {
	switch m {
	case ModePublishInput:
		return "publish.input"
	case ModeEncodeFrames:
		return "render.video.frames-input"
	default:
		return "full"
	}
}

// ModeOf decides which entry mode a configuration asks for, and refuses one
// that asks for two.
//
// Two generators for the same slot is not a preference to resolve; it is a
// configuration whose author believed two different things, and picking one
// would hide that.
func ModeOf(cfg *config.Config) (Mode, error) {
	input := strings.TrimSpace(cfg.Publish.Input)
	frames := strings.TrimSpace(cfg.Render.Video.FramesInput)

	switch {
	case input != "" && frames != "":
		return ModeFull, failf(ExitConfig,
			"publish.input and render.video.frames-input both name where the artifact comes from; set one")
	case input != "" && cfg.Render.Video.Enabled:
		return ModeFull, failf(ExitConfig,
			"publish.input names a file to publish and render.video.enabled asks crier to make one; set one")
	case input != "":
		return ModePublishInput, nil
	case frames != "":
		return ModeEncodeFrames, nil
	default:
		return ModeFull, nil
	}
}

// sniff reads a file's first bytes and says what it is.
//
// The extension is not consulted. A file named .png that is a JPEG would be
// sent to a platform as the wrong content type, and the platforms that check
// reject it with a message about something else entirely.
func sniff(path string) (render.Kind, config.Format, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, 16)
	n, err := f.Read(head)
	if err != nil && n == 0 {
		return "", "", "", fmt.Errorf("reading %s: %w", path, err)
	}
	head = head[:n]

	switch {
	case len(head) >= 8 && string(head[:8]) == "\x89PNG\r\n\x1a\n":
		return render.KindImage, config.PNG, config.PNG.ContentType(), nil
	case len(head) >= 3 && head[0] == 0xff && head[1] == 0xd8 && head[2] == 0xff:
		return render.KindImage, config.JPEG, config.JPEG.ContentType(), nil
	case len(head) >= 6 && (string(head[:6]) == "GIF87a" || string(head[:6]) == "GIF89a"):
		return render.KindGIF, "", render.GIFContentType, nil
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		return render.KindVideo, "", render.VideoContentType, nil
	default:
		return "", "", "", fmt.Errorf(
			"%s is not a PNG, JPEG, GIF or MP4; crier publishes those four", path)
	}
}

// LoadInput describes a file the configuration named as the thing to publish.
func LoadInput(path string) (render.Artifact, error) {
	st, err := os.Stat(path)
	if err != nil {
		return render.Artifact{}, failf(ExitConfig, "publish.input: %v", err)
	}
	if st.IsDir() {
		return render.Artifact{}, failf(ExitConfig, "publish.input: %s is a directory", path)
	}
	kind, format, contentType, err := sniff(path)
	if err != nil {
		return render.Artifact{}, fail(ExitConfig, err)
	}

	a := render.Artifact{
		Kind: kind, Format: format, ContentType: contentType,
		Path: path, Size: st.Size(),
	}
	// The dimensions are worth having for the report and for the platforms
	// that check them, and every format above except MP4 can be measured
	// without decoding the whole file.
	if kind != render.KindVideo {
		if cfg, err := decodeConfigOf(path); err == nil {
			a.Width, a.Height = cfg.Width, cfg.Height
		}
	}
	return a, nil
}

func decodeConfigOf(path string) (image.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return image.Config{}, err
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	return cfg, err
}

// FrameFiles expands render.video.frames-input into the frames to encode.
//
// A directory means every image in it; anything containing a wildcard is a
// glob. Either way the order is lexicographic, which is what makes
// frame-0001.png through frame-0090.png work and is why a frame numbered
// without padding is worth warning about.
func FrameFiles(spec string) ([]string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, failf(ExitConfig, "render.video.frames-input is empty")
	}

	var candidates []string
	if st, err := os.Stat(spec); err == nil && st.IsDir() {
		entries, err := os.ReadDir(spec)
		if err != nil {
			return nil, failf(ExitConfig, "render.video.frames-input: %v", err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				candidates = append(candidates, filepath.Join(spec, e.Name()))
			}
		}
	} else {
		matches, err := filepath.Glob(spec)
		if err != nil {
			return nil, failf(ExitConfig, "render.video.frames-input: %v", err)
		}
		candidates = matches
	}

	var out []string
	for _, path := range candidates {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png", ".jpg", ".jpeg", ".gif":
			out = append(out, path)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, failf(ExitConfig,
			"render.video.frames-input matched no PNG, JPEG or GIF files: %s", spec)
	}
	return out, nil
}

// frameReader decodes the frame files one at a time, checking as it goes that
// they all agree about the size.
//
// One at a time rather than all at once: ninety 1080-square frames decoded up
// front is a gigabyte of images to hold while ffmpeg reads them one by one.
type frameReader struct {
	files []string
	w, h  int
}

// newFrameReader reads the first frame, which decides the size.
func newFrameReader(files []string) (*frameReader, *image.RGBA, error) {
	first, err := decodeFrame(files[0])
	if err != nil {
		return nil, nil, err
	}
	b := first.Bounds()
	return &frameReader{files: files, w: b.Dx(), h: b.Dy()}, first, nil
}

// at decodes one frame and refuses one that is a different size.
func (r *frameReader) at(_ context.Context, i int) (*image.RGBA, error) {
	if i < 0 || i >= len(r.files) {
		return nil, fmt.Errorf("frame %d is outside the %d frames given", i, len(r.files))
	}
	img, err := decodeFrame(r.files[i])
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if b.Dx() != r.w || b.Dy() != r.h {
		return nil, fmt.Errorf("%s is %dx%d but the first frame is %dx%d; every frame has to be the same size",
			filepath.Base(r.files[i]), b.Dx(), b.Dy(), r.w, r.h)
	}
	return img, nil
}

// decodeFrame reads one image file as RGBA.
func decodeFrame(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", filepath.Base(path), err)
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba, nil
	}
	b := img.Bounds()
	// Drawn into a new image at the origin: a decoded frame may have bounds
	// that do not start at (0,0), and the encoder indexes from the origin.
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out, nil
}
