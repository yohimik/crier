package render

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/procutil"
	"github.com/yohimik/crier/internal/raster"
)

// FrameFunc produces one frame. It is called with the frame index, in order,
// and the image it returns is written to ffmpeg before the next call, which is
// what keeps memory at one frame however long the clip is.
type FrameFunc func(ctx context.Context, index int) (*image.RGBA, error)

// VideoOptions describes one encode.
type VideoOptions struct {
	// Output is the file ffmpeg writes.
	Output string
	// Frames is how many frames to render. Required.
	Frames int
	// FPS is the frame rate.
	FPS int
	// Width and Height are the frame size in pixels.
	Width, Height int
	// Bin is the ffmpeg executable.
	Bin string
	// Preset selects the codec arguments: h264, h265, vp9 or none. It is
	// ignored when Format is GIF, which has one encoder.
	Preset string
	// Format is mp4 or gif. Empty means mp4.
	Format string
	// FitFilter is a video filter chain applied before everything else — the
	// object-fit for the platform this clip is for. It is a filter expression
	// rather than an argument list because ffmpeg takes one -vf, and a GIF
	// already spends it on the palette: the two have to be composed into one
	// chain rather than passed separately.
	FitFilter string
	// ExtraArgs are appended after the preset and before the output.
	ExtraArgs []string
	// Audio is an optional audio file mixed in.
	Audio string
	// Env replaces ffmpeg's environment when non-nil.
	Env []string
	// Background is what a transparent pixel is flattened onto. Video has no
	// alpha channel, so a frame is always made opaque before it is written.
	Background color.Color
	// ProgressEvery logs a debug line every this many frames. Zero means 30.
	ProgressEvery int
	// Logger receives ffmpeg's output and the progress.
	Logger zerolog.Logger
}

// ErrFFmpegMissing says the encoder is not installed. crier does not bundle
// ffmpeg, and saying so plainly is better than a failure deep in a pipe.
var ErrFFmpegMissing = errors.New("ffmpeg was not found on PATH; install it, or set render.video.ffmpeg-bin")

// CheckFFmpeg reports whether the encoder is available, so a video run fails
// before it renders its first frame rather than after its last.
func CheckFFmpeg(bin string) error {
	if bin == "" {
		bin = "ffmpeg"
	}
	if _, err := procutil.LookPath(bin); err != nil {
		return fmt.Errorf("%w (%v)", ErrFFmpegMissing, err)
	}
	return nil
}

// EncodeVideo renders every frame and streams it into ffmpeg.
//
// The frames go out as raw RGBA on ffmpeg's standard input. Writing a file per
// frame would be simpler and would also mean a three second clip at 30 frames
// a second leaves ninety files behind when something fails halfway.
func EncodeVideo(ctx context.Context, o VideoOptions, frame FrameFunc) (Artifact, error) {
	if o.Frames <= 0 {
		return Artifact{}, fmt.Errorf("video: no frames to render")
	}
	if o.Width <= 0 || o.Height <= 0 {
		return Artifact{}, fmt.Errorf("video: frame size is %dx%d", o.Width, o.Height)
	}
	if o.Output == "" {
		return Artifact{}, fmt.Errorf("video: no output path")
	}
	if err := CheckFFmpeg(o.Bin); err != nil {
		return Artifact{}, err
	}
	fps := o.FPS
	if fps <= 0 {
		fps = 30
	}
	every := o.ProgressEvery
	if every <= 0 {
		every = 30
	}
	bin := o.Bin
	if bin == "" {
		bin = "ffmpeg"
	}

	args := FFmpegArgs(o)
	start := time.Now()
	proc, err := procutil.Start(ctx, procutil.Options{
		Name:      "ffmpeg",
		Bin:       bin,
		Args:      args,
		Env:       o.Env,
		Logger:    o.Logger,
		WithStdin: true,
	})
	if err != nil {
		return Artifact{}, err
	}

	writeErr := writeFrames(ctx, o, proc, frame, every)
	// Closing the input is what tells ffmpeg the stream is over; it has to
	// happen even when a frame failed, or Wait would block until the timeout.
	closeErr := proc.CloseStdin()
	waitErr := proc.Wait()

	switch {
	case writeErr != nil:
		return Artifact{}, writeErr
	case waitErr != nil:
		return Artifact{}, waitErr
	case closeErr != nil:
		return Artifact{}, fmt.Errorf("ffmpeg input: %w", closeErr)
	}

	st, err := os.Stat(o.Output)
	if err != nil {
		return Artifact{}, fmt.Errorf("ffmpeg produced no output: %w", err)
	}
	o.Logger.Info().
		Str("path", o.Output).
		Int("frames", o.Frames).
		Int("fps", fps).
		Int64("bytes", st.Size()).
		Dur("elapsed", time.Since(start)).
		Msg("encoded video")

	kind, contentType := KindVideo, VideoContentType
	if IsGIF(o.Format) {
		kind, contentType = KindGIF, GIFContentType
	}
	return Artifact{
		Kind:        kind,
		ContentType: contentType,
		Path:        o.Output,
		Size:        st.Size(),
		Width:       o.Width,
		Height:      o.Height,
	}, nil
}

