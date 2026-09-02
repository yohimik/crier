// Package config holds crier's whole configuration surface.
//
// Every value is settable in three layers, with file < env < flags precedence:
//
//   - a config file (--config, CRIER_CONFIG, or ./crier.{yaml,yml,json,toml})
//   - an environment variable (CRIER_ + the key, dots and dashes to underscores)
//   - a command line flag (the key with dots turned into dashes)
//
// The three layers can never drift because they are all generated from the one
// descriptor table in registry.go, and the decode table in fields.go is
// generated from the same key strings. The anti-drift tests assert exactly that.
package config

// Config is the fully decoded configuration.
//
// Durations and floats are stored as strings: the decoder has no setter for
// them, and keeping the raw text means an invalid value is reported by
// Validate with the text the operator actually wrote.
type Config struct {
	Log     Log
	Render  Render
	HTTP    HTTP
	Stage   Stage
	Publish Publish
}

// Log configures the zerolog logger. Logs always go to stderr.
type Log struct {
	Level  string
	Format string
}

// Render configures the HTML -> image rendering pipeline.
type Render struct {
	Template      string
	Data          string
	CSS           []string
	Overlays      []string
	Pool          []string
	Seed          int
	Width         int
	Height        int
	Scale         string
	SuperSample   int
	PagesMax      int
	Format        string
	JPEGQuality   int
	Output        string
	BaseURL       string
	MediaType     string
	Background    string
	FontsDir      []string
	HermeticFonts bool
	Video         Video
}

// Video configures rendering an animated template into an MP4 through ffmpeg.
//
// The template is executed and laid out once per frame, with the frame counter
// injected as .Video, and the frames are streamed straight into ffmpeg's stdin
// so memory stays at one frame however long the clip is.
type Video struct {
	Enabled bool
	// Format is mp4 or gif. A GIF is a different artifact kind rather than a
	// video with another extension, because the platforms treat it as one.
	Format string
	// FramesInput is a directory or glob of images to encode instead of
	// rendering the frames.
	FramesInput string
	FPS         int
	Duration    string
	Frames      int
	FFmpegBin   string
	FFmpegArgs  []string
	CodecPreset string
	Audio       string
}

// Layout is the per-platform override of what gets rendered: extra template
// overlays and the output size. Platforms sharing a layout share a render.
type Layout struct {
	Overlay []string
	Width   int
	Height  int
	// Fit is how the render is made to match this platform's frame: none,
	// cover, contain or stretch. Anything but none turns Width and Height from
	// "render at this size" into "fit into this size", so the layout is drawn
	// at the master size and resampled rather than reflowed.
	Fit string
	// FitBackground is the colour behind a contain letterbox, and what an
	// image with transparency is flattened onto.
	FitBackground string
	// MaxAttachments lowers how many pages this platform takes in one post.
	// Zero uses the platform's own limit, which is also the ceiling: asking
	// for more than a platform accepts would only be refused by the platform.
	MaxAttachments int
}

// HTTP configures the shared HTTP client used by every publisher and stager.
type HTTP struct {
	Timeout        string
	UploadTimeout  string
	RetryMax       int
	RetryBaseDelay string
	RetryMaxDelay  string
}

// Stage configures how a rendered image is made reachable by a public URL.
type Stage struct {
	Mode   string
	URL    string
	S3     S3
	Server Server
}

// S3 configures the S3-compatible staging backend.
type S3 struct {
	Endpoint      string
	Region        string
	Bucket        string
	Prefix        string
	AccessKey     string
	SecretKey     string
	UseSSL        bool
	ACL           string
	Presign       bool
	PresignExpiry string
	PublicBaseURL string
	DeleteAfter   bool
}

// Server configures the built-in ephemeral HTTP file server.
type Server struct {
	Listen          string
	PublicURL       string
	ShutdownTimeout string
	Tunnel          Tunnel
}

// Tunnel configures the optional subprocess that exposes the local stage
// server on a public URL.
type Tunnel struct {
	Mode           string
	Bin            string
	Args           []string
	URLPattern     string
	APIURL         string
	StartupTimeout string
}

