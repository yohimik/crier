package publish

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// realPages makes n image artifacts that exist on disk, which is what a
// publisher that uploads bytes needs.
func realPages(t *testing.T, n int) []render.Artifact {
	t.Helper()
	out := make([]render.Artifact, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, imageArtifact(t))
	}
	return out
}

// --- discord ---------------------------------------------------------------

func discordMusicConfig(t *testing.T, url string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Publish.Discord.Enabled = true
	cfg.Publish.Discord.WebhookURL = url + "/api/webhooks/1/token"
	cfg.Publish.MusicFile = musicArtifact(t)
	return &cfg
}

// TestDiscordAttachesTheAudioToTheSameMessage: the track is one more file in
// the message the pictures are in, so it plays under them.
func TestDiscordAttachesTheAudioToTheSameMessage(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"id":"99","channel_id":"55"}`))
	})

	arts := realPages(t, 2)
	p := onlyPublisher(t, discordMusicConfig(t, srv.URL))
	if _, err := p.Publish(context.Background(), Input{
		Artifact: arts[0], Artifacts: arts, Caption: "hi",
	}); err != nil {
		t.Fatal(err)
	}

	reqs := rec.all()
	if len(reqs) != 1 {
		t.Fatalf("the audio should ride along, not make a second message: %v", rec.paths())
	}
	body := reqs[0].Body
	// The pages keep their indexes and the audio takes the next one.
	for _, want := range []string{`name="files[0]"`, `name="files[1]"`, `name="files[2]"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in the multipart body", want)
		}
	}
	if !strings.Contains(body, `filename="jingle.mp3"`) {
		t.Errorf("the audio file name is missing: %q", body)
	}
	if !strings.Contains(body, "audio/mpeg") {
		t.Errorf("the audio part carries no audio content type: %q", body)
	}
	// The audio is last, after every page.
	if strings.Index(body, `name="files[2]"`) < strings.Index(body, `name="files[1]"`) {
		t.Error("the audio should come after the pictures")
	}
}

// TestDiscordCountsTheAudioAgainstTheFileCap: the audio takes a slot, so ten
// pages and a track is eleven files and does not fit.
func TestDiscordCountsTheAudioAgainstTheFileCap(t *testing.T) {
	cfg := discordMusicConfig(t, "https://discord.example")
	p := onlyPublisher(t, cfg)

	arts := realPages(t, DiscordFileMax)
	_, err := p.Publish(context.Background(), Input{Artifact: arts[0], Artifacts: arts})
	if err == nil {
		t.Fatal("expected the post to be refused")
	}
	for _, want := range []string{"plus the audio", "publish.discord.max-attachments"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
}

// TestMusicReservesAFileSlot: the cap the pipeline paginates against already
// counts the audio, so the refusal above is a backstop rather than something
// an ordinary run walks into.
func TestMusicReservesAFileSlot(t *testing.T) {
	tests := []struct {
		platform string
		full     int
	}{
		{"discord", DiscordFileMax},
		{"slack", SlackFileMax},
	}
	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			silent := config.Defaults()
			enableForMusic(t, &silent, tt.platform)
			if got := onlyPublisher(t, &silent).Needs().Capacity(); got != tt.full {
				t.Errorf("without music the capacity is %d, want %d", got, tt.full)
			}

			withMusic := config.Defaults()
			enableForMusic(t, &withMusic, tt.platform)
			withMusic.Publish.MusicFile = musicArtifact(t)
			if got := onlyPublisher(t, &withMusic).Needs().Capacity(); got != tt.full-1 {
				t.Errorf("with music the capacity is %d, want %d", got, tt.full-1)
			}
		})
	}
}

// TestTelegramKeepsItsAlbumSize: the audio is its own message there, so it
// takes nothing away from the album.
func TestTelegramKeepsItsAlbumSize(t *testing.T) {
	cfg := config.Defaults()
	enableForMusic(t, &cfg, "telegram")
	cfg.Publish.MusicFile = musicArtifact(t)
	if got := onlyPublisher(t, &cfg).Needs().Capacity(); got != TelegramGroupMax {
		t.Errorf("capacity = %d, want %d", got, TelegramGroupMax)
	}
}

func TestDiscordRefusesAnOversizedAudioFile(t *testing.T) {
	cfg := discordMusicConfig(t, "https://discord.example")
	p := onlyPublisher(t, cfg)
	dc, ok := p.(*Discord)
	if !ok {
		t.Fatalf("built a %T", p)
	}
	dc.music.Size = DiscordUploadLimit + 1

	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "discord's limit") {
		t.Fatalf("err = %v", err)
	}
}

// --- slack -----------------------------------------------------------------

func slackMusicConfig(t *testing.T, url string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Publish.Slack.Enabled = true
	cfg.Publish.Slack.APIBaseURL = url
	cfg.Publish.Slack.Token = "xoxb-1"
	cfg.Publish.Slack.Channel = "C1"
	cfg.Publish.MusicFile = musicArtifact(t)
	return &cfg
}

