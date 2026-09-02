package publish

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// threadsUser is the account every fake in this file posts as.
const threadsUser = "th-user"

func threadsConfig(api string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.Threads.Enabled = true
	cfg.Publish.Threads.APIBaseURL = api
	cfg.Publish.Threads.Token = "threads-test-token"
	cfg.Publish.Threads.UserID = threadsUser
	cfg.Publish.Threads.PollInterval = "1ms"
	cfg.Publish.Threads.PollTimeout = "2s"
	return &cfg
}

// fakeThreads is the API as the publisher has to speak it.
//
// It enforces the linkage rather than only answering, because that is the part
// that would break silently: threads_publish has to name a container this fake
// created, a CAROUSEL parent has to name children that were themselves created
// as carousel items, and a container's id is minted here rather than guessed —
// so a post assembled out of the wrong pieces shows up as a refusal instead of
// as a passing test.
type fakeThreads struct {
	t *testing.T

	mu         sync.Mutex
	containers map[string]url.Values
	posts      int
}

func newFakeThreads(t *testing.T, rec *recorder) (api string, f *fakeThreads) {
	t.Helper()
	f = &fakeThreads{t: t, containers: map[string]url.Values{}}
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		f.serve(w, r, req)
	})
	return srv.URL, f
}

func (f *fakeThreads) serve(w http.ResponseWriter, r *http.Request, req recorded) {
	switch {
	case req.Path == "/"+threadsUser+"/threads":
		f.create(w, req)
	case req.Path == "/"+threadsUser+"/threads_publish":
		f.publish(w, req)
	case strings.HasPrefix(req.Path, "/th-c"):
		// The status poll. A container this fake did not create has no status.
		f.mu.Lock()
		_, ok := f.containers[strings.TrimPrefix(req.Path, "/")]
		f.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":{"message":"no such container","code":24,"error_subcode":2207006}}`,
				http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"status":"FINISHED"}`))
	case strings.HasPrefix(req.Path, "/th-post-"):
		fmt.Fprintf(w, `{"permalink":"https://www.threads.net/@crier/post/%s"}`,
			strings.TrimPrefix(req.Path, "/th-post-"))
	case req.Path == "/me":
		_, _ = w.Write([]byte(`{"id":"` + threadsUser + `","username":"crier"}`))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeThreads) create(w http.ResponseWriter, req recorded) {
	form, err := url.ParseQuery(req.Body)
	if err != nil {
		f.t.Errorf("the container body is not a form: %q", req.Body)
	}
	if form.Get("access_token") == "" {
		f.refuse(w, "no access token")
		return
	}
	switch form.Get("media_type") {
	case "IMAGE":
		if form.Get("image_url") == "" {
			f.refuse(w, "an IMAGE container with no image_url")
			return
		}
	case "VIDEO":
		if form.Get("video_url") == "" {
			f.refuse(w, "a VIDEO container with no video_url")
			return
		}
	case "CAROUSEL":
		children := strings.Split(form.Get("children"), ",")
		if len(children) < ThreadsCarouselMin {
			f.refuse(w, fmt.Sprintf("a carousel of %d children; threads takes at least %d",
				len(children), ThreadsCarouselMin))
			return
		}
		f.mu.Lock()
		for _, id := range children {
			child, ok := f.containers[id]
			if !ok {
				f.mu.Unlock()
				f.refuse(w, "the carousel names container "+id+", which was never created")
				return
			}
			if child.Get("is_carousel_item") != "true" {
				f.mu.Unlock()
				f.refuse(w, "the carousel names container "+id+", which is not a carousel item")
				return
			}
		}
		f.mu.Unlock()
	default:
		f.refuse(w, "media_type "+form.Get("media_type"))
		return
	}

	f.mu.Lock()
	id := fmt.Sprintf("th-c%d", len(f.containers)+1)
	f.containers[id] = form
	f.mu.Unlock()
	fmt.Fprintf(w, `{"id":%q}`, id)
}

func (f *fakeThreads) publish(w http.ResponseWriter, req recorded) {
	form, _ := url.ParseQuery(req.Body)
	f.mu.Lock()
	container, ok := f.containers[form.Get("creation_id")]
	f.mu.Unlock()
	if !ok {
		f.refuse(w, "creation_id "+form.Get("creation_id")+" names no container")
		return
	}
	if container.Get("is_carousel_item") == "true" {
		f.refuse(w, "a carousel child cannot be published on its own")
		return
	}
	f.mu.Lock()
	f.posts++
	n := f.posts
	f.mu.Unlock()
	fmt.Fprintf(w, `{"id":"th-post-%d"}`, n)
}

func (f *fakeThreads) refuse(w http.ResponseWriter, why string) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `{"error":{"message":%q,"code":100}}`, why)
}

// threadsContainers is every recorded container creation, parsed, in the order
// they were made.
func threadsContainers(t *testing.T, rec *recorder) []url.Values {
	t.Helper()
	var out []url.Values
	for _, req := range rec.all() {
		if req.Method != http.MethodPost || !strings.HasSuffix(req.Path, "/threads") {
			continue
		}
		form, err := url.ParseQuery(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, form)
	}
	return out
}

// TestThreadsPostsASingleImage is the whole of an ordinary post: the container,
// the poll, the publish that names it, and the permalink lookup.
func TestThreadsPostsASingleImage(t *testing.T) {
	rec := newRecorder()
	api, _ := newFakeThreads(t, rec)

	res, err := onlyPublisher(t, threadsConfig(api)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
		Caption: "a card from crier",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "th-post-1" {
		t.Errorf("id = %q", res.ID)
	}
	if res.URL != "https://www.threads.net/@crier/post/1" {
		t.Errorf("url = %q, want the permalink threads named", res.URL)
	}
	if res.Extra["containerId"] != "th-c1" {
		t.Errorf("extra = %v, want the container id", res.Extra)
	}

	containers := threadsContainers(t, rec)
	if len(containers) != 1 {
		t.Fatalf("created %d containers, want one", len(containers))
	}
	c := containers[0]
	if c.Get("media_type") != "IMAGE" {
		t.Errorf("media_type = %q, want IMAGE", c.Get("media_type"))
	}
	if c.Get("image_url") != "https://cdn.example/card.jpg" {
		t.Errorf("image_url = %q", c.Get("image_url"))
	}
	if c.Get("text") != "a card from crier" {
		t.Errorf("text = %q, want the caption", c.Get("text"))
	}
	if c.Get("access_token") != "threads-test-token" {
		t.Errorf("the token did not go out: %v", c)
	}
	if _, ok := c["is_carousel_item"]; ok {
		t.Errorf("a single post is not a carousel item: %v", c)
	}

	// The publish names the container that was polled, which is the linkage the
	// fake refuses to fake.
	pub, ok := findRequest(rec, "/threads_publish")
	if !ok {
		t.Fatal("nothing was published")
	}
	form, err := url.ParseQuery(pub.Body)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("creation_id") != "th-c1" {
		t.Errorf("creation_id = %q", form.Get("creation_id"))
	}

	// The status poll asked for the field the failure reason arrives in.
	status, ok := findRequest(rec, "/th-c1")
	if !ok {
		t.Fatal("the container was never polled")
	}
	if !strings.Contains(status.Query, "error_message") {
		t.Errorf("the poll query = %q, want status and error_message", status.Query)
	}
}

// TestThreadsPostsAVideo: a clip is a different media_type and a differently
// named URL parameter, and sending the image pair for one is a container that
// never finishes.
func TestThreadsPostsAVideo(t *testing.T) {
	rec := newRecorder()
	api, _ := newFakeThreads(t, rec)

	if _, err := onlyPublisher(t, threadsConfig(api)).Publish(context.Background(), Input{
		Artifact: videoArtifact(t, 64), URL: "https://cdn.example/clip.mp4", Caption: "the clip",
	}); err != nil {
		t.Fatal(err)
	}
	c := threadsContainers(t, rec)[0]
	if c.Get("media_type") != "VIDEO" || c.Get("video_url") != "https://cdn.example/clip.mp4" {
		t.Errorf("container = %v, want a VIDEO with a video_url", c)
	}
	if c.Get("image_url") != "" {
		t.Errorf("a video container carried an image_url: %v", c)
	}
}

// TestThreadsPagesBecomeOneCarousel: three pages are one post of three items,
// in page order, with the text on the parent and nowhere else.
func TestThreadsPagesBecomeOneCarousel(t *testing.T) {
	rec := newRecorder()
	api, _ := newFakeThreads(t, rec)

	arts := []render.Artifact{imageArtifact(t), imageArtifact(t), imageArtifact(t)}
	urls := []string{
		"https://cdn.example/card-p01.jpg",
		"https://cdn.example/card-p02.jpg",
		"https://cdn.example/card-p03.jpg",
	}
	res, err := onlyPublisher(t, threadsConfig(api)).Publish(context.Background(), Input{
		Artifact: arts[0], Artifacts: arts, URL: urls[0], URLs: urls, Caption: "three pages",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := countPaths(rec, "/threads_publish"); got != 1 {
		t.Errorf("published %d times, want one carousel", got)
	}

	containers := threadsContainers(t, rec)
	if len(containers) != 4 {
		t.Fatalf("created %d containers, want three children and a parent", len(containers))
	}
	for i, c := range containers[:3] {
		if c.Get("is_carousel_item") != "true" {
			t.Errorf("child %d is not a carousel item: %v", i+1, c)
		}
		if c.Get("media_type") != "IMAGE" || c.Get("image_url") != urls[i] {
			t.Errorf("child %d = %v, want page %d", i+1, c, i+1)
		}
		if _, ok := c["text"]; ok {
			t.Errorf("child %d carried text, which threads does not accept: %v", i+1, c)
		}
	}

	parent := containers[3]
	if parent.Get("media_type") != "CAROUSEL" {
		t.Errorf("the parent is not a carousel: %v", parent)
	}
	if _, ok := parent["is_carousel_item"]; ok {
		t.Errorf("the parent is marked a carousel item: %v", parent)
	}
	if parent.Get("children") != "th-c1,th-c2,th-c3" {
		t.Errorf("children = %q, want the child containers in page order", parent.Get("children"))
	}
	if parent.Get("text") != "three pages" {
		t.Errorf("the caption belongs to the parent: %v", parent)
	}
	if res.Extra["children"] != "th-c1,th-c2,th-c3" {
		t.Errorf("extra = %v", res.Extra)
	}
}

// TestThreadsCarouselTakesAVideoChild: a mixed carousel names each child's own
// kind, so a clip among the pages is a VIDEO child rather than a broken IMAGE
// one.
func TestThreadsCarouselTakesAVideoChild(t *testing.T) {
	rec := newRecorder()
	api, _ := newFakeThreads(t, rec)

	arts := []render.Artifact{videoArtifact(t, 32), imageArtifact(t)}
	urls := []string{"https://cdn.example/clip.mp4", "https://cdn.example/card-p01.jpg"}
	if _, err := onlyPublisher(t, threadsConfig(api)).Publish(context.Background(), Input{
		Artifact: arts[0], Artifacts: arts, URL: urls[0], URLs: urls,
	}); err != nil {
		t.Fatal(err)
	}
	containers := threadsContainers(t, rec)
	if containers[0].Get("media_type") != "VIDEO" || containers[0].Get("video_url") != urls[0] {
		t.Errorf("the first child = %v, want a VIDEO", containers[0])
	}
	if containers[1].Get("media_type") != "IMAGE" || containers[1].Get("image_url") != urls[1] {
		t.Errorf("the second child = %v, want an IMAGE", containers[1])
	}
}

// TestThreadsSinglePageIsNeverACarousel is the rule Instagram does not share:
// a CAROUSEL container naming one child is refused, so a one-page run has to
// go out as plain media. The fake refuses it too, so this is a real proof
// rather than an assertion about a string.
func TestThreadsSinglePageIsNeverACarousel(t *testing.T) {
	rec := newRecorder()
	api, _ := newFakeThreads(t, rec)

	arts := []render.Artifact{imageArtifact(t)}
	if _, err := onlyPublisher(t, threadsConfig(api)).Publish(context.Background(), Input{
		Artifact: arts[0], Artifacts: arts,
		URL:  "https://cdn.example/only.jpg",
		URLs: []string{"https://cdn.example/only.jpg"},
	}); err != nil {
		t.Fatal(err)
	}
	containers := threadsContainers(t, rec)
	if len(containers) != 1 {
		t.Fatalf("created %d containers for one page, want one", len(containers))
	}
	c := containers[0]
	if c.Get("media_type") != "IMAGE" {
		t.Errorf("media_type = %q, want IMAGE and never CAROUSEL", c.Get("media_type"))
	}
	if _, ok := c["children"]; ok {
		t.Errorf("a single page listed children: %v", c)
	}
	if _, ok := c["is_carousel_item"]; ok {
		t.Errorf("a single page was marked a carousel item: %v", c)
	}
}

// TestThreadsRefusesMoreThanACarouselHolds is the backstop under the cap the
// pipeline paginates against.
func TestThreadsRefusesMoreThanACarouselHolds(t *testing.T) {
	arts := make([]render.Artifact, ThreadsAttachmentMax+1)
	urls := make([]string, len(arts))
	for i := range arts {
		arts[i] = imageArtifact(t)
		urls[i] = fmt.Sprintf("https://cdn.example/p%d.jpg", i+1)
	}
	_, err := onlyPublisher(t, threadsConfig("https://threads.example")).
		Publish(context.Background(), Input{Artifact: arts[0], Artifacts: arts, URLs: urls})
	if err == nil || !strings.Contains(err.Error(), "holds 20 items") {
		t.Errorf("err = %v", err)
	}
}

// TestThreadsNeedsAPublicURL: Threads fetches the media itself, so a run with
// no staging has nothing to hand it.
func TestThreadsNeedsAPublicURL(t *testing.T) {
	_, err := onlyPublisher(t, threadsConfig("https://threads.example")).
		Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "stage.mode") {
		t.Errorf("err = %v, want it to name the key that fixes it", err)
	}
}

// TestThreadsSurfacesTheContainerError: the reason lives in error_message, and
// "could not process the media" without it is not something to act on.
func TestThreadsSurfacesTheContainerError(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/threads") {
			_, _ = w.Write([]byte(`{"id":"th-c1"}`))
			return
		}
		_, _ = w.Write([]byte(
			`{"status":"ERROR","error_message":"The media could not be fetched from the URL"}`))
	})
	_, err := onlyPublisher(t, threadsConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "http://localhost:9/card.jpg",
	})
	if err == nil {
		t.Fatal("a container in ERROR is a failure")
	}
	if !strings.Contains(err.Error(), "could not be fetched") {
		t.Errorf("the reason should reach the operator: %v", err)
	}
}

// TestThreadsSurfacesAnExpiredContainer, which is the other terminal state.
func TestThreadsSurfacesAnExpiredContainer(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/threads") {
			_, _ = w.Write([]byte(`{"id":"th-c1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"EXPIRED"}`))
	})
	_, err := onlyPublisher(t, threadsConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
	})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("err = %v", err)
	}
}

