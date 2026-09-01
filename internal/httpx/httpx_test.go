package httpx

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// fast builds a client whose retries do not really wait, so a test that
// exhausts a backoff runs in microseconds.
func fast(o Options) *Client {
	c := New(o)
	for _, cl := range []*Client{c, c.noRetry} {
		rt := cl.hc.Transport.(*retryTransport)
		rt.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
		rt.jitter = func(d time.Duration) time.Duration { return d }
	}
	return c
}

func testPolicy() RetryPolicy {
	return RetryPolicy{Max: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond, Timeout: 5 * time.Second}
}

// --- URL building ----------------------------------------------------------

func TestJoinURL(t *testing.T) {
	for _, tt := range []struct {
		base string
		segs []string
		want string
	}{
		{"https://a.example", nil, "https://a.example"},
		{"https://a.example/", []string{"b"}, "https://a.example/b"},
		{"https://a.example", []string{"/b/", "c"}, "https://a.example/b/c"},
		{"https://a.example", []string{"", "c"}, "https://a.example/c"},
		// A trailing slash on the last segment is part of the path for some
		// APIs, so it survives.
		{"https://a.example", []string{"v2/init/"}, "https://a.example/v2/init/"},
		{"https://a.example", []string{"a/", "b/"}, "https://a.example/a/b/"},
	} {
		if got := JoinURL(tt.base, tt.segs...); got != tt.want {
			t.Errorf("JoinURL(%q, %v) = %q, want %q", tt.base, tt.segs, got, tt.want)
		}
	}
}

func TestBuilderQueryAndHeaders(t *testing.T) {
	req, err := NewRequest(http.MethodGet, "https://a.example", "v1", "things").
		Query("a", "1").
		QueryIf(false, "skip", "me").
		QueryIf(true, "b", "2 3").
		Header("X-Test", "yes").
		Bearer("tok").
		Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://a.example/v1/things?a=1&b=2+3" {
		t.Errorf("url = %s", req.URL)
	}
	if req.Header.Get("X-Test") != "yes" || req.Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("headers = %v", req.Header)
	}
}

func TestBuilderQueryOnURLWithExistingQuery(t *testing.T) {
	req, err := NewRequest(http.MethodGet, "https://a.example/x?already=1").Query("b", "2").Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.RawQuery != "already=1&b=2" {
		t.Errorf("query = %q", req.URL.RawQuery)
	}
}

func TestBuilderJSONBodyIsReplayable(t *testing.T) {
	req, err := NewRequest(http.MethodPost, "https://a.example").JSON(map[string]int{"n": 1}).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("content type = %q", req.Header.Get("Content-Type"))
	}
	if req.ContentLength != int64(len(`{"n":1}`)) {
		t.Errorf("content length = %d", req.ContentLength)
	}
	for i := 0; i < 2; i++ {
		rc, err := req.GetBody()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		if string(b) != `{"n":1}` {
			t.Errorf("replay %d = %q", i, b)
		}
	}
}

func TestBuilderJSONError(t *testing.T) {
	_, err := NewRequest(http.MethodPost, "https://a.example").JSON(func() {}).Build(context.Background())
	if err == nil {
		t.Fatal("expected an encoding error")
	}
}

func TestBuilderForm(t *testing.T) {
	req, err := NewRequest(http.MethodPost, "https://a.example").Form(url.Values{"a": {"b c"}}).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != "a=b+c" {
		t.Errorf("body = %q", body)
	}
	if req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Error("wrong content type")
	}
}

func TestBuilderFileBody(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := NewRequest(http.MethodPut, "https://a.example").File("image/png", p).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if req.ContentLength != 5 {
		t.Errorf("length = %d", req.ContentLength)
	}
	rc, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "hello" {
		t.Errorf("replayed body = %q", b)
	}
}

func TestBuilderFileMissing(t *testing.T) {
	_, err := NewRequest(http.MethodPut, "https://a.example").
		File("image/png", filepath.Join(t.TempDir(), "nope")).
		Build(context.Background())
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func readMultipart(t *testing.T, contentType string, body []byte) (fields map[string]string, files map[string][]byte) {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
	fields, files = map[string]string{}, map[string][]byte{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			files[part.FormName()] = data
		} else {
			fields[part.FormName()] = string(data)
		}
	}
	return fields, files
}

