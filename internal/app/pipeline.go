package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/render"
	"github.com/yohimik/crier/internal/stage"
	"github.com/yohimik/crier/internal/template"
)

// CleanupTimeout is how long the deferred cleanup gets, on a context of its
// own so that a cancelled run still deletes what it uploaded.
const CleanupTimeout = 10 * time.Second

// Variant is one thing to render: a set of template overlays and a size.
//
// Platforms that agree on both share a variant and therefore share one render,
// which is what keeps "Instagram gets a story, Discord gets a card, everyone
// else gets the default" from laying the document out three times when two
// would do.
type Variant struct {
	Overlays []string
	Width    int
	Height   int
	// Platforms are the publishers this variant is rendered for. It is empty
	// for the base variant of `crier render`.
	Platforms []string
}

// Key identifies a variant. Two platforms with the same overlays and size have
// the same key and are rendered once.
func (v Variant) Key() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%v|%d|%d", v.Overlays, v.Width, v.Height)))
	return hex.EncodeToString(sum[:8])
}

// Name is a short label for logs and file names.
func (v Variant) Name() string {
	if len(v.Platforms) == 0 {
		return "base"
	}
	return strings.Join(v.Platforms, "-")
}

// Pipeline runs the whole job: template, render, encode, stage, publish,
// report, clean up.
type Pipeline struct {
	cfg    *config.Config
	log    zerolog.Logger
	client *httpx.Client
	engine *template.Engine
	fonts  *render.Fonts

	stdin io.Reader
	dir   string

	// template is the layout this run draws, chosen once from render.pool when
	// there is one, so every variant and every video frame uses the same one.
	template string

	cleanups []func(context.Context) error
}

// PipelineOptions configures NewPipeline.
type PipelineOptions struct {
	Config *config.Config
	Logger zerolog.Logger
	Client *httpx.Client
	Stdin  io.Reader
	// Dir is where artifacts are written. Empty makes a temporary one that is
	// removed on Cleanup.
	Dir string
}

// NewPipeline builds the pipeline and its font configuration.
func NewPipeline(o PipelineOptions) (*Pipeline, error) {
	rnd := template.NewRand(int64(o.Config.Render.Seed))
	// The seed is always logged: it is the whole of what makes a run people
	// liked reproducible with --render-seed.
	o.Logger.Info().Int64("seed", rnd.Seed()).Msg("template randomisation seed")

	p := &Pipeline{
		cfg:    o.Config,
		log:    o.Logger,
		client: o.Client,
		engine: template.NewWithRand(rnd),
		stdin:  o.Stdin,
		dir:    o.Dir,
	}
	p.template = o.Config.Render.Template
	if picked, ok := p.engine.Pick(o.Config.Render.Pool); ok {
		p.template = picked
		o.Logger.Info().Str("template", picked).Int("pool", len(o.Config.Render.Pool)).
			Msg("picked a template from the pool")
	}
	if p.stdin == nil {
		p.stdin = os.Stdin
	}
	if p.dir == "" {
		dir, err := os.MkdirTemp("", "crier-")
		if err != nil {
			return nil, fail(ExitRender, fmt.Errorf("creating a working directory: %w", err))
		}
		p.dir = dir
		p.onCleanup(func(context.Context) error { return os.RemoveAll(dir) })
	}

	fonts, err := render.NewFonts(render.FontOptions{
		Hermetic: o.Config.Render.HermeticFonts,
		Dirs:     o.Config.Render.FontsDir,
		Logger:   o.Logger,
	})
	if err != nil {
		return nil, fail(ExitRender, err)
	}
	p.fonts = fonts
	return p, nil
}

func (p *Pipeline) onCleanup(f func(context.Context) error) {
	p.cleanups = append(p.cleanups, f)
}

// Cleanup releases everything the run acquired, newest first.
//
// It runs on a context of its own: the point of cleaning up is that it happens
// even when the run was cancelled, and a cancelled context would cancel the
// delete as well.
func (p *Pipeline) Cleanup(parent context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), CleanupTimeout)
	defer cancel()
	for i := len(p.cleanups) - 1; i >= 0; i-- {
		if err := p.cleanups[i](ctx); err != nil {
			p.log.Warn().Err(err).Msg("cleanup did not finish")
		}
	}
	p.cleanups = nil
}

