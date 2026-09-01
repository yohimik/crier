package publish

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
	"github.com/yohimik/crier/internal/version"
)

// Reddit posts to a subreddit.
//
// Reddit is two hosts: tokens come from the www host and everything else goes
// to the oauth host. Both are configurable, which is what lets the end-to-end
// tests point them at one fake.
//
// Media does not go to Reddit directly. A lease call returns a signed form for
// Reddit's own object store, the file is POSTed there, and the resulting URL is
// what the submit call refers to.
type Reddit struct {
	cfg       config.Reddit
	client    *httpx.Client
	log       zerolog.Logger
	userAgent string
}

func newReddit(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.Reddit
	if err := require(c.APIBaseURL, "publish.reddit.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.AuthBaseURL, "publish.reddit.auth-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.ClientID, "publish.reddit.client-id"); err != nil {
		return nil, err
	}
	if err := require(c.ClientSecret, "publish.reddit.client-secret"); err != nil {
		return nil, err
	}
	if err := require(c.Subreddit, "publish.reddit.subreddit"); err != nil {
		return nil, err
	}
	if c.RefreshToken == "" {
		if err := require(c.Username, "publish.reddit.username"); err != nil {
			return nil, err
		}
		if err := require(c.Password, "publish.reddit.password"); err != nil {
			return nil, fmt.Errorf("%w (or set publish.reddit.refresh-token instead)", err)
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.Kind)) {
	case "", "auto", "image", "video", "link":
	default:
		return nil, fmt.Errorf("publish.reddit.kind %q is not one of auto, image, video or link", c.Kind)
	}

	r := &Reddit{cfg: c, client: d.Client, log: d.Logger}
	// crier's generic User-Agent is deliberately not in this chain: Reddit's
	// terms want "platform:appid:version (by /u/name)", and anything else is
	// throttled to a crawl in a way that looks like an unexplained rate limit.
	r.userAgent = firstNonEmpty(c.UserAgent, defaultRedditUserAgent(c.Username))
	return r, nil
}

// defaultRedditUserAgent builds the descriptive agent Reddit's terms require.
// A generic one is throttled hard, which looks like a rate limit nobody can
// explain.
func defaultRedditUserAgent(username string) string {
	who := strings.TrimPrefix(strings.TrimSpace(username), "u/")
	if who == "" {
		who = "unknown"
	}
	return fmt.Sprintf("cli:com.yohimik.crier:%s (by /u/%s)", version.Get().Version, who)
}

// Name implements Publisher.
func (r *Reddit) Name() string { return "reddit" }

// Needs implements Publisher.
//
// A link post is the only kind that needs a staged URL; image and video are
// uploaded. URL is declared as needed only in link mode so that an
// image-posting configuration does not demand staging it will not use.
func (r *Reddit) Needs() Needs {
	return Needs{
		URL:     strings.EqualFold(strings.TrimSpace(r.cfg.Kind), "link"),
		Formats: []config.Format{config.JPEG, config.PNG},
		Kinds:   imageVideoAndGIF,
	}
}

type redditToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
}

type redditLease struct {
	Args struct {
		Action string `json:"action"`
		// The fields are an array of name/value pairs rather than an object,
		// and their order is part of the signature the object store checks.
		Fields []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"fields"`
	} `json:"args"`
	Asset struct {
		AssetID         string `json:"asset_id"`
		ProcessingState string `json:"processing_state"`
	} `json:"asset"`
}

type redditSubmit struct {
	JSON struct {
		Errors [][]any `json:"errors"`
		Data   struct {
			URL  string `json:"url"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	} `json:"json"`
}

