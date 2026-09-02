package app

import (
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/template"
)

// textField is one configuration value that is itself a template: a caption, a
// title, an alt text.
type textField struct {
	Key string
	Ptr *string
	// Caption marks the field a post's body is written in. Those are rendered
	// once per post rather than once per run, because a paged run binds that
	// post's own numbers into them.
	Caption bool
}

// textFields are the post-text values of one platform.
//
// They are listed rather than derived because each platform calls its text
// something different — TikTok has a title as well as a description, Mastodon
// has an alt text, Reddit's title is mandatory — and the list is what the tests
// check for completeness.
func textFields(cfg *config.Config, platform string) []textField {
	p := &cfg.Publish
	switch platform {
	case "instagram":
		return []textField{{"publish.instagram.caption", &p.Instagram.Caption, true}}
	case "facebook":
		return []textField{{"publish.facebook.caption", &p.Facebook.Caption, true}}
	case "tiktok":
		return []textField{
			{"publish.tiktok.caption", &p.TikTok.Caption, true},
			{"publish.tiktok.title", &p.TikTok.Title, false},
		}
	case "telegram":
		return []textField{{"publish.telegram.caption", &p.Telegram.Caption, true}}
	case "x":
		return []textField{{"publish.x.caption", &p.X.Caption, true}}
	case "slack":
		return []textField{{"publish.slack.caption", &p.Slack.Caption, true}}
	case "vk":
		return []textField{{"publish.vk.caption", &p.VK.Caption, true}}
	case "threads":
		return []textField{{"publish.threads.caption", &p.Threads.Caption, true}}
	case "mastodon":
		return []textField{
			{"publish.mastodon.caption", &p.Mastodon.Caption, true},
			{"publish.mastodon.alt-text", &p.Mastodon.AltText, false},
		}
	case "discord":
		return []textField{{"publish.discord.caption", &p.Discord.Caption, true}}
	case "linkedin":
		return []textField{{"publish.linkedin.caption", &p.LinkedIn.Caption, true}}
	case "reddit":
		return []textField{
			{"publish.reddit.caption", &p.Reddit.Caption, true},
			{"publish.reddit.title", &p.Reddit.Title, false},
		}
	default:
		// A custom platform has one text, and it is a caption like any other.
		if c := config.CustomOf(p, platform); c != nil {
			return []textField{{config.CustomPrefix + "." + platform + ".caption", &c.Caption, true}}
		}
		return nil
	}
}

// captionOf is a platform's own caption template, before it is rendered.
func captionOf(cfg *config.Config, platform string) string {
	for _, f := range textFields(cfg, platform) {
		if f.Caption {
			return *f.Ptr
		}
	}
	return ""
}

// ResolveTexts renders every platform's post text as a template, in place.
//
// A caption is configuration like everything else, and a caption that can say
// `Release {{.Version}} is out` says once what would otherwise be repeated for
// each platform. The data is the same document the layout was rendered with,
// plus the platform's own name, so one line can also say where it is going.
func ResolveTexts(engine *template.Engine, cfg *config.Config, data any) error {
	for _, name := range publish.Enabled(cfg) {
		for _, f := range textFields(cfg, name) {
			if f.Caption {
				// A caption is rendered per post instead, by Captions.
				continue
			}
			out, err := engine.RenderCaption(*f.Ptr, data, name)
			if err != nil {
				return failf(ExitRender, "%s: %v", f.Key, err)
			}
			*f.Ptr = out
		}
	}
	return nil
}

// CaptionFor is the text one platform posts with: its own caption when it has
// one, and the shared publish.caption otherwise.
func CaptionFor(engine *template.Engine, cfg *config.Config, platform string, data any) (string, error) {
	return CaptionAt(engine, cfg, platform, data, template.OnePost())
}

// CaptionAt is CaptionFor for one post of a paged run, with that post's own
// numbers bound so a caption can write "2 of 3".
func CaptionAt(engine *template.Engine, cfg *config.Config, platform string, data any,
	at template.Paging,
) (string, error) {
	tmpl, key := captionOf(cfg, platform), "publish."+platform+".caption"
	if tmpl == "" {
		tmpl, key = cfg.Publish.Caption, "publish.caption"
	}
	out, err := engine.RenderCaptionAt(tmpl, data, platform, at)
	if err != nil {
		return "", failf(ExitRender, "%s: %v", key, err)
	}
	return out, nil
}