// TestSlackSharesTheAudioInTheSameMessage: step three takes every file at
// once, so the pictures and the track land as one message.
func TestSlackSharesTheAudioInTheSameMessage(t *testing.T) {
	rec := newRecorder()
	var srvURL string
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case strings.HasSuffix(r.URL.Path, "/files.getUploadURLExternal"):
			_, _ = w.Write([]byte(`{"ok":true,"upload_url":"` + srvURL + `/put","file_id":"F1"}`))
		case strings.HasSuffix(r.URL.Path, "/files.completeUploadExternal"):
			_, _ = w.Write([]byte(`{"ok":true,"files":[{"id":"F1","title":"card"}]}`))
		default:
			_, _ = w.Write([]byte("OK"))
		}
	})
	srvURL = srv.URL

	arts := realPages(t, 2)
	p := onlyPublisher(t, slackMusicConfig(t, srv.URL))
	if _, err := p.Publish(context.Background(), Input{
		Artifact: arts[0], Artifacts: arts, Caption: "hi",
	}); err != nil {
		t.Fatal(err)
	}

	// Two pictures and one track: three slots, three uploads, one share.
	if got := countPaths(rec, "/files.getUploadURLExternal"); got != 3 {
		t.Errorf("asked for %d upload slots, want 3", got)
	}
	if got := countPaths(rec, "/put"); got != 3 {
		t.Errorf("uploaded %d files, want 3", got)
	}
	if got := countPaths(rec, "/files.completeUploadExternal"); got != 1 {
		t.Errorf("shared in %d calls, want 1 so it is one message", got)
	}

	slots := bodiesFor(rec, "/files.getUploadURLExternal")
	if !strings.Contains(slots[len(slots)-1], "jingle.mp3") {
		t.Errorf("the audio slot was not asked for last: %q", slots[len(slots)-1])
	}
	share := bodiesFor(rec, "/files.completeUploadExternal")[0]
	if !strings.Contains(share, "jingle.mp3") {
		t.Errorf("the audio is not in the files array: %q", share)
	}
}

func TestSlackCountsTheAudioAgainstTheFileCap(t *testing.T) {
	p := onlyPublisher(t, slackMusicConfig(t, "https://slack.example"))
	arts := realPages(t, SlackFileMax)
	_, err := p.Publish(context.Background(), Input{Artifact: arts[0], Artifacts: arts})
	if err == nil {
		t.Fatal("expected the post to be refused")
	}
	for _, want := range []string{"plus the audio", "publish.slack.max-attachments"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
}

// --- telegram --------------------------------------------------------------

func telegramMusicConfig(t *testing.T, url string) *config.Config {
	t.Helper()
	cfg := telegramConfig(url)
	cfg.Publish.MusicFile = musicArtifact(t)
	return cfg
}

// TestTelegramSendsTheAudioAfterThePost is the ordering rule. Audio cannot
// join a media group, so it is a second message — and a second message that
// arrived first would be a track above the pictures it belongs to.
func TestTelegramSendsTheAudioAfterThePost(t *testing.T) {
	tests := []struct {
		name string
		in   func(t *testing.T) Input
		post string
	}{
		{"one page", func(t *testing.T) Input {
			return Input{Artifact: imageArtifact(t)}
		}, "/sendPhoto"},
		{"an album", func(t *testing.T) Input {
			arts := realPages(t, 3)
			return Input{Artifact: arts[0], Artifacts: arts}
		}, "/sendMediaGroup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newRecorder()
			srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				if strings.HasSuffix(r.URL.Path, "/sendMediaGroup") {
					_, _ = w.Write([]byte(
						`{"ok":true,"result":[{"message_id":10,"chat":{"id":1,"username":"chan"}}]}`))
					return
				}
				_, _ = w.Write([]byte(
					`{"ok":true,"result":{"message_id":11,"chat":{"id":1,"username":"chan"}}}`))
			})

			p := onlyPublisher(t, telegramMusicConfig(t, srv.URL))
			res, err := p.Publish(context.Background(), tt.in(t))
			if err != nil {
				t.Fatal(err)
			}

			paths := rec.paths()
			if len(paths) != 2 {
				t.Fatalf("requests = %v, want the post and the audio", paths)
			}
			if !strings.HasSuffix(paths[0], tt.post) {
				t.Errorf("the first request was %q, want %s", paths[0], tt.post)
			}
			if !strings.HasSuffix(paths[1], "/sendAudio") {
				t.Errorf("the second request was %q, want sendAudio", paths[1])
			}

			audio := rec.all()[1]
			if !strings.Contains(audio.Body, `name="audio"`) {
				t.Errorf("the audio part is missing: %q", audio.Body)
			}
			if !strings.Contains(audio.Body, `filename="jingle.mp3"`) {
				t.Errorf("the audio file name is missing: %q", audio.Body)
			}
			if !strings.Contains(audio.Body, "@channel") {
				t.Errorf("the audio went to no chat: %q", audio.Body)
			}
			if res.Extra["audioMessageId"] != "11" {
				t.Errorf("the audio message was not reported: %+v", res.Extra)
			}
		})
	}
}

