package exec

import (
	"errors"
	"fmt"
	htmltemplate "html/template"
	"strings"
	"testing"
	texttemplate "text/template"
	"time"
)

// The tests hold this executor to the standard library's: every template in
// the corpus runs through text/template (or html/template, for the escaping
// cases) and through exec, and the two have to agree on the output and on
// whether there was an error. The standard executors are reflection's; this
// one is the reason the package exists.

var when = time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)

// data is the shape a crier template sees: what a YAML or JSON document
// decodes into, plus a time and the typed slices the pipeline injects.
func data() map[string]any {
	return map[string]any{
		"a": 1, "f": 1.5, "s": "x&y<z>'\"+", "list": []any{"p", "q"}, "ss": []string{"p", "q"},
		"n": nil, "m": map[string]any{"k": "v", "z": map[string]any{"deep": 3}}, "b": true,
		"big": int64(7), "u": uint8(3), "t": when, "e": map[string]any{}, "el": []any{},
		"ints": []any{3, 1, 2}, "neg": -4, "zero": 0, "str0": "",
		"items":  []any{map[string]any{"name": "one", "n": 1}, map[string]any{"name": "two", "n": 2}},
		"nested": map[string]any{"list": []any{[]any{1, 2}, []any{3}}},
	}
}

// stdFuncs and execFuncs are the same user functions, in each executor's
// form. They stand in for crier's function set: a few shapes of signature,
// including a variadic one, one returning an error, and one taking a slice.
func stdFuncs() texttemplate.FuncMap {
	return texttemplate.FuncMap{
		"upper": strings.ToUpper,
		"add":   func(a, b int) int { return a + b },
		"join":  func(sep string, items []string) string { return strings.Join(items, sep) },
		"pair":  func(k string, v any) map[string]any { return map[string]any{k: v} },
		"fail":  func(s string) (string, error) { return "", fmt.Errorf("failed on %s", s) },
		"many":  func(items ...any) int { return len(items) },
		"tick":  func() time.Time { return when },
		"half":  func(f float64) float64 { return f / 2 },
	}
}

func execFuncs() FuncMap {
	return FuncMap{
		"upper": func(args []any) (any, error) {
			if err := Arity("upper", args, 1); err != nil {
				return nil, err
			}
			s, err := StringArg(args, 0)
			if err != nil {
				return nil, err
			}
			return strings.ToUpper(s), nil
		},
		"add": func(args []any) (any, error) {
			if err := Arity("add", args, 2); err != nil {
				return nil, err
			}
			a, err := IntArg(args, 0)
			if err != nil {
				return nil, err
			}
			b, err := IntArg(args, 1)
			if err != nil {
				return nil, err
			}
			return a + b, nil
		},
		"join": func(args []any) (any, error) {
			if err := Arity("join", args, 2); err != nil {
				return nil, err
			}
			sep, err := StringArg(args, 0)
			if err != nil {
				return nil, err
			}
			items, err := StringsArg(args, 1)
			if err != nil {
				return nil, err
			}
			return strings.Join(items, sep), nil
		},
		"pair": func(args []any) (any, error) {
			if err := Arity("pair", args, 2); err != nil {
				return nil, err
			}
			k, err := StringArg(args, 0)
			if err != nil {
				return nil, err
			}
			return map[string]any{k: args[1]}, nil
		},
		"fail": func(args []any) (any, error) {
			if err := Arity("fail", args, 1); err != nil {
				return nil, err
			}
			s, err := StringArg(args, 0)
			if err != nil {
				return nil, err
			}
			return "", fmt.Errorf("failed on %s", s)
		},
		"many": func(args []any) (any, error) { return len(args), nil },
		"tick": func(args []any) (any, error) {
			if err := Arity("tick", args, 0); err != nil {
				return nil, err
			}
			return when, nil
		},
		"half": func(args []any) (any, error) {
			if err := Arity("half", args, 1); err != nil {
				return nil, err
			}
			f, err := FloatArg(args, 0)
			if err != nil {
				return nil, err
			}
			return f / 2, nil
		},
	}
}

