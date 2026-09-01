package app

import (
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
