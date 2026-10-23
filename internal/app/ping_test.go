package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPingIncludesTheStagerWhenItHoldsCredentials checks the staging row, which
// is the half of a setup that fails after the render rather than before it.
func TestPingIncludesTheStagerWhenItHoldsCredentials(t *testing.T) {
	dir := project(t, strings.Join([]string{
		// Nothing is listening, so the retries would only make the test slow.
		"http:",
		"  retry-max: 0",
		"  timeout: 2s",
		"stage:",
		"  mode: s3",
		"  s3:",
		// A port nothing is listening on: the point is that the row appears
		// and says what went wrong, not that a bucket exists.
		"    endpoint: http://127.0.0.1:1",
		"    bucket: media",
		"    access-key: k",
		"    secret-key: s",
		"publish:",
		"  telegram:",
		"    enabled: true",
		"    api-base-url: http://127.0.0.1:1",
		"    token: t",
		"    chat-id: c",
	}, "\n"))

	code, stdout, _ := run(t, dir, []string{}, "ping")
	// Everything is unreachable, so this is the all-failed code.
	if code != ExitPublish {
		t.Fatalf("code = %d, want the all-failed code", code)
	}
	if !strings.Contains(stdout, "stage:s3") {
		t.Errorf("no staging row:\n%s", stdout)
	}
	if !strings.Contains(stdout, "telegram") {
		t.Errorf("no platform row:\n%s", stdout)
	}
}

// TestPingSkipsStagingThatHoldsNothing: reporting "ok" for a mode that was
// never checked would be a lie in the most reassuring possible place.
func TestPingSkipsStagingThatHoldsNothing(t *testing.T) {
	for _, mode := range []string{"none", "url"} {
		dir := project(t, strings.Join([]string{
			"http:",
			"  retry-max: 0",
			"  timeout: 2s",
			"stage:",
			"  mode: " + mode,
			"  url: https://example.test/x.jpg",
			"publish:",
			"  telegram:",
			"    enabled: true",
			"    api-base-url: http://127.0.0.1:1",
			"    token: t",
			"    chat-id: c",
		}, "\n"))
		_, stdout, _ := run(t, dir, []string{}, "ping")
		if strings.Contains(stdout, "stage:") {
			t.Errorf("%s: a staging row appeared for a mode with nothing to check:\n%s", mode, stdout)
		}
	}
}

func TestPingTransportFailureHidesTokensInReportAndStderr(t *testing.T) {
	for _, platform := range []string{"telegram", "discord"} {
		t.Run(platform, func(t *testing.T) {
			const secret = "short-secret"
			platformConfig := "  discord:\n    enabled: true\n    webhook-url: http://127.0.0.1:1/api/webhooks/42/" + secret
			if platform == "telegram" {
				platformConfig = "  telegram:\n    enabled: true\n    token: 42:" + secret + "\n    chat-id: 1\n    api-base-url: http://127.0.0.1:1"
			}
			dir := project(t, "http:\n  retry-max: 0\n  timeout: 1s\nlog:\n  level: debug\npublish:\n"+platformConfig)
			code, stdout, stderr := run(t, dir, []string{}, "ping", "--json")
			if code != ExitPublish {
				t.Fatalf("code = %d: %s %s", code, stdout, stderr)
			}
			var report PingReport
			if err := json.Unmarshal([]byte(stdout), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Results) != 1 || report.Results[0].Error == "" {
				t.Fatalf("missing failure: %+v", report)
			}
			if strings.Contains(stdout+stderr, secret) {
				t.Fatalf("token leaked: %s %s", stdout, stderr)
			}
			if !strings.Contains(report.Results[0].Error, "REDACTED") {
				t.Fatalf("not a redacted transport failure: %+v", report)
			}
		})
	}
}
