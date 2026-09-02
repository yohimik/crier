package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

// boostyBlogName is the blog every fake in this file posts in.
const boostyBlogName = "crierhq"

func boostyConfig(api, upload string) *config.Config {
	cfg := config.Defaults()
	cfg.Publish.Boosty.Enabled = true
	cfg.Publish.Boosty.APIBaseURL = api
	cfg.Publish.Boosty.UploadBaseURL = upload
	cfg.Publish.Boosty.Blog = boostyBlogName
	cfg.Publish.Boosty.AccessToken = "access-1"
	return &cfg
}

// fakeBoosty is the undocumented API as the publisher has to speak it, and the
// contract this package pins.
//
// It enforces the linkage rather than only answering. A file id is minted by
// the slot call, the parts have to arrive at that id carrying an X-PartNumber,
// completion has to follow the parts, and the post is refused unless every
// image block names a file that was completed here. So a post assembled out of
// the wrong pieces shows up as a refusal rather than as a passing test.
//
// The bearer is checked on every call, and which token counts as good can be
// changed mid-test, which is what makes the expiry-and-refresh path testable
// without a clock.
type fakeBoosty struct {
	t *testing.T

	mu sync.Mutex
	// files maps a minted file id to the bytes its parts carried.
	files map[string]int
	// done is the file ids completion was called on.
	done map[string]bool
	// parts records the X-PartNumber of every part, per file, in arrival order.
	parts map[string][]string
	// token is the access token calls have to carry now.
	token string
	// refreshes counts the token endpoint's calls.
	refreshes int
	// newTokens is what a refresh hands out. Empty values are left out of the
	// answer, which is how a refusal is written.
	newAccess, newRefresh string
	// refreshStatus is what the token endpoint answers with. 200 by default.
	refreshStatus int
	// blog is what the identity call reports.
	blog map[string]any
}

func newFakeBoosty(t *testing.T, rec *recorder) (api, upload string, f *fakeBoosty) {
	t.Helper()
	f = &fakeBoosty{
		t:          t,
		files:      map[string]int{},
		done:       map[string]bool{},
		parts:      map[string][]string{},
		token:      "access-1",
		newAccess:  "access-2",
		newRefresh: "refresh-2",
		blog: map[string]any{
			"title": "Crier HQ", "blogUrl": boostyBlogName, "isOwner": true,
			"owner":        map[string]any{"id": 4242, "name": "Crier"},
			"accessRights": map[string]any{"canCreate": true},
		},
	}
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		f.serve(w, rec.record(r))
	})
	return srv.URL + "/api", srv.URL + "/up", f
}

