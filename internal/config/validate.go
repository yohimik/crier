package config

import (
	"errors"
	"fmt"
	"image/color"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Limits that keep a typo from allocating gigabytes.
const (
	// MaxDimension is the largest render width or height, in CSS pixels.
	MaxDimension = 10000
	// MaxScale is the largest device pixel ratio.
	MaxScale = 4
	// MaxSuperSample is the largest extra supersampling factor.
	MaxSuperSample = 4
	// MaxVideoFrames caps a clip, because a frame is a full page layout and a
	// mistyped duration would otherwise run for hours.
	MaxVideoFrames = 36000
)

// InvalidError reports one configuration key whose value cannot be used.
type InvalidError struct {
	Key    string
	Value  string
	Reason string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("%s: invalid value %q: %s (set it in the config file, %s or --%s)",
		e.Key, e.Value, e.Reason, EnvName(e.Key), FlagName(e.Key))
}

func invalid(key, value, reason string) error {
	return &InvalidError{Key: key, Value: value, Reason: reason}
}

// MissingError reports a configuration key that has to be set for what the
// rest of the configuration asks for.
type MissingError struct {
	Key    string
	Reason string
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("%s is required: %s (set it in the config file, %s or --%s)",
		e.Key, e.Reason, EnvName(e.Key), FlagName(e.Key))
}

func missing(key, reason string) error {
	return &MissingError{Key: key, Reason: reason}
}

// Validate checks every value that has a shape of its own — durations, floats,
// enumerations, bounds — and the combinations that cannot both hold.
//
// It reports every problem it finds rather than only the first, because a
// half-corrected configuration file costs another run.
func Validate(cfg *Config) error {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	add(validateLog(&cfg.Log))
	add(validateRender(&cfg.Render))
	add(validateVideo(&cfg.Render.Video))
	add(validateHTTP(&cfg.HTTP))
	errs = append(errs, validateStage(&cfg.Stage)...)
	errs = append(errs, validatePublish(&cfg.Publish)...)

	return errors.Join(errs...)
}

func validateLog(l *Log) error {
	var errs []error
	if _, err := parseLevelName(l.Level); err != nil {
		errs = append(errs, invalid("log.level", l.Level, "want trace, debug, info, warn, error, fatal, panic or disabled"))
	}
	switch strings.ToLower(l.Format) {
	case "console", "json":
	default:
		errs = append(errs, invalid("log.format", l.Format, "want console or json"))
	}
	return errors.Join(errs...)
}

var logLevels = map[string]bool{
	"trace": true, "debug": true, "info": true, "warn": true,
	"error": true, "fatal": true, "panic": true, "disabled": true, "": true,
}

func parseLevelName(s string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if !logLevels[v] {
		return "", fmt.Errorf("unknown level %q", s)
	}
	return v, nil
}

func validateRender(r *Render) error {
	var errs []error
	if r.Width < 0 || r.Width > MaxDimension {
		errs = append(errs, invalid("render.width", strconv.Itoa(r.Width),
			fmt.Sprintf("want 0 to %d", MaxDimension)))
	}
	if r.Height < 0 || r.Height > MaxDimension {
		errs = append(errs, invalid("render.height", strconv.Itoa(r.Height),
			fmt.Sprintf("want 0 to %d", MaxDimension)))
	}
	scale, err := strconv.ParseFloat(strings.TrimSpace(r.Scale), 64)
	switch {
	case err != nil:
		errs = append(errs, invalid("render.scale", r.Scale, "want a decimal number"))
	case scale <= 0 || scale > MaxScale:
		errs = append(errs, invalid("render.scale", r.Scale, fmt.Sprintf("want more than 0 and at most %d", MaxScale)))
	}
	if r.SuperSample < 1 || r.SuperSample > MaxSuperSample {
		errs = append(errs, invalid("render.supersample", strconv.Itoa(r.SuperSample),
			fmt.Sprintf("want 1 to %d", MaxSuperSample)))
	}
	if _, err := ParseFormat(r.Format); err != nil {
		errs = append(errs, invalid("render.format", r.Format, "want png or jpeg"))
	}
	if r.JPEGQuality < 1 || r.JPEGQuality > 100 {
		errs = append(errs, invalid("render.jpeg-quality", strconv.Itoa(r.JPEGQuality), "want 1 to 100"))
	}
	switch strings.ToLower(r.MediaType) {
	case "screen", "print":
	default:
		errs = append(errs, invalid("render.media-type", r.MediaType, "want screen or print"))
	}
	if _, err := ParseColor(r.Background); err != nil {
		errs = append(errs, invalid("render.background", r.Background, err.Error()))
	}
	return errors.Join(errs...)
}

func validateVideo(v *Video) error {
	var errs []error
	if v.FPS < 1 || v.FPS > 240 {
		errs = append(errs, invalid("render.video.fps", strconv.Itoa(v.FPS), "want 1 to 240"))
	}
	if err := checkDuration("render.video.duration", v.Duration, true); err != nil {
		errs = append(errs, err)
	}
	if v.Frames < 0 || v.Frames > MaxVideoFrames {
		errs = append(errs, invalid("render.video.frames", strconv.Itoa(v.Frames),
			fmt.Sprintf("want 0 to %d", MaxVideoFrames)))
	}
	switch strings.ToLower(strings.TrimSpace(v.CodecPreset)) {
	case "h264", "h265", "vp9", "none":
	default:
		errs = append(errs, invalid("render.video.codec-preset", v.CodecPreset, "want h264, h265, vp9 or none"))
	}
	if v.Enabled && strings.TrimSpace(v.FFmpegBin) == "" {
		errs = append(errs, missing("render.video.ffmpeg-bin", "video rendering needs an ffmpeg executable"))
	}
	if v.Enabled && v.Frames == 0 && Duration(v.Duration) <= 0 {
		errs = append(errs, missing("render.video.duration",
			"video rendering needs either a duration or a frame count"))
	}
	return errors.Join(errs...)
}

func validateHTTP(h *HTTP) error {
	var errs []error
	errs = append(errs, checkDuration("http.timeout", h.Timeout, true))
	errs = append(errs, checkDuration("http.retry-base-delay", h.RetryBaseDelay, true))
	errs = append(errs, checkDuration("http.retry-max-delay", h.RetryMaxDelay, true))
	if h.RetryMax < 0 {
		errs = append(errs, invalid("http.retry-max", strconv.Itoa(h.RetryMax), "want 0 or more"))
	}
	return errors.Join(errs...)
}

func validateStage(s *Stage) []error {
	var errs []error
	mode := strings.ToLower(strings.TrimSpace(s.Mode))
	switch mode {
	case "", "none":
	case "url":
		if s.URL == "" {
			errs = append(errs, missing("stage.url", "stage.mode is url"))
		} else if err := checkAbsURL(s.URL); err != nil {
			errs = append(errs, invalid("stage.url", s.URL, err.Error()))
		}
	case "s3":
		if s.S3.Endpoint == "" {
			errs = append(errs, missing("stage.s3.endpoint", "stage.mode is s3"))
		}
		if s.S3.Bucket == "" {
			errs = append(errs, missing("stage.s3.bucket", "stage.mode is s3"))
		}
		if s.S3.AccessKey == "" {
			errs = append(errs, missing("stage.s3.access-key", "stage.mode is s3"))
		}
		if s.S3.SecretKey == "" {
			errs = append(errs, missing("stage.s3.secret-key", "stage.mode is s3"))
		}
		if !s.S3.Presign && s.S3.PublicBaseURL == "" {
			errs = append(errs, missing("stage.s3.public-base-url",
				"stage.s3.presign is false, so the object needs a public base URL"))
		}
		if err := checkDuration("stage.s3.presign-expiry", s.S3.PresignExpiry, true); err != nil {
			errs = append(errs, err)
		}
	case "server":
		errs = append(errs, validateServer(&s.Server)...)
	default:
		errs = append(errs, invalid("stage.mode", s.Mode, "want none, s3, server or url"))
	}
	return errs
}

func validateServer(srv *Server) []error {
	var errs []error
	if srv.Listen == "" {
		errs = append(errs, missing("stage.server.listen", "the stage server needs an address to listen on"))
	}
	if err := checkDuration("stage.server.shutdown-timeout", srv.ShutdownTimeout, true); err != nil {
		errs = append(errs, err)
	}

	tunnelMode := strings.ToLower(strings.TrimSpace(srv.Tunnel.Mode))
	switch tunnelMode {
	case "", "none":
		if srv.PublicURL == "" {
			errs = append(errs, missing("stage.server.public-url",
				"stage.mode is server with no tunnel, so the URL the platform will fetch has to be given"))
		}
	case "ngrok", "zrok", "custom":
		if srv.PublicURL != "" {
			errs = append(errs, invalid("stage.server.public-url", srv.PublicURL,
				"a tunnel discovers the public URL itself; set either the tunnel mode or the public URL, not both"))
		}
		if tunnelMode == "custom" {
			if srv.Tunnel.Bin == "" {
				errs = append(errs, missing("stage.server.tunnel.bin", "tunnel mode is custom"))
			}
			if srv.Tunnel.URLPattern == "" {
				errs = append(errs, missing("stage.server.tunnel.url-pattern", "tunnel mode is custom"))
			}
		}
	default:
		errs = append(errs, invalid("stage.server.tunnel.mode", srv.Tunnel.Mode, "want none, ngrok, zrok or custom"))
	}

	if srv.Tunnel.URLPattern != "" {
		re, err := regexp.Compile(srv.Tunnel.URLPattern)
		if err != nil {
			errs = append(errs, invalid("stage.server.tunnel.url-pattern", srv.Tunnel.URLPattern, err.Error()))
		} else if re.NumSubexp() != 1 {
			errs = append(errs, invalid("stage.server.tunnel.url-pattern", srv.Tunnel.URLPattern,
				"want exactly one capture group, holding the public URL"))
		}
	}
	if err := checkDuration("stage.server.tunnel.startup-timeout", srv.Tunnel.StartupTimeout, true); err != nil {
		errs = append(errs, err)
	}
	if srv.PublicURL != "" {
		if err := checkAbsURL(srv.PublicURL); err != nil {
			errs = append(errs, invalid("stage.server.public-url", srv.PublicURL, err.Error()))
		}
	}
	return errs
}

func validatePublish(p *Publish) []error {
	var errs []error
	if p.Concurrency < 1 {
		errs = append(errs, invalid("publish.concurrency", strconv.Itoa(p.Concurrency), "want 1 or more"))
	}
	for _, d := range []struct {
		key string
		val string
	}{
		{"publish.instagram.poll-interval", p.Instagram.PollInterval},
		{"publish.instagram.poll-timeout", p.Instagram.PollTimeout},
		{"publish.tiktok.poll-interval", p.TikTok.PollInterval},
		{"publish.tiktok.poll-timeout", p.TikTok.PollTimeout},
		{"publish.mastodon.poll-interval", p.Mastodon.PollInterval},
		{"publish.mastodon.poll-timeout", p.Mastodon.PollTimeout},
		{"publish.reddit.poll-interval", p.Reddit.PollInterval},
		{"publish.reddit.poll-timeout", p.Reddit.PollTimeout},
	} {
		if err := checkDuration(d.key, d.val, true); err != nil {
			errs = append(errs, err)
		}
	}
	switch strings.ToLower(strings.TrimSpace(p.Reddit.Kind)) {
	case "auto", "image", "video", "link":
	default:
		errs = append(errs, invalid("publish.reddit.kind", p.Reddit.Kind, "want auto, image, video or link"))
	}

	errs = append(errs, validateCustom(p)...)

	for _, name := range append(append([]string(nil), Platforms...), CustomNames(p)...) {
		l := LayoutOf(p, name)
		if l == nil {
			continue
		}
		if l.Width < 0 || l.Width > MaxDimension {
			errs = append(errs, invalid("publish."+name+".width", strconv.Itoa(l.Width),
				fmt.Sprintf("want 0 to %d", MaxDimension)))
		}
		if l.Height < 0 || l.Height > MaxDimension {
			errs = append(errs, invalid("publish."+name+".height", strconv.Itoa(l.Height),
				fmt.Sprintf("want 0 to %d", MaxDimension)))
		}
	}
	return errs
}

// LayoutOf returns a platform's render overrides, or nil when the name is not
// a platform crier knows.
func LayoutOf(p *Publish, name string) *Layout {
	switch name {
	case "instagram":
		return &p.Instagram.Layout
	case "facebook":
		return &p.Facebook.Layout
	case "tiktok":
		return &p.TikTok.Layout
	case "telegram":
		return &p.Telegram.Layout
	case "x":
		return &p.X.Layout
	case "mastodon":
		return &p.Mastodon.Layout
	case "discord":
		return &p.Discord.Layout
	case "linkedin":
		return &p.LinkedIn.Layout
	case "reddit":
		return &p.Reddit.Layout
	default:
		if c := CustomOf(p, name); c != nil {
			return &c.Layout
		}
		return nil
	}
}

func checkDuration(key, value string, positive bool) error {
	d, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return invalid(key, value, `want a Go duration such as "500ms", "30s" or "1h30m"`)
	}
	if positive && d <= 0 {
		return invalid(key, value, "want a positive duration")
	}
	return nil
}

func checkAbsURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("want an absolute http or https URL")
	}
	if u.Host == "" {
		return errors.New("want an absolute URL with a host")
	}
	return nil
}

// Format is an output image format.
type Format string

const (
	// PNG is lossless and keeps transparency.
	PNG Format = "png"
	// JPEG is lossy, opaque, and the only format Instagram accepts.
	JPEG Format = "jpeg"
)

// ContentType is the MIME type of the format.
func (f Format) ContentType() string {
	if f == JPEG {
		return "image/jpeg"
	}
	return "image/png"
}

// Ext is the file extension of the format, dot included.
func (f Format) Ext() string {
	if f == JPEG {
		return ".jpg"
	}
	return ".png"
}

// ParseFormat reads an image format name, accepting "jpg" for "jpeg".
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "png":
		return PNG, nil
	case "jpeg", "jpg":
		return JPEG, nil
	default:
		return "", fmt.Errorf("unknown image format %q (want png or jpeg)", s)
	}
}

// ParseColor reads a #rgb, #rrggbb or #rrggbbaa colour.
func ParseColor(s string) (color.RGBA, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}, nil
	}
	if !strings.HasPrefix(v, "#") {
		return color.RGBA{}, errors.New("want a hex colour such as #ffffff")
	}
	hex := v[1:]
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 && len(hex) != 8 {
		return color.RGBA{}, errors.New("want #rgb, #rrggbb or #rrggbbaa")
	}
	n, err := strconv.ParseUint(hex, 16, 64)
	if err != nil {
		return color.RGBA{}, errors.New("want a hex colour such as #ffffff")
	}
	if len(hex) == 6 {
		return color.RGBA{R: byteOf(n >> 16), G: byteOf(n >> 8), B: byteOf(n), A: 255}, nil
	}
	return color.RGBA{R: byteOf(n >> 24), G: byteOf(n >> 16), B: byteOf(n >> 8), A: byteOf(n)}, nil
}

