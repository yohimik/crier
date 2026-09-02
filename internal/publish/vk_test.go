package publish

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// The two walls VK draws a line between. The sign of the owner id is the whole
// of the distinction, and nearly every behaviour worth testing hangs off it.
const (
	vkCommunity = -123
	vkUser      = 777
)

func vkConfig(api string, owner int) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.VK.Enabled = true
	cfg.Publish.VK.APIBaseURL = api
	cfg.Publish.VK.Token = "vk-test-token"
	cfg.Publish.VK.OwnerID = owner
	return &cfg
}

// fakeVK is the API host and the upload servers it hands out.
//
// The upload servers are a second httptest server on purpose: VK's upload URLs
// are on a different host from the API and carry no token, and a fake that
// served both from one origin would not notice a publisher that sent the
// credential to the wrong one.
//
// The photo flow is the part with real linkage, so the fake enforces it: the
// server, photo and hash it hands out are what saveWallPhoto has to send back,
// and the id it answers with is derived from the blob, so a mixed-up pairing
// shows up as a wrong attachment string rather than as a passing test.
func fakeVK(t *testing.T, rec *recorder, owner int) (api string) {
	t.Helper()
	photos := 0

	var store string
	storeSrv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(req.Path, "/vk-photo-upload"):
			photos++
			fmt.Fprintf(w, `{"server":42,"photo":"BLOB-%d","hash":"HASH-%d"}`, photos, photos)
		case strings.HasSuffix(req.Path, "/vk-doc-upload"):
			_, _ = w.Write([]byte(`{"file":"DOCFILE-1"}`))
		case strings.HasSuffix(req.Path, "/vk-video-upload"):
			_, _ = w.Write([]byte(`{"size":1024,"video_id":77}`))
		default:
			http.NotFound(w, r)
		}
	})
	store = storeSrv.URL

	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		form, err := url.ParseQuery(req.Body)
		if err != nil {
			t.Errorf("%s: body is not a form: %q", req.Path, req.Body)
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(req.Path, "/method/photos.getWallUploadServer"):
			fmt.Fprintf(w, `{"response":{"upload_url":%q}}`, store+"/vk-photo-upload")
		case strings.HasSuffix(req.Path, "/method/photos.saveWallPhoto"):
			// The id comes out of the blob, so the attachment string is only
			// right when the save call carried what the upload returned.
			n := strings.TrimPrefix(form.Get("photo"), "BLOB-")
			if form.Get("hash") != "HASH-"+n || form.Get("server") != "42" {
				t.Errorf("saveWallPhoto got server=%q photo=%q hash=%q, which do not belong together",
					form.Get("server"), form.Get("photo"), form.Get("hash"))
			}
			id, _ := strconv.Atoi(n)
			fmt.Fprintf(w, `{"response":[{"id":%d,"owner_id":%d}]}`, 1000+id, owner)
		case strings.HasSuffix(req.Path, "/method/video.save"):
			fmt.Fprintf(w, `{"response":{"upload_url":%q,"owner_id":%d,"video_id":77}}`,
				store+"/vk-video-upload", owner)
		case strings.HasSuffix(req.Path, "/method/docs.getWallUploadServer"):
			fmt.Fprintf(w, `{"response":{"upload_url":%q}}`, store+"/vk-doc-upload")
		case strings.HasSuffix(req.Path, "/method/docs.save"):
			fmt.Fprintf(w, `{"response":{"type":"doc","doc":{"id":55,"owner_id":%d}}}`, owner)
		case strings.HasSuffix(req.Path, "/method/wall.post"):
			_, _ = w.Write([]byte(`{"response":{"post_id":9001}}`))
		case strings.HasSuffix(req.Path, "/method/groups.getById"):
			_, _ = w.Write([]byte(
				`{"response":[{"id":123,"name":"Crier Community","screen_name":"crierhq"}]}`))
		case strings.HasSuffix(req.Path, "/method/users.get"):
			fmt.Fprintf(w, `{"response":[{"id":%d,"first_name":"Crier","last_name":"Bot"}]}`, vkUser)
		default:
			http.NotFound(w, r)
		}
	})
	return srv.URL
}

