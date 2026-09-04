package template

import (
	htmltemplate "html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The engine used to be html/template with a reflective function map. This
// test renders every real template in the repository — the examples, the
// release announcement's two layouts — through the executor the engine has
// now and through html/template with the old function map, and requires the
// same bytes. The committed preview images were rendered by the old engine,
// so the same HTML is what keeps them the same images.

// oldFuncs is the function map as it was written for html/template, in its
// reflective form, kept here so the comparison is against what shipped.
func oldFuncs(r *Rand) htmltemplate.FuncMap {
	funcs := htmltemplate.FuncMap{
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"title": func(s string) string {
			if s == "" {
				return s
			}
			return strings.ToUpper(s[:1]) + s[1:]
		},
		"trim":   strings.TrimSpace,
		"join":   func(sep string, items []string) string { return strings.Join(items, sep) },
		"repeat": strings.Repeat,
		"now":    func() time.Time { return time.Now() },
		"date":   func(layout string, t time.Time) string { return t.Format(layout) },
		"default": func(fallback, value any) any {
			if isEmpty(value) {
				return fallback
			}
			return value
		},
		"dict": func(pairs ...any) (map[string]any, error) {
			out := make(map[string]any, len(pairs)/2)
			for i := 0; i+1 < len(pairs); i += 2 {
				out[pairs[i].(string)] = pairs[i+1]
			}
			return out, nil
		},
		"randChoice": func(items ...any) (any, error) {
			flat := flatten(items)
			return flat[r.IntN(len(flat))], nil
		},
		"randInt": func(minimum, maximum int) (int, error) {
			if maximum == minimum {
				return minimum, nil
			}
			return minimum + r.IntN(maximum-minimum+1), nil
		},
		"randFloat": func(minimum, maximum float64) (float64, error) {
			return minimum + r.Float64()*(maximum-minimum), nil
		},
		"randShuffle": func(items ...any) []any {
			flat := flatten(items)
			out := append([]any(nil), flat...)
			for i := len(out) - 1; i > 0; i-- {
				j := r.IntN(i + 1)
				out[i], out[j] = out[j], out[i]
			}
			return out
		},
		"randSeed": func() int64 { return r.Seed() },
	}
	return funcs
}

// renderOld is the old engine: html/template, the old function map, the same
// overlay order.
func renderOld(t *testing.T, seed int64, path string, overlays []string, data any, extra map[string]any) (string, error) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := htmltemplate.New(filepath.Base(path)).Funcs(oldFuncs(NewRand(seed).Fork())).Parse(string(body))
	if err != nil {
		return "", err
	}
	for _, overlay := range overlays {
		text, err := os.ReadFile(overlay)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tpl.Parse(string(text)); err != nil {
			return "", err
		}
	}
	var out strings.Builder
	err = tpl.Execute(&out, merge(data, extra))
	return out.String(), err
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// fixture is one real template with its data.
type fixture struct {
	name     string
	path     string
	overlays []string
	data     string // a data file, or "" for none
	extra    map[string]any
}

func fixtures(t *testing.T) []fixture {
	t.Helper()
	root := repoRoot(t)
	var out []fixture
	dirs, err := filepath.Glob(filepath.Join(root, "examples", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		templates, _ := filepath.Glob(filepath.Join(dir, "template*.html"))
		if len(templates) == 0 {
			continue
		}
		data := filepath.Join(dir, "data.yaml")
		if _, err := os.Stat(data); err != nil {
			data = ""
		}
		for _, tpl := range templates {
			out = append(out, fixture{name: filepath.Base(dir) + "/" + filepath.Base(tpl), path: tpl, data: data})
			// The pipeline injects the video frame and the platform; the
			// examples that mention them should render with them too.
			out = append(out, fixture{name: filepath.Base(dir) + "/" + filepath.Base(tpl) + "+extra", path: tpl, data: data,
				extra: map[string]any{
					"Video":    map[string]any{"Frame": 3, "Frames": 10, "Time": 0.1, "Progress": 0.3},
					"Platform": "instagram",
				}})
		}
		if overlay := filepath.Join(dir, "story-overlay.html"); len(templates) > 0 {
			if _, err := os.Stat(overlay); err == nil {
				out = append(out, fixture{name: filepath.Base(dir) + "/story", path: templates[0], overlays: []string{overlay}, data: data})
			}
		}
	}
	return out
}

func TestRealTemplatesRenderTheSameHTML(t *testing.T) {
	for _, f := range fixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			data, err := LoadData(f.data, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, seed := range []int64{1, 20260904, 777} {
				want, wantErr := renderOld(t, seed, f.path, f.overlays, data, f.extra)
				got, gotErr := NewWithRand(NewRand(seed)).RenderWith(Options{Path: f.path, Overlays: f.overlays, Extra: f.extra}, data)
				if (wantErr != nil) != (gotErr != nil) {
					t.Fatalf("seed %d: html/template err=%v, engine err=%v", seed, wantErr, gotErr)
				}
				if want != stripComments(got) {
					t.Errorf("seed %d: the engine renders different HTML than html/template did\n%s", seed, firstDifference(want, stripComments(got)))
				}
			}
		})
	}
}

