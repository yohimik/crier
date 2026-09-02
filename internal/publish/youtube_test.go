package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// youtubeChannel is the channel every fake in this file uploads to.
const youtubeChannel = "UC-crier"

func youtubeConfig(api, auth string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.YouTube.Enabled = true
	cfg.Publish.YouTube.APIBaseURL = api
	cfg.Publish.YouTube.AuthBaseURL = auth
	cfg.Publish.YouTube.ClientID = "client-id"
	cfg.Publish.YouTube.ClientSecret = "client-secret"
	cfg.Publish.YouTube.RefreshToken = "refresh-token"
	return &cfg
}

// fakeYouTube is the API as the publisher has to speak it.
//
// It enforces the linkage rather than only answering. The upload session is
// minted here and the PUT has to go to the one that was handed out, the video
// id is derived from the bytes actually received so a truncated upload cannot
// pass, and every call but the token refresh has to carry the bearer the token
// endpoint issued. A flow assembled out of the wrong pieces therefore shows up
// as a refusal instead of as a passing test.
type fakeYouTube struct {
	t *testing.T
	// base is where the session URLs this fake hands out point back to.
	base string

	mu       sync.Mutex
	sessions map[string]map[string]any
	// tokens counts the refreshes, which is how "once per run" is checked.
	tokens int
	// videos maps a video id to the bytes the PUT carried.
	videos map[string]int
	// thumbnailStatus is what thumbnails/set answers with. 200 by default.
	thumbnailStatus int
	// channels is what the identity call reports. Nil means one channel.
	channels []map[string]any
	noChanel bool
}

func newFakeYouTube(t *testing.T, rec *recorder) (base string, f *fakeYouTube) {
	t.Helper()
	f = &fakeYouTube{
		t: t, sessions: map[string]map[string]any{}, videos: map[string]int{},
		thumbnailStatus: http.StatusOK,
	}
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		f.serve(w, r, req)
	})
	f.base = srv.URL
	return srv.URL, f
}

