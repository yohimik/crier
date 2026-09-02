package publish

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/render"
)

func testLogger(t *testing.T) zerolog.Logger {
	return zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel)
}

func testDeps(t *testing.T) Deps {
	return Deps{
		Client: httpx.New(httpx.Options{
			Retry:  httpx.RetryPolicy{Max: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, Timeout: 10 * time.Second},
			Logger: testLogger(t),
		}),
		Logger:    testLogger(t),
		UserAgent: "crier/test",
	}
}

// artifact writes a file and describes it, which is what every publisher takes.
func artifact(t *testing.T, kind render.Kind, format config.Format, body string) render.Artifact {
	t.Helper()
	dir := t.TempDir()
	name := "card" + format.Ext()
	contentType := format.ContentType()
	if kind == render.KindVideo {
		name, contentType = "clip.mp4", render.VideoContentType
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return render.Artifact{
		Kind: kind, Format: format, ContentType: contentType,
		Path: p, Size: int64(len(body)), Width: 100, Height: 100,
	}
}

func imageArtifact(t *testing.T) render.Artifact {
	return artifact(t, render.KindImage, config.JPEG, "JPEGDATA")
}

func videoArtifact(t *testing.T, size int) render.Artifact {
	return artifact(t, render.KindVideo, "", strings.Repeat("v", size))
}

// recorder collects the requests a fake platform received.
type recorder struct {
	mu       chan struct{}
	requests []recorded
}

type recorded struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

func newRecorder() *recorder {
	r := &recorder{mu: make(chan struct{}, 1)}
	r.mu <- struct{}{}
	return r
}

func (r *recorder) record(req *http.Request) recorded {
	body := readAll(req)
	rec := recorded{
		Method: req.Method, Path: req.URL.Path, Query: req.URL.RawQuery,
		Header: req.Header.Clone(), Body: body,
	}
	<-r.mu
	r.requests = append(r.requests, rec)
	r.mu <- struct{}{}
	return rec
}

func (r *recorder) all() []recorded {
	<-r.mu
	out := append([]recorded(nil), r.requests...)
	r.mu <- struct{}{}
	return out
}

func (r *recorder) paths() []string {
	var out []string
	for _, req := range r.all() {
		out = append(out, req.Method+" "+req.Path)
	}
	return out
}

func readAll(req *http.Request) string {
	if req.Body == nil {
		return ""
	}
	buf := make([]byte, 1<<20)
	n, _ := req.Body.Read(buf)
	for {
		m, err := req.Body.Read(buf[n:])
		n += m
		if err != nil || n == len(buf) {
			break
		}
	}
	return string(buf[:n])
}

func fakeServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func mustBuild(t *testing.T, cfg *config.Config) []Publisher {
	t.Helper()
	ps, err := Build(cfg, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func onlyPublisher(t *testing.T, cfg *config.Config) Publisher {
	t.Helper()
	ps := mustBuild(t, cfg)
	if len(ps) != 1 {
		t.Fatalf("built %d publishers, want 1", len(ps))
	}
	return ps[0]
}

// --- registry --------------------------------------------------------------

func TestRegistryCoversEveryConfiguredPlatform(t *testing.T) {
	for _, name := range config.Platforms {
		if _, ok := registry[name]; !ok {
			t.Errorf("no publisher for the configured platform %q", name)
		}
	}
	for name := range registry {
		found := false
		for _, p := range config.Platforms {
			if p == name {
				found = true
			}
		}
		if !found {
			t.Errorf("publisher %q is not a configured platform", name)
		}
	}
	if len(Names()) != len(config.Platforms) {
		t.Errorf("Names has %d entries, want %d", len(Names()), len(config.Platforms))
	}
}

func TestEnabledFollowsTheConfiguration(t *testing.T) {
	cfg := config.Defaults()
	if got := Enabled(&cfg); len(got) != 0 {
		t.Errorf("nothing is enabled by default, got %v", got)
	}
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Reddit.Enabled = true
	got := Enabled(&cfg)
	if len(got) != 2 || got[0] != "telegram" || got[1] != "reddit" {
		t.Errorf("Enabled = %v", got)
	}
	if enabledIn(&cfg, "myspace") {
		t.Error("an unknown platform is never enabled")
	}
}

func TestBuildReportsEveryMisconfiguredPlatform(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Instagram.Enabled = true
	_, err := Build(&cfg, testDeps(t))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"publish.telegram.token", "publish.instagram.token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestBuildNeedsAClient(t *testing.T) {
	cfg := config.Defaults()
	if _, err := Build(&cfg, Deps{}); err == nil {
		t.Error("expected an error with no HTTP client")
	}
}

func TestNeedsHelpers(t *testing.T) {
	n := Needs{Formats: []config.Format{config.JPEG, config.PNG}, Kinds: imageOnly}
	if !n.Accepts(render.KindImage) || n.Accepts(render.KindVideo) {
		t.Error("Accepts")
	}
	available := map[config.Format]render.Artifact{config.PNG: {Format: config.PNG}}
	got, ok := n.Prefers(available)
	if !ok || got.Format != config.PNG {
		t.Errorf("Prefers = %v %v", got, ok)
	}
	if _, ok := (Needs{Formats: []config.Format{config.JPEG}}).Prefers(available); ok {
		t.Error("Prefers should refuse when nothing matches")
	}
}

// --- telegram --------------------------------------------------------------

func telegramConfig(url string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Telegram.APIBaseURL = url
	cfg.Publish.Telegram.Token = "123:abc"
	cfg.Publish.Telegram.ChatID = "@channel"
	return &cfg
}

func TestTelegramSendsAPhoto(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42,"chat":{"id":1,"username":"chan"}}}`))
	})

	p := onlyPublisher(t, telegramConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), Caption: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "42" || res.URL != "https://t.me/chan/42" {
		t.Errorf("result = %+v", res)
	}

	reqs := rec.all()
	if len(reqs) != 1 || reqs[0].Path != "/bot123:abc/sendPhoto" {
		t.Fatalf("requests = %v", rec.paths())
	}
	if !strings.Contains(reqs[0].Body, `name="chat_id"`) || !strings.Contains(reqs[0].Body, "@channel") {
		t.Errorf("body = %q", reqs[0].Body)
	}
	if !strings.Contains(reqs[0].Body, `name="photo"`) {
		t.Errorf("the photo part is missing: %q", reqs[0].Body)
	}
	if !strings.Contains(reqs[0].Body, "hello") {
		t.Errorf("the caption is missing: %q", reqs[0].Body)
	}
}

func TestTelegramSendsAVideo(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":7,"chat":{"id":1}}}`))
	})
	p := onlyPublisher(t, telegramConfig(srv.URL))
	if _, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 100)}); err != nil {
		t.Fatal(err)
	}
	reqs := rec.all()
	if !strings.HasSuffix(reqs[0].Path, "/sendVideo") {
		t.Errorf("path = %q", reqs[0].Path)
	}
	if !strings.Contains(reqs[0].Body, "supports_streaming") {
		t.Errorf("body = %q", reqs[0].Body)
	}
}

