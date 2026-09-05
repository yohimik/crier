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
	music  AudioFile
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
	music, err := MusicFor(cfg, "discord")
	if err != nil {
		return nil, err
	}
	return &Discord{cfg: c, music: music, client: d.Client, log: d.Logger}, nil
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
//
// The audio takes one of the message's file slots, so a run with music
// declares one fewer page per post. Reserving it here rather than refusing the
// post later is what makes a long document paginate into messages that fit.
func (d *Discord) Needs() Needs {
	max := DiscordFileMax
	if d.music.Attached() {
		max--
	}
	return Needs{
		Formats:        []config.Format{config.PNG, config.JPEG},
		Kinds:          imageVideoAndGIF,
		MaxAttachments: max,
	}
}

// discordMessage is the JSON half of the multipart request.
type discordMessage struct {
	Content         string                  `json:"content,omitempty"`
	Username        string                  `json:"username,omitempty"`
	AllowedMentions *discordAllowedMentions `json:"allowed_mentions,omitempty"`
}

type discordAllowedMentions struct {
	Parse []string `json:"parse"`
}

// discordResponse is what ?wait=true returns.
type discordResponse struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
}

// Publish posts the artifact to the webhook.
//
// The audio, when there is one, is another file in the same message rather
// than a message of its own. Discord's clients give an audio attachment a
// player, so the track sits under the pictures it belongs to.
func (d *Discord) Publish(ctx context.Context, in Input) (Result, error) {
	arts := in.Sequence()
	files := len(arts)
	if d.music.Attached() {
		files++
	}
	if files > DiscordFileMax {
		if d.music.Attached() {
			return Result{}, fmt.Errorf(
				"a discord message carries %d files and this post has %d pages plus the audio; "+
					"lower publish.discord.max-attachments", DiscordFileMax, len(arts))
		}
		return Result{}, fmt.Errorf("a discord message carries %d files and this post has %d",
			DiscordFileMax, len(arts))
	}
	message := discordMessage{Content: in.Caption, Username: d.cfg.Username}
	if d.cfg.MentionEveryone {
		// Webhooks parse only user mentions by default. Opting in is explicit:
		// Discord will notify @everyone in Content only when this list says so.
		message.AllowedMentions = &discordAllowedMentions{Parse: []string{"everyone"}}
	}
	payload, err := json.Marshal(message)
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
	if d.music.Attached() {
		if err := checkSizeOf(d.music.Path, d.music.Size, DiscordUploadLimit, "discord"); err != nil {
			return Result{}, err
		}
		// The audio comes last, so it reads as an addition to the pictures
		// rather than as the first thing in the message.
		parts = append(parts, d.music.Part(fmt.Sprintf("files[%d]", len(arts))))
		d.log.Debug().Str("audio", d.music.Name).Str("format", d.music.Format).
			Msg("attaching the audio to the discord message")
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