func (f *fakeYouTube) serve(w http.ResponseWriter, r *http.Request, req recorded) {
	switch {
	case req.Path == "/token":
		f.token(w, req)
	case req.Path == "/upload/youtube/v3/videos":
		f.initiate(w, req)
	case strings.HasPrefix(req.Path, "/upload-session/"):
		f.upload(w, req)
	case req.Path == "/upload/youtube/v3/thumbnails/set":
		f.thumbnail(w, req)
	case req.Path == "/youtube/v3/channels":
		f.channelList(w, req)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeYouTube) token(w http.ResponseWriter, req recorded) {
	form, err := url.ParseQuery(req.Body)
	if err != nil {
		f.t.Errorf("the token body is not a form: %q", req.Body)
	}
	for _, want := range []string{"client_id", "client_secret", "refresh_token"} {
		if form.Get(want) == "" {
			f.refuse(w, http.StatusBadRequest, "invalid_request", "no "+want)
			return
		}
	}
	if form.Get("grant_type") != "refresh_token" {
		f.refuse(w, http.StatusBadRequest, "unsupported_grant_type", form.Get("grant_type"))
		return
	}
	if form.Get("refresh_token") == "bad-token" {
		f.refuse(w, http.StatusBadRequest, "invalid_grant", "Token has been expired or revoked.")
		return
	}
	f.mu.Lock()
	f.tokens++
	f.mu.Unlock()
	_, _ = w.Write([]byte(`{"access_token":"yt-access","expires_in":3600,"token_type":"Bearer"}`))
}

func (f *fakeYouTube) initiate(w http.ResponseWriter, req recorded) {
	if !f.authed(w, req) {
		return
	}
	query, _ := url.ParseQuery(req.Query)
	if query.Get("uploadType") != "resumable" {
		f.refuse(w, http.StatusBadRequest, "badRequest", "uploadType is "+query.Get("uploadType"))
		return
	}
	if query.Get("part") != "snippet,status" {
		f.refuse(w, http.StatusBadRequest, "badRequest", "part is "+query.Get("part"))
		return
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		f.refuse(w, http.StatusBadRequest, "parseError", err.Error())
		return
	}
	snippet, _ := body["snippet"].(map[string]any)
	title, _ := snippet["title"].(string)
	if strings.TrimSpace(title) == "" {
		f.refuse(w, http.StatusBadRequest, "invalidTitle", "a video needs a title")
		return
	}
	if strings.ContainsAny(title, "<>") {
		f.refuse(w, http.StatusBadRequest, "invalidTitle", "a title cannot contain < or >")
		return
	}
	if len([]rune(title)) > YouTubeTitleMax {
		f.refuse(w, http.StatusBadRequest, "invalidTitle",
			fmt.Sprintf("a title of %d characters is over the limit", len([]rune(title))))
		return
	}

	f.mu.Lock()
	id := fmt.Sprintf("s%d", len(f.sessions)+1)
	f.sessions[id] = body
	f.mu.Unlock()

	w.Header().Set("Location", f.base+"/upload-session/"+id+"?upload_id="+id)
	_, _ = w.Write([]byte(`{}`))
}

// upload mints the video id out of the byte count, so an upload that arrived
// short is a wrong id rather than a passing test.
func (f *fakeYouTube) upload(w http.ResponseWriter, req recorded) {
	if !f.authed(w, req) {
		return
	}
	session := strings.TrimPrefix(req.Path, "/upload-session/")
	f.mu.Lock()
	body, ok := f.sessions[session]
	f.mu.Unlock()
	if !ok {
		f.refuse(w, http.StatusNotFound, "notFound", "no such upload session: "+session)
		return
	}
	id := fmt.Sprintf("vid-%d", len(req.Body))
	f.mu.Lock()
	f.videos[id] = len(req.Body)
	f.mu.Unlock()

	snippet, _ := body["snippet"].(map[string]any)
	out := map[string]any{"id": id, "snippet": snippet, "status": body["status"]}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeYouTube) thumbnail(w http.ResponseWriter, req recorded) {
	if !f.authed(w, req) {
		return
	}
	query, _ := url.ParseQuery(req.Query)
	if query.Get("videoId") == "" {
		f.refuse(w, http.StatusBadRequest, "badRequest", "no videoId")
		return
	}
	if query.Get("uploadType") != "media" {
		f.refuse(w, http.StatusBadRequest, "badRequest", "uploadType is "+query.Get("uploadType"))
		return
	}
	if f.thumbnailStatus != http.StatusOK {
		// Google's own answer for an unverified channel.
		f.refuse(w, f.thumbnailStatus, "forbidden",
			"The authenticated user doesn't have permissions to upload and set custom video thumbnails.")
		return
	}
	_, _ = w.Write([]byte(`{"items":[{"default":{"url":"https://i.ytimg.com/x.jpg"}}]}`))
}

func (f *fakeYouTube) channelList(w http.ResponseWriter, req recorded) {
	if !f.authed(w, req) {
		return
	}
	query, _ := url.ParseQuery(req.Query)
	if query.Get("mine") != "true" || query.Get("part") != "snippet" {
		f.refuse(w, http.StatusBadRequest, "badRequest", "mine and part are what name the channel")
		return
	}
	items := f.channels
	if items == nil && !f.noChanel {
		items = []map[string]any{
			{"id": youtubeChannel, "snippet": map[string]any{"title": "Crier Releases"}},
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

// authed is the rule every call but the token refresh obeys.
func (f *fakeYouTube) authed(w http.ResponseWriter, req recorded) bool {
	if req.Header.Get("Authorization") != "Bearer yt-access" {
		f.refuse(w, http.StatusUnauthorized, "unauthorized",
			"the call carried "+req.Header.Get("Authorization"))
		return false
	}
	return true
}

func (f *fakeYouTube) refuse(w http.ResponseWriter, status int, reason, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":             map[string]any{"code": status, "message": message},
		"error_description": message,
		"errorReason":       reason,
	})
}

// --- the flow ---------------------------------------------------------------

// TestYouTubeUploadsAVideoInTwoSteps is the whole contract: one token refresh,
// the metadata in the initiate body, the Location honoured, the bytes streamed
// to it, and the watch link built from the id that came back.
func TestYouTubeUploadsAVideoInTwoSteps(t *testing.T) {
	rec := newRecorder()
	base, f := newFakeYouTube(t, rec)

	cfg := youtubeConfig(base, base)
	cfg.Publish.YouTube.Title = "crier v9.9.9 is out"
	cfg.Publish.YouTube.CategoryID = "28"
	cfg.Publish.YouTube.PrivacyStatus = "unlisted"

	clip := videoArtifact(t, 4096)
	res, err := onlyPublisher(t, cfg).Publish(context.Background(), Input{
		Artifact: clip,
		Caption:  "the release, in ten seconds\n\n#Shorts",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "vid-4096" {
		t.Errorf("id = %q; the fake names the video after the bytes it received", res.ID)
	}
	if res.URL != "https://www.youtube.com/watch?v=vid-4096" {
		t.Errorf("url = %q", res.URL)
	}
	if res.Extra["videoId"] != "vid-4096" {
		t.Errorf("extra = %v", res.Extra)
	}

	// One refresh for the two calls that needed a bearer.
	if f.tokens != 1 {
		t.Errorf("the token was refreshed %d times, want once per run", f.tokens)
	}

	init, ok := findRequest(rec, "/upload/youtube/v3/videos")
	if !ok {
		t.Fatal("nothing initiated an upload")
	}
	var body struct {
		Snippet struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			CategoryID  string `json:"categoryId"`
		} `json:"snippet"`
		Status struct {
			PrivacyStatus           string `json:"privacyStatus"`
			SelfDeclaredMadeForKids *bool  `json:"selfDeclaredMadeForKids"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(init.Body), &body); err != nil {
		t.Fatalf("the initiate body is not JSON: %v\n%s", err, init.Body)
	}
	if body.Snippet.Title != "crier v9.9.9 is out" {
		t.Errorf("title = %q", body.Snippet.Title)
	}
	if body.Snippet.Description != "the release, in ten seconds\n\n#Shorts" {
		t.Errorf("description = %q; it is the resolved caption", body.Snippet.Description)
	}
	if body.Snippet.CategoryID != "28" {
		t.Errorf("categoryId = %q", body.Snippet.CategoryID)
	}
	if body.Status.PrivacyStatus != "unlisted" {
		t.Errorf("privacyStatus = %q", body.Status.PrivacyStatus)
	}
	if body.Status.SelfDeclaredMadeForKids == nil || *body.Status.SelfDeclaredMadeForKids {
		t.Errorf("selfDeclaredMadeForKids = %v, want a declared false", body.Status.SelfDeclaredMadeForKids)
	}

	// The bytes went to the session the initiate named, not to the API host.
	put, ok := findRequest(rec, "/upload-session/")
	if !ok {
		t.Fatal("the bytes never reached the upload session")
	}
	if put.Method != http.MethodPut {
		t.Errorf("the upload was a %s", put.Method)
	}
	if len(put.Body) != 4096 {
		t.Errorf("the upload carried %d bytes, want 4096", len(put.Body))
	}
	if put.Header.Get("Content-Type") != render.VideoContentType {
		t.Errorf("content type = %q", put.Header.Get("Content-Type"))
	}
	if put.Header.Get("Content-Length") != "4096" {
		t.Errorf("content length = %q; a resumable upload wants a length",
			put.Header.Get("Content-Length"))
	}
}

// TestYouTubeRefusesAnImage: the Data API has no way to post a still, and a
// picture that arrived here would otherwise be uploaded as a video file.
func TestYouTubeRefusesAnImage(t *testing.T) {
	rec := newRecorder()
	base, _ := newFakeYouTube(t, rec)

	_, err := onlyPublisher(t, youtubeConfig(base, base)).Publish(context.Background(),
		Input{Artifact: imageArtifact(t), Caption: "a card"})
	if err == nil || !strings.Contains(err.Error(), "community post") {
		t.Fatalf("err = %v, want a refusal explaining there is no image endpoint", err)
	}
	if len(rec.all()) != 0 {
		t.Errorf("it made %d requests before refusing", len(rec.all()))
	}
}

// TestYouTubeTitleFallsBackThroughTheChain: the configured title, then the
// caption's first line cut to the limit, then a name rather than a blank.
func TestYouTubeTitleFallsBackThroughTheChain(t *testing.T) {
	long := strings.Repeat("a", 150)
	for _, tt := range []struct {
		name    string
		title   string
		caption string
		want    string
	}{
		{"configured", "A title", "some words\nmore words", "A title"},
		{"first line of the caption", "", "some words\nmore words", "some words"},
		{"the caption cut to the limit", "", long + "\ntail", strings.Repeat("a", YouTubeTitleMax)},
		{"nothing at all", "", "", "crier"},
		{"blank lines only", "", "\n\nwords below", "crier"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := newRecorder()
			base, _ := newFakeYouTube(t, rec)
			cfg := youtubeConfig(base, base)
			cfg.Publish.YouTube.Title = tt.title

			if _, err := onlyPublisher(t, cfg).Publish(context.Background(), Input{
				Artifact: videoArtifact(t, 8), Caption: tt.caption,
			}); err != nil {
				t.Fatal(err)
			}
			init, _ := findRequest(rec, "/upload/youtube/v3/videos")
			var body struct {
				Snippet struct {
					Title string `json:"title"`
				} `json:"snippet"`
			}
			if err := json.Unmarshal([]byte(init.Body), &body); err != nil {
				t.Fatal(err)
			}
			if body.Snippet.Title != tt.want {
				t.Errorf("title = %q, want %q", body.Snippet.Title, tt.want)
			}
		})
	}
}

// TestYouTubeStripsAngleBracketsFromTheTitle: YouTube refuses a title holding
// one, whatever it was meant to be, so a caption written with a "<v2>" in it
// would fail the upload for a reason nothing in the message explains.
func TestYouTubeStripsAngleBracketsFromTheTitle(t *testing.T) {
	rec := newRecorder()
	base, _ := newFakeYouTube(t, rec)
	cfg := youtubeConfig(base, base)
	cfg.Publish.YouTube.Title = "crier <v2> ships"

	if _, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 8)}); err != nil {
		t.Fatalf("the fake refuses a title with brackets, so this is the check: %v", err)
	}
	init, _ := findRequest(rec, "/upload/youtube/v3/videos")
	if !strings.Contains(init.Body, `"title":"crier v2 ships"`) {
		t.Errorf("title body = %q", init.Body)
	}
}

func TestYouTubeTitleHelper(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"  spaced  ", "spaced"},
		{"<script>", "script"},
		{">>>", ""},
		{strings.Repeat("b", YouTubeTitleMax+5), strings.Repeat("b", YouTubeTitleMax)},
		// Runes, not bytes: a hundred emoji are a hundred characters.
		{strings.Repeat("é", YouTubeTitleMax+2), strings.Repeat("é", YouTubeTitleMax)},
	} {
		if got := youtubeTitle(tt.in); got != tt.want {
			t.Errorf("youtubeTitle(%.20q) = %.20q, want %.20q", tt.in, got, tt.want)
		}
	}
}

// TestYouTubeSetsAThumbnail is the happy half of the optional step.
func TestYouTubeSetsAThumbnail(t *testing.T) {
	rec := newRecorder()
	base, _ := newFakeYouTube(t, rec)
	cover := artifact(t, render.KindImage, config.JPEG, "JPEGCOVER")

	cfg := youtubeConfig(base, base)
	cfg.Publish.YouTube.Thumbnail = cover.Path

	res, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 16)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Extra["thumbnail"] != cover.Path {
		t.Errorf("extra = %v, want the thumbnail reported", res.Extra)
	}
	set, ok := findRequest(rec, "/upload/youtube/v3/thumbnails/set")
	if !ok {
		t.Fatal("no thumbnail was set")
	}
	if !strings.Contains(set.Query, "videoId=vid-16") {
		t.Errorf("query = %q, want the video that was just uploaded", set.Query)
	}
	if set.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("content type = %q", set.Header.Get("Content-Type"))
	}
	if set.Body != "JPEGCOVER" {
		t.Errorf("body = %q", set.Body)
	}
}

// TestYouTubeThumbnailRefusalIsAWarning is the half that matters. A custom
// thumbnail needs a phone-verified channel and Google answers 403 for one that
// is not, and the video is already up: calling that a failed run would say the
// upload did not happen.
func TestYouTubeThumbnailRefusalIsAWarning(t *testing.T) {
	rec := newRecorder()
	base, f := newFakeYouTube(t, rec)
	f.thumbnailStatus = http.StatusForbidden
	cover := artifact(t, render.KindImage, config.PNG, "PNGCOVER")

	cfg := youtubeConfig(base, base)
	cfg.Publish.YouTube.Thumbnail = cover.Path

	res, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 32)})
	if err != nil {
		t.Fatalf("a refused thumbnail must not fail the post: %v", err)
	}
	if res.ID != "vid-32" {
		t.Errorf("id = %q; the video is still the result", res.ID)
	}
	if !strings.Contains(res.Extra["thumbnailError"], "phone-verified") {
		t.Errorf("extra = %v, want the reason reported alongside the success", res.Extra)
	}
	set, _ := findRequest(rec, "/upload/youtube/v3/thumbnails/set")
	if set.Header.Get("Content-Type") != "image/png" {
		t.Errorf("content type = %q; the extension picks it", set.Header.Get("Content-Type"))
	}
}

// TestYouTubeRefusesAThumbnailItCannotName: a file that is neither a JPEG nor a
// PNG is refused before it is uploaded, and still only as a warning.
func TestYouTubeRefusesAThumbnailItCannotName(t *testing.T) {
	rec := newRecorder()
	base, _ := newFakeYouTube(t, rec)
	cfg := youtubeConfig(base, base)
	cfg.Publish.YouTube.Thumbnail = "cover.webp"

	res, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 8)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Extra["thumbnailError"], "JPEG or a PNG") {
		t.Errorf("extra = %v", res.Extra)
	}
	if _, ok := findRequest(rec, "thumbnails/set"); ok {
		t.Error("a file youtube would refuse should not be uploaded to find that out")
	}
}

// TestYouTubeSurvivesAMissingLocation: without a session URL there is nowhere
// to send the bytes, and the message has to say that rather than fail on an
// empty URL.
func TestYouTubeSurvivesAMissingLocation(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"yt-access","expires_in":3600}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	_, err := onlyPublisher(t, youtubeConfig(srv.URL, srv.URL)).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 8)})
	if err == nil || !strings.Contains(err.Error(), "Location") {
		t.Fatalf("err = %v, want it to name the missing Location", err)
	}
}

// TestYouTubeReportsAnUploadWithNoID: a 200 that names no video is not a
// success, because the watch link would go to /watch?v=.
func TestYouTubeReportsAnUploadWithNoID(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"access_token":"yt-access","expires_in":3600}`))
		case r.Method == http.MethodPost:
			w.Header().Set("Location", "http://"+r.Host+"/session")
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{"snippet":{"title":"x"}}`))
		}
	})
	_, err := onlyPublisher(t, youtubeConfig(srv.URL, srv.URL)).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 8)})
	if err == nil || !strings.Contains(err.Error(), "no video id") {
		t.Fatalf("err = %v", err)
	}
}

// --- the token --------------------------------------------------------------

// TestYouTubeBadRefreshTokenIsNamed: Google's whole answer is "invalid_grant",
// so the message has to say which three values to look at.
func TestYouTubeBadRefreshTokenIsNamed(t *testing.T) {
	rec := newRecorder()
	base, _ := newFakeYouTube(t, rec)
	cfg := youtubeConfig(base, base)
	cfg.Publish.YouTube.RefreshToken = "bad-token"

	_, err := onlyPublisher(t, cfg).Publish(context.Background(),
		Input{Artifact: videoArtifact(t, 8)})
	if err == nil {
		t.Fatal("a revoked refresh token has to fail")
	}
	for _, want := range []string{"refresh", "client-id", "refresh-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	if _, ok := findRequest(rec, "/upload/"); ok {
		t.Error("nothing should have been uploaded without a token")
	}
}

// TestYouTubeTokenEndpointOddities: a 200 with no token in it, and a refusal
// carrying an OAuth error object. Neither is a token, and both have to say so.
func TestYouTubeTokenEndpointOddities(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{"no token in a 200", `{"expires_in":3600}`, "no access token"},
		{"an error object in a 200", `{"error":"invalid_client","error_description":"Unauthorized"}`,
			"invalid_client: Unauthorized"},
		{"an error with no reason", `{"error":"invalid_client"}`, "no reason given"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := onlyPublisher(t, youtubeConfig(srv.URL, srv.URL)).Ping(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestYouTubeReportsTheStepThatFailed: an initiate that is refused and an
// upload that is, each named for the step rather than for the status code.
func TestYouTubeReportsTheStepThatFailed(t *testing.T) {
	for _, tt := range []struct {
		name       string
		failUpload bool
		want       string
	}{
		{"initiate", false, "starting the youtube upload"},
		{"upload", true, "uploading the video"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/token":
					_, _ = w.Write([]byte(`{"access_token":"yt-access","expires_in":3600}`))
				case r.Method == http.MethodPost && !tt.failUpload:
					w.WriteHeader(http.StatusInternalServerError)
				case r.Method == http.MethodPost:
					w.Header().Set("Location", "http://"+r.Host+"/session")
					_, _ = w.Write([]byte(`{}`))
				default:
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			})
			_, err := onlyPublisher(t, youtubeConfig(srv.URL, srv.URL)).Publish(context.Background(),
				Input{Artifact: videoArtifact(t, 8)})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestYouTubePingReportsAChannelCallThatFails, so a token that refreshes and
// then cannot read the API is not reported as working credentials.
func TestYouTubePingReportsAChannelCallThatFails(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"yt-access","expires_in":3600}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"YouTube Data API v3 has not been used in project 1"}}`))
	})
	_, err := onlyPublisher(t, youtubeConfig(srv.URL, srv.URL)).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want the refusal", err)
	}
}

