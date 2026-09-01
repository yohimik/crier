//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakes is one server standing in for every platform crier can post to.
//
// One server rather than nine because every base URL is configurable: the
// contract each platform's fake enforces is the point, not the host it lives
// on.
type fakes struct {
	*httptest.Server

	mu       sync.Mutex
	requests []request
	// s3 is where Reddit's media lease points.
	uploadHost string
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
	f := &fakes{}
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
// the config can point nine base URLs at one server.
func (f *fakes) serve(w http.ResponseWriter, r *http.Request) {
	req := f.record(r)
	path := req.Path

	switch {
	// --- telegram -------------------------------------------------------
	case strings.Contains(path, "/sendPhoto"), strings.Contains(path, "/sendVideo"):
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

	default:
		http.Error(w, "no fake for "+path, http.StatusNotFound)
	}
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
