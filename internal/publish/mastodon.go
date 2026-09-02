package publish

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// Mastodon posts to one instance.
//
// There is no central API: the instance's own base URL is the endpoint, and
// the token belongs to that instance alone.
type Mastodon struct {
	cfg    config.Mastodon
	client *httpx.Client
	log    zerolog.Logger
}

func newMastodon(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.Mastodon
	if err := require(c.APIBaseURL, "publish.mastodon.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.Token, "publish.mastodon.token"); err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(c.Visibility)) {
	case "", "public", "unlisted", "private", "direct":
	default:
		return nil, fmt.Errorf("publish.mastodon.visibility %q is not one of public, unlisted, private or direct", c.Visibility)
	}
	return &Mastodon{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (m *Mastodon) Name() string { return "mastodon" }

// MastodonMediaMax is how many attachments one status holds.
const MastodonMediaMax = 4

// Needs implements Publisher.
func (m *Mastodon) Needs() Needs {
	return Needs{Formats: imageFormats, Kinds: imageVideoAndGIF, MaxAttachments: MastodonMediaMax}
}

type mastodonAttachment struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type mastodonStatus struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Publish uploads the media, waits for the instance to finish processing it,
// and posts a status referring to it.
func (m *Mastodon) Publish(ctx context.Context, in Input) (Result, error) {
	arts := in.Sequence()
	if len(arts) > MastodonMediaMax {
		return Result{}, fmt.Errorf("a mastodon status holds %d attachments and this post has %d",
			MastodonMediaMax, len(arts))
	}
	// Uploaded in page order; media_ids is the order the instance shows them
	// in.
	ids := make([]string, 0, len(arts))
	for n, a := range arts {
		id, err := m.upload(ctx, a, in.Caption)
		if err != nil {
			if len(arts) > 1 {
				return Result{}, fmt.Errorf("attachment %d of %d: %w", n+1, len(arts), err)
			}
			return Result{}, err
		}
		ids = append(ids, id)
	}

	body := map[string]any{
		"status":    in.Caption,
		"media_ids": ids,
	}
	if v := strings.TrimSpace(m.cfg.Visibility); v != "" {
		body["visibility"] = v
	}

	var status mastodonStatus
	req := httpx.NewRequest(http.MethodPost, m.cfg.APIBaseURL, "api/v1/statuses").
		Bearer(m.cfg.Token).
		// An idempotency key lets the instance collapse a duplicate, which is
		// the belt to NoRetry's braces.
		Header("Idempotency-Key", idempotencyKey(in)).
		JSON(body)
	if err := m.client.NoRetry().JSON(ctx, req, &status); err != nil {
		return Result{}, err
	}
	return Result{ID: status.ID, URL: status.URL,
		Extra: map[string]string{"mediaId": strings.Join(ids, ",")}}, nil
}

// upload posts the file and returns the attachment id, waiting when the
// instance says it is still processing.
//
// A 202 means "accepted, not ready": posting a status that refers to an
// attachment in that state is rejected, so the wait is not optional.
func (m *Mastodon) upload(ctx context.Context, a render.Artifact, caption string) (string, error) {
	parts := []httpx.Part{httpx.FilePart("file", a.Path, a.ContentType)}
	if alt := firstNonEmpty(m.cfg.AltText, caption); alt != "" {
		parts = append(parts, httpx.Field("description", alt))
	}

	var att mastodonAttachment
	status, err := m.client.StatusOf(ctx,
		httpx.NewRequest(http.MethodPost, m.cfg.APIBaseURL, "api/v2/media").
			Bearer(m.cfg.Token).
			Multipart(parts...),
		func(code int) bool { return code == http.StatusOK || code == http.StatusAccepted },
		&att,
	)
	if err != nil {
		return "", err
	}
	if att.ID == "" {
		return "", fmt.Errorf("mastodon returned no attachment id")
	}
	if status == http.StatusOK {
		return att.ID, nil
	}

	m.log.Debug().Str("id", att.ID).Msg("mastodon is still processing the media; waiting")
	interval := config.Duration(m.cfg.PollInterval)
	timeout := config.Duration(m.cfg.PollTimeout)
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	err = httpx.Poll(ctx, interval, timeout, func(ctx context.Context) (bool, error) {
		code, err := m.client.StatusOf(ctx,
			httpx.NewRequest(http.MethodGet, m.cfg.APIBaseURL, "api/v1/media", att.ID).Bearer(m.cfg.Token),
			func(code int) bool {
				return code == http.StatusOK || code == http.StatusPartialContent
			}, nil)
		if err != nil {
			return false, err
		}
		return code == http.StatusOK, nil
	})
	if err != nil {
		return "", fmt.Errorf("waiting for mastodon to process the media: %w", err)
	}
	return att.ID, nil
}

// Ping verifies the token against the instance it belongs to.
func (m *Mastodon) Ping(ctx context.Context) (Identity, error) {
	var out struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Acct     string `json:"acct"`
	}
	req := httpx.NewRequest(http.MethodGet, m.cfg.APIBaseURL, "api/v1/accounts/verify_credentials").
		Bearer(m.cfg.Token)
	if err := m.client.JSON(ctx, req, &out); err != nil {
		return Identity{}, err
	}
	return Identity{ID: out.ID, Name: "@" + firstNonEmpty(out.Acct, out.Username)}, nil
}