func (f *fakeBoosty) serve(w http.ResponseWriter, req recorded) {
	w.Header().Set("Content-Type", "application/json")

	// The refresh is the one call that carries no bearer: the credentials are
	// in the form body, so the token check below cannot see them.
	if req.Path == "/api/oauth/token/" {
		f.refresh(w, req)
		return
	}
	if req.Header.Get("Authorization") != "Bearer "+f.current() {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"unauthorized","error_description":"the call carried %q"}`,
			req.Header.Get("Authorization"))
		return
	}
	if req.Header.Get("X-App") != "web" {
		f.t.Errorf("%s carried X-App=%q", req.Path, req.Header.Get("X-App"))
	}

	switch {
	case req.Path == "/up/image":
		f.slot(w, req)
	case strings.HasSuffix(req.Path, "/complete"):
		f.complete(w, strings.TrimSuffix(strings.TrimPrefix(req.Path, "/up/upload/"), "/complete"))
	case strings.HasPrefix(req.Path, "/up/upload/"):
		f.part(w, strings.TrimPrefix(req.Path, "/up/upload/"), req)
	case req.Path == "/api/v1/blog/"+boostyBlogName+"/post/":
		f.create(w, req)
	case req.Path == "/api/v1/blog/"+boostyBlogName:
		writeBoostyJSON(w, f.blog)
	default:
		http.Error(w, "no boosty fake for "+req.Path, http.StatusNotFound)
	}
}

func (f *fakeBoosty) current() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.token
}

// expire makes the token calls are carrying no longer the good one, which is
// how an access token that ran out is written without waiting an hour.
func (f *fakeBoosty) expire(next string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token = next
}

func (f *fakeBoosty) refresh(w http.ResponseWriter, req recorded) {
	form, err := url.ParseQuery(req.Body)
	if err != nil {
		f.t.Errorf("the refresh body is not a form: %q", req.Body)
	}
	f.mu.Lock()
	f.refreshes++
	status, access, refresh := f.refreshStatus, f.newAccess, f.newRefresh
	f.mu.Unlock()

	for k, want := range map[string]string{"grant_type": "refresh_token", "device_os": "web"} {
		if form.Get(k) != want {
			f.t.Errorf("the refresh sent %s=%q, want %q", k, form.Get(k), want)
		}
	}
	for _, k := range []string{"device_id", "refresh_token"} {
		if form.Get(k) == "" {
			f.t.Errorf("the refresh sent no %s", k)
		}
	}
	if status != 0 && status != http.StatusOK {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	out := map[string]any{"expires_in": 3600}
	if access != "" {
		out["access_token"] = access
		f.expire(access)
	}
	if refresh != "" {
		out["refresh_token"] = refresh
	}
	writeBoostyJSON(w, out)
}

func (f *fakeBoosty) slot(w http.ResponseWriter, req recorded) {
	if strings.TrimSpace(req.Body) != "{}" {
		f.t.Errorf("the slot call sent %q, want an empty JSON object", req.Body)
	}
	f.mu.Lock()
	id := fmt.Sprintf("file-%d", len(f.files)+1)
	f.files[id] = 0
	f.mu.Unlock()
	writeBoostyJSON(w, map[string]any{"fileId": id})
}

func (f *fakeBoosty) part(w http.ResponseWriter, id string, req recorded) {
	part := req.Header.Get("X-PartNumber")
	if part == "" {
		// The real host answers 400 without it, and a publisher that forgot it
		// would otherwise upload nothing and notice nothing.
		http.Error(w, `{"error":"a part needs X-PartNumber"}`, http.StatusBadRequest)
		return
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/octet-stream" {
		f.t.Errorf("part %s of %s carried Content-Type %q", part, id, ct)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[id]; !ok {
		http.Error(w, `{"error":"no such upload"}`, http.StatusNotFound)
		return
	}
	f.files[id] += len(req.Body)
	f.parts[id] = append(f.parts[id], part)
	writeBoostyJSON(w, map[string]any{"ok": true})
}

func (f *fakeBoosty) complete(w http.ResponseWriter, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files[id] == 0 {
		http.Error(w, `{"error":"nothing was uploaded"}`, http.StatusBadRequest)
		return
	}
	f.done[id] = true
	writeBoostyJSON(w, map[string]any{"fileId": id})
}

// create is the post itself, and the assertion that a post is only made of
// pictures this fake actually finished.
func (f *fakeBoosty) create(w http.ResponseWriter, req recorded) {
	form, err := url.ParseQuery(req.Body)
	if err != nil {
		f.t.Errorf("the create body is not a form: %q", req.Body)
	}
	if strings.TrimSpace(form.Get("title")) == "" {
		http.Error(w, `{"error":"a post needs a title"}`, http.StatusBadRequest)
		return
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(form.Get("data")), &blocks); err != nil {
		http.Error(w, `{"error":"data is not an array of blocks"}`, http.StatusBadRequest)
		return
	}
	if len(blocks) == 0 {
		http.Error(w, `{"error":"a post needs content"}`, http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range blocks {
		if b["type"] != "image" {
			continue
		}
		id, _ := b["id"].(string)
		if !f.done[id] {
			http.Error(w, `{"error":"the post names a picture that was never finished: `+id+`"}`,
				http.StatusBadRequest)
			return
		}
	}
	writeBoostyJSON(w, map[string]any{
		"id": "9f1c-post", "int_id": 5150, "title": form.Get("title"),
	})
}

func writeBoostyJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

// findAllRequests is every recorded request whose path contains fragment.
func findAllRequests(r *recorder, fragment string) []recorded {
	var out []recorded
	for _, req := range r.all() {
		if strings.Contains(req.Path, fragment) {
			out = append(out, req)
		}
	}
	return out
}

// boostyForm is the recorded create call's body, parsed.
func boostyForm(t *testing.T, rec *recorder) url.Values {
	t.Helper()
	req, ok := findRequest(rec, "/post/")
	if !ok {
		t.Fatal("boosty was never asked to create a post")
	}
	form, err := url.ParseQuery(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	return form
}

// boostyBlocks is the create call's content array, decoded.
func boostyBlocks(t *testing.T, rec *recorder) []map[string]any {
	t.Helper()
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(boostyForm(t, rec).Get("data")), &blocks); err != nil {
		t.Fatal(err)
	}
	return blocks
}

func boostyInput(t *testing.T, caption string, pages int) Input {
	t.Helper()
	arts := make([]render.Artifact, 0, pages)
	for i := 0; i < pages; i++ {
		arts = append(arts, artifact(t, render.KindImage, config.PNG,
			strings.Repeat(string(rune('a'+i)), 8+i)))
	}
	return Input{Artifact: arts[0], Artifacts: arts, Caption: caption, Post: 1, Posts: 1}
}

// --- the post ---------------------------------------------------------------

// TestBoostyPostsEveryPageAsOneImageBlockRun is the whole point of the
// publisher: a paginated run is one post carrying the pages in order, not a
// post per page.
func TestBoostyPostsEveryPageAsOneImageBlockRun(t *testing.T) {
	rec := newRecorder()
	api, upload, f := newFakeBoosty(t, rec)
	p := onlyPublisher(t, boostyConfig(api, upload))

	res, err := p.Publish(context.Background(), boostyInput(t, "crier v1\n\nthree cards", 3))
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "9f1c-post" {
		t.Errorf("id = %q", res.ID)
	}
	if want := "https://boosty.to/" + boostyBlogName + "/posts/9f1c-post"; res.URL != want {
		t.Errorf("url = %q, want %q", res.URL, want)
	}
	if res.Extra["pictures"] != "3" {
		t.Errorf("extra = %v", res.Extra)
	}

	// One post, three pictures, three completions.
	if n := len(findAllRequests(rec, "/post/")); n != 1 {
		t.Errorf("%d posts were created, want one", n)
	}
	if n := len(findAllRequests(rec, "/up/image")); n != 3 {
		t.Errorf("%d slots were opened, want one per page", n)
	}
	if n := len(findAllRequests(rec, "/complete")); n != 3 {
		t.Errorf("%d uploads were completed, want one per page", n)
	}

	// The image blocks are the pages, in the order the pages were rendered.
	var ids []string
	for _, b := range boostyBlocks(t, rec) {
		if b["type"] == "image" {
			id, _ := b["id"].(string)
			ids = append(ids, id)
			if b["uploadId"] != b["id"] {
				t.Errorf("block %v names a different uploadId", b)
			}
			if b["url"] != boostyImagesCDN+"/image/"+id {
				t.Errorf("block url = %v", b["url"])
			}
			if b["rendition"] != "" {
				t.Errorf("block rendition = %v, want the full-size empty one", b["rendition"])
			}
		}
	}
	if strings.Join(ids, ",") != "file-1,file-2,file-3" {
		t.Errorf("image blocks = %v, want the pages in order", ids)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		if !f.done[id] {
			t.Errorf("the post named %s, which was never completed", id)
		}
	}
}

// TestBoostyCaptionBecomesTextBlocks pins the draft-style encoding: a
// paragraph is a text block whose content is itself a JSON string, and each one
// is closed by a BLOCK_END.
func TestBoostyCaptionBecomesTextBlocks(t *testing.T) {
	rec := newRecorder()
	api, upload, _ := newFakeBoosty(t, rec)
	p := onlyPublisher(t, boostyConfig(api, upload))

	if _, err := p.Publish(context.Background(), boostyInput(t, "one\n\ntwo", 1)); err != nil {
		t.Fatal(err)
	}

	blocks := boostyBlocks(t, rec)
	if len(blocks) != 5 {
		t.Fatalf("got %d blocks, want two paragraphs, two ends and a picture: %v", len(blocks), blocks)
	}
	for i, want := range []struct{ content, mod string }{
		{`["one","unstyled",[]]`, ""},
		{"", "BLOCK_END"},
		{`["two","unstyled",[]]`, ""},
		{"", "BLOCK_END"},
	} {
		if blocks[i]["type"] != "text" {
			t.Fatalf("block %d is %v", i, blocks[i])
		}
		if blocks[i]["content"] != want.content {
			t.Errorf("block %d content = %v, want %s", i, blocks[i]["content"], want.content)
		}
		if blocks[i]["modificator"] != want.mod {
			t.Errorf("block %d modificator = %v, want %q", i, blocks[i]["modificator"], want.mod)
		}
	}
	if blocks[4]["type"] != "image" {
		t.Errorf("the last block is %v, want the picture", blocks[4])
	}
}

// TestBoostyPostsWithoutACaption: Boosty refuses a post with no content, so a
// run of pictures and no words still carries one empty paragraph.
func TestBoostyPostsWithoutACaption(t *testing.T) {
	rec := newRecorder()
	api, upload, _ := newFakeBoosty(t, rec)
	p := onlyPublisher(t, boostyConfig(api, upload))

	if _, err := p.Publish(context.Background(), boostyInput(t, "", 1)); err != nil {
		t.Fatal(err)
	}
	blocks := boostyBlocks(t, rec)
	if len(blocks) != 3 || blocks[0]["content"] != `["","unstyled",[]]` {
		t.Errorf("blocks = %v", blocks)
	}
	if got := boostyForm(t, rec).Get("title"); got != "crier" {
		t.Errorf("title = %q, want the last fallback", got)
	}
}

// --- the access levels ------------------------------------------------------

// TestBoostyAccessLevelsAreTwoNumbers is the field-exact contract for the
// feature: three configurations, one call, and the two numbers that carry the
// difference.
func TestBoostyAccessLevelsAreTwoNumbers(t *testing.T) {
	for _, tc := range []struct {
		name         string
		set          func(*config.Boosty)
		price, level string
		note         string
	}{
		{"free", func(b *config.Boosty) { b.Access = "free" }, "0", "0", "posting free to everyone"},
		{"paid", func(b *config.Boosty) { b.Access = "paid"; b.Price = 300 }, "300", "0",
			"posting behind a one-time price of 300 RUB"},
		{"level", func(b *config.Boosty) { b.Access = "level"; b.LevelID = "778899" }, "0", "778899",
			"posting for subscription level 778899"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder()
			api, upload, _ := newFakeBoosty(t, rec)
			cfg := boostyConfig(api, upload)
			tc.set(&cfg.Publish.Boosty)
			p := onlyPublisher(t, cfg)

			res, err := p.Publish(context.Background(), boostyInput(t, "hello", 1))
			if err != nil {
				t.Fatal(err)
			}
			if res.Extra["access"] != tc.name {
				t.Errorf("extra access = %q", res.Extra["access"])
			}

			form := boostyForm(t, rec)
			if got := form.Get("price"); got != tc.price {
				t.Errorf("price = %q, want %q", got, tc.price)
			}
			if got := form.Get("subscription_level_id"); got != tc.level {
				t.Errorf("subscription_level_id = %q, want %q", got, tc.level)
			}
			// The rest of the call is the same whatever the access level is.
			for k, want := range map[string]string{
				"teaser_data": "[]", "tags": "", "deny_comments": "false",
				"wait_video": "false", "has_chat": "false", "advertiser_info": "",
			} {
				if got := form.Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}

			id, err := p.Ping(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if id.Note != tc.note {
				t.Errorf("ping note = %q, want %q", id.Note, tc.note)
			}
		})
	}
}

// TestBoostyCurrencyTravelsAsAHeader, because it is what the price means.
func TestBoostyCurrencyTravelsAsAHeader(t *testing.T) {
	rec := newRecorder()
	api, upload, _ := newFakeBoosty(t, rec)
	cfg := boostyConfig(api, upload)
	cfg.Publish.Boosty.Access, cfg.Publish.Boosty.Price = "paid", 5
	cfg.Publish.Boosty.Currency = "usd"
	p := onlyPublisher(t, cfg)

	if _, err := p.Publish(context.Background(), boostyInput(t, "hi", 1)); err != nil {
		t.Fatal(err)
	}
	req, ok := findRequest(rec, "/post/")
	if !ok {
		t.Fatal("nothing was posted")
	}
	if got := req.Header.Get("X-Currency"); got != "USD" {
		t.Errorf("X-Currency = %q, want the configured currency upper-cased", got)
	}
}

// --- the upload -------------------------------------------------------------

// TestBoostyUploadsInNumberedParts: the host refuses a part with no
// X-PartNumber, and the numbers start at one.
func TestBoostyUploadsInNumberedParts(t *testing.T) {
	rec := newRecorder()
	api, upload, f := newFakeBoosty(t, rec)
	p := onlyPublisher(t, boostyConfig(api, upload))

	in := boostyInput(t, "hi", 1)
	if _, err := p.Publish(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := strings.Join(f.parts["file-1"], ","); got != "1" {
		t.Errorf("parts = %q, want a single part numbered from one", got)
	}
	if got, want := f.files["file-1"], int(in.Artifact.Size); got != want {
		t.Errorf("%d bytes arrived, want %d; something was truncated", got, want)
	}
}

// TestBoostyChunkArithmetic pins the split the upload host wants, since a
// wrong last chunk is the classic way an upload is accepted and then fails.
func TestBoostyChunkArithmetic(t *testing.T) {
	chunks := SplitChunks(BoostyChunkSize+1, BoostyChunkSize)
	if len(chunks) != 2 || chunks[1].Size != 1 || chunks[1].Start != BoostyChunkSize {
		t.Errorf("chunks = %+v", chunks)
	}
}

// TestBoostyRefusesMorePagesThanItTakes, before anything is uploaded.
func TestBoostyRefusesMorePagesThanItTakes(t *testing.T) {
	rec := newRecorder()
	api, upload, _ := newFakeBoosty(t, rec)
	p := onlyPublisher(t, boostyConfig(api, upload))

	_, err := p.Publish(context.Background(), boostyInput(t, "hi", BoostyAttachmentMax+1))
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(BoostyAttachmentMax)) {
		t.Fatalf("err = %v", err)
	}
	if len(rec.all()) != 0 {
		t.Errorf("it made %d requests before refusing", len(rec.all()))
	}
}

// TestBoostyReportsWhichPageFailed, because "picture 2 of 3" is what says how
// far a post got.
func TestBoostyReportsWhichPageFailed(t *testing.T) {
	rec := newRecorder()
	api, _, _ := newFakeBoosty(t, rec)
	cfg := boostyConfig(api, api+"/nowhere")
	p := onlyPublisher(t, cfg)

	_, err := p.Publish(context.Background(), boostyInput(t, "hi", 2))
	if err == nil || !strings.Contains(err.Error(), "picture 1 of 2") {
		t.Fatalf("err = %v", err)
	}
}

// --- the token --------------------------------------------------------------

// TestBoostyRefreshesAnExpiredTokenAndRetriesOnce is the auth model: a 401,
// one refresh, the same call again, and no second refresh however many calls
// follow.
func TestBoostyRefreshesAnExpiredTokenAndRetriesOnce(t *testing.T) {
	rec := newRecorder()
	api, upload, f := newFakeBoosty(t, rec)
	cfg := boostyConfig(api, upload)
	cfg.Publish.Boosty.RefreshToken = "refresh-1"
	cfg.Publish.Boosty.DeviceID = "device-1"
	p := onlyPublisher(t, cfg)

	// The configured token is not the one the fake will accept, which is what
	// an access token that ran out between runs looks like.
	f.expire("access-2")

	if _, err := p.Publish(context.Background(), boostyInput(t, "hi", 2)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	refreshes := f.refreshes
	f.mu.Unlock()
	if refreshes != 1 {
		t.Errorf("the token was refreshed %d times, want once per run", refreshes)
	}
	// Everything after the refresh carried the new token, including the post.
	req, ok := findRequest(rec, "/post/")
	if !ok {
		t.Fatal("nothing was posted")
	}
	if req.Header.Get("Authorization") != "Bearer access-2" {
		t.Errorf("the post carried %q", req.Header.Get("Authorization"))
	}
}

// TestBoostyWithoutRefreshCredentialsReportsTheExpiry: there is nothing to
// renew with, so the 401 stands and the operator is told why it is one.
func TestBoostyWithoutRefreshCredentialsReportsTheExpiry(t *testing.T) {
	rec := newRecorder()
	api, upload, f := newFakeBoosty(t, rec)
	p := onlyPublisher(t, boostyConfig(api, upload))
	f.expire("access-2")

	_, err := p.Publish(context.Background(), boostyInput(t, "hi", 1))
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
	if n := len(findAllRequests(rec, "/oauth/token/")); n != 0 {
		t.Errorf("it tried to refresh %d times with nothing to refresh with", n)
	}
}

// TestBoostyRefreshFailureKeepsTheOriginalRefusal, so the message says the
// token was refused as well as that renewing it did not work.
func TestBoostyRefreshFailureKeepsTheOriginalRefusal(t *testing.T) {
	rec := newRecorder()
	api, upload, f := newFakeBoosty(t, rec)
	cfg := boostyConfig(api, upload)
	cfg.Publish.Boosty.RefreshToken = "refresh-1"
	cfg.Publish.Boosty.DeviceID = "device-1"
	p := onlyPublisher(t, cfg)

	f.expire("access-2")
	f.mu.Lock()
	f.refreshStatus = http.StatusBadRequest
	f.mu.Unlock()

	_, err := p.Publish(context.Background(), boostyInput(t, "hi", 1))
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, want := range []string{"401", "refreshing the token failed too", "already dead"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

// TestBoostyReportsARotatedRefreshToken: Boosty spends the one it was given,
// so the replacement has to reach the operator or the next run cannot renew.
func TestBoostyReportsARotatedRefreshToken(t *testing.T) {
	rec := newRecorder()
	api, upload, f := newFakeBoosty(t, rec)
	cfg := boostyConfig(api, upload)
	cfg.Publish.Boosty.RefreshToken = "refresh-1"
	cfg.Publish.Boosty.DeviceID = "device-1"

	var logged bytes.Buffer
	d := testDeps(t)
	d.Logger = testLoggerTo(&logged)
	built, err := Build(cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	f.expire("access-2")
	if _, err := built[0].Publish(context.Background(), boostyInput(t, "hi", 1)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logged.String(), "refresh-2") {
		t.Errorf("the new refresh token was not reported:\n%s", logged.String())
	}
}

// --- ping -------------------------------------------------------------------

// TestBoostyPingNamesTheBlog, which is the answer a setup is asking for.
func TestBoostyPingNamesTheBlog(t *testing.T) {
	rec := newRecorder()
	api, upload, _ := newFakeBoosty(t, rec)
	p := onlyPublisher(t, boostyConfig(api, upload))

	id, err := p.Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "Crier HQ" || id.ID != boostyBlogName {
		t.Errorf("identity = %+v", id)
	}
	// Nothing was posted and nothing was uploaded.
	for _, fragment := range []string{"/post/", "/up/image", "/complete"} {
		if _, ok := findRequest(rec, fragment); ok {
			t.Errorf("ping reached %s", fragment)
		}
	}
}

// TestBoostyPingSaysWhenTheTokenCannotPost is the state a credential check
// would otherwise pass: the token reads the blog and may not write to it.
func TestBoostyPingSaysWhenTheTokenCannotPost(t *testing.T) {
	for _, tc := range []struct {
		name           string
		owner, canPost bool
		want           string
	}{
		{"a stranger", false, false, "cannot post to it"},
		{"the author without the right", true, false, "no right to create posts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder()
			api, upload, f := newFakeBoosty(t, rec)
			f.blog["isOwner"] = tc.owner
			f.blog["accessRights"] = map[string]any{"canCreate": tc.canPost}
			p := onlyPublisher(t, boostyConfig(api, upload))

			id, err := p.Ping(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(id.Note, tc.want) {
				t.Errorf("note = %q, want it to mention %q", id.Note, tc.want)
			}
		})
	}
}

// --- the title --------------------------------------------------------------

// TestBoostyTitleFallsBackThroughTheChain: the configured title, then the
// caption's first line, then crier's own name.
func TestBoostyTitleFallsBackThroughTheChain(t *testing.T) {
	for _, tc := range []struct{ title, caption, want string }{
		{"Release notes", "crier v1\nmore", "Release notes"},
		{"", "crier v1\nmore", "crier v1"},
		{"", "\n\n", "crier"},
		{"   ", "  spaced  \nmore", "spaced"},
	} {
		rec := newRecorder()
		api, upload, _ := newFakeBoosty(t, rec)
		cfg := boostyConfig(api, upload)
		cfg.Publish.Boosty.Title = tc.title
		p := onlyPublisher(t, cfg)

		if _, err := p.Publish(context.Background(), boostyInput(t, tc.caption, 1)); err != nil {
			t.Fatal(err)
		}
		if got := boostyForm(t, rec).Get("title"); got != tc.want {
			t.Errorf("title %q + caption %q = %q, want %q", tc.title, tc.caption, got, tc.want)
		}
	}
}

// --- configuration ----------------------------------------------------------

// TestBoostyRefusesAConfigurationThatCannotWork is the validation matrix: every
// way of asking for a post crier cannot make, refused at build time with the
// key that has to change.
func TestBoostyRefusesAConfigurationThatCannotWork(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*config.Boosty)
		want string
	}{
		{"no blog", func(b *config.Boosty) { b.Blog = "" }, "publish.boosty.blog"},
		{"no token", func(b *config.Boosty) { b.AccessToken = "" }, "publish.boosty.access-token"},
		{"no api host", func(b *config.Boosty) { b.APIBaseURL = "" }, "publish.boosty.api-base-url"},
		{"no upload host", func(b *config.Boosty) { b.UploadBaseURL = "" }, "publish.boosty.upload-base-url"},
		{"an access level boosty has no field for",
			func(b *config.Boosty) { b.Access = "members" }, "free, paid or level"},
		{"paid with no price", func(b *config.Boosty) { b.Access = "paid" }, "publish.boosty.price"},
		{"paid with a zero price",
			func(b *config.Boosty) { b.Access = "paid"; b.Price = 0 }, "publish.boosty.price"},
		{"a level with no id", func(b *config.Boosty) { b.Access = "level" }, "publish.boosty.level-id"},
		{"a refresh token with no device id",
			func(b *config.Boosty) { b.RefreshToken = "r" }, "publish.boosty.device-id"},
		{"a device id with no refresh token",
			func(b *config.Boosty) { b.DeviceID = "d" }, "publish.boosty.refresh-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := boostyConfig("https://api.example", "https://up.example")
			tc.set(&cfg.Publish.Boosty)
			_, err := Build(cfg, testDeps(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// TestBoostyValidatesTheAccessLevelInTheConfiguration as well as at build
// time, so `crier config` reports it without a token being present.
func TestBoostyValidatesTheAccessLevelInTheConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*config.Boosty)
		want string
	}{
		{"unknown", func(b *config.Boosty) { b.Access = "patrons" }, "publish.boosty.access"},
		{"paid with no price", func(b *config.Boosty) { b.Access = "paid" }, "publish.boosty.price"},
		{"level with no id", func(b *config.Boosty) { b.Access = "level" }, "publish.boosty.level-id"},
		{"a negative price", func(b *config.Boosty) { b.Price = -1 }, "publish.boosty.price"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			tc.set(&cfg.Publish.Boosty)
			err := config.Validate(&cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// TestBoostyDefaultsAreThePublishedHosts, so a configuration that names only
// the credentials reaches Boosty rather than nothing.
func TestBoostyDefaultsAreThePublishedHosts(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Publish.Boosty.APIBaseURL != "https://api.boosty.to" {
		t.Errorf("api-base-url = %q", cfg.Publish.Boosty.APIBaseURL)
	}
	if cfg.Publish.Boosty.UploadBaseURL != "https://upload.boosty.to" {
		t.Errorf("upload-base-url = %q", cfg.Publish.Boosty.UploadBaseURL)
	}
	if cfg.Publish.Boosty.Access != "free" {
		t.Errorf("access = %q, want the level that charges nobody", cfg.Publish.Boosty.Access)
	}
}

// TestBoostyTakesBytesAndPicturesOnly pins Needs, which is what the pipeline
// refuses a mismatched run against.
func TestBoostyTakesBytesAndPicturesOnly(t *testing.T) {
	p := onlyPublisher(t, boostyConfig("https://api.example", "https://up.example"))
	n := p.Needs()
	if n.URL {
		t.Error("boosty takes the bytes; it needs no staging")
	}
	if !n.Accepts(render.KindImage) {
		t.Error("boosty posts pictures")
	}
	for _, kind := range []render.Kind{render.KindVideo, render.KindGIF} {
		if n.Accepts(kind) {
			t.Errorf("boosty declares %s, which no client pins an endpoint for", kind)
		}
	}
	if n.Capacity() != BoostyAttachmentMax {
		t.Errorf("capacity = %d", n.Capacity())
	}
}
