package render

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/raster"
)

// Kind is what an artifact is: a still image or a video clip.
//
// Publishers declare which kinds they can post, so routing a video at a
// platform that only takes stills is a configuration error found before the
// first byte goes out rather than an API rejection halfway through.
type Kind string

const (
	// KindImage is a still image, PNG or JPEG.
	KindImage Kind = "image"
	// KindVideo is an MP4 clip.
	KindVideo Kind = "video"
	// KindGIF is an animated GIF.
	//
	// It is a kind of its own rather than a video with a different extension,
	// because the platforms treat it as one: Telegram wants sendAnimation
	// rather than sendVideo, X wants a different media category, and four
	// platforms will not take one at all.
	KindGIF Kind = "gif"
)

// VideoContentType is the MIME type of the MP4s crier produces.
const VideoContentType = "video/mp4"

// GIFContentType is the MIME type of the animations it produces.
const GIFContentType = "image/gif"

// Artifact is one encoded file on disk, ready to be uploaded.
type Artifact struct {
	Kind Kind
	// Format is the image format; empty for a video.
	Format      config.Format
	ContentType string
	Path        string
	Size        int64
	Width       int
	Height      int
}

// Encoder writes rendered images into a directory.
type Encoder struct {
	// Dir is where the files are written. Required.
	Dir string
	// JPEGQuality is 1 to 100.
	JPEGQuality int
	// Background is what a transparent pixel is flattened onto for JPEG, which
	// has no alpha channel.
	Background color.Color
}

// Encode writes one image in one format and returns the artifact.
//
// name is the base file name without an extension, which is how one rendering
// produces both the PNG a Telegram post wants and the JPEG Instagram insists
// on without the two overwriting each other.
func (e Encoder) Encode(img *image.RGBA, format config.Format, name string) (Artifact, error) {
	if e.Dir == "" {
		return Artifact{}, fmt.Errorf("encode: no output directory")
	}
	path := filepath.Join(e.Dir, name+format.Ext())
	return e.EncodeTo(img, format, path)
}

// EncodeTo writes one image to an exact path.
func (e Encoder) EncodeTo(img *image.RGBA, format config.Format, path string) (Artifact, error) {
	if img == nil {
		return Artifact{}, fmt.Errorf("encode: no image")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Artifact{}, fmt.Errorf("encode: %w", err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("encode: %w", err)
	}
	// Close is deferred and its error checked: an encoder that buffers would
	// report a full disk only here.
	encodeErr := func() error {
		switch format {
		case config.JPEG:
			return raster.EncodeJPEG(f, img, e.background(), e.JPEGQuality)
		default:
			return raster.EncodePNG(f, img)
		}
	}()
	closeErr := f.Close()
	if encodeErr != nil {
		_ = os.Remove(path)
		return Artifact{}, fmt.Errorf("encoding %s: %w", format, encodeErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return Artifact{}, fmt.Errorf("writing %s: %w", path, closeErr)
	}

	st, err := os.Stat(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("encode: %w", err)
	}
	b := img.Bounds()
	return Artifact{
		Kind:        KindImage,
		Format:      format,
		ContentType: format.ContentType(),
		Path:        path,
		Size:        st.Size(),
		Width:       b.Dx(),
		Height:      b.Dy(),
	}, nil
}

func (e Encoder) background() color.Color {
	if e.Background == nil {
		return color.White
	}
	return e.Background
}
