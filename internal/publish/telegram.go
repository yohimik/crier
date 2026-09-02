package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

// TelegramPhotoLimit is the largest photo the Bot API accepts as an upload.
const TelegramPhotoLimit = 10 << 20

// TelegramVideoLimit is the largest file the Bot API accepts at all.
const TelegramVideoLimit = 50 << 20

// Telegram posts through the Bot API.
//
// It is the simplest of the platforms: one multipart request, no container, no
// polling, and it takes the bytes rather than a URL.
type Telegram struct {
	cfg    config.Telegram
	music  AudioFile
	lead   VideoFile
	client *httpx.Client
	log    zerolog.Logger
}

func newTelegram(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.Telegram
	if err := require(c.Token, "publish.telegram.token"); err != nil {
		return nil, err
	}
	if err := require(c.ChatID, "publish.telegram.chat-id"); err != nil {
		return nil, err
	}
	if err := require(c.APIBaseURL, "publish.telegram.api-base-url"); err != nil {
		return nil, err
	}
	music, err := MusicFor(cfg, "telegram")
	if err != nil {
		return nil, err
	}
	if music.Attached() && !telegramPlaysInline(music.Format) {
		// The Bot API documents sendAudio as taking .MP3 or .M4A. Anything else
		// is usually accepted and then shown as a file rather than as a player,
		// which is a disappointment worth predicting rather than a failure.
		d.Logger.Warn().Str("platform", "telegram").Str("audio", music.Name).
			Str("format", music.Format).
			Msg("telegram documents mp3 and m4a for a music player; this may arrive as a plain file")
	}
	lead, err := LeadVideoFor(cfg, "telegram")
	if err != nil {
		return nil, err
	}
	return &Telegram{cfg: c, music: music, lead: lead, client: d.Client, log: d.Logger}, nil
}

// telegramPlaysInline reports whether the Bot API names this container as one
// sendAudio takes.
func telegramPlaysInline(format string) bool { return format == "mp3" || format == "m4a" }

// Name implements Publisher.
func (t *Telegram) Name() string { return "telegram" }

// TelegramGroupMax is how many items one sendMediaGroup call takes.
//
// The method also has a minimum of two, which is why a batch of one falls back
// to sendPhoto rather than sending a group of one.
const TelegramGroupMax = 10

// Needs implements Publisher.
//
// A lead video is one of the album's ten items, so a run that opens with one
// declares nine pages per post. The audio takes nothing away: it is a message
// of its own, so an album can carry both a lead video and the track after it.
func (t *Telegram) Needs() Needs {
	max := TelegramGroupMax
	if t.lead.Attached() {
		max--
	}
	return Needs{Formats: imageFormats, Kinds: imageVideoAndGIF, MaxAttachments: max}
}

