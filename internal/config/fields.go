package config

import (
	"sort"
	"strings"

	dispat "github.com/yohimik/dispat/pkg/config"
)

// Binding is one configuration key's two directions: the setter the decoder
// fills the field through, and the getter `crier config` and the docs read it
// back with. They are declared together so a key can never be readable and not
// writable, or the other way round.
type Binding struct {
	Set dispat.Setter
	Get func() any
}

func bindString(p *string) Binding {
	return Binding{Set: dispat.String(p), Get: func() any { return *p }}
}

func bindInt(p *int) Binding {
	return Binding{Set: dispat.Int(p), Get: func() any { return *p }}
}

func bindBool(p *bool) Binding {
	return Binding{Set: dispat.Bool(p), Get: func() any { return *p }}
}

func bindStrings(p *[]string) Binding {
	return Binding{Set: dispat.Strings(p), Get: func() any { return append([]string(nil), *p...) }}
}

// Bindings maps every configuration key to the field of cfg it fills.
//
// It is deliberately flat and keyed by the same dotted paths registry.go
// declares, because that is what makes the anti-drift test a set comparison
// rather than a reflective walk. The nested decode table the loader wants is
// derived from this map by Fields.
func Bindings(cfg *Config) map[string]Binding {
	l, r, h, s, p := &cfg.Log, &cfg.Render, &cfg.HTTP, &cfg.Stage, &cfg.Publish
	s3, srv := &s.S3, &s.Server
	tun := &srv.Tunnel
	ig, fb, tt, tg := &p.Instagram, &p.Facebook, &p.TikTok, &p.Telegram
	x, ma, dc, li, rd := &p.X, &p.Mastodon, &p.Discord, &p.LinkedIn, &p.Reddit
	sl := &p.Slack
	vid := &r.Video

	out := map[string]Binding{
		"log.level":  bindString(&l.Level),
		"log.format": bindString(&l.Format),

		"render.template":       bindString(&r.Template),
		"render.data":           bindString(&r.Data),
		"render.css":            bindStrings(&r.CSS),
		"render.overlays":       bindStrings(&r.Overlays),
		"render.pool":           bindStrings(&r.Pool),
		"render.seed":           bindInt(&r.Seed),
		"render.width":          bindInt(&r.Width),
		"render.height":         bindInt(&r.Height),
		"render.scale":          bindString(&r.Scale),
		"render.supersample":    bindInt(&r.SuperSample),
		"render.format":         bindString(&r.Format),
		"render.jpeg-quality":   bindInt(&r.JPEGQuality),
		"render.output":         bindString(&r.Output),
		"render.base-url":       bindString(&r.BaseURL),
		"render.media-type":     bindString(&r.MediaType),
		"render.background":     bindString(&r.Background),
		"render.fonts-dir":      bindStrings(&r.FontsDir),
		"render.hermetic-fonts": bindBool(&r.HermeticFonts),

		"render.video.enabled":      bindBool(&vid.Enabled),
		"render.video.format":       bindString(&vid.Format),
		"render.video.frames-input": bindString(&vid.FramesInput),
		"render.video.fps":          bindInt(&vid.FPS),
		"render.video.duration":     bindString(&vid.Duration),
		"render.video.frames":       bindInt(&vid.Frames),
		"render.video.ffmpeg-bin":   bindString(&vid.FFmpegBin),
		"render.video.ffmpeg-args":  bindStrings(&vid.FFmpegArgs),
		"render.video.codec-preset": bindString(&vid.CodecPreset),
		"render.video.audio":        bindString(&vid.Audio),

		"http.timeout":          bindString(&h.Timeout),
		"http.upload-timeout":   bindString(&h.UploadTimeout),
		"http.retry-max":        bindInt(&h.RetryMax),
		"http.retry-base-delay": bindString(&h.RetryBaseDelay),
		"http.retry-max-delay":  bindString(&h.RetryMaxDelay),

		"stage.mode": bindString(&s.Mode),
		"stage.url":  bindString(&s.URL),

		"stage.s3.endpoint":        bindString(&s3.Endpoint),
		"stage.s3.region":          bindString(&s3.Region),
		"stage.s3.bucket":          bindString(&s3.Bucket),
		"stage.s3.prefix":          bindString(&s3.Prefix),
		"stage.s3.access-key":      bindString(&s3.AccessKey),
		"stage.s3.secret-key":      bindString(&s3.SecretKey),
		"stage.s3.use-ssl":         bindBool(&s3.UseSSL),
		"stage.s3.acl":             bindString(&s3.ACL),
		"stage.s3.presign":         bindBool(&s3.Presign),
		"stage.s3.presign-expiry":  bindString(&s3.PresignExpiry),
		"stage.s3.public-base-url": bindString(&s3.PublicBaseURL),
		"stage.s3.delete-after":    bindBool(&s3.DeleteAfter),

		"stage.server.listen":           bindString(&srv.Listen),
		"stage.server.public-url":       bindString(&srv.PublicURL),
		"stage.server.shutdown-timeout": bindString(&srv.ShutdownTimeout),

		"stage.server.tunnel.mode":            bindString(&tun.Mode),
		"stage.server.tunnel.bin":             bindString(&tun.Bin),
		"stage.server.tunnel.args":            bindStrings(&tun.Args),
		"stage.server.tunnel.url-pattern":     bindString(&tun.URLPattern),
		"stage.server.tunnel.api-url":         bindString(&tun.APIURL),
		"stage.server.tunnel.startup-timeout": bindString(&tun.StartupTimeout),

		"publish.input":       bindString(&p.Input),
		"publish.caption":     bindString(&p.Caption),
		"publish.concurrency": bindInt(&p.Concurrency),
		"publish.dry-run":     bindBool(&p.DryRun),

		"publish.instagram.enabled":       bindBool(&ig.Enabled),
		"publish.instagram.api-base-url":  bindString(&ig.APIBaseURL),
		"publish.instagram.token":         bindString(&ig.Token),
		"publish.instagram.user-id":       bindString(&ig.UserID),
		"publish.instagram.story":         bindBool(&ig.Story),
		"publish.instagram.caption":       bindString(&ig.Caption),
		"publish.instagram.poll-interval": bindString(&ig.PollInterval),
		"publish.instagram.poll-timeout":  bindString(&ig.PollTimeout),

		"publish.facebook.enabled":      bindBool(&fb.Enabled),
		"publish.facebook.api-base-url": bindString(&fb.APIBaseURL),
		"publish.facebook.token":        bindString(&fb.Token),
		"publish.facebook.page-id":      bindString(&fb.PageID),
		"publish.facebook.story":        bindBool(&fb.Story),
		"publish.facebook.use-url":      bindBool(&fb.UseURL),
		"publish.facebook.caption":      bindString(&fb.Caption),

		"publish.tiktok.enabled":       bindBool(&tt.Enabled),
		"publish.tiktok.api-base-url":  bindString(&tt.APIBaseURL),
		"publish.tiktok.token":         bindString(&tt.Token),
		"publish.tiktok.title":         bindString(&tt.Title),
		"publish.tiktok.privacy-level": bindString(&tt.PrivacyLevel),
		"publish.tiktok.caption":       bindString(&tt.Caption),
		"publish.tiktok.poll-interval": bindString(&tt.PollInterval),
		"publish.tiktok.poll-timeout":  bindString(&tt.PollTimeout),

		"publish.telegram.enabled":      bindBool(&tg.Enabled),
		"publish.telegram.api-base-url": bindString(&tg.APIBaseURL),
		"publish.telegram.token":        bindString(&tg.Token),
		"publish.telegram.chat-id":      bindString(&tg.ChatID),
		"publish.telegram.caption":      bindString(&tg.Caption),

		"publish.slack.enabled":      bindBool(&sl.Enabled),
		"publish.slack.api-base-url": bindString(&sl.APIBaseURL),
		"publish.slack.token":        bindString(&sl.Token),
		"publish.slack.channel":      bindString(&sl.Channel),
		"publish.slack.caption":      bindString(&sl.Caption),

		"publish.x.enabled":      bindBool(&x.Enabled),
		"publish.x.api-base-url": bindString(&x.APIBaseURL),
		"publish.x.token":        bindString(&x.Token),
		"publish.x.caption":      bindString(&x.Caption),

		"publish.mastodon.enabled":       bindBool(&ma.Enabled),
		"publish.mastodon.api-base-url":  bindString(&ma.APIBaseURL),
		"publish.mastodon.token":         bindString(&ma.Token),
		"publish.mastodon.visibility":    bindString(&ma.Visibility),
		"publish.mastodon.alt-text":      bindString(&ma.AltText),
		"publish.mastodon.caption":       bindString(&ma.Caption),
		"publish.mastodon.poll-interval": bindString(&ma.PollInterval),
		"publish.mastodon.poll-timeout":  bindString(&ma.PollTimeout),

		"publish.discord.enabled":     bindBool(&dc.Enabled),
		"publish.discord.webhook-url": bindString(&dc.WebhookURL),
		"publish.discord.username":    bindString(&dc.Username),
		"publish.discord.caption":     bindString(&dc.Caption),

		"publish.linkedin.enabled":      bindBool(&li.Enabled),
		"publish.linkedin.api-base-url": bindString(&li.APIBaseURL),
		"publish.linkedin.token":        bindString(&li.Token),
		"publish.linkedin.author-urn":   bindString(&li.AuthorURN),
		"publish.linkedin.version":      bindString(&li.Version),
		"publish.linkedin.caption":      bindString(&li.Caption),

		"publish.reddit.enabled":       bindBool(&rd.Enabled),
		"publish.reddit.api-base-url":  bindString(&rd.APIBaseURL),
		"publish.reddit.auth-base-url": bindString(&rd.AuthBaseURL),
		"publish.reddit.client-id":     bindString(&rd.ClientID),
		"publish.reddit.client-secret": bindString(&rd.ClientSecret),
		"publish.reddit.refresh-token": bindString(&rd.RefreshToken),
		"publish.reddit.username":      bindString(&rd.Username),
		"publish.reddit.password":      bindString(&rd.Password),
		"publish.reddit.user-agent":    bindString(&rd.UserAgent),
		"publish.reddit.subreddit":     bindString(&rd.Subreddit),
		"publish.reddit.title":         bindString(&rd.Title),
		"publish.reddit.flair-id":      bindString(&rd.FlairID),
		"publish.reddit.kind":          bindString(&rd.Kind),
		"publish.reddit.nsfw":          bindBool(&rd.NSFW),
		"publish.reddit.spoiler":       bindBool(&rd.Spoiler),
		"publish.reddit.caption":       bindString(&rd.Caption),
		"publish.reddit.poll-interval": bindString(&rd.PollInterval),
		"publish.reddit.poll-timeout":  bindString(&rd.PollTimeout),
	}

	// The layout keys are generated exactly like their descriptors are, so a
	// platform can never have the descriptor and not the field.
	for name, l := range map[string]*Layout{
		"instagram": &ig.Layout, "facebook": &fb.Layout, "tiktok": &tt.Layout,
		"telegram": &tg.Layout, "x": &x.Layout, "mastodon": &ma.Layout,
		"discord": &dc.Layout, "linkedin": &li.Layout, "reddit": &rd.Layout,
		"slack": &sl.Layout,
	} {
		out["publish."+name+".overlay"] = bindStrings(&l.Overlay)
		out["publish."+name+".width"] = bindInt(&l.Width)
		out["publish."+name+".height"] = bindInt(&l.Height)
	}

	// The custom platforms a configuration happens to declare. There are none
	// before the file has been read, which is exactly right: the defaults and
	// the environment binding are built from a Config that has none, and the
	// read-back and the path anchoring from one that has them all.
	for _, name := range CustomNames(p) {
		c := p.Custom[name]
		for leaf, bind := range customBindings(c) {
			out[CustomPrefix+"."+name+"."+leaf] = bind
		}
	}
	return out
}