func TestTelegramReportsARefusal(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	})
	p := onlyPublisher(t, telegramConfig(srv.URL))
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestTelegramRefusesAnOversizedVideo(t *testing.T) {
	p := onlyPublisher(t, telegramConfig("http://unused.example"))
	a := videoArtifact(t, 10)
	a.Size = TelegramVideoLimit + 1
	_, err := p.Publish(context.Background(), Input{Artifact: a})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
}

func TestTelegramNeedsItsKeys(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Telegram.Enabled = true
	if _, err := Build(&cfg, testDeps(t)); err == nil {
		t.Error("expected a missing token error")
	}
	cfg.Publish.Telegram.Token = "t"
	if _, err := Build(&cfg, testDeps(t)); err == nil || !strings.Contains(err.Error(), "chat-id") {
		t.Errorf("err = %v", err)
	}
}

// --- discord ---------------------------------------------------------------

func TestDiscordPostsToTheWebhook(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"id":"99","channel_id":"55"}`))
	})
	cfg := config.Defaults()
	cfg.Publish.Discord.Enabled = true
	cfg.Publish.Discord.WebhookURL = srv.URL + "/api/webhooks/1/token"
	cfg.Publish.Discord.Username = "crier"

	p := onlyPublisher(t, &cfg)
	res, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), Caption: "hi there"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "99" || !strings.Contains(res.URL, "55") {
		t.Errorf("result = %+v", res)
	}
	req := rec.all()[0]
	if req.Query != "wait=true" {
		t.Errorf("query = %q, want wait=true so the message comes back", req.Query)
	}
	if !strings.Contains(req.Body, `name="files[0]"`) {
		t.Errorf("the file part is missing: %q", req.Body)
	}
	if !strings.Contains(req.Body, `"content":"hi there"`) || !strings.Contains(req.Body, `"username":"crier"`) {
		t.Errorf("payload_json = %q", req.Body)
	}
}

func TestDiscordChecksTheWebhookURL(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Discord.Enabled = true
	if _, err := Build(&cfg, testDeps(t)); err == nil {
		t.Error("expected a missing webhook error")
	}
	cfg.Publish.Discord.WebhookURL = "not-a-url"
	if _, err := Build(&cfg, testDeps(t)); err == nil || !strings.Contains(err.Error(), "full webhook URL") {
		t.Errorf("err = %v", err)
	}
}

func TestDiscordRefusesAnOversizedFile(t *testing.T) {
	cfg := config.Defaults()
	cfg.Publish.Discord.Enabled = true
	cfg.Publish.Discord.WebhookURL = "https://discord.example/webhook"
	p := onlyPublisher(t, &cfg)
	a := imageArtifact(t)
	a.Size = DiscordUploadLimit + 1
	if _, err := p.Publish(context.Background(), Input{Artifact: a}); err == nil {
		t.Error("expected a size error")
	}
}

// --- mastodon --------------------------------------------------------------

func mastodonConfig(url string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.Mastodon.Enabled = true
	cfg.Publish.Mastodon.APIBaseURL = url
	cfg.Publish.Mastodon.Token = "tok"
	cfg.Publish.Mastodon.PollInterval = "1ms"
	cfg.Publish.Mastodon.PollTimeout = "2s"
	return &cfg
}

func TestMastodonUploadsAndPosts(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case r.URL.Path == "/api/v2/media":
			_, _ = w.Write([]byte(`{"id":"m1","url":"https://cdn/x.jpg"}`))
		case r.URL.Path == "/api/v1/statuses":
			_, _ = w.Write([]byte(`{"id":"s1","url":"https://m.example/@a/s1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	p := onlyPublisher(t, mastodonConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), Caption: "toot"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "s1" || res.URL != "https://m.example/@a/s1" || res.Extra["mediaId"] != "m1" {
		t.Errorf("result = %+v", res)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("requests = %v", rec.paths())
	}
	if reqs[1].Header.Get("Idempotency-Key") == "" {
		t.Error("the status request should carry an idempotency key")
	}
	if !strings.Contains(reqs[1].Body, `"visibility":"public"`) {
		t.Errorf("status body = %q", reqs[1].Body)
	}
}

func TestMastodonWaitsForProcessing(t *testing.T) {
	polls := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/media":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"m1"}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/media/"):
			polls++
			if polls < 3 {
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte(`{"id":"m1"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"m1","url":"https://cdn/x"}`))
		case r.URL.Path == "/api/v1/statuses":
			_, _ = w.Write([]byte(`{"id":"s1","url":"u"}`))
		}
	})
	p := onlyPublisher(t, mastodonConfig(srv.URL))
	if _, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 10)}); err != nil {
		t.Fatal(err)
	}
	if polls < 3 {
		t.Errorf("polled %d times, want it to have waited", polls)
	}
}

func TestMastodonProcessingTimeout(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/media" {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"m1"}`))
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte(`{"id":"m1"}`))
	})
	cfg := mastodonConfig(srv.URL)
	cfg.Publish.Mastodon.PollTimeout = "20ms"
	p := onlyPublisher(t, cfg)
	_, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 10)})
	if !errors.Is(err, httpx.ErrPollTimeout) {
		t.Fatalf("err = %v", err)
	}
}

