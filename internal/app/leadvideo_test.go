package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/publish"
	"github.com/yohimik/crier/internal/render"
	"github.com/yohimik/crier/internal/stage"
)

// anthemBytes begins like an MP4 and says nothing else. crier reads the first
// twelve bytes and stops.
const anthemBytes = "\x00\x00\x00\x20ftypisomCRIER"

// leadStager records what it was asked to stage, counts the removals, and can
// be made to refuse. countingStager next door does the first of those and
// nothing else.
type leadStager struct {
	staged  []string
	removed int
	err     error
}

func (s *leadStager) Name() string { return "lead" }

func (s *leadStager) Stage(_ context.Context, a stage.Asset) (*stage.Object, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.staged = append(s.staged, a.Name)
	return &stage.Object{
		URL: fmt.Sprintf("https://staged.example/%d/%s", len(s.staged), a.Name),
		Remove: func(context.Context) error {
			s.removed++
			return nil
		},
	}, nil
}

func (s *leadStager) Close(context.Context) error { return nil }

// leadPipeline is a pipeline with nothing but the configuration, which is all
// StageLeadVideos reads.
func leadPipeline(t *testing.T, cfg *config.Config) *Pipeline {
	t.Helper()
	return &Pipeline{cfg: cfg, log: zerolog.New(zerolog.NewTestWriter(t)), dir: t.TempDir()}
}