// TestThreadsGivesUpOnAContainerThatNeverFinishes: the poll is bounded, and a
// run that hung here would hold the whole fan-out open.
func TestThreadsGivesUpOnAContainerThatNeverFinishes(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/threads") {
			_, _ = w.Write([]byte(`{"id":"th-c1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"IN_PROGRESS"}`))
	})
	cfg := threadsConfig(srv.URL)
	cfg.Publish.Threads.PollTimeout = "20ms"

	_, err := onlyPublisher(t, cfg).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
	})
	if err == nil || !strings.Contains(err.Error(), "waiting for the media container") {
		t.Errorf("err = %v", err)
	}
}

// TestThreadsPublishesAContainerThatIsAlreadyAPost: PUBLISHED is not a state
// this poll expects, and waiting out the budget for one that will never change
// would turn a success into a timeout.
func TestThreadsAcceptsAPublishedContainer(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/threads_publish"):
			_, _ = w.Write([]byte(`{"id":"th-post-1"}`))
		case strings.HasSuffix(r.URL.Path, "/threads"):
			_, _ = w.Write([]byte(`{"id":"th-c1"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"PUBLISHED"}`))
		}
	})
	res, err := onlyPublisher(t, threadsConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "th-post-1" {
		t.Errorf("result = %+v", res)
	}
}

