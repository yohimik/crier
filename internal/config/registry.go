package config

import (
	"fmt"
	"sort"
	"strings"

	dispat "github.com/yohimik/dispat/pkg/config"
)

// EnvPrefix is the namespace every crier environment variable carries.
const EnvPrefix = "CRIER_"

// Kind is the shape of a configuration value, which decides how it is spelled
// on the command line and in the generated documentation.
type Kind uint8

const (
	// KindString is a plain text value.
	KindString Kind = iota
	// KindInt is a whole number.
	KindInt
	// KindBool is a boolean; its flag needs no argument.
	KindBool
	// KindStrings is a list, written as a comma separated string on the
	// command line and in the environment, or as a list in the config file.
	KindStrings
	// KindDuration is a Go duration string such as "1h30m" or "500ms".
	KindDuration
	// KindFloat is a decimal number, kept as text because the decoder has no
	// float setter.
	KindFloat
)

// String renders the kind the way docs and flag usage strings name it.
func (k Kind) String() string {
	switch k {
	case KindString:
		return "string"
	case KindInt:
		return "int"
	case KindBool:
		return "bool"
	case KindStrings:
		return "list"
	case KindDuration:
		return "duration"
	case KindFloat:
		return "float"
	default:
		return "unknown"
	}
}

// Descriptor declares one configuration key, once, for all three layers.
type Descriptor struct {
	// Key is the dotted, kebab-cased path of the value: "render.jpeg-quality".
	Key string
	// Kind is the value's shape.
	Kind Kind
	// Default is the value used when no layer sets the key. An empty string
	// means the Go zero value.
	Default string
	// Usage is the one line description shown by --help and in docs/config.md.
	Usage string
	// Secret marks a credential, which is redacted by `crier config`.
	Secret bool
	// Path marks a value that names a file or a directory. A relative path
	// written in a config file resolves against that file's own directory,
	// which is what lets a project keep its template next to its config; a
	// relative path given as a flag or an environment variable resolves
	// against the working directory, because that is where it was typed.
	Path bool
}

// FlagName is the command line flag for a key: dots become dashes.
func (d Descriptor) FlagName() string { return FlagName(d.Key) }

// EnvName is the environment variable for a key.
func (d Descriptor) EnvName() string { return EnvName(d.Key) }

// FlagName converts a config key into its flag name (without leading dashes).
func FlagName(key string) string { return strings.ReplaceAll(key, ".", "-") }

// EnvName converts a config key into its environment variable name.
func EnvName(key string) string {
	return dispat.EnvVarName(EnvPrefix, key, dispat.DefaultKeyDelim)
}

// Platforms are every publisher crier knows, in the order they are documented
// and reported. It is the list the per-platform layout keys are generated from
// and the list the publisher registry is built against.
var Platforms = []string{
	"instagram", "facebook", "tiktok", "telegram", "x",
	"slack",
	"mastodon", "discord", "linkedin", "reddit",
	"vk",
	"threads",
	"youtube",
	"boosty",
}

// MusicPlatforms are the platforms whose API can carry an audio file beside
// the pictures, in the order they are documented.
//
// The list is short because the APIs are. Discord and Slack take several files
// in one message, so the audio is one of them. Telegram takes an adjacent
// message, which its clients render as a player under the album. Nothing else
// offers a way in: the licensed-music pickers Instagram, Facebook and TikTok
// show inside their apps have no public endpoint at all.
var MusicPlatforms = []string{"discord", "slack", "telegram"}

// CanCarryMusic reports whether a platform accepts an audio file.
func CanCarryMusic(platform string) bool {
	for _, name := range MusicPlatforms {
		if name == platform {
			return true
		}
	}
	return false
}

// LeadVideoPlatforms are the platforms whose API takes a post of mixed media,
// so a clip can be item one of something whose other items are pictures.
//
// Two, and for two different reasons. An Instagram feed carousel takes video
// children beside image children. A Telegram media group mixes InputMediaPhoto
// and InputMediaVideo in the one array. Everywhere else a post is pictures or
// it is a video, and there is no arrangement of API calls that makes it both.
var LeadVideoPlatforms = []string{"instagram", "telegram"}

// CanCarryLeadVideo reports whether a platform accepts a clip at the head of a
// multi-file post.
func CanCarryLeadVideo(platform string) bool {
	for _, name := range LeadVideoPlatforms {
		if name == platform {
			return true
		}
	}
	return false
}