// RefitOptions describes reshaping a clip that already exists.
type RefitOptions struct {
	// Input is the clip to read.
	Input string
	// Output is the clip to write.
	Output string
	// Filter is the video filter chain, which is what does the fitting. An
	// empty filter is a caller mistake: there would be nothing to re-encode
	// for.
	Filter string
	// Width and Height are the frame size the filter produces. They are the
	// platform's own frame, and they are reported on the artifact rather than
	// measured, because the filter is what decides them.
	Width, Height int
	// Bin is the ffmpeg executable.
	Bin string
	// Preset selects the codec arguments: h264, h265, vp9 or none.
	Preset string
	// ExtraArgs are appended after the preset and before the output.
	ExtraArgs []string
	// Env replaces ffmpeg's environment when non-nil.
	Env    []string
	Logger zerolog.Logger
}

// RefitVideo re-encodes a clip through a fit filter.
//
// It exists for the clip crier was handed rather than made. A rendered clip is
// fitted while it is encoded, in the same pass that draws it; one that arrives
// as a file has already been encoded, and the only way to change its shape is
// to encode it again.
//
// The audio is copied rather than re-encoded. Re-encoding it would cost time
// and a generation of quality to produce the same soundtrack, and the whole
// point of the clip is often the soundtrack. A clip with no audio copies
// nothing, which ffmpeg treats as the ordinary case rather than as an error.
func RefitVideo(ctx context.Context, o RefitOptions) (Artifact, error) {
	if strings.TrimSpace(o.Filter) == "" {
		return Artifact{}, fmt.Errorf("refit: no filter to apply")
	}
	if o.Output == "" {
		return Artifact{}, fmt.Errorf("refit: no output path")
	}
	if err := CheckFFmpeg(o.Bin); err != nil {
		return Artifact{}, err
	}
	bin := o.Bin
	if bin == "" {
		bin = "ffmpeg"
	}

	start := time.Now()
	proc, err := procutil.Start(ctx, procutil.Options{
		Name: "ffmpeg", Bin: bin, Args: RefitArgs(o), Env: o.Env, Logger: o.Logger,
	})
	if err != nil {
		return Artifact{}, err
	}
	if err := proc.Wait(); err != nil {
		return Artifact{}, fmt.Errorf("fitting %s to %dx%d: %w\n%s",
			filepath.Base(o.Input), o.Width, o.Height, err, proc.Tail())
	}
	st, err := os.Stat(o.Output)
	if err != nil {
		return Artifact{}, fmt.Errorf("ffmpeg produced no fitted clip: %w", err)
	}
	o.Logger.Info().
		Str("path", o.Output).Str("filter", o.Filter).
		Int("width", o.Width).Int("height", o.Height).
		Int64("bytes", st.Size()).Dur("elapsed", time.Since(start)).
		Msg("fitted a clip to the platform's frame")

	return Artifact{
		Kind:        KindVideo,
		ContentType: VideoContentType,
		Path:        o.Output,
		Size:        st.Size(),
		Width:       o.Width,
		Height:      o.Height,
	}, nil
}

// RefitArgs is the command line RefitVideo runs, exported so a test can assert
// it without running ffmpeg.
func RefitArgs(o RefitOptions) []string {
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", o.Input,
		"-vf", o.Filter,
	}
	args = append(args, presetArgs(o.Preset)...)
	// The audio stream as it stands. -c:a copy on a file with no audio is a
	// no-op rather than a failure.
	args = append(args, "-c:a", "copy")
	args = append(args, o.ExtraArgs...)
	return append(args, o.Output)
}

