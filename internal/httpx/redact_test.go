package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// TestRedactURLHidesCredentials covers credential query parameters, userinfo,
// and Telegram bot paths.
func TestRedactURLHidesCredentials(t *testing.T) {
	for _, tt := range []struct {
		raw      string
		mustHide string
		mustKeep string
	}{
		{
			"https://graph.facebook.com/v25.0/ig-user/media?access_token=EAABsecret123&caption=hi",
			"EAABsecret123", "caption=hi",
		},
		{
			"https://api.telegram.org/bot123456:AAHsecretTOKEN/sendPhoto",
			"AAHsecretTOKEN", "sendPhoto",
		},
		{
			"https://s3.example.com/media/card.jpg?X-Amz-Signature=deadbeefsig&X-Amz-Expires=3600",
			"deadbeefsig", "X-Amz-Expires=3600",
		},
		{
			"https://api.example.com/v1/token?client_secret=shh&refresh_token=alsoshh",
			"alsoshh", "client_secret",
		},
		{
			"https://user:hunter2@example.com/x",
			"hunter2", "example.com",
		},
	} {
		u, err := url.Parse(tt.raw)
		if err != nil {
			t.Fatal(err)
		}
		got := RedactURL(u)
		if strings.Contains(got, tt.mustHide) {
			t.Errorf("RedactURL(%s) leaked %q: %s", tt.raw, tt.mustHide, got)
		}
		if !strings.Contains(got, tt.mustKeep) {
			t.Errorf("RedactURL(%s) lost %q: %s", tt.raw, tt.mustKeep, got)
		}
	}

	// The bot id survives so a log still says which bot, and a path segment
	// that merely starts with "bot" is left alone.
	if got := RedactURL(mustParse(t, "https://api.telegram.org/bot42:secret/getMe")); !strings.Contains(got, "bot42") {
		t.Errorf("the bot id should survive: %s", got)
	}
	if got := RedactURL(mustParse(t, "https://example.com/bottles/x")); !strings.Contains(got, "bottles") {
		t.Errorf("a path that merely starts with bot was masked: %s", got)
	}

	if RedactURL(nil) != "" {
		t.Error("a nil URL should render as nothing")
	}
	if got := RedactURLString("://not a url"); got != RedactedValue {
		t.Errorf("an unparseable URL = %q; it cannot be promised to be clean", got)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestNonTwoHundredErrorCarriesNoToken is the regression test for the leak:
// an APIError is printed by `crier publish` on every failure, and a Telegram
// failure printed the bot token with it.
func TestNonTwoHundredErrorCarriesNoToken(t *testing.T) {
	const token = "123456:AAHverySecretToken"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	}))
	t.Cleanup(srv.Close)

	c := New(Options{Retry: RetryPolicy{Max: 0, Timeout: 5 * time.Second}, Logger: zerolog.Nop()})
	req := NewRequest(http.MethodPost, srv.URL, "bot"+token, "sendPhoto").
		Query("access_token", "another-secret")

	_, err := c.Send(context.Background(), req)
	if err == nil {
		t.Fatal("a 400 should be an error")
	}
	msg := err.Error()
	if strings.Contains(msg, "AAHverySecretToken") || strings.Contains(msg, "another-secret") {
		t.Fatalf("the error leaked a credential: %s", msg)
	}
	// It still says what went wrong.
	if !strings.Contains(msg, "chat not found") || !strings.Contains(msg, "sendPhoto") {
		t.Errorf("the error stopped being useful: %s", msg)
	}
}

// TestRetryWarningCarriesNoToken is the same leak in the log rather than in
// the error: a retried upload logged the full URL at warn level.
func TestRetryWarningCarriesNoToken(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	var logs bytes.Buffer
	c := New(Options{
		Retry: RetryPolicy{
			Max: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, Timeout: 5 * time.Second,
		},
		Logger: zerolog.New(&logs).Level(zerolog.DebugLevel),
	})
	req := NewRequest(http.MethodGet, srv.URL, "bot9:AAHsecret", "getMe").
		Query("access_token", "EAABleak")

	_, _ = c.Send(context.Background(), req)
	if attempts < 2 {
		t.Fatalf("the request was not retried: %d attempts", attempts)
	}
	out := logs.String()
	if !strings.Contains(out, "retrying request") {
		t.Fatalf("nothing was logged: %s", out)
	}
	for _, secret := range []string{"AAHsecret", "EAABleak"} {
		if strings.Contains(out, secret) {
			t.Errorf("the retry log leaked %q:\n%s", secret, out)
		}
	}
}

