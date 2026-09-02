//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakes is one server standing in for every platform crier can post to.
//
// One server rather than thirteen because every base URL is configurable: the
// contract each platform's fake enforces is the point, not the host it lives
// on.
type fakes struct {
	*httptest.Server

	mu       sync.Mutex
	requests []request
	// s3 is where Reddit's media lease points.
	uploadHost string
	// vkPhotos numbers VK's photo uploads, so the blob a save call forwards
	// can be traced back to the upload that produced it.
	vkPhotos int
	// threadsContainers is every Threads container this fake minted, keyed by
	// the id it handed out, so a publish or a carousel can be checked against
	// what was actually created rather than against a string.
	threadsContainers map[string]url.Values
	threadsPosts      int
	// youtubeSessions is every resumable upload session this fake opened, keyed
	// by the id in the Location it handed out, so a PUT can be checked against
	// a session that exists rather than against a string.
	youtubeSessions map[string]string
}

type request struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

func newFakes(t *testing.T) *fakes {
	t.Helper()
	f := &fakes{
		threadsContainers: map[string]url.Values{},
		youtubeSessions:   map[string]string{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	f.uploadHost = f.URL
	t.Cleanup(f.Close)
	return f
}

func (f *fakes) record(r *http.Request) request {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	req := request{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Header: r.Header.Clone(), Body: string(body),
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	return req
}

// all returns every request the fakes received.
func (f *fakes) all() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]request(nil), f.requests...)
}

// find returns the first request whose path contains the fragment.
func (f *fakes) find(fragment string) (request, bool) {
	for _, r := range f.all() {
		if strings.Contains(r.Path, fragment) {
			return r, true
		}
	}
	return request{}, false
}

// count is how many requests touched a path fragment.
func (f *fakes) count(fragment string) int {
	n := 0
	for _, r := range f.all() {
		if strings.Contains(r.Path, fragment) {
			n++
		}
	}
	return n
}