// Aliases are extra, shorter flag names for keys people type often. They are
// documented alongside their key and resolve to exactly the same value.
var Aliases = map[string]string{
	"dry-run":  "publish.dry-run",
	"caption":  "publish.caption",
	"template": "render.template",
	"data":     "render.data",
	"output":   "render.output",
	"width":    "render.width",
	"height":   "render.height",
	"scale":    "render.scale",
	"format":   "render.format",
}

// registry is the single source of truth for the configuration surface.
//
// Adding a key here makes it settable from the file, the environment and the
// command line, gives it a default, and puts it in docs/config.md. It must
// also be bound to a struct field in fields.go; the anti-drift tests fail
// loudly when the two disagree.
var registry = []Descriptor{
	{Key: "log.level", Kind: KindString, Default: "info", Usage: "log level: trace, debug, info, warn, error"},
	{Key: "log.format", Kind: KindString, Default: "console", Usage: "log format: console or json"},

	{Key: "render.template", Kind: KindString, Path: true, Usage: "path to the Go html/template file to render"},
	{Key: "render.data", Kind: KindString, Path: true, Usage: `where the template's data comes from: a JSON or YAML file, "-" for stdin, or "env:PREFIX" to build it from the environment`},
	{Key: "render.css", Kind: KindStrings, Path: true, Usage: "extra stylesheet files applied after the document's own CSS"},
	{Key: "render.overlays", Kind: KindStrings, Path: true, Usage: `template files parsed after the base one, redefining its {{block}} sections`},
	{Key: "render.pool", Kind: KindStrings, Path: true, Usage: "a pool of base templates; one is chosen at random per run"},
	{Key: "render.seed", Kind: KindInt, Usage: "seed for the template randomisation; 0 draws a new one and logs it"},
	{Key: "render.width", Kind: KindInt, Default: "1080", Usage: "output width in CSS pixels (0 lets the document's @page rule decide)"},
	{Key: "render.height", Kind: KindInt, Default: "1920", Usage: "output height in CSS pixels (0 lets the document's @page rule decide)"},
	{Key: "render.scale", Kind: KindFloat, Default: "1", Usage: "device pixel ratio: output pixels per CSS pixel (max 4)"},
	{Key: "render.supersample", Kind: KindInt, Default: "1", Usage: "extra supersampling factor applied on top of scale, then downsampled"},
	{Key: "render.pages-max", Kind: KindInt, Default: "10", Usage: "most pages a document may lay out into; content past it is refused (hard cap 20)"},
	{Key: "render.format", Kind: KindString, Default: "png", Usage: "output image format: png or jpeg"},
	{Key: "render.jpeg-quality", Kind: KindInt, Default: "90", Usage: "JPEG quality, 1 to 100"},
	{Key: "render.output", Kind: KindString, Path: true, Usage: "output file path; empty writes into a temporary directory"},
	{Key: "render.base-url", Kind: KindString, Path: true, Usage: "base URL, or a directory, that relative resources in the template resolve against"},
	{Key: "render.media-type", Kind: KindString, Default: "screen", Usage: "CSS media type used for the cascade: screen or print"},
	{Key: "render.background", Kind: KindString, Default: "#ffffff", Usage: "background colour transparent pixels are flattened onto for JPEG"},
	{Key: "render.fonts-dir", Kind: KindStrings, Path: true, Usage: "extra directories scanned for fonts"},
	{Key: "render.hermetic-fonts", Kind: KindBool, Usage: "ignore system fonts and use only the embedded Go fonts (deterministic output)"},

	{Key: "render.video.enabled", Kind: KindBool, Usage: "render an animated template into a clip instead of a still image"},
	{Key: "render.video.format", Kind: KindString, Default: "mp4", Usage: "clip format: mp4 or gif"},
	{Key: "render.video.frames-input", Kind: KindString, Path: true, Usage: "directory or glob of existing frames to encode, instead of rendering them"},
	{Key: "render.video.fps", Kind: KindInt, Default: "30", Usage: "video frame rate"},
	{Key: "render.video.duration", Kind: KindDuration, Default: "3s", Usage: "video length; ignored when render.video.frames is set"},
	{Key: "render.video.frames", Kind: KindInt, Usage: "exact number of frames to render; 0 derives it from the duration and the frame rate"},
	{Key: "render.video.ffmpeg-bin", Kind: KindString, Default: "ffmpeg", Usage: "ffmpeg executable, resolved on PATH; ffmpeg is a prerequisite crier does not bundle"},
	{Key: "render.video.ffmpeg-args", Kind: KindStrings, Usage: "extra ffmpeg arguments, inserted before the output file"},
	{Key: "render.video.codec-preset", Kind: KindString, Default: "h264", Usage: "output codec preset: h264, h265, vp9 or none to rely on ffmpeg-args alone"},
	{Key: "render.video.audio", Kind: KindString, Path: true, Usage: "audio file mixed into the video"},
	{Key: "render.video.audio-pool", Kind: KindStrings, Path: true, Usage: "a pool of audio files; one is chosen at random per run"},

	{Key: "http.timeout", Kind: KindDuration, Default: "60s", Usage: "per-request HTTP timeout for calls that carry no media"},
	{Key: "http.upload-timeout", Kind: KindDuration, Default: "10m", Usage: "per-request HTTP timeout for a request carrying media; covers the whole upload"},
	{Key: "http.retry-max", Kind: KindInt, Default: "3", Usage: "maximum number of retries for retryable requests"},
	{Key: "http.retry-base-delay", Kind: KindDuration, Default: "500ms", Usage: "initial backoff delay between retries"},
	{Key: "http.retry-max-delay", Kind: KindDuration, Default: "10s", Usage: "maximum backoff delay between retries"},

	{Key: "stage.mode", Kind: KindString, Default: "none", Usage: "how to publish a public image URL: none, s3, server or url"},
	{Key: "stage.url", Kind: KindString, Usage: "pre-hosted image URL used when stage.mode is url"},

	{Key: "stage.s3.endpoint", Kind: KindString, Usage: "S3-compatible endpoint host, e.g. s3.amazonaws.com or localhost:9000"},
	{Key: "stage.s3.region", Kind: KindString, Default: "us-east-1", Usage: "S3 region"},
	{Key: "stage.s3.bucket", Kind: KindString, Usage: "S3 bucket the image is uploaded to"},
	{Key: "stage.s3.prefix", Kind: KindString, Usage: "key prefix inside the bucket"},
	{Key: "stage.s3.access-key", Kind: KindString, Usage: "S3 access key id", Secret: true},
	{Key: "stage.s3.secret-key", Kind: KindString, Usage: "S3 secret access key", Secret: true},
	{Key: "stage.s3.use-ssl", Kind: KindBool, Default: "true", Usage: "talk to the S3 endpoint over HTTPS"},
	{Key: "stage.s3.acl", Kind: KindString, Usage: `canned ACL applied to the object, e.g. "public-read"`},
	{Key: "stage.s3.presign", Kind: KindBool, Default: "true", Usage: "hand out a presigned URL instead of a public one"},
	{Key: "stage.s3.presign-expiry", Kind: KindDuration, Default: "1h", Usage: "lifetime of the presigned URL"},
	{Key: "stage.s3.public-base-url", Kind: KindString, Usage: "base URL objects are publicly reachable at, when presign is false"},
	{Key: "stage.s3.delete-after", Kind: KindBool, Default: "true", Usage: "delete the staged object once publishing is done"},

	{Key: "stage.server.listen", Kind: KindString, Default: "127.0.0.1:0", Usage: "address the built-in stage server listens on"},
	{Key: "stage.server.public-url", Kind: KindString, Usage: "base URL the stage server is reachable at from the internet"},
	{Key: "stage.server.shutdown-timeout", Kind: KindDuration, Default: "10s", Usage: "how long to wait for the stage server to shut down"},

	{Key: "stage.server.tunnel.mode", Kind: KindString, Default: "none", Usage: "expose the stage server publicly: none, ngrok, zrok or custom"},
	{Key: "stage.server.tunnel.bin", Kind: KindString, Usage: "tunnel executable; defaults to the mode's own name, resolved on PATH"},
	{Key: "stage.server.tunnel.args", Kind: KindStrings, Usage: "extra tunnel arguments; {port} and {addr} are substituted"},
	{Key: "stage.server.tunnel.url-pattern", Kind: KindString, Usage: "regexp with one capture group finding the public URL in the tunnel output (custom mode)"},
	{Key: "stage.server.tunnel.api-url", Kind: KindString, Default: "http://127.0.0.1:4040/api/tunnels", Usage: "ngrok local agent API polled for the public URL"},
	{Key: "stage.server.tunnel.startup-timeout", Kind: KindDuration, Default: "30s", Usage: "how long to wait for the tunnel to report a public URL"},

	{Key: "publish.input", Kind: KindString, Path: true, Usage: "publish this existing file instead of rendering one"},
	{Key: "publish.caption", Kind: KindString, Usage: "caption used by every platform that has no caption of its own"},
	{Key: "publish.concurrency", Kind: KindInt, Default: "4", Usage: "how many platforms are published to at the same time"},
	{Key: "publish.dry-run", Kind: KindBool, Usage: "render and validate only; make no network calls"},
	{Key: "publish.music-file", Kind: KindString, Path: true,
		Usage: "audio file attached to the post on the platforms whose API can carry one " +
			"(discord, slack, telegram); empty attaches none"},

	{Key: "publish.instagram.enabled", Kind: KindBool, Usage: "publish to Instagram"},
	{Key: "publish.instagram.api-base-url", Kind: KindString, Default: "https://graph.facebook.com/v25.0", Usage: "Instagram Graph API base URL"},
	{Key: "publish.instagram.token", Kind: KindString, Usage: "Instagram access token", Secret: true},
	{Key: "publish.instagram.user-id", Kind: KindString, Usage: "Instagram professional account user id"},
	{Key: "publish.instagram.story", Kind: KindBool, Usage: "publish as a story instead of a feed post"},
	{Key: "publish.instagram.caption", Kind: KindString, Usage: "Instagram specific caption"},
	{Key: "publish.instagram.poll-interval", Kind: KindDuration, Default: "2s", Usage: "how often the media container status is polled"},
	{Key: "publish.instagram.poll-timeout", Kind: KindDuration, Default: "2m", Usage: "how long to wait for the media container to be ready"},

	{Key: "publish.facebook.enabled", Kind: KindBool, Usage: "publish to a Facebook Page"},
	{Key: "publish.facebook.api-base-url", Kind: KindString, Default: "https://graph.facebook.com/v25.0", Usage: "Facebook Graph API base URL"},
	{Key: "publish.facebook.token", Kind: KindString, Usage: "Facebook Page access token", Secret: true},
	{Key: "publish.facebook.page-id", Kind: KindString, Usage: "Facebook Page id"},
	{Key: "publish.facebook.story", Kind: KindBool, Usage: "publish as a Page story instead of a photo post"},
	{Key: "publish.facebook.use-url", Kind: KindBool, Usage: "send the staged URL instead of uploading the bytes"},
	{Key: "publish.facebook.caption", Kind: KindString, Usage: "Facebook specific caption"},

	{Key: "publish.tiktok.enabled", Kind: KindBool, Usage: "publish to TikTok"},
	{Key: "publish.tiktok.api-base-url", Kind: KindString, Default: "https://open.tiktokapis.com", Usage: "TikTok Content Posting API base URL"},
	{Key: "publish.tiktok.token", Kind: KindString, Usage: "TikTok access token", Secret: true},
	{Key: "publish.tiktok.title", Kind: KindString, Usage: "TikTok post title"},
	{Key: "publish.tiktok.privacy-level", Kind: KindString, Default: "SELF_ONLY", Usage: "TikTok privacy level: SELF_ONLY, MUTUAL_FOLLOW_FRIENDS, FOLLOWER_OF_CREATOR or PUBLIC_TO_EVERYONE"},
	{Key: "publish.tiktok.caption", Kind: KindString, Usage: "TikTok post description"},
	{Key: "publish.tiktok.poll-interval", Kind: KindDuration, Default: "2s", Usage: "how often the publish status is polled"},
	{Key: "publish.tiktok.poll-timeout", Kind: KindDuration, Default: "2m", Usage: "how long to wait for TikTok to finish the upload"},
	{Key: "publish.tiktok.auto-add-music", Kind: KindBool,
		Usage: "let TikTok put a recommended track under a photo post; " +
			"no API anywhere names a specific licensed track"},

	{Key: "publish.telegram.enabled", Kind: KindBool, Usage: "publish to Telegram"},
	{Key: "publish.telegram.api-base-url", Kind: KindString, Default: "https://api.telegram.org", Usage: "Telegram Bot API base URL"},
	{Key: "publish.telegram.token", Kind: KindString, Usage: "Telegram bot token", Secret: true},
	{Key: "publish.telegram.chat-id", Kind: KindString, Usage: "Telegram chat id or @channelusername"},
	{Key: "publish.telegram.caption", Kind: KindString, Usage: "Telegram specific caption"},

	{Key: "publish.x.enabled", Kind: KindBool, Usage: "publish to X"},
	{Key: "publish.x.api-base-url", Kind: KindString, Default: "https://api.x.com", Usage: "X API base URL"},
	{Key: "publish.x.token", Kind: KindString, Usage: "X OAuth 2.0 user access token", Secret: true},
	{Key: "publish.x.caption", Kind: KindString, Usage: "X specific post text"},
	{Key: "publish.x.poll-interval", Kind: KindDuration, Default: "2s", Usage: "how often a not-ready tweet create is retried"},
	{Key: "publish.x.poll-timeout", Kind: KindDuration, Default: "30s", Usage: "how long to keep retrying a tweet whose media is not ready"},

	{Key: "publish.mastodon.enabled", Kind: KindBool, Usage: "publish to Mastodon"},
	{Key: "publish.mastodon.api-base-url", Kind: KindString, Usage: "Mastodon instance base URL, e.g. https://mastodon.social"},
	{Key: "publish.mastodon.token", Kind: KindString, Usage: "Mastodon access token", Secret: true},
	{Key: "publish.mastodon.visibility", Kind: KindString, Default: "public", Usage: "status visibility: public, unlisted, private or direct"},
	{Key: "publish.mastodon.alt-text", Kind: KindString, Usage: "media description used for accessibility"},
	{Key: "publish.mastodon.caption", Kind: KindString, Usage: "Mastodon specific status text"},
	{Key: "publish.mastodon.poll-interval", Kind: KindDuration, Default: "2s", Usage: "how often a still-processing attachment is polled"},
	{Key: "publish.mastodon.poll-timeout", Kind: KindDuration, Default: "2m", Usage: "how long to wait for the attachment to finish processing"},

	{Key: "publish.discord.enabled", Kind: KindBool, Usage: "publish to a Discord webhook"},
	{Key: "publish.discord.webhook-url", Kind: KindString, Usage: "full Discord webhook URL (it is the credential)", Secret: true},
	{Key: "publish.discord.username", Kind: KindString, Usage: "override the webhook's display name"},
	{Key: "publish.discord.caption", Kind: KindString, Usage: "Discord specific message content"},

	{Key: "publish.linkedin.enabled", Kind: KindBool, Usage: "publish to LinkedIn"},
	{Key: "publish.linkedin.api-base-url", Kind: KindString, Default: "https://api.linkedin.com", Usage: "LinkedIn REST API base URL"},
	{Key: "publish.linkedin.token", Kind: KindString, Usage: "LinkedIn OAuth 2.0 access token", Secret: true},
	{Key: "publish.linkedin.author-urn", Kind: KindString, Usage: "author URN, e.g. urn:li:person:XXXX or urn:li:organization:NNNN"},
	{Key: "publish.linkedin.version", Kind: KindString, Default: "202606", Usage: "value of the mandatory LinkedIn-Version header"},
	{Key: "publish.linkedin.caption", Kind: KindString, Usage: "LinkedIn specific commentary"},

	{Key: "publish.reddit.enabled", Kind: KindBool, Usage: "publish to Reddit"},
	{Key: "publish.reddit.api-base-url", Kind: KindString, Default: "https://oauth.reddit.com", Usage: "Reddit API base URL (the OAuth host)"},
	{Key: "publish.reddit.auth-base-url", Kind: KindString, Default: "https://www.reddit.com", Usage: "Reddit token endpoint base URL"},
	{Key: "publish.reddit.client-id", Kind: KindString, Usage: "Reddit script app client id", Secret: true},
	{Key: "publish.reddit.client-secret", Kind: KindString, Usage: "Reddit script app client secret", Secret: true},
	{Key: "publish.reddit.refresh-token", Kind: KindString, Usage: "Reddit refresh token; when set it is used instead of the password grant", Secret: true},
	{Key: "publish.reddit.username", Kind: KindString, Usage: "Reddit account name, also used in the mandatory User-Agent"},
	{Key: "publish.reddit.password", Kind: KindString, Usage: "Reddit account password, for the script-app password grant", Secret: true},
	{Key: "publish.reddit.user-agent", Kind: KindString, Usage: "override the User-Agent; empty builds the descriptive one Reddit requires"},
	{Key: "publish.reddit.subreddit", Kind: KindString, Usage: "subreddit to post to, without the r/ prefix"},
	{Key: "publish.reddit.title", Kind: KindString, Usage: "Reddit post title"},
	{Key: "publish.reddit.flair-id", Kind: KindString, Usage: "flair template id applied to the post"},
	{Key: "publish.reddit.kind", Kind: KindString, Default: "auto", Usage: "post kind: auto, image, video or link"},
	{Key: "publish.reddit.nsfw", Kind: KindBool, Usage: "mark the post NSFW"},
	{Key: "publish.reddit.spoiler", Kind: KindBool, Usage: "mark the post a spoiler"},
	{Key: "publish.reddit.caption", Kind: KindString, Usage: "Reddit specific text; used as the title when no title is set"},
	{Key: "publish.reddit.poll-interval", Kind: KindDuration, Default: "2s", Usage: "how often the new post is looked for after submitting"},
	{Key: "publish.slack.enabled", Kind: KindBool, Usage: "publish to Slack"},
	{Key: "publish.slack.api-base-url", Kind: KindString, Default: "https://slack.com/api", Usage: "Slack Web API base URL"},
	{Key: "publish.slack.token", Kind: KindString, Secret: true, Usage: "Slack bot token (xoxb-…) with files:write and chat:write"},
	{Key: "publish.slack.channel", Kind: KindString, Usage: "Slack channel ID to post in, such as C0123ABCD; the bot has to be in it"},
	{Key: "publish.slack.caption", Kind: KindString, Usage: "Slack specific caption"},

	{Key: "publish.reddit.poll-timeout", Kind: KindDuration, Default: "30s", Usage: "how long to look for the new post's permalink"},

	{Key: "publish.vk.enabled", Kind: KindBool, Usage: "publish to VK"},
	{Key: "publish.vk.api-base-url", Kind: KindString, Default: "https://api.vk.com", Usage: "VK API base URL; methods are called under /method/"},
	{Key: "publish.vk.api-version", Kind: KindString, Default: "5.199", Usage: "value of the mandatory VK API version parameter"},
	{Key: "publish.vk.token", Kind: KindString, Secret: true, Usage: "VK access token with the wall, photos, video and docs scopes"},
	{Key: "publish.vk.owner-id", Kind: KindInt,
		Usage: "wall the post lands on: negative for a community, such as -123456, positive for a user"},
	{Key: "publish.vk.caption", Kind: KindString, Usage: "VK specific post text"},

	{Key: "publish.threads.enabled", Kind: KindBool, Usage: "publish to Threads"},
	{Key: "publish.threads.api-base-url", Kind: KindString, Default: "https://graph.threads.net/v1.0", Usage: "Threads API base URL"},
	{Key: "publish.threads.token", Kind: KindString, Secret: true,
		Usage: "Threads access token, with the threads_basic and threads_content_publish scopes; " +
			"an Instagram token is not one"},
	{Key: "publish.threads.user-id", Kind: KindString, Usage: "Threads account user id, as /me reports it"},
	{Key: "publish.threads.caption", Kind: KindString, Usage: "Threads specific post text"},
	{Key: "publish.threads.poll-interval", Kind: KindDuration, Default: "2s", Usage: "how often the media container status is polled"},
	{Key: "publish.threads.poll-timeout", Kind: KindDuration, Default: "2m", Usage: "how long to wait for the media container to be ready"},

	{Key: "publish.youtube.enabled", Kind: KindBool, Usage: "publish to YouTube (videos only)"},
	{Key: "publish.youtube.api-base-url", Kind: KindString, Default: "https://www.googleapis.com",
		Usage: "YouTube Data API v3 base URL"},
	{Key: "publish.youtube.auth-base-url", Kind: KindString, Default: "https://oauth2.googleapis.com",
		Usage: "Google OAuth 2.0 token endpoint base URL"},
	{Key: "publish.youtube.client-id", Kind: KindString, Secret: true,
		Usage: "Google Cloud OAuth client id"},
	{Key: "publish.youtube.client-secret", Kind: KindString, Secret: true,
		Usage: "Google Cloud OAuth client secret"},
	{Key: "publish.youtube.refresh-token", Kind: KindString, Secret: true,
		Usage: "OAuth refresh token carrying the youtube.upload scope; it is traded for an access token each run"},
	{Key: "publish.youtube.title", Kind: KindString,
		Usage: "YouTube video title; empty uses the caption's first line, cut to 100 characters"},
	{Key: "publish.youtube.caption", Kind: KindString, Usage: "YouTube video description"},
	{Key: "publish.youtube.category-id", Kind: KindString, Default: "22",
		Usage: `YouTube category id; 22 is "People & Blogs"`},
	{Key: "publish.youtube.privacy-status", Kind: KindString, Default: "private",
		Usage: "video privacy: private, unlisted or public; " +
			"an API project Google has not audited gets private whatever is asked for"},
	{Key: "publish.youtube.thumbnail", Kind: KindString, Path: true,
		Usage: "JPEG or PNG set as the video's thumbnail; it needs a phone-verified channel, " +
			"so a refusal is a warning rather than a failed post"},

	{Key: "publish.boosty.enabled", Kind: KindBool, Usage: "publish to a Boosty blog"},
	{Key: "publish.boosty.api-base-url", Kind: KindString, Default: "https://api.boosty.to",
		Usage: "Boosty API base URL; the API is unofficial and undocumented, so it is configurable"},
	{Key: "publish.boosty.upload-base-url", Kind: KindString, Default: "https://upload.boosty.to",
		Usage: "Boosty upload host; media goes here rather than to the API host"},
	{Key: "publish.boosty.blog", Kind: KindString,
		Usage: "blog to post in: the slug in boosty.to/<blog>"},
	{Key: "publish.boosty.access-token", Kind: KindString, Secret: true,
		Usage: "Boosty access token, taken from a signed-in browser session; it expires"},
	{Key: "publish.boosty.refresh-token", Kind: KindString, Secret: true,
		Usage: "Boosty refresh token; with device-id it renews an expired access token, " +
			"and boosty replaces it every time it is used"},
	{Key: "publish.boosty.device-id", Kind: KindString, Secret: true,
		Usage: "the browser session's device id, which a refresh will not run without"},
	{Key: "publish.boosty.access", Kind: KindString, Default: "free",
		Usage: "who can read the post: free, paid (a one-time price) or level (a subscription tier)"},
	{Key: "publish.boosty.price", Kind: KindInt,
		Usage: "one-time price the post is unlocked by, in publish.boosty.currency; required when access is paid"},
	{Key: "publish.boosty.level-id", Kind: KindString,
		Usage: "subscription level id the post is limited to; required when access is level"},
	{Key: "publish.boosty.currency", Kind: KindString, Default: "RUB",
		Usage: "currency publish.boosty.price is quoted in, sent as the X-Currency header"},
	{Key: "publish.boosty.title", Kind: KindString,
		Usage: "Boosty post title; empty uses the caption's first line"},
	{Key: "publish.boosty.caption", Kind: KindString, Usage: "Boosty specific post text"},
}