// Engine is the run's template engine, shared so captions draw from the same
// seeded random source the layout does.
func (p *Pipeline) Engine() *template.Engine { return p.engine }

// Template is the layout this run renders: render.template, or the one picked
// out of render.pool.
func (p *Pipeline) Template() string { return p.template }

// Data loads the template's data document once, so it can be shared by the
// layout and by every caption.
func (p *Pipeline) Data() (any, error) {
	data, err := template.LoadData(p.cfg.Render.Data, p.stdin)
	if err != nil {
		return nil, fail(ExitRender, err)
	}
	return data, nil
}

// Artifacts is what one variant produced.
type Artifacts struct {
	Variant Variant
	// Images are the encoded stills, by format.
	Images map[config.Format]render.Artifact
	// Video is the encoded clip, when video rendering is on.
	Video *render.Artifact
	// Poster is the first frame as a still, for platforms that need one
	// alongside a video.
	Poster *render.Artifact

	// URL is where the primary artifact was staged, empty when nothing needed
	// staging.
	URL string
	// PosterURL is where the poster was staged.
	PosterURL string
}

// Primary is the artifact a publisher posts: the video when there is one, and
// the preferred still otherwise.
func (a Artifacts) Primary(needs publish.Needs) (render.Artifact, error) {
	if a.Video != nil {
		// The artifact's own kind, not KindVideo: a GIF lives in this field
		// too, and four platforms take a video and not an animation. Asking
		// the wrong question here would upload a GIF to Instagram as if it
		// were an MP4.
		if !needs.Accepts(a.Video.Kind) {
			return render.Artifact{}, fmt.Errorf("this platform does not take %s", a.Video.Kind)
		}
		return *a.Video, nil
	}
	art, ok := needs.Prefers(a.Images)
	if !ok {
		return render.Artifact{}, fmt.Errorf("none of the encoded formats is one this platform accepts")
	}
	return art, nil
}