// serve routes by path prefix. Every platform gets a namespace of its own so
// the config can point fourteen base URLs at one server: thirteen platforms,
// and YouTube's second host for its token.
func (f *fakes) serve(w http.ResponseWriter, r *http.Request) {
	req := f.record(r)
	path := req.Path

	// A token spelled "bad-token" is refused everywhere, which is how the
	// one-bad-credential scenario is written without a second server.
	if strings.Contains(req.Header.Get("Authorization"), "bad-token") || strings.Contains(path, "bad-token") {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"ok": false, "description": "Unauthorized", "error": "invalid_grant"})
		return
	}

	switch {
	// --- identity, for crier ping ---------------------------------------
	//
	// One read-only endpoint per platform, which is the whole of what ping
	// touches. They come first so a prefix meant for the publish flow cannot
	// swallow one of them — /tiktok/v2/post/publish/ would otherwise claim
	// creator_info/query/.
	case strings.HasSuffix(path, "/getMe"):
		writeJSON(w, map[string]any{"ok": true, "result": map[string]any{
			"id": 42, "username": "crier_bot", "first_name": "Crier",
		}})
	case path == "/discord/webhook" && r.Method == http.MethodGet:
		writeJSON(w, map[string]any{"id": "d-1", "name": "crier hook", "channel_id": "c-1"})
	case strings.HasPrefix(path, "/mastodon/api/v1/accounts/verify_credentials"):
		writeJSON(w, map[string]any{"id": "ma-1", "username": "crier", "acct": "crier@example.social"})
	case strings.HasPrefix(path, "/x/2/users/me"):
		writeJSON(w, map[string]any{"data": map[string]any{
			"id": "x-1", "name": "Crier", "username": "criertest",
		}})
	case path == "/instagram/ig-user":
		writeJSON(w, map[string]any{"id": "ig-user", "username": "crier_ig"})
	case path == "/facebook/fb-page":
		writeJSON(w, map[string]any{"id": "fb-page", "name": "Crier Page"})
	case strings.HasPrefix(path, "/tiktok/v2/post/publish/creator_info/query/"):
		writeJSON(w, map[string]any{"data": map[string]any{
			"creator_nickname": "Crier", "creator_username": "criertt",
		}})
	case strings.HasPrefix(path, "/linkedin/v2/userinfo"):
		writeJSON(w, map[string]any{"sub": "li-1", "name": "Crier Member"})
	case strings.HasPrefix(path, "/reddit/api/v1/me"):
		writeJSON(w, map[string]any{"id": "rd-1", "name": "crierbot"})
	case strings.HasPrefix(path, "/slack/auth.test"):
		writeJSON(w, map[string]any{
			"ok": true, "url": "https://acme.slack.com/", "team": "Acme",
			"user": "crier", "team_id": "T-E2E", "user_id": "U-E2E", "bot_id": "B-E2E",
		})

	// --- telegram -------------------------------------------------------
	//
	// sendMediaGroup answers with one message per item, which is the shape
	// that differs from every other Bot API method.
	case strings.Contains(path, "/sendMediaGroup"):
		n := strings.Count(req.Body, `"type":"photo"`)
		result := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			result = append(result, map[string]any{
				"message_id": 2000 + i,
				"chat":       map[string]any{"id": 5, "username": "criertest"},
			})
		}
		writeJSON(w, map[string]any{"ok": true, "result": result})
	// The audio message, which is its own message because the Bot API groups
	// audio only with audio. Its id is distinct so a test can tell it apart
	// from the post it follows.
	case strings.Contains(path, "/sendAudio"):
		writeJSON(w, map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 3001,
				"chat":       map[string]any{"id": 5, "username": "criertest"},
			},
		})
	case strings.Contains(path, "/sendPhoto"), strings.Contains(path, "/sendVideo"),
		strings.Contains(path, "/sendAnimation"):
		writeJSON(w, map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 1001,
				"chat":       map[string]any{"id": 5, "username": "criertest"},
			},
		})

	// --- discord --------------------------------------------------------
	case strings.HasPrefix(path, "/discord/"):
		writeJSON(w, map[string]any{"id": "d-1", "channel_id": "c-1"})

	// --- mastodon -------------------------------------------------------
	case strings.HasPrefix(path, "/mastodon/api/v2/media"):
		writeJSON(w, map[string]any{"id": "m-1", "url": "https://cdn.example/m-1"})
	case strings.HasPrefix(path, "/mastodon/api/v1/statuses"):
		writeJSON(w, map[string]any{"id": "s-1", "url": "https://mastodon.example/@crier/s-1"})

	// --- x --------------------------------------------------------------
	case strings.HasPrefix(path, "/x/2/media/upload"):
		if strings.Contains(req.Body, "INIT") {
			writeJSON(w, map[string]any{"data": map[string]any{"id": "xm-1"}})
			return
		}
		if strings.Contains(req.Body, "APPEND") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.Contains(req.Body, "FINALIZE") || r.URL.Query().Get("command") == "STATUS" {
			writeJSON(w, map[string]any{"data": map[string]any{
				"id":              "xm-1",
				"processing_info": map[string]any{"state": "succeeded"},
			}})
			return
		}
		writeJSON(w, map[string]any{"data": map[string]any{"id": "xm-1"}})
	case strings.HasPrefix(path, "/x/2/tweets"):
		writeJSON(w, map[string]any{"data": map[string]any{"id": "t-1"}})

	// --- instagram ------------------------------------------------------
	case strings.HasPrefix(path, "/instagram/") && strings.HasSuffix(path, "/media"):
		writeJSON(w, map[string]any{"id": "ig-container-1"})
	case strings.HasPrefix(path, "/instagram/") && strings.HasSuffix(path, "/media_publish"):
		writeJSON(w, map[string]any{"id": "ig-post-1"})
	case strings.HasPrefix(path, "/instagram/ig-container-1"):
		writeJSON(w, map[string]any{"status_code": "FINISHED"})
	case strings.HasPrefix(path, "/instagram/ig-post-1"):
		// The permalink lookup: the media id is not the shortcode.
		writeJSON(w, map[string]any{"permalink": "https://www.instagram.com/p/CxE2E123/"})

	// --- facebook -------------------------------------------------------
	case strings.HasPrefix(path, "/facebook/") && strings.HasSuffix(path, "/photos"):
		writeJSON(w, map[string]any{"id": "fb-photo-1", "post_id": "fb-post-1"})
	case strings.HasPrefix(path, "/facebook/") && strings.HasSuffix(path, "/videos"):
		writeJSON(w, map[string]any{"id": "fb-video-1", "post_id": "fb-post-1"})
	case strings.HasPrefix(path, "/facebook/") && strings.HasSuffix(path, "/photo_stories"):
		writeJSON(w, map[string]any{"success": true, "post_id": "fb-story-1"})

	// --- tiktok ---------------------------------------------------------
	case strings.HasPrefix(path, "/tiktok/v2/post/publish/status/fetch/"):
		writeJSON(w, map[string]any{"data": map[string]any{"status": "PUBLISH_COMPLETE"}})
	case strings.HasPrefix(path, "/tiktok/v2/post/publish/"):
		writeJSON(w, map[string]any{"data": map[string]any{
			"publish_id": "tt-1",
			"upload_url": f.uploadHost + "/tiktok-upload",
		}})
	case strings.HasPrefix(path, "/tiktok-upload"):
		w.WriteHeader(http.StatusCreated)

	// --- linkedin -------------------------------------------------------
	case strings.HasPrefix(path, "/linkedin/rest/images/"):
		// The image status poll the upload waits on before posting.
		writeJSON(w, map[string]any{"status": "AVAILABLE"})
	case strings.HasPrefix(path, "/linkedin/rest/images"):
		writeJSON(w, map[string]any{"value": map[string]any{
			"uploadUrl": f.uploadHost + "/linkedin-upload",
			"image":     "urn:li:image:1",
		}})
	case strings.HasPrefix(path, "/linkedin/rest/videos"):
		switch r.URL.Query().Get("action") {
		case "initializeUpload":
			writeJSON(w, map[string]any{"value": map[string]any{
				"video":       "urn:li:video:1",
				"uploadToken": "tk",
				"uploadInstructions": []map[string]any{
					{"uploadUrl": f.uploadHost + "/linkedin-part-0", "firstByte": 0, "lastByte": 7},
				},
			}})
		case "finalizeUpload":
			w.WriteHeader(http.StatusOK)
		default:
			writeJSON(w, map[string]any{"status": "AVAILABLE"})
		}
	case strings.HasPrefix(path, "/linkedin-upload"):
		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(path, "/linkedin-part-"):
		w.Header().Set("ETag", `"etag-`+strings.TrimPrefix(path, "/linkedin-part-")+`"`)
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(path, "/linkedin/rest/posts"):
		w.Header().Set("x-restli-id", "urn:li:share:1")
		w.WriteHeader(http.StatusCreated)

	// --- slack ----------------------------------------------------------
	//
	// The three-step external upload: a URL is handed out, the bytes go to it
	// unauthenticated, and a third call says what the file is for.
	case strings.HasPrefix(path, "/slack/files.getUploadURLExternal"):
		writeJSON(w, map[string]any{
			"ok":         true,
			"upload_url": f.uploadHost + "/slack-upload?t=e2e",
			"file_id":    "F-E2E",
		})
	case strings.HasPrefix(path, "/slack-upload"):
		_, _ = io.WriteString(w, "OK")
	case strings.HasPrefix(path, "/slack/files.completeUploadExternal"):
		writeJSON(w, map[string]any{
			"ok":    true,
			"files": []map[string]any{{"id": "F-E2E", "title": "card"}},
		})

	// --- reddit ---------------------------------------------------------
	case strings.HasPrefix(path, "/reddit-auth/api/v1/access_token"):
		writeJSON(w, map[string]any{"access_token": "reddit-token", "expires_in": 3600})
	case strings.HasPrefix(path, "/reddit/api/media/asset.json"):
		writeJSON(w, map[string]any{
			"args": map[string]any{
				"action": f.uploadHost + "/reddit-store",
				"fields": []map[string]string{
					{"name": "acl", "value": "public-read"},
					{"name": "key", "value": "media/e2e.jpg"},
				},
			},
			"asset": map[string]any{"asset_id": "asset-1"},
		})
	case strings.HasPrefix(path, "/reddit-store"):
		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(path, "/reddit/api/submit"):
		writeJSON(w, map[string]any{"json": map[string]any{"errors": []any{}, "data": map[string]any{}}})
	case strings.HasPrefix(path, "/reddit/user/"):
		writeJSON(w, map[string]any{"data": map[string]any{"children": []map[string]any{
			{"data": map[string]any{
				"id": "abc", "name": "t3_abc", "title": "",
				"permalink": "/r/crier/comments/abc/e2e/",
			}},
		}}})

	// --- vk ---------------------------------------------------------------
	//
	// Every method is a POST form under /method/, and every one answers 200
	// whether it worked or not: a failure is an "error" object in the body.
	// The upload servers are separate paths because VK's are a separate host,
	// and they carry no token.
	case strings.HasPrefix(path, "/vk/method/"):
		f.serveVK(w, req)
	case strings.HasPrefix(path, "/vk-photo-upload"):
		n := f.nextVKPhoto()
		writeJSON(w, map[string]any{
			"server": 42,
			"photo":  fmt.Sprintf("BLOB-%d", n),
			"hash":   fmt.Sprintf("HASH-%d", n),
		})
	case strings.HasPrefix(path, "/vk-doc-upload"):
		writeJSON(w, map[string]any{"file": "DOCFILE-1"})
	case strings.HasPrefix(path, "/vk-video-upload"):
		writeJSON(w, map[string]any{"size": 1024, "video_id": 77})

	// --- threads ----------------------------------------------------------
	//
	// Instagram's shape on another host: a container, a status poll, a publish
	// that names the container. One prefix covers the identity endpoint too,
	// because the token travels in the query there and the shared bad-token
	// rule at the top cannot see it.
	case strings.HasPrefix(path, "/threads/"):
		f.serveThreads(w, req)

	// --- youtube ----------------------------------------------------------
	//
	// Two hosts, like Reddit: the token comes from the auth namespace and
	// everything else from the API one. The upload session is a third path
	// because Google's is a third host.
	case strings.HasPrefix(path, "/youtube-auth/token"):
		f.youtubeToken(w, req)
	case strings.HasPrefix(path, "/youtube/"), strings.HasPrefix(path, "/youtube-upload/"):
		f.serveYouTube(w, req)

	default:
		http.Error(w, "no fake for "+path, http.StatusNotFound)
	}
}