// vkForm is one recorded method call's body, parsed.
func vkForm(t *testing.T, rec *recorder, method string) url.Values {
	t.Helper()
	req, ok := findRequest(rec, "/method/"+method)
	if !ok {
		t.Fatalf("vk was never asked for %s", method)
	}
	form, err := url.ParseQuery(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	return form
}

// TestVKRunsThePhotoFlow is the whole of an ordinary post, and the linkage
// between its steps: a photo the save call did not name is a post with an
// attachment nobody uploaded.
func TestVKRunsThePhotoFlow(t *testing.T) {
	rec := newRecorder()
	api := fakeVK(t, rec, vkCommunity)

	res, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), Caption: "a card from crier",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "-123_9001" {
		t.Errorf("id = %q, want the owner and the post id", res.ID)
	}
	if res.URL != "https://vk.com/wall-123_9001" {
		t.Errorf("url = %q", res.URL)
	}

	// Every call carries the token and the API version as form fields; VK has
	// no header for either.
	first := vkForm(t, rec, "photos.getWallUploadServer")
	if first.Get("access_token") != "vk-test-token" || first.Get("v") != "5.199" {
		t.Errorf("the credential and the version did not go out: %v", first)
	}
	// A community wall needs the group id: without it the slot is on the
	// caller's own wall and the photo cannot be attached to a community post.
	if first.Get("group_id") != "123" {
		t.Errorf("group_id = %q, want the owner id without its sign", first.Get("group_id"))
	}

	// The bytes went to the URL the API named, on the other host, with no
	// token on them.
	upload, ok := findRequest(rec, "/vk-photo-upload")
	if !ok {
		t.Fatal("nothing was uploaded")
	}
	if !strings.Contains(upload.Body, `name="photo"`) {
		t.Errorf("the file part should be named photo: %q", upload.Body)
	}
	if !strings.Contains(upload.Body, "JPEGDATA") {
		t.Errorf("the file itself did not go out: %q", upload.Body)
	}
	if upload.Header.Get("Authorization") != "" {
		t.Error("the upload carried an Authorization header vk never asked for")
	}

	// The save call forwards exactly what the upload server said. The fake
	// checks the triple belongs together; this checks it was sent at all.
	save := vkForm(t, rec, "photos.saveWallPhoto")
	if save.Get("server") != "42" || save.Get("photo") != "BLOB-1" || save.Get("hash") != "HASH-1" {
		t.Errorf("saveWallPhoto sent %v, want what the upload server returned", save)
	}
	if save.Get("group_id") != "123" {
		t.Errorf("saveWallPhoto group_id = %q", save.Get("group_id"))
	}

	// And the post names the saved photo, the wall, and the caption.
	post := vkForm(t, rec, "wall.post")
	if post.Get("owner_id") != "-123" {
		t.Errorf("owner_id = %q", post.Get("owner_id"))
	}
	if post.Get("from_group") != "1" {
		t.Error("a community post should be signed by the community")
	}
	if post.Get("message") != "a card from crier" {
		t.Errorf("message = %q", post.Get("message"))
	}
	if post.Get("attachments") != "photo-123_1001" {
		t.Errorf("attachments = %q, want the photo the save call returned", post.Get("attachments"))
	}
	if res.Extra["attachments"] != "photo-123_1001" {
		t.Errorf("extra = %v", res.Extra)
	}
}

