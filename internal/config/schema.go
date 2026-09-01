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
	Width         int
	Height        int
	Scale         string
	SuperSample   int
	Format        string
	JPEGQuality   int
	Output        string
	BaseURL       string
	MediaType     string
	Background    string
	FontsDir      []string
	HermeticFonts bool
}

// HTTP configures the shared HTTP client used by every publisher and stager.
type HTTP struct {
	Timeout        string
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
}

// Instagram configures the Instagram Graph API publisher.
type Instagram struct {
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
	Enabled    bool
	APIBaseURL string
	Token      string
	ChatID     string
	Caption    string
}

// X configures the X (Twitter) v2 publisher.
type X struct {
	Enabled    bool
	APIBaseURL string
	Token      string
	Caption    string
}

// Mastodon configures the Mastodon publisher.
type Mastodon struct {
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
	Enabled    bool
	WebhookURL string
	Username   string
	Caption    string
}

// LinkedIn configures the LinkedIn REST publisher.
type LinkedIn struct {
	Enabled    bool
	APIBaseURL string
	Token      string
	AuthorURN  string
	Version    string
	Caption    string
}