// vkOwner is the community wall the fake posts on. Negative, because that is
// what makes it a community.
const vkOwner = -123

// nextVKPhoto numbers one photo upload.
func (f *fakes) nextVKPhoto() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vkPhotos++
	return f.vkPhotos
}

// serveVK is the method dispatch, and the binding contract for the publisher.
//
// The linkage is the part worth enforcing rather than merely answering:
// saveWallPhoto only produces an id when the server, photo and hash it was
// given belong to the same upload, and the id it produces is derived from the
// blob — so a post whose attachments do not match its uploads shows up as a
// wrong attachment string rather than as a passing test.
func (f *fakes) serveVK(w http.ResponseWriter, req request) {
	method := strings.TrimPrefix(req.Path, "/vk/method/")
	form, _ := url.ParseQuery(req.Body)

	// The token travels in the body here, not in a header, so the shared
	// bad-token rule at the top of serve cannot see it.
	if form.Get("access_token") == "bad-token" {
		writeJSON(w, map[string]any{"error": map[string]any{
			"error_code": 5, "error_msg": "User authorization failed: invalid access_token.",
		}})
		return
	}

	switch method {
	case "photos.getWallUploadServer":
		writeJSON(w, map[string]any{"response": map[string]any{
			"upload_url": f.uploadHost + "/vk-photo-upload",
		}})
	case "photos.saveWallPhoto":
		n := strings.TrimPrefix(form.Get("photo"), "BLOB-")
		if n == form.Get("photo") || form.Get("hash") != "HASH-"+n || form.Get("server") != "42" {
			writeJSON(w, map[string]any{"error": map[string]any{
				"error_code": 100,
				"error_msg": fmt.Sprintf("the save call carried server=%q photo=%q hash=%q, "+
					"which did not come from one upload",
					form.Get("server"), form.Get("photo"), form.Get("hash")),
			}})
			return
		}
		id, _ := strconv.Atoi(n)
		writeJSON(w, map[string]any{"response": []map[string]any{
			{"id": 1000 + id, "owner_id": vkOwner},
		}})
	case "video.save":
		writeJSON(w, map[string]any{"response": map[string]any{
			"upload_url": f.uploadHost + "/vk-video-upload",
			"owner_id":   vkOwner,
			"video_id":   77,
		}})
	case "docs.getWallUploadServer":
		writeJSON(w, map[string]any{"response": map[string]any{
			"upload_url": f.uploadHost + "/vk-doc-upload",
		}})
	case "docs.save":
		if form.Get("file") != "DOCFILE-1" {
			writeJSON(w, map[string]any{"error": map[string]any{
				"error_code": 100, "error_msg": "docs.save did not carry what the upload server returned",
			}})
			return
		}
		writeJSON(w, map[string]any{"response": map[string]any{
			"type": "doc",
			"doc":  map[string]any{"id": 55, "owner_id": vkOwner},
		}})
	case "wall.post":
		writeJSON(w, map[string]any{"response": map[string]any{"post_id": 9001}})
	case "groups.getById":
		writeJSON(w, map[string]any{"response": []map[string]any{
			{"id": -vkOwner, "name": "Crier Community", "screen_name": "crierhq"},
		}})
	case "users.get":
		writeJSON(w, map[string]any{"response": []map[string]any{
			{"id": 777, "first_name": "Crier", "last_name": "Bot"},
		}})
	default:
		writeJSON(w, map[string]any{"error": map[string]any{
			"error_code": 3, "error_msg": "Unknown method passed: " + method,
		}})
	}
}