// byteOf takes the low eight bits, which is what each colour channel is.
func byteOf(v uint64) uint8 {
	return uint8(v & 0xff) //nolint:gosec // masked to eight bits on the line above
}

// Duration parses a duration this package has already validated. It returns
// the zero duration for text that cannot be parsed, so a caller that skipped
// Validate degrades rather than panics.
func Duration(s string) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return d
}

// Float parses a decimal this package has already validated, falling back to
// def when it cannot be read.
func Float(s string, def float64) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return def
	}
	return f
}

// validateCustom checks the script-backed platforms.
//
// Only the enabled ones are checked for a command: a half-written entry sitting
// disabled in a config file is somebody's work in progress, and refusing to
// start over it would be officious.
func validateCustom(p *Publish) []error {
	var errs []error
	for _, name := range CustomNames(p) {
		c := p.Custom[name]
		at := CustomPrefix + "." + name
		if c.Enabled && strings.TrimSpace(c.Command) == "" {
			errs = append(errs, fmt.Errorf("%s.command is required when the platform is enabled", at))
		}
		if err := checkDuration(at+".timeout", c.Timeout, true); err != nil {
			errs = append(errs, err)
		}
		for _, kind := range c.Kinds {
			switch strings.ToLower(strings.TrimSpace(kind)) {
			case "image", "video":
			default:
				errs = append(errs, invalid(at+".kinds", kind, "want image or video"))
			}
		}
		if len(c.Kinds) == 0 {
			errs = append(errs, invalid(at+".kinds", "", "want at least one of image, video"))
		}
		switch strings.ToLower(strings.TrimSpace(c.Format)) {
		case "png", "jpeg", "jpg":
		default:
			errs = append(errs, invalid(at+".format", c.Format, "want png or jpeg"))
		}
	}
	return errs
}