// telegramResponse is the envelope every Bot API method returns.
type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		MessageID int64 `json:"message_id"`
		Chat      struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"chat"`
	} `json:"result"`
}

// Publish sends the artifact to the chat.
//
// Several images go as one album through sendMediaGroup. One goes through
// sendPhoto: a media group has a minimum of two, so the last batch of an odd
// page count would be refused as a group of one.
func (t *Telegram) Publish(ctx context.Context, in Input) (Result, error) {
	res, err := t.post(ctx, in)
	if err != nil {
		return res, err
	}
	t.sendMusic(ctx, &res)
	return res, nil
}

// post sends the pictures themselves.
func (t *Telegram) post(ctx context.Context, in Input) (Result, error) {
	// A lead video makes an album of any post: the clip and the card are two
	// items, and a media group takes exactly the mix of photo and video that
	// this is.
	if arts := in.Sequence(); len(arts) > 1 || t.lead.Attached() {
		return t.mediaGroup(ctx, arts, in.Caption)
	}
	method, field := "sendPhoto", "photo"
	limit := int64(TelegramPhotoLimit)
	switch in.Artifact.Kind {
	case render.KindVideo:
		method, field = "sendVideo", "video"
		limit = TelegramVideoLimit
	case render.KindGIF:
		// sendAnimation, not sendVideo. sendVideo with a GIF is accepted and
		// then shown as a still: Telegram converts an animation to MPEG4 and
		// only the animation method keeps it playing.
		method, field = "sendAnimation", "animation"
		limit = TelegramVideoLimit
	}
	if err := checkSize(in.Artifact, limit, "telegram"); err != nil {
		return Result{}, err
	}

	parts := []httpx.Part{
		httpx.Field("chat_id", t.cfg.ChatID),
		httpx.FilePart(field, in.Artifact.Path, in.Artifact.ContentType),
	}
	if in.Caption != "" {
		parts = append(parts, httpx.Field("caption", in.Caption))
	}
	if in.Artifact.Kind == render.KindVideo {
		parts = append(parts, httpx.Field("supports_streaming", "true"))
	}

	req := httpx.NewRequest(http.MethodPost, t.cfg.APIBaseURL, "bot"+t.cfg.Token, method).
		Multipart(parts...)

	// Sending a message is the act itself: a 5xx may mean it went out and the
	// answer was lost, and a retry would post twice.
	var out telegramResponse
	if err := t.client.NoRetry().JSON(ctx, req, &out); err != nil {
		return Result{}, err
	}
	if !out.OK {
		return Result{}, fmt.Errorf("telegram refused the message: %s", out.Description)
	}

	res := Result{ID: strconv.FormatInt(out.Result.MessageID, 10)}
	if u := out.Result.Chat.Username; u != "" {
		res.URL = fmt.Sprintf("https://t.me/%s/%d", strings.TrimPrefix(u, "@"), out.Result.MessageID)
	}
	return res, nil
}

// telegramGroupResponse is what sendMediaGroup answers with: one message per
// item rather than the single message every other method returns.
type telegramGroupResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      []struct {
		MessageID int64 `json:"message_id"`
		Chat      struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"chat"`
	} `json:"result"`
}

// telegramMedia is one entry of the media array sendMediaGroup takes.
type telegramMedia struct {
	Type    string `json:"type"`
	Media   string `json:"media"`
	Caption string `json:"caption,omitempty"`
}

// mediaGroup posts several images as one album.
//
// The files ride along as ordinary multipart parts and the media array points
// at them by name, which is what the attach:// scheme is for.
//
// A lead video is the album's first entry, an InputMediaVideo among the
// photos. A media group is the one Telegram shape that mixes the two, which is
// why the clip can open the album here and cannot open anything at eight of
// the ten platforms.
//
// Only the first item carries the caption. The Bot API has no caption of its
// own for a group — the caption belongs to an item — and every client shows an
// album's caption from the first one, so putting it anywhere else hides it.
// With a lead video the first item is the clip, so the caption travels with it
// and appears under the album exactly as before.
func (t *Telegram) mediaGroup(ctx context.Context, arts []render.Artifact, caption string) (Result, error) {
	items := len(arts)
	if t.lead.Attached() {
		items++
	}
	if items > TelegramGroupMax {
		if t.lead.Attached() {
			return Result{}, fmt.Errorf(
				"a telegram media group holds %d items and this post has %d pages plus the lead video; "+
					"lower publish.telegram.max-attachments", TelegramGroupMax, len(arts))
		}
		return Result{}, fmt.Errorf("a telegram media group holds %d items and this post has %d",
			TelegramGroupMax, len(arts))
	}

	parts := []httpx.Part{httpx.Field("chat_id", t.cfg.ChatID)}
	media := make([]telegramMedia, 0, items)
	if t.lead.Attached() {
		if err := checkSizeOf(t.lead.Path, t.lead.Size, TelegramVideoLimit, "telegram"); err != nil {
			return Result{}, err
		}
		parts = append(parts, t.lead.Part("lead"))
		m := telegramMedia{Type: "video", Media: "attach://lead", Caption: caption}
		media = append(media, m)
		// The caption belongs to the album's first item, and that is now the
		// clip, so the pictures carry none.
		caption = ""
		t.log.Debug().Str("video", t.lead.Name).Msg("the telegram album opens with the lead video")
	}
	for n, a := range arts {
		if a.Kind != render.KindImage {
			return Result{}, fmt.Errorf("a telegram media group takes photos; item %d is %s", n+1, a.Kind)
		}
		if err := checkSize(a, TelegramPhotoLimit, "telegram"); err != nil {
			return Result{}, err
		}
		name := fmt.Sprintf("photo%d", n)
		parts = append(parts, httpx.FilePart(name, a.Path, a.ContentType))
		m := telegramMedia{Type: "photo", Media: "attach://" + name}
		if n == 0 {
			m.Caption = caption
		}
		media = append(media, m)
	}
	encoded, err := json.Marshal(media)
	if err != nil {
		return Result{}, fmt.Errorf("describing the media group: %w", err)
	}
	parts = append(parts, httpx.Field("media", string(encoded)))

	req := httpx.NewRequest(http.MethodPost, t.cfg.APIBaseURL, "bot"+t.cfg.Token, "sendMediaGroup").
		Multipart(parts...)

	// As with a single message: a 5xx may mean the album went out and the
	// answer was lost, and a retry would post it twice.
	var out telegramGroupResponse
	if err := t.client.NoRetry().JSON(ctx, req, &out); err != nil {
		return Result{}, err
	}
	if !out.OK {
		return Result{}, fmt.Errorf("telegram refused the media group: %s", out.Description)
	}
	if len(out.Result) == 0 {
		return Result{}, fmt.Errorf("telegram accepted the media group but named no messages")
	}
	t.log.Debug().Int("items", len(arts)).Int("messages", len(out.Result)).
		Msg("posted a telegram media group")

	first := out.Result[0]
	res := Result{
		ID:    strconv.FormatInt(first.MessageID, 10),
		Extra: map[string]string{"items": strconv.Itoa(len(out.Result))},
	}
	if u := first.Chat.Username; u != "" {
		res.URL = fmt.Sprintf("https://t.me/%s/%d", strings.TrimPrefix(u, "@"), first.MessageID)
	}
	return res, nil
}

