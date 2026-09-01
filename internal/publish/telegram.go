package publish

import (
	"context"
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
	return &Telegram{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (t *Telegram) Name() string { return "telegram" }

// Needs implements Publisher.
func (t *Telegram) Needs() Needs {
	return Needs{Formats: imageFormats, Kinds: imageAndVideo}
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
func (t *Telegram) Publish(ctx context.Context, in Input) (Result, error) {
	method, field := "sendPhoto", "photo"
	limit := int64(TelegramPhotoLimit)
	if in.Artifact.Kind == render.KindVideo {
		method, field = "sendVideo", "video"
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
