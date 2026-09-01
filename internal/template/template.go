// Package template turns a Go html/template file plus a data document into
// the HTML the renderer lays out.
//
// The data may come from a JSON or YAML file, or from stdin, which is what
// makes crier scriptable: a program that produces the numbers pipes them
// straight into the template that draws them.
package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// StdinName is the data path that means "read the document from stdin".
const StdinName = "-"

// MaxDataSize bounds a data document, so a mistyped path to a huge file fails
// with a clear message instead of eating memory.
const MaxDataSize = 32 << 20

// Options describes one rendering.
type Options struct {
	// Path is the base template file. Required.
	Path string
	// Overlays are template files parsed after the base one, in order. They
	// hold {{define}} blocks that replace the base layout's {{block}} sections,
	// which is how one layout produces a story for Instagram and a card for
	// Discord.
	Overlays []string
	// DataPath is a JSON or YAML file, or StdinName. Empty means no data, and
	// the template is rendered against nil.
	DataPath string
	// Stdin is where StdinName reads from. Zero value: os.Stdin.
	Stdin io.Reader
	// Extra values are merged over the loaded document when it is an object;
	// they are how the pipeline injects things the template did not come with.
	Extra map[string]any
}

// Engine renders templates.
//
// It carries the run's random source as well as the function map, so that the
// layout, the captions and every frame of a video all draw from one seeded
// stream and vary together rather than independently.
type Engine struct {
	funcs template.FuncMap
	rnd   *Rand
}

// New builds an engine with a fresh random source.
func New() *Engine { return NewWithRand(NewRand(0)) }

// NewWithRand builds an engine sharing a caller's random source.
func NewWithRand(r *Rand) *Engine {
	if r == nil {
		r = NewRand(0)
	}
	funcs := Funcs()
	for name, fn := range randomFuncs(r) {
		funcs[name] = fn
	}
	return &Engine{funcs: funcs, rnd: r}
}

// Rand is the engine's random source.
func (e *Engine) Rand() *Rand { return e.rnd }

// Funcs is the function set every crier template can use, apart from the
// random helpers, which need a source and are added by NewWithRand.
func Funcs() template.FuncMap {
	return template.FuncMap{
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
		"date": func(layout string, t time.Time) string {
			return t.Format(layout)
		},
		"default": func(fallback, value any) any {
			if isEmpty(value) {
				return fallback
			}
			return value
		},
		"dict": func(pairs ...any) (map[string]any, error) {
			if len(pairs)%2 != 0 {
				return nil, fmt.Errorf("dict wants an even number of arguments, got %d", len(pairs))
			}
			out := make(map[string]any, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key %d is %T, want a string", i, pairs[i])
				}
				out[key] = pairs[i+1]
			}
			return out, nil
		},
	}
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case int:
		return t == 0
	case float64:
		return t == 0
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	default:
		return false
	}
}

// Render executes the template against the data document and returns HTML.
func (e *Engine) Render(o Options) (string, error) {
	data, err := LoadData(o.DataPath, o.Stdin)
	if err != nil {
		return "", err
	}
	return e.RenderWith(o, data)
}

// RenderWith executes the template against a document already in hand.
//
// Rendering a video means executing the template once per frame, and reading
// the data document ninety times — or reading standard input twice at all —
// is not something a caller can do. So the document is loaded once and passed
// back in.
func (e *Engine) RenderWith(o Options, data any) (string, error) {
	if o.Path == "" {
		return "", fmt.Errorf("no template given")
	}
	body, err := os.ReadFile(o.Path)
	if err != nil {
		return "", fmt.Errorf("reading template: %w", err)
	}
	data = merge(data, o.Extra)

	tpl, err := template.New(filepath.Base(o.Path)).Funcs(e.funcs).Parse(string(body))
	if err != nil {
		return "", fmt.Errorf("parsing template %s: %w", o.Path, err)
	}
	// Later definitions win, so the overlays are parsed in the order the caller
	// listed them: global ones first, then the platform's own.
	for _, overlay := range o.Overlays {
		text, err := os.ReadFile(overlay)
		if err != nil {
			return "", fmt.Errorf("reading overlay: %w", err)
		}
		if _, err := tpl.Parse(string(text)); err != nil {
			return "", fmt.Errorf("parsing overlay %s: %w", overlay, err)
		}
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("executing template %s: %w", o.Path, err)
	}
	return out.String(), nil
}

// merge overlays extra onto an object document. A document that is not an
// object is left alone unless there is nothing else, in which case extra
// becomes the document.
func merge(data any, extra map[string]any) any {
	if len(extra) == 0 {
		return data
	}
	obj, ok := data.(map[string]any)
	if !ok {
		if data == nil {
			out := make(map[string]any, len(extra))
			for k, v := range extra {
				out[k] = v
			}
			return out
		}
		return data
	}
	out := make(map[string]any, len(obj)+len(extra))
	for k, v := range obj {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// Pick chooses one template out of a pool.
//
// A pool is how one project keeps several layouts and posts a different one
// each time without anybody choosing. The choice is made once per run, from
// the run's seeded source, so every platform variant and every video frame
// uses the same layout.
func (e *Engine) Pick(pool []string) (string, bool) {
	clean := make([]string, 0, len(pool))
	for _, p := range pool {
		if strings.TrimSpace(p) != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return "", false
	}
	i, _ := e.rnd.Choose(len(clean))
	return clean[i], true
}

// LoadData reads a JSON or YAML document.
//
// The format follows the extension for a file; a document on stdin is decoded
// as YAML, which also accepts JSON, so a script does not have to say which one
// it is producing.
func LoadData(path string, stdin io.Reader) (any, error) {
	if path == "" {
		return nil, nil
	}
	var (
		raw []byte
		err error
	)
	if path == StdinName {
		if stdin == nil {
			stdin = os.Stdin
		}
		raw, err = io.ReadAll(io.LimitReader(stdin, MaxDataSize+1))
		if err != nil {
			return nil, fmt.Errorf("reading data from stdin: %w", err)
		}
	} else {
		raw, err = readLimited(path)
		if err != nil {
			return nil, err
		}
	}
	if len(raw) > MaxDataSize {
		return nil, fmt.Errorf("data document is larger than %d bytes", MaxDataSize)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}

	if strings.EqualFold(filepath.Ext(path), ".json") {
		var out any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		return out, nil
	}

	var out any
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", displayName(path), err)
	}
	return normalise(out), nil
}

func displayName(path string) string {
	if path == StdinName {
		return "stdin"
	}
	return path
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading data: %w", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, MaxDataSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading data: %w", err)
	}
	return raw, nil
}

// normalise turns the map[any]any a YAML mapping can decode into something
// html/template can index by name.
func normalise(v any) any {
	switch t := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalise(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalise(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalise(val)
		}
		return out
	default:
		return v
	}
}
