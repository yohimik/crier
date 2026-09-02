package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
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
	// Width and Height are what the layout is drawn at.
	Width  int
	Height int
	// Fit is how the drawn image is then made to match the platform's frame.
	// FitNone leaves it alone, which is what every platform did before this
	// existed.
	Fit config.Fit
	// FitWidth and FitHeight are that frame. They are the platform's own
	// width and height, which is why a fitted variant draws at the master size
	// instead: a story is the square card resampled, not the card reflowed
	// into a tall box.
	FitWidth  int
	FitHeight int
	// FitBackground is the colour behind a contain letterbox.
	FitBackground string
	// Platforms are the publishers this variant is rendered for. It is empty
	// for the base variant of `crier render`.
	Platforms []string
}

// Key identifies a variant. Two platforms that would produce the same file
// share a key and are rendered and encoded once.
//
// The fit is part of the key because it is part of the file: two platforms
// agreeing about the layout and disagreeing about the frame are two pictures,
// however much of the work they share.
func (v Variant) Key() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%v|%d|%d|%s|%d|%d|%s",
		v.Overlays, v.Width, v.Height, v.Fit, v.FitWidth, v.FitHeight, v.FitBackground)))
	return hex.EncodeToString(sum[:8])
}

// Fits reports whether this variant reshapes what it drew.
func (v Variant) Fits() bool {
	return v.Fit != "" && v.Fit != config.FitNone && v.FitWidth > 0 && v.FitHeight > 0
}

// OutWidth and OutHeight are the size of what a platform receives, which is
// the frame when there is one and the render otherwise.
func (v Variant) OutWidth() int {
	if v.Fits() {
		return v.FitWidth
	}
	return v.Width
}

// OutHeight is OutWidth's other half.
func (v Variant) OutHeight() int {
	if v.Fits() {
		return v.FitHeight
	}
	return v.Height
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

	stdin   io.Reader
	environ []string
	dir     string

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
	// Environ is where an `env:` data source reads from. Nil means the process
	// environment.
	Environ []string
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
		cfg:     o.Config,
		log:     o.Logger,
		client:  o.Client,
		engine:  template.NewWithRand(rnd),
		stdin:   o.Stdin,
		environ: o.Environ,
		dir:     o.Dir,
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
	spec := p.cfg.Render.Data
	data, err := template.LoadData(spec, p.stdin, p.environ)
	if err != nil {
		return nil, fail(ExitRender, err)
	}

	// A prefix that matched nothing is almost always a typo or a variable that
	// did not survive the shell, and the render that follows would be a card
	// full of blanks with nothing anywhere saying why.
	if prefix, ok := template.EnvPrefixOf(spec); ok {
		names := template.EnvNames(prefix, p.environ)
		if len(names) == 0 {
			p.log.Warn().Str("prefix", prefix).
				Msg("no environment variables carry this prefix; the template will render with no data")
		} else {
			p.log.Debug().Str("prefix", prefix).Strs("variables", names).
				Msg("read the template data from the environment")
		}
	}
	return data, nil
}

// Page is one laid-out page of a document, encoded into every format the run
// needs.
//
// A document that fits on one page has one of these, which is what every run
// was before pagination existed.
type Page struct {
	// Images are the encoded stills of this page, by format.
	Images map[config.Format]render.Artifact
	// URL is where this page was staged, empty when nothing needed staging.
	URL string
}

// Artifacts is what one variant produced.
type Artifacts struct {
	Variant Variant
	// Pages are the encoded stills, one per laid-out page, in page order.
	Pages []Page
	// Video is the encoded clip, when video rendering is on. A clip is never
	// paginated: it is one file however long it runs.
	Video *render.Artifact
	// Poster is the first frame as a still, for platforms that need one
	// alongside a video.
	Poster *render.Artifact

	// PosterURL is where the poster was staged.
	PosterURL string
}

// First is the first page, or an empty one when nothing was encoded.
//
// It is what the single-file paths — the render report, staging a clip, the
// dry-run plan — are asking for when they ask for "the" artifact.
func (a Artifacts) First() Page {
	if len(a.Pages) == 0 {
		return Page{Images: map[config.Format]render.Artifact{}}
	}
	return a.Pages[0]
}

// URL is where the first page was staged.
func (a Artifacts) URL() string { return a.First().URL }

// Primary is the artifact a publisher posts first: the video when there is one,
// and the first page's preferred still otherwise.
func (a Artifacts) Primary(needs publish.Needs) (render.Artifact, error) {
	arts, err := a.Sequence(needs)
	if err != nil {
		return render.Artifact{}, err
	}
	return arts[0], nil
}