func TestRedactCustomAndDiscordCredentials(t *testing.T) {
	for _, tt := range []struct{ raw, hide, keep string }{
		{"https://discord.com/api/webhooks/123/part%2Fshort-secret?wait=true", "short-secret", "/123/"},
		{"https://api.telegram.org/bot123%3Apart%2Fshort-secret/getMe", "short-secret", "getMe"},
		{"https://example.com/api-key/part%2Fshort-secret/items", "short-secret", "/items"},
		{"https://discord.com/api/webhooks/123/short-secret?wait=true", "short-secret", "/123/"},
		{"https://discord.com/api/v10/webhooks/123/short-secret/messages/456", "short-secret", "/messages/456"},
		{"https://example.com/api-key/short-secret/items", "short-secret", "/items"},
		{"https://example.com/v1/abcdefghijklmnopqrstuvwxyz123456/items", "abcdefghijklmnopqrstuvwxyz123456", "/items"},
		{"https://example.com/v1?custom_token=short-secret&limit=10", "short-secret", "limit=10"},
		{"https://example.com/v1?api-key=short-secret", "short-secret", "api-key="},
		{"https://example.com/v1?opaque=abcdefghijklmnopqrstuvwxyz123456", "abcdefghijklmnopqrstuvwxyz123456", "opaque="},
		{"https://example.com/v1?access_token=first&access_token=second", "second", "access_token="},
		{"https://example.com/v1?access_token=first;second", "first", "REDACTED"},
		{"https://example.com/v1#short-secret", "short-secret", "/v1"},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			u := mustParse(t, tt.raw)
			before := u.String()
			got := RedactURL(u)
			if strings.Contains(got, tt.hide) || !strings.Contains(got, tt.keep) {
				t.Fatalf("redacted URL = %q, must hide %q and keep %q", got, tt.hide, tt.keep)
			}
			if u.String() != before {
				t.Fatal("redaction mutated the request URL")
			}
		})
	}
}

type redactionRoundTripper func(*http.Request) (*http.Response, error)

func (f redactionRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransportErrorsAndLogsHideCredentials(t *testing.T) {
	for _, debug := range []bool{false, true} {
		for _, noRetry := range []bool{false, true} {
			t.Run(fmt.Sprintf("debug=%t/noRetry=%t", debug, noRetry), func(t *testing.T) {
				const secret = "hook-secret"
				var logs bytes.Buffer
				logger := zerolog.Nop()
				if debug {
					logger = zerolog.New(&logs).Level(zerolog.DebugLevel)
				}
				var original *url.Error
				client := New(Options{
					Logger: logger,
					Retry:  RetryPolicy{Max: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
					Transport: redactionRoundTripper(func(req *http.Request) (*http.Response, error) {
						original = &url.Error{Op: "upstream", URL: req.URL.String(), Err: fmt.Errorf("transport repeated %s: %w", secret, context.DeadlineExceeded)}
						return nil, original
					}),
				})
				if noRetry {
					client = client.NoRetry()
				}
				req, err := http.NewRequest(http.MethodGet, "https://example.com/api/webhooks/123/"+secret+"?custom_token=query-secret", nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = client.Do(req)
				if err == nil {
					t.Fatal("transport unexpectedly succeeded")
				}
				for _, value := range []string{secret, "query-secret"} {
					if strings.Contains(err.Error()+logs.String(), value) {
						t.Fatalf("credential leaked: %s %s", err, logs.String())
					}
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("lost original cause: %v", err)
				}
				var urlErr *url.Error
				if !errors.As(err, &urlErr) || !urlErr.Timeout() || strings.Contains(urlErr.URL, secret) {
					t.Fatalf("lost safe URL/timeout behavior: %v", err)
				}
				if !strings.Contains(original.URL, secret) {
					t.Fatal("mutated transport error")
				}
			})
		}
	}
}

func TestRedirectTransportErrorHidesDestinationCredentials(t *testing.T) {
	const secret = "redirect-secret"
	client := New(Options{Logger: zerolog.Nop(), Transport: redactionRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "start.test" {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://end.test/api/webhooks/1/" + secret}}, Body: http.NoBody, Request: req}, nil
		}
		return nil, errors.New("connection refused")
	})})
	req, _ := http.NewRequest(http.MethodGet, "https://start.test/", nil)
	_, err := client.Do(req)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("unsafe or unhelpful redirect error: %v", err)
	}
}
