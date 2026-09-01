package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderWithYAMLData(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `<h1>{{ .title | upper }}</h1><ul>{{ range .items }}<li>{{ . }}</li>{{ end }}</ul>`)
	data := write(t, dir, "d.yaml", "title: hello\nitems:\n  - a\n  - b\n")

	got, err := New().Render(Options{Path: tpl, DataPath: data})
	if err != nil {
		t.Fatal(err)
	}
	want := `<h1>HELLO</h1><ul><li>a</li><li>b</li></ul>`
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRenderWithJSONData(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ .n }}`)
	data := write(t, dir, "d.json", `{"n": 42}`)
	got, err := New().Render(Options{Path: tpl, DataPath: data})
	if err != nil {
		t.Fatal(err)
	}
	if got != "42" {
		t.Errorf("got %q", got)
	}
}

func TestRenderFromStdin(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ .who }}`)
	got, err := New().Render(Options{Path: tpl, DataPath: StdinName, Stdin: strings.NewReader("who: world\n")})
	if err != nil {
		t.Fatal(err)
	}
	if got != "world" {
		t.Errorf("got %q", got)
	}
}

func TestRenderJSONFromStdin(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ .who }}`)
	got, err := New().Render(Options{Path: tpl, DataPath: StdinName, Stdin: strings.NewReader(`{"who":"json"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got != "json" {
		t.Errorf("got %q", got)
	}
}

func TestRenderWithoutData(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `<p>static</p>`)
	got, err := New().Render(Options{Path: tpl})
	if err != nil {
		t.Fatal(err)
	}
	if got != "<p>static</p>" {
		t.Errorf("got %q", got)
	}
}

func TestRenderEscapesData(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `<p>{{ .x }}</p>`)
	data := write(t, dir, "d.yaml", "x: <script>alert(1)</script>\n")
	got, err := New().Render(Options{Path: tpl, DataPath: data})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<script>") {
		t.Errorf("html/template should have escaped the value: %q", got)
	}
}

func TestRenderExtraOverlaysData(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ .a }}-{{ .b }}`)
	data := write(t, dir, "d.yaml", "a: 1\nb: 2\n")
	got, err := New().Render(Options{Path: tpl, DataPath: data, Extra: map[string]any{"b": "over"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1-over" {
		t.Errorf("got %q", got)
	}
}

func TestRenderExtraWithoutData(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ .only }}`)
	got, err := New().Render(Options{Path: tpl, Extra: map[string]any{"only": "me"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "me" {
		t.Errorf("got %q", got)
	}
}

func TestRenderExtraLeavesNonObjectAlone(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ . }}`)
	data := write(t, dir, "d.yaml", "- a\n- b\n")
	got, err := New().Render(Options{Path: tpl, DataPath: data, Extra: map[string]any{"x": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "[a b]" {
		t.Errorf("got %q", got)
	}
}

func TestFuncs(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", strings.Join([]string{
		`{{ upper "a" }}`,
		`{{ lower "B" }}`,
		`{{ title "cat" }}`,
		`{{ title "" }}`,
		`{{ trim "  x  " }}`,
		`{{ join "," .list }}`,
		`{{ repeat "ab" 2 }}`,
		`{{ default "fallback" "" }}`,
		`{{ default "fallback" "given" }}`,
		`{{ (dict "k" "v").k }}`,
		`{{ date "2006" .when }}`,
	}, "|"))
	got, err := New().Render(Options{Path: tpl, Extra: map[string]any{
		"list": []string{"p", "q"},
		"when": time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "A|b|Cat||x|p,q|abab|fallback|given|v|2026"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNowFunc(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ if now.IsZero }}zero{{ else }}set{{ end }}`)
	got, err := New().Render(Options{Path: tpl})
	if err != nil {
		t.Fatal(err)
	}
	if got != "set" {
		t.Errorf("got %q", got)
	}
}

