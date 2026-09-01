package template

import (
	"path/filepath"
	"strings"
	"testing"
)

const baseLayout = `<body><header>{{ block "title" . }}base title{{ end }}</header>` +
	`<main>{{ block "body" . }}base body{{ end }}</main></body>`

func TestOverlayRedefinesABlock(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "base.html", baseLayout)
	overlay := write(t, dir, "story.html", `{{ define "title" }}story title{{ end }}`)

	got, err := New().Render(Options{Path: base, Overlays: []string{overlay}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "story title") || strings.Contains(got, "base title") {
		t.Errorf("title not overridden: %q", got)
	}
	if !strings.Contains(got, "base body") {
		t.Errorf("untouched block should stay: %q", got)
	}
}

func TestLaterOverlayWins(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "base.html", baseLayout)
	global := write(t, dir, "global.html", `{{ define "title" }}global{{ end }}`)
	platform := write(t, dir, "platform.html", `{{ define "title" }}platform{{ end }}`)

	got, err := New().Render(Options{Path: base, Overlays: []string{global, platform}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "platform") || strings.Contains(got, "global") {
		t.Errorf("last overlay should win: %q", got)
	}
}

func TestOverlaySeesTheData(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "base.html", `{{ block "b" . }}{{ end }}`)
	overlay := write(t, dir, "o.html", `{{ define "b" }}{{ .who }}{{ end }}`)
	data := write(t, dir, "d.yaml", "who: world\n")

	got, err := New().Render(Options{Path: base, DataPath: data, Overlays: []string{overlay}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "world" {
		t.Errorf("got %q", got)
	}
}

func TestOverlayErrors(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "base.html", baseLayout)

	if _, err := New().Render(Options{Path: base, Overlays: []string{filepath.Join(dir, "missing.html")}}); err == nil {
		t.Error("expected an error for a missing overlay")
	}

	broken := write(t, dir, "broken.html", `{{ define "title" }}oops`)
	_, err := New().Render(Options{Path: base, Overlays: []string{broken}})
	if err == nil || !strings.Contains(err.Error(), "parsing overlay") {
		t.Errorf("err = %v", err)
	}
}

func TestVideoFrameVariablesReachTheTemplate(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "base.html", `frame {{ .Video.Frame }}/{{ .Video.Frames }} t={{ .Video.Time }} p={{ .Video.Progress }}`)
	got, err := New().Render(Options{Path: base, Extra: map[string]any{
		"Video": map[string]any{"Frame": 3, "Frames": 10, "Time": 0.1, "Progress": 0.3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "frame 3/10 t=0.1 p=0.3" {
		t.Errorf("got %q", got)
	}
}