// leadPublishers builds the publishers for a configuration, which is what
// StageLeadVideos is handed.
func leadPublishers(t *testing.T, cfg *config.Config) []publish.Publisher {
	t.Helper()
	// A client is required to build a publisher, and none of these tests makes
	// a request: the hosts above are names nothing listens on.
	ps, err := publish.Build(cfg, publish.Deps{
		Client: httpx.New(httpx.Options{Logger: zerolog.Nop()}),
		Logger: zerolog.New(zerolog.NewTestWriter(t)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

// leadConfig enables instagram and telegram with a clip each, pointing both at
// hosts nothing listens on: nothing here makes a request.
func leadConfig(t *testing.T, instagramClip, telegramClip string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Publish.Instagram.Enabled = true
	cfg.Publish.Instagram.APIBaseURL = "https://ig.example"
	cfg.Publish.Instagram.Token = "t"
	cfg.Publish.Instagram.UserID = "u"
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Telegram.APIBaseURL = "https://tg.example"
	cfg.Publish.Telegram.Token = "123:abc"
	cfg.Publish.Telegram.ChatID = "@c"
	cfg.Publish.Instagram.LeadVideo.File = instagramClip
	cfg.Publish.Telegram.LeadVideo.File = telegramClip
	return &cfg
}

// TestStageLeadVideosOnlyForThePlatformsThatFetch: Telegram takes the bytes,
// so staging its clip would be an upload nobody reads.
func TestStageLeadVideosOnlyForThePlatformsThatFetch(t *testing.T) {
	dir := t.TempDir()
	ig := filepath.Join(dir, "ig.mp4")
	tg := filepath.Join(dir, "tg.mp4")
	write(t, dir, "ig.mp4", anthemBytes)
	write(t, dir, "tg.mp4", anthemBytes)

	cfg := leadConfig(t, ig, tg)
	p := leadPipeline(t, cfg)
	st := &leadStager{}
	var arts Artifacts

	if err := p.StageLeadVideos(context.Background(), st, &arts, leadPublishers(t, cfg)); err != nil {
		t.Fatal(err)
	}
	if len(st.staged) != 1 || st.staged[0] != "ig.mp4" {
		t.Fatalf("staged %v, want instagram's clip alone", st.staged)
	}
	if arts.LeadVideoURLs["instagram"] == "" {
		t.Error("instagram got no URL")
	}
	if _, ok := arts.LeadVideoURLs["telegram"]; ok {
		t.Error("telegram was given a URL it does not need")
	}
	// The staged object is cleaned up with everything else the run acquired.
	p.Cleanup(context.Background())
	if st.removed != 1 {
		t.Errorf("removed %d staged objects, want 1", st.removed)
	}
}

// TestStageLeadVideosDoesNothingWithoutOne is the ordinary run: no clip, no
// staging, no map.
func TestStageLeadVideosDoesNothingWithoutOne(t *testing.T) {
	cfg := leadConfig(t, "", "")
	p := leadPipeline(t, cfg)
	st := &leadStager{}
	var arts Artifacts

	if err := p.StageLeadVideos(context.Background(), st, &arts, leadPublishers(t, cfg)); err != nil {
		t.Fatal(err)
	}
	if len(st.staged) != 0 || arts.LeadVideoURLs != nil {
		t.Errorf("staged %v and got %v", st.staged, arts.LeadVideoURLs)
	}
}

// TestStageLeadVideosReportsAStagingFailure: a clip that cannot be staged is a
// staging error, not a publish one, and it stops the run before the post.
func TestStageLeadVideosReportsAStagingFailure(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ig.mp4", anthemBytes)
	cfg := leadConfig(t, filepath.Join(dir, "ig.mp4"), "")
	p := leadPipeline(t, cfg)
	st := &leadStager{err: errors.New("the bucket said no")}

	var arts Artifacts
	err := p.StageLeadVideos(context.Background(), st, &arts, leadPublishers(t, cfg))
	if err == nil {
		t.Fatal("expected a staging error")
	}
	if !strings.Contains(err.Error(), "instagram lead video") ||
		!strings.Contains(err.Error(), "the bucket said no") {
		t.Errorf("err = %v", err)
	}
	if code := codeOf(err); code != ExitStaging {
		t.Errorf("exit code = %d, want the staging one", code)
	}
}

// TestStageLeadVideosRefusesAFileThatIsNotAnMP4 covers the second guard: the
// publisher checked it when it was built, and staging checks it again because
// the file is read twice and could have changed between them.
func TestStageLeadVideosRefusesAFileThatIsNotAnMP4(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ig.mp4", anthemBytes)
	cfg := leadConfig(t, filepath.Join(dir, "ig.mp4"), "")
	publishers := leadPublishers(t, cfg)

	// Swapped after the publishers were built, which is the only way this
	// branch is reachable.
	write(t, dir, "ig.mp4", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

	p := leadPipeline(t, cfg)
	var arts Artifacts
	err := p.StageLeadVideos(context.Background(), &leadStager{}, &arts, publishers)
	if err == nil || !strings.Contains(err.Error(), "does not begin like an MP4") {
		t.Fatalf("err = %v", err)
	}
	if code := codeOf(err); code != ExitConfig {
		t.Errorf("exit code = %d, want the config one", code)
	}
}

// TestPostsForGivesEveryPostTheLeadVideo: a reader meets each post on its own,
// and one that began with a page out of the middle would be the only one
// without an opening.
func TestPostsForGivesEveryPostTheLeadVideo(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ig.mp4", anthemBytes)
	cfg := leadConfig(t, filepath.Join(dir, "ig.mp4"), "")
	// Two pages per post, so a five-page run becomes three posts.
	cfg.Publish.Instagram.Layout.MaxAttachments = 2

	var pub publish.Publisher
	for _, p := range leadPublishers(t, cfg) {
		if p.Name() == "instagram" {
			pub = p
		}
	}
	if pub == nil {
		t.Fatal("no instagram publisher")
	}

	arts := Artifacts{LeadVideoURLs: map[string]string{"instagram": "https://staged/anthem.mp4"}}
	for i := 0; i < 5; i++ {
		arts.Pages = append(arts.Pages, Page{
			Images: map[config.Format]render.Artifact{
				config.JPEG: {Kind: render.KindImage, Format: config.JPEG,
					Path: filepath.Join(dir, "p.jpg"), ContentType: "image/jpeg"},
			},
			URL: fmt.Sprintf("https://staged/%d.jpg", i+1),
		})
	}

	posts, err := PostsFor(nil, cfg, pub, arts, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 3 {
		t.Fatalf("made %d posts, want three", len(posts))
	}
	for i, in := range posts {
		if in.LeadVideoURL != "https://staged/anthem.mp4" {
			t.Errorf("post %d opens with %q", i+1, in.LeadVideoURL)
		}
	}
}

// --- ping ------------------------------------------------------------------

// TestPingChecksTheLeadVideo: the clip gets a row saying what it is and which
// post it opens.
func TestPingChecksTheLeadVideo(t *testing.T) {
	dir := project(t, strings.Join(append(append([]string(nil), telegramBlock...),
		"    lead-video: anthem.mp4"), "\n"))
	write(t, dir, "anthem.mp4", anthemBytes)

	_, stdout, _ := run(t, dir, []string{}, "ping")
	if !strings.Contains(stdout, "lead-video:telegram") {
		t.Fatalf("no lead video row:\n%s", stdout)
	}
	for _, want := range []string{"anthem.mp4", "mp4", "opens the telegram post"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s", want, stdout)
		}
	}
}

// TestPingReportsALeadVideoThatIsNotAnMP4: building a publisher refuses it, so
// the row has to come first or it would never be printed at all.
func TestPingReportsALeadVideoThatIsNotAnMP4(t *testing.T) {
	dir := project(t, strings.Join(append(append([]string(nil), telegramBlock...),
		"    lead-video: anthem.mp4"), "\n"))
	write(t, dir, "anthem.mp4", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

	code, stdout, _ := run(t, dir, []string{}, "ping")
	if code != ExitConfig {
		t.Fatalf("code = %d, want a config error", code)
	}
	if !strings.Contains(stdout, "does not begin like an MP4") {
		t.Fatalf("stdout:\n%s", stdout)
	}
}

// TestPingSaysWhenTheLeadVideoPlatformIsOff: the file is fine and nothing will
// post it, which is the finding worth a note rather than a failure.
func TestPingSaysWhenTheLeadVideoPlatformIsOff(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"http:",
		"  retry-max: 0",
		"  timeout: 2s",
		"publish:",
		"  instagram:",
		"    lead-video: anthem.mp4",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: http://127.0.0.1:1",
		"    token: t",
		"    chat-id: c",
	}, "\n"))
	write(t, dir, "anthem.mp4", anthemBytes)

	_, stdout, _ := run(t, dir, []string{}, "ping")
	if !strings.Contains(stdout, "instagram is not enabled") {
		t.Fatalf("stdout:\n%s", stdout)
	}
}

// TestLeadVideoOnAPlatformThatCannotCarryItIsRefused is the validation error
// as an operator meets it.
func TestLeadVideoOnAPlatformThatCannotCarryItIsRefused(t *testing.T) {
	dir := project(t, strings.Join([]string{
		"publish:",
		"  dry-run: true",
		"  discord:",
		"    enabled: true",
		"    webhook-url: https://discord.example/webhook",
		"    lead-video: anthem.mp4",
	}, "\n"))
	write(t, dir, "anthem.mp4", anthemBytes)

	code, _, stderr := run(t, dir, []string{}, "publish")
	if code != ExitConfig {
		t.Fatalf("code = %d, want a config error; stderr = %s", code, stderr)
	}
	for _, want := range []string{"publish.discord.lead-video", "instagram and telegram"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
}