type redditListing struct {
	Data struct {
		Children []struct {
			Data struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Title     string `json:"title"`
				Permalink string `json:"permalink"`
				URL       string `json:"url"`
			} `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

// Publish authenticates, uploads what needs uploading, and submits the post.
func (r *Reddit) Publish(ctx context.Context, in Input) (Result, error) {
	token, err := r.token(ctx)
	if err != nil {
		return Result{}, err
	}

	kind := r.kindFor(in)
	form := url.Values{
		"api_type":    {"json"},
		"sr":          {strings.TrimPrefix(r.cfg.Subreddit, "r/")},
		"title":       {r.title(in)},
		"kind":        {kind},
		"resubmit":    {"true"},
		"sendreplies": {"true"},
	}
	if r.cfg.NSFW {
		form.Set("nsfw", "true")
	}
	if r.cfg.Spoiler {
		form.Set("spoiler", "true")
	}
	if r.cfg.FlairID != "" {
		form.Set("flair_id", r.cfg.FlairID)
	}

	extra := map[string]string{}
	switch kind {
	case "link":
		if in.URL == "" {
			return Result{}, fmt.Errorf("reddit is in link mode but nothing staged a URL")
		}
		form.Set("url", in.URL)
	default:
		mediaURL, err := r.upload(ctx, token, in.Artifact)
		if err != nil {
			return Result{}, err
		}
		form.Set("url", mediaURL)
		extra["mediaUrl"] = mediaURL

		if kind == "video" {
			if in.Poster == nil {
				return Result{}, fmt.Errorf("a reddit video post needs a poster image")
			}
			posterURL, err := r.upload(ctx, token, *in.Poster)
			if err != nil {
				return Result{}, fmt.Errorf("uploading the video poster: %w", err)
			}
			form.Set("video_poster_url", posterURL)
			extra["posterUrl"] = posterURL
		}
	}

	var out redditSubmit
	req := r.authed(http.MethodPost, token, "api/submit").Form(form)
	// Submitting is the post itself.
	if err := r.client.NoRetry().JSON(ctx, req, &out); err != nil {
		return Result{}, fmt.Errorf("submitting the post: %w", err)
	}
	if len(out.JSON.Errors) > 0 {
		return Result{}, fmt.Errorf("reddit refused the post: %v", out.JSON.Errors)
	}

	res := Result{ID: firstNonEmpty(out.JSON.Data.Name, out.JSON.Data.ID), URL: out.JSON.Data.URL, Extra: extra}
	if res.URL == "" {
		// A media post's submit response usually carries no post id at all;
		// the reliable way to find the permalink is to look at the account's
		// own submissions. It is best effort: the post is already made either
		// way, so a failure here is a warning rather than an error.
		if link, id := r.findPermalink(ctx, token, form.Get("title")); link != "" {
			res.URL, res.ID = link, firstNonEmpty(res.ID, id)
		}
	}
	return res, nil
}

// kindFor decides what sort of post to make.
func (r *Reddit) kindFor(in Input) string {
	switch strings.ToLower(strings.TrimSpace(r.cfg.Kind)) {
	case "image", "video", "link":
		return strings.ToLower(strings.TrimSpace(r.cfg.Kind))
	default:
		if in.Artifact.Kind == render.KindVideo {
			return "video"
		}
		// An animation is submitted as an image, not as `videogif`.
		//
		// The asset store is told image/gif through the lease's mime type and
		// keeps the file animated; `videogif` is for an MP4 standing in for a
		// GIF and wants a poster URL crier has no reason to produce. Verified
		// against Reddit's media asset documentation, 2026-09-01.
		return "image"
	}
}

// title is the post's title, which Reddit requires.
func (r *Reddit) title(in Input) string {
	return firstNonEmpty(r.cfg.Title, in.Caption, "crier post")
}

// authed starts a request against the API host with the bearer token and the
// mandatory User-Agent.
func (r *Reddit) authed(method, token string, segments ...string) *httpx.Builder {
	return httpx.NewRequest(method, r.cfg.APIBaseURL, segments...).
		Bearer(token).
		Header("User-Agent", r.userAgent)
}

// token gets an access token.
//
// A password grant returns a one hour token and no refresh token, so crier
// asks for a new one every run. It also fails outright when the account has
// two-factor authentication on, which is why a refresh token is the better
// setup and is preferred when one is configured.
func (r *Reddit) token(ctx context.Context) (string, error) {
	form := url.Values{}
	if r.cfg.RefreshToken != "" {
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", r.cfg.RefreshToken)
	} else {
		form.Set("grant_type", "password")
		form.Set("username", r.cfg.Username)
		form.Set("password", r.cfg.Password)
	}

	basic := base64.StdEncoding.EncodeToString([]byte(r.cfg.ClientID + ":" + r.cfg.ClientSecret))
	var out redditToken
	req := httpx.NewRequest(http.MethodPost, r.cfg.AuthBaseURL, "api/v1/access_token").
		Header("Authorization", "Basic "+basic).
		Header("User-Agent", r.userAgent).
		Form(form)
	if err := r.client.JSON(ctx, req, &out); err != nil {
		return "", fmt.Errorf("getting a reddit token: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("getting a reddit token: %s "+
			"(a password grant does not work on an account with two-factor authentication; "+
			"use publish.reddit.refresh-token)", out.Error)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("reddit returned no access token")
	}
	return out.AccessToken, nil
}

// upload leases a slot in Reddit's object store and posts the file into it.
func (r *Reddit) upload(ctx context.Context, token string, a render.Artifact) (string, error) {
	name := filepath.Base(a.Path)
	var lease redditLease
	req := r.authed(http.MethodPost, token, "api/media/asset.json").
		Form(url.Values{"filepath": {name}, "mimetype": {a.ContentType}})
	if err := r.client.JSON(ctx, req, &lease); err != nil {
		return "", fmt.Errorf("leasing an upload slot: %w", err)
	}
	if lease.Args.Action == "" || len(lease.Args.Fields) == 0 {
		return "", fmt.Errorf("reddit returned no upload lease")
	}

	action := redditActionURL(lease.Args.Action)

	// The fields go first, in the order Reddit gave them, and the file goes
	// last: the object store's policy check depends on both.
	parts := make([]httpx.Part, 0, len(lease.Args.Fields)+1)
	key := ""
	for _, f := range lease.Args.Fields {
		if f.Name == "key" {
			key = f.Value
		}
		parts = append(parts, httpx.Field(f.Name, f.Value))
	}
	if key == "" {
		return "", fmt.Errorf("the upload lease had no key field")
	}
	parts = append(parts, httpx.FilePart("file", a.Path, a.ContentType))

	// The lease is single use, so a retry would upload against a spent policy.
	resp, err := r.client.NoRetry().Send(ctx, httpx.NewRequest(http.MethodPost, action).Multipart(parts...))
	if err != nil {
		return "", fmt.Errorf("uploading to reddit's media store: %w", err)
	}
	_ = resp.Body.Close()

	mediaURL := strings.TrimRight(action, "/") + "/" + key
	r.log.Debug().Str("url", mediaURL).Str("asset", lease.Asset.AssetID).Msg("uploaded media to reddit")
	return mediaURL, nil
}

// redditActionURL gives the lease's upload target a scheme.
//
// Reddit returns the object store as a protocol-relative "//host" URL, which
// is not something Go's HTTP client will accept.
func redditActionURL(action string) string {
	if strings.HasPrefix(action, "//") {
		return "https:" + action
	}
	return action
}

// findPermalink looks for the post that was just made.
//
// Reddit used to report this over a websocket, which has been unreliable since
// 2023; reading the account's own recent submissions is slower and works.
func (r *Reddit) findPermalink(ctx context.Context, token, title string) (link, id string) {
	who := strings.TrimPrefix(strings.TrimSpace(r.cfg.Username), "u/")
	if who == "" {
		return "", ""
	}
	interval := config.Duration(r.cfg.PollInterval)
	timeout := config.Duration(r.cfg.PollTimeout)
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	err := httpx.Poll(ctx, interval, timeout, func(ctx context.Context) (bool, error) {
		var out redditListing
		req := r.authed(http.MethodGet, token, "user", who, "submitted").Query("limit", "1")
		if err := r.client.JSON(ctx, req, &out); err != nil {
			return false, err
		}
		if len(out.Data.Children) == 0 {
			return false, nil
		}
		post := out.Data.Children[0].Data
		if title != "" && post.Title != title {
			return false, nil
		}
		if post.Permalink != "" {
			link = "https://www.reddit.com" + post.Permalink
		} else {
			link = post.URL
		}
		id = firstNonEmpty(post.Name, post.ID)
		return true, nil
	})
	if err != nil {
		r.log.Warn().Err(err).Msg("could not find the reddit post's permalink; the post itself was made")
		return "", ""
	}
	return link, id
}

// Ping gets a token and reads the account it belongs to.
//
// The token grant is the half of a Reddit setup that goes wrong — a password
// grant against an account with two-factor authentication, a client secret
// from the wrong app — so getting one is most of the check. /api/v1/me then
// confirms the token is good for a read and names the user.
func (r *Reddit) Ping(ctx context.Context) (Identity, error) {
	token, err := r.token(ctx)
	if err != nil {
		return Identity{}, err
	}
	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := r.client.JSON(ctx, r.authed(http.MethodGet, token, "api/v1/me"), &out); err != nil {
		return Identity{}, err
	}
	id := Identity{ID: out.ID, Name: "u/" + out.Name}
	if s := strings.TrimPrefix(r.cfg.Subreddit, "r/"); s != "" {
		id.Note = "posting to r/" + s
	}
	return id, nil
}