// threadsUser is the account the Threads fake posts as.
const threadsUser = "th-user"

// serveThreads is the whole Threads surface, and the binding contract for the
// publisher.
//
// The linkage is what is enforced rather than merely answered. A container's id
// is minted here, threads_publish only works when it names one of them, a
// CAROUSEL parent only works when every child it lists was created as a
// carousel item, and a carousel of fewer than two children is refused the way
// the real API refuses it. So a post assembled out of the wrong pieces shows up
// as a failed run rather than as a passing test.
func (f *fakes) serveThreads(w http.ResponseWriter, req request) {
	rest := strings.TrimPrefix(req.Path, "/threads/")
	form, _ := url.ParseQuery(req.Body)
	query, _ := url.ParseQuery(req.Query)

	// The token travels in the body on a POST and in the query on a GET, so
	// neither reaches the header the shared bad-token rule reads.
	token := form.Get("access_token")
	if token == "" {
		token = query.Get("access_token")
	}
	if token == "bad-token" {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": map[string]any{
			"message": "Invalid OAuth access token.", "type": "OAuthException", "code": 190,
		}})
		return
	}

	switch {
	case rest == "me":
		writeJSON(w, map[string]any{"id": threadsUser, "username": "crier_threads"})
	case rest == threadsUser+"/threads":
		f.threadsContainer(w, form)
	case rest == threadsUser+"/threads_publish":
		f.threadsPublish(w, form)
	case strings.HasPrefix(rest, "th-c"):
		f.mu.Lock()
		_, ok := f.threadsContainers[rest]
		f.mu.Unlock()
		if !ok {
			threadsRefuse(w, "container "+rest+" was never created")
			return
		}
		writeJSON(w, map[string]any{"status": "FINISHED"})
	case strings.HasPrefix(rest, "th-post-"):
		writeJSON(w, map[string]any{
			"permalink": "https://www.threads.net/@crier_threads/post/" +
				strings.TrimPrefix(rest, "th-post-"),
		})
	default:
		http.Error(w, "no threads fake for "+req.Path, http.StatusNotFound)
	}
}

