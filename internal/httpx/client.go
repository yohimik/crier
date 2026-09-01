// Package httpx is the HTTP client every publisher and stager shares.
//
// It is net/http with three things added, each of which exists because a
// social platform API taught it: retries that know a 429 from a 500, request
// bodies that can be replayed so a retry has something to send, and a NoRetry
// twin for the calls that must not be repeated because repeating them would
// publish the same post twice.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Options configures New.
type Options struct {
	// Retry is the retry policy applied to idempotent requests.
	Retry RetryPolicy
	// Logger receives one debug record per request and a warning per retry.
	Logger zerolog.Logger
	// Transport is the round tripper the retrying one wraps. Zero value:
	// http.DefaultTransport.
	Transport http.RoundTripper
	// UserAgent is sent with every request.
	UserAgent string
}

// Client is the HTTP client every publisher and stager shares.
//
// It is a thin facade over net/http with three things added: retries that know
// what a social API means by 429, request bodies that can be replayed, and a
// NoRetry twin for the calls that must not be repeated.
type Client struct {
	hc        *http.Client
	log       zerolog.Logger
	userAgent string

	noRetry *Client
}

// New builds a client and its NoRetry twin.
func New(o Options) *Client {
	base := o.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	policy := o.Retry
	if policy.BaseDelay <= 0 {
		policy.BaseDelay = DefaultRetryPolicy.BaseDelay
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = DefaultRetryPolicy.MaxDelay
	}

	build := func(only429 bool) *Client {
		return &Client{
			hc: &http.Client{Transport: &retryTransport{
				base:    &loggingTransport{base: base, log: o.Logger},
				policy:  policy,
				log:     o.Logger,
				only429: only429,
			}},
			log:       o.Logger,
			userAgent: o.UserAgent,
		}
	}
	c := build(false)
	c.noRetry = build(true)
	c.noRetry.noRetry = c.noRetry
	return c
}

// NoRetry returns a twin of the client that retries rate limits and nothing
// else.
//
// Use it for the call that actually creates something on the platform —
// media_publish, POST /2/tweets, POST /statuses — where a 5xx may mean the
// post was created and the response was lost. Retrying that would publish
// twice, which is worse than reporting a failure that turns out to have
// succeeded.
func (c *Client) NoRetry() *Client { return c.noRetry }

// Do sends a request. The caller owns the response body and must close it.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	return c.hc.Do(req)
}

// Send builds and sends a request, turning a non-2xx status into an APIError.
// The caller owns the response body and must close it.
func (c *Client) Send(ctx context.Context, b *Builder) (*http.Response, error) {
	req, err := b.Build(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer drainClose(resp.Body)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBody))
		return nil, &APIError{
			Method: req.Method, URL: req.URL.Redacted(),
			Status: resp.StatusCode, Header: resp.Header, Body: body,
		}
	}
	return resp, nil
}

// JSON sends a request and decodes a JSON response into out, which may be nil
// when the caller does not care about the body. The body is always drained and
// closed.
func (c *Client) JSON(ctx context.Context, b *Builder, out any) error {
	resp, err := c.Send(ctx, b)
	if err != nil {
		return err
	}
	defer drainClose(resp.Body)
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// Bytes sends a request and returns the whole response body.
func (c *Client) Bytes(ctx context.Context, b *Builder) ([]byte, error) {
	resp, err := c.Send(ctx, b)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp.Body)
	return io.ReadAll(resp.Body)
}

// Discard sends a request and throws the body away.
func (c *Client) Discard(ctx context.Context, b *Builder) error {
	resp, err := c.Send(ctx, b)
	if err != nil {
		return err
	}
	drainClose(resp.Body)
	return nil
}

// StatusOf sends a request that is expected to return a body the caller reads
// itself, and reports the status alongside it. It exists for Mastodon, whose
// 206 is a normal outcome rather than an error.
func (c *Client) StatusOf(ctx context.Context, b *Builder, accept func(int) bool, out any) (int, error) {
	req, err := b.Build(ctx)
	if err != nil {
		return 0, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer drainClose(resp.Body)
	if !accept(resp.StatusCode) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBody))
		return resp.StatusCode, &APIError{
			Method: req.Method, URL: req.URL.Redacted(),
			Status: resp.StatusCode, Header: resp.Header, Body: body,
		}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decoding response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// drainClose reads what is left of a body before closing it, so the connection
// goes back to the pool instead of being torn down.
func drainClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

// loggingTransport records every attempt at debug level. It sits under the
// retrying transport so each attempt shows up, not just the last one.
type loggingTransport struct {
	base http.RoundTripper
	log  zerolog.Logger
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.log.Debug().Enabled() {
		return t.base.RoundTrip(req)
	}
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	ev := t.log.Debug().
		Str("method", req.Method).
		Str("url", req.URL.Redacted()).
		Dur("elapsed", time.Since(start))
	if err != nil {
		ev.Err(err).Msg("http request failed")
		return resp, err
	}
	ev.Int("status", resp.StatusCode).Msg("http request")
	return resp, nil
}

// ErrPollTimeout is returned when a polled operation never reached a terminal
// state.
var ErrPollTimeout = errors.New("timed out waiting for the operation to finish")

// Poll calls step every interval until it reports it is done, the timeout
// expires, or the context is cancelled.
//
// Instagram containers, TikTok uploads and Mastodon attachments all become
// ready asynchronously, and all three want the same loop; keeping it in one
// place is what keeps their timeout behaviour identical.
func Poll(ctx context.Context, interval, timeout time.Duration, step func(ctx context.Context) (done bool, err error)) error {
	if interval <= 0 {
		interval = time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		done, err := step(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if timeout > 0 && !time.Now().Add(interval).Before(deadline) {
			return ErrPollTimeout
		}
		if err := Sleep(ctx, interval); err != nil {
			return err
		}
	}
}