// TestYouTubeRefreshesOncePerRun: the token lasts an hour and a publish and a
// thumbnail are seconds apart, so asking twice is two round trips for nothing.
func TestYouTubeRefreshesOncePerRun(t *testing.T) {
	rec := newRecorder()
	base, f := newFakeYouTube(t, rec)
	cover := artifact(t, render.KindImage, config.JPEG, "COVER")
	cfg := youtubeConfig(base, base)
	cfg.Publish.YouTube.Thumbnail = cover.Path

	p := onlyPublisher(t, cfg)
	if _, err := p.Publish(context.Background(), Input{Artifact: videoArtifact(t, 8)}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.tokens != 1 {
		t.Errorf("the token was refreshed %d times across a publish and a ping, want once", f.tokens)
	}
	if n := countRequests(rec, "/token"); n != 1 {
		t.Errorf("%d calls reached the token endpoint", n)
	}
}

// --- ping -------------------------------------------------------------------

func TestYouTubePingNamesTheChannel(t *testing.T) {
	rec := newRecorder()
	base, _ := newFakeYouTube(t, rec)

	id, err := onlyPublisher(t, youtubeConfig(base, base)).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != youtubeChannel || id.Name != "Crier Releases" {
		t.Errorf("identity = %+v", id)
	}
	if id.Note != "" {
		t.Errorf("note = %q, want none for a channel that exists", id.Note)
	}
	if got := rec.paths(); len(got) != 2 ||
		got[0] != "POST /token" || got[1] != "GET /youtube/v3/channels" {
		t.Errorf("requests = %v", got)
	}
	// Nothing that uploads was touched.
	if _, ok := findRequest(rec, "/upload/"); ok {
		t.Error("ping uploaded something")
	}
}

// TestYouTubePingNotesAnAccountWithNoChannel: the token is good and there is
// nowhere for a video to go, which is how a YouTube setup passes every
// credential check and then fails at the upload.
func TestYouTubePingNotesAnAccountWithNoChannel(t *testing.T) {
	rec := newRecorder()
	base, f := newFakeYouTube(t, rec)
	f.noChanel = true

	id, err := onlyPublisher(t, youtubeConfig(base, base)).Ping(context.Background())
	if err != nil {
		t.Fatalf("a token without a channel is legal: %v", err)
	}
	if !strings.Contains(id.Note, "no youtube channel") {
		t.Errorf("note = %q", id.Note)
	}
}

func TestYouTubePingFailsOnABadRefreshToken(t *testing.T) {
	rec := newRecorder()
	base, _ := newFakeYouTube(t, rec)
	cfg := youtubeConfig(base, base)
	cfg.Publish.YouTube.RefreshToken = "bad-token"

	if _, err := onlyPublisher(t, cfg).Ping(context.Background()); err == nil {
		t.Fatal("ping has to fail on a token that cannot be refreshed")
	}
	if _, ok := findRequest(rec, "/youtube/v3/channels"); ok {
		t.Error("the channel was asked for without a token")
	}
}

// --- what it declares and what it refuses ------------------------------------

// TestYouTubeNeedsAndConstructor: video only, one file, bytes not a URL, and
// what it will not be built without.
func TestYouTubeNeedsAndConstructor(t *testing.T) {
	needs := onlyPublisher(t, youtubeConfig("https://api.example", "https://auth.example")).Needs()
	if needs.URL {
		t.Error("youtube takes the bytes; it needs no staging")
	}
	if !needs.Accepts(render.KindVideo) {
		t.Errorf("kinds = %v", needs.Kinds)
	}
	if needs.Accepts(render.KindImage) {
		t.Error("the Data API uploads videos; a community post has no public API")
	}
	if needs.Accepts(render.KindGIF) {
		t.Error("a GIF uploaded as a video would be a silent clip, not an animation")
	}
	// No image format is asked for, so an image-only run encodes nothing extra
	// on youtube's account.
	if len(needs.Formats) != 0 {
		t.Errorf("formats = %v, want none", needs.Formats)
	}
	if needs.Capacity() != YouTubeAttachmentMax {
		t.Errorf("capacity = %d, want %d", needs.Capacity(), YouTubeAttachmentMax)
	}

	for _, tt := range []struct {
		name  string
		build func(c *config.Config)
		want  string
	}{
		{"no api base url", func(c *config.Config) { c.Publish.YouTube.APIBaseURL = "" },
			"publish.youtube.api-base-url"},
		{"no auth base url", func(c *config.Config) { c.Publish.YouTube.AuthBaseURL = "" },
			"publish.youtube.auth-base-url"},
		{"no client id", func(c *config.Config) { c.Publish.YouTube.ClientID = "" },
			"publish.youtube.client-id"},
		{"no client secret", func(c *config.Config) { c.Publish.YouTube.ClientSecret = "" },
			"publish.youtube.client-secret"},
		{"no refresh token", func(c *config.Config) { c.Publish.YouTube.RefreshToken = "" },
			"publish.youtube.refresh-token"},
		{"a privacy it does not have", func(c *config.Config) { c.Publish.YouTube.PrivacyStatus = "friends" },
			"publish.youtube.privacy-status"},
		{"a carousel it does not have", func(c *config.Config) { c.Publish.YouTube.MaxAttachments = 4 },
			"publish.youtube.max-attachments"},
	} {
		cfg := youtubeConfig("https://api.example", "https://auth.example")
		tt.build(cfg)
		_, err := Build(cfg, testDeps(t))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want it to name %s", tt.name, err, tt.want)
		}
	}
}

