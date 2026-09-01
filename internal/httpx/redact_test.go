package httpx

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// TestRedactURLHidesCredentials covers the two places crier's URLs carry one:
// a query parameter, and Telegram's path.
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