// corpus is every template shape the executor has to get right. Each is run
// against data() unless a case names its own.
var corpus = []string{
	// Fields, chains, printing.
	`{{ .a }}|{{ .f }}|{{ .s }}|{{ .b }}|{{ .big }}|{{ .u }}|{{ .t }}|{{ .list }}|{{ .ss }}|{{ .m }}`,
	`{{ .missing }}|[{{ .n }}]|{{ .missing.x }}|{{ .m.k }}|{{ .m.zz }}|{{ .m.z.deep }}|{{ (index . "m").k }}`,
	`{{ . }}`,
	`{{ $ }}`,
	`{{ .e }}|{{ .el }}|{{ .zero }}|{{ .str0 }}|{{ .neg }}`,
	// Constants.
	`{{ 3 }} {{ 1.0 }} {{ 0.1 }} {{ 1e3 }} {{ 1e21 }} {{ -2 }} {{ 0x10 }} {{ "s" }} {{ true }} {{ 'a' }} {{ 1.5e-3 }}`,
	`{{ nil }}`,
	`{{ "a" "b" }}`,
	`{{ .missing "arg" }}`,
	`{{ (.a) "b" }}`,
	// Truth.
	`{{ if .missing }}y{{ else }}n{{ end }}{{ if .n }}y{{ else }}n{{ end }}{{ if .a }}y{{ end }}{{ if .zero }}y{{ else }}n{{ end }}`,
	`{{ if .e }}E{{ end }}{{ if .el }}L{{ end }}{{ if .u }}U{{ end }}{{ if .t }}T{{ end }}{{ if 0.0 }}Z{{ end }}{{ if .str0 }}S{{ end }}{{ if .list }}I{{ end }}`,
	`{{ with .missing }}y{{ else }}n{{ end }}{{ with .m }}{{ .k }}{{ end }}{{ with .n }}y{{ else with .a }}{{ . }}{{ end }}`,
	`{{ if .missing }}a{{ else if .zero }}b{{ else if .a }}c{{ else }}d{{ end }}`,
	// Comparisons.
	`{{ eq .a 1 }} {{ eq .a 2 }} {{ eq .a 1 2 3 }} {{ ne .a 2 }} {{ eq .s "x" "x&y<z>'\"+" }} {{ eq .b true }}`,
	`{{ eq .a 1.0 }}`,
	`{{ lt .a .f }}`,
	`{{ eq .missing nil }} {{ eq .n nil }} {{ eq .a .missing }} {{ eq .missing .n }} {{ eq nil nil }}`,
	`{{ lt .a 2 }} {{ le .a 1 }} {{ gt .a 0 }} {{ ge .a 1 }} {{ lt "a" "b" }} {{ gt .f 1.2 }} {{ lt .neg .zero }}`,
	`{{ ge .big .a }} {{ le .u .a }}`,
	`{{ ne .f .a }}`,
	`{{ eq 1 "a" }}`,
	`{{ lt .s .missing }}`,
	`{{ lt .b true }}`,
	`{{ eq .list .list }}`,
	`{{ eq .t .t }} {{ eq .m .m }}`,
	`{{ eq .a }}`,
	`{{ lt .a }}`,
	`{{ eq .u 3 }} {{ eq 3 .u }} {{ lt .u 4 }} {{ lt 2 .u }} {{ eq .neg .u }} {{ lt .neg .u }}`,
	// Builtins.
	`{{ len .list }} {{ len .m }} {{ len .s }} {{ len .ss }} {{ len .e }}`,
	`{{ len .missing }}`,
	`{{ len 5 }}`,
	`{{ index .m "k" }}|{{ index .m "nope" }}|{{ index .list 1 }}|{{ index .ss 0 }}|{{ index .s 0 }}|{{ index .nested.list 0 1 }}|{{ index .m "z" "deep" }}`,
	`{{ index .list 5 }}`,
	`{{ index .list -1 }}`,
	`{{ index .list "a" }}`,
	`{{ index .m 1 }}`,
	`{{ index .missing 1 }}`,
	`{{ index .a 1 }}`,
	`{{ slice .s 1 3 }}|{{ slice .list 1 }}|{{ slice .ss 0 1 }}|{{ slice .list }}|{{ slice .ints 1 2 3 }}|{{ slice .s 2 }}`,
	`{{ slice .s 1 3 3 }}`,
	`{{ slice .list 2 1 }}`,
	`{{ slice .list 0 5 }}`,
	`{{ print .a .f .b }}|{{ println .a }}|{{ printf "%05.1f|%s|%v|%d" .f .s .list .a }}|{{ print "a" "b" 1 }}|{{ printf "%v" .n }}|{{ printf "%q" .s }}`,
	`{{ printf }}`,
	`{{ printf 1 }}`,
	`{{ or .missing .n "" 0 "x" }} {{ and .a .b .s }} {{ not .missing }} {{ not .a }} {{ or .missing .zero }} {{ and .a .zero .s }} {{ and .a }} {{ or .missing }}`,
	`{{ and }}`,
	`{{ or }}`,
	`{{ not }}`,
	`{{ not .a .b }}`,
	`{{ if and .a (eq .a 1) }}ok{{ end }}{{ if or .missing (gt .f 1.0) }}ok2{{ end }}`,
	`{{ .a | printf "%d!" }} {{ "q" | printf "%s-%s" "p" }} {{ .a | and .zero }} {{ .missing | or "d" }} {{ .s | len }} {{ .list | index 1 }}`,
	`{{ html .s }} {{ js .s }} {{ urlquery .s }} {{ html .a .s }} {{ js "a\tb\u2028" }}`,
	`{{ call .missing }}`,
	`{{ call .a }}`,
	// User functions.
	`{{ upper .s }} {{ add .a 2 }} {{ add 1 2 }} {{ join "," .ss }} {{ (pair "x" .a).x }} {{ many }} {{ many 1 2 3 }} {{ many .list }} {{ half 3 }} {{ half .f }}`,
	`{{ fail "here" }}`,
	`{{ upper }}`,
	`{{ upper 1 }}`,
	`{{ upper .a }}`,
	`{{ add "a" 1 }}`,
	`{{ add .f 1 }}`,
	`{{ tick.Year }} {{ (tick).Format "2006-01-02" }} {{ tick.IsZero }} {{ tick.Month }} {{ tick.Weekday }}`,
	`{{ nope .a }}`,
	`{{ .s | upper }} {{ .ss | join "-" }} {{ 4 | add 1 }} {{ .a | add 1 | add 1 }}`,
	// Methods on values.
	`{{ .t.Year }} {{ .t.IsZero }} {{ .t.Month }} {{ .t.Day }} {{ .t.Weekday }} {{ .t.Unix }} {{ .t.YearDay }} {{ .t.Hour }} {{ .t.Minute }} {{ .t.Second }}`,
	`{{ .t.Format "2006-01-02" }} {{ (.t.AddDate 1 0 0).Year }} {{ .t.Before .t }} {{ .t.Equal .t }} {{ (.t.UTC).String }} {{ .t.Nanosecond }}`,
	`{{ .t.Format }}`,
	`{{ .t.Format 1 }}`,
	`{{ .t.Nope }}`,
	`{{ .a.x }}`,
	`{{ .list.x }}`,
	`{{ .m.k.z }}`,
	`{{ .s.Len }}`,
	// Variables.
	`{{ $x := .a }}{{ $x = 5 }}{{ $x }}{{ $y := "s" }}{{ $y }}{{ $ }}{{ $.a }}`,
	`{{ $x := 1 }}{{ if true }}{{ $x = 2 }}{{ end }}{{ $x }}`,
	`{{ $x := 1 }}{{ if true }}{{ $x := 2 }}{{ $x }}{{ end }}{{ $x }}`,
	`{{ $m := .m }}{{ $m.k }}{{ $m.z.deep }}{{ ($m).k }}`,
	`{{ $y }}`,
	`{{ $v := .missing }}[{{ $v }}]{{ if $v }}y{{ else }}n{{ end }}`,
	// Range.
	`{{ range .list }}{{ . }},{{ end }}|{{ range .ss }}{{ . }},{{ end }}|{{ range .m }}{{ . }},{{ end }}|{{ range $k, $v := .m }}{{ $k }}={{ $v }},{{ end }}|{{ range $i, $v := .list }}{{ $i }}={{ $v }},{{ end }}|{{ range $v := .list }}{{ $v }},{{ end }}`,
	`{{ range .missing }}y{{ else }}none{{ end }}|{{ range .n }}y{{ else }}none{{ end }}|{{ range .el }}y{{ else }}none{{ end }}|{{ range .e }}y{{ else }}none{{ end }}`,
	`{{ range 3 }}{{ . }}{{ end }}|{{ range $i := 2 }}{{ $i }}{{ end }}|{{ range $i, $v := 2 }}{{ $i }}{{ $v }}{{ end }}|{{ range .a }}{{ . }}{{ end }}|{{ range .zero }}x{{ else }}z{{ end }}|{{ range .neg }}x{{ else }}z{{ end }}`,
	`{{ range .list }}{{ if eq . "q" }}{{ break }}{{ end }}{{ . }}{{ end }}|{{ range .ints }}{{ if eq . 1 }}{{ continue }}{{ end }}{{ . }}{{ end }}`,
	`{{ range .items }}{{ .name }}:{{ .n }} {{ end }}{{ range .nested.list }}[{{ range . }}{{ . }}{{ end }}]{{ end }}`,
	`{{ range $i, $e := .items }}{{ $i }}{{ $e.name }}{{ $.a }}{{ end }}`,
	`{{ $x := 0 }}{{ range .ints }}{{ $x = add $x . }}{{ end }}{{ $x }}`,
	`{{ $i := 9 }}{{ $v := 9 }}{{ range $i, $v = .list }}{{ $i }}{{ $v }}{{ end }}{{ $i }}{{ $v }}`,
	`{{ range .s }}{{ end }}`,
	`{{ range .f }}{{ end }}`,
	`{{ range .t }}{{ end }}`,
	`{{ range .b }}{{ end }}`,
	// define, template, block.
	`{{ template "x" . }}{{ define "x" }}[{{ .a }}]{{ end }}`,
	`{{ block "b" .a }}<{{ . }}>{{ end }}|{{ template "b" .f }}`,
	`{{ define "row" }}{{ .name }}={{ .n }};{{ end }}{{ range .items }}{{ template "row" . }}{{ end }}`,
	`{{ template "nope" }}`,
	`{{ template "x" }}{{ define "x" }}{{ . }}{{ end }}`,
	`{{ $v := 1 }}{{ template "x" . }}{{ define "x" }}{{ $v }}{{ end }}`,
	`{{ define "x" }}a{{ end }}{{ define "x" }}b{{ end }}{{ template "x" }}`,
	// Whitespace and comments.
	"a  {{- .a -}}  b {{/* comment */}} c {{- /* another */ -}} d\n{{ .b }}\n",
	`{{ .a }}{{ "literal" }}` + "\t" + `{{ 1 }}`,
	// Text with delimiters-ish content.
	`{ .a } {{ "{{" }} }}`,
}