// sendMusic sends the audio as its own message, straight after the post.
//
// It has to be its own message. The Bot API groups audio only with audio:
// "Documents and audio files can be only grouped in an album with messages of
// the same type", so a track cannot join the album it belongs to. An adjacent
// message is the closest thing available, and it is what clients render as a
// player under the pictures — which is why it goes out immediately after, in
// the same chat, rather than as a reply that would arrive quoted.
//
// A failure here is a warning rather than a failure of the post. The pictures
// and the caption are already published; reporting the platform as failed
// would say something untrue, and there is no way to take them back.
func (t *Telegram) sendMusic(ctx context.Context, res *Result) {
	if !t.music.Attached() {
		return
	}
	if t.music.Size > TelegramVideoLimit {
		t.log.Warn().Str("audio", t.music.Path).Int64("bytes", t.music.Size).
			Msg("the audio is over telegram's 50MB limit and was not sent; the post itself went out")
		return
	}

	req := httpx.NewRequest(http.MethodPost, t.cfg.APIBaseURL, "bot"+t.cfg.Token, "sendAudio").
		Multipart(
			httpx.Field("chat_id", t.cfg.ChatID),
			t.music.Part("audio"),
		)

	var out telegramResponse
	if err := t.client.NoRetry().JSON(ctx, req, &out); err != nil {
		t.log.Warn().Err(err).Str("audio", t.music.Name).
			Msg("the post went out but the audio message did not")
		return
	}
	if !out.OK {
		t.log.Warn().Str("audio", t.music.Name).Str("reason", out.Description).
			Msg("the post went out but telegram refused the audio message")
		return
	}
	id := strconv.FormatInt(out.Result.MessageID, 10)
	t.log.Info().Str("audio", t.music.Name).Str("message", id).
		Msg("sent the audio under the telegram post")
	if res.Extra == nil {
		res.Extra = map[string]string{}
	}
	res.Extra["audioMessageId"] = id
}

// Ping asks the Bot API who the bot is.
//
// getMe is the cheapest call there is and needs no permission beyond the token
// itself, which is exactly what a credential check wants.
func (t *Telegram) Ping(ctx context.Context) (Identity, error) {
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Name     string `json:"first_name"`
		} `json:"result"`
	}
	req := httpx.NewRequest(http.MethodGet, t.cfg.APIBaseURL, "bot"+t.cfg.Token, "getMe")
	if err := t.client.JSON(ctx, req, &out); err != nil {
		return Identity{}, err
	}
	if !out.OK {
		return Identity{}, fmt.Errorf("telegram refused the token: %s", out.Description)
	}
	name := firstNonEmpty("@"+strings.TrimPrefix(out.Result.Username, "@"), out.Result.Name)
	if out.Result.Username == "" {
		name = out.Result.Name
	}
	return Identity{ID: strconv.FormatInt(out.Result.ID, 10), Name: name}, nil
}