func TestBuilderMultipartBuffered(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "img.png")
	if err := os.WriteFile(p, []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := NewRequest(http.MethodPost, "https://a.example").
		Multipart(Field("caption", "hi"), FilePart("photo", p, "image/png")).
		Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if req.ContentLength <= 0 {
		t.Errorf("a buffered multipart body should have a Content-Length, got %d", req.ContentLength)
	}
	body, _ := io.ReadAll(req.Body)
	fields, files := readMultipart(t, req.Header.Get("Content-Type"), body)
	if fields["caption"] != "hi" {
		t.Errorf("fields = %v", fields)
	}
	if string(files["photo"]) != "PNGDATA" {
		t.Errorf("files = %v", files)
	}
	// and it replays identically
	rc, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := io.ReadAll(rc)
	if string(again) != string(body) {
		t.Error("replayed multipart body differs")
	}
}

func TestBuilderMultipartStreamedWhenSizeUnknown(t *testing.T) {
	part := Part{
		Name: "photo", FileName: "x.png", ContentType: "image/png", Size: -1,
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("DATA")), nil },
	}
	req, err := NewRequest(http.MethodPost, "https://a.example").
		Multipart(Field("k", "v"), part).
		Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if req.ContentLength >= 0 {
		t.Errorf("a streamed body has no Content-Length, got %d", req.ContentLength)
	}
	body, _ := io.ReadAll(req.Body)
	fields, files := readMultipart(t, req.Header.Get("Content-Type"), body)
	if fields["k"] != "v" || string(files["photo"]) != "DATA" {
		t.Errorf("fields=%v files=%v", fields, files)
	}
	rc, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := io.ReadAll(rc)
	if len(again) != len(body) {
		t.Errorf("replay length %d != %d", len(again), len(body))
	}
}

func TestBytesPartAndFieldHelpers(t *testing.T) {
	p := BytesPart("f", "n.png", "image/png", []byte("x"))
	if p.Size != 1 || p.FileName != "n.png" {
		t.Errorf("%+v", p)
	}
	if f := Field("a", "b"); f.Open != nil || f.Value != "b" {
		t.Errorf("%+v", f)
	}
	if got := baseName("/a/b/c.png"); got != "c.png" {
		t.Errorf("baseName = %q", got)
	}
	if got := baseName(`C:\a\b.png`); got != "b.png" {
		t.Errorf("baseName windows = %q", got)
	}
}

func TestEscapeQuotesInPartNames(t *testing.T) {
	req, err := NewRequest(http.MethodPost, "https://a.example").
		Multipart(BytesPart(`we"ird`, `na"me.png`, "image/png", []byte("x"))).
		Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), `name="we\"ird"`) {
		t.Errorf("part name not escaped: %q", body)
	}
}

// --- client and retries ----------------------------------------------------

func TestSendReturnsAPIErrorForNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"nope"}`)
	}))
	defer srv.Close()

	c := fast(Options{Retry: testPolicy()})
	_, err := c.Send(context.Background(), NewRequest(http.MethodGet, srv.URL))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want APIError, got %v", err)
	}
	if apiErr.Status != 400 || !strings.Contains(apiErr.Error(), "nope") {
		t.Errorf("apiErr = %v", apiErr)
	}
	if apiErr.Temporary() || apiErr.RateLimited() {
		t.Error("400 is neither temporary nor rate limited")
	}
	if apiErr.StatusCode() != 400 {
		t.Error("StatusCode")
	}
}

func TestAPIErrorWithEmptyBody(t *testing.T) {
	e := &APIError{Method: "GET", URL: "https://a.example", Status: 503}
	if !strings.Contains(e.Error(), "Service Unavailable") {
		t.Errorf("%v", e)
	}
	if !e.Temporary() {
		t.Error("503 is temporary")
	}
}

func TestRetriesFiveHundredThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := fast(Options{Retry: testPolicy()})
	var out struct{ OK bool }
	if err := c.JSON(context.Background(), NewRequest(http.MethodGet, srv.URL), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Error("body not decoded")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestRetriesExhaustAndReturnLastStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := fast(Options{Retry: testPolicy()})
	err := c.Discard(context.Background(), NewRequest(http.MethodGet, srv.URL))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 502 {
		t.Fatalf("err = %v", err)
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("calls = %d, want 1 attempt plus 3 retries", got)
	}
}

func TestRetriesReplayTheBody(t *testing.T) {
	var bodies []string
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := fast(Options{Retry: testPolicy()})
	if err := c.Discard(context.Background(), NewRequest(http.MethodPost, srv.URL).JSON(map[string]string{"a": "b"})); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[0] != `{"a":"b"}` {
		t.Errorf("bodies = %q", bodies)
	}
}

func TestNoRetryStillRetriesRateLimits(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := fast(Options{Retry: testPolicy()})
	if err := c.NoRetry().Discard(context.Background(), NewRequest(http.MethodPost, srv.URL)); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want the 429 retried once", got)
	}
}

func TestNoRetryDoesNotRepeatServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := fast(Options{Retry: testPolicy()})
	err := c.NoRetry().Discard(context.Background(), NewRequest(http.MethodPost, srv.URL))
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want exactly one: a publish must never be repeated", got)
	}
}

func TestNoRetryTwinIsStable(t *testing.T) {
	c := New(Options{})
	if c.NoRetry().NoRetry() != c.NoRetry() {
		t.Error("NoRetry of a NoRetry client should be itself")
	}
}

func TestRetryAfterHeaderHonoured(t *testing.T) {
	var waits []time.Duration
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := New(Options{Retry: RetryPolicy{Max: 2, BaseDelay: time.Hour, MaxDelay: time.Hour, Timeout: 5 * time.Second}})
	rt := c.hc.Transport.(*retryTransport)
	rt.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	if err := c.Discard(context.Background(), NewRequest(http.MethodGet, srv.URL)); err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0] != time.Second {
		t.Errorf("waits = %v, want the Retry-After value, not the backoff", waits)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	h := http.Header{}
	if _, ok := retryAfter(h); ok {
		t.Error("no header means no delay")
	}
	h.Set("Retry-After", "5")
	if d, ok := retryAfter(h); !ok || d != 5*time.Second {
		t.Errorf("seconds form: %v %v", d, ok)
	}
	h.Set("Retry-After", "-5")
	if _, ok := retryAfter(h); ok {
		t.Error("a negative delay is not a delay")
	}
	h.Set("Retry-After", "soon")
	if _, ok := retryAfter(h); ok {
		t.Error("garbage is not a delay")
	}
	h.Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))
	if d, ok := retryAfter(h); !ok || d <= 0 {
		t.Errorf("http date form: %v %v", d, ok)
	}
	h.Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
	if d, ok := retryAfter(h); !ok || d != 0 {
		t.Errorf("past date should clamp to zero: %v %v", d, ok)
	}
}

func TestRetryAfterIsCappedByMaxDelay(t *testing.T) {
	var waits []time.Duration
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := New(Options{Retry: RetryPolicy{Max: 2, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Second, Timeout: 5 * time.Second}})
	c.hc.Transport.(*retryTransport).sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	if err := c.Discard(context.Background(), NewRequest(http.MethodGet, srv.URL)); err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0] != 2*time.Second {
		t.Errorf("waits = %v, want the cap", waits)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	rt := &retryTransport{jitter: func(d time.Duration) time.Duration { return d }}
	p := RetryPolicy{Max: 10, BaseDelay: time.Second, MaxDelay: 4 * time.Second}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	for i, w := range want {
		if got := rt.delay(p, i); got != w {
			t.Errorf("delay(%d) = %v, want %v", i, got, w)
		}
	}
}

func TestFullJitterStaysInRange(t *testing.T) {
	if fullJitter(0) != 0 {
		t.Error("zero")
	}
	for i := 0; i < 100; i++ {
		d := fullJitter(time.Second)
		if d < time.Second/2 || d >= time.Second+time.Second/2 {
			t.Fatalf("jitter out of range: %v", d)
		}
	}
}

func TestNetworkErrorIsRetried(t *testing.T) {
	var calls atomic.Int32
	c := fast(Options{
		Retry: testPolicy(),
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("dial failed")
		}),
	})
	err := c.Discard(context.Background(), NewRequest(http.MethodGet, "https://a.example"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("calls = %d, want the network error retried", got)
	}
}

func TestNetworkErrorIsNotRetriedForNonIdempotentCalls(t *testing.T) {
	var calls atomic.Int32
	c := fast(Options{
		Retry: testPolicy(),
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("dial failed")
		}),
	})
	if err := c.NoRetry().Discard(context.Background(), NewRequest(http.MethodPost, "https://a.example")); err == nil {
		t.Fatal("expected an error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want one", got)
	}
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	c := New(Options{
		Retry: testPolicy(),
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			cancel()
			return nil, r.Context().Err()
		}),
	})
	if err := c.Discard(ctx, NewRequest(http.MethodGet, "https://a.example")); err == nil {
		t.Fatal("expected an error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want one: the caller gave up", got)
	}
}

func TestBodyThatCannotBeReplayed(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = nil
	req.ContentLength = -1

	c := fast(Options{Retry: testPolicy()})
	resp, err := c.Do(req)
	if err == nil {
		drainClose(resp.Body)
		t.Fatal("expected a replay error")
	}
	if !strings.Contains(err.Error(), "cannot be replayed") {
		t.Errorf("err = %v", err)
	}
}

func TestUserAgentIsSet(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	defer srv.Close()
	c := fast(Options{Retry: testPolicy(), UserAgent: "crier/test"})
	if err := c.Discard(context.Background(), NewRequest(http.MethodGet, srv.URL)); err != nil {
		t.Fatal(err)
	}
	if got != "crier/test" {
		t.Errorf("user agent = %q", got)
	}
}

func TestBytesReadsTheWholeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "payload")
	}))
	defer srv.Close()
	c := fast(Options{Retry: testPolicy()})
	b, err := c.Bytes(context.Background(), NewRequest(http.MethodGet, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "payload" {
		t.Errorf("body = %q", b)
	}
}

func TestJSONDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer srv.Close()
	c := fast(Options{Retry: testPolicy()})
	var out struct{}
	if err := c.JSON(context.Background(), NewRequest(http.MethodGet, srv.URL), &out); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestJSONWithNilOutSkipsDecoding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer srv.Close()
	c := fast(Options{Retry: testPolicy()})
	if err := c.JSON(context.Background(), NewRequest(http.MethodGet, srv.URL), nil); err != nil {
		t.Fatal(err)
	}
}

func TestStatusOfAcceptsPartialContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, `{"id":"7"}`)
	}))
	defer srv.Close()
	c := fast(Options{Retry: testPolicy()})
	var out struct{ ID string }
	status, err := c.StatusOf(context.Background(), NewRequest(http.MethodPost, srv.URL),
		func(s int) bool { return s == 200 || s == 202 || s == 206 }, &out)
	if err != nil {
		t.Fatal(err)
	}
	if status != 206 || out.ID != "7" {
		t.Errorf("status=%d out=%+v", status, out)
	}
}

func TestStatusOfRejectsUnacceptedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := fast(Options{Retry: testPolicy()})
	status, err := c.StatusOf(context.Background(), NewRequest(http.MethodGet, srv.URL),
		func(s int) bool { return s == 200 }, nil)
	if status != 403 || err == nil {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

func TestStatusOfBuildError(t *testing.T) {
	c := fast(Options{Retry: testPolicy()})
	if _, err := c.StatusOf(context.Background(), NewRequest(http.MethodPost, "://bad").JSON(func() {}),
		func(int) bool { return true }, nil); err == nil {
		t.Fatal("expected a build error")
	}
}

func TestSendBuildError(t *testing.T) {
	c := fast(Options{Retry: testPolicy()})
	if _, err := c.Send(context.Background(), NewRequest("bad method\n", "https://a.example")); err == nil {
		t.Fatal("expected a build error")
	}
}

func TestLoggingTransportRecordsAttempts(t *testing.T) {
	var buf strings.Builder
	lg := zerolog.New(&buf).Level(zerolog.DebugLevel)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	c := fast(Options{Retry: testPolicy(), Logger: lg})
	if err := c.Discard(context.Background(), NewRequest(http.MethodGet, srv.URL)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "http request") {
		t.Errorf("no debug record: %q", buf.String())
	}
}

func TestLoggingTransportPassesErrorsThrough(t *testing.T) {
	var buf strings.Builder
	lg := zerolog.New(&buf).Level(zerolog.DebugLevel)
	c := New(Options{
		Logger: lg,
		Retry:  RetryPolicy{Max: 0, Timeout: time.Second},
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("boom")
		}),
	})
	if err := c.Discard(context.Background(), NewRequest(http.MethodGet, "https://a.example")); err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(buf.String(), "http request failed") {
		t.Errorf("no failure record: %q", buf.String())
	}
}

// --- polling ---------------------------------------------------------------

func TestPollStopsWhenDone(t *testing.T) {
	n := 0
	err := Poll(context.Background(), time.Millisecond, time.Second, func(context.Context) (bool, error) {
		n++
		return n == 3, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("steps = %d", n)
	}
}

func TestPollPropagatesStepError(t *testing.T) {
	want := errors.New("boom")
	err := Poll(context.Background(), time.Millisecond, time.Second, func(context.Context) (bool, error) {
		return false, want
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v", err)
	}
}

func TestPollTimesOut(t *testing.T) {
	err := Poll(context.Background(), 5*time.Millisecond, 12*time.Millisecond, func(context.Context) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, ErrPollTimeout) {
		t.Errorf("err = %v", err)
	}
}

func TestPollStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Poll(ctx, time.Millisecond, time.Second, func(context.Context) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestPollDefaultInterval(t *testing.T) {
	err := Poll(context.Background(), 0, time.Nanosecond, func(context.Context) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, ErrPollTimeout) {
		t.Errorf("err = %v", err)
	}
}

func TestSleepReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if err := Sleep(context.Background(), time.Millisecond); err != nil {
		t.Errorf("err = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