func TestDictErrors(t *testing.T) {
	dir := t.TempDir()
	for _, body := range []string{`{{ dict "k" }}`, `{{ dict 1 2 }}`} {
		tpl := write(t, dir, "t.html", body)
		if _, err := New().Render(Options{Path: tpl}); err == nil {
			t.Errorf("%s should have failed", body)
		}
	}
}

func TestIsEmpty(t *testing.T) {
	for _, tt := range []struct {
		v    any
		want bool
	}{
		{nil, true}, {"", true}, {"x", false},
		{false, true}, {true, false},
		{0, true}, {1, false},
		{0.0, true}, {1.5, false},
		{[]any{}, true}, {[]any{1}, false},
		{map[string]any{}, true}, {map[string]any{"a": 1}, false},
		{struct{}{}, false},
	} {
		if got := isEmpty(tt.v); got != tt.want {
			t.Errorf("isEmpty(%#v) = %v", tt.v, got)
		}
	}
}

func TestRenderErrors(t *testing.T) {
	dir := t.TempDir()

	if _, err := New().Render(Options{}); err == nil {
		t.Error("expected an error with no template")
	}
	if _, err := New().Render(Options{Path: filepath.Join(dir, "nope.html")}); err == nil {
		t.Error("expected an error for a missing template")
	}

	bad := write(t, dir, "bad.html", `{{ .x `)
	if _, err := New().Render(Options{Path: bad}); err == nil {
		t.Error("expected a parse error")
	}

	boom := write(t, dir, "boom.html", `{{ .x.y }}`)
	data := write(t, dir, "d.yaml", "x: 1\n")
	if _, err := New().Render(Options{Path: boom, DataPath: data}); err == nil {
		t.Error("expected an execution error")
	}

	badData := write(t, dir, "bad.yaml", "a:\n  - b\n c\n")
	ok := write(t, dir, "ok.html", `{{ . }}`)
	if _, err := New().Render(Options{Path: ok, DataPath: badData}); err == nil {
		t.Error("expected a yaml parse error")
	}

	badJSON := write(t, dir, "bad.json", `{`)
	if _, err := New().Render(Options{Path: ok, DataPath: badJSON}); err == nil {
		t.Error("expected a json parse error")
	}

	if _, err := LoadData(filepath.Join(dir, "missing.yaml"), nil); err == nil {
		t.Error("expected a read error")
	}
}

func TestLoadDataEmptyAndNested(t *testing.T) {
	dir := t.TempDir()
	empty := write(t, dir, "empty.yaml", "   \n")
	v, err := LoadData(empty, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v != nil {
		t.Errorf("empty document should be nil, got %#v", v)
	}

	if v, err := LoadData("", nil); err != nil || v != nil {
		t.Errorf("no path means no data: %v %v", v, err)
	}

	nested := write(t, dir, "n.yaml", "a:\n  1: one\n  b:\n    - c: d\n")
	v, err = LoadData(nested, nil)
	if err != nil {
		t.Fatal(err)
	}
	top, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want map[string]any, got %T", v)
	}
	inner, ok := top["a"].(map[string]any)
	if !ok {
		t.Fatalf("want a nested map[string]any, got %T", top["a"])
	}
	if inner["1"] != "one" {
		t.Errorf("non-string keys should be stringified: %#v", inner)
	}
	list, ok := inner["b"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("want a list, got %#v", inner["b"])
	}
	if _, ok := list[0].(map[string]any); !ok {
		t.Errorf("list elements should be normalised too: %T", list[0])
	}
}

func TestLoadDataTooLarge(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, MaxDataSize+1)
	for i := range big {
		big[i] = 'a'
	}
	p := write(t, dir, "big.yaml", string(big))
	if _, err := LoadData(p, nil); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v", err)
	}
	if _, err := LoadData(StdinName, strings.NewReader(string(big))); err == nil {
		t.Error("stdin should be bounded too")
	}
}

func TestDisplayName(t *testing.T) {
	if displayName(StdinName) != "stdin" {
		t.Error("stdin")
	}
	if displayName("a.yaml") != "a.yaml" {
		t.Error("file")
	}
}