// TestThreadsReportsAContainerWithNoID: an answer with no id is a step that
// cannot be linked to the next one.
func TestThreadsReportsAContainerWithNoID(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	_, err := onlyPublisher(t, threadsConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
	})
	if err == nil || !strings.Contains(err.Error(), "no container id") {
		t.Errorf("err = %v", err)
	}
}

// TestThreadsReportsAPublishWithNoID: the same at the other end. A publish that
// named nothing leaves crier reporting a post it cannot link to.
func TestThreadsReportsAPublishWithNoID(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/threads_publish"):
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/threads"):
			_, _ = w.Write([]byte(`{"id":"th-c1"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"FINISHED"}`))
		}
	})
	_, err := onlyPublisher(t, threadsConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
	})
	if err == nil || !strings.Contains(err.Error(), "named no post id") {
		t.Errorf("err = %v", err)
	}
}

// TestThreadsNamesWhichCarouselItemFailed: a five-page post that stops on item
// four should say four, because "the post failed" leaves nothing to act on.
func TestThreadsNamesWhichCarouselItemFailed(t *testing.T) {
	created := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/threads") {
			created++
			if created == 2 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"nope","code":100}}`))
				return
			}
			fmt.Fprintf(w, `{"id":"th-c%d"}`, created)
			return
		}
		_, _ = w.Write([]byte(`{"status":"FINISHED"}`))
	})
	arts := []render.Artifact{imageArtifact(t), imageArtifact(t), imageArtifact(t)}
	urls := []string{"https://cdn.example/1.jpg", "https://cdn.example/2.jpg", "https://cdn.example/3.jpg"}
	_, err := onlyPublisher(t, threadsConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: arts[0], Artifacts: arts, URLs: urls,
	})
	if err == nil || !strings.Contains(err.Error(), "carousel item 2 of 3") {
		t.Errorf("err = %v", err)
	}
}

// TestThreadsReportsAFailedCarouselParent: the children were made and the
// parent that would have joined them was not, so the error says which step
// stopped rather than naming a page.
func TestThreadsReportsAFailedCarouselParent(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/threads") {
			_, _ = w.Write([]byte(`{"status":"FINISHED"}`))
			return
		}
		form, _ := url.ParseQuery(readBody(t, r))
		if form.Get("media_type") == "CAROUSEL" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"nope","code":100}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"th-c1"}`))
	})
	arts := []render.Artifact{imageArtifact(t), imageArtifact(t)}
	urls := []string{"https://cdn.example/1.jpg", "https://cdn.example/2.jpg"}
	_, err := onlyPublisher(t, threadsConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: arts[0], Artifacts: arts, URLs: urls,
	})
	if err == nil || !strings.Contains(err.Error(), "creating the carousel container") {
		t.Errorf("err = %v", err)
	}
}