// PosterOptions describes extracting a still from a clip.
type PosterOptions struct {
	// Input is the clip to read.
	Input string
	// Output is the JPEG to write.
	Output string
	// Bin is the ffmpeg executable.
	Bin string
	// Env replaces ffmpeg's environment when non-nil.
	Env    []string
	Logger zerolog.Logger
}

// ExtractPoster pulls the first frame out of a clip.
//
// Reddit will not take a video without a poster image, and a clip crier was
// handed rather than made has no frame 0 lying around to encode. Rather than
// refuse the combination, the frame is taken out of the file — which is a
// thing ffmpeg does in milliseconds and nobody should have to do by hand.
func ExtractPoster(ctx context.Context, o PosterOptions) (Artifact, error) {
	if err := CheckFFmpeg(o.Bin); err != nil {
		return Artifact{}, err
	}
	bin := o.Bin
	if bin == "" {
		bin = "ffmpeg"
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", o.Input,
		"-frames:v", "1",
		"-f", "image2",
		o.Output,
	}
	proc, err := procutil.Start(ctx, procutil.Options{
		Name: "ffmpeg", Bin: bin, Args: args, Env: o.Env, Logger: o.Logger,
	})
	if err != nil {
		return Artifact{}, err
	}
	if err := proc.Wait(); err != nil {
		return Artifact{}, fmt.Errorf("extracting a poster frame from %s: %w\n%s",
			filepath.Base(o.Input), err, proc.Tail())
	}
	st, err := os.Stat(o.Output)
	if err != nil {
		return Artifact{}, fmt.Errorf("ffmpeg produced no poster frame: %w", err)
	}
	art := Artifact{
		Kind:        KindImage,
		Format:      config.JPEG,
		ContentType: config.JPEG.ContentType(),
		Path:        o.Output,
		Size:        st.Size(),
	}
	if cfg, err := imageConfigOf(o.Output); err == nil {
		art.Width, art.Height = cfg.Width, cfg.Height
	}
	o.Logger.Debug().Str("poster", o.Output).Int64("bytes", art.Size).
		Msg("extracted a poster frame from the clip")
	return art, nil
}

// imageConfigOf reads a file's dimensions without decoding it.
func imageConfigOf(path string) (image.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return image.Config{}, err
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	return cfg, err
}

// IsGIF reports whether a render.video.format names an animation.
func IsGIF(format string) bool {
	return strings.EqualFold(strings.TrimSpace(format), "gif")
}

// VideoExt is the file extension a format writes.
func VideoExt(format string) string {
	if IsGIF(format) {
		return ".gif"
	}
	return ".mp4"
}

// writeFrames renders and streams every frame.
func writeFrames(ctx context.Context, o VideoOptions, proc *procutil.Process, frame FrameFunc, every int) error {
	bg := o.Background
	if bg == nil {
		bg = color.Black
	}
	stdin := proc.Stdin()
	want := o.Width * o.Height * 4

	for i := 0; i < o.Frames; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		img, err := frame(ctx, i)
		if err != nil {
			return fmt.Errorf("rendering frame %d: %w", i, err)
		}
		if img == nil {
			return fmt.Errorf("rendering frame %d: no image", i)
		}
		b := img.Bounds()
		if b.Dx() != o.Width || b.Dy() != o.Height {
			return fmt.Errorf("frame %d is %dx%d, but the video is %dx%d; every frame has to be the same size",
				i, b.Dx(), b.Dy(), o.Width, o.Height)
		}
		// ffmpeg's rawvideo rgba is straight alpha, and a rendered frame is
		// premultiplied, so the frame is made opaque first. Video has no alpha
		// anyway, and the two agree once alpha is 255.
		flat := raster.Flatten(img, bg)
		if len(flat.Pix) != want {
			return fmt.Errorf("frame %d has %d bytes, want %d", i, len(flat.Pix), want)
		}
		if _, err := stdin.Write(flat.Pix); err != nil {
			// A broken pipe means ffmpeg died; its own error is the useful one.
			return fmt.Errorf("writing frame %d to ffmpeg: %w\n%s", i, err, proc.Tail())
		}
		if (i+1)%every == 0 {
			o.Logger.Debug().Int("frame", i+1).Int("frames", o.Frames).Msg("rendering video")
		}
	}
	return nil
}

