package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
)

// DiscordUploadLimit is the attachment size a webhook on a free server takes.
// A boosted server allows more, which is why it is only checked here to give a
// clear error rather than being a hard rule.
const DiscordUploadLimit = 10 << 20

// Discord posts through an incoming webhook.
//
// The webhook URL is the whole credential — anyone holding it can post — which
// is why it is the secret rather than a token beside it.
type Discord struct {
	cfg    config.Discord
	client *httpx.Client
	log    zerolog.Logger
}

func newDiscord(cfg *config.Config, d Deps) (Publisher, error) {
	c := cfg.Publish.Discord
	if err := require(c.WebhookURL, "publish.discord.webhook-url"); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(c.WebhookURL, "http://") && !strings.HasPrefix(c.WebhookURL, "https://") {
		return nil, fmt.Errorf("publish.discord.webhook-url must be the full webhook URL")
	}
	return &Discord{cfg: c, client: d.Client, log: d.Logger}, nil
}

// Name implements Publisher.
func (d *Discord) Name() string { return "discord" }

// Needs implements Publisher.
func (d *Discord) Needs() Needs {
	return Needs{Formats: []config.Format{config.PNG, config.JPEG}, Kinds: imageAndVideo}
}

// discordMessage is the JSON half of the multipart request.
type discordMessage struct {
	Content  string `json:"content,omitempty"`
	Username string `json:"username,omitempty"`
}

// discordResponse is what ?wait=true returns.
type discordResponse struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
}

// Publish posts the artifact to the webhook.
func (d *Discord) Publish(ctx context.Context, in Input) (Result, error) {
	if err := checkSize(in.Artifact, DiscordUploadLimit, "discord"); err != nil {
		return Result{}, err
	}
	payload, err := json.Marshal(discordMessage{Content: in.Caption, Username: d.cfg.Username})
	if err != nil {
		return Result{}, err
	}

	req := httpx.NewRequest(http.MethodPost, d.cfg.WebhookURL).
		// wait=true makes Discord return the message it created, which is the
		// only way to report an id.
		Query("wait", "true").
		Multipart(
			httpx.Field("payload_json", string(payload)),
			httpx.FilePart("files[0]", in.Artifact.Path, in.Artifact.ContentType),
		)

	var out discordResponse
	if err := d.client.NoRetry().JSON(ctx, req, &out); err != nil {
		return Result{}, err
	}
	res := Result{ID: out.ID}
	if out.ChannelID != "" && out.ID != "" {
		res.URL = fmt.Sprintf("https://discord.com/channels/@me/%s/%s", out.ChannelID, out.ID)
		res.Extra = map[string]string{"channelId": out.ChannelID}
	}
	return res, nil
}
