package httpx

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

// RetryPolicy decides whether and how long to wait before trying again.
type RetryPolicy struct {
	// Max is the number of retries after the first attempt. Zero disables
	// retrying entirely.
	Max int
	// BaseDelay is the first backoff delay; it doubles each attempt.
	BaseDelay time.Duration
	// MaxDelay caps the backoff.
	MaxDelay time.Duration
	// Timeout bounds one attempt. Zero means no per-attempt bound.
	Timeout time.Duration
	// UploadTimeout bounds one attempt that carries a large body. Zero falls
	// back to Timeout.
	//
	// It exists because the per-attempt timeout bounds the body write as well
	// as the response wait, and those are not the same kind of wait. A minute
	// is generous for an API call and nowhere near enough to push a 50MB video
	// up a domestic uplink — so the one setting that makes an API feel
	// responsive is the one that makes every large upload fail, at a
	// deterministic size, with an opaque deadline error.
	UploadTimeout time.Duration
}

// UploadThreshold is the body size past which UploadTimeout applies.
//
// A megabyte: comfortably above any JSON call crier makes and comfortably
// below any media it uploads, so the two kinds of request are told apart by
// what they are rather than by who called.
const UploadThreshold = 1 << 20

// DefaultRetryPolicy is used when a zero policy reaches the transport.
var DefaultRetryPolicy = RetryPolicy{
	Max: 3, BaseDelay: 500 * time.Millisecond, MaxDelay: 10 * time.Second,
	Timeout: time.Minute, UploadTimeout: 10 * time.Minute,
}

// attemptTimeout is how long one attempt at this request may take.
//
// A body of unknown length is treated as an upload: crier streams a multipart
// body when the file is too large to buffer, which is exactly the case that
// needs the longer bound.
func attemptTimeout(policy RetryPolicy, req *http.Request) time.Duration {
	if policy.UploadTimeout <= 0 {
		return policy.Timeout
	}
	if req.ContentLength < 0 || req.ContentLength > UploadThreshold {
		return policy.UploadTimeout
	}
	return policy.Timeout
}

// retryTransport retries a request the way a social platform API wants to be
// retried, and no more than that.
//
// The distinction that matters is idempotency. A GET or a container-creation
// call can be repeated safely; the call that actually publishes a post cannot,
// because a 5xx from a gateway may still have created the post. So the
// finalisation calls go through a transport with only429 set: a 429 means the
// request was refused and never ran, and is retried; everything else is
// surfaced to the caller and the post is not duplicated.
type retryTransport struct {
	base   http.RoundTripper
	policy RetryPolicy
	log    zerolog.Logger

	// only429 restricts retrying to rate limits, for non-idempotent requests.
	only429 bool

	// sleep is swapped out in tests so retries do not really wait.
	sleep func(ctx context.Context, d time.Duration) error
	// jitter is swapped out in tests to make the backoff deterministic.
	jitter func(d time.Duration) time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	policy := t.policy
	if policy.BaseDelay <= 0 {
		policy.BaseDelay = DefaultRetryPolicy.BaseDelay
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = DefaultRetryPolicy.MaxDelay
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		body, err := attemptBody(req, attempt)
		if err != nil {
			return nil, err
		}

		ctx := req.Context()
		cancel := context.CancelFunc(func() {})
		if timeout := attemptTimeout(policy, req); timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, timeout)
		}
		attemptReq := req.Clone(ctx)
		attemptReq.Body = body

		resp, err := t.base.RoundTrip(attemptReq)
		if err == nil && !t.shouldRetryStatus(resp.StatusCode) {
			// The caller owns the body, so the per-attempt deadline has to live
			// until the body is closed.
			resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}

		if err != nil && req.Context().Err() != nil {
			// The caller gave up; another attempt would only fail again.
			cancel()
			return nil, err
		}

		wait, retryable := t.backoff(policy, attempt, resp, err)
		if !retryable {
			if err != nil {
				cancel()
				return nil, err
			}
			resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}

		if resp != nil {
			// Drain a little so the connection can be reused, then close it:
			// an undrained body on a retried request is the classic connection
			// leak.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			lastErr = statusError(attemptReq, resp)
		} else {
			lastErr = err
		}
		cancel()

		t.log.Warn().
			Str("method", req.Method).
			Str("url", RedactURL(req.URL)).
			Int("attempt", attempt+1).
			Dur("wait", wait).
			Err(lastErr).
			Msg("retrying request")

		if err := t.sleepFor(req.Context(), wait); err != nil {
			return nil, errors.Join(err, lastErr)
		}
	}
}

// attemptBody returns the body for one attempt, rebuilding it from GetBody on
// every attempt after the first.
func attemptBody(req *http.Request, attempt int) (io.ReadCloser, error) {
	if attempt == 0 {
		return req.Body, nil
	}
	if req.Body == nil || req.Body == http.NoBody {
		return req.Body, nil
	}
	if req.GetBody == nil {
		return nil, errors.New("httpx: request body cannot be replayed")
	}
	return req.GetBody()
}

// shouldRetryStatus says whether a status is worth another attempt at all.
func (t *retryTransport) shouldRetryStatus(status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if t.only429 {
		return false
	}
	return status >= 500
}

// backoff decides whether to retry and how long to wait first.
func (t *retryTransport) backoff(policy RetryPolicy, attempt int, resp *http.Response, err error) (time.Duration, bool) {
	if attempt >= policy.Max {
		return 0, false
	}
	if err != nil {
		if t.only429 || !retryableNetError(err) {
			return 0, false
		}
		return t.delay(policy, attempt), true
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		if d, ok := retryAfter(resp.Header); ok {
			if d > policy.MaxDelay {
				d = policy.MaxDelay
			}
			return d, true
		}
		return t.delay(policy, attempt), true
	}
	if t.only429 {
		return 0, false
	}
	return t.delay(policy, attempt), true
}

// delay is exponential backoff with full jitter, capped.
func (t *retryTransport) delay(policy RetryPolicy, attempt int) time.Duration {
	d := policy.BaseDelay << attempt
	if d > policy.MaxDelay || d <= 0 {
		d = policy.MaxDelay
	}
	if t.jitter != nil {
		return t.jitter(d)
	}
	return fullJitter(d)
}

func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(d))) + d/2
}

func (t *retryTransport) sleepFor(ctx context.Context, d time.Duration) error {
	if t.sleep != nil {
		return t.sleep(ctx, d)
	}
	return Sleep(ctx, d)
}

// Sleep waits for d, or returns early when the context is done.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryableNetError says whether a transport error is worth another attempt.
// A cancelled or expired caller context is not: the caller has moved on.
func retryableNetError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	// A per-attempt deadline is a timeout worth retrying; the caller's own
	// deadline is not, and it will fail the next attempt immediately anyway.
	return true
}

// retryAfter reads the Retry-After header in both of its spellings.
func retryAfter(h http.Header) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if when, err := http.ParseTime(v); err == nil {
		d := time.Until(when)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// statusError renders a response as an error, for logging a retried attempt.
func statusError(req *http.Request, resp *http.Response) error {
	return &APIError{Method: req.Method, URL: RedactURL(req.URL), Status: resp.StatusCode, Header: resp.Header}
}

// cancelOnClose ties a per-attempt context to the lifetime of the body.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   bool
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	if !c.once {
		c.once = true
		c.cancel()
	}
	return err
}