// TestAnnouncementRendersTheSameHTML: the release card, both layouts, over the
// data notes.sh builds from the release variables, including a hostile one.
func TestAnnouncementRendersTheSameHTML(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	root := repoRoot(t)
	for _, env := range [][]string{
		{"DISPAT_NEW_VERSION=1.1.0", "DISPAT_VERSION=1.0.0", "DISPAT_FEATURES=a feature\nanother one", "DISPAT_FIXES=a fix"},
		{"DISPAT_NEW_VERSION=2.0.0", "DISPAT_VERSION=1.9.0", "DISPAT_FEATURES=<script>alert(1)</script> & \"quotes\" 'and' +plus+\n{{ .injected }}", "DISPAT_BREAKING=drops </style> the old <b>api</b>"},
	} {
		cmd := exec.Command("sh", filepath.Join(root, "announce", "notes.sh"))
		cmd.Env = append(os.Environ(), env...)
		cmd.Env = append(cmd.Env, "DISPAT_PACKAGE=crier")
		notes, err := cmd.Output()
		if err != nil {
			t.Fatalf("notes.sh: %v", err)
		}
		dataPath := filepath.Join(t.TempDir(), "notes.json")
		if err := os.WriteFile(dataPath, notes, 0o600); err != nil {
			t.Fatal(err)
		}
		data, err := LoadData(dataPath, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, layout := range []string{"template.html", "template-b.html"} {
			path := filepath.Join(root, "announce", layout)
			for _, seed := range []int64{487772968, 5, 99} {
				want, wantErr := renderOld(t, seed, path, nil, data, nil)
				got, gotErr := NewWithRand(NewRand(seed)).RenderWith(Options{Path: path}, data)
				if (wantErr != nil) != (gotErr != nil) {
					t.Fatalf("%s seed %d: html/template err=%v, engine err=%v", layout, seed, wantErr, gotErr)
				}
				if want != stripComments(got) {
					t.Errorf("%s seed %d: the engine renders different HTML than html/template did\n%s", layout, seed, firstDifference(want, stripComments(got)))
				}
			}
		}
	}
}

// stripComments removes what html/template removed from a template's own
// text and this engine keeps: HTML comments, and CSS comments inside a style
// element (replaced by a space). html/template drops them as a defence against a comment hiding an
// injection from its context parser; crier's renderer reads the HTML itself
// and a comment is nothing to it, so the engine leaves them where the author
// wrote them. The test compares everything else byte for byte.
func stripComments(s string) string {
	var out strings.Builder
	for {
		start := strings.Index(s, "<!--")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "-->")
		if end < 0 {
			break
		}
		out.WriteString(s[:start])
		s = s[start+end+3:]
	}
	s = out.String() + s
	out.Reset()
	for {
		open := strings.Index(s, "<style")
		if open < 0 {
			break
		}
		close := strings.Index(s[open:], "</style")
		if close < 0 {
			break
		}
		block := s[open : open+close]
		for {
			c := strings.Index(block, "/*")
			if c < 0 {
				break
			}
			e := strings.Index(block[c:], "*/")
			if e < 0 {
				break
			}
			// html/template puts one space where the comment was, so the
			// tokens on either side stay apart.
			block = block[:c] + " " + block[c+e+2:]
		}
		out.WriteString(s[:open])
		out.WriteString(block)
		s = s[open+close:]
	}
	return out.String() + s
}

// firstDifference shows where two renderings part ways.
func firstDifference(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	lo := i - 80
	if lo < 0 {
		lo = 0
	}
	cut := func(s string) string {
		hi := i + 120
		if hi > len(s) {
			hi = len(s)
		}
		return s[lo:hi]
	}
	return "html/template: ..." + cut(want) + "...\nengine:        ..." + cut(got) + "..."
}
