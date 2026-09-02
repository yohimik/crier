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

// Instagram posts through the Graph API.
//
// It is the strictest platform crier talks to: it will not take bytes at all,
// only a public URL that Meta's own servers fetch, and for a photo that URL
// has to serve JPEG. That is why an Instagram post needs staging configured,
// and why a laptop needs a tunnel.
type Instagram struct {
	cfg    config.Instagram
	lead   VideoFile
	client *httpx.Client
	log    zerolog.Logger
}

func newInstagram(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.Instagram
	if err := require(c.APIBaseURL, "publish.instagram.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.Token, "publish.instagram.token"); err != nil {
		return nil, err
	}
	if err := require(c.UserID, "publish.instagram.user-id"); err != nil {
		return nil, err
	}
	lead, err := LeadVideoFor(cfg, "instagram")
	if err != nil {
		return nil, err
	}
	if lead.Attached() && c.Story {
		// A story has no carousel to open, so there is nothing for a lead
		// video to lead. Refusing the run would be officious when the same
		// configuration file drives a feed pass and a story pass; saying so is
		// not, because a clip that silently goes nowhere reads as a crier bug.
		d.Logger.Warn().Str("platform", "instagram").Str("video", lead.Name).
			Msg("stories have no carousel, so the lead video is not posted in a story pass")
	}
	return &Instagram{cfg: c, lead: lead, client: d.Client, log: d.Logger}, nil
}

// leads reports whether this post opens with a clip. A story never does.
func (i *Instagram) leads() bool { return i.lead.Attached() && !i.cfg.Story }

// Name implements Publisher.
func (i *Instagram) Name() string { return "instagram" }

// IGCarouselMax is how many items one Instagram carousel holds.
const IGCarouselMax = 10

// Needs implements Publisher.
//
// JPEG only: Instagram rejects a PNG image_url outright.
//
// A feed post takes up to ten images as one carousel. A story takes one: the
// Stories API has no carousel at all, so a paged run becomes one story per
// page. Those are published in order, one finished before the next begins,
// because a story reel is ordered by when each story was published.
// A lead video is one of the carousel's ten items, so a run that opens with
// one declares nine pages per post. Reserving the slot is what lets a long
// document paginate into carousels that fit.
func (i *Instagram) Needs() Needs {
	n := Needs{URL: true, Formats: []config.Format{config.JPEG}, Kinds: imageAndVideo}
	if !i.cfg.Story {
		n.MaxAttachments = IGCarouselMax
		if i.leads() {
			n.MaxAttachments--
		}
	}
	return n
}

type igContainer struct {
	ID string `json:"id"`
}

type igStatus struct {
	StatusCode string `json:"status_code"`
	Status     string `json:"status"`
	ID         string `json:"id"`
}

type igPublished struct {
	ID string `json:"id"`
}

// Publish creates a media container, waits for Meta to fetch and accept the
// media, and then publishes the container.
//
// Several images become a carousel: one child container per image, then a
// parent that lists them, then one publish. The caption belongs to the parent,
// which is why a carousel posts as one entry in the feed rather than as a run
// of separate posts.
func (i *Instagram) Publish(ctx context.Context, in Input) (Result, error) {
	urls := in.SequenceURLs()
	if len(urls) == 0 || urls[0] == "" {
		return Result{}, fmt.Errorf("instagram needs a public URL for the media; configure stage.mode")
	}
	caption := i.captionFor(in.Caption)

	// A lead video makes a carousel of any post, including a single-page one:
	// the clip and the card are two items, and two items is a carousel.
	if len(urls) > 1 || i.leads() {
		return i.carousel(ctx, in.LeadVideoURL, urls, caption)
	}

	form := url.Values{"access_token": {i.cfg.Token}}
	if caption != "" {
		form.Set("caption", caption)
	}
	switch {
	case in.Artifact.Kind == render.KindVideo && i.cfg.Story:
		form.Set("media_type", "STORIES")
		form.Set("video_url", urls[0])
	case in.Artifact.Kind == render.KindVideo:
		form.Set("media_type", "REELS")
		form.Set("video_url", urls[0])
	case i.cfg.Story:
		form.Set("media_type", "STORIES")
		form.Set("image_url", urls[0])
	default:
		form.Set("image_url", urls[0])
	}

	id, err := i.container(ctx, form)
	if err != nil {
		return Result{}, err
	}
	return i.publishContainer(ctx, id)
}

