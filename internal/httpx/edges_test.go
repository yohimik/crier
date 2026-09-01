package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func testClient(t *testing.T, policy RetryPolicy) *Client {
	t.Helper()
	return New(Options{Retry: policy, Logger: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel)})
}

func TestBaseNameHandlesBothSeparators(t *testing.T) {
	for path, want := range map[string]string{
		"/tmp/card.png":        "card.png",
		`C:\Users\me\card.png`: "card.png",
		"card.png":             "card.png",
		"":                     "",
		"/trailing/":           "",
		`mixed/path\file.jpg`:  "file.jpg",
	} {
		if got := baseName(path); got != want {
			t.Errorf("baseName(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestCancelledContextIsNotRetried: the caller has moved on, and another
// attempt would only fail again more slowly.
func TestCancelledContextIsNotRetried(t *testing.T) {
	if retryableNetError(context.Canceled) {
		t.Error("a cancelled caller should not be retried")
	}
	if retryableNetError(fmt.Errorf("wrapped: %w", context.Canceled)) {
		t.Error("a wrapped cancellation should not be retried")
	}
	if !retryableNetError(errors.New("connection reset by peer")) {
		t.Error("an ordinary transport error is worth another attempt")
	}
	// A per-attempt deadline is a timeout worth retrying.
	if !retryableNetError(context.DeadlineExceeded) {
		t.Error("a per-attempt deadline should be retried")
	}
}

func TestDrainCloseToleratesNothing(t *testing.T) {
	drainClose(nil) // must not panic

	closed := false
	drainClose(&countingBody{Reader: strings.NewReader("body"), onClose: func() { closed = true }})
	if !closed {
		t.Error("the body was not closed")
	}
}

type countingBody struct {
	io.Reader
	onClose func()
}

func (c *countingBody) Close() error {
	c.onClose()
	return nil
}

// TestMultipartStreamsALargeBody covers the branch that a file too big to
// buffer takes, including the part names and the replay.
func TestMultipartStreamsALargeBody(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, MaxBufferedBody+1), 0o600); err != nil {
		t.Fatal(err)
	}

	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("the body is not readable multipart: %v", err)
		}
		for name := range r.MultipartForm.File {
			got = append(got, "file:"+name)
		}
		for name := range r.MultipartForm.Value {
			got = append(got, "field:"+name)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	req := NewRequest(http.MethodPost, srv.URL).
		Multipart(Field("caption", "hello"), FilePart("media", big, "application/octet-stream"))
	resp, err := testClient(t, RetryPolicy{Max: 0, Timeout: 30 * time.Second}).Send(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "file:media") || !strings.Contains(joined, "field:caption") {
		t.Errorf("the server received %v", got)
	}
}

// TestMultipartRefusesAnUnreadablePart: a file that cannot be opened is an
// error on the builder rather than a request with a truncated body.
func TestMultipartRefusesAnUnreadablePart(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.png")
	req := NewRequest(http.MethodPost, "https://example.test/").
		Multipart(FilePart("file", missing, "image/png"))
	if _, err := req.Build(context.Background()); err == nil {
		t.Fatal("a missing file should fail the build")
	}
}

// TestStatusOfAcceptsWhatTheCallerAccepts is Mastodon's 206, which is a normal
// outcome rather than an error.
func TestStatusOfAcceptsWhatTheCallerAccepts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte(`{"id":"m-1"}`))
	}))
	t.Cleanup(srv.Close)
	c := testClient(t, RetryPolicy{Max: 0, Timeout: 5 * time.Second})

	var out struct {
		ID string `json:"id"`
	}
	status, err := c.StatusOf(context.Background(), NewRequest(http.MethodGet, srv.URL),
		func(code int) bool { return code == http.StatusOK || code == http.StatusPartialContent }, &out)
	if err != nil || status != http.StatusPartialContent || out.ID != "m-1" {
		t.Fatalf("status=%d out=%+v err=%v", status, out, err)
	}

	// A status the caller does not accept comes back as an APIError carrying
	// the body, which is the only thing that says why.
	status, err = c.StatusOf(context.Background(), NewRequest(http.MethodGet, srv.URL),
		func(code int) bool { return code == http.StatusOK }, nil)
	if status != http.StatusPartialContent {
		t.Errorf("status = %d", status)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !strings.Contains(string(apiErr.Body), "m-1") {
		t.Errorf("err = %v", err)
	}

	// A build error surfaces before anything is sent.
	if _, err := c.StatusOf(context.Background(), NewRequest(http.MethodGet, "://bad"),
		func(int) bool { return true }, nil); err == nil {
		t.Error("an unbuildable request should fail")
	}
}

// TestStatusOfRejectsAnUndecodableBody covers the decode branch.
func TestStatusOfRejectsAnUndecodableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	t.Cleanup(srv.Close)

	var out map[string]any
	_, err := testClient(t, RetryPolicy{Max: 0}).StatusOf(context.Background(),
		NewRequest(http.MethodGet, srv.URL), func(int) bool { return true }, &out)
	if err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("err = %v", err)
	}
}

// TestJSONReportsADecodeFailure is the same for the plain JSON helper.
func TestJSONReportsADecodeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"truncated":`))
	}))
	t.Cleanup(srv.Close)

	var out map[string]any
	err := testClient(t, RetryPolicy{Max: 0}).JSON(context.Background(), NewRequest(http.MethodGet, srv.URL), &out)
	if err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Errorf("err = %v", err)
	}

	// And a build error, which happens before the request exists.
	if err := testClient(t, RetryPolicy{Max: 0}).JSON(context.Background(),
		NewRequest(http.MethodGet, "://bad"), &out); err == nil {
		t.Error("an unbuildable request should fail")
	}
}

// TestBytesRejectsAnUnsendableRequest covers the Bytes helper's error paths.
func TestBytesRejectsAnUnsendableRequest(t *testing.T) {
	c := testClient(t, RetryPolicy{Max: 0})
	if _, err := c.Bytes(context.Background(), NewRequest(http.MethodGet, "://bad")); err == nil {
		t.Error("an unbuildable request should fail")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(srv.Close)
	body, err := c.Bytes(context.Background(), NewRequest(http.MethodGet, srv.URL))
	if err != nil || string(body) != "hello" {
		t.Errorf("body = %q, err = %v", body, err)
	}
}

// TestSleepStopsWithTheContext: a backoff that ignores cancellation is a run
// that will not stop on Ctrl-C.
func TestSleepStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := Sleep(ctx, time.Minute); err == nil {
		t.Error("a cancelled context should end the wait")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the wait took %s", elapsed)
	}
	// A zero or negative wait returns at once.
	if err := Sleep(context.Background(), 0); err != nil {
		t.Errorf("Sleep(0) = %v", err)
	}
}

// TestRedactURLStringRoundTrip covers the string form and its refusal.
func TestRedactURLStringRoundTrip(t *testing.T) {
	got := RedactURLString("https://graph.facebook.com/v25.0/me?access_token=secret&fields=id")
	if strings.Contains(got, "secret") {
		t.Errorf("leaked: %s", got)
	}
	if !strings.Contains(got, "fields=id") {
		t.Errorf("lost the harmless part: %s", got)
	}
	// A query that cannot be parsed may hold anything.
	if got := redactQuery("%zz"); got != RedactedValue {
		t.Errorf("an unparseable query = %q", got)
	}
}