// missingKeyCorpus runs under missingkey=error, where a lookup that finds
// nothing is a failure.
var missingKeyCorpus = []string{
	`{{ .nope }}`,
	`{{ .a }}`,
	`{{ .m.k }}`,
	`{{ .m.nope }}`,
	`{{ .a.b }}`,
	`{{ if .nope }}y{{ end }}`,
	`{{ if .a }}y{{ end }}`,
	`{{ index .m "nope" }}`,
	`{{ .n }}`,
	`{{ .n.x }}`,
	`{{ range .nope }}{{ end }}`,
	`{{ with .nope }}{{ end }}`,
	`{{ eq .nope 1 }}`,
	`{{ .missing.x }}`,
}

// TestAgreesWithTextTemplate is the differential test over the corpus.
func TestAgreesWithTextTemplate(t *testing.T) {
	for _, src := range corpus {
		compareText(t, src, data(), "")
	}
	for _, src := range missingKeyCorpus {
		compareText(t, src, data(), "missingkey=error")
	}
}

// TestAgreesOnOtherData runs the corpus against a nil document, a list and a
// scalar, which is what a template gets when the data file is not an object.
func TestAgreesOnOtherData(t *testing.T) {
	for _, doc := range []any{nil, []any{"a", "b"}, "scalar", 42, map[string]string{"a": "b"}} {
		for _, src := range []string{
			`{{ . }}`, `{{ .x }}`, `{{ .x.y }}`, `{{ if . }}y{{ else }}n{{ end }}`, `{{ range . }}{{ . }}{{ end }}`,
			`{{ len . }}`, `{{ index . 0 }}`, `{{ index . "a" }}`, `{{ with . }}{{ . }}{{ end }}`, `{{ printf "%v" . }}`,
			`{{ eq . nil }}`, `{{ $ }}`, `{{ .a }}`, `{{ template "x" . }}{{ define "x" }}{{ . }}{{ end }}`,
		} {
			compareText(t, src, doc, "")
			compareText(t, src, doc, "missingkey=error")
		}
	}
}

