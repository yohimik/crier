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
		return []textField{{"publish.instagram.caption", &p.Instagram.Caption}}
	case "facebook":
		return []textField{{"publish.facebook.caption", &p.Facebook.Caption}}
	case "tiktok":
		return []textField{
			{"publish.tiktok.caption", &p.TikTok.Caption},
			{"publish.tiktok.title", &p.TikTok.Title},
		}
	case "telegram":
		return []textField{{"publish.telegram.caption", &p.Telegram.Caption}}
	case "x":
		return []textField{{"publish.x.caption", &p.X.Caption}}
	case "mastodon":
		return []textField{
			{"publish.mastodon.caption", &p.Mastodon.Caption},
			{"publish.mastodon.alt-text", &p.Mastodon.AltText},
		}
	case "discord":
		return []textField{{"publish.discord.caption", &p.Discord.Caption}}
	case "linkedin":
		return []textField{{"publish.linkedin.caption", &p.LinkedIn.Caption}}
	case "reddit":
		return []textField{
			{"publish.reddit.caption", &p.Reddit.Caption},
			{"publish.reddit.title", &p.Reddit.Title},
		}
	default:
		// A custom platform has one text, and it is a caption like any other.
		if c := config.CustomOf(p, platform); c != nil {
			return []textField{{config.CustomPrefix + "." + platform + ".caption", &c.Caption}}
		}
		return nil
	}
}

// captionOf is a platform's own caption, already rendered.
func captionOf(cfg *config.Config, platform string) string {
	fields := textFields(cfg, platform)
	if len(fields) == 0 {
		return ""
	}
	return *fields[0].Ptr
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
	if own := captionOf(cfg, platform); own != "" {
		return own, nil
	}
	out, err := engine.RenderCaption(cfg.Publish.Caption, data, platform)
	if err != nil {
		return "", failf(ExitRender, "publish.caption: %v", err)
	}
	return out, nil
}