// Variants groups the enabled platforms by what they need rendered.
func Variants(cfg *config.Config, platforms []string) []Variant {
	byKey := map[string]*Variant{}
	var order []string

	for _, name := range platforms {
		l := config.LayoutOf(&cfg.Publish, name)
		v := Variant{
			Overlays: append(append([]string(nil), cfg.Render.Overlays...), overlayOf(l)...),
			Width:    dimensionOr(widthOf(l), cfg.Render.Width),
			Height:   dimensionOr(heightOf(l), cfg.Render.Height),
		}
		key := v.Key()
		if existing, ok := byKey[key]; ok {
			existing.Platforms = append(existing.Platforms, name)
			continue
		}
		v.Platforms = []string{name}
		byKey[key] = &v
		order = append(order, key)
	}

	out := make([]Variant, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	return out
}

func overlayOf(l *config.Layout) []string {
	if l == nil {
		return nil
	}
	return l.Overlay
}

func widthOf(l *config.Layout) int {
	if l == nil {
		return 0
	}
	return l.Width
}

func heightOf(l *config.Layout) int {
	if l == nil {
		return 0
	}
	return l.Height
}

func dimensionOr(override, fallback int) int {
	if override > 0 {
		return override
	}
	return fallback
}

// BaseVariant is what `crier render` produces when no platform is named.
func BaseVariant(cfg *config.Config) Variant {
	return Variant{
		Overlays: append([]string(nil), cfg.Render.Overlays...),
		Width:    cfg.Render.Width,
		Height:   cfg.Render.Height,
	}
}

// Render lays out one variant and encodes it into the requested formats.
//
// Or does neither: a run that was given the file to publish, or the frames to
// encode, enters the pipeline further along. The three paths converge on the
// same Artifacts, so everything downstream — staging, format negotiation, the
// fan-out — is unaware of which one ran.
func (p *Pipeline) Render(ctx context.Context, v Variant, data any, formats []config.Format) (Artifacts, error) {
	mode, err := ModeOf(p.cfg)
	if err != nil {
		return Artifacts{}, err
	}
	switch mode {
	case ModePublishInput:
		return p.fromInput(formats)
	case ModeEncodeFrames:
		return p.fromFrames(ctx, v)
	}

	out := Artifacts{Variant: v, Images: map[config.Format]render.Artifact{}}

	if p.cfg.Render.Video.Enabled {
		video, poster, err := p.renderVideo(ctx, v, data)
		if err != nil {
			return out, err
		}
		out.Video, out.Poster = video, poster
		return out, nil
	}

	img, err := p.renderFrame(ctx, v, data, nil)
	if err != nil {
		return out, err
	}
	enc := p.encoder()
	for _, format := range formats {
		art, err := enc.Encode(img, format, v.Key())
		if err != nil {
			return out, fail(ExitRender, err)
		}
		out.Images[format] = art
		p.log.Debug().Str("variant", v.Name()).Str("format", string(format)).
			Str("path", art.Path).Int64("bytes", art.Size).Msg("encoded an image")
	}
	return out, nil
}

// PosterFor produces the still that goes alongside a clip crier was handed.
//
// A rendered clip has a frame 0 to encode; one taken from disk does not, so
// the frame is pulled out of the file. Reddit is the only platform that
// insists on a poster, and refusing the whole combination for want of one
// frame would be a poor answer when ffmpeg is right there.
func (p *Pipeline) PosterFor(ctx context.Context, clip render.Artifact) (*render.Artifact, error) {
	art, err := render.ExtractPoster(ctx, render.PosterOptions{
		Input:  clip.Path,
		Output: filepath.Join(p.dir, "input-poster.jpg"),
		Bin:    p.cfg.Render.Video.FFmpegBin,
		Logger: p.log,
	})
	if err != nil {
		return nil, fail(ExitRender, err)
	}
	return &art, nil
}

// fromInput publishes a file that already exists.
//
// The only work is transcoding: a PNG aimed at Instagram has to become a JPEG,
// because Instagram will not fetch a PNG. A clip is passed through untouched —
// re-encoding somebody's video to satisfy a format preference would be a
// surprising thing for a publish command to do.
func (p *Pipeline) fromInput(formats []config.Format) (Artifacts, error) {
	art, err := LoadInput(p.cfg.Publish.Input)
	if err != nil {
		return Artifacts{}, err
	}
	out := Artifacts{
		Variant: Variant{Width: art.Width, Height: art.Height},
		Images:  map[config.Format]render.Artifact{},
	}
	p.log.Info().Str("file", art.Path).Str("kind", string(art.Kind)).
		Int64("bytes", art.Size).Int("width", art.Width).Int("height", art.Height).
		Msg("publishing an existing file")

	if art.Kind != render.KindImage {
		out.Video = &art
		return out, nil
	}

	out.Images[art.Format] = art
	img, err := decodeFrame(art.Path)
	if err != nil {
		return out, fail(ExitRender, err)
	}
	enc := p.encoder()
	for _, format := range formats {
		if _, have := out.Images[format]; have {
			continue
		}
		transcoded, err := enc.Encode(img, format, "input")
		if err != nil {
			return out, fail(ExitRender, err)
		}
		out.Images[format] = transcoded
		p.log.Info().Str("from", string(art.Format)).Str("to", string(format)).
			Str("path", transcoded.Path).Msg("transcoded the input for a platform that needs it")
	}
	return out, nil
}

// fromFrames encodes images that already exist.
//
// The same ffmpeg pipeline the rendered path uses, fed from a directory rather
// than from the renderer, which is what lets frames made anywhere else become
// a crier post.
func (p *Pipeline) fromFrames(ctx context.Context, v Variant) (Artifacts, error) {
	vid := &p.cfg.Render.Video
	files, err := FrameFiles(vid.FramesInput)
	if err != nil {
		return Artifacts{}, err
	}
	if err := render.CheckFFmpeg(vid.FFmpegBin); err != nil {
		return Artifacts{}, fail(ExitRender, err)
	}

	reader, first, err := newFrameReader(files)
	if err != nil {
		return Artifacts{}, fail(ExitRender, err)
	}
	p.log.Info().Int("frames", len(files)).
		Int("width", reader.w).Int("height", reader.h).
		Str("first", filepath.Base(files[0])).Str("last", filepath.Base(files[len(files)-1])).
		Msg("encoding frames from disk")

	out := Artifacts{Variant: v, Images: map[config.Format]render.Artifact{}}

	// The first frame doubles as the poster, as it does for a rendered clip.
	poster, err := p.encoder().Encode(first, config.JPEG, "frames-poster")
	if err != nil {
		return out, fail(ExitRender, err)
	}

	bg, _ := config.ParseColor(p.cfg.Render.Background)
	art, err := render.EncodeVideo(ctx, render.VideoOptions{
		Output:     filepath.Join(p.dir, "frames"+render.VideoExt(vid.Format)),
		Frames:     len(files),
		FPS:        vid.FPS,
		Width:      reader.w,
		Height:     reader.h,
		Bin:        vid.FFmpegBin,
		Preset:     vid.CodecPreset,
		Format:     vid.Format,
		ExtraArgs:  vid.FFmpegArgs,
		Audio:      vid.Audio,
		Background: bg,
		Logger:     p.log,
	}, reader.at)
	if err != nil {
		return out, fail(ExitRender, err)
	}
	out.Video, out.Poster = &art, &poster
	return out, nil
}

func (p *Pipeline) encoder() render.Encoder {
	bg, _ := config.ParseColor(p.cfg.Render.Background)
	return render.Encoder{Dir: p.dir, JPEGQuality: p.cfg.Render.JPEGQuality, Background: bg}
}

// renderFrame executes the template and lays the result out.
//
// frameVars is nil for a still and carries .Video for one frame of a clip.
func (p *Pipeline) renderFrame(ctx context.Context, v Variant, data any, frameVars map[string]any) (*image.RGBA, error) {
	r := &p.cfg.Render
	extra := map[string]any{}
	if frameVars != nil {
		extra["Video"] = frameVars
	}

	// The data document is loaded once and passed in, so a template rendered
	// ninety times for a video reads standard input once.
	html, err := p.execute(p.template, v.Overlays, data, extra)
	if err != nil {
		return nil, err
	}

	css, err := readAllFiles(r.CSS)
	if err != nil {
		return nil, fail(ExitRender, err)
	}
	bg, _ := config.ParseColor(r.Background)

	base, err := baseURL(r.BaseURL)
	if err != nil {
		return nil, fail(ExitConfig, err)
	}

	img, err := render.RenderOne(ctx, render.Options{
		HTML:        html,
		BaseURL:     base,
		Width:       v.Width,
		Height:      v.Height,
		Scale:       config.Float(r.Scale, 1),
		SuperSample: r.SuperSample,
		MediaType:   r.MediaType,
		Background:  bg,
		ExtraCSS:    css,
		Fonts:       p.fonts,
		Logger:      p.log,
	})
	if err != nil {
		return nil, fail(ExitRender, err)
	}
	return img, nil
}

// execute renders the template with the data already in hand.
func (p *Pipeline) execute(path string, overlays []string, data any, extra map[string]any) (string, error) {
	html, err := p.engine.RenderWith(template.Options{Path: path, Overlays: overlays, Extra: extra}, data)
	if err != nil {
		return "", fail(ExitRender, err)
	}
	return html, nil
}

// renderVideo renders every frame into ffmpeg and encodes a poster.
func (p *Pipeline) renderVideo(ctx context.Context, v Variant, data any) (*render.Artifact, *render.Artifact, error) {
	vid := &p.cfg.Render.Video
	frames := render.FrameCount(vid.Frames, vid.FPS, config.Duration(vid.Duration))
	if frames <= 0 {
		return nil, nil, failf(ExitConfig, "render.video needs either a duration or a frame count")
	}
	if err := render.CheckFFmpeg(vid.FFmpegBin); err != nil {
		return nil, nil, fail(ExitRender, err)
	}

	// The first frame decides the size and doubles as the poster.
	first, err := p.renderFrame(ctx, v, data, render.FrameVars(0, frames, vid.FPS))
	if err != nil {
		return nil, nil, err
	}
	bounds := first.Bounds()

	poster, err := p.encoder().Encode(first, config.JPEG, v.Key()+"-poster")
	if err != nil {
		return nil, nil, fail(ExitRender, err)
	}

	bg, _ := config.ParseColor(p.cfg.Render.Background)
	output := filepath.Join(p.dir, v.Key()+render.VideoExt(vid.Format))
	art, err := render.EncodeVideo(ctx, render.VideoOptions{
		Output:     output,
		Frames:     frames,
		FPS:        vid.FPS,
		Width:      bounds.Dx(),
		Height:     bounds.Dy(),
		Bin:        vid.FFmpegBin,
		Preset:     vid.CodecPreset,
		Format:     vid.Format,
		ExtraArgs:  vid.FFmpegArgs,
		Audio:      vid.Audio,
		Background: bg,
		Logger:     p.log,
	}, func(ctx context.Context, i int) (*image.RGBA, error) {
		if i == 0 {
			return first, nil
		}
		return p.renderFrame(ctx, v, data, render.FrameVars(i, frames, vid.FPS))
	})
	if err != nil {
		return nil, nil, fail(ExitRender, err)
	}
	return &art, &poster, nil
}

// baseURL turns render.base-url into something webrender can resolve against.
//
// An absolute URL is used as it is; anything else is a directory on this
// machine, which becomes a file URL — that is what lets a template say
// url("../fonts/poppins/Poppins-Bold.ttf") and mean the file next to it.
func baseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if u, err := url.Parse(value); err == nil && u.IsAbs() && u.Scheme != "" {
		return value, nil
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("render.base-url: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("render.base-url: %w", err)
	}
	if st.IsDir() && !strings.HasSuffix(abs, string(filepath.Separator)) {
		// Without the trailing separator a relative reference would resolve
		// from the parent directory.
		abs += string(filepath.Separator)
	}
	return "file://" + filepath.ToSlash(abs), nil
}

// readAllFiles reads the extra stylesheets.
func readAllFiles(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		out = append(out, string(body))
	}
	return out, nil
}