func compareText(t *testing.T, src string, doc any, option string) {
	t.Helper()
	want, wantErr := runStdText(src, doc, option)
	got, gotErr := runExec(src, doc, option, false)
	report(t, "text", src, option, want, wantErr, got, gotErr)
}

func report(t *testing.T, kind, src, option, want string, wantErr error, got string, gotErr error) {
	t.Helper()
	switch {
	case (wantErr != nil) != (gotErr != nil):
		t.Errorf("%s %q %s: stdlib err=%v out=%q; exec err=%v out=%q", kind, src, option, wantErr, want, gotErr, got)
	case wantErr == nil && want != got:
		t.Errorf("%s %q %s:\n stdlib %q\n exec   %q", kind, src, option, want, got)
	case wantErr != nil && !t.Failed() && testing.Verbose():
		t.Logf("%s %q: both fail\n stdlib %v\n exec   %v", kind, src, wantErr, gotErr)
	}
}

func runStdText(src string, doc any, option string) (string, error) {
	tmpl := texttemplate.New("t").Funcs(stdFuncs())
	if option != "" {
		tmpl = tmpl.Option(option)
	}
	tmpl, err := tmpl.Parse(src)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	err = tmpl.Execute(&out, doc)
	return out.String(), err
}

func runExec(src string, doc any, option string, html bool) (string, error) {
	tmpl := New("t").Funcs(execFuncs())
	if option != "" {
		tmpl = tmpl.Option(option)
	}
	if html {
		tmpl = tmpl.HTML()
	}
	if _, err := tmpl.Parse(src); err != nil {
		return "", err
	}
	var out strings.Builder
	err := tmpl.Execute(&out, doc)
	return out.String(), err
}