// threadsContainer mints one container, after checking it is a shape Threads
// would accept.
func (f *fakes) threadsContainer(w http.ResponseWriter, form url.Values) {
	switch form.Get("media_type") {
	case "IMAGE":
		if form.Get("image_url") == "" {
			threadsRefuse(w, "an IMAGE container with no image_url")
			return
		}
	case "VIDEO":
		if form.Get("video_url") == "" {
			threadsRefuse(w, "a VIDEO container with no video_url")
			return
		}
	case "CAROUSEL":
		children := strings.Split(form.Get("children"), ",")
		if len(children) < 2 {
			threadsRefuse(w, fmt.Sprintf("a carousel of %d children; threads takes at least two",
				len(children)))
			return
		}
		f.mu.Lock()
		for _, id := range children {
			child, ok := f.threadsContainers[id]
			if !ok {
				f.mu.Unlock()
				threadsRefuse(w, "the carousel names container "+id+", which was never created")
				return
			}
			if child.Get("is_carousel_item") != "true" {
				f.mu.Unlock()
				threadsRefuse(w, "the carousel names container "+id+", which is not a carousel item")
				return
			}
		}
		f.mu.Unlock()
	default:
		threadsRefuse(w, "media_type "+form.Get("media_type"))
		return
	}

	f.mu.Lock()
	id := fmt.Sprintf("th-c%d", len(f.threadsContainers)+1)
	f.threadsContainers[id] = form
	f.mu.Unlock()
	writeJSON(w, map[string]any{"id": id})
}

