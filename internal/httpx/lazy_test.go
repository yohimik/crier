package httpx

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBuildErrorLeaksNoFileDescriptor is a regression test.
//
// File opened the file as the builder was written, so a request that never
// left — a bad URL, a later error — leaked a descriptor per attempt. The same
// went for the streamed multipart body, which started a pipe and a goroutine
// at build time.
func TestBuildErrorLeaksNoFileDescriptor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "card.png")
	if err := os.WriteFile(path, []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}

	before := runtime.NumGoroutine()
	for i := 0; i < 200; i++ {
		// An invalid URL, so Build fails after the body was described.
		req := NewRequest(http.MethodPost, "://not a url").File("image/png", path)
		if _, err := req.Build(context.Background()); err == nil {
			t.Fatal("the URL is invalid; Build should fail")
		}

		// And the streamed multipart body, which is the goroutine half.
		big := filepath.Join(dir, "big.bin")
		if _, err := os.Stat(big); err != nil {
			if err := os.WriteFile(big, make([]byte, MaxBufferedBody+1), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		m := NewRequest(http.MethodPost, "://not a url").
			Multipart(Field("a", "b"), FilePart("file", big, "application/octet-stream"))
		if _, err := m.Build(context.Background()); err == nil {
			t.Fatal("the URL is invalid; Build should fail")
		}
	}

	// The descriptors: opening the file again must still work, and on a leak
	// of 400 descriptors a low ulimit would already have failed above. The
	// goroutine count is the check that survives any ulimit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+5 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines grew from %d to %d: the streamed body started before it was read",
		before, runtime.NumGoroutine())
}

// TestLazyBodyOpensOnceAndClosesCleanly covers the reader itself.
func TestLazyBodyOpensOnceAndClosesCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	opens := 0
	l := &lazyBody{open: func() (io.ReadCloser, error) {
		opens++
		return os.Open(path)
	}}
	if opens != 0 {
		t.Fatal("the body was opened before anything read it")
	}

	buf := make([]byte, 5)
	if n, err := l.Read(buf); err != nil || n != 5 || string(buf) != "hello" {
		t.Fatalf("read = %d, %q, %v", n, buf, err)
	}
	if _, err := l.Read(buf); err == nil {
		t.Error("the second read should be EOF")
	}
	if opens != 1 {
		t.Errorf("opened %d times, want once", opens)
	}
	if err := l.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := l.Read(buf); err == nil {
		t.Error("reading a closed body should fail")
	}

	// A body that never opened closes without error and without opening.
	never := &lazyBody{open: func() (io.ReadCloser, error) {
		t.Error("Close must not open the body")
		return nil, nil
	}}
	if err := never.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// An open that fails surfaces on the read rather than being swallowed.
	broken := &lazyBody{open: func() (io.ReadCloser, error) { return nil, os.ErrNotExist }}
	if _, err := broken.Read(buf); err == nil || !strings.Contains(err.Error(), "exist") {
		t.Errorf("err = %v", err)
	}
}