// layoutDescriptors are the three keys every platform has for overriding what
// gets rendered for it. They are generated rather than written out fourteen
// times, because a platform silently missing one would be a hole nobody
// notices.
func layoutDescriptors(platform string) []Descriptor {
	return []Descriptor{
		{
			Key: "publish." + platform + ".overlay", Kind: KindStrings, Path: true,
			Usage: "template overlays applied for " + platform + " only, after the global ones",
		},
		{
			Key: "publish." + platform + ".width", Kind: KindInt,
			Usage: "render width for " + platform + "; 0 inherits render.width",
		},
		{
			Key: "publish." + platform + ".height", Kind: KindInt,
			Usage: "render height for " + platform + "; 0 inherits render.height",
		},
		{
			Key: "publish." + platform + ".fit", Kind: KindString, Default: "none",
			Usage: "how the render is made to match " + platform + "'s frame: " +
				"none, cover, contain or stretch; anything but none needs width and height",
		},
		{
			Key: "publish." + platform + ".fit-background", Kind: KindString, Default: "#ffffff",
			Usage: "hex colour behind a contain letterbox for " + platform + ", and what transparency is flattened onto",
		},
		{
			Key: "publish." + platform + ".max-attachments", Kind: KindInt,
			Usage: "post at most this many pages to " + platform + " at once; " +
				"0 uses the platform's own limit, which is also the ceiling",
		},
	}
}