// TestVKPostsSeveralPagesAsOnePost: the whole point of the ten-attachment cap
// is that a five-page changelog is one entry on the wall rather than five.
func TestVKPostsSeveralPagesAsOnePost(t *testing.T) {
	rec := newRecorder()
	api := fakeVK(t, rec, vkCommunity)

	arts := []render.Artifact{imageArtifact(t), imageArtifact(t), imageArtifact(t)}
	res, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Publish(context.Background(), Input{
		Artifact: arts[0], Artifacts: arts, Caption: "three pages",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := countPaths(rec, "/method/wall.post"); got != 1 {
		t.Errorf("made %d posts, want one", got)
	}
	if got := countPaths(rec, "/vk-photo-upload"); got != 3 {
		t.Errorf("uploaded %d photos, want 3", got)
	}

	// The attachments are comma joined, and they are in page order: the fake
	// numbers each upload, so a reordered list shows up here.
	post := vkForm(t, rec, "wall.post")
	want := "photo-123_1001,photo-123_1002,photo-123_1003"
	if post.Get("attachments") != want {
		t.Errorf("attachments = %q, want %q", post.Get("attachments"), want)
	}
	if res.Extra["attachments"] != want {
		t.Errorf("extra = %v", res.Extra)
	}
}

// TestVKRefusesMoreThanTheWallTakes is the backstop under the cap the pipeline
// paginates against.
func TestVKRefusesMoreThanTheWallTakes(t *testing.T) {
	arts := make([]render.Artifact, VKAttachmentMax+1)
	for i := range arts {
		arts[i] = imageArtifact(t)
	}
	_, err := onlyPublisher(t, vkConfig("https://vk.example", vkCommunity)).
		Publish(context.Background(), Input{Artifact: arts[0], Artifacts: arts})
	if err == nil || !strings.Contains(err.Error(), "takes 10 attachments") {
		t.Errorf("err = %v", err)
	}
}

// TestVKPostsOnAUserWallUnsigned: from_group is what makes a post the
// community's rather than the person's, and it means nothing on a personal
// wall.
func TestVKPostsOnAUserWallUnsigned(t *testing.T) {
	rec := newRecorder()
	api := fakeVK(t, rec, vkUser)

	res, err := onlyPublisher(t, vkConfig(api, vkUser)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t)})
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "https://vk.com/wall777_9001" {
		t.Errorf("url = %q", res.URL)
	}
	post := vkForm(t, rec, "wall.post")
	if _, ok := post["from_group"]; ok {
		t.Errorf("from_group was sent for a personal wall: %v", post)
	}
	// And no group_id anywhere, because there is no group.
	if got := vkForm(t, rec, "photos.getWallUploadServer").Get("group_id"); got != "" {
		t.Errorf("group_id = %q on a personal wall", got)
	}
	// A caption nobody set is absent rather than empty.
	if _, ok := post["message"]; ok {
		t.Errorf("an empty message was sent: %v", post)
	}
}

// TestVKPostsAVideo: video.save hands out the ids and the upload URL at once,
// so the attachment string is known before the bytes go anywhere.
func TestVKPostsAVideo(t *testing.T) {
	rec := newRecorder()
	api := fakeVK(t, rec, vkCommunity)

	_, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Publish(context.Background(), Input{
		Artifact: videoArtifact(t, 64), Caption: "the clip\nand a second line",
	})
	if err != nil {
		t.Fatal(err)
	}
	save := vkForm(t, rec, "video.save")
	if save.Get("name") != "the clip" {
		t.Errorf("name = %q, want the caption's first line", save.Get("name"))
	}
	if save.Get("group_id") != "123" {
		t.Errorf("group_id = %q", save.Get("group_id"))
	}
	upload, ok := findRequest(rec, "/vk-video-upload")
	if !ok {
		t.Fatal("the clip was never uploaded")
	}
	if !strings.Contains(upload.Body, `name="video_file"`) {
		t.Errorf("the file part should be named video_file: %q", upload.Body)
	}
	if got := vkForm(t, rec, "wall.post").Get("attachments"); got != "video-123_77" {
		t.Errorf("attachments = %q", got)
	}
	// Nothing went near the photo methods.
	if _, ok := findRequest(rec, "photos."); ok {
		t.Error("a video post touched the photo methods")
	}
}

// TestVKVideoNameFallsBackToCrier: video.save wants a name and shows it beside
// the clip, so an untitled video is worth avoiding.
func TestVKVideoNameFallsBackToCrier(t *testing.T) {
	rec := newRecorder()
	api := fakeVK(t, rec, vkCommunity)

	if _, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 8)}); err != nil {
		t.Fatal(err)
	}
	if got := vkForm(t, rec, "video.save").Get("name"); got != "crier" {
		t.Errorf("name = %q, want the fallback", got)
	}
}