// readBody is a handler-side read of a request body, for a fake that has to
// look at what it was sent before answering.
func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestThreadsPollsOnDefaultsWhenNoneAreConfigured: the two keys have defaults,
// and a configuration that cleared them still has to poll rather than give up
// on a zero budget.
func TestThreadsPollsOnDefaultsWhenNoneAreConfigured(t *testing.T) {
	rec := newRecorder()
	api, _ := newFakeThreads(t, rec)
	cfg := threadsConfig(api)
	cfg.Publish.Threads.PollInterval = ""
	cfg.Publish.Threads.PollTimeout = ""

	res, err := onlyPublisher(t, cfg).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "th-post-1" {
		t.Errorf("result = %+v", res)
	}
}

// --- the not-ready publish ---------------------------------------------------

// threadsPublishScript is a fake whose threads_publish answers with the given
// bodies in order, and with a success once the list runs out.
func threadsPublishScript(t *testing.T, refusals []string) (api string, attempts *int) {
	t.Helper()
	n := 0
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/threads_publish"):
			if n < len(refusals) {
				body := refusals[n]
				n++
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(body))
				return
			}
			n++
			_, _ = w.Write([]byte(`{"id":"th-post-1"}`))
		case strings.HasSuffix(r.URL.Path, "/threads"):
			_, _ = w.Write([]byte(`{"id":"th-c1"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"FINISHED"}`))
		}
	})
	return srv.URL, &n
}