// htmlCorpus is what html/template escapes in text content, which is the
// one context this executor implements. Attribute, URL, CSS and script
// contexts are deliberately absent: see the package comment.
var htmlCorpus = []string{
	`<p>{{ .s }}</p>`,
	`{{ .missing }}|[{{ .n }}]|{{ .a }}|{{ .f }}|{{ .t }}|{{ .list }}|{{ .m }}|{{ .b }}`,
	`{{ . }}`,
	`{{ html .s }}|{{ .s | html }}|{{ js .s }}|{{ urlquery .s }}|{{ printf "%v" .n }}|{{ printf "<%s>" .s }}`,
	`{{ if .missing }}y{{ else }}<n>{{ end }}<b class="x">{{ .s | upper }}</b>`,
	`{{ range .list }}<li>{{ . }}</li>{{ end }}{{ range $k, $v := .m }}{{ $k }}{{ end }}`,
	`{{ define "x" }}<i>{{ .s }}</i>{{ end }}{{ template "x" . }}{{ block "y" .a }}{{ . }}{{ end }}`,
	`<p style="color: {{ .a }}">{{ .a }}</p>`,
	`<p style="color: #{{ printf "%02x" .a }}0000">x</p>`,
	`<div title="{{ .s }}">{{ "a+b" }}</div>`,
	`{{ .s }}{{ .missing }}{{ .s }}`,
	`{{ printf "%s" .s | html }}`,
	`{{ .s | html | upper }}`,
	`{{ .a.x }}`,
	`{{ nope }}`,
	"line\n{{- .a -}}\nend",
}

// TestAgreesWithHTMLTemplate is the differential test for the escaping flag.
func TestAgreesWithHTMLTemplate(t *testing.T) {
	for _, src := range htmlCorpus {
		tmpl, err := htmltemplate.New("t").Funcs(htmltemplate.FuncMap(stdFuncs())).Parse(src)
		var want string
		var wantErr error
		if err != nil {
			wantErr = err
		} else {
			var out strings.Builder
			wantErr = tmpl.Execute(&out, data())
			want = out.String()
		}
		got, gotErr := runExec(src, data(), "", true)
		report(t, "html", src, "", want, wantErr, got, gotErr)
	}
}

