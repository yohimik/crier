package publish

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
)

type failedRedactionTransport struct{}

func (failedRedactionTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func TestPublishTransportFailureHidesTokensInOutcomeAndLogs(t *testing.T) {
	for _, platform := range []string{"telegram", "discord"} {
		t.Run(platform, func(t *testing.T) {
			const secret = "short-secret"
			var logs bytes.Buffer
			log := zerolog.New(&logs).Level(zerolog.DebugLevel)
			cfg := config.Defaults()
			if platform == "telegram" {
				cfg.Publish.Telegram.Enabled = true
				cfg.Publish.Telegram.Token = "42:" + secret
				cfg.Publish.Telegram.ChatID = "1"
				cfg.Publish.Telegram.APIBaseURL = "https://example.test"
			} else {
				cfg.Publish.Discord.Enabled = true
				cfg.Publish.Discord.WebhookURL = "https://example.test/api/webhooks/42/" + secret
			}
			ps, err := Build(&cfg, Deps{Client: httpx.New(httpx.Options{Logger: log, Transport: failedRedactionTransport{}}), Logger: log})
			if err != nil {
				t.Fatal(err)
			}
			report := RunAll(context.Background(), []Job{{Publisher: ps[0], Posts: []Input{{Artifact: imageArtifact(t)}}}}, 1, log)
			if len(report.Outcomes) != 1 || report.Outcomes[0].OK || report.Outcomes[0].Error == "" {
				t.Fatalf("missing failure: %+v", report)
			}
			message := report.Outcomes[0].Error + logs.String()
			if strings.Contains(message, secret) {
				t.Fatalf("token leaked: %s", message)
			}
			if !strings.Contains(report.Outcomes[0].Error, "REDACTED") {
				t.Fatalf("not a redacted transport error: %+v", report)
			}
		})
	}
}