func TestMastodonValidatesVisibility(t *testing.T) {
	cfg := mastodonConfig("https://m.example")
	cfg.Publish.Mastodon.Visibility = "shouted"
	if _, err := Build(cfg, testDeps(t)); err == nil {
		t.Error("expected a visibility error")
	}
}

func TestMastodonNeedsAnAttachmentID(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	p := onlyPublisher(t, mastodonConfig(srv.URL))
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "no attachment id") {
		t.Fatalf("err = %v", err)
	}
}

// --- x ---------------------------------------------------------------------

func xConfig(url string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.X.Enabled = true
	cfg.Publish.X.APIBaseURL = url
	cfg.Publish.X.Token = "tok"
	return &cfg
}

func TestXPostsAnImage(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch r.URL.Path {
		case "/2/media/upload":
			_, _ = w.Write([]byte(`{"data":{"id":"media-1"}}`))
		case "/2/tweets":
			_, _ = w.Write([]byte(`{"data":{"id":"tweet-1","text":"hi"}}`))
		}
	})
	p := onlyPublisher(t, xConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), Caption: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "tweet-1" || !strings.Contains(res.URL, "tweet-1") {
		t.Errorf("result = %+v", res)
	}
	reqs := rec.all()
	if !strings.Contains(reqs[1].Body, `"media_ids":["media-1"]`) {
		t.Errorf("the tweet should reference the media: %q", reqs[1].Body)
	}
	if reqs[0].Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("auth = %q", reqs[0].Header.Get("Authorization"))
	}
}

func TestXUploadsAVideoInSegments(t *testing.T) {
	rec := newRecorder()
	statusCalls := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		switch {
		case r.URL.Query().Get("command") == "STATUS":
			statusCalls++
			if statusCalls < 2 {
				_, _ = w.Write([]byte(`{"data":{"id":"m","processing_info":{"state":"in_progress","check_after_secs":0}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"id":"m","processing_info":{"state":"succeeded"}}}`))
		case strings.Contains(req.Body, "INIT"):
			_, _ = w.Write([]byte(`{"data":{"id":"m"}}`))
		case strings.Contains(req.Body, "APPEND"):
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(req.Body, "FINALIZE"):
			_, _ = w.Write([]byte(`{"data":{"id":"m","processing_info":{"state":"pending","check_after_secs":0}}}`))
		case r.URL.Path == "/2/tweets":
			_, _ = w.Write([]byte(`{"data":{"id":"t"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	p := onlyPublisher(t, xConfig(srv.URL))
	// Two full segments plus a short one.
	a := videoArtifact(t, 16)
	a.Size = int64(len("v") * 16)
	if _, err := p.Publish(context.Background(), Input{Artifact: a, Caption: "clip"}); err != nil {
		t.Fatal(err)
	}
	appends := 0
	for _, req := range rec.all() {
		if strings.Contains(req.Body, "APPEND") {
			appends++
		}
	}
	if appends != 1 {
		t.Errorf("appends = %d, want one segment for a tiny file", appends)
	}
	if statusCalls < 2 {
		t.Errorf("status calls = %d, want the processing wait", statusCalls)
	}
}

func TestXReportsAFailedTranscode(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		switch {
		case r.URL.Query().Get("command") == "STATUS":
			_, _ = w.Write([]byte(`{"data":{"processing_info":{"state":"failed","error":{"name":"E","message":"bad codec"}}}}`))
		case strings.Contains(body, "INIT"):
			_, _ = w.Write([]byte(`{"data":{"id":"m"}}`))
		case strings.Contains(body, "FINALIZE"):
			_, _ = w.Write([]byte(`{"data":{"id":"m","processing_info":{"state":"pending","check_after_secs":0}}}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	p := onlyPublisher(t, xConfig(srv.URL))
	_, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 8)})
	if err == nil || !strings.Contains(err.Error(), "bad codec") {
		t.Fatalf("err = %v", err)
	}
}

func TestXRefusesAnOversizedVideo(t *testing.T) {
	p := onlyPublisher(t, xConfig("http://unused.example"))
	a := videoArtifact(t, 8)
	a.Size = XVideoLimit + 1
	if _, err := p.Publish(context.Background(), Input{Artifact: a}); err == nil {
		t.Error("expected a size error")
	}
}

// --- instagram -------------------------------------------------------------

func instagramConfig(url string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.Instagram.Enabled = true
	cfg.Publish.Instagram.APIBaseURL = url
	cfg.Publish.Instagram.Token = "tok"
	cfg.Publish.Instagram.UserID = "123"
	cfg.Publish.Instagram.PollInterval = "1ms"
	cfg.Publish.Instagram.PollTimeout = "2s"
	return &cfg
}

func TestInstagramContainerThenPublish(t *testing.T) {
	rec := newRecorder()
	polls := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			polls++
			if polls < 2 {
				_, _ = w.Write([]byte(`{"status_code":"IN_PROGRESS"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		case "/123/media_publish":
			_, _ = w.Write([]byte(`{"id":"p1"}`))
		case "/p1":
			// Instagram is the only one that knows the shortcode; the media id
			// is not it.
			_, _ = w.Write([]byte(`{"permalink":"https://www.instagram.com/p/CxYz123/"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	p := onlyPublisher(t, instagramConfig(srv.URL))
	if !p.Needs().URL {
		t.Fatal("instagram must declare that it needs a URL")
	}
	if p.Needs().Accepts(render.KindImage) != true {
		t.Fatal("instagram accepts images")
	}
	res, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/x.jpg", Caption: "post",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "p1" || res.Extra["containerId"] != "c1" {
		t.Errorf("result = %+v", res)
	}
	// The reported link is the permalink Instagram gave, not the media id
	// glued onto /p/ — that URL is a 404, and it was reported on every
	// successful post.
	if res.URL != "https://www.instagram.com/p/CxYz123/" {
		t.Errorf("url = %q, want the permalink instagram reported", res.URL)
	}
	if strings.Contains(res.URL, "/p/p1") {
		t.Error("the media id was used as a shortcode; that link 404s")
	}
	first := rec.all()[0]
	if !strings.Contains(first.Body, "image_url=https") || !strings.Contains(first.Body, "caption=post") {
		t.Errorf("container body = %q", first.Body)
	}
}

func TestInstagramStoryUsesTheStoryMediaType(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		default:
			_, _ = w.Write([]byte(`{"id":"p1"}`))
		}
	})
	cfg := instagramConfig(srv.URL)
	cfg.Publish.Instagram.Story = true
	p := onlyPublisher(t, cfg)
	if _, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), URL: "https://x/y.jpg"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.all()[0].Body, "media_type=STORIES") {
		t.Errorf("body = %q", rec.all()[0].Body)
	}
}