// musicDescriptor is a platform's own audio file, declared for every platform
// rather than only for the three that can carry one.
//
// A key that simply did not exist for Instagram would answer the question
// "can I attach a track here" with an unknown-key error, which reads like a
// typo. The key exists everywhere, its description says where it works, and
// setting it on a platform that cannot carry audio is a validation error that
// says why. That way the answer is in the platform's own reference page.
func musicDescriptor(platform string) Descriptor {
	d := Descriptor{Key: "publish." + platform + ".music-file", Kind: KindString, Path: true}
	if CanCarryMusic(platform) {
		d.Usage = "audio file attached to the " + platform + " post, " +
			"overriding publish.music-file for this platform alone"
		return d
	}
	d.Usage = "not available: " + platform + " has no API for attaching an audio file, " +
		"so a value here is refused rather than ignored"
	return d
}

// leadVideoDescriptor is a platform's opening clip, declared for every platform
// for the same reason musicDescriptor is: the answer to "can I open the
// carousel with a video here" belongs on the platform's own reference page,
// not in an unknown-key error that reads like a typo.
func leadVideoDescriptor(platform string) Descriptor {
	d := Descriptor{Key: "publish." + platform + ".lead-video", Kind: KindString, Path: true}
	if CanCarryLeadVideo(platform) {
		d.Usage = "video file posted as the first item of the " + platform +
			" post, ahead of the pages; it counts towards the attachment limit"
		return d
	}
	d.Usage = "not available: a " + platform + " post is pictures or a video and never both, " +
		"so a value here is refused rather than ignored"
	return d
}