// threadsPublish turns a container into a post, and only a container.
func (f *fakes) threadsPublish(w http.ResponseWriter, form url.Values) {
	f.mu.Lock()
	container, ok := f.threadsContainers[form.Get("creation_id")]
	f.mu.Unlock()
	if !ok {
		threadsRefuse(w, "creation_id "+form.Get("creation_id")+" names no container")
		return
	}
	if container.Get("is_carousel_item") == "true" {
		threadsRefuse(w, "a carousel child cannot be published on its own")
		return
	}
	f.mu.Lock()
	f.threadsPosts++
	n := f.threadsPosts
	f.mu.Unlock()
	writeJSON(w, map[string]any{"id": fmt.Sprintf("th-post-%d", n)})
}

// youtubeChannel is the channel this fake uploads to.
const youtubeChannel = "UC-e2e"

// youtubeToken is the OAuth refresh, which is the one YouTube call that carries
// no bearer: the credentials travel in the form body, so the shared bad-token
// rule at the top of serve cannot see them.
func (f *fakes) youtubeToken(w http.ResponseWriter, req request) {
	form, _ := url.ParseQuery(req.Body)
	if form.Get("refresh_token") == "bad-token" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{
			"error": "invalid_grant", "error_description": "Token has been expired or revoked.",
		})
		return
	}
	if form.Get("grant_type") != "refresh_token" ||
		form.Get("client_id") == "" || form.Get("client_secret") == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{
			"error": "invalid_request", "error_description": "the refresh needs all four fields",
		})
		return
	}
	writeJSON(w, map[string]any{"access_token": "yt-access", "expires_in": 3600})
}

// serveYouTube is the Data API surface, and the binding contract for the
// publisher.
//
// Two things are enforced rather than merely answered. The bytes have to arrive
// at a session this fake opened, so an upload sent to the API host instead of
// to the Location is a refusal; and the video id is derived from the byte count
// received, so a truncated upload comes out as a different id rather than as a
// passing test.
func (f *fakes) serveYouTube(w http.ResponseWriter, req request) {
	if req.Header.Get("Authorization") != "Bearer yt-access" {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": map[string]any{
			"code": 401, "message": "the call carried " + req.Header.Get("Authorization"),
		}})
		return
	}
	query, _ := url.ParseQuery(req.Query)

	switch {
	case req.Path == "/youtube/youtube/v3/channels":
		writeJSON(w, map[string]any{"items": []map[string]any{
			{"id": youtubeChannel, "snippet": map[string]any{"title": "Crier Releases"}},
		}})

	case req.Path == "/youtube/upload/youtube/v3/videos":
		if query.Get("uploadType") != "resumable" || query.Get("part") != "snippet,status" {
			youtubeRefuse(w, "uploadType="+query.Get("uploadType")+" part="+query.Get("part"))
			return
		}
		var body struct {
			Snippet struct {
				Title string `json:"title"`
			} `json:"snippet"`
		}
		if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
			youtubeRefuse(w, "the initiate body is not JSON: "+err.Error())
			return
		}
		if strings.TrimSpace(body.Snippet.Title) == "" {
			youtubeRefuse(w, "a video needs a title")
			return
		}
		f.mu.Lock()
		id := fmt.Sprintf("s%d", len(f.youtubeSessions)+1)
		f.youtubeSessions[id] = body.Snippet.Title
		f.mu.Unlock()
		w.Header().Set("Location", f.uploadHost+"/youtube-upload/"+id+"?upload_id="+id)
		writeJSON(w, map[string]any{})

	case strings.HasPrefix(req.Path, "/youtube-upload/"):
		session := strings.TrimPrefix(req.Path, "/youtube-upload/")
		f.mu.Lock()
		_, ok := f.youtubeSessions[session]
		f.mu.Unlock()
		if !ok {
			youtubeRefuse(w, "no such upload session: "+session)
			return
		}
		// The id says how many bytes arrived, so a short upload is visible.
		writeJSON(w, map[string]any{"id": fmt.Sprintf("yt-%d", len(req.Body))})

	case req.Path == "/youtube/upload/youtube/v3/thumbnails/set":
		if query.Get("videoId") == "" || query.Get("uploadType") != "media" {
			youtubeRefuse(w, "a thumbnail needs videoId and uploadType=media")
			return
		}
		writeJSON(w, map[string]any{"items": []map[string]any{
			{"default": map[string]any{"url": "https://i.ytimg.com/e2e.jpg"}},
		}})

	default:
		http.Error(w, "no youtube fake for "+req.Path, http.StatusNotFound)
	}
}