func TestInstagramVideoUsesReels(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch r.URL.Path {
		case "/123/media":
			_, _ = w.Write([]byte(`{"id":"c1"}`))
		case "/c1":
			_, _ = w.Write([]byte(`{"status_code":"FINISHED"}`))
		default:
			_, _ = w.Write([]byte(`{"id":"p1"}`))
		}
	})
	p := onlyPublisher(t, instagramConfig(srv.URL))
	if _, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 10), URL: "https://x/y.mp4"}); err != nil {
		t.Fatal(err)
	}
	body := rec.all()[0].Body
	if !strings.Contains(body, "media_type=REELS") || !strings.Contains(body, "video_url=") {
		t.Errorf("body = %q", body)
	}
}

func TestInstagramNeedsAStagedURL(t *testing.T) {
	p := onlyPublisher(t, instagramConfig("http://unused.example"))
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "stage.mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstagramContainerError(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/123/media" {
			_, _ = w.Write([]byte(`{"id":"c1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status_code":"ERROR","status":"could not fetch the image"}`))
	})
	p := onlyPublisher(t, instagramConfig(srv.URL))
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), URL: "https://x/y.jpg"})
	if err == nil || !strings.Contains(err.Error(), "could not fetch the image") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstagramContainerTimeout(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/123/media" {
			_, _ = w.Write([]byte(`{"id":"c1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status_code":"IN_PROGRESS"}`))
	})
	cfg := instagramConfig(srv.URL)
	cfg.Publish.Instagram.PollTimeout = "20ms"
	p := onlyPublisher(t, cfg)
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), URL: "https://x/y.jpg"})
	if !errors.Is(err, httpx.ErrPollTimeout) {
		t.Fatalf("err = %v", err)
	}
}

// --- facebook --------------------------------------------------------------

func facebookConfig(url string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.Facebook.Enabled = true
	cfg.Publish.Facebook.APIBaseURL = url
	cfg.Publish.Facebook.Token = "tok"
	cfg.Publish.Facebook.PageID = "page1"
	return &cfg
}

func TestFacebookUploadsAPhoto(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"id":"ph1","post_id":"page1_1"}`))
	})
	p := onlyPublisher(t, facebookConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), Caption: "look"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "page1_1" || res.Extra["photoId"] != "ph1" {
		t.Errorf("result = %+v", res)
	}
	req := rec.all()[0]
	if req.Path != "/page1/photos" {
		t.Errorf("path = %q", req.Path)
	}
	if !strings.Contains(req.Body, `name="source"`) || !strings.Contains(req.Body, "look") {
		t.Errorf("body = %q", req.Body)
	}
}

func TestFacebookStoryIsTwoCalls(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if strings.HasSuffix(r.URL.Path, "/photo_stories") {
			_, _ = w.Write([]byte(`{"success":true,"post_id":"story1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"ph1"}`))
	})
	cfg := facebookConfig(srv.URL)
	cfg.Publish.Facebook.Story = true
	p := onlyPublisher(t, cfg)
	res, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "story1" {
		t.Errorf("result = %+v", res)
	}
	paths := rec.paths()
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/page1/photos") || !strings.HasSuffix(paths[1], "/page1/photo_stories") {
		t.Fatalf("paths = %v", paths)
	}
	if !strings.Contains(rec.all()[0].Body, "published") {
		t.Errorf("the photo should be uploaded unpublished: %q", rec.all()[0].Body)
	}
	if !strings.Contains(rec.all()[1].Body, "photo_id=ph1") {
		t.Errorf("the story should reference the photo: %q", rec.all()[1].Body)
	}
}

func TestFacebookUseURL(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"id":"ph1"}`))
	})
	cfg := facebookConfig(srv.URL)
	cfg.Publish.Facebook.UseURL = true
	p := onlyPublisher(t, cfg)
	if !p.Needs().URL {
		t.Fatal("use-url mode needs a staged URL")
	}
	if _, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), URL: "https://cdn/x.jpg"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.all()[0].Body, "url=https") {
		t.Errorf("body = %q", rec.all()[0].Body)
	}

	if _, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)}); err == nil {
		t.Error("use-url with nothing staged should fail")
	}
}

func TestFacebookVideo(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"id":"v1"}`))
	})
	p := onlyPublisher(t, facebookConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 10), Caption: "clip"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Extra["videoId"] != "v1" {
		t.Errorf("result = %+v", res)
	}
	if !strings.HasSuffix(rec.all()[0].Path, "/page1/videos") {
		t.Errorf("path = %q", rec.all()[0].Path)
	}
}

// --- tiktok ----------------------------------------------------------------

func tiktokConfig(url string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.TikTok.Enabled = true
	cfg.Publish.TikTok.APIBaseURL = url
	cfg.Publish.TikTok.Token = "tok"
	cfg.Publish.TikTok.PollInterval = "1ms"
	cfg.Publish.TikTok.PollTimeout = "2s"
	return &cfg
}

func TestTikTokPhotoIsPulledFromAURL(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if strings.HasSuffix(r.URL.Path, "/status/fetch/") {
			_, _ = w.Write([]byte(`{"data":{"status":"PUBLISH_COMPLETE"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"publish_id":"pub1"}}`))
	})
	p := onlyPublisher(t, tiktokConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn/x.jpg", Caption: "tik",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "pub1" {
		t.Errorf("result = %+v", res)
	}
	body := rec.all()[0].Body
	if !strings.Contains(body, `"source":"PULL_FROM_URL"`) || !strings.Contains(body, "https://cdn/x.jpg") {
		t.Errorf("body = %q", body)
	}
	if !strings.Contains(body, `"privacy_level":"SELF_ONLY"`) {
		t.Errorf("the default privacy level should be the safe one: %q", body)
	}
}

