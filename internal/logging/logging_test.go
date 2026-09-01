package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    zerolog.Level
		wantErr bool
	}{
		{"", zerolog.InfoLevel, false},
		{"  ", zerolog.InfoLevel, false},
		{"debug", zerolog.DebugLevel, false},
		{"TRACE", zerolog.TraceLevel, false},
		{" warn ", zerolog.WarnLevel, false},
		{"error", zerolog.ErrorLevel, false},
		{"nope", zerolog.NoLevel, true},
	}
	for _, tt := range tests {
		got, err := ParseLevel(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseLevel(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if err == nil && got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for _, tt := range []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"", FormatConsole, false},
		{"console", FormatConsole, false},
		{"JSON", FormatJSON, false},
		{"xml", "", true},
	} {
		got, err := ParseFormat(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseFormat(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if err == nil && got != tt.want {
			t.Errorf("ParseFormat(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestNewJSONHonoursLevel(t *testing.T) {
	var buf bytes.Buffer
	lg, err := New(Options{Level: "warn", Format: "json", Writer: &buf})
	if err != nil {
		t.Fatal(err)
	}
	lg.Info().Msg("dropped")
	lg.Warn().Str("k", "v").Msg("kept")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("not json: %v", err)
	}
	if rec["message"] != "kept" || rec["k"] != "v" || rec["level"] != "warn" {
		t.Fatalf("unexpected record %v", rec)
	}
	if rec["time"] == nil {
		t.Fatal("expected a timestamp field")
	}
}

func TestNewConsole(t *testing.T) {
	var buf bytes.Buffer
	lg, err := New(Options{Level: "debug", Format: "console", Writer: &buf, NoColor: true})
	if err != nil {
		t.Fatal(err)
	}
	lg.Debug().Msg("hello")
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("console output %q missing message", buf.String())
	}
}

func TestNewErrors(t *testing.T) {
	var buf bytes.Buffer
	if _, err := New(Options{Level: "nope", Writer: &buf}); err == nil {
		t.Error("expected level error")
	}
	if _, err := New(Options{Format: "nope", Writer: &buf}); err == nil {
		t.Error("expected format error")
	}
	if _, err := New(Options{}); err == nil {
		t.Error("expected missing writer error")
	}
}

func TestNop(t *testing.T) {
	lg := Nop()
	lg.Info().Msg("must not panic")
}