func init() {
	for _, p := range Platforms {
		registry = append(registry, layoutDescriptors(p)...)
		registry = append(registry, musicDescriptor(p))
		registry = append(registry, leadVideoDescriptor(p))
	}
}

// Registry returns every declared key, in declaration order.
//
// The slice is copied so a caller cannot rewrite the schema.
func Registry() []Descriptor {
	out := make([]Descriptor, len(registry))
	copy(out, registry)
	return out
}

// Descriptors indexes the registry by key.
func Descriptors() map[string]Descriptor {
	out := make(map[string]Descriptor, len(registry))
	for _, d := range registry {
		out[d.Key] = d
	}
	return out
}

// Keys returns every declared key, sorted, which is the order the environment
// binding and the documentation use.
func Keys() []string {
	out := make([]string, 0, len(registry))
	for _, d := range registry {
		out = append(out, d.Key)
	}
	sort.Strings(out)
	return out
}

// CheckKey reports whether a dotted key is one crier has, with a message
// naming the closest thing it does have when it is not.
//
// It backs --set, whose whole risk is a typo that goes nowhere: a flag layer
// that silently accepts publish.telegram.chatid would look like it worked and
// change nothing, which is the worst outcome available.
func CheckKey(key string) error {
	if key == "" {
		return fmt.Errorf("a key is required")
	}
	if _, ok := Descriptors()[key]; ok {
		return nil
	}
	if err, isCustom := checkCustomKey(key); isCustom {
		return err
	}
	if target, ok := Aliases[FlagName(key)]; ok {
		return fmt.Errorf("%q is a flag alias; set %s instead", key, target)
	}
	if near := nearest(key); near != "" {
		return fmt.Errorf("unknown key %q; did you mean %s?", key, near)
	}
	return fmt.Errorf("unknown key %q", key)
}

// nearest is the declared key sharing the longest dotted prefix with key, so a
// mistyped leaf is answered with its own section rather than with silence.
func nearest(key string) string {
	segs := strings.Split(key, ".")
	best, bestDepth := "", 0
	for _, candidate := range Keys() {
		other := strings.Split(candidate, ".")
		depth := 0
		for depth < len(segs) && depth < len(other) && segs[depth] == other[depth] {
			depth++
		}
		if depth > bestDepth {
			best, bestDepth = candidate, depth
		}
	}
	if bestDepth == 0 {
		return ""
	}
	return best
}