// TestVKPostsAnAnimationAsADocument: saveWallPhoto flattens a GIF into a still,
// and a document is the only shape that keeps it moving on a wall.
func TestVKPostsAnAnimationAsADocument(t *testing.T) {
	rec := newRecorder()
	api := fakeVK(t, rec, vkCommunity)

	_, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Publish(context.Background(),
		Input{Artifact: gifArtifact(t, 100), Caption: "it moves"})
	if err != nil {
		t.Fatal(err)
	}
	upload, ok := findRequest(rec, "/vk-doc-upload")
	if !ok {
		t.Fatal("the animation was never uploaded")
	}
	if !strings.Contains(upload.Body, `name="file"`) {
		t.Errorf("the file part should be named file: %q", upload.Body)
	}
	if !strings.Contains(upload.Body, "GIF89a") {
		t.Errorf("the animation itself did not go out: %q", upload.Body)
	}
	if got := vkForm(t, rec, "docs.save").Get("file"); got != "DOCFILE-1" {
		t.Errorf("docs.save file = %q, want what the upload server returned", got)
	}
	if got := vkForm(t, rec, "wall.post").Get("attachments"); got != "doc-123_55" {
		t.Errorf("attachments = %q", got)
	}
	if _, ok := findRequest(rec, "photos."); ok {
		t.Error("an animation went through the photo methods and would arrive as a still")
	}
}

// TestVKAnswersAnErrorInsideATwoHundred is the trap: VK reports every failure
// with HTTP 200 and an error object, so the status code alone says nothing.
func TestVKAnswersAnErrorInsideATwoHundred(t *testing.T) {
	for _, tt := range []struct {
		name  string
		reply string
		says  []string
	}{
		{"a bad token", `{"error":{"error_code":5,"error_msg":"User authorization failed: invalid access_token."}}`,
			[]string{"error 5", "invalid access_token"}},
		{"no rights to the wall", `{"error":{"error_code":15,"error_msg":"Access denied"}}`,
			[]string{"error 15", "Access denied"}},
		{"nothing at all", `{}`, []string{"neither a response nor an error"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.reply))
			})
			_, err := onlyPublisher(t, vkConfig(srv.URL, vkCommunity)).Publish(context.Background(),
				Input{Artifact: imageArtifact(t)})
			if err == nil {
				t.Fatal("a 200 carrying an error is a failure")
			}
			for _, want := range tt.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in: %v", want, err)
				}
			}
		})
	}
}

// TestVKUploadServerSavesNoPhoto: the upload server answers 200 either way and
// says so in the body, so reading it as a success is how a post ends up with
// no picture and no error.
func TestVKUploadServerSavesNoPhoto(t *testing.T) {
	for _, reply := range []string{
		`{"server":42,"photo":"[]","hash":"h"}`,
		`{"server":42,"photo":"","hash":"h"}`,
	} {
		store := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(reply))
		})
		srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/photos.getWallUploadServer") {
				fmt.Fprintf(w, `{"response":{"upload_url":%q}}`, store.URL+"/u")
				return
			}
			t.Errorf("nothing should have been saved or posted: %s", r.URL.Path)
			http.NotFound(w, r)
		})
		_, err := onlyPublisher(t, vkConfig(srv.URL, vkCommunity)).Publish(context.Background(),
			Input{Artifact: imageArtifact(t)})
		if err == nil || !strings.Contains(err.Error(), "saved no photo") {
			t.Errorf("%s: err = %v", reply, err)
		}
	}
}

// TestVKPingReadsTheCommunity: groups.getById is where a token without rights
// to the group fails, which is the half of a VK setup that goes wrong.
func TestVKPingReadsTheCommunity(t *testing.T) {
	rec := newRecorder()
	api := fakeVK(t, rec, vkCommunity)

	id, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "123" {
		t.Errorf("id = %q", id.ID)
	}
	if id.Name != "Crier Community (@crierhq)" {
		t.Errorf("name = %q", id.Name)
	}
	if !strings.Contains(id.Note, "signed by the community") {
		t.Errorf("note = %q", id.Note)
	}
	if got := vkForm(t, rec, "groups.getById").Get("group_id"); got != "123" {
		t.Errorf("group_id = %q", got)
	}
	// Nothing was posted, which is the property that makes ping safe.
	for _, method := range []string{"wall.post", "photos.getWallUploadServer", "docs.save"} {
		if _, ok := findRequest(rec, "/method/"+method); ok {
			t.Errorf("ping called %s", method)
		}
	}
}

