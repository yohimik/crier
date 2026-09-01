package httpx

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// TestAttemptTimeoutTellsUploadsApart is the rule: an API call and a media
// upload are different kinds of wait and cannot share one bound.
func TestAttemptTimeoutTellsUploadsApart(t *testing.T) {
	policy := RetryPolicy{Timeout: time.Minute, UploadTimeout: 10 * time.Minute}

	small, _ := http.NewRequest(http.MethodPost, "https://example.test/", strings.NewReader("{}"))
	if got := attemptTimeout(policy, small); got != time.Minute {
		t.Errorf("a small body = %s, want the plain timeout", got)
	}

	big, _ := http.NewRequest(http.MethodPost, "https://example.test/", bytes.NewReader(make([]byte, UploadThreshold+1)))
	if got := attemptTimeout(policy, big); got != 10*time.Minute {
		t.Errorf("a large body = %s, want the upload timeout", got)
	}

	// A streamed body has no declared length, and streaming is what crier does
	// for a file too large to buffer — the upload case exactly.
	streamed, _ := http.NewRequest(http.MethodPost, "https://example.test/", io.NopCloser(strings.NewReader("x")))
	streamed.ContentLength = -1
	if got := attemptTimeout(policy, streamed); got != 10*time.Minute {
		t.Errorf("an unknown length = %s, want the upload timeout", got)
	}

	// Without an upload timeout configured, nothing changes.
	if got := attemptTimeout(RetryPolicy{Timeout: time.Minute}, big); got != time.Minute {
		t.Errorf("= %s, want the plain timeout when no upload timeout is set", got)
	}
}

// TestSlowUploadSurvivesAShortAPITimeout is the regression test.
//
// http.timeout bounds one attempt, and one attempt includes writing the body.
// A 50MB video on a slow uplink therefore failed deterministically, with an
// opaque deadline error, at whatever size took longer than the timeout that
// makes ordinary API calls feel responsive.
func TestSlowUploadSurvivesAShortAPITimeout(t *testing.T) {
	const size = UploadThreshold + 4096
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the body slowly, the way a congested uplink delivers it.
		buf := make([]byte, 64<<10)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				time.Sleep(15 * time.Millisecond)
			}
			if err != nil {
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := New(Options{
		Retry: RetryPolicy{
			Max: 0,
			// Short enough that the upload cannot finish inside it.
			Timeout:       120 * time.Millisecond,
			UploadTimeout: 30 * time.Second,
		},
		Logger: zerolog.Nop(),
	})

	req := NewRequest(http.MethodPost, srv.URL).Bytes("application/octet-stream", make([]byte, size))
	resp, err := c.Send(context.Background(), req)
	if err != nil {
		t.Fatalf("the upload was cut off by the API timeout: %v", err)
	}
	_ = resp.Body.Close()

	// And the same request under the old rule — no upload timeout — does fail,
	// so this test is exercising the difference rather than a fast machine.
	strict := New(Options{
		Retry:  RetryPolicy{Max: 0, Timeout: 120 * time.Millisecond},
		Logger: zerolog.Nop(),
	})
	if _, err := strict.Send(context.Background(),
		NewRequest(http.MethodPost, srv.URL).Bytes("application/octet-stream", make([]byte, size))); err == nil {
		t.Error("without an upload timeout the same upload should have been cut off")
	}
}