func youtubeRefuse(w http.ResponseWriter, why string) {
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]any{"error": map[string]any{"code": 400, "message": why}})
}

// youtubeConfig is the YouTube block, kept out of platformConfig on purpose.
//
// YouTube takes no images, so it cannot join the all-platform image fan-out the
// way the other twelve do: a run that enabled it there would be refused before
// anything was staged, which is the right behaviour and the wrong test. It has
// a video scenario of its own instead, and joins the ping fan-out through this.
func (f *fakes) youtubeConfig() string {
	return fmt.Sprintf(`  youtube:
    enabled: true
    api-base-url: %[1]s/youtube
    auth-base-url: %[1]s/youtube-auth
    client-id: yt-client
    client-secret: yt-secret
    refresh-token: yt-refresh
`, f.URL)
}

func threadsRefuse(w http.ResponseWriter, why string) {
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]any{"error": map[string]any{"message": why, "code": 100}})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// platformConfig is the YAML that points every platform at the fakes.
func (f *fakes) platformConfig() string {
	return fmt.Sprintf(`  instagram:
    api-base-url: %[1]s/instagram
    token: ig-token
    user-id: ig-user
    poll-interval: 1ms
    poll-timeout: 5s
  facebook:
    api-base-url: %[1]s/facebook
    token: fb-token
    page-id: fb-page
  tiktok:
    api-base-url: %[1]s/tiktok
    token: tt-token
    poll-interval: 1ms
    poll-timeout: 5s
  telegram:
    api-base-url: %[1]s
    token: tg-token
    chat-id: "@crier"
  x:
    api-base-url: %[1]s/x
    token: x-token
  mastodon:
    api-base-url: %[1]s/mastodon
    token: ma-token
    poll-interval: 1ms
    poll-timeout: 5s
  discord:
    webhook-url: %[1]s/discord/webhook
  linkedin:
    api-base-url: %[1]s/linkedin
    token: li-token
    author-urn: "urn:li:person:e2e"
  slack:
    api-base-url: %[1]s/slack
    token: xoxb-e2e
    channel: C-E2E
  vk:
    api-base-url: %[1]s/vk
    token: vk-token
    owner-id: -123
  threads:
    api-base-url: %[1]s/threads
    token: threads-token
    user-id: th-user
    poll-interval: 1ms
    poll-timeout: 5s
  reddit:
    api-base-url: %[1]s/reddit
    auth-base-url: %[1]s/reddit-auth
    client-id: cid
    client-secret: csec
    username: crierbot
    password: pw
    subreddit: crier
    poll-interval: 1ms
    poll-timeout: 200ms
`, f.URL)
}

// findAll returns every request whose path contains the fragment, in the order
// they arrived. It is what an ordering assertion is made of.
func (f *fakes) findAll(fragment string) []request {
	var out []request
	for _, r := range f.all() {
		if strings.Contains(r.Path, fragment) {
			out = append(out, r)
		}
	}
	return out
}
