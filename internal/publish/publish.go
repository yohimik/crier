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
	// MaxAttachments is how many files the platform takes in one post: a
	// carousel of ten at Instagram, four at X, one wherever a post is one
	// picture. Zero means one.
	//
	// It is a cap, not a target. A page list longer than it becomes several
	// posts in a row rather than a truncated one.
	MaxAttachments int
}

// Capacity is MaxAttachments with the zero value read as one, which is what
// every platform was before carousels existed.
func (n Needs) Capacity() int {
	if n.MaxAttachments < 1 {
		return 1
	}
	return n.MaxAttachments
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
//
// A post carries one file or several. Artifact and URL are the first of them,
// which is the whole of the post for a platform that takes one file and the
// cover of a carousel for a platform that takes more.
type Input struct {
	// Artifact is the first file to publish.
	Artifact render.Artifact
	// URL is where that artifact can be fetched, set when Needs.URL.
	URL string
	// Artifacts is every file this post carries, in page order. It always has
	// at least one entry and its first is Artifact.
	Artifacts []render.Artifact
	// URLs is where each of those can be fetched, in the same order, set when
	// Needs.URL.
	URLs []string
	// Post and Posts are this post's place in the sequence, counting from one.
	// A page list that fits in one post is post 1 of 1.
	Post, Posts int
	// Page is the first page this post carries and Pages is the run's total
	// page count, both counting from one.
	Page, Pages int
	// Caption is the already-rendered post text.
	Caption string
	// LeadVideoURL is where the post's opening clip can be fetched, set for the
	// platforms that need a URL rather than the bytes. The clip itself is the
	// publisher's own, resolved when it was built; only its staged address has
	// to travel with the post.
	LeadVideoURL string
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

// Identity is who a platform says the credentials belong to.
type Identity struct {
	// ID is the platform's own identifier for the account.
	ID string
	// Name is the handle or display name, when the platform returns one.
	Name string
	// Note carries a caveat worth reporting alongside a success — a token
	// that works but cannot be traced back to an account, for instance.
	Note string
}

// String is the identity as it appears in a log line.
func (i Identity) String() string {
	switch {
	case i.Name != "" && i.ID != "" && i.Name != i.ID:
		return i.Name + " (" + i.ID + ")"
	case i.Name != "":
		return i.Name
	default:
		return i.ID
	}
}

// Publisher posts to one platform.
type Publisher interface {
	// Name is the platform's configuration key: "instagram", "reddit".
	Name() string
	// Needs describes what the publisher requires.
	Needs() Needs
	// Publish posts the input and returns what the platform said.
	Publish(ctx context.Context, in Input) (Result, error)
	// Ping asks the platform who the credentials belong to, without posting
	// anything.
	//
	// It is a read-only call against the cheapest identity endpoint each
	// platform offers, and it is the answer to the question every setup
	// eventually asks: are these credentials right? Finding that out by
	// posting is how a test post ends up on a real feed.
	Ping(ctx context.Context) (Identity, error)
}

// Deps are what every publisher is built with.
type Deps struct {
	// Client is the shared HTTP client. Required.
	Client *httpx.Client
	// Logger records the publisher's own steps.
	Logger zerolog.Logger
	// UserAgent identifies crier to the platforms that insist on one.
	UserAgent string
	// Dir is the project root — the directory the configuration file sits in.
	// A custom platform's command runs there, so `sh ./publish.sh` in a config
	// means the script beside it, the way every path in a config file does.
	Dir string
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
	"slack":     newSlack,
	"vk":        newVK,
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
//
// The script-backed ones come after the built-ins and are sorted among
// themselves, so the order a run reports is the same twice.
func Enabled(cfg *config.Config) []string {
	var out []string
	for _, name := range config.Platforms {
		if enabledIn(cfg, name) {
			out = append(out, name)
		}
	}
	for _, name := range config.CustomNames(&cfg.Publish) {
		if cfg.Publish.Custom[name].Enabled {
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
	case "slack":
		return p.Slack.Enabled
	case "vk":
		return p.VK.Enabled
	default:
		if c := config.CustomOf(p, name); c != nil {
			return c.Enabled
		}
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
		var (
			p   Publisher
			err error
		)
		switch make, ok := registry[name]; {
		case ok:
			p, err = make(cfg, d)
		default:
			c := config.CustomOf(&cfg.Publish, name)
			if c == nil {
				errs = append(errs, fmt.Errorf("unknown platform %q", name))
				continue
			}
			p, err = newCustom(name, c, d)
		}
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

// BuildAll constructs every enabled publisher, whatever the pipeline needs.
//
// It is Build under another name, kept separate so a reader of `crier ping`
// does not have to wonder whether pinging validates a different set of
// platforms than publishing would. It does not: the same constructors, the
// same configuration errors, the same order.
func BuildAll(cfg *config.Config, d Deps) ([]Publisher, error) { return Build(cfg, d) }

// PingAll checks every publisher's credentials, a bounded number at a time.
//
// The shape mirrors RunAll exactly — same fan-out, same panic containment, same
// platform-ordered report — because the two commands answer for the same set of
// platforms and a difference in how they report would be a difference nobody
// could explain.
func PingAll(ctx context.Context, publishers []Publisher, concurrency int, log zerolog.Logger) Report {
	if concurrency < 1 {
		concurrency = 1
	}
	outcomes := make([]Outcome, len(publishers))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, p := range publishers {
		wg.Add(1)
		go func(i int, p Publisher) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outcomes[i] = ping(ctx, p, log)
		}(i, p)
	}
	wg.Wait()

	sort.SliceStable(outcomes, func(i, j int) bool { return outcomes[i].Platform < outcomes[j].Platform })
	return Report{Outcomes: outcomes}
}

// ping checks one publisher, turning a panic into a failure.
func ping(ctx context.Context, p Publisher, log zerolog.Logger) (out Outcome) {
	name := p.Name()
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
				ev = ev.Str("account", out.ID)
			}
			if note := out.Extra["note"]; note != "" {
				ev = ev.Str("note", note)
			}
			ev.Msg("credentials accepted")
			return
		}
		log.Error().Str("platform", name).Dur("elapsed", out.Elapsed).Err(out.Err).Msg("credentials rejected")
	}()

	log.Debug().Str("platform", name).Msg("checking credentials")
	id, err := p.Ping(ctx)
	if err != nil {
		out.Err = err
		out.Error = err.Error()
		return out
	}
	out.OK = true
	out.ID = id.String()
	if id.Note != "" {
		out.Extra = map[string]string{"note": id.Note}
	}
	return out
}

// Files is how many files this post carries.
func (in Input) Files() int {
	if len(in.Artifacts) == 0 {
		return 1
	}
	return len(in.Artifacts)
}

// Sequence is every file this post carries, in page order.
//
// A publisher written before carousels reads Artifact and gets the first file.
// One that takes several reads this and gets them all, and gets a one-entry
// list on an ordinary post, so there is no second code path to keep working.
func (in Input) Sequence() []render.Artifact {
	if len(in.Artifacts) == 0 {
		return []render.Artifact{in.Artifact}
	}
	return in.Artifacts
}

// SequenceURLs is Sequence's other half, for the platforms that fetch.
func (in Input) SequenceURLs() []string {
	if len(in.URLs) == 0 {
		if in.URL == "" {
			return nil
		}
		return []string{in.URL}
	}
	return in.URLs
}

// Job is one publisher and the posts it was given, in page order.
//
// More than one post means the run's page list was longer than the platform
// takes in one go. They are published in order, and one that fails stops the
// rest: a gap in the middle of a sequence is worse than a short sequence,
// because the reader cannot tell it happened.
type Job struct {
	Publisher Publisher
	Posts     []Input
}

// PostOutcome is what one post of a paged job came to.
//
// A job that was one post reports no posts at all: there is nothing a per-post
// breakdown would add to the platform's own line.
type PostOutcome struct {
	// Post is which post this was, counting from one.
	Post int `json:"post"`
	// Pages is how many pages it carried.
	Pages int    `json:"pages"`
	OK    bool   `json:"ok"`
	ID    string `json:"id,omitempty"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

// Outcome is what one job came to.
type Outcome struct {
	Platform string        `json:"platform"`
	OK       bool          `json:"ok"`
	ID       string        `json:"id,omitempty"`
	URL      string        `json:"url,omitempty"`
	Error    string        `json:"error,omitempty"`
	Elapsed  time.Duration `json:"-"`

	// Posts is the per-post breakdown, set only when the job was more than one
	// post. It is what says how far a sequence got before it stopped.
	Posts []PostOutcome `json:"posts,omitempty"`

	Extra map[string]string `json:"extra,omitempty"`
	// Err is the failure itself, for a caller that wants to inspect it.
	Err error `json:"-"`
}

// Published counts the posts that landed.
func (o Outcome) Published() int {
	if len(o.Posts) == 0 {
		if o.OK {
			return 1
		}
		return 0
	}
	n := 0
	for _, p := range o.Posts {
		if p.OK {
			n++
		}
	}
	return n
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
// to eleven places is that ten of them still get the post when the eleventh is
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
//
// A job's posts go out one at a time, in page order, each one finished before
// the next begins. That is not caution: several platforms order a feed by when
// a post completed, so publishing two at once is how a two-part sequence turns
// up back to front. A post that fails stops the ones after it, and the outcome
// says how far it got.
func run(ctx context.Context, job Job, log zerolog.Logger) (out Outcome) {
	name := job.Publisher.Name()
	out = Outcome{Platform: name}
	start := time.Now()
	posts := job.Posts

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
			if len(posts) > 1 {
				ev = ev.Int("posts", len(posts))
			}
			ev.Msg("published")
			return
		}
		log.Error().Str("platform", name).Dur("elapsed", out.Elapsed).Err(out.Err).Msg("publishing failed")
	}()

	if len(posts) == 0 {
		out.Err = fmt.Errorf("nothing to publish")
		out.Error = out.Err.Error()
		return out
	}

	for i, in := range posts {
		if err := ctx.Err(); err != nil {
			out.Err = err
			out.Error = err.Error()
			return out
		}
		ev := log.Debug().Str("platform", name).Str("file", in.Artifact.Path)
		if len(posts) > 1 {
			ev = ev.Int("post", i+1).Int("posts", len(posts)).Int("files", in.Files())
		}
		ev.Msg("publishing")

		res, err := job.Publisher.Publish(ctx, in)
		if len(posts) > 1 {
			p := PostOutcome{Post: i + 1, Pages: in.Files(), OK: err == nil}
			if err != nil {
				p.Error = err.Error()
			} else {
				p.ID, p.URL = res.ID, res.URL
			}
			out.Posts = append(out.Posts, p)
		}
		if err != nil {
			out.Err = postError(err, i, len(posts))
			out.Error = out.Err.Error()
			return out
		}
		if i == 0 {
			// The first post is the platform's line in the report: it is the
			// one a reader follows to find the sequence.
			out.ID, out.URL, out.Extra = res.ID, res.URL, res.Extra
		}
	}
	out.OK = true
	return out
}

// postError says how far a sequence got before it stopped, because "posts 1 to
// 3 of 5 went out" is what the operator has to know to decide what to do next.
func postError(err error, i, total int) error {
	if total <= 1 {
		return err
	}
	if i == 0 {
		return fmt.Errorf("post 1 of %d failed and none went out: %w", total, err)
	}
	return fmt.Errorf("posts 1 to %d of %d went out; post %d failed and the rest were not sent: %w",
		i, total, i+1, err)
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
	return checkSizeOf(a.Path, a.Size, limit, platform)
}

// checkSizeOf is checkSize for a file that is not a rendered artifact: an
// audio attachment or a lead video, which are the operator's own files rather
// than anything crier drew.
func checkSizeOf(path string, size, limit int64, platform string) error {
	if limit <= 0 || size <= limit {
		return nil
	}
	return fmt.Errorf("%s is %s, which is over %s's limit of %s",
		path, humanSize(size), platform, humanSize(limit))
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

// imageVideoAndGIF is the Kinds of a platform that also takes an animation.
//
// It is a separate list because a GIF is not "a small video" to these APIs:
// Telegram wants a different method for it, X a different media category, and
// Instagram, Facebook, TikTok and LinkedIn will not take one at all.
var imageVideoAndGIF = []render.Kind{render.KindImage, render.KindVideo, render.KindGIF}