// Publish configures the fan-out and every platform publisher.
type Publish struct {
	// Input is a file to publish instead of rendering one.
	Input       string
	Caption     string
	Concurrency int
	DryRun      bool

	Instagram Instagram
	Facebook  Facebook
	TikTok    TikTok
	Telegram  Telegram
	X         X
	Mastodon  Mastodon
	Discord   Discord
	LinkedIn  LinkedIn
	Reddit    Reddit
	Slack     Slack

	// Custom are script-backed platforms, keyed by the name the configuration
	// gave them. They are peers of the nine above: they take part in the
	// fan-out, in the render variants, in caption templating, in `crier ping`
	// and in a dry run.
	//
	// This is the one place in the configuration where the keys are not known
	// in advance, which is why it is a map and why the loader has to discover
	// the names before it can bind the environment to them.
	Custom map[string]*Custom
}

// Custom is a platform crier knows nothing about, defined by a shell command.
//
// The command receives the rendered file and the caption through the
// environment and reports what it published by writing to a file. That is the
// whole contract: anything with a shell and an HTTP client can be a platform.
type Custom struct {
	Layout

	Enabled bool
	// Command is the shell command that publishes. It is run with `sh -c`, so
	// it can be a script path or a pipeline written inline.
	Command string
	// PingCommand is what `crier ping` runs instead. Empty means the platform
	// reports that it has nothing to check rather than running Command, which
	// would publish.
	PingCommand string
	Caption     string
	// Kinds are the artifact kinds the command accepts: image, video, or both.
	Kinds []string
	// Format is the image format it prefers.
	Format string
	// NeedsURL asks the pipeline to stage the file and pass its URL.
	NeedsURL bool
	Timeout  string
	// Env are extra environment variables the command is run with. The keys
	// are used as written, so this is the one place a value's spelling is not
	// crier's to decide.
	Env map[string]string
}

// Slack configures the Slack Web API publisher.
//
// The token is a bot token (xoxb-) carrying files:write and chat:write, and
// the bot has to be a member of the channel it posts in — neither of which a
// configuration can check for you.
type Slack struct {
	Layout

	Enabled    bool
	APIBaseURL string
	Token      string
	// Channel is a channel ID such as C0123ABCD, not a name.
	Channel string
	Caption string
}

// Instagram configures the Instagram Graph API publisher.
type Instagram struct {
	Layout

	Enabled      bool
	APIBaseURL   string
	Token        string
	UserID       string
	Story        bool
	Caption      string
	PollInterval string
	PollTimeout  string
}

// Facebook configures the Facebook Page publisher.
type Facebook struct {
	Layout

	Enabled    bool
	APIBaseURL string
	Token      string
	PageID     string
	Story      bool
	UseURL     bool
	Caption    string
}

// TikTok configures the TikTok Content Posting API publisher.
type TikTok struct {
	Layout

	Enabled      bool
	APIBaseURL   string
	Token        string
	Title        string
	PrivacyLevel string
	Caption      string
	PollInterval string
	PollTimeout  string
}

// Telegram configures the Telegram Bot API publisher.
type Telegram struct {
	Layout

	Enabled    bool
	APIBaseURL string
	Token      string
	ChatID     string
	Caption    string
}

// X configures the X (Twitter) v2 publisher.
type X struct {
	Layout

	Enabled    bool
	APIBaseURL string
	Token      string
	Caption    string
}

// Mastodon configures the Mastodon publisher.
type Mastodon struct {
	Layout

	Enabled      bool
	APIBaseURL   string
	Token        string
	Visibility   string
	AltText      string
	Caption      string
	PollInterval string
	PollTimeout  string
}

// Discord configures the Discord webhook publisher.
type Discord struct {
	Layout

	Enabled    bool
	WebhookURL string
	Username   string
	Caption    string
}

// LinkedIn configures the LinkedIn REST publisher.
type LinkedIn struct {
	Layout

	Enabled    bool
	APIBaseURL string
	Token      string
	AuthorURN  string
	Version    string
	Caption    string
}

// Reddit configures the Reddit publisher.
//
// Reddit is the only platform with two hosts: tokens come from the www host
// and everything else goes to the oauth host, and both are configurable so the
// end-to-end tests can point them at a fake.
type Reddit struct {
	Layout

	Enabled      bool
	APIBaseURL   string
	AuthBaseURL  string
	ClientID     string
	ClientSecret string
	RefreshToken string
	Username     string
	Password     string
	UserAgent    string
	Subreddit    string
	Title        string
	FlairID      string
	Kind         string
	NSFW         bool
	Spoiler      bool
	Caption      string
	PollInterval string
	PollTimeout  string
}