// TestYouTubeValidationMatchesTheConstructor: the same two values are checked
// by config.Validate, so a bad one is reported by `crier config` as well as by
// the run that would have used it.
func TestYouTubeValidationMatchesTheConstructor(t *testing.T) {
	for key, set := range map[string]func(c *config.Config){
		"publish.youtube.privacy-status":  func(c *config.Config) { c.Publish.YouTube.PrivacyStatus = "friends" },
		"publish.youtube.max-attachments": func(c *config.Config) { c.Publish.YouTube.MaxAttachments = 2 },
		"publish.youtube.music-file":      func(c *config.Config) { c.Publish.YouTube.Music.File = "jingle.mp3" },
		"publish.youtube.lead-video":      func(c *config.Config) { c.Publish.YouTube.LeadVideo.File = "anthem.mp4" },
	} {
		cfg := youtubeConfig("https://api.example", "https://auth.example")
		set(cfg)
		err := config.Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: err = %v", key, err)
		}
	}
	// The defaults themselves have to pass, or nothing could be turned on.
	if err := config.Validate(youtubeConfig("https://api.example", "https://auth.example")); err != nil {
		t.Errorf("the youtube defaults do not validate: %v", err)
	}
}

// TestYouTubeIsNamedForItsConfigurationKey, which is what the report and every
// per-platform key are spelled with.
func TestYouTubeIsNamedForItsConfigurationKey(t *testing.T) {
	if got := onlyPublisher(t, youtubeConfig("https://a.example", "https://b.example")).Name(); got != "youtube" {
		t.Errorf("name = %q", got)
	}
}

// countRequests is how many recorded requests touched a path fragment.
func countRequests(r *recorder, fragment string) int {
	n := 0
	for _, req := range r.all() {
		if strings.Contains(req.Path, fragment) {
			n++
		}
	}
	return n
}
