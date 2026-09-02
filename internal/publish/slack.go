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
	music  AudioFile
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
	music, err := MusicFor(cfg, "slack")
	if err != nil {
		return nil, err
	}
	return &Slack{cfg: c, music: music, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (s *Slack) Name() string { return "slack" }

// Needs implements Publisher.
//
// Slack takes the bytes, so it needs no staging, and it renders an animation
// inline like any other image.
//
// The audio is one of the files the message shares, so a run with music
// declares one fewer page per post and a long document paginates into messages
// that fit.
func (s *Slack) Needs() Needs {
	max := SlackFileMax
	if s.music.Attached() {
		max--
	}
	return Needs{Formats: imageFormats, Kinds: imageVideoAndGIF, MaxAttachments: max}
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

// SlackFileMax is how many files crier will share in one call.
//
// Steps 1 and 2 are per file; step 3 takes the lot, which is what makes
// several files land as one message rather than as several. Slack documents
// neither a maximum for the files array nor the one-message behaviour, so ten
// is crier's own ceiling.
const SlackFileMax = 10

// slackUpload is one file on its way through the three steps: a path and the
// length Slack sizes the slot from.
//
// The pictures and the audio go through it identically, which is the whole
// reason it exists: step 3 takes them as one list, so they land as one message
// with the track playing under the pictures.
type slackUpload struct {
	Path string
	Size int64
}

// Publish runs the three-step external upload.
func (s *Slack) Publish(ctx context.Context, in Input) (Result, error) {
	arts := in.Sequence()
	uploads := make([]slackUpload, 0, len(arts)+1)
	for _, a := range arts {
		uploads = append(uploads, slackUpload{Path: a.Path, Size: a.Size})
	}
	if s.music.Attached() {
		uploads = append(uploads, slackUpload{Path: s.music.Path, Size: s.music.Size})
		s.log.Debug().Str("audio", s.music.Name).Str("format", s.music.Format).
			Msg("sharing the audio in the same slack message")
	}
	if len(uploads) > SlackFileMax {
		if s.music.Attached() {
			return Result{}, fmt.Errorf(
				"a slack message shares %d files and this post has %d pages plus the audio; "+
					"lower publish.slack.max-attachments", SlackFileMax, len(arts))
		}
		return Result{}, fmt.Errorf("a slack message shares %d files and this post has %d",
			SlackFileMax, len(arts))
	}

	uploaded := make([]map[string]string, 0, len(uploads))
	firstID := ""
	for n, a := range uploads {
		name := filepath.Base(a.Path)

		// 1. Ask for somewhere to put the bytes. The length is not advisory:
		//    Slack sizes the slot from it and refuses a body that disagrees.
		var slot slackUploadURL
		req := httpx.NewRequest(http.MethodPost, s.cfg.APIBaseURL, "files.getUploadURLExternal").
			Bearer(s.cfg.Token).
			Form(url.Values{
				"filename": {name},
				"length":   {strconv.FormatInt(a.Size, 10)},
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
		s.log.Debug().Str("file", slot.FileID).Int("of", len(uploads)).
			Msg("slack handed out an upload url")

		// 2. The bytes, raw, to the URL Slack named. No token: the URL is the
		//    credential, and sending an Authorization header to it is how a
		//    request ends up rejected by a host that never wanted one.
		upload := httpx.NewRequest(http.MethodPost, slot.UploadURL).
			File("application/octet-stream", a.Path)
		if err := s.client.Discard(ctx, upload); err != nil {
			if len(uploads) > 1 {
				return Result{}, fmt.Errorf("uploading file %d of %d to slack: %w", n+1, len(uploads), err)
			}
			return Result{}, fmt.Errorf("uploading the file to slack: %w", err)
		}
		if firstID == "" {
			firstID = slot.FileID
		}
		uploaded = append(uploaded, map[string]string{"id": slot.FileID, "title": name})
	}

	// 3. Tell Slack what the files are for, in page order. This is the step
	//    that posts, so it is the one that must not be retried: a 5xx from a
	//    gateway may still have shared them, and repeating it would share them
	//    twice.
	files, err := json.Marshal(uploaded)
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

	id := firstID
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
