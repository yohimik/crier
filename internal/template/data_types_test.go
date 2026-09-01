package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJSONFileNumbersStayIntegers: the same document must type-check the same
// whatever path it arrived on. encoding/json hands every number over as
// float64, so a `gt .count 0` that worked from stdin failed from count.json —
// the value now comes from the YAML decode for both, and the JSON parse is
// kept only for its error messages.
func TestJSONFileNumbersStayIntegers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte(`{"count": 2, "name": "x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadData(path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("doc = %T", doc)
	}
	if _, isInt := m["count"].(int); !isInt {
		t.Errorf("count = %T, want int like the stdin path delivers", m["count"])
	}
}

// TestJSONFileErrorsSpeakJSON: the error for a broken .json file names JSON,
// not YAML — that is what the probe parse is for.
func TestJSONFileErrorsSpeakJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte(`{"count": 2,}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadData(path, nil, nil)
	if err == nil {
		t.Fatal("a trailing comma is not JSON")
	}
	if strings.Contains(err.Error(), "yaml") {
		t.Errorf("the error blames YAML for a JSON file: %v", err)
	}
}