// TestVKPingAcceptsTheWrappedGroupList: the method answered with a bare array
// and became an object with a "groups" key partway through the 5.x line, and
// the version is configurable.
func TestVKPingAcceptsTheWrappedGroupList(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"groups":[{"id":123,"name":"Crier Community"}]}}`))
	})
	id, err := onlyPublisher(t, vkConfig(srv.URL, vkCommunity)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "123" || id.Name != "Crier Community" {
		t.Errorf("identity = %+v", id)
	}
}

// TestVKPingReadsTheTokensOwnAccount: users.get with no user_ids is the one
// question a token can always be asked.
func TestVKPingReadsTheTokensOwnAccount(t *testing.T) {
	rec := newRecorder()
	api := fakeVK(t, rec, vkUser)

	id, err := onlyPublisher(t, vkConfig(api, vkUser)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "777" || id.Name != "Crier Bot" {
		t.Errorf("identity = %+v", id)
	}
	if id.Note != "" {
		t.Errorf("the token owns the wall, so there is nothing to warn about: %q", id.Note)
	}
	if got := vkForm(t, rec, "users.get"); got.Get("user_ids") != "" {
		t.Errorf("user_ids was sent: %v", got)
	}
}

// TestVKPingNotesAWallThatIsNotTheTokensOwn: legal, and almost always a
// mistyped owner id.
func TestVKPingNotesAWallThatIsNotTheTokensOwn(t *testing.T) {
	rec := newRecorder()
	api := fakeVK(t, rec, vkUser)

	id, err := onlyPublisher(t, vkConfig(api, 999)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(id.Note, "999") || !strings.Contains(id.Note, "not this token's own account") {
		t.Errorf("note = %q", id.Note)
	}
}

// TestVKPingRejectsABadToken is the scenario the command exists for.
func TestVKPingRejectsABadToken(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"error":{"error_code":5,"error_msg":"User authorization failed: invalid access_token."}}`))
	})
	for _, owner := range []int{vkCommunity, vkUser} {
		_, err := onlyPublisher(t, vkConfig(srv.URL, owner)).Ping(context.Background())
		if err == nil || !strings.Contains(err.Error(), "error 5") {
			t.Errorf("owner %d: err = %v", owner, err)
		}
	}
}

// TestVKPingFindsNoCommunity: the token works and the owner id names nothing.
func TestVKPingFindsNoCommunity(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":[]}`))
	})
	_, err := onlyPublisher(t, vkConfig(srv.URL, vkCommunity)).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "publish.vk.owner-id") {
		t.Errorf("err = %v", err)
	}
}

// TestVKNeedsAndConstructor: what VK declares, and what it refuses to be built
// without.
func TestVKNeedsAndConstructor(t *testing.T) {
	needs := onlyPublisher(t, vkConfig("https://vk.example", vkCommunity)).Needs()
	if needs.URL {
		t.Error("vk takes the bytes; it needs no staging")
	}
	for _, kind := range []render.Kind{render.KindImage, render.KindVideo, render.KindGIF} {
		if !needs.Accepts(kind) {
			t.Errorf("vk should accept %s", kind)
		}
	}
	if len(needs.Formats) == 0 || needs.Formats[0] != config.JPEG {
		t.Errorf("formats = %v", needs.Formats)
	}
	if needs.Capacity() != VKAttachmentMax {
		t.Errorf("capacity = %d, want %d", needs.Capacity(), VKAttachmentMax)
	}

	for _, tt := range []struct {
		name  string
		build func(c *config.Config)
		want  string
	}{
		{"no token", func(c *config.Config) { c.Publish.VK.Token = "" }, "publish.vk.token"},
		{"no base url", func(c *config.Config) { c.Publish.VK.APIBaseURL = "" }, "publish.vk.api-base-url"},
		{"no version", func(c *config.Config) { c.Publish.VK.APIVersion = "" }, "publish.vk.api-version"},
		{"no owner", func(c *config.Config) { c.Publish.VK.OwnerID = 0 }, "publish.vk.owner-id"},
	} {
		cfg := vkConfig("https://vk.example", vkCommunity)
		tt.build(cfg)
		_, err := Build(cfg, testDeps(t))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want it to name %s", tt.name, err, tt.want)
		}
	}
}