// TestOverlayParse: a later Parse redefines a block, an empty define keeps
// the earlier body, and a second Parse of nothing but defines leaves the
// main template alone — the three rules overlays rest on, in both executors.
func TestOverlayParse(t *testing.T) {
	base := `<b>{{ block "title" . }}base{{ end }}</b>{{ block "body" . }}body{{ end }}`
	overlays := [][]string{
		{`{{ define "title" }}over{{ end }}`},
		{`{{ define "title" }}one{{ end }}`, `{{ define "title" }}two{{ end }}`},
		{`{{ define "title" }}{{ end }}`},
		{`   {{/* nothing */}}   `},
		{`{{ define "body" }}{{ .a }}{{ end }}`, `{{ define "extra" }}x{{ end }}`},
	}
	for _, set := range overlays {
		std := texttemplate.New("t").Funcs(stdFuncs())
		mine := New("t").Funcs(execFuncs())
		var wantErr, gotErr error
		if _, err := std.Parse(base); err != nil {
			t.Fatal(err)
		}
		if _, err := mine.Parse(base); err != nil {
			t.Fatal(err)
		}
		for _, o := range set {
			if _, err := std.Parse(o); err != nil {
				wantErr = err
			}
			if _, err := mine.Parse(o); err != nil {
				gotErr = err
			}
		}
		var want, got strings.Builder
		if wantErr == nil {
			wantErr = std.Execute(&want, data())
		}
		if gotErr == nil {
			gotErr = mine.Execute(&got, data())
		}
		report(t, "overlay", strings.Join(set, " + "), "", want.String(), wantErr, got.String(), gotErr)
	}
}

// TestErrorsCarryPositions: an execution error names the template, the line
// and column and the node, as text/template's does, so a user's error reads
// the same.
func TestErrorsCarryPositions(t *testing.T) {
	tmpl, err := New("card.html").Funcs(execFuncs()).Parse("line one\n{{ .a.b }}")
	if err != nil {
		t.Fatal(err)
	}
	err = tmpl.Execute(&strings.Builder{}, data())
	if err == nil {
		t.Fatal("expected an error")
	}
	want := `template: card.html:2:5: executing "card.html" at <.a.b>: can't evaluate field b in type int`
	if err.Error() != want {
		t.Errorf("got  %s\nwant %s", err, want)
	}
	var execErr ExecError
	if !errors.As(err, &execErr) || execErr.Name != "card.html" {
		t.Errorf("not an ExecError naming the template: %#v", err)
	}

	// A write failure comes back as the writer's own error.
	tmpl, _ = New("t").Parse("{{ .a }}")
	if err := tmpl.Execute(failingWriter{}, data()); err == nil || err.Error() != "boom" {
		t.Errorf("write error = %v", err)
	}

	// Executing nothing is an error too.
	if err := New("empty").Execute(&strings.Builder{}, nil); err == nil {
		t.Error("an unparsed template executed")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

// TestOptions: the missingkey spellings text/template accepts, and the
// refusal of anything else.
func TestOptions(t *testing.T) {
	for _, opt := range []string{"missingkey=default", "missingkey=invalid", "missingkey=zero", "missingkey=error"} {
		New("t").Option(opt)
	}
	for _, opt := range []string{"missingkey=nope", "nope=1", "nope"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%q should have panicked", opt)
				}
			}()
			New("t").Option(opt)
		}()
	}
	tmpl, _ := New("t").Option("missingkey=zero").Parse(`[{{ .nope }}]`)
	var out strings.Builder
	if err := tmpl.Execute(&out, data()); err != nil || out.String() != "[<no value>]" {
		t.Errorf("zero: %q %v", out.String(), err)
	}
}

// TestDefinedTemplates: the listing text/template puts in its errors.
func TestDefinedTemplates(t *testing.T) {
	tmpl := New("t")
	if got := tmpl.DefinedTemplates(); got != "" {
		t.Errorf("empty set listed as %q", got)
	}
	if _, err := tmpl.Parse(`{{ define "a" }}x{{ end }}`); err != nil {
		t.Fatal(err)
	}
	if !tmpl.Lookup("a") || !tmpl.Lookup("t") || tmpl.Lookup("zz") {
		t.Error("lookup is wrong")
	}
	if got := tmpl.DefinedTemplates(); !strings.HasPrefix(got, "; defined templates are: ") || !strings.Contains(got, `"a"`) {
		t.Errorf("listing = %q", got)
	}
	if tmpl.Name() != "t" {
		t.Error("name")
	}
}

// TestDelims: another pair of delimiters parses.
func TestDelims(t *testing.T) {
	tmpl, err := New("t").Delims("[[", "]]").Parse(`{{ [[ .a ]] }}`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, data()); err != nil || out.String() != "{{ 1 }}" {
		t.Errorf("%q %v", out.String(), err)
	}
}