// TestTelegramAudioFailureIsAWarning: the pictures are already published, and
// there is no taking them back. Reporting the platform as failed would say
// something untrue.
func TestTelegramAudioFailureIsAWarning(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if strings.HasSuffix(r.URL.Path, "/sendAudio") {
			_, _ = w.Write([]byte(`{"ok":false,"description":"file is too big"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42,"chat":{"id":1,"username":"chan"}}}`))
	})

	p := onlyPublisher(t, telegramMusicConfig(t, srv.URL))
	res, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err != nil {
		t.Fatalf("the post itself went out, so this should not fail: %v", err)
	}
	if res.ID != "42" {
		t.Errorf("result = %+v", res)
	}
	if _, ok := res.Extra["audioMessageId"]; ok {
		t.Errorf("an audio message that was refused should not be reported: %+v", res.Extra)
	}
	if len(rec.all()) != 2 {
		t.Errorf("requests = %v", rec.paths())
	}
}

// TestTelegramSendsNoAudioWhenNoneIsConfigured guards the ordinary case: one
// message, exactly as before.
func TestTelegramSendsNoAudioWhenNoneIsConfigured(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42,"chat":{"id":1}}}`))
	})
	p := onlyPublisher(t, telegramConfig(srv.URL))
	if _, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)}); err != nil {
		t.Fatal(err)
	}
	if len(rec.all()) != 1 {
		t.Errorf("requests = %v, want just the post", rec.paths())
	}
}

// --- tiktok ----------------------------------------------------------------

func TestTikTokAsksForARecommendedTrack(t *testing.T) {
	for _, on := range []bool{true, false} {
		name := "off"
		if on {
			name = "on"
		}
		t.Run(name, func(t *testing.T) {
			rec := newRecorder()
			srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				if strings.HasSuffix(r.URL.Path, "/status/fetch/") {
					_, _ = w.Write([]byte(`{"data":{"status":"PUBLISH_COMPLETE"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":{"publish_id":"pub1"}}`))
			})
			cfg := tiktokConfig(srv.URL)
			cfg.Publish.TikTok.AutoAddMusic = on

			p := onlyPublisher(t, cfg)
			if _, err := p.Publish(context.Background(), Input{
				Artifact: imageArtifact(t), URL: "https://cdn/x.jpg",
			}); err != nil {
				t.Fatal(err)
			}

			body := rec.all()[0].Body
			if !strings.Contains(body, `"post_mode":"DIRECT_POST"`) {
				t.Errorf("auto_add_music only works on a direct post: %q", body)
			}
			if got := strings.Contains(body, `"auto_add_music":true`); got != on {
				t.Errorf("auto_add_music present = %v, want %v: %q", got, on, body)
			}
		})
	}
}

// --- building --------------------------------------------------------------

// TestAMusicFileThatIsNotAudioStopsTheBuild: the check happens where a missing
// token is checked, which is before anything is rendered or uploaded.
func TestAMusicFileThatIsNotAudioStopsTheBuild(t *testing.T) {
	notAudio := writeAudio(t, "cover.mp3", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))

	for _, name := range config.MusicPlatforms {
		t.Run(name, func(t *testing.T) {
			cfg := config.Defaults()
			enableForMusic(t, &cfg, name)
			cfg.Publish.MusicFile = notAudio

			_, err := Build(&cfg, testDeps(t))
			if err == nil {
				t.Fatal("expected the build to fail")
			}
			for _, want := range []string{"publish.music-file", "mp3, m4a, ogg and wav"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in: %v", want, err)
				}
			}
		})
	}
}

// enableForMusic turns on one of the three platforms with enough
// configuration to be built.
func enableForMusic(t *testing.T, cfg *config.Config, name string) {
	t.Helper()
	switch name {
	case "discord":
		cfg.Publish.Discord.Enabled = true
		cfg.Publish.Discord.WebhookURL = "https://discord.example/webhook"
	case "slack":
		cfg.Publish.Slack.Enabled = true
		cfg.Publish.Slack.APIBaseURL = "https://slack.example"
		cfg.Publish.Slack.Token = "xoxb-1"
		cfg.Publish.Slack.Channel = "C1"
	case "telegram":
		cfg.Publish.Telegram.Enabled = true
		cfg.Publish.Telegram.APIBaseURL = "https://telegram.example"
		cfg.Publish.Telegram.Token = "123:abc"
		cfg.Publish.Telegram.ChatID = "@channel"
	default:
		t.Fatalf("no setup for %q", name)
	}
}

// countPaths is how many recorded requests touched a path fragment.
func countPaths(rec *recorder, fragment string) int {
	n := 0
	for _, r := range rec.all() {
		if strings.Contains(r.Path, fragment) {
			n++
		}
	}
	return n
}

// bodiesFor is the bodies of the requests that touched a path fragment, in
// the order they arrived.
func bodiesFor(rec *recorder, fragment string) []string {
	var out []string
	for _, r := range rec.all() {
		if strings.Contains(r.Path, fragment) {
			out = append(out, r.Body)
		}
	}
	return out
}
