package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDataFromEnvMappingRules is the whole contract of the source, and it is
// deliberately dull: strip, lower-case, keep the underscores.
func TestDataFromEnvMappingRules(t *testing.T) {
	env := []string{
		"CARD_TITLE=crier ships v1",
		"CARD_MAIN_TITLE=the headline",
		"CARD_SUBTITLE=One template, ten platforms.",
		// Mixed case in the tail is lower-cased; the prefix itself is matched
		// as written.
		"CARD_Mixed_Case=kept",
		// An empty value is a value: a template asking for it gets "".
		"CARD_EMPTY=",
		// Not the prefix.
		"CARDBOARD=no",
		"OTHER_TITLE=no",
		"PATH=/usr/bin",
		// The prefix and nothing else is not a name.
		"CARD_=no",
		// Not a pair at all.
		"MALFORMED",
	}

	got := DataFromEnv("CARD_", env)
	want := map[string]any{
		"title":      "crier ships v1",
		"main_title": "the headline",
		"subtitle":   "One template, ten platforms.",
		"mixed_case": "kept",
		"empty":      "",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
}

// TestDataFromEnvKeepsValuesVerbatim: a template overwhelmingly prints what it
// is given, and a value that silently became a number would render 1.0 where
// "1.0" was written.
func TestDataFromEnvKeepsValuesVerbatim(t *testing.T) {
	got := DataFromEnv("V_", []string{
		"V_NUMBER=1.0",
		"V_INT=007",
		"V_BOOL=true",
		"V_NULL=null",
		"V_DATE=2026-09-01",
		"V_YAMLISH={a: 1}",
		"V_EQUALS=a=b=c",
	})
	for key, want := range map[string]string{
		"number": "1.0", "int": "007", "bool": "true", "null": "null",
		"date": "2026-09-01", "yamlish": "{a: 1}",
		// Only the first = separates the name from the value.
		"equals": "a=b=c",
	} {
		if got[key] != want {
			t.Errorf("%s = %#v, want the string %q", key, got[key], want)
		}
		if _, ok := got[key].(string); !ok {
			t.Errorf("%s is %T, want a string", key, got[key])
		}
	}
}

// TestDataFromEnvEmptyIsAMapNotAnError: a prefix matching nothing renders a
// card full of blanks, which is a warning's job rather than a failure's.
func TestDataFromEnvEmptyIsAMapNotAnError(t *testing.T) {
	got, err := LoadData("env:NOTHING_", nil, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatalf("an empty match should not be an error: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want a map", got)
	}
	if len(m) != 0 {
		t.Errorf("got %v, want an empty map", m)
	}
}

// TestDataFromEnvLastOccurrenceWins matches how a process environment is read
// everywhere else.
func TestDataFromEnvLastOccurrenceWins(t *testing.T) {
	got := DataFromEnv("X_", []string{"X_A=first", "X_A=second"})
	if got["a"] != "second" {
		t.Errorf("a = %#v, want the last occurrence", got["a"])
	}
}

func TestEnvPrefixOf(t *testing.T) {
	for spec, want := range map[string]string{
		"env:CARD_": "CARD_",
		"env:X":     "X",
		"env:":      "",
	} {
		got, ok := EnvPrefixOf(spec)
		if !ok || got != want {
			t.Errorf("EnvPrefixOf(%q) = %q, %t; want %q", spec, got, ok, want)
		}
	}
	for _, spec := range []string{"data.yaml", "-", "", "environment:X", "./env:X"} {
		if _, ok := EnvPrefixOf(spec); ok {
			t.Errorf("EnvPrefixOf(%q) said it was an env source", spec)
		}
	}
}

// TestCheckEnvPrefixRefusesTheWholeEnvironment: `env:` on its own would hand
// the template PATH, the shell, and whatever CI exports.
func TestCheckEnvPrefixRefusesTheWholeEnvironment(t *testing.T) {
	for _, spec := range []string{"env:", "env:   "} {
		err := CheckEnvPrefix(spec)
		if err == nil {
			t.Errorf("CheckEnvPrefix(%q) should refuse it", spec)
			continue
		}
		if !strings.Contains(err.Error(), "env:CARD_") {
			t.Errorf("the error should show what one looks like: %v", err)
		}
	}
	for _, spec := range []string{"env:CARD_", "data.yaml", "-", ""} {
		if err := CheckEnvPrefix(spec); err != nil {
			t.Errorf("CheckEnvPrefix(%q) = %v", spec, err)
		}
	}
}

func TestEnvNames(t *testing.T) {
	env := []string{"CARD_B=2", "CARD_A=1", "OTHER=x", "CARD_=skip", "MALFORMED"}
	got := EnvNames("CARD_", env)
	if strings.Join(got, ",") != "CARD_A,CARD_B" {
		t.Errorf("= %v, want the matches sorted", got)
	}
	if len(EnvNames("NOPE_", env)) != 0 {
		t.Error("a prefix matching nothing lists nothing")
	}
}

// TestEnvDataRendersATemplate is the source used the way a project uses it.
func TestEnvDataRendersATemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.html")
	if err := os.WriteFile(path, []byte(`<h1>{{ .title }}</h1><p>{{ .main_title }}</p>`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := New().Render(Options{
		Path:     path,
		DataPath: "env:CARD_",
		Environ:  []string{"CARD_TITLE=hello", "CARD_MAIN_TITLE=world", "PATH=/usr/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "<h1>hello</h1><p>world</p>" {
		t.Errorf("= %q", out)
	}
}

// TestEnvDataReachesACaption: the caption sees the same document the layout
// does, which is what makes one source enough.
func TestEnvDataReachesACaption(t *testing.T) {
	e := New()
	data, err := LoadData("env:CARD_", nil, []string{"CARD_TITLE=a release", "CARD_VERSION=2.0"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.RenderCaption(`{{ .title }} {{ .version }} on {{ .Platform }}`, data, "slack")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a release 2.0 on slack" {
		t.Errorf("= %q", got)
	}
}

// TestEnvDataIsNotNested says out loud what the source cannot do: a flat
// namespace has no way to say whether CARD_MAIN_TITLE is main.title or
// main_title, so it never guesses.
func TestEnvDataIsNotNested(t *testing.T) {
	got := DataFromEnv("CARD_", []string{"CARD_MAIN_TITLE=x"})
	if _, nested := got["main"]; nested {
		t.Error("the source invented a nested object")
	}
	if got["main_title"] != "x" {
		t.Errorf("= %v", got)
	}
}
