package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
)

// Slack posts a file to a channel.
//
// Three calls rather than one, because the one-call method is gone:
// files.upload was retired and its replacement hands out a short-lived upload
// URL, takes the bytes at that URL, and is then told what to do with the file.
// The upload itself carries no credential — the URL is the credential — which
// is why the token is only on the first and third calls.
//
// Verified against Slack's method reference on 2026-09-01.
type Slack struct {
	cfg    config.Slack
	client *httpx.Client
	log    zerolog.Logger
}

func newSlack(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.Slack
	if err := require(c.APIBaseURL, "publish.slack.api-base-url"); err != nil {
		return nil, err
	}
	if err := require(c.Token, "publish.slack.token"); err != nil {
		return nil, err
	}
	if err := require(c.Channel, "publish.slack.channel"); err != nil {
		return nil, err
	}
	return &Slack{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (s *Slack) Name() string { return "slack" }

// Needs implements Publisher.
//
// Slack takes the bytes, so it needs no staging, and it renders an animation
// inline like any other image.
func (s *Slack) Needs() Needs {
	return Needs{Formats: imageFormats, Kinds: imageVideoAndGIF}
}

// slackResponse is the envelope every Web API method returns.
//
// Slack answers 200 with ok:false for an application error — a bad token, a
// channel the bot is not in — so the status code alone says nothing.
type slackResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	// Warning carries a non-fatal note, such as a deprecated argument.
	Warning string `json:"warning"`
}

// err turns an ok:false envelope into an error worth reading.
func (r slackResponse) err(what string) error {
	reason := r.Error
	if reason == "" {
		reason = "no reason given"
	}
	switch reason {
	case "not_in_channel":
		return fmt.Errorf("%s: the bot is not in that channel; invite it with /invite @your-app", what)
	case "invalid_auth", "not_authed", "token_revoked":
		return fmt.Errorf("%s: slack refused the token (%s); check publish.slack.token", what, reason)
	case "missing_scope":
		return fmt.Errorf("%s: the token is missing a scope; it needs files:write and chat:write", what)
	case "channel_not_found":
		return fmt.Errorf("%s: no such channel; publish.slack.channel wants a channel ID like C0123ABCD, "+
			"not a name", what)
	default:
		return fmt.Errorf("%s: slack said %s", what, reason)
	}
}

type slackUploadURL struct {
	slackResponse
	UploadURL string `json:"upload_url"`
	FileID    string `json:"file_id"`
}

type slackComplete struct {
	slackResponse
	Files []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"files"`
	// Only present when the file was shared into a channel.
	Channels []string `json:"channels"`
}

// Publish runs the three-step external upload.
func (s *Slack) Publish(ctx context.Context, in Input) (Result, error) {
	name := filepath.Base(in.Artifact.Path)

	// 1. Ask for somewhere to put the bytes. The length is not advisory:
	//    Slack sizes the slot from it and refuses a body that disagrees.
	var slot slackUploadURL
	req := httpx.NewRequest(http.MethodPost, s.cfg.APIBaseURL, "files.getUploadURLExternal").
		Bearer(s.cfg.Token).
		Form(url.Values{
			"filename": {name},
			"length":   {strconv.FormatInt(in.Artifact.Size, 10)},
		})
	if err := s.client.JSON(ctx, req, &slot); err != nil {
		return Result{}, err
	}
	if !slot.OK {
		return Result{}, slot.err("asking slack where to upload")
	}
	if slot.UploadURL == "" || slot.FileID == "" {
		return Result{}, fmt.Errorf("slack returned no upload url")
	}
	s.log.Debug().Str("file", slot.FileID).Msg("slack handed out an upload url")

	// 2. The bytes, raw, to the URL Slack named. No token: the URL is the
	//    credential, and sending an Authorization header to it is how a
	//    request ends up rejected by a host that never wanted one.
	upload := httpx.NewRequest(http.MethodPost, slot.UploadURL).
		File("application/octet-stream", in.Artifact.Path)
	if err := s.client.Discard(ctx, upload); err != nil {
		return Result{}, fmt.Errorf("uploading the file to slack: %w", err)
	}

	// 3. Tell Slack what the file is for. This is the step that posts, so it
	//    is the one that must not be retried: a 5xx from a gateway may still
	//    have shared the file, and repeating it would share it twice.
	files, err := json.Marshal([]map[string]string{{"id": slot.FileID, "title": name}})
	if err != nil {
		return Result{}, err
	}
	form := url.Values{
		"files":      {string(files)},
		"channel_id": {s.cfg.Channel},
	}
	if in.Caption != "" {
		form.Set("initial_comment", in.Caption)
	}

	var done slackComplete
	complete := httpx.NewRequest(http.MethodPost, s.cfg.APIBaseURL, "files.completeUploadExternal").
		Bearer(s.cfg.Token).
		Form(form)
	if err := s.client.NoRetry().JSON(ctx, complete, &done); err != nil {
		return Result{}, err
	}
	if !done.OK {
		return Result{}, done.err("sharing the file")
	}

	id := slot.FileID
	if len(done.Files) > 0 && done.Files[0].ID != "" {
		id = done.Files[0].ID
	}
	return Result{
		ID:    id,
		URL:   s.permalink(id),
		Extra: map[string]string{"channel": s.cfg.Channel},
	}, nil
}

// permalink is where the file lives, when the workspace is known.
//
// Slack does not return a permalink from completeUploadExternal, and the file
// id alone is not a URL anyone can open. The archives form needs the workspace
// host, which only auth.test knows — so rather than guess, this reports
// nothing and leaves the id, which is what the API gave.
func (s *Slack) permalink(string) string { return "" }

// Ping asks Slack who the token belongs to.
//
// auth.test needs no scope at all, which makes it the right check: it
// distinguishes a token Slack has never heard of from one that merely cannot
// do what crier wants.
func (s *Slack) Ping(ctx context.Context) (Identity, error) {
	var out struct {
		slackResponse
		Team   string `json:"team"`
		TeamID string `json:"team_id"`
		User   string `json:"user"`
		UserID string `json:"user_id"`
		BotID  string `json:"bot_id"`
	}
	req := httpx.NewRequest(http.MethodPost, s.cfg.APIBaseURL, "auth.test").Bearer(s.cfg.Token)
	if err := s.client.JSON(ctx, req, &out); err != nil {
		return Identity{}, err
	}
	if !out.OK {
		return Identity{}, out.err("checking the slack token")
	}

	id := Identity{ID: firstNonEmpty(out.UserID, out.BotID, out.TeamID)}
	switch {
	case out.User != "" && out.Team != "":
		id.Name = out.User + " in " + out.Team
	case out.User != "":
		id.Name = out.User
	default:
		id.Name = out.Team
	}
	if s.cfg.Channel != "" {
		// auth.test says nothing about the channel, and being in it is the
		// other half of a working setup — worth saying which one was checked
		// and which was not.
		id.Note = "posting to " + s.cfg.Channel + "; auth.test cannot confirm the bot is in it"
	}
	if out.BotID == "" {
		id.Note = strings.TrimSpace(id.Note + " (this is a user token, not a bot token)")
	}
	return id, nil
}
