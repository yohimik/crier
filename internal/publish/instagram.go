package publish

import (
	"context"
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
	return &Instagram{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (i *Instagram) Name() string { return "instagram" }

// Needs implements Publisher.
//
// JPEG only: Instagram rejects a PNG image_url outright.
func (i *Instagram) Needs() Needs {
	return Needs{URL: true, Formats: []config.Format{config.JPEG}, Kinds: imageAndVideo}
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
func (i *Instagram) Publish(ctx context.Context, in Input) (Result, error) {
	if in.URL == "" {
		return Result{}, fmt.Errorf("instagram needs a public URL for the media; configure stage.mode")
	}

	form := url.Values{"access_token": {i.cfg.Token}}
	if in.Caption != "" {
		form.Set("caption", in.Caption)
	}
	switch {
	case in.Artifact.Kind == render.KindVideo && i.cfg.Story:
		form.Set("media_type", "STORIES")
		form.Set("video_url", in.URL)
	case in.Artifact.Kind == render.KindVideo:
		form.Set("media_type", "REELS")
		form.Set("video_url", in.URL)
	case i.cfg.Story:
		form.Set("media_type", "STORIES")
		form.Set("image_url", in.URL)
	default:
		form.Set("image_url", in.URL)
	}

	var container igContainer
	err := i.client.JSON(ctx,
		httpx.NewRequest(http.MethodPost, i.cfg.APIBaseURL, i.cfg.UserID, "media").Form(form),
		&container)
	if err != nil {
		return Result{}, fmt.Errorf("creating the media container: %w", err)
	}
	if container.ID == "" {
		return Result{}, fmt.Errorf("instagram returned no container id")
	}
	i.log.Debug().Str("container", container.ID).Msg("created an instagram media container")

	if err := i.await(ctx, container.ID); err != nil {
		// The container is left behind on purpose: it expires in 24 hours, and
		// its id in the log is what an operator needs to look it up.
		i.log.Warn().Str("container", container.ID).
			Msg("an instagram container was created but not published; it expires in 24 hours")
		return Result{}, err
	}

	var published igPublished
	req := httpx.NewRequest(http.MethodPost, i.cfg.APIBaseURL, i.cfg.UserID, "media_publish").
		Form(url.Values{"creation_id": {container.ID}, "access_token": {i.cfg.Token}})
	// Publishing is the irreversible step: a 5xx here may still have created
	// the post, and repeating it would create a second one.
	if err := i.client.NoRetry().JSON(ctx, req, &published); err != nil {
		i.log.Warn().Str("container", container.ID).
			Msg("publishing the instagram container failed; it expires in 24 hours")
		return Result{}, fmt.Errorf("publishing the container: %w", err)
	}

	return Result{
		ID:    published.ID,
		URL:   "https://www.instagram.com/p/" + published.ID,
		Extra: map[string]string{"containerId": container.ID},
	}, nil
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