// TestVKOwnerIDZeroSaysWhyItMatters: a zero that defaulted to the token's own
// account would put a community post on somebody's personal page.
func TestVKOwnerIDZeroSaysWhyItMatters(t *testing.T) {
	cfg := vkConfig("https://vk.example", 0)
	_, err := Build(cfg, testDeps(t))
	if err == nil {
		t.Fatal("a zero owner id is not a wall")
	}
	for _, want := range []string{"negative for a community", "CRIER_PUBLISH_VK_OWNER_ID"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
}

// --- one step at a time ------------------------------------------------------

// vkScript is a fake whose answer to each step is written out, so one test can
// put exactly one of them wrong and leave the rest working. An empty answer is
// a 500 from that step.
//
// The keys are method names, plus "upload:photo", "upload:video" and
// "upload:doc" for the three upload servers.
type vkScript map[string]string

func fakeVKScript(t *testing.T, script vkScript) string {
	t.Helper()

	store := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		key := "upload:" + strings.TrimPrefix(r.URL.Path, "/")
		reply, overridden := script[key]
		if overridden && reply == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if overridden {
			_, _ = w.Write([]byte(reply))
			return
		}
		switch key {
		case "upload:photo":
			_, _ = w.Write([]byte(`{"server":42,"photo":"B","hash":"H"}`))
		case "upload:doc":
			_, _ = w.Write([]byte(`{"file":"F"}`))
		default:
			_, _ = w.Write([]byte(`{"size":1024}`))
		}
	})

	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/method/")
		reply, overridden := script[name]
		if overridden && reply == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if overridden {
			_, _ = w.Write([]byte(reply))
			return
		}
		switch name {
		case "photos.getWallUploadServer":
			fmt.Fprintf(w, `{"response":{"upload_url":%q}}`, store.URL+"/photo")
		case "photos.saveWallPhoto":
			fmt.Fprintf(w, `{"response":[{"id":11,"owner_id":%d}]}`, vkCommunity)
		case "video.save":
			fmt.Fprintf(w, `{"response":{"upload_url":%q,"owner_id":%d,"video_id":77}}`,
				store.URL+"/video", vkCommunity)
		case "docs.getWallUploadServer":
			fmt.Fprintf(w, `{"response":{"upload_url":%q}}`, store.URL+"/doc")
		case "docs.save":
			fmt.Fprintf(w, `{"response":{"type":"doc","doc":{"id":55,"owner_id":%d}}}`, vkCommunity)
		case "wall.post":
			_, _ = w.Write([]byte(`{"response":{"post_id":9001}}`))
		case "users.get":
			fmt.Fprintf(w, `{"response":[{"id":%d,"first_name":"Crier","last_name":"Bot"}]}`, vkUser)
		case "groups.getById":
			_, _ = w.Write([]byte(`{"response":[{"id":123,"name":"Crier Community"}]}`))
		default:
			http.NotFound(w, r)
		}
	})
	return srv.URL
}