// carousel builds the children, the parent, and publishes the parent.
//
// A child that fails leaves the ones before it behind. They are unpublished
// containers and expire on their own in 24 hours, so the cost of stopping is a
// log line rather than a half-posted carousel.
func (i *Instagram) carousel(ctx context.Context, leadURL string, urls []string, caption string) (Result, error) {
	items := len(urls)
	if i.leads() {
		items++
	}
	if items > IGCarouselMax {
		if i.leads() {
			return Result{}, fmt.Errorf(
				"an instagram carousel holds %d items and this post has %d pages plus the lead video; "+
					"lower publish.instagram.max-attachments", IGCarouselMax, len(urls))
		}
		return Result{}, fmt.Errorf("an instagram carousel holds %d images and this post has %d",
			IGCarouselMax, len(urls))
	}
	if i.cfg.Story {
		return Result{}, fmt.Errorf("instagram stories have no carousel; each page posts as its own story")
	}

	children := make([]string, 0, items)
	if i.leads() {
		if leadURL == "" {
			return Result{}, fmt.Errorf(
				"instagram needs a public URL for the lead video; configure stage.mode")
		}
		// media_type=VIDEO, because the endpoint says so. The reference's enum
		// for this parameter lists CAROUSEL, REELS and STORIES and omits VIDEO
		// entirely, which reads as "do not send it" — and sending nothing gets
		// a 400 IGApiException code 100, "The parameter image_url is required"
		// (fbtrace A0MpoozUBsHxq-KQTcd9gcG, rc.8's announce run). Without a
		// media_type the API presumes an image child and asks for the
		// parameter an image child would have. Behaviour wins over the
		// reference: the documentation is wrong here, and this is the shape
		// that posts.
		//
		// No caption, which Meta does not accept on a child.
		id, err := i.container(ctx, url.Values{
			"access_token":     {i.cfg.Token},
			"video_url":        {leadURL},
			"is_carousel_item": {"true"},
			"media_type":       {"VIDEO"},
		})
		if err != nil {
			return Result{}, fmt.Errorf("the lead video: %w", err)
		}
		children = append(children, id)
		i.log.Debug().Str("container", id).Str("video", i.lead.Name).
			Msg("created the instagram carousel's lead video child")
	}

	for n, u := range urls {
		form := url.Values{
			"access_token":     {i.cfg.Token},
			"image_url":        {u},
			"is_carousel_item": {"true"},
		}
		id, err := i.container(ctx, form)
		if err != nil {
			return Result{}, fmt.Errorf("carousel image %d of %d: %w", n+1, len(urls), err)
		}
		children = append(children, id)
	}

	// The children are listed in the order the pages were rendered, which is
	// the order Instagram shows them in.
	form := url.Values{
		"access_token": {i.cfg.Token},
		"media_type":   {"CAROUSEL"},
		"children":     {strings.Join(children, ",")},
	}
	if caption != "" {
		form.Set("caption", caption)
	}
	parent, err := i.container(ctx, form)
	if err != nil {
		return Result{}, fmt.Errorf("creating the carousel container: %w", err)
	}
	i.log.Debug().Int("items", len(children)).Bool("opensWithVideo", i.leads()).
		Str("container", parent).Msg("created an instagram carousel")

	res, err := i.publishContainer(ctx, parent)
	if err != nil {
		return res, err
	}
	if res.Extra == nil {
		res.Extra = map[string]string{}
	}
	res.Extra["children"] = strings.Join(children, ",")
	return res, nil
}

// captionFor is the text that actually goes out, which for a story is none.
func (i *Instagram) captionFor(caption string) string {
	if caption == "" || !i.cfg.Story {
		return caption
	}
	// The Stories API has no caption: Meta ignores the parameter, so a story
	// goes out as bare media whatever the text said. Not sending it changes
	// nothing on the wire that matters — the warning is the point, because the
	// only way to put text on a story is to draw it into the image, and a
	// silently dropped caption reads as a crier bug.
	i.log.Warn().Msg("instagram stories carry no caption; the text was not sent — put it in the image itself, or in a story overlay")
	return ""
}

// container creates one media container and waits for Meta to fetch its media.
func (i *Instagram) container(ctx context.Context, form url.Values) (string, error) {
	var container igContainer
	err := i.client.JSON(ctx,
		httpx.NewRequest(http.MethodPost, i.cfg.APIBaseURL, i.cfg.UserID, "media").Form(form),
		&container)
	if err != nil {
		return "", fmt.Errorf("creating the media container: %w", err)
	}
	if container.ID == "" {
		return "", fmt.Errorf("instagram returned no container id")
	}
	i.log.Debug().Str("container", container.ID).Msg("created an instagram media container")

	if err := i.await(ctx, container.ID); err != nil {
		// The container is left behind on purpose: it expires in 24 hours, and
		// its id in the log is what an operator needs to look it up.
		i.log.Warn().Str("container", container.ID).
			Msg("an instagram container was created but not published; it expires in 24 hours")
		return "", err
	}
	return container.ID, nil
}