// TestEscapers pins the two escaping functions to the standard library's.
func TestEscapers(t *testing.T) {
	for _, s := range []string{"", "plain", "x&y<z>'\"+=\\", "a\tb\nc\u2028d", "\x00", "ünï", "<script>alert(1)</script>"} {
		if got, want := HTMLEscapeString(s), texttemplate.HTMLEscapeString(s); got != want {
			t.Errorf("HTMLEscapeString(%q) = %q, want %q", s, got, want)
		}
		if got, want := JSEscapeString(s), texttemplate.JSEscapeString(s); got != want {
			t.Errorf("JSEscapeString(%q) = %q, want %q", s, got, want)
		}
	}
}

// TestMaxDepth: a template that includes itself stops.
func TestMaxDepth(t *testing.T) {
	tmpl, err := New("t").Parse(`{{ define "r" }}{{ template "r" . }}{{ end }}{{ template "r" . }}`)
	if err != nil {
		t.Fatal(err)
	}
	err = tmpl.Execute(&strings.Builder{}, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeded maximum template depth") {
		t.Errorf("err = %v", err)
	}
}

// TestCallOwnFunc: call reaches a Func value, which is the one function value
// a template can hold.
func TestCallOwnFunc(t *testing.T) {
	tmpl, err := New("t").Parse(`{{ call .fn 2 }}`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	doc := map[string]any{"fn": Func(func(args []any) (any, error) { return len(args) * 10, nil })}
	if err := tmpl.Execute(&out, doc); err != nil || out.String() != "10" {
		t.Errorf("%q %v", out.String(), err)
	}
	tmpl, _ = New("t").Parse(`{{ .fn }}`)
	if err := tmpl.Execute(&strings.Builder{}, doc); err == nil {
		t.Error("a function value printed")
	}
}

// TestDeliberateWidenings: the two places this executor accepts what
// text/template refused, both of them errors turning into the obvious
// answer — an integer where a function wants a float, and a list from a data
// document where a function wants a []string.
func TestDeliberateWidenings(t *testing.T) {
	for _, src := range []string{`{{ half .a }}`, `{{ join "," .list }}`} {
		if _, err := runStdText(src, data(), ""); err == nil {
			t.Errorf("%s: text/template accepted what this test says it refuses", src)
		}
		got, err := runExec(src, data(), "", false)
		if err != nil {
			t.Errorf("%s: %v", src, err)
		}
		if src == `{{ half .a }}` && got != "0.5" || src == `{{ join "," .list }}` && got != "p,q" {
			t.Errorf("%s = %q", src, got)
		}
	}
}

// TestOtherValueTypes runs the corpus's shapes over the typed slices and maps
// a function or the pipeline can hand a template, which text/template reaches
// through reflection and this package through a case each.
func TestOtherValueTypes(t *testing.T) {
	doc := map[string]any{
		"ints": []int{3, 1}, "floats": []float64{1.5, 2}, "bools": []bool{true, false},
		"maps": []map[string]any{{"k": 1}, {"k": 2}}, "lists": [][]any{{1}, {2, 3}},
		"ms": map[string]string{"b": "2", "a": "1"}, "mi": map[string]int{"x": 1}, "mb": map[string]bool{"x": true},
		"mf": map[string]float64{"x": 1.5}, "mss": map[string][]string{"x": {"p"}}, "mla": map[string][]any{"x": {1}},
		"mm": map[string]map[string]any{"x": {"k": "v"}}, "d": 90 * time.Minute, "bytes": []byte("hi"),
		"u16": uint16(7), "i8": int8(-3), "f32": float32(0.5), "u64": uint64(9),
	}
	for _, src := range []string{
		`{{ range .ints }}{{ . }}{{ end }}|{{ range .floats }}{{ . }}{{ end }}|{{ range .bools }}{{ . }}{{ end }}|{{ range .maps }}{{ .k }}{{ end }}|{{ range .lists }}{{ len . }}{{ end }}`,
		`{{ range $k, $v := .ms }}{{ $k }}{{ $v }}{{ end }}|{{ range .mi }}{{ . }}{{ end }}|{{ range .mb }}{{ . }}{{ end }}|{{ range .mf }}{{ . }}{{ end }}|{{ range .mss }}{{ . }}{{ end }}|{{ range .mla }}{{ . }}{{ end }}|{{ range .mm }}{{ .k }}{{ end }}`,
		`{{ .ms.a }}{{ .mi.x }}{{ .mb.x }}{{ .mf.x }}{{ .mss.x }}{{ .mla.x }}{{ .mm.x.k }}{{ .ms.nope }}`,
		`{{ index .ints 1 }}{{ index .floats 0 }}{{ index .bools 1 }}{{ index .maps 1 "k" }}{{ index .lists 1 1 }}{{ index .ms "a" }}{{ index .mi "x" }}{{ index .mm "x" "k" }}`,
		`{{ slice .ints 0 1 }}{{ slice .floats 1 }}{{ slice .bools 0 1 1 }}{{ slice .maps 1 }}{{ slice .lists 0 1 }}`,
		`{{ len .ints }}{{ len .ms }}{{ len .mi }}{{ len .mb }}{{ len .mf }}{{ len .mss }}{{ len .mla }}{{ len .mm }}{{ len .bytes }}{{ len .lists }}`,
		`{{ if .ints }}a{{ end }}{{ if .ms }}b{{ end }}{{ if .mi }}c{{ end }}{{ if .u16 }}d{{ end }}{{ if .i8 }}e{{ end }}{{ if .f32 }}f{{ end }}{{ if .u64 }}g{{ end }}{{ if .d }}h{{ end }}`,
		`{{ eq .u16 7 }} {{ lt .i8 0 }} {{ eq .f32 0.5 }} {{ eq .u64 9 }} {{ lt .u16 .u64 }} {{ eq .i8 .u16 }} {{ lt .u64 .i8 }} {{ eq .d .d }}`,
		`{{ .d }} {{ .d.Minutes }} {{ .d.String }} {{ .d.Seconds }} {{ .d.Hours }} {{ .d.Milliseconds }} {{ .d.Microseconds }} {{ .d.Nanoseconds }} {{ .d.Abs }} {{ (.d.Round 3600000000000).Hours }} {{ (.d.Truncate 3600000000000).Hours }}`,
		`{{ .d.Nope }}`,
		`{{ .d.Round }}`,
		`{{ .d.Round "x" }}`,
		`{{ index .ints "x" }}`,
		`{{ index .bytes 0 }}`,
		`{{ range .u16 }}{{ . }}{{ end }}|{{ range .u64 }}{{ . }}{{ end }}|{{ range .i8 }}x{{ else }}z{{ end }}`,
		`{{ index .ints .u16 }}`,
		`{{ index .ints .u64 }}`,
		`{{ slice .ints .i8 }}`,
	} {
		compareText(t, src, doc, "")
	}
}

// TestTimeMethodsAgree runs every time method this package knows against
// text/template, which calls them through reflection.
func TestTimeMethodsAgree(t *testing.T) {
	doc := map[string]any{"t": when, "u": when.Add(time.Hour), "p": &when}
	for _, src := range []string{
		`{{ .t.IsZero }} {{ .t.Year }} {{ .t.Month }} {{ .t.Day }} {{ .t.Hour }} {{ .t.Minute }} {{ .t.Second }} {{ .t.Nanosecond }} {{ .t.YearDay }} {{ .t.Weekday }}`,
		`{{ .t.Unix }} {{ .t.UnixMilli }} {{ .t.UnixMicro }} {{ .t.UnixNano }} {{ .t.UTC }} {{ .t.String }}`,
		`{{ .t.Format "Jan 2" }} {{ .t.Before .u }} {{ .t.After .u }} {{ .t.Equal .t }} {{ .u.Sub .t }} {{ (.t.AddDate 0 1 1).Format "2006-01-02" }}`,
		`{{ (.t.Add 3600000000000).Hour }} {{ (.t.Truncate 3600000000000).Minute }} {{ (.t.Round 3600000000000).Minute }} {{ .p.Year }} {{ .p.Format "2006" }}`,
		`{{ .t.Format }}`,
		`{{ .t.Format 1 }}`,
		`{{ .t.Before 1 }}`,
		`{{ .t.AddDate 1 }}`,
		`{{ .t.AddDate "a" 1 1 }}`,
		`{{ .t.Add "x" }}`,
		`{{ .t.Year 1 }}`,
		`{{ .t.Nope }}`,
		`{{ .t.Year.x }}`,
	} {
		compareText(t, src, doc, "")
	}
}