// FFmpegArgs builds the command line. It is exported so the tests can assert
// the arguments without running ffmpeg.
func FFmpegArgs(o VideoOptions) []string {
	fps := o.FPS
	if fps <= 0 {
		fps = 30
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "rawvideo",
		"-pix_fmt", "rgba",
		"-s", strconv.Itoa(o.Width) + "x" + strconv.Itoa(o.Height),
		"-r", strconv.Itoa(fps),
		"-i", "-",
	}
	if IsGIF(o.Format) {
		// A GIF has a 256 colour palette, and ffmpeg's default one is built
		// from a fixed table that turns a gradient into bands. palettegen
		// derives the palette from this clip's own frames and paletteuse
		// applies it, in one pass: the stream is split, one branch measures
		// and the other waits for the answer.
		//
		// Audio is dropped rather than refused: a GIF has none, and a
		// configuration that names an audio file is asking for something the
		// format cannot carry.
		const palette = "split[s0][s1];[s0]palettegen=stats_mode=diff[p];[s1][p]paletteuse=dither=bayer:bayer_scale=3"
		args = append(args, "-vf", chainFilters(o.FitFilter, palette), "-loop", "0")
		args = append(args, o.ExtraArgs...)
		return append(args, o.Output)
	}
	if o.Audio != "" {
		args = append(args, "-i", o.Audio)
	}
	args = append(args, presetArgs(o.Preset)...)
	if o.Audio != "" {
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-shortest")
	}
	// One -vf, whatever wants a place in it. ffmpeg keeps only the last one it
	// is given, so a second flag would silently discard the first — which is
	// how a fitted GIF would have come out with the default palette.
	even := ""
	if needsEvenSize(o.Preset) && (o.Width%2 != 0 || o.Height%2 != 0) {
		// yuv420p subsamples chroma, so an odd dimension has nowhere to go.
		even = "scale=trunc(iw/2)*2:trunc(ih/2)*2"
	}
	if chain := chainFilters(o.FitFilter, even); chain != "" {
		args = append(args, "-vf", chain)
	}
	args = append(args, o.ExtraArgs...)
	return append(args, o.Output)
}

// chainFilters joins filter expressions with commas, dropping the empty ones.
func chainFilters(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, ",")
}

// presetArgs is the codec half of the command line.
//
// The h264 default is the one every platform accepts, and +faststart puts the
// index at the front of the file — Instagram rejects a video without it, and
// nothing else minds.
func presetArgs(preset string) []string {
	switch preset {
	case "", "h264":
		return []string{"-c:v", "libx264", "-pix_fmt", "yuv420p", "-movflags", "+faststart"}
	case "h265":
		return []string{"-c:v", "libx265", "-pix_fmt", "yuv420p", "-tag:v", "hvc1", "-movflags", "+faststart"}
	case "vp9":
		return []string{"-c:v", "libvpx-vp9", "-pix_fmt", "yuv420p"}
	default: // "none": the caller supplies everything through ExtraArgs
		return nil
	}
}

func needsEvenSize(preset string) bool {
	switch preset {
	case "", "h264", "h265", "vp9":
		return true
	default:
		return false
	}
}

// FrameCount is how many frames a duration at a frame rate comes to, or the
// explicit count when one was given.
func FrameCount(frames, fps int, duration time.Duration) int {
	if frames > 0 {
		return frames
	}
	if fps <= 0 || duration <= 0 {
		return 0
	}
	n := int(duration.Seconds() * float64(fps))
	if n < 1 {
		n = 1
	}
	return n
}

// FrameVars are the values injected into the template for one frame, under the
// name "Video".
func FrameVars(index, frames, fps int) map[string]any {
	var progress float64
	if frames > 1 {
		progress = float64(index) / float64(frames-1)
	}
	seconds := 0.0
	if fps > 0 {
		seconds = float64(index) / float64(fps)
	}
	return map[string]any{
		"Frame":    index,
		"Frames":   frames,
		"Time":     seconds,
		"Progress": progress,
	}
}
