package publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// Threads posts through Meta's Threads API.
//
// The shape is Instagram's, because it is the same machinery underneath: a
// container is created from a public URL, polled until Meta has fetched the
// media, and then published. So Threads needs staging for the same reason
// Instagram does — it will not take bytes, only an address its own servers can
// reach.
//
// It is a publisher of its own rather than a mode of the Instagram one because
// almost nothing the two share is spelled the same: a different host, a token
// with its own scopes that an Instagram token cannot stand in for, /threads
// where Instagram has /media, and a carousel that refuses to hold fewer than
// two items.
type Threads struct {
	cfg    config.Threads
	client *httpx.Client
	log    zerolog.Logger
}

func newThreads(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.Threads
	if err := require(c.APIBaseURL, "publish.threads.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.Token, "publish.threads.token"); err != nil {
		return nil, err
	}
	if err := require(c.UserID, "publish.threads.user-id"); err != nil {
		return nil, err
	}
	return &Threads{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (t *Threads) Name() string { return "threads" }

// ThreadsAttachmentMax is how many items one Threads carousel holds.
//
// Twenty, which the reference gives as the carousel's ceiling. It is double
// Instagram's, so a document that paginates into one post here can still be
// two there.
const ThreadsAttachmentMax = 20

// ThreadsCarouselMin is the fewest items a Threads carousel takes.
//
// Two, and it is not a formality: a CAROUSEL container naming one child is
// refused, so a single page has to go out as plain media instead. It is the
// one rule here that Instagram does not share, and the one that would only
// ever fail in production.
const ThreadsCarouselMin = 2

// Needs implements Publisher.
//
// JPEG and PNG both, unlike Instagram, which takes only the first of them.
//
// An animation is not a kind Threads has: there is no way to post a GIF as a
// file, so the pipeline's kind gate refuses the run before anything is
// rendered rather than letting one arrive as a still.
//
// Up to twenty pages become one carousel. A longer page list becomes several
// posts in a row, which is the generic overflow policy every capped platform
// follows.
func (t *Threads) Needs() Needs {
	return Needs{
		URL:            true,
		Formats:        imageFormats,
		Kinds:          imageAndVideo,
		MaxAttachments: ThreadsAttachmentMax,
	}
}

type threadsContainer struct {
	ID string `json:"id"`
}

// threadsStatus is what the container status poll answers with. The states are
// IN_PROGRESS, FINISHED, ERROR, EXPIRED and PUBLISHED, and only the middle
// three are worth acting on.
type threadsStatus struct {
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
}

type threadsPublished struct {
	ID string `json:"id"`
}

// Publish creates a container, waits for Meta to fetch the media, and publishes
// it.
//
// Several pages become a carousel: one child container per page, then a parent
// that lists them, then one publish. The text belongs to the parent, which is
// what makes a paginated document one post rather than a run of them.
func (t *Threads) Publish(ctx context.Context, in Input) (Result, error) {
	urls := in.SequenceURLs()
	if len(urls) == 0 || urls[0] == "" {
		return Result{}, fmt.Errorf("threads needs a public URL for the media; configure stage.mode")
	}
	if len(urls) > ThreadsAttachmentMax {
		return Result{}, fmt.Errorf("a threads carousel holds %d items and this post has %d",
			ThreadsAttachmentMax, len(urls))
	}

	// Two items are a carousel and one is not. The dispatch is explicit
	// because a CAROUSEL container naming a single child is refused by the
	// API, and a one-page run is the commonest run there is.
	if len(urls) >= ThreadsCarouselMin {
		return t.carousel(ctx, in, urls)
	}

	form := url.Values{"access_token": {t.cfg.Token}}
	threadsMedia(form, in.Artifact.Kind, urls[0])
	if in.Caption != "" {
		form.Set("text", in.Caption)
	}

	id, err := t.container(ctx, form)
	if err != nil {
		return Result{}, err
	}
	return t.publishContainer(ctx, id)
}

// threadsMedia names the pair of parameters that say what is being posted.
//
// media_type is not optional here the way it is for an Instagram image child:
// every Threads container declares its own kind, and the URL parameter that
// goes with it changes name to match.
func threadsMedia(form url.Values, kind render.Kind, mediaURL string) {
	if kind == render.KindVideo {
		form.Set("media_type", "VIDEO")
		form.Set("video_url", mediaURL)
		return
	}
	form.Set("media_type", "IMAGE")
	form.Set("image_url", mediaURL)
}

// carousel builds the children, the parent, and publishes the parent.
//
// A child that fails leaves the ones before it behind. They are unpublished
// containers and expire on their own, so the cost of stopping is a log line
// rather than half a carousel on the feed.
func (t *Threads) carousel(ctx context.Context, in Input, urls []string) (Result, error) {
	arts := in.Sequence()
	children := make([]string, 0, len(urls))
	for n, u := range urls {
		form := url.Values{
			"access_token":     {t.cfg.Token},
			"is_carousel_item": {"true"},
		}
		// The kind comes from the page's own artifact, so a carousel of
		// pictures with a clip among them sends the right pair of parameters
		// for each. No text: Threads does not accept one on a child.
		kind := render.KindImage
		if n < len(arts) {
			kind = arts[n].Kind
		}
		threadsMedia(form, kind, u)

		id, err := t.container(ctx, form)
		if err != nil {
			return Result{}, fmt.Errorf("carousel item %d of %d: %w", n+1, len(urls), err)
		}
		children = append(children, id)
	}

	// The children are listed in the order the pages were rendered, which is
	// the order Threads shows them in.
	form := url.Values{
		"access_token": {t.cfg.Token},
		"media_type":   {"CAROUSEL"},
		"children":     {strings.Join(children, ",")},
	}
	if in.Caption != "" {
		form.Set("text", in.Caption)
	}
	parent, err := t.container(ctx, form)
	if err != nil {
		return Result{}, fmt.Errorf("creating the carousel container: %w", err)
	}
	t.log.Debug().Int("items", len(children)).Str("container", parent).
		Msg("created a threads carousel")

	res, err := t.publishContainer(ctx, parent)
	if err != nil {
		return res, err
	}
	if res.Extra == nil {
		res.Extra = map[string]string{}
	}
	res.Extra["children"] = strings.Join(children, ",")
	return res, nil
}

// container creates one container and waits for Meta to fetch its media.
func (t *Threads) container(ctx context.Context, form url.Values) (string, error) {
	var container threadsContainer
	err := t.client.JSON(ctx,
		httpx.NewRequest(http.MethodPost, t.cfg.APIBaseURL, t.cfg.UserID, "threads").Form(form),
		&container)
	if err != nil {
		return "", fmt.Errorf("creating the media container: %w", err)
	}
	if container.ID == "" {
		return "", fmt.Errorf("threads returned no container id")
	}
	t.log.Debug().Str("container", container.ID).Msg("created a threads container")

	if err := t.await(ctx, container.ID); err != nil {
		// The container is left behind on purpose: it expires by itself, and
		// its id in the log is what an operator needs to look it up.
		t.log.Warn().Str("container", container.ID).
			Msg("a threads container was created but not published; it expires on its own")
		return "", err
	}
	return container.ID, nil
}

// await polls the container until Meta has fetched the media.
//
// Meta fetches the URL itself, from its own network, which is the step that
// fails when the URL is a localhost address or behind an interstitial page.
// The reason comes back in error_message, so it is asked for and reported
// rather than left as "could not process".
func (t *Threads) await(ctx context.Context, containerID string) error {
	interval := config.Duration(t.cfg.PollInterval)
	timeout := config.Duration(t.cfg.PollTimeout)
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	err := httpx.Poll(ctx, interval, timeout, func(ctx context.Context) (bool, error) {
		var st threadsStatus
		req := httpx.NewRequest(http.MethodGet, t.cfg.APIBaseURL, containerID).
			Query("fields", "status,error_message").
			Query("access_token", t.cfg.Token)
		if err := t.client.JSON(ctx, req, &st); err != nil {
			return false, err
		}
		switch strings.ToUpper(st.Status) {
		case "FINISHED", "PUBLISHED":
			// PUBLISHED is not a state this poll expects to see, since nothing
			// has published the container yet. It is accepted anyway: a
			// container that is already a post is certainly done processing,
			// and waiting out the budget for a state that will never change
			// would turn a success into a timeout.
			return true, nil
		case "ERROR":
			return false, fmt.Errorf("threads could not process the media: %s",
				firstNonEmpty(st.ErrorMessage, "no reason given"))
		case "EXPIRED":
			return false, fmt.Errorf("the threads media container expired before it was published")
		default:
			t.log.Debug().Str("container", containerID).Str("status", st.Status).
				Msg("waiting for threads to fetch the media")
			return false, nil
		}
	})
	if err != nil {
		return fmt.Errorf("waiting for the media container: %w", err)
	}
	return nil
}

// publishContainer turns a finished container into a post.
//
// Publishing is never repeated on a 5xx: the post may have been created and
// the answer lost, and asking again would make a second one. The one exception
// is the not-ready refusal, which is the same pair of Meta errors Instagram
// serves — see threadsMediaNotReady.
func (t *Threads) publishContainer(ctx context.Context, id string) (Result, error) {
	var published threadsPublished
	err := retryNotReady(ctx, t.log,
		config.Duration(t.cfg.PollInterval), config.Duration(t.cfg.PollTimeout),
		"threads threads_publish", func() error {
			req := httpx.NewRequest(http.MethodPost, t.cfg.APIBaseURL, t.cfg.UserID, "threads_publish").
				Form(url.Values{"creation_id": {id}, "access_token": {t.cfg.Token}})
			return t.client.NoRetry().JSON(ctx, req, &published)
		}, threadsMediaNotReady)
	if err != nil {
		t.log.Warn().Str("container", id).
			Msg("publishing the threads container failed; it expires on its own")
		return Result{}, fmt.Errorf("publishing the container: %w", err)
	}
	if published.ID == "" {
		return Result{}, fmt.Errorf("threads accepted the publish and named no post id")
	}
	return Result{
		ID:    published.ID,
		URL:   t.permalink(ctx, published.ID),
		Extra: map[string]string{"containerId": id},
	}, nil
}

// threadsMediaNotReady recognises the publish refusals that mean nothing
// happened.
//
// It is the same pair Instagram serves, for the same reason: this is the same
// Meta infrastructure, the container polls ready while replication catches up,
// and the refusal itself is the proof that no post was created.
//
//   - 9007, "Media ID is not available".
//   - 24 with subcode 2207006, "Media Not Found": the very container this
//     process created and polled seconds earlier does not exist yet at the
//     publish endpoint.
//
// Anything else keeps the never-repeat rule. A container that is genuinely
// gone spends the poll budget and surfaces the same error at the end of it.
func threadsMediaNotReady(err error) bool {
	var api *httpx.APIError
	if !errors.As(err, &api) {
		return false
	}
	if bytes.Contains(api.Body, []byte(`"code":9007`)) {
		return true
	}
	return bytes.Contains(api.Body, []byte(`"code":24`)) &&
		bytes.Contains(api.Body, []byte(`"error_subcode":2207006`))
}

// permalink asks Threads where the post ended up.
//
// A failure here is not a failure of the post: the post exists, and reporting
// no link is better than reporting one that goes nowhere.
func (t *Threads) permalink(ctx context.Context, mediaID string) string {
	var out struct {
		Permalink string `json:"permalink"`
	}
	req := httpx.NewRequest(http.MethodGet, t.cfg.APIBaseURL, mediaID).
		Query("fields", "permalink").
		Query("access_token", t.cfg.Token)
	if err := t.client.JSON(ctx, req, &out); err != nil {
		t.log.Warn().Str("media", mediaID).Err(err).
			Msg("threads published the post but would not say where it is")
		return ""
	}
	return out.Permalink
}

// Ping reads the Threads account the token belongs to.
//
// /me rather than the configured user id, because that is the one question a
// Threads token can always be asked: it answers with the account the token was
// issued for, which is what a wrong token gets wrong.
func (t *Threads) Ping(ctx context.Context) (Identity, error) {
	var out struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	req := httpx.NewRequest(http.MethodGet, t.cfg.APIBaseURL, "me").
		Query("fields", "id,username").
		Query("access_token", t.cfg.Token)
	if err := t.client.JSON(ctx, req, &out); err != nil {
		return Identity{}, err
	}
	id := Identity{ID: out.ID}
	if out.Username != "" {
		id.Name = "@" + out.Username
	}
	if out.ID != "" && t.cfg.UserID != "" && out.ID != t.cfg.UserID {
		// Legal, and almost always a user id copied from the wrong place —
		// an Instagram professional account id, most often, since the two
		// setups sit next to each other in the Meta dashboard.
		id.Note = fmt.Sprintf("the token belongs to account %s, which is not publish.threads.user-id (%s)",
			out.ID, t.cfg.UserID)
	}
	return id, nil
}
