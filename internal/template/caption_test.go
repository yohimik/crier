package template

import (
	"strings"
	"testing"
)

func TestRenderCaptionPlainStringPassesThrough(t *testing.T) {
	got, err := RenderCaption("just words, 100% of them", nil, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if got != "just words, 100% of them" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCaptionUsesTheDocument(t *testing.T) {
	data := map[string]any{"version": "1.2.3", "title": "Release"}
	got, err := RenderCaption("{{ .title }} {{ .version }} on {{ .Platform }}", data, "mastodon")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Release 1.2.3 on mastodon" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCaptionPlatformWithoutData(t *testing.T) {
	got, err := RenderCaption("posting to {{ .Platform }}", nil, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got != "posting to x" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCaptionNonObjectDocument(t *testing.T) {
	got, err := RenderCaption("{{ index .Data 1 }} to {{ .Platform }}", []any{"a", "b"}, "discord")
	if err != nil {
		t.Fatal(err)
	}
	if got != "b to discord" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCaptionMissingKeyIsAnError(t *testing.T) {
	_, err := RenderCaption("{{ .nope }}", map[string]any{"a": 1}, "telegram")
	if err == nil {
		t.Fatal("a missing key must fail rather than post <no value>")
	}
	if !strings.Contains(err.Error(), "executing caption template") {
		t.Errorf("err = %v", err)
	}
}

func TestRenderCaptionParseErrorIsReported(t *testing.T) {
	_, err := RenderCaption("{{ .broken", nil, "telegram")
	if err == nil || !strings.Contains(err.Error(), "parsing caption template") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderCaptionHasTheStandardFuncs(t *testing.T) {
	got, err := RenderCaption(`{{ upper .who }}{{ default "!" "" }}`, map[string]any{"who": "hi"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got != "HI!" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCaptionDoesNotHTMLEscape(t *testing.T) {
	// A caption is posted as plain text, so & must stay &.
	got, err := RenderCaption("{{ .x }}", map[string]any{"x": "a & b"}, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a & b" {
		t.Errorf("got %q, want no HTML escaping", got)
	}
}

func TestCaptionDataShapes(t *testing.T) {
	if m := captionData(nil, "p").(map[string]any); m[PlatformKey] != "p" || len(m) != 1 {
		t.Errorf("nil: %v", m)
	}
	if m := captionData(map[string]any{"a": 1}, "p").(map[string]any); m["a"] != 1 || m[PlatformKey] != "p" {
		t.Errorf("object: %v", m)
	}
	if m := captionData("scalar", "p").(map[string]any); m[DataKey] != "scalar" || m[PlatformKey] != "p" {
		t.Errorf("scalar: %v", m)
	}
	// merging must not write into the caller's document
	src := map[string]any{"a": 1}
	_ = captionData(src, "p")
	if _, leaked := src[PlatformKey]; leaked {
		t.Error("captionData wrote into the caller's map")
	}
}
