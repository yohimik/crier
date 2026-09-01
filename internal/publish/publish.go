// Package publish posts a rendered artifact to social platforms.
//
// Every platform is one Publisher, in one file, behind the same three-method
// interface. What differs between them — whether they take bytes or a URL,
// which image formats they accept, whether they can take a video — is declared
// in Needs rather than discovered at the API, so a configuration that cannot
// work is refused before anything is uploaded.
package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// Needs is what a publisher requires of the pipeline.
type Needs struct {
	// URL says the platform will not take bytes: it fetches the media from a
	// public URL of its own accord. Instagram and TikTok's pull mode are the
	// two, and they are the reason internal/stage exists.
	URL bool
	// Formats are the image formats the platform accepts, in order of
	// preference. The pipeline encodes the union of what every enabled
	// publisher asks for.
	Formats []config.Format
	// Kinds are the artifact kinds the platform can post.
	Kinds []render.Kind
}

// Accepts reports whether the publisher can post an artifact of this kind.
func (n Needs) Accepts(kind render.Kind) bool {
	for _, k := range n.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Prefers picks the format to hand a publisher from what was encoded.
func (n Needs) Prefers(available map[config.Format]render.Artifact) (render.Artifact, bool) {
	for _, f := range n.Formats {
		if a, ok := available[f]; ok {
			return a, true
		}
	}
	return render.Artifact{}, false
}

// Input is one post.
type Input struct {
	// Artifact is the file to publish.
	Artifact render.Artifact
	// URL is where the artifact can be fetched, set when Needs.URL.
	URL string
	// Caption is the already-rendered post text.
	Caption string
	// Poster is a still image that goes with a video, for the platforms that
	// insist on one.
	Poster *render.Artifact
	// PosterURL is where the poster can be fetched.
	PosterURL string
}

// Result is what a platform gave back.
type Result struct {
	// ID is the platform's own identifier for the post.
	ID string
	// URL is a link to the post, when the platform returns one.
	URL string
	// Extra carries anything else worth reporting, such as a media id.
	Extra map[string]string
}

// Publisher posts to one platform.
type Publisher interface {
	// Name is the platform's configuration key: "instagram", "reddit".
	Name() string
	// Needs describes what the publisher requires.
	Needs() Needs
	// Publish posts the input and returns what the platform said.
	Publish(ctx context.Context, in Input) (Result, error)
}

// Deps are what every publisher is built with.
type Deps struct {
	// Client is the shared HTTP client. Required.
	Client *httpx.Client
	// Logger records the publisher's own steps.
	Logger zerolog.Logger
	// UserAgent identifies crier to the platforms that insist on one.
	UserAgent string
}

// constructor builds one publisher from the configuration.
type constructor func(cfg *config.Config, d Deps) (Publisher, error)

// registry maps a platform name to its constructor. A platform that is not in
// here does not exist as far as the rest of the program is concerned.
var registry = map[string]constructor{
	"instagram": newInstagram,
	"facebook":  newFacebook,
	"tiktok":    newTikTok,
	"telegram":  newTelegram,
	"x":         newX,
	"mastodon":  newMastodon,
	"discord":   newDiscord,
	"linkedin":  newLinkedIn,
	"reddit":    newReddit,
}

// Names are every platform crier knows, sorted.
func Names() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Enabled reports which platforms the configuration turns on.
func Enabled(cfg *config.Config) []string {
	var out []string
	for _, name := range config.Platforms {
		if enabledIn(cfg, name) {
			out = append(out, name)
		}
	}
	return out
}

func enabledIn(cfg *config.Config, name string) bool {
	p := &cfg.Publish
	switch name {
	case "instagram":
		return p.Instagram.Enabled
	case "facebook":
		return p.Facebook.Enabled
	case "tiktok":
		return p.TikTok.Enabled
	case "telegram":
		return p.Telegram.Enabled
	case "x":
		return p.X.Enabled
	case "mastodon":
		return p.Mastodon.Enabled
	case "discord":
		return p.Discord.Enabled
	case "linkedin":
		return p.LinkedIn.Enabled
	case "reddit":
		return p.Reddit.Enabled
	default:
		return false
	}
}

// Build constructs every enabled publisher.
//
// Each constructor validates its own configuration, so a missing token is an
// error here — before a single request goes out, and before the platforms that
// were configured correctly have posted anything.
func Build(cfg *config.Config, d Deps) ([]Publisher, error) {
	if d.Client == nil {
		return nil, errors.New("publish: no HTTP client")
	}
	var (
		out  []Publisher
		errs []error
	)
	for _, name := range Enabled(cfg) {
		make, ok := registry[name]
		if !ok {
			errs = append(errs, fmt.Errorf("unknown platform %q", name))
			continue
		}
		p, err := make(cfg, d)
		if err != nil {
			errs = append(errs, fmt.Errorf("publish.%s: %w", name, err))
			continue
		}
		out = append(out, p)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Job is one publisher and the input it was given.
type Job struct {
	Publisher Publisher
	Input     Input
}

// Outcome is what one job came to.
type Outcome struct {
	Platform string        `json:"platform"`
	OK       bool          `json:"ok"`
	ID       string        `json:"id,omitempty"`
	URL      string        `json:"url,omitempty"`
	Error    string        `json:"error,omitempty"`
	Elapsed  time.Duration `json:"-"`

	Extra map[string]string `json:"extra,omitempty"`
	// Err is the failure itself, for a caller that wants to inspect it.
	Err error `json:"-"`
}

// Report is every outcome of one run, in platform order.
type Report struct {
	Outcomes []Outcome
}

// Succeeded counts the platforms that took the post.
func (r Report) Succeeded() int {
	n := 0
	for _, o := range r.Outcomes {
		if o.OK {
			n++
		}
	}
	return n
}

// Failed counts the platforms that did not.
func (r Report) Failed() int { return len(r.Outcomes) - r.Succeeded() }

// Err joins every failure, or is nil when everything worked.
func (r Report) Err() error {
	var errs []error
	for _, o := range r.Outcomes {
		if o.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", o.Platform, o.Err))
		}
	}
	return errors.Join(errs...)
}

// RunAll publishes to every platform, a bounded number at a time.
//
// One platform's failure never cancels another's: the whole point of posting
// to eight places is that seven of them still get the post when the eighth is
// down. The context is passed through unchanged, so a cancelled run does stop
// everything.
func RunAll(ctx context.Context, jobs []Job, concurrency int, log zerolog.Logger) Report {
	if concurrency < 1 {
		concurrency = 1
	}
	outcomes := make([]Outcome, len(jobs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, job := range jobs {
		wg.Add(1)
		go func(i int, job Job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outcomes[i] = run(ctx, job, log)
		}(i, job)
	}
	wg.Wait()

	sort.SliceStable(outcomes, func(i, j int) bool { return outcomes[i].Platform < outcomes[j].Platform })
	return Report{Outcomes: outcomes}
}

// run publishes one job, turning a panic in a publisher into a failure rather
// than taking the whole run down with it.
func run(ctx context.Context, job Job, log zerolog.Logger) (out Outcome) {
	name := job.Publisher.Name()
	out = Outcome{Platform: name}
	start := time.Now()

	defer func() {
		out.Elapsed = time.Since(start)
		if r := recover(); r != nil {
			out.OK = false
			out.Err = fmt.Errorf("the %s publisher panicked: %v", name, r)
			out.Error = out.Err.Error()
		}
		if out.OK {
			ev := log.Info().Str("platform", name).Dur("elapsed", out.Elapsed)
			if out.ID != "" {
				ev = ev.Str("id", out.ID)
			}
			if out.URL != "" {
				ev = ev.Str("url", out.URL)
			}
			ev.Msg("published")
			return
		}
		log.Error().Str("platform", name).Dur("elapsed", out.Elapsed).Err(out.Err).Msg("publishing failed")
	}()

	log.Debug().Str("platform", name).Str("file", job.Input.Artifact.Path).Msg("publishing")
	res, err := job.Publisher.Publish(ctx, job.Input)
	if err != nil {
		out.Err = err
		out.Error = err.Error()
		return out
	}
	out.OK = true
	out.ID = res.ID
	out.URL = res.URL
	out.Extra = res.Extra
	return out
}

// --- shared helpers --------------------------------------------------------

// firstNonEmpty is the fallback chain a per-platform setting resolves through.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// require reports a missing configuration key by name, so the message says
// what to set rather than what went wrong.
func require(value, key string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required (set it in the config file, %s or --%s)",
			key, config.EnvName(key), config.FlagName(key))
	}
	return nil
}

// checkSize refuses an upload the platform is known to reject, before it is
// sent. A 30 megabyte video and a slow connection is a long wait for a
// rejection crier could have predicted.
func checkSize(a render.Artifact, limit int64, platform string) error {
	if limit <= 0 || a.Size <= limit {
		return nil
	}
	return fmt.Errorf("%s is %s, which is over %s's limit of %s",
		a.Path, humanSize(a.Size), platform, humanSize(limit))
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fkB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// idempotencyKey is a stable name for one post, for the platforms that offer a
// duplicate-suppression header. The same artifact and caption produce the same
// key, so a retry crier did not intend cannot become a second post.
func idempotencyKey(in Input) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", in.Artifact.Path, in.Artifact.Size, in.Caption)))
	return hex.EncodeToString(sum[:16])
}

// decodeJSON reads a JSON body into out, ignoring an empty one.
func decodeJSON(r io.Reader, out any) error {
	return json.NewDecoder(r).Decode(out)
}

// imageFormats is what most platforms accept.
var imageFormats = []config.Format{config.JPEG, config.PNG}

// imageOnly is the Kinds of a platform that cannot take video.
var imageOnly = []render.Kind{render.KindImage}

// imageAndVideo is the Kinds of a platform that can take either.
var imageAndVideo = []render.Kind{render.KindImage, render.KindVideo}