// TestVKSurfacesEveryStepThatCannotBeUsed walks the flow one broken step at a
// time.
//
// Each of these answers 200 and is unusable, which is the shape VK's failures
// take: an empty object where an id should be, a saved nothing, a document
// whose upload server took the file and kept it. Any of them read as a success
// is a post that goes out with a piece missing and no error anywhere.
func TestVKSurfacesEveryStepThatCannotBeUsed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		kind   render.Kind
		script vkScript
		says   string
	}{
		{"no photo upload server", render.KindImage,
			vkScript{"photos.getWallUploadServer": `{"response":{}}`}, "no photo upload server"},
		{"the photo upload host refused", render.KindImage,
			vkScript{"upload:photo": ""}, "uploading the photo to vk"},
		{"the photo saved as nothing", render.KindImage,
			vkScript{"photos.saveWallPhoto": `{"response":[]}`}, "named none"},
		{"a response of the wrong shape", render.KindImage,
			vkScript{"photos.getWallUploadServer": `{"response":"a string"}`},
			"decoding the vk photos.getWallUploadServer response"},
		{"the post named no id", render.KindImage,
			vkScript{"wall.post": `{"response":{}}`}, "named no post id"},

		{"no video upload server", render.KindVideo,
			vkScript{"video.save": `{"response":{}}`}, "no video upload server"},
		{"the video upload host refused", render.KindVideo,
			vkScript{"upload:video": ""}, "uploading the video to vk"},

		{"no document upload server", render.KindGIF,
			vkScript{"docs.getWallUploadServer": `{"response":{}}`}, "no document upload server"},
		{"the document upload host refused", render.KindGIF,
			vkScript{"upload:doc": ""}, "uploading the document to vk"},
		{"the document saved as nothing", render.KindGIF,
			vkScript{"upload:doc": `{"file":""}`}, "saved no document"},
		{"the document has no id", render.KindGIF,
			vkScript{"docs.save": `{"response":{"type":"doc","doc":{}}}`}, "named no id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := fakeVKScript(t, tt.script)
			var art render.Artifact
			switch tt.kind {
			case render.KindVideo:
				art = videoArtifact(t, 16)
			case render.KindGIF:
				art = gifArtifact(t, 16)
			default:
				art = imageArtifact(t)
			}
			_, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Publish(context.Background(),
				Input{Artifact: art, Caption: "a post"})
			if err == nil {
				t.Fatal("expected the post to be refused")
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("missing %q in: %v", tt.says, err)
			}
		})
	}
}

// TestVKNamesWhichAttachmentFailed: a five-page post that stops on page four
// should say four, because "the post failed" leaves nothing to act on.
func TestVKNamesWhichAttachmentFailed(t *testing.T) {
	api := fakeVKScript(t, vkScript{"photos.saveWallPhoto": `{"response":[]}`})
	arts := []render.Artifact{imageArtifact(t), imageArtifact(t)}
	_, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Publish(context.Background(),
		Input{Artifact: arts[0], Artifacts: arts})
	if err == nil || !strings.Contains(err.Error(), "attachment 1 of 2") {
		t.Errorf("err = %v", err)
	}
}

// TestVKPingWithNoAccount: the token was accepted and named nobody, which is
// not an identity crier can report.
func TestVKPingWithNoAccount(t *testing.T) {
	api := fakeVKScript(t, vkScript{"users.get": `{"response":[]}`})
	_, err := onlyPublisher(t, vkConfig(api, vkUser)).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "named no account") {
		t.Errorf("err = %v", err)
	}
}

// TestVKPingWithAnUnreadableGroupList: neither shape the method has answered
// with, so there is nothing to read.
func TestVKPingWithAnUnreadableGroupList(t *testing.T) {
	api := fakeVKScript(t, vkScript{"groups.getById": `{"response":"a string"}`})
	_, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "groups.getById") {
		t.Errorf("err = %v", err)
	}
}

// TestVKPingWithAGroupThatHasNoScreenName: the name alone is the identity.
func TestVKPingWithAGroupThatHasNoScreenName(t *testing.T) {
	api := fakeVKScript(t, nil)
	id, err := onlyPublisher(t, vkConfig(api, vkCommunity)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "Crier Community" {
		t.Errorf("name = %q", id.Name)
	}
}

// TestVKIsNamedForItsConfigurationKey, which is what the report and every
// per-platform key are spelled with.
func TestVKIsNamedForItsConfigurationKey(t *testing.T) {
	if got := onlyPublisher(t, vkConfig("https://vk.example", vkCommunity)).Name(); got != "vk" {
		t.Errorf("name = %q", got)
	}
}