func TestTikTokVideoUploadsChunks(t *testing.T) {
	rec := newRecorder()
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case strings.HasSuffix(r.URL.Path, "/status/fetch/"):
			_, _ = w.Write([]byte(`{"data":{"status":"PUBLISH_COMPLETE"}}`))
		case strings.HasSuffix(r.URL.Path, "/video/init/"):
			_, _ = w.Write([]byte(`{"data":{"publish_id":"pub1","upload_url":"` + r.Host + `"}}`))
		default:
			w.WriteHeader(http.StatusCreated)
		}
	})
	// The upload URL has to be absolute; point it back at the same server.
	rec2 := newRecorder()
	upload := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec2.record(r)
		w.WriteHeader(http.StatusCreated)
	})
	srv2 := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case strings.HasSuffix(r.URL.Path, "/status/fetch/"):
			_, _ = w.Write([]byte(`{"data":{"status":"PUBLISH_COMPLETE"}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"publish_id":"pub1","upload_url":"` + upload.URL + `"}}`))
		}
	})
	_ = srv

	p := onlyPublisher(t, tiktokConfig(srv2.URL))
	if _, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 1000)}); err != nil {
		t.Fatal(err)
	}
	uploads := rec2.all()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d", len(uploads))
	}
	if got := uploads[0].Header.Get("Content-Range"); got != "bytes 0-999/1000" {
		t.Errorf("content range = %q", got)
	}
}

func TestTikTokReportsAnEnvelopeError(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_params","message":"bad url","log_id":"L1"}}`))
	})
	p := onlyPublisher(t, tiktokConfig(srv.URL))
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), URL: "https://x/y.jpg"})
	if err == nil || !strings.Contains(err.Error(), "bad url") || !strings.Contains(err.Error(), "L1") {
		t.Fatalf("err = %v", err)
	}
}

func TestTikTokPublishFailure(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status/fetch/") {
			_, _ = w.Write([]byte(`{"data":{"status":"FAILED","fail_reason":"spam"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"publish_id":"p"}}`))
	})
	p := onlyPublisher(t, tiktokConfig(srv.URL))
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), URL: "https://x/y.jpg"})
	if err == nil || !strings.Contains(err.Error(), "spam") {
		t.Fatalf("err = %v", err)
	}
}

func TestTikTokValidatesPrivacyLevel(t *testing.T) {
	cfg := tiktokConfig("https://x")
	cfg.Publish.TikTok.PrivacyLevel = "EVERYONE"
	if _, err := Build(cfg, testDeps(t)); err == nil {
		t.Error("expected a privacy level error")
	}
}

func TestTikTokPhotoNeedsAURL(t *testing.T) {
	p := onlyPublisher(t, tiktokConfig("http://unused.example"))
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "stage.mode") {
		t.Fatalf("err = %v", err)
	}
}

// --- linkedin --------------------------------------------------------------

func linkedinConfig(url string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.LinkedIn.Enabled = true
	cfg.Publish.LinkedIn.APIBaseURL = url
	cfg.Publish.LinkedIn.Token = "tok"
	cfg.Publish.LinkedIn.AuthorURN = "urn:li:person:abc"
	return &cfg
}

func TestLinkedInImageFlow(t *testing.T) {
	rec := newRecorder()
	var uploadURL string
	upload := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.WriteHeader(http.StatusCreated)
	})
	uploadURL = upload.URL + "/upload"

	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case strings.HasPrefix(r.URL.Path, "/rest/images/"):
			// The image status poll the upload now waits on. Rest.li wants
			// the URN's colons percent-encoded in the path and answers a raw
			// colon with this 400 — rc.13 died on it in production, so the
			// fake refuses it the way LinkedIn does.
			if !strings.Contains(r.URL.EscapedPath(), "%3A") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"status":400,"code":"ILLEGAL_ARGUMENT","message":"Syntax exception in path variables"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"AVAILABLE"}`))
		case r.URL.Path == "/rest/images":
			_, _ = w.Write([]byte(`{"value":{"uploadUrl":"` + uploadURL + `","image":"urn:li:image:1"}}`))
		case r.URL.Path == "/rest/posts":
			w.Header().Set("x-restli-id", "urn:li:share:9")
			w.WriteHeader(http.StatusCreated)
		}
	})

	p := onlyPublisher(t, linkedinConfig(srv.URL))
	res, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), Caption: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "urn:li:share:9" || res.Extra["mediaUrn"] != "urn:li:image:1" {
		t.Errorf("result = %+v", res)
	}
	for _, req := range rec.all() {
		if strings.HasPrefix(req.Path, "/rest/") {
			if req.Header.Get("LinkedIn-Version") == "" || req.Header.Get("X-Restli-Protocol-Version") != "2.0.0" {
				t.Errorf("%s is missing a mandatory header: %v", req.Path, req.Header)
			}
		}
	}
}

func TestLinkedInVideoFlowKeepsETagOrder(t *testing.T) {
	rec := newRecorder()
	parts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		// The ETag encodes which part it was, so the order can be asserted.
		w.Header().Set("ETag", `"etag`+strings.TrimPrefix(req.Path, "/part")+`"`)
		w.WriteHeader(http.StatusOK)
	})

	var finalizeBody string
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		switch {
		case r.URL.Path == "/rest/videos" && r.URL.Query().Get("action") == "initializeUpload":
			_, _ = w.Write([]byte(`{"value":{"video":"urn:li:video:1","uploadToken":"tk",
				"uploadInstructions":[
					{"uploadUrl":"` + parts.URL + `/part0","firstByte":0,"lastByte":3},
					{"uploadUrl":"` + parts.URL + `/part1","firstByte":4,"lastByte":7}]}}`))
		case r.URL.Path == "/rest/videos" && r.URL.Query().Get("action") == "finalizeUpload":
			finalizeBody = req.Body
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/rest/videos/"):
			// Encoded colons or Rest.li's 400, the same as the image poll.
			if !strings.Contains(r.URL.EscapedPath(), "%3A") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"status":400,"code":"ILLEGAL_ARGUMENT","message":"Syntax exception in path variables"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"AVAILABLE"}`))
		case r.URL.Path == "/rest/posts":
			w.Header().Set("x-restli-id", "urn:li:share:1")
			w.WriteHeader(http.StatusCreated)
		}
	})

	p := onlyPublisher(t, linkedinConfig(srv.URL))
	if _, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 8)}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(finalizeBody, `["etag0","etag1"]`) {
		t.Errorf("finalize body = %q, want the ETags in part order", finalizeBody)
	}
}