// Sequence is every artifact a publisher posts, in page order.
//
// A clip is one artifact whatever the page count, because a clip is one file.
// Stills are one per page, and the order is the page order — the run's single
// ordered page list, which every platform receives identically.
func (a Artifacts) Sequence(needs publish.Needs) ([]render.Artifact, error) {
	if a.Video != nil {
		// The artifact's own kind, not KindVideo: a GIF lives in this field
		// too, and four platforms take a video and not an animation. Asking
		// the wrong question here would upload a GIF to Instagram as if it
		// were an MP4.
		if !needs.Accepts(a.Video.Kind) {
			return nil, fmt.Errorf("this platform does not take %s", a.Video.Kind)
		}
		return []render.Artifact{*a.Video}, nil
	}
	if len(a.Pages) == 0 {
		return nil, fmt.Errorf("nothing was encoded to publish")
	}
	if !needs.Accepts(render.KindImage) {
		return nil, fmt.Errorf("this platform does not take %s", render.KindImage)
	}
	out := make([]render.Artifact, 0, len(a.Pages))
	for i, page := range a.Pages {
		art, ok := needs.Prefers(page.Images)
		if !ok {
			return nil, fmt.Errorf("none of the encoded formats is one this platform accepts")
		}
		if art.Kind != render.KindImage {
			// A page list is images. Mixing kinds across pages would make a
			// carousel that some platforms take and others silently truncate.
			return nil, fmt.Errorf("page %d is %s; a paged post has to be images throughout",
				i+1, art.Kind)
		}
		out = append(out, art)
	}
	return out, nil
}

// URLs is where every page was staged, in page order.
func (a Artifacts) URLs() []string {
	out := make([]string, 0, len(a.Pages))
	for _, p := range a.Pages {
		out = append(out, p.URL)
	}
	return out
}