// Fields builds the nested decode table the loader wants from the flat
// bindings, so the two can never disagree about a key.
//
// publish.custom is the exception, and the only one: its keys are names the
// configuration invents, so no flat binding can exist for a name that has not
// been read yet. That subtree gets a setter that creates entries as it decodes
// them, and it replaces whatever the flat bindings would have produced there.
func Fields(cfg *Config) dispat.Fields {
	b := Bindings(cfg)
	flat := make(map[string]dispat.Setter, len(b))
	for k, v := range b {
		if strings.HasPrefix(k, CustomPrefix+".") {
			continue
		}
		flat[k] = v.Set
	}
	return nest(flat, map[string]dispat.Setter{CustomPrefix: customSetter(cfg)})
}

// Values reads every configuration key back out of cfg, keyed by its dotted
// path. Secrets are returned as they are; redaction is the caller's decision.
func Values(cfg *Config) map[string]any {
	b := Bindings(cfg)
	out := make(map[string]any, len(b))
	for k, v := range b {
		out[k] = v.Get()
	}
	return out
}

// nest turns dotted paths into the tree of Fields tables DecodeObject walks.
//
// An intermediate level becomes a setter that decodes its own object, which is
// what makes an unknown key deep in the tree an error naming its full path.
func nest(flat map[string]dispat.Setter, override map[string]dispat.Setter) dispat.Fields {
	type level struct {
		leaves   map[string]dispat.Setter
		children map[string]*level
	}
	newLevel := func() *level {
		return &level{leaves: map[string]dispat.Setter{}, children: map[string]*level{}}
	}
	root := newLevel()

	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		segs := strings.Split(key, ".")
		node := root
		for _, seg := range segs[:len(segs)-1] {
			child, ok := node.children[seg]
			if !ok {
				child = newLevel()
				node.children[seg] = child
			}
			node = child
		}
		node.leaves[segs[len(segs)-1]] = flat[key]
	}

	// An override's own path is opened even when no flat key reaches it, so a
	// dynamic subtree exists in the table before anything has been read into
	// it.
	for path := range override {
		segs := strings.Split(path, ".")
		node := root
		for _, seg := range segs[:len(segs)-1] {
			child, ok := node.children[seg]
			if !ok {
				child = newLevel()
				node.children[seg] = child
			}
			node = child
		}
		node.leaves[segs[len(segs)-1]] = override[path]
	}

	var build func(n *level, path string) dispat.Fields
	build = func(n *level, path string) dispat.Fields {
		out := make(dispat.Fields, len(n.leaves)+len(n.children))
		for k, set := range n.leaves {
			out[strings.ToLower(k)] = set
		}
		for k, child := range n.children {
			table := build(child, join(path, k))
			out[strings.ToLower(k)] = func(val any, at string) error {
				return dispat.DecodeObject(val, at, table)
			}
		}
		return out
	}
	return build(root, "")
}

func join(path, seg string) string {
	if path == "" {
		return seg
	}
	return path + "." + seg
}
