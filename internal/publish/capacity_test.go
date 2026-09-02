package publish

import (
	"testing"

	"github.com/yohimik/crier/internal/config"
)

// TestEveryPlatformDeclaresItsCapacity pins the number each platform takes in
// one post, and says where the number came from.
//
// The list is the test: a platform whose capacity changes without this file
// changing is a platform whose posts would silently start splitting, or
// silently start being refused.
func TestEveryPlatformDeclaresItsCapacity(t *testing.T) {
	cfg := config.Defaults()
	p := &cfg.Publish

	// Enough configuration for each constructor to succeed. Nothing here is
	// contacted: Needs is answered from the configuration alone.
	p.Instagram.Token, p.Instagram.UserID = "t", "u"
	p.Facebook.Token, p.Facebook.PageID = "t", "p"
	p.TikTok.Token = "t"
	p.Telegram.Token, p.Telegram.ChatID = "t", "@c"
	p.X.Token = "t"
	p.Mastodon.Token, p.Mastodon.APIBaseURL = "t", "https://m.example"
	p.Discord.WebhookURL = "https://discord.example/webhook"
	p.LinkedIn.Token, p.LinkedIn.AuthorURN = "t", "urn:li:person:x"
	p.Reddit.ClientID, p.Reddit.ClientSecret = "i", "s"
	p.Reddit.Username, p.Reddit.Password = "u", "pw"
	p.Reddit.Subreddit, p.Reddit.Title = "r", "t"
	p.Slack.Token, p.Slack.Channel = "xoxb-t", "C1"
	p.VK.Token, p.VK.OwnerID = "t", -1
	p.Threads.Token, p.Threads.UserID = "t", "u"
	p.YouTube.ClientID, p.YouTube.ClientSecret = "i", "s"
	p.YouTube.RefreshToken = "r"
	p.Boosty.Blog, p.Boosty.AccessToken = "crier", "t"

	want := map[string]int{
		// Documented by the platform.
		"instagram": 10, // carousel, "limited to 10 images, videos, or a mix"
		"telegram":  10, // sendMediaGroup, "must include 2-10 items"
		"x":         4,  // media_ids, maxItems 4
		"linkedin":  20, // multiImage, "minimum of 2 and maximum of 20"
		"threads":   20, // a carousel holds 20, and refuses fewer than 2
		"tiktok":    35, // photo_images, "up to 35 photo content URLs"
		// crier's own ceiling, where the platform documents the mechanism and
		// no limit on it.
		"facebook": 10,
		"discord":  10,
		"slack":    10,
		"vk":       10, // wall.post, "no more than 10 media objects"
		"boosty":   10, // boosty documents nothing and no client names a limit
		"mastodon": 4,  // the instance's max_media_attachments; 4 is the default
		// One at a time, on purpose.
		"reddit":  1, // galleries need an endpoint reddit does not document
		"youtube": 1, // videos.insert uploads one video; there is no carousel
	}

	for name := range want {
		p.Instagram.Enabled = name == "instagram"
		p.Facebook.Enabled = name == "facebook"
		p.TikTok.Enabled = name == "tiktok"
		p.Telegram.Enabled = name == "telegram"
		p.X.Enabled = name == "x"
		p.Mastodon.Enabled = name == "mastodon"
		p.Discord.Enabled = name == "discord"
		p.LinkedIn.Enabled = name == "linkedin"
		p.Reddit.Enabled = name == "reddit"
		p.Slack.Enabled = name == "slack"
		p.VK.Enabled = name == "vk"
		p.Threads.Enabled = name == "threads"
		p.YouTube.Enabled = name == "youtube"
		p.Boosty.Enabled = name == "boosty"

		pubs, err := Build(&cfg, testDeps(t))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(pubs) != 1 {
			t.Fatalf("%s: built %d publishers", name, len(pubs))
		}
		if got := pubs[0].Needs().Capacity(); got != want[name] {
			t.Errorf("%s takes %d files per post, want %d", name, got, want[name])
		}
	}
}

// TestStoriesTakeOneFileAtATime: neither Instagram nor Facebook has a carousel
// for stories, so a paged run has to become a run of stories rather than one
// post with several pictures.
func TestStoriesTakeOneFileAtATime(t *testing.T) {
	cfg := config.Defaults()
	p := &cfg.Publish
	p.Instagram.Enabled, p.Instagram.Token, p.Instagram.UserID = true, "t", "u"
	p.Facebook.Enabled, p.Facebook.Token, p.Facebook.PageID = true, "t", "p"

	pubs, err := Build(&cfg, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, pub := range pubs {
		if got := pub.Needs().Capacity(); got < 2 {
			t.Errorf("%s takes %d as a feed post; it should carry a set", pub.Name(), got)
		}
	}

	p.Instagram.Story, p.Facebook.Story = true, true
	pubs, err = Build(&cfg, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, pub := range pubs {
		if got := pub.Needs().Capacity(); got != 1 {
			t.Errorf("%s takes %d as a story; stories have no carousel", pub.Name(), got)
		}
	}
}

// TestACustomPlatformsCapacityIsItsConfiguration, because the command is the
// platform and there is no API here to know better.
func TestACustomPlatformsCapacityIsItsConfiguration(t *testing.T) {
	for want, set := range map[int]int{1: 0, 3: 3, 12: 12} {
		cfg := config.Defaults()
		cfg.Publish.Custom = map[string]*config.Custom{
			"mine": {Enabled: true, Command: "true"},
		}
		cfg.Publish.Custom["mine"].Layout.MaxAttachments = set

		pubs, err := Build(&cfg, testDeps(t))
		if err != nil {
			t.Fatal(err)
		}
		if got := pubs[0].Needs().Capacity(); got != want {
			t.Errorf("max-attachments %d gives a capacity of %d, want %d", set, got, want)
		}
	}
}
