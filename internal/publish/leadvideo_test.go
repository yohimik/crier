package publish

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
)

// mp4Bytes begins like an MP4 and then says something a test can look for.
// Nothing here decodes video: crier reads the first twelve bytes and stops.
var mp4Bytes = append([]byte("\x00\x00\x00\x20ftypisom"), []byte("CRIER-ANTHEM-BYTES")...)

// leadVideoFile writes the clip a test posts.
func leadVideoFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "anthem.mp4")
	if err := os.WriteFile(p, mp4Bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSniffVideoAcceptsAnMP4AndRefusesTheRest(t *testing.T) {
	got, err := SniffVideo(leadVideoFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Attached() || got.Name != "anthem.mp4" || got.ContentType != "video/mp4" {
		t.Errorf("got %+v", got)
	}
	if got.Size != int64(len(mp4Bytes)) {
		t.Errorf("size = %d, want %d", got.Size, len(mp4Bytes))
	}

	refused := map[string]string{
		"a png":      "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
		"a gif":      "GIF89a\x01\x00\x01\x00\x00\x00\x00",
		"an mp3":     "ID3\x04\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00",
		"plain text": "not a video at all, not even close",
		"empty":      "",
	}
	for name, body := range refused {
		t.Run(name, func(t *testing.T) {
			// The extension deliberately lies; the bytes win.
			p := filepath.Join(t.TempDir(), "anthem.mp4")
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := SniffVideo(p)
			if err == nil || !strings.Contains(err.Error(), "does not begin like an MP4") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSniffVideoReportsAMissingFile(t *testing.T) {
	_, err := SniffVideo(filepath.Join(t.TempDir(), "nowhere.mp4"))
	if err == nil || !strings.Contains(err.Error(), "reading the video file") {
		t.Fatalf("err = %v", err)
	}
}

func TestLeadVideoForIsSilentWhereItCannotWork(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Instagram.LeadVideo.File = leadVideoFile(t)

	got, err := LeadVideoFor(&cfg, "instagram")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Attached() {
		t.Error("instagram got no clip")
	}
	// Telegram names none of its own, and there is no shared key.
	other, err := LeadVideoFor(&cfg, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if other.Attached() {
		t.Errorf("telegram got %q", other.Path)
	}
}

// --- instagram -------------------------------------------------------------

// igLeadConfig is a feed post that opens with a clip.
func igLeadConfig(t *testing.T, srvURL string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Publish.Instagram.Enabled = true
	cfg.Publish.Instagram.APIBaseURL = srvURL
	cfg.Publish.Instagram.Token = "tok"
	cfg.Publish.Instagram.UserID = "ig-user"
	cfg.Publish.Instagram.PollInterval = "1ms"
	cfg.Publish.Instagram.PollTimeout = "2s"
	cfg.Publish.Instagram.LeadVideo.File = leadVideoFile(t)
	return &cfg
}

// igFake answers the container, status and publish calls, naming each
// container after the order it was created in.
func igFake(t *testing.T, rec *recorder) string {
	t.Helper()
	n := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		switch {
		case strings.HasSuffix(r.URL.Path, "/media_publish"):
			_, _ = w.Write([]byte(`{"id":"post-1"}`))
		case strings.HasSuffix(r.URL.Path, "/media"):
			if !igContainerIsWellFormed(w, req.Body) {
				return
			}
			n++
			_, _ = w.Write([]byte(`{"id":"c` + itoa(n) + `"}`))
		case strings.HasSuffix(r.URL.Path, "/post-1"):
			_, _ = w.Write([]byte(`{"permalink":"https://www.instagram.com/p/x/"}`))
		default:
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		}
	})
	return srv.URL
}

// igContainerIsWellFormed answers a malformed container the way Instagram
// does, so a fake cannot accept a request the real endpoint refuses.
//
// The one rule worth enforcing: a carousel child carrying a video_url has to
// say media_type=VIDEO. Without it the API presumes an image child and answers
// "The parameter image_url is required", which is how rc.8's feed post failed.
func igContainerIsWellFormed(w http.ResponseWriter, body string) bool {
	v, err := url.ParseQuery(body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	if v.Get("is_carousel_item") != "true" || v.Get("video_url") == "" {
		return true
	}
	if v.Get("media_type") != "VIDEO" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(
			`{"error":{"message":"The parameter image_url is required.","type":"IGApiException","code":100}}`))
		return false
	}
	return true
}

func itoa(n int) string { return string(rune('0' + n)) }

// containers is every /media body the fake received, parsed, in order.
func containers(t *testing.T, rec *recorder) []url.Values {
	t.Helper()
	var out []url.Values
	for _, r := range rec.all() {
		if !strings.HasSuffix(r.Path, "/media") {
			continue
		}
		v, err := url.ParseQuery(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// TestInstagramCarouselOpensWithTheVideo is the whole feature at Instagram:
// the video child is created first and the parent lists it first.
func TestInstagramCarouselOpensWithTheVideo(t *testing.T) {
	rec := newRecorder()
	cfg := igLeadConfig(t, igFake(t, rec))

	p := onlyPublisher(t, cfg)
	res, err := p.Publish(context.Background(), Input{
		Artifact:     imageArtifact(t),
		Artifacts:    realPages(t, 2),
		URLs:         []string{"https://cdn/1.jpg", "https://cdn/2.jpg"},
		URL:          "https://cdn/1.jpg",
		LeadVideoURL: "https://cdn/anthem.mp4",
		Caption:      "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "post-1" {
		t.Errorf("result = %+v", res)
	}

	got := containers(t, rec)
	if len(got) != 4 {
		t.Fatalf("created %d containers, want the video, two images and the parent", len(got))
	}

	// The video child comes first: video_url, is_carousel_item, and
	// media_type=VIDEO. The reference omits VIDEO from that parameter's enum,
	// and the endpoint answers a child without it with "The parameter
	// image_url is required" — it presumes an image child. This assertion
	// exists because the documentation led the other way once already.
	lead := got[0]
	if lead.Get("video_url") != "https://cdn/anthem.mp4" {
		t.Errorf("the first container is not the clip: %v", lead)
	}
	if lead.Get("is_carousel_item") != "true" {
		t.Errorf("the clip is not a carousel item: %v", lead)
	}
	if lead.Get("media_type") != "VIDEO" {
		t.Errorf("media_type = %q, want VIDEO or instagram asks for an image_url",
			lead.Get("media_type"))
	}
	if lead.Has("caption") {
		t.Error("meta does not accept a caption on a carousel child")
	}

	for n, child := range got[1:3] {
		if child.Get("image_url") == "" || child.Get("is_carousel_item") != "true" {
			t.Errorf("image child %d = %v", n+1, child)
		}
	}

	parent := got[3]
	if parent.Get("media_type") != "CAROUSEL" {
		t.Errorf("parent = %v", parent)
	}
	if got := parent.Get("children"); got != "c1,c2,c3" {
		t.Errorf("children = %q, want the video first", got)
	}
	if parent.Get("caption") != "hello" {
		t.Errorf("the caption belongs to the parent: %v", parent)
	}
}

// TestInstagramVideoChildIsAwaited: video children process asynchronously, so
// the parent must not be created before the clip's container says FINISHED.
func TestInstagramVideoChildIsAwaited(t *testing.T) {
	rec := newRecorder()
	polls := 0
	n := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case strings.HasSuffix(r.URL.Path, "/media_publish"):
			_, _ = w.Write([]byte(`{"id":"post-1"}`))
		case strings.HasSuffix(r.URL.Path, "/media"):
			n++
			_, _ = w.Write([]byte(`{"id":"c` + itoa(n) + `"}`))
		case strings.HasSuffix(r.URL.Path, "/c1"):
			// The clip is still encoding for the first two polls.
			polls++
			if polls < 3 {
				_, _ = w.Write([]byte(`{"status_code":"IN_PROGRESS"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		default:
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		}
	})

	p := onlyPublisher(t, igLeadConfig(t, srv.URL))
	if _, err := p.Publish(context.Background(), Input{
		Artifact:     imageArtifact(t),
		URLs:         []string{"https://cdn/1.jpg"},
		URL:          "https://cdn/1.jpg",
		LeadVideoURL: "https://cdn/anthem.mp4",
	}); err != nil {
		t.Fatal(err)
	}
	if polls < 3 {
		t.Errorf("polled the video container %d times; it should have waited", polls)
	}

	// The image child was not created until the clip had finished.
	order := rec.paths()
	firstStatus, secondMedia := -1, -1
	seen := 0
	for i, path := range order {
		switch {
		case strings.HasSuffix(path, "/c1"):
			if firstStatus < 0 {
				firstStatus = i
			}
		case strings.HasSuffix(path, "/media"):
			seen++
			if seen == 2 {
				secondMedia = i
			}
		}
	}
	if firstStatus < 0 || secondMedia < 0 || firstStatus > secondMedia {
		t.Errorf("the second container was created before the clip finished: %v", order)
	}
}

// TestInstagramLeadVideoMakesACarouselOfOnePage: a clip and one card are two
// items, and two items is a carousel rather than a single-image post.
func TestInstagramLeadVideoMakesACarouselOfOnePage(t *testing.T) {
	rec := newRecorder()
	p := onlyPublisher(t, igLeadConfig(t, igFake(t, rec)))
	if _, err := p.Publish(context.Background(), Input{
		Artifact:     imageArtifact(t),
		URLs:         []string{"https://cdn/1.jpg"},
		URL:          "https://cdn/1.jpg",
		LeadVideoURL: "https://cdn/anthem.mp4",
		Caption:      "one page",
	}); err != nil {
		t.Fatal(err)
	}

	got := containers(t, rec)
	if len(got) != 3 {
		t.Fatalf("created %d containers, want the clip, the page and the parent", len(got))
	}
	if got[2].Get("children") != "c1,c2" {
		t.Errorf("children = %q", got[2].Get("children"))
	}
}

// TestInstagramLeadVideoCountsAgainstTheCarousel: ten items, and the clip is
// one of them.
func TestInstagramLeadVideoCountsAgainstTheCarousel(t *testing.T) {
	silent := config.Defaults()
	silent.Publish.Instagram.Enabled = true
	silent.Publish.Instagram.APIBaseURL = "https://ig.example"
	silent.Publish.Instagram.Token = "t"
	silent.Publish.Instagram.UserID = "u"
	if got := onlyPublisher(t, &silent).Needs().Capacity(); got != IGCarouselMax {
		t.Errorf("without a clip the capacity is %d, want %d", got, IGCarouselMax)
	}

	cfg := igLeadConfig(t, "https://ig.example")
	p := onlyPublisher(t, cfg)
	if got := p.Needs().Capacity(); got != IGCarouselMax-1 {
		t.Errorf("with a clip the capacity is %d, want %d", got, IGCarouselMax-1)
	}

	// The refusal is the backstop for a post that got past the batching.
	urls := make([]string, IGCarouselMax)
	for i := range urls {
		urls[i] = "https://cdn/x.jpg"
	}
	_, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URLs: urls, URL: urls[0],
		LeadVideoURL: "https://cdn/anthem.mp4",
	})
	if err == nil {
		t.Fatal("expected the post to be refused")
	}
	for _, want := range []string{"plus the lead video", "publish.instagram.max-attachments"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
}

// TestInstagramLeadVideoNeedsAStagedURL: Instagram fetches, so a clip with no
// address is a configuration problem said plainly.
func TestInstagramLeadVideoNeedsAStagedURL(t *testing.T) {
	rec := newRecorder()
	p := onlyPublisher(t, igLeadConfig(t, igFake(t, rec)))
	_, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URLs: []string{"https://cdn/1.jpg"}, URL: "https://cdn/1.jpg",
	})
	if err == nil || !strings.Contains(err.Error(), "public URL for the lead video") {
		t.Fatalf("err = %v", err)
	}
}

// TestInstagramStoryIgnoresTheLeadVideo: a story has no carousel to open, and
// the same config file drives a feed pass and a story pass.
func TestInstagramStoryIgnoresTheLeadVideo(t *testing.T) {
	rec := newRecorder()
	cfg := igLeadConfig(t, igFake(t, rec))
	cfg.Publish.Instagram.Story = true

	p := onlyPublisher(t, cfg)
	if got := p.Needs().Capacity(); got != 1 {
		t.Errorf("a story takes one item, got %d", got)
	}
	if _, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URLs: []string{"https://cdn/1.jpg"}, URL: "https://cdn/1.jpg",
		LeadVideoURL: "https://cdn/anthem.mp4",
	}); err != nil {
		t.Fatal(err)
	}
	got := containers(t, rec)
	if len(got) != 1 || got[0].Get("media_type") != "STORIES" {
		t.Fatalf("containers = %v, want one story", got)
	}
	if got[0].Get("video_url") != "" {
		t.Errorf("the story carried the lead video: %v", got[0])
	}
}

// --- telegram --------------------------------------------------------------

func telegramLeadConfig(t *testing.T, srvURL string) *config.Config {
	t.Helper()
	cfg := telegramConfig(srvURL)
	cfg.Publish.Telegram.LeadVideo.File = leadVideoFile(t)
	return cfg
}

// TestTelegramAlbumOpensWithTheVideo: a media group is the one Telegram shape
// that mixes a video with photos, and the clip is entry one.
func TestTelegramAlbumOpensWithTheVideo(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(
			`{"ok":true,"result":[{"message_id":10,"chat":{"id":1,"username":"chan"}}]}`))
	})

	arts := realPages(t, 2)
	p := onlyPublisher(t, telegramLeadConfig(t, srv.URL))
	if _, err := p.Publish(context.Background(), Input{
		Artifact: arts[0], Artifacts: arts, Caption: "hello",
	}); err != nil {
		t.Fatal(err)
	}

	reqs := rec.all()
	if len(reqs) != 1 || !strings.HasSuffix(reqs[0].Path, "/sendMediaGroup") {
		t.Fatalf("requests = %v", rec.paths())
	}
	media := mediaArray(t, reqs[0].Body)
	if len(media) != 3 {
		t.Fatalf("media = %v, want the clip and two pages", media)
	}
	if media[0].Type != "video" || media[0].Media != "attach://lead" {
		t.Errorf("the album does not open with the clip: %+v", media[0])
	}
	for n, m := range media[1:] {
		if m.Type != "photo" {
			t.Errorf("item %d is %q, want a photo", n+2, m.Type)
		}
	}
	// The caption belongs to the album's first item, which is now the clip.
	if media[0].Caption != "hello" {
		t.Errorf("the clip carries no caption: %+v", media[0])
	}
	if media[1].Caption != "" {
		t.Errorf("a second caption would hide the first: %+v", media[1])
	}
	if !strings.Contains(reqs[0].Body, `name="lead"`) {
		t.Errorf("the clip is not a multipart part: %q", reqs[0].Body)
	}
	if !strings.Contains(reqs[0].Body, "CRIER-ANTHEM-BYTES") {
		t.Error("the clip's bytes did not go out")
	}
}

// TestTelegramLeadVideoMakesAnAlbumOfOnePage: one card and a clip is two
// items, which is a media group rather than a sendPhoto.
func TestTelegramLeadVideoMakesAnAlbumOfOnePage(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(
			`{"ok":true,"result":[{"message_id":10,"chat":{"id":1,"username":"chan"}}]}`))
	})
	p := onlyPublisher(t, telegramLeadConfig(t, srv.URL))
	if _, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(rec.paths()[0], "/sendMediaGroup") {
		t.Errorf("requests = %v", rec.paths())
	}
	if got := mediaArray(t, rec.all()[0].Body); len(got) != 2 || got[0].Type != "video" {
		t.Errorf("media = %+v", got)
	}
}

// TestTelegramCarriesBothAClipAndATrack: the clip is in the album and the
// audio is the message after it, so neither crowds the other out.
func TestTelegramCarriesBothAClipAndATrack(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if strings.HasSuffix(r.URL.Path, "/sendMediaGroup") {
			_, _ = w.Write([]byte(
				`{"ok":true,"result":[{"message_id":10,"chat":{"id":1,"username":"chan"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":11,"chat":{"id":1}}}`))
	})
	cfg := telegramLeadConfig(t, srv.URL)
	cfg.Publish.MusicFile = musicArtifact(t)

	p := onlyPublisher(t, cfg)
	// The album keeps its ten items; only the clip takes one of them.
	if got := p.Needs().Capacity(); got != TelegramGroupMax-1 {
		t.Errorf("capacity = %d, want %d", got, TelegramGroupMax-1)
	}
	if _, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)}); err != nil {
		t.Fatal(err)
	}

	paths := rec.paths()
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/sendMediaGroup") ||
		!strings.HasSuffix(paths[1], "/sendAudio") {
		t.Fatalf("requests = %v, want the album then the track", paths)
	}
	if got := mediaArray(t, rec.all()[0].Body); got[0].Type != "video" {
		t.Errorf("the album does not open with the clip: %+v", got)
	}
}

func TestTelegramLeadVideoCountsAgainstTheAlbum(t *testing.T) {
	p := onlyPublisher(t, telegramLeadConfig(t, "https://telegram.example"))
	arts := realPages(t, TelegramGroupMax)
	_, err := p.Publish(context.Background(), Input{Artifact: arts[0], Artifacts: arts})
	if err == nil {
		t.Fatal("expected the post to be refused")
	}
	for _, want := range []string{"plus the lead video", "publish.telegram.max-attachments"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
}

// TestALeadVideoThatIsNotAnMP4StopsTheBuild: checked where a missing token is
// checked, which is before anything is rendered.
func TestALeadVideoThatIsNotAnMP4StopsTheBuild(t *testing.T) {
	notVideo := filepath.Join(t.TempDir(), "anthem.mp4")
	if err := os.WriteFile(notVideo, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range config.LeadVideoPlatforms {
		t.Run(name, func(t *testing.T) {
			cfg := config.Defaults()
			switch name {
			case "instagram":
				cfg.Publish.Instagram.Enabled = true
				cfg.Publish.Instagram.APIBaseURL = "https://ig.example"
				cfg.Publish.Instagram.Token = "t"
				cfg.Publish.Instagram.UserID = "u"
			case "telegram":
				cfg.Publish.Telegram.Enabled = true
				cfg.Publish.Telegram.APIBaseURL = "https://tg.example"
				cfg.Publish.Telegram.Token = "123:abc"
				cfg.Publish.Telegram.ChatID = "@c"
			}
			config.LeadVideoOf(&cfg.Publish, name).File = notVideo

			_, err := Build(&cfg, testDeps(t))
			if err == nil {
				t.Fatal("expected the build to fail")
			}
			for _, want := range []string{"publish." + name + ".lead-video", "does not begin like an MP4"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in: %v", want, err)
				}
			}
		})
	}
}

func TestCheckLeadVideosReportsOneRowPerPlatform(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Telegram.LeadVideo.File = leadVideoFile(t)
	cfg.Publish.Instagram.LeadVideo.File = leadVideoFile(t)

	got := CheckLeadVideos(&cfg)
	if len(got) != 2 {
		t.Fatalf("rows = %+v", got)
	}
	// Documented order: instagram, then telegram.
	if got[0].Platform != "instagram" || got[1].Platform != "telegram" {
		t.Fatalf("rows = %+v", got)
	}
	if !strings.Contains(got[0].Describe(), "instagram is not enabled") {
		t.Errorf("describe = %q", got[0].Describe())
	}
	if !strings.Contains(got[1].Describe(), "opens the telegram post") {
		t.Errorf("describe = %q", got[1].Describe())
	}
}

// mediaArray parses the media parameter out of a multipart sendMediaGroup body.
func mediaArray(t *testing.T, body string) []telegramMedia {
	t.Helper()
	start := strings.Index(body, `name="media"`)
	if start < 0 {
		t.Fatalf("no media field in:\n%s", body)
	}
	rest := body[start:]
	open := strings.Index(rest, "[")
	end := strings.Index(rest, "]")
	if open < 0 || end < open {
		t.Fatalf("no media array in:\n%s", rest)
	}
	var out []telegramMedia
	if err := json.Unmarshal([]byte(rest[open:end+1]), &out); err != nil {
		t.Fatalf("%v\n%s", err, rest[open:end+1])
	}
	return out
}