// publishContainer turns a finished container into a post.
//
// One refusal is retried: error 9007, "Media ID is not available". The
// container can poll FINISHED while Meta is still making the media
// publishable, and this answer means no post was created, so asking again is
// safe where a blind retry of the publish would not be. It happened on a real
// release: the carousel published and the story, seconds behind it, was told
// to wait a moment. The wait is bounded by the same poll budget the container
// fetch uses.
func (i *Instagram) publishContainer(ctx context.Context, id string) (Result, error) {
	var published igPublished
	err := retryNotReady(ctx, i.log,
		config.Duration(i.cfg.PollInterval), config.Duration(i.cfg.PollTimeout),
		"instagram media_publish", func() error {
			req := httpx.NewRequest(http.MethodPost, i.cfg.APIBaseURL, i.cfg.UserID, "media_publish").
				Form(url.Values{"creation_id": {id}, "access_token": {i.cfg.Token}})
			// Publishing is the irreversible step: a 5xx here may still have
			// created the post, and repeating it would create a second one.
			return i.client.NoRetry().JSON(ctx, req, &published)
		}, igMediaNotReady)
	if err != nil {
		i.log.Warn().Str("container", id).
			Msg("publishing the instagram container failed; it expires in 24 hours")
		return Result{}, fmt.Errorf("publishing the container: %w", err)
	}
	return Result{
		ID:    published.ID,
		URL:   i.permalink(ctx, published.ID),
		Extra: map[string]string{"containerId": id},
	}, nil
}

// igMediaNotReady recognises the publish refusals that mean nothing
// happened, of which Meta has served two so far, both marked is_transient:
// false and both cured by waiting:
//
//   - 9007, "Media ID is not available": the container polled FINISHED while
//     the media was still becoming publishable. Seen on rc.3.
//   - 24 with subcode 2207006, "Media Not Found": the very container this
//     process created and polled seconds earlier "does not exist" at the
//     publish endpoint — replication lag wearing a different costume. Seen
//     on rc.5, again on the first story seconds behind the carousel. A
//     container that does not exist published nothing, which is what makes
//     asking again safe; one that is genuinely gone spends the budget and
//     surfaces the same error.
func igMediaNotReady(err error) bool {
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

// permalink asks Instagram where the post ended up.
//
// The media id is not the shortcode, so instagram.com/p/<media-id> is a 404 —
// a link crier reported on every successful post and that never worked. Only
// Instagram knows the shortcode, so it has to be asked.
//
// A failure here is not a failure of the post: the post exists, and reporting
// no link is better than reporting one that goes nowhere.
func (i *Instagram) permalink(ctx context.Context, mediaID string) string {
	var out struct {
		Permalink string `json:"permalink"`
	}
	req := httpx.NewRequest(http.MethodGet, i.cfg.APIBaseURL, mediaID).
		Query("fields", "permalink").
		Query("access_token", i.cfg.Token)
	if err := i.client.JSON(ctx, req, &out); err != nil {
		i.log.Warn().Str("media", mediaID).Err(err).
			Msg("instagram published the post but would not say where it is")
		return ""
	}
	return out.Permalink
}

// await polls the container until Meta has fetched the media.
//
// Meta fetches the URL itself, from its own network, which is the step that
// fails when the URL is a localhost address or behind an interstitial page.
func (i *Instagram) await(ctx context.Context, containerID string) error {
	interval := config.Duration(i.cfg.PollInterval)
	timeout := config.Duration(i.cfg.PollTimeout)
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	err := httpx.Poll(ctx, interval, timeout, func(ctx context.Context) (bool, error) {
		var st igStatus
		req := httpx.NewRequest(http.MethodGet, i.cfg.APIBaseURL, containerID).
			Query("fields", "status_code,status").
			Query("access_token", i.cfg.Token)
		if err := i.client.JSON(ctx, req, &st); err != nil {
			return false, err
		}
		switch strings.ToUpper(st.StatusCode) {
		case "FINISHED":
			return true, nil
		case "ERROR":
			return false, fmt.Errorf("instagram could not process the media: %s", firstNonEmpty(st.Status, "no reason given"))
		case "EXPIRED":
			return false, fmt.Errorf("the instagram media container expired before it was published")
		default:
			i.log.Debug().Str("container", containerID).Str("status", st.StatusCode).
				Msg("waiting for instagram to fetch the media")
			return false, nil
		}
	})
	if err != nil {
		return fmt.Errorf("waiting for the media container: %w", err)
	}
	return nil
}

// Ping reads the Instagram account the token and user id point at.
//
// The same call the publisher would make first, minus everything that writes:
// a wrong user id and a wrong token fail here in the same way they would fail
// halfway through a post.
func (i *Instagram) Ping(ctx context.Context) (Identity, error) {
	var out struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	req := httpx.NewRequest(http.MethodGet, i.cfg.APIBaseURL, i.cfg.UserID).
		Query("fields", "id,username").
		Query("access_token", i.cfg.Token)
	if err := i.client.JSON(ctx, req, &out); err != nil {
		return Identity{}, err
	}
	name := out.Username
	if name != "" {
		name = "@" + name
	}
	return Identity{ID: out.ID, Name: name}, nil
}
