package configgen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
)

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{
		"":      YAML,
		"yaml":  YAML,
		"YAML":  YAML,
		" yml ": YAML,
		"json":  JSON,
		"toml":  TOML,
	} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseFormat("ini"); err == nil {
		t.Error("ParseFormat(ini) should fail")
	}
	for _, f := range Formats {
		if f.Ext() != "."+string(f) {
			t.Errorf("%q.Ext() = %q", f, f.Ext())
		}
	}
	if Format("").Ext() != ".yaml" {
		t.Error("the empty format should extend to .yaml")
	}
}

// TestFullCoversEveryKey is the anti-drift check: the sample is a walk over the
// registry, so a key can only be missing if the walk is wrong.
func TestFullCoversEveryKey(t *testing.T) {
	for _, f := range Formats {
		body, err := Sample(Options{Format: f, Full: true})
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		text := string(body)
		for _, d := range config.Registry() {
			if !strings.Contains(text, lastSegment(d.Key)) {
				t.Errorf("%s: no line for %s", f, d.Key)
			}
		}
	}
}

// TestFullJSONIsNestedAndParses guards the one format that has no comments to
// hide a structural mistake in.
func TestFullJSONIsNestedAndParses(t *testing.T) {
	body, err := Sample(Options{Format: JSON, Full: true})
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	publish, ok := root["publish"].(map[string]any)
	if !ok {
		t.Fatalf("publish is %T, want a nested object", root["publish"])
	}
	if _, ok := publish["telegram"].(map[string]any); !ok {
		t.Errorf("publish.telegram is %T, want a nested object", publish["telegram"])
	}
	// A secret must never carry a value that looks like it could work.
	tg, _ := publish["telegram"].(map[string]any)
	if token, _ := tg["token"].(string); !strings.HasPrefix(token, "<your-") {
		t.Errorf("publish.telegram.token = %q, want a placeholder", token)
	}
}

// TestTOMLTablesComeAfterTheirScalars is the ordering rule TOML has and the
// other two formats do not: a key written after a [table] header belongs to
// that table, so a parent's own keys have to precede its sub-tables.
func TestTOMLTablesComeAfterTheirScalars(t *testing.T) {
	body, err := Sample(Options{Format: TOML, Full: true})
	if err != nil {
		t.Fatal(err)
	}
	current := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			table := line[1 : len(line)-1]
			if current != "" && strings.HasPrefix(current, table+".") {
				t.Fatalf("[%s] comes after its own sub-table [%s]", table, current)
			}
			current = table
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			if current == "" {
				t.Fatalf("a key outside any table: %q", line)
			}
		}
	}
}

// TestStarterIsSmall is the whole point of the starter: it is the file somebody
// edits, so it has to be short enough to read in one go.
func TestStarterIsSmall(t *testing.T) {
	for _, f := range Formats {
		body, err := Sample(Options{Format: f})
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(body), "\n"); n > 60 {
			t.Errorf("%s starter is %d lines; a starter that long is a reference", f, n)
		}
		for _, want := range []string{"template", "data", "telegram"} {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s starter has no %s", f, want)
			}
		}
	}
}

func TestHeaderIsUsedAsGiven(t *testing.T) {
	body, err := Sample(Options{Format: YAML, Full: true, Header: "hello\n\nworld"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "# hello\n#\n# world\n") {
		t.Errorf("header = %q", string(body)[:40])
	}
	if _, err := Sample(Options{Format: "ini"}); err == nil {
		t.Error("an unknown format should fail")
	}
}

func TestValuesAreQuoted(t *testing.T) {
	// A bare - is YAML for a list item and 127.0.0.1:0 is YAML for a map, so
	// both have to come out quoted or the sample means something else.
	if got := yamlValue("-"); got != `"-"` {
		t.Errorf("yamlValue(-) = %s", got)
	}
	if got := yamlValue("127.0.0.1:0"); got != `"127.0.0.1:0"` {
		t.Errorf("yamlValue(addr) = %s", got)
	}
	if got := yamlValue([]string{}); got != "[]" {
		t.Errorf("empty list = %s", got)
	}
	if got := yamlValue([]string{"a", `b"c`}); got != `["a", "b\"c"]` {
		t.Errorf("list = %s", got)
	}
	if got := yamlValue(true); got != "true" {
		t.Errorf("bool = %s", got)
	}
	if got := yamlValue(7); got != "7" {
		t.Errorf("int = %s", got)
	}
	if got := yamlValue(1.5); got != `"1.5"` {
		t.Errorf("float = %s", got)
	}
}
