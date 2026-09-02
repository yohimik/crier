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

// DiscordFileMax is how many files crier will put in one webhook message.
//
// Discord documents the files[n] mechanism and no limit on the count, so ten
// is crier's own ceiling. It is the number Discord's own clients settle on and
// the one its Media Gallery component documents, which makes it the safest
// guess available.
const DiscordFileMax = 10

// Needs implements Publisher.
func (d *Discord) Needs() Needs {
	return Needs{
		Formats:        []config.Format{config.PNG, config.JPEG},
		Kinds:          imageVideoAndGIF,
		MaxAttachments: DiscordFileMax,
	}
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
	arts := in.Sequence()
	if len(arts) > DiscordFileMax {
		return Result{}, fmt.Errorf("a discord message carries %d files and this post has %d",
			DiscordFileMax, len(arts))
	}
	payload, err := json.Marshal(discordMessage{Content: in.Caption, Username: d.cfg.Username})
	if err != nil {
		return Result{}, err
	}

	// files[0], files[1] and so on: the index is the order Discord lays the
	// attachments out in, which is the page order.
	parts := []httpx.Part{httpx.Field("payload_json", string(payload))}
	for n, a := range arts {
		if err := checkSize(a, DiscordUploadLimit, "discord"); err != nil {
			return Result{}, err
		}
		parts = append(parts, httpx.FilePart(fmt.Sprintf("files[%d]", n), a.Path, a.ContentType))
	}

	req := httpx.NewRequest(http.MethodPost, d.cfg.WebhookURL).
		// wait=true makes Discord return the message it created, which is the
		// only way to report an id.
		Query("wait", "true").
		Multipart(parts...)

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

// Ping reads the webhook back.
//
// A webhook URL is the whole credential, and a GET on it returns the webhook
// itself — so the check is whether the URL still exists, which is the only
// thing that can be wrong with a Discord setup.
func (d *Discord) Ping(ctx context.Context) (Identity, error) {
	var out struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ChannelID string `json:"channel_id"`
	}
	if err := d.client.JSON(ctx, httpx.NewRequest(http.MethodGet, d.cfg.WebhookURL), &out); err != nil {
		return Identity{}, err
	}
	id := Identity{ID: out.ID, Name: out.Name}
	if out.ChannelID != "" {
		id.Note = "channel " + out.ChannelID
	}
	return id, nil
}