const (
	threadsNotReady = `{"error":{"message":"Media ID is not available","type":"OAuthException",` +
		`"code":9007,"error_subcode":2207027,"is_transient":false}}`
	threadsVanished = `{"error":{"message":"The requested resource does not exist",` +
		`"type":"OAuthException","code":24,"error_subcode":2207006,"is_transient":false}}`
)

// TestThreadsRetriesTheNotReadyPublish: the container polled FINISHED while
// Meta was still making the media publishable. The refusal proves no post was
// created, which is the whole of what makes asking again safe.
func TestThreadsRetriesTheNotReadyPublish(t *testing.T) {
	for name, refusals := range map[string][]string{
		"media id is not available": {threadsNotReady, threadsNotReady},
		"the container vanished":    {threadsVanished},
	} {
		t.Run(name, func(t *testing.T) {
			api, attempts := threadsPublishScript(t, refusals)
			res, err := onlyPublisher(t, threadsConfig(api)).Publish(context.Background(), Input{
				Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.ID != "th-post-1" {
				t.Errorf("result = %+v", res)
			}
			if *attempts != len(refusals)+1 {
				t.Errorf("threads_publish was attempted %d times, want %d",
					*attempts, len(refusals)+1)
			}
		})
	}
}

// TestThreadsDoesNotRetryOtherPublishFailures: everything that is not the
// not-ready refusal keeps the never-repeat rule, because the publish may have
// happened and a second attempt would be a second post.
func TestThreadsDoesNotRetryOtherPublishFailures(t *testing.T) {
	api, attempts := threadsPublishScript(t, []string{
		`{"error":{"message":"something else","code":100}}`,
	})
	if _, err := onlyPublisher(t, threadsConfig(api)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
	}); err == nil {
		t.Fatal("expected the publish to fail")
	}
	if *attempts != 1 {
		t.Errorf("threads_publish was attempted %d times, want exactly 1", *attempts)
	}
}

// --- ping and construction ---------------------------------------------------

// TestThreadsPingReadsTheAccount: /me is the one question a Threads token can
// always be asked, and it is what a wrong token gets wrong.
func TestThreadsPingReadsTheAccount(t *testing.T) {
	rec := newRecorder()
	api, _ := newFakeThreads(t, rec)

	id, err := onlyPublisher(t, threadsConfig(api)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != threadsUser || id.Name != "@crier" {
		t.Errorf("identity = %+v", id)
	}
	if id.Note != "" {
		t.Errorf("the token owns the account, so there is nothing to warn about: %q", id.Note)
	}
	req := rec.all()[0]
	if req.Path != "/me" || !strings.Contains(req.Query, "fields=id%2Cusername") {
		t.Errorf("request = %s?%s", req.Path, req.Query)
	}
	// Nothing was posted, which is the property that makes ping safe.
	if _, ok := findRequest(rec, "/threads_publish"); ok {
		t.Error("ping published something")
	}
}

// TestThreadsPingNotesAUserIDThatIsNotTheTokens: an Instagram professional
// account id copied into publish.threads.user-id is the common mistake, and it
// posts nowhere rather than failing loudly.
func TestThreadsPingNotesAUserIDThatIsNotTheTokens(t *testing.T) {
	rec := newRecorder()
	api, _ := newFakeThreads(t, rec)
	cfg := threadsConfig(api)
	cfg.Publish.Threads.UserID = "17841400000000000"

	id, err := onlyPublisher(t, cfg).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{threadsUser, "17841400000000000", "publish.threads.user-id"} {
		if !strings.Contains(id.Note, want) {
			t.Errorf("missing %q in the note: %q", want, id.Note)
		}
	}
}

// TestThreadsPingRejectsABadToken is the scenario the command exists for.
func TestThreadsPingRejectsABadToken(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid OAuth access token.","code":190}}`))
	})
	_, err := onlyPublisher(t, threadsConfig(srv.URL)).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Invalid OAuth access token") {
		t.Errorf("err = %v", err)
	}
}