func TestLinkedInVideoNeedsETags(t *testing.T) {
	parts := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // no ETag
	})
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") == "initializeUpload" {
			_, _ = w.Write([]byte(`{"value":{"video":"urn:li:video:1","uploadToken":"tk",
				"uploadInstructions":[{"uploadUrl":"` + parts.URL + `/p","firstByte":0,"lastByte":7}]}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	p := onlyPublisher(t, linkedinConfig(srv.URL))
	_, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 8)})
	if err == nil || !strings.Contains(err.Error(), "ETag") {
		t.Fatalf("err = %v", err)
	}
}

func TestLinkedInValidatesTheAuthorURN(t *testing.T) {
	cfg := linkedinConfig("https://x")
	cfg.Publish.LinkedIn.AuthorURN = "abc"
	if _, err := Build(cfg, testDeps(t)); err == nil || !strings.Contains(err.Error(), "urn:li:") {
		t.Errorf("err = %v", err)
	}
}

// --- reddit ----------------------------------------------------------------

func redditConfig(api, auth string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.Reddit.Enabled = true
	cfg.Publish.Reddit.APIBaseURL = api
	cfg.Publish.Reddit.AuthBaseURL = auth
	cfg.Publish.Reddit.ClientID = "cid"
	cfg.Publish.Reddit.ClientSecret = "csec"
	cfg.Publish.Reddit.Username = "someone"
	cfg.Publish.Reddit.Password = "pw"
	cfg.Publish.Reddit.Subreddit = "test"
	cfg.Publish.Reddit.Title = "A post"
	cfg.Publish.Reddit.PollInterval = "1ms"
	cfg.Publish.Reddit.PollTimeout = "200ms"
	return &cfg
}

// fakeReddit is the whole three-host flow: token, lease, object store, submit.
func fakeReddit(t *testing.T, rec *recorder) (api, auth string, store *recorder) {
	t.Helper()
	store = newRecorder()
	s3 := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		store.record(r)
		w.WriteHeader(http.StatusCreated)
	})
	authSrv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"bearer","expires_in":3600}`))
	})
	apiSrv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		switch {
		case r.URL.Path == "/api/media/asset.json":
			_, _ = w.Write([]byte(`{"args":{"action":"` + s3.URL + `","fields":[` +
				`{"name":"acl","value":"public-read"},` +
				`{"name":"key","value":"media/abc.jpg"},` +
				`{"name":"policy","value":"p"}]},` +
				`"asset":{"asset_id":"asset1"}}`))
		case r.URL.Path == "/api/submit":
			_, _ = w.Write([]byte(`{"json":{"errors":[],"data":{}}}`))
		case strings.HasPrefix(r.URL.Path, "/user/"):
			_, _ = w.Write([]byte(`{"data":{"children":[{"data":{"id":"abc","name":"t3_abc",` +
				`"title":"A post","permalink":"/r/test/comments/abc/a_post/"}}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return apiSrv.URL, authSrv.URL, store
}

func TestRedditImagePost(t *testing.T) {
	rec := newRecorder()
	api, auth, store := fakeReddit(t, rec)

	p := onlyPublisher(t, redditConfig(api, auth))
	res, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), Caption: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.URL, "/r/test/comments/abc/") {
		t.Errorf("result = %+v", res)
	}
	if res.ID != "t3_abc" {
		t.Errorf("id = %q", res.ID)
	}

	paths := rec.paths()
	want := []string{"POST /api/v1/access_token", "POST /api/media/asset.json", "POST /api/submit"}
	for i, w := range want {
		if i >= len(paths) || paths[i] != w {
			t.Fatalf("paths = %v, want %v first", paths, want)
		}
	}

	// The store upload has the lease fields first and the file last.
	uploads := store.all()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d", len(uploads))
	}
	body := uploads[0].Body
	iKey := strings.Index(body, `name="key"`)
	iFile := strings.Index(body, `name="file"`)
	if iKey < 0 || iFile < 0 || iKey > iFile {
		t.Errorf("the file part must come last: key at %d, file at %d", iKey, iFile)
	}

	// The mandatory User-Agent is on every call.
	for _, req := range rec.all() {
		ua := req.Header.Get("User-Agent")
		if !strings.Contains(ua, "com.yohimik.crier") || !strings.Contains(ua, "/u/someone") {
			t.Errorf("%s carried User-Agent %q", req.Path, ua)
		}
	}

	// The submit call links the uploaded media.
	var submit recorded
	for _, req := range rec.all() {
		if req.Path == "/api/submit" {
			submit = req
		}
	}
	if !strings.Contains(submit.Body, "kind=image") {
		t.Errorf("submit body = %q", submit.Body)
	}
	if !strings.Contains(submit.Body, "media%2Fabc.jpg") {
		t.Errorf("the submit should point at the uploaded key: %q", submit.Body)
	}
	if !strings.Contains(submit.Body, "title=A+post") {
		t.Errorf("submit body = %q", submit.Body)
	}
}

func TestRedditVideoNeedsAPoster(t *testing.T) {
	rec := newRecorder()
	api, auth, _ := fakeReddit(t, rec)
	p := onlyPublisher(t, redditConfig(api, auth))
	_, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 10)})
	if err == nil || !strings.Contains(err.Error(), "poster") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedditVideoUploadsBoth(t *testing.T) {
	rec := newRecorder()
	api, auth, store := fakeReddit(t, rec)
	poster := imageArtifact(t)
	p := onlyPublisher(t, redditConfig(api, auth))
	if _, err := p.Publish(context.Background(), Input{
		Artifact: videoArtifact(t, 10), Poster: &poster,
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.all()) != 2 {
		t.Fatalf("uploads = %d, want the video and its poster", len(store.all()))
	}
	var submit recorded
	for _, req := range rec.all() {
		if req.Path == "/api/submit" {
			submit = req
		}
	}
	if !strings.Contains(submit.Body, "kind=video") || !strings.Contains(submit.Body, "video_poster_url=") {
		t.Errorf("submit body = %q", submit.Body)
	}
}

func TestRedditLinkMode(t *testing.T) {
	rec := newRecorder()
	api, auth, store := fakeReddit(t, rec)
	cfg := redditConfig(api, auth)
	cfg.Publish.Reddit.Kind = "link"
	p := onlyPublisher(t, cfg)
	if !p.Needs().URL {
		t.Fatal("link mode needs a staged URL")
	}
	if _, err := p.Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn/x.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.all()) != 0 {
		t.Error("link mode should upload nothing")
	}

	// And without a URL it says so.
	if _, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)}); err == nil {
		t.Error("expected an error with nothing staged")
	}
}

func TestRedditReportsSubmitErrors(t *testing.T) {
	rec := newRecorder()
	store := newRecorder()
	s3 := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		store.record(r)
		w.WriteHeader(http.StatusCreated)
	})
	auth := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"at"}`))
	})
	api := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.URL.Path == "/api/media/asset.json" {
			_, _ = w.Write([]byte(`{"args":{"action":"` + s3.URL +
				`","fields":[{"name":"key","value":"k"}]},"asset":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"json":{"errors":[["SUBREDDIT_NOEXIST","that subreddit does not exist","sr"]]}}`))
	})

	p := onlyPublisher(t, redditConfig(api.URL, auth.URL))
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "SUBREDDIT_NOEXIST") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedditTokenFailureIsExplained(t *testing.T) {
	auth := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	p := onlyPublisher(t, redditConfig("https://api.example", auth.URL))
	_, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "two-factor") {
		t.Fatalf("the error should explain the 2FA trap, got %v", err)
	}
}

