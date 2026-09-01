package stage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yohimik/crier/internal/config"
)

// fakeS3 is the smallest server minio-go will talk to: it answers the location
// probe, stores what is PUT, and forgets what is DELETEd.
type fakeS3 struct {
	*httptest.Server

	mu      sync.Mutex
	objects map[string][]byte
	acls    map[string]string
	types   map[string]string
	deleted []string
	putFail int
	// bucketMissing makes the bucket HEAD answer 404, which is how an object
	// store says "no such bucket, or you cannot see it".
	bucketMissing bool
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	f := &fakeS3{
		objects: map[string][]byte{},
		acls:    map[string]string{},
		types:   map[string]string{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	if _, ok := r.URL.Query()["location"]; ok {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w,
			`<?xml version="1.0" encoding="UTF-8"?><LocationConstraint>us-east-1</LocationConstraint>`)
		return
	}
	// Path-style addressing: the first element is the bucket.
	key := strings.TrimPrefix(r.URL.Path, "/")
	bucketOnly := true
	if _, rest, ok := strings.Cut(key, "/"); ok && rest != "" {
		key, bucketOnly = rest, false
	}
	// A HEAD on the bucket itself is what BucketExists sends.
	if bucketOnly && r.Method == http.MethodHead {
		f.mu.Lock()
		missing := f.bucketMissing
		f.mu.Unlock()
		if missing {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		if f.putFail != 0 {
			w.WriteHeader(f.putFail)
			_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>nope</Message></Error>`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.objects[key] = dechunk(body)
		f.acls[key] = r.Header.Get("X-Amz-Acl")
		f.types[key] = r.Header.Get("Content-Type")
		w.Header().Set("ETag", `"deadbeef"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		delete(f.objects, key)
		f.deleted = append(f.deleted, key)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodHead, http.MethodGet:
		body, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// dechunk undoes the aws-chunked framing minio-go uses when it streams a body
// over a plain http endpoint. Each chunk is a size line, the bytes, and a CRLF.
func dechunk(body []byte) []byte {
	if !bytes.Contains(body, []byte(";chunk-signature=")) {
		return body
	}
	var out []byte
	rest := body
	for {
		line, tail, ok := bytes.Cut(rest, []byte("\r\n"))
		if !ok {
			return out
		}
		sizeHex, _, _ := bytes.Cut(line, []byte(";"))
		size, err := strconv.ParseInt(string(sizeHex), 16, 64)
		if err != nil || size == 0 {
			return out
		}
		if int64(len(tail)) < size {
			return out
		}
		out = append(out, tail[:size]...)
		rest = tail[size:]
		if len(rest) >= 2 {
			rest = rest[2:] // the CRLF after the chunk
		}
	}
}

func (f *fakeS3) stored() map[string][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]byte, len(f.objects))
	for k, v := range f.objects {
		out[k] = v
	}
	return out
}

func s3Config(f *fakeS3) config.S3 {
	return config.S3{
		Endpoint:      strings.TrimPrefix(f.URL, "http://"),
		Region:        "us-east-1",
		Bucket:        "media",
		Prefix:        "cards",
		AccessKey:     "key",
		SecretKey:     "secret",
		UseSSL:        false,
		Presign:       true,
		PresignExpiry: "1h",
		DeleteAfter:   true,
	}
}

func TestS3UploadsPresignsAndDeletes(t *testing.T) {
	f := newFakeS3(t)
	s, err := NewS3(s3Config(f), testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	obj, err := s.Stage(context.Background(), writeAsset(t, "PNGDATA"))
	if err != nil {
		t.Fatal(err)
	}

	stored := f.stored()
	if len(stored) != 1 {
		t.Fatalf("stored %d objects", len(stored))
	}
	var key string
	for k, v := range stored {
		key = k
		if string(v) != "PNGDATA" {
			t.Errorf("body = %q", v)
		}
	}
	if !strings.HasPrefix(key, "cards/") || !strings.HasSuffix(key, "-card.png") {
		t.Errorf("key = %q, want the prefix and the asset name", key)
	}
	if got := f.types[key]; got != "image/png" {
		t.Errorf("content type = %q", got)
	}

	u, err := url.Parse(obj.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("X-Amz-Signature") == "" {
		t.Errorf("url = %q, want a presigned one", obj.URL)
	}
	if !strings.Contains(u.Path, key) {
		t.Errorf("url %q does not point at %q", obj.URL, key)
	}

	if err := obj.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.stored()) != 0 {
		t.Error("the object should have been deleted")
	}
}

func TestS3KeepsTheObjectWhenDeleteAfterIsOff(t *testing.T) {
	f := newFakeS3(t)
	cfg := s3Config(f)
	cfg.DeleteAfter = false
	s, err := NewS3(cfg, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	obj, err := s.Stage(context.Background(), writeAsset(t, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := obj.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.stored()) != 1 {
		t.Error("the object should still be there")
	}
}

func TestS3PublicBaseURL(t *testing.T) {
	f := newFakeS3(t)
	cfg := s3Config(f)
	cfg.Presign = false
	cfg.PublicBaseURL = "https://cdn.example/"
	cfg.ACL = "public-read"
	s, err := NewS3(cfg, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	obj, err := s.Stage(context.Background(), writeAsset(t, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(obj.URL, "https://cdn.example/cards/") {
		t.Errorf("url = %q", obj.URL)
	}
	for _, acl := range f.acls {
		if acl != "public-read" {
			t.Errorf("acl = %q, want the canned ACL to reach the request", acl)
		}
	}
}

func TestS3PublicBaseURLIsRequiredWithoutPresigning(t *testing.T) {
	f := newFakeS3(t)
	cfg := s3Config(f)
	cfg.Presign = false
	s, err := NewS3(cfg, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Stage(context.Background(), writeAsset(t, "x"))
	if err == nil || !strings.Contains(err.Error(), "public-base-url") {
		t.Fatalf("err = %v", err)
	}
	// The unreachable object is cleaned up rather than left behind.
	if len(f.stored()) != 0 {
		t.Error("an object nobody can fetch should not be left in the bucket")
	}
}

func TestS3UploadFailure(t *testing.T) {
	f := newFakeS3(t)
	f.putFail = http.StatusForbidden
	s, err := NewS3(s3Config(f), testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Stage(context.Background(), writeAsset(t, "x"))
	if err == nil || !strings.Contains(err.Error(), "uploading to s3://media/") {
		t.Fatalf("err = %v", err)
	}
}

func TestS3EndpointWithAScheme(t *testing.T) {
	f := newFakeS3(t)
	cfg := s3Config(f)
	cfg.Endpoint = f.URL // with the http:// prefix
	cfg.UseSSL = true    // and contradicted by the scheme
	s, err := NewS3(cfg, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stage(context.Background(), writeAsset(t, "x")); err != nil {
		t.Fatalf("the scheme in the endpoint should have decided: %v", err)
	}
}

func TestS3Basics(t *testing.T) {
	f := newFakeS3(t)
	s, err := NewS3(s3Config(f), testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "s3" {
		t.Errorf("name = %q", s.Name())
	}
	if err := s.Close(context.Background()); err != nil {
		t.Error(err)
	}
	if _, err := NewS3(config.S3{}, testLogger(t)); err == nil {
		t.Error("an empty endpoint should be refused")
	}
	if _, err := NewS3(config.S3{Endpoint: "a b c"}, testLogger(t)); err == nil {
		t.Error("an unusable endpoint should be refused")
	}
}

func TestS3KeyFallsBackToTheFileName(t *testing.T) {
	f := newFakeS3(t)
	s, err := NewS3(s3Config(f), testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	a := writeAsset(t, "x")
	a.Name = ""
	key := s.key(a)
	if !strings.HasSuffix(key, "-card.png") {
		t.Errorf("key = %q", key)
	}
}

// TestS3PingChecksTheBucket covers `crier ping`'s staging half.
//
// A bucket crier cannot reach fails a publish after the render, which is the
// expensive half of the run — so it is worth a HEAD before anything is drawn.
func TestS3PingChecksTheBucket(t *testing.T) {
	f := newFakeS3(t)
	s, err := NewS3(s3Config(f), testLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	what, err := s.Ping(context.Background())
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !strings.Contains(what, "media") {
		t.Errorf("ping said %q, want the bucket named", what)
	}

	f.mu.Lock()
	f.bucketMissing = true
	f.mu.Unlock()
	if _, err := s.Ping(context.Background()); err == nil {
		t.Error("a missing bucket should fail the ping")
	}
}

// TestPingerIsOptional checks the modes that hold no credentials do not
// pretend to have been verified.
func TestPingerIsOptional(t *testing.T) {
	for _, st := range []Stager{None{}, NewURL("https://example.test/x.jpg")} {
		if _, ok := st.(Pinger); ok {
			t.Errorf("%s should not implement Pinger: it holds nothing to check", st.Name())
		}
	}
}