// TestThreadsReportsThePostWithoutALink: the post exists, and no link is better
// than one that goes nowhere.
func TestThreadsReportsThePostWithoutALink(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/threads_publish"):
			_, _ = w.Write([]byte(`{"id":"th-post-1"}`))
		case strings.HasSuffix(r.URL.Path, "/threads"):
			_, _ = w.Write([]byte(`{"id":"th-c1"}`))
		case r.URL.Path == "/th-post-1":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_, _ = w.Write([]byte(`{"status":"FINISHED"}`))
		}
	})
	res, err := onlyPublisher(t, threadsConfig(srv.URL)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://cdn.example/card.jpg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "th-post-1" || res.URL != "" {
		t.Errorf("result = %+v, want the post and no link", res)
	}
}

// TestThreadsNeedsAndConstructor: what Threads declares, and what it refuses to
// be built without.
func TestThreadsNeedsAndConstructor(t *testing.T) {
	needs := onlyPublisher(t, threadsConfig("https://threads.example")).Needs()
	if !needs.URL {
		t.Error("threads fetches the media itself; it needs staging")
	}
	if !needs.Accepts(render.KindImage) || !needs.Accepts(render.KindVideo) {
		t.Errorf("kinds = %v", needs.Kinds)
	}
	if needs.Accepts(render.KindGIF) {
		t.Error("threads takes no animation as a file")
	}
	// JPEG and PNG both, unlike Instagram.
	if len(needs.Formats) != 2 || needs.Formats[0] != config.JPEG || needs.Formats[1] != config.PNG {
		t.Errorf("formats = %v", needs.Formats)
	}
	if needs.Capacity() != ThreadsAttachmentMax {
		t.Errorf("capacity = %d, want %d", needs.Capacity(), ThreadsAttachmentMax)
	}

	for _, tt := range []struct {
		name  string
		build func(c *config.Config)
		want  string
	}{
		{"no token", func(c *config.Config) { c.Publish.Threads.Token = "" }, "publish.threads.token"},
		{"no user id", func(c *config.Config) { c.Publish.Threads.UserID = "" }, "publish.threads.user-id"},
		{"no base url", func(c *config.Config) { c.Publish.Threads.APIBaseURL = "" }, "publish.threads.api-base-url"},
	} {
		cfg := threadsConfig("https://threads.example")
		tt.build(cfg)
		_, err := Build(cfg, testDeps(t))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want it to name %s", tt.name, err, tt.want)
		}
	}
}

// TestThreadsRefusesMusicAndALeadVideo: the API takes neither, so the keys are
// refused with a reason rather than ignored.
func TestThreadsRefusesMusicAndALeadVideo(t *testing.T) {
	for key, set := range map[string]func(c *config.Config){
		"publish.threads.music-file": func(c *config.Config) { c.Publish.Threads.Music.File = "jingle.mp3" },
		"publish.threads.lead-video": func(c *config.Config) { c.Publish.Threads.LeadVideo.File = "anthem.mp4" },
	} {
		cfg := threadsConfig("https://threads.example")
		set(cfg)
		err := config.Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: err = %v", key, err)
		}
	}
}

// TestThreadsIsNamedForItsConfigurationKey, which is what the report and every
// per-platform key are spelled with.
func TestThreadsIsNamedForItsConfigurationKey(t *testing.T) {
	if got := onlyPublisher(t, threadsConfig("https://threads.example")).Name(); got != "threads" {
		t.Errorf("name = %q", got)
	}
}