// Variants groups the enabled platforms by what they need rendered.
func Variants(cfg *config.Config, platforms []string) []Variant {
	byKey := map[string]*Variant{}
	var order []string

	for _, name := range platforms {
		l := config.LayoutOf(&cfg.Publish, name)
		fit, _ := config.ParseFit(fitOf(l))
		v := Variant{
			Overlays: append(append([]string(nil), cfg.Render.Overlays...), overlayOf(l)...),
			Width:    dimensionOr(widthOf(l), cfg.Render.Width),
			Height:   dimensionOr(heightOf(l), cfg.Render.Height),
		}
		if fit != config.FitNone && widthOf(l) > 0 && heightOf(l) > 0 {
			// The platform's dimensions become the frame, and the layout is
			// drawn at the master size instead. Reflowing a square card into a
			// story is a different picture; resampling it is the one asked for.
			v.Fit = fit
			v.FitWidth, v.FitHeight = widthOf(l), heightOf(l)
			v.FitBackground = fitBackgroundOf(l)
			v.Width, v.Height = cfg.Render.Width, cfg.Render.Height
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

func fitOf(l *config.Layout) string {
	if l == nil {
		return ""
	}
	return l.Fit
}

func fitBackgroundOf(l *config.Layout) string {
	if l == nil {
		return ""
	}
	return l.FitBackground
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

	out := Artifacts{Variant: v}

	if p.cfg.Render.Video.Enabled {
		video, poster, err := p.renderVideo(ctx, v, data)
		if err != nil {
			return out, err
		}
		out.Video, out.Poster = video, poster
		return out, nil
	}

	imgs, err := p.renderPages(ctx, v, data)
	if err != nil {
		return out, err
	}
	enc := p.encoder()
	for i, img := range imgs {
		// The fit is per page: every page is made to match the platform's
		// frame on its own, so a carousel is a set of pictures the same shape.
		img = p.applyFit(v, img)
		page := Page{Images: map[config.Format]render.Artifact{}}
		for _, format := range formats {
			art, err := enc.Encode(img, format, pageKey(v, i, len(imgs)))
			if err != nil {
				return out, fail(ExitRender, err)
			}
			page.Images[format] = art
			p.log.Debug().Str("variant", v.Name()).Str("format", string(format)).
				Int("page", i+1).Int("pages", len(imgs)).
				Str("path", art.Path).Int64("bytes", art.Size).Msg("encoded a page")
		}
		out.Pages = append(out.Pages, page)
	}
	if len(imgs) > 1 {
		p.log.Info().Str("variant", v.Name()).Int("pages", len(imgs)).
			Msg("the document laid out into several pages")
	}
	return out, nil
}

// pageKey names a page's encoded files. A single-page run keeps the name it
// always had, so nothing that reads a rendered file by name has to learn about
// pagination to keep working.
func pageKey(v Variant, i, total int) string {
	if total <= 1 {
		return v.Key()
	}
	return fmt.Sprintf("%s-p%02d", v.Key(), i+1)
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
	out := Artifacts{Variant: Variant{Width: art.Width, Height: art.Height}}
	page := Page{Images: map[config.Format]render.Artifact{}}
	p.log.Info().Str("file", art.Path).Str("kind", string(art.Kind)).
		Int64("bytes", art.Size).Int("width", art.Width).Int("height", art.Height).
		Msg("publishing an existing file")

	if art.Kind != render.KindImage {
		out.Video = &art
		return out, nil
	}

	page.Images[art.Format] = art
	img, err := decodeFrame(art.Path)
	if err != nil {
		return out, fail(ExitRender, err)
	}
	enc := p.encoder()
	for _, format := range formats {
		if _, have := page.Images[format]; have {
			continue
		}
		transcoded, err := enc.Encode(img, format, "input")
		if err != nil {
			return out, fail(ExitRender, err)
		}
		page.Images[format] = transcoded
		p.log.Info().Str("from", string(art.Format)).Str("to", string(format)).
			Str("path", transcoded.Path).Msg("transcoded the input for a platform that needs it")
	}
	out.Pages = []Page{page}
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

	out := Artifacts{Variant: v}

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

// applyFit reshapes a rendered image to the platform's frame.
//
// It runs after the layout and before the encoder, so every format a variant
// produces is the same picture — and so the file a platform receives is
// already the size it wanted, rather than something it will crop on its own
// servers without saying where.
func (p *Pipeline) applyFit(v Variant, img *image.RGBA) *image.RGBA {
	if !v.Fits() || img == nil {
		return img
	}
	bg, err := config.ParseColor(v.FitBackground)
	if err != nil {
		// Validation refused a colour this could not read, so reaching here
		// means the value came from somewhere validation does not see. White
		// is the documented default and a wrong background beats no picture.
		p.log.Warn().Str("colour", v.FitBackground).Err(err).
			Msg("could not read the fit background; using white")
		bg = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	b := img.Bounds()
	out := render.FitImage(img, v.FitWidth, v.FitHeight, v.Fit, bg)
	p.log.Debug().
		Str("variant", v.Name()).Str("fit", string(v.Fit)).
		Int("from_width", b.Dx()).Int("from_height", b.Dy()).
		Int("to_width", v.FitWidth).Int("to_height", v.FitHeight).
		Msg("fitted the render to the platform's frame")
	return out
}

func (p *Pipeline) encoder() render.Encoder {
	bg, _ := config.ParseColor(p.cfg.Render.Background)
	return render.Encoder{Dir: p.dir, JPEGQuality: p.cfg.Render.JPEGQuality, Background: bg}
}

// renderPages lays the document out and keeps every page it produced.
//
// This is the whole of pagination on the rendering side. Laying out was always
// producing pages; what changed is that crier keeps them all rather than
// refusing anything past the first.
func (p *Pipeline) renderPages(ctx context.Context, v Variant, data any) ([]*image.RGBA, error) {
	opts, err := p.renderOptions(v, data, nil)
	if err != nil {
		return nil, err
	}
	pages, err := render.Render(ctx, opts)
	if err != nil {
		return nil, fail(ExitRender, err)
	}
	// The ceiling is checked after the layout rather than before, because the
	// page count is not knowable until the content has flowed. A runaway loop
	// in a template is caught here rather than at the platform, which would
	// have taken the first ten and said nothing about the rest.
	if max := p.cfg.Render.PagesMax; max > 0 && len(pages) > max {
		return nil, failf(ExitRender,
			"the document laid out into %d pages and render.pages-max is %d; "+
				"shorten the content, raise render.pages-max (up to %d), or raise render.height",
			len(pages), max, config.MaxPages)
	}
	return pages, nil
}

// renderFrame executes the template and lays the result out as a single page.
//
// frameVars is nil for a still and carries .Video for one frame of a clip.
// Video is the one caller left: a clip's frames are each one page by
// definition, and a frame that overflowed would be a template bug rather than
// a document to paginate.
func (p *Pipeline) renderFrame(ctx context.Context, v Variant, data any, frameVars map[string]any) (*image.RGBA, error) {
	opts, err := p.renderOptions(v, data, frameVars)
	if err != nil {
		return nil, err
	}
	img, err := render.RenderOne(ctx, opts)
	if err != nil {
		return nil, fail(ExitRender, err)
	}
	return img, nil
}

// renderOptions assembles what one layout needs: the executed template, the
// extra stylesheets, the page size and the fonts.
func (p *Pipeline) renderOptions(v Variant, data any, frameVars map[string]any) (render.Options, error) {
	r := &p.cfg.Render
	extra := map[string]any{}
	if frameVars != nil {
		extra["Video"] = frameVars
	}

	// The data document is loaded once and passed in, so a template rendered
	// ninety times for a video reads standard input once.
	html, err := p.execute(p.template, v.Overlays, data, extra)
	if err != nil {
		return render.Options{}, err
	}

	css, err := readAllFiles(r.CSS)
	if err != nil {
		return render.Options{}, fail(ExitRender, err)
	}
	bg, _ := config.ParseColor(r.Background)

	base, err := baseURL(r.BaseURL)
	if err != nil {
		return render.Options{}, fail(ExitConfig, err)
	}

	return render.Options{
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
	}, nil
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

	// The poster is fitted like the clip, so the still a platform shows before
	// the video plays is the same shape as the video.
	poster, err := p.encoder().Encode(p.applyFit(v, first), config.JPEG, v.Key()+"-poster")
	if err != nil {
		return nil, nil, fail(ExitRender, err)
	}

	bg, _ := config.ParseColor(p.cfg.Render.Background)
	output := filepath.Join(p.dir, v.Key()+render.VideoExt(vid.Format))

	// The frames go to ffmpeg at the size they were drawn, and ffmpeg does the
	// fitting. Resampling every frame here and then handing ffmpeg the result
	// would be the same picture and a great deal slower — a filter graph is
	// what ffmpeg is for.
	fitFilter := ""
	if v.Fits() {
		fitFilter = render.FitFilter(v.FitWidth, v.FitHeight, v.Fit, v.FitBackground)
		p.log.Debug().Str("variant", v.Name()).Str("fit", string(v.Fit)).
			Str("filter", fitFilter).Msg("fitting the clip to the platform's frame")
	}

	art, err := render.EncodeVideo(ctx, render.VideoOptions{
		Output:     output,
		Frames:     frames,
		FPS:        vid.FPS,
		Width:      bounds.Dx(),
		Height:     bounds.Dy(),
		Bin:        vid.FFmpegBin,
		Preset:     vid.CodecPreset,
		Format:     vid.Format,
		FitFilter:  fitFilter,
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
	// The artifact reports the frame's size, not the drawn size: it is what
	// the file actually is, and what the report tells the operator.
	if v.Fits() {
		art.Width, art.Height = v.FitWidth, v.FitHeight
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
	// A clip is one file, so it stages once and lands on the first page. Stills
	// stage one page at a time: a platform that fetches by URL fetches every
	// page of a carousel, so every page needs an address of its own.
	if a.Video != nil {
		obj, err := stager.Stage(ctx, videoAsset(a.Video))
		if err != nil {
			return fail(ExitStaging, err)
		}
		a.Pages = []Page{{Images: map[config.Format]render.Artifact{}, URL: obj.URL}}
		p.onCleanup(obj.Remove)
	} else {
		for i := range a.Pages {
			asset, err := stagedAsset(a.Pages[i])
			if err != nil {
				return fail(ExitStaging, err)
			}
			obj, err := stager.Stage(ctx, asset)
			if err != nil {
				return fail(ExitStaging, fmt.Errorf("page %d: %w", i+1, err))
			}
			a.Pages[i].URL = obj.URL
			p.onCleanup(obj.Remove)
		}
	}

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

func videoAsset(a *render.Artifact) stage.Asset {
	return stage.Asset{
		Path: a.Path, ContentType: a.ContentType,
		Name: filepath.Base(a.Path), Size: a.Size,
	}
}

// stagedAsset picks what one page publishes by URL.
func stagedAsset(p Page) (stage.Asset, error) {
	// JPEG is what the URL-fetching platforms want; PNG is the fallback for a
	// run that produced only that.
	for _, f := range []config.Format{config.JPEG, config.PNG} {
		if art, ok := p.Images[f]; ok {
			return stage.Asset{
				Path: art.Path, ContentType: art.ContentType,
				Name: filepath.Base(art.Path), Size: art.Size,
			}, nil
		}
	}
	return stage.Asset{}, fmt.Errorf("nothing was encoded to stage")
}
