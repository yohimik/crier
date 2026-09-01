package config

import (
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

	{Key: "render.template", Kind: KindString, Usage: "path to the Go html/template file to render"},
	{Key: "render.data", Kind: KindString, Usage: `path to a JSON or YAML data file, or "-" to read it from stdin`},
	{Key: "render.css", Kind: KindStrings, Usage: "extra stylesheet files applied after the document's own CSS"},
	{Key: "render.width", Kind: KindInt, Default: "1080", Usage: "output width in CSS pixels (0 lets the document's @page rule decide)"},
	{Key: "render.height", Kind: KindInt, Default: "1920", Usage: "output height in CSS pixels (0 lets the document's @page rule decide)"},
	{Key: "render.scale", Kind: KindFloat, Default: "1", Usage: "device pixel ratio: output pixels per CSS pixel (max 4)"},
	{Key: "render.supersample", Kind: KindInt, Default: "1", Usage: "extra supersampling factor applied on top of scale, then downsampled"},
	{Key: "render.format", Kind: KindString, Default: "png", Usage: "output image format: png or jpeg"},
	{Key: "render.jpeg-quality", Kind: KindInt, Default: "90", Usage: "JPEG quality, 1 to 100"},
	{Key: "render.output", Kind: KindString, Usage: "output file path; empty writes into a temporary directory"},
	{Key: "render.base-url", Kind: KindString, Usage: "base URL used to resolve relative resources in the template"},
	{Key: "render.media-type", Kind: KindString, Default: "screen", Usage: "CSS media type used for the cascade: screen or print"},
	{Key: "render.background", Kind: KindString, Default: "#ffffff", Usage: "background colour transparent pixels are flattened onto for JPEG"},
	{Key: "render.fonts-dir", Kind: KindStrings, Usage: "extra directories scanned for fonts"},
	{Key: "render.hermetic-fonts", Kind: KindBool, Usage: "ignore system fonts and use only the embedded Go fonts (deterministic output)"},

	{Key: "http.timeout", Kind: KindDuration, Default: "60s", Usage: "per-request HTTP timeout"},
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

	{Key: "publish.caption", Kind: KindString, Usage: "caption used by every platform that has no caption of its own"},
	{Key: "publish.concurrency", Kind: KindInt, Default: "4", Usage: "how many platforms are published to at the same time"},
	{Key: "publish.dry-run", Kind: KindBool, Usage: "render and validate only; make no network calls"},

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

	{Key: "publish.telegram.enabled", Kind: KindBool, Usage: "publish to Telegram"},
	{Key: "publish.telegram.api-base-url", Kind: KindString, Default: "https://api.telegram.org", Usage: "Telegram Bot API base URL"},
	{Key: "publish.telegram.token", Kind: KindString, Usage: "Telegram bot token", Secret: true},
	{Key: "publish.telegram.chat-id", Kind: KindString, Usage: "Telegram chat id or @channelusername"},
	{Key: "publish.telegram.caption", Kind: KindString, Usage: "Telegram specific caption"},

	{Key: "publish.x.enabled", Kind: KindBool, Usage: "publish to X"},
	{Key: "publish.x.api-base-url", Kind: KindString, Default: "https://api.x.com", Usage: "X API base URL"},
	{Key: "publish.x.token", Kind: KindString, Usage: "X OAuth 2.0 user access token", Secret: true},
	{Key: "publish.x.caption", Kind: KindString, Usage: "X specific post text"},

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