// FormatsFor is the minimal set of image formats a variant has to be encoded
// into: the configured one, plus whatever a publisher insists on.
//
// Instagram takes JPEG and nothing else, so a run configured for PNG still
// produces a JPEG — for Instagram alone, rather than for everyone.
func FormatsFor(cfg *config.Config, publishers []publish.Publisher) []config.Format {
	base, err := config.ParseFormat(cfg.Render.Format)
	if err != nil {
		base = config.PNG
	}
	have := map[config.Format]bool{base: true}
	out := []config.Format{base}

	for _, pub := range publishers {
		needs := pub.Needs()
		if len(needs.Formats) == 0 {
			continue
		}
		satisfied := false
		for _, f := range needs.Formats {
			if have[f] {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}
		f := needs.Formats[0]
		have[f] = true
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Stage makes a variant's artifacts reachable, when any of its publishers
// needs a URL.
func (p *Pipeline) Stage(ctx context.Context, stager stage.Stager, a *Artifacts, needsURL bool, needsPoster bool) error {
	if !needsURL {
		return nil
	}
	primary, err := stagedAsset(a)
	if err != nil {
		return fail(ExitStaging, err)
	}
	obj, err := stager.Stage(ctx, primary)
	if err != nil {
		return fail(ExitStaging, err)
	}
	a.URL = obj.URL
	p.onCleanup(obj.Remove)

	if needsPoster && a.Poster != nil {
		posterObj, err := stager.Stage(ctx, stage.Asset{
			Path:        a.Poster.Path,
			ContentType: a.Poster.ContentType,
			Name:        filepath.Base(a.Poster.Path),
			Size:        a.Poster.Size,
		})
		if err != nil {
			return fail(ExitStaging, err)
		}
		a.PosterURL = posterObj.URL
		p.onCleanup(posterObj.Remove)
	}
	return nil
}

// stagedAsset picks what a variant publishes by URL.
func stagedAsset(a *Artifacts) (stage.Asset, error) {
	if a.Video != nil {
		return stage.Asset{
			Path: a.Video.Path, ContentType: a.Video.ContentType,
			Name: filepath.Base(a.Video.Path), Size: a.Video.Size,
		}, nil
	}
	// JPEG is what the URL-fetching platforms want; PNG is the fallback for a
	// run that produced only that.
	for _, f := range []config.Format{config.JPEG, config.PNG} {
		if art, ok := a.Images[f]; ok {
			return stage.Asset{
				Path: art.Path, ContentType: art.ContentType,
				Name: filepath.Base(art.Path), Size: art.Size,
			}, nil
		}
	}
	return stage.Asset{}, fmt.Errorf("nothing was encoded to stage")
}