func TestRedditRefreshTokenGrant(t *testing.T) {
	rec := newRecorder()
	auth := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		_, _ = w.Write([]byte(`{"access_token":"at"}`))
	})
	api := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/submit" {
			_, _ = w.Write([]byte(`{"json":{"errors":[],"data":{"url":"https://reddit/x","name":"t3_x"}}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	cfg := redditConfig(api.URL, auth.URL)
	cfg.Publish.Reddit.Kind = "link"
	cfg.Publish.Reddit.RefreshToken = "rt"
	cfg.Publish.Reddit.Password = ""
	p := onlyPublisher(t, cfg)
	if _, err := p.Publish(context.Background(), Input{Artifact: imageArtifact(t), URL: "https://x/y"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.all()[0].Body, "grant_type=refresh_token") {
		t.Errorf("token body = %q", rec.all()[0].Body)
	}
}

func TestRedditConfigChecks(t *testing.T) {
	base := func() *config.Config { return redditConfig("https://a", "https://b") }
	for _, tt := range []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"client id", func(c *config.Config) { c.Publish.Reddit.ClientID = "" }, "client-id"},
		{"client secret", func(c *config.Config) { c.Publish.Reddit.ClientSecret = "" }, "client-secret"},
		{"subreddit", func(c *config.Config) { c.Publish.Reddit.Subreddit = "" }, "subreddit"},
		{"username", func(c *config.Config) { c.Publish.Reddit.Username = "" }, "username"},
		{"password", func(c *config.Config) { c.Publish.Reddit.Password = "" }, "refresh-token"},
		{"kind", func(c *config.Config) { c.Publish.Reddit.Kind = "poll" }, "kind"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(cfg)
			_, err := Build(cfg, testDeps(t))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRedditActionURL(t *testing.T) {
	if got := redditActionURL("//reddit-uploaded-media.s3.example"); got != "https://reddit-uploaded-media.s3.example" {
		t.Errorf("got %q", got)
	}
	if got := redditActionURL("http://localhost:1234"); got != "http://localhost:1234" {
		t.Errorf("an explicit scheme should be left alone, got %q", got)
	}
}

func TestDefaultRedditUserAgent(t *testing.T) {
	if got := defaultRedditUserAgent("u/bob"); !strings.Contains(got, "/u/bob") {
		t.Errorf("got %q", got)
	}
	if got := defaultRedditUserAgent(""); !strings.Contains(got, "/u/unknown") {
		t.Errorf("got %q", got)
	}
}

// --- fan-out ---------------------------------------------------------------

type stubPublisher struct {
	name  string
	needs Needs
	fn    func(ctx context.Context, in Input) (Result, error)
	ping  func(ctx context.Context) (Identity, error)
}

func (s stubPublisher) Name() string { return s.name }
func (s stubPublisher) Needs() Needs { return s.needs }
func (s stubPublisher) Publish(ctx context.Context, in Input) (Result, error) {
	return s.fn(ctx, in)
}

func (s stubPublisher) Ping(ctx context.Context) (Identity, error) {
	if s.ping == nil {
		return Identity{ID: s.name}, nil
	}
	return s.ping(ctx)
}

// onePost is the ordinary case: one post carrying one file.
var onePost = []Input{{}}

func TestRunAllKeepsGoingAfterAFailure(t *testing.T) {
	jobs := []Job{
		{Posts: onePost, Publisher: stubPublisher{name: "b", fn: func(context.Context, Input) (Result, error) {
			return Result{}, errors.New("b is down")
		}}},
		{Posts: onePost, Publisher: stubPublisher{name: "a", fn: func(context.Context, Input) (Result, error) {
			return Result{ID: "1", URL: "https://a/1"}, nil
		}}},
		{Posts: onePost, Publisher: stubPublisher{name: "c", fn: func(context.Context, Input) (Result, error) {
			return Result{ID: "2"}, nil
		}}},
	}
	rep := RunAll(context.Background(), jobs, 2, testLogger(t))
	if rep.Succeeded() != 2 || rep.Failed() != 1 {
		t.Fatalf("succeeded=%d failed=%d", rep.Succeeded(), rep.Failed())
	}
	if rep.Outcomes[0].Platform != "a" || rep.Outcomes[1].Platform != "b" || rep.Outcomes[2].Platform != "c" {
		t.Errorf("outcomes are not in platform order: %+v", rep.Outcomes)
	}
	if err := rep.Err(); err == nil || !strings.Contains(err.Error(), "b: b is down") {
		t.Errorf("Err = %v", err)
	}
	if rep.Outcomes[0].Elapsed < 0 {
		t.Error("elapsed should be recorded")
	}
}

func TestRunAllSucceedsQuietly(t *testing.T) {
	rep := RunAll(context.Background(), []Job{
		{Posts: onePost, Publisher: stubPublisher{name: "a", fn: func(context.Context, Input) (Result, error) {
			return Result{ID: "1"}, nil
		}}},
	}, 0, testLogger(t))
	if rep.Failed() != 0 || rep.Err() != nil {
		t.Errorf("report = %+v", rep)
	}
}

func TestRunAllSurvivesAPanickingPublisher(t *testing.T) {
	rep := RunAll(context.Background(), []Job{
		{Posts: onePost, Publisher: stubPublisher{name: "boom", fn: func(context.Context, Input) (Result, error) {
			panic("kaboom")
		}}},
		{Posts: onePost, Publisher: stubPublisher{name: "ok", fn: func(context.Context, Input) (Result, error) {
			return Result{ID: "1"}, nil
		}}},
	}, 2, testLogger(t))
	if rep.Succeeded() != 1 || rep.Failed() != 1 {
		t.Fatalf("report = %+v", rep.Outcomes)
	}
	if !strings.Contains(rep.Outcomes[0].Error, "kaboom") {
		t.Errorf("outcome = %+v", rep.Outcomes[0])
	}
}

func TestRunAllRespectsConcurrency(t *testing.T) {
	var (
		running, peak int
		mu            = make(chan struct{}, 1)
	)
	mu <- struct{}{}
	step := func(delta int) {
		<-mu
		running += delta
		if running > peak {
			peak = running
		}
		mu <- struct{}{}
	}
	var jobs []Job
	for i := 0; i < 8; i++ {
		jobs = append(jobs, Job{Posts: onePost, Publisher: stubPublisher{
			name: string(rune('a' + i)),
			fn: func(context.Context, Input) (Result, error) {
				step(1)
				time.Sleep(5 * time.Millisecond)
				step(-1)
				return Result{}, nil
			},
		}})
	}
	RunAll(context.Background(), jobs, 2, testLogger(t))
	if peak > 2 {
		t.Errorf("peak concurrency = %d, want at most 2", peak)
	}
}

// --- helpers ---------------------------------------------------------------

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "x", "y"); got != "x" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestRequireNamesAllThreeLayers(t *testing.T) {
	err := require("", "publish.x.token")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"publish.x.token", "CRIER_PUBLISH_X_TOKEN", "--publish-x-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	if require("set", "k") != nil {
		t.Error("a set value is fine")
	}
}

func TestHumanSize(t *testing.T) {
	for _, tt := range []struct {
		n    int64
		want string
	}{{10, "10B"}, {2048, "2.0kB"}, {5 << 20, "5.0MB"}, {2 << 30, "2.0GB"}} {
		if got := humanSize(tt.n); got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestIdempotencyKeyIsStable(t *testing.T) {
	in := Input{Artifact: render.Artifact{Path: "/a.png", Size: 10}, Caption: "hi"}
	first, second := idempotencyKey(in), idempotencyKey(in)
	if first != second {
		t.Errorf("the same post gave %q then %q", first, second)
	}
	other := in
	other.Caption = "different"
	if idempotencyKey(in) == idempotencyKey(other) {
		t.Error("a different post should give a different key")
	}
}

// --- chunk math ------------------------------------------------------------

func TestSplitChunks(t *testing.T) {
	got := SplitChunks(10, 4)
	want := []Chunk{
		{Index: 0, Start: 0, End: 3, Size: 4},
		{Index: 1, Start: 4, End: 7, Size: 4},
		{Index: 2, Start: 8, End: 9, Size: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chunk %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	if got := SplitChunks(8, 4); len(got) != 2 || got[1].End != 7 {
		t.Errorf("an exact multiple = %+v", got)
	}
	if SplitChunks(0, 4) != nil || SplitChunks(10, 0) != nil {
		t.Error("degenerate inputs give no chunks")
	}
	if got := SplitChunks(3, 10); len(got) != 1 || got[0].Size != 3 {
		t.Errorf("a file smaller than one chunk = %+v", got)
	}
}

func TestChunkContentRange(t *testing.T) {
	c := Chunk{Start: 0, End: 5242879}
	if got := c.ContentRange(10485760); got != "bytes 0-5242879/10485760" {
		t.Errorf("got %q", got)
	}
}

func TestTikTokChunks(t *testing.T) {
	// A small file goes as one chunk covering the whole thing.
	size, chunks := TikTokChunks(1000)
	if size != 1000 || len(chunks) != 1 || chunks[0].Size != 1000 {
		t.Fatalf("small file: size=%d chunks=%+v", size, chunks)
	}

	// A file that is an exact multiple of the minimum splits evenly.
	const min = 5 << 20
	size, chunks = TikTokChunks(3 * min)
	if size != min || len(chunks) != 3 {
		t.Fatalf("exact multiple: size=%d chunks=%d", size, len(chunks))
	}
	if chunks[2].End != 3*min-1 {
		t.Errorf("last chunk ends at %d", chunks[2].End)
	}

	// A remainder rides along with the last chunk rather than becoming a short
	// one TikTok would refuse.
	_, chunks = TikTokChunks(2*min + 1000)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d", len(chunks))
	}
	if chunks[1].Size != min+1000 {
		t.Errorf("last chunk is %d bytes, want the remainder folded in", chunks[1].Size)
	}
	if chunks[len(chunks)-1].End != 2*min+999 {
		t.Errorf("last chunk ends at %d", chunks[len(chunks)-1].End)
	}
	var covered int64
	for _, c := range chunks {
		covered += c.Size
	}
	if covered != 2*min+1000 {
		t.Errorf("the chunks cover %d bytes, want the whole file", covered)
	}

	if _, chunks := TikTokChunks(0); chunks != nil {
		t.Error("an empty file has no chunks")
	}
}

func TestReadChunk(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readChunk(p, Chunk{Start: 3, End: 6, Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "3456" {
		t.Errorf("got %q", got)
	}
	if _, err := readChunk(filepath.Join(dir, "nope"), Chunk{Size: 1}); err == nil {
		t.Error("expected a read error")
	}
}
