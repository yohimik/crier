// Package template turns a Go template file plus a data document into the
// HTML the renderer lays out.
//
// The data may come from a JSON or YAML file, or from stdin, which is what
// makes crier scriptable: a program that produces the numbers pipes them
// straight into the template that draws them.
//
// The templates are Go's — the grammar of text/template and html/template,
// parsed by the standard parser — executed by the exec package beside this
// one, which walks the parse tree over plain values without reflection. See
// that package for what it keeps of html/template (every value HTML-escaped
// as text content) and what it leaves out (the URL, CSS and script contexts a
// browser would need; crier's renderer is not one).
package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yohimik/crier/internal/template/exec"
)

// StdinName is the data path that means "read the document from stdin".
const StdinName = "-"

// EnvScheme introduces a data source built from environment variables:
// `env:CARD_` reads every CARD_* variable.
//
// It is a scheme rather than another key because render.data answers one
// question — where the values come from — and answering it in two places is
// how a configuration ends up with a file and an environment both half set.
const EnvScheme = "env:"

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
	// DataPath is a JSON or YAML file, StdinName, or an EnvScheme prefix.
	// Empty means no data, and the template is rendered against nil.
	DataPath string
	// Stdin is where StdinName reads from. Zero value: os.Stdin.
	Stdin io.Reader
	// Environ is where EnvScheme reads from, as KEY=value pairs. A nil slice
	// means os.Environ(); a non-nil empty slice means an empty environment,
	// which is what lets a test say "nothing is set" rather than "whatever
	// this machine happens to have".
	Environ []string
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
	funcs exec.FuncMap
	rnd   *Rand
}

// New builds an engine with a fresh random source.
func New() *Engine { return NewWithRand(NewRand(0)) }

// NewWithRand builds an engine sharing a caller's random source.
func NewWithRand(r *Rand) *Engine {
	if r == nil {
		r = NewRand(0)
	}
	return &Engine{funcs: Funcs(), rnd: r}
}

// execFuncs is the function set for one execution.
//
// The random helpers are bound per execution, to a generator forked from the
// run's seed, so every execution of every template in one run sees the same
// sequence. Binding them once to a shared stream instead means the second
// execution continues where the first left off — which makes a video strobe,
// because each frame is another execution, and makes a platform variant differ
// from the base card it is supposed to be a variant of.
func (e *Engine) execFuncs() exec.FuncMap {
	out := make(exec.FuncMap, len(e.funcs)+6)
	for name, fn := range e.funcs {
		out[name] = fn
	}
	for name, fn := range randomFuncs(e.rnd.Fork()) {
		out[name] = fn
	}
	return out
}

// Rand is the engine's random source.
func (e *Engine) Rand() *Rand { return e.rnd }

// Funcs is the function set every crier template can use, apart from the
// random helpers, which need a source and are added by NewWithRand.
//
// Each entry reads its arguments through the exec package's helpers, which
// check the count and the types the way text/template's reflection did and
// fail with the same words; the one deliberate widening is join, which takes
// the list a data document decodes to as well as a []string.
func Funcs() exec.FuncMap {
	return exec.FuncMap{
		"upper": stringFunc("upper", strings.ToUpper),
		"lower": stringFunc("lower", strings.ToLower),
		"title": stringFunc("title", func(s string) string {
			if s == "" {
				return s
			}
			return strings.ToUpper(s[:1]) + s[1:]
		}),
		"trim": stringFunc("trim", strings.TrimSpace),
		"join": func(args []any) (any, error) {
			if err := exec.Arity("join", args, 2); err != nil {
				return nil, err
			}
			sep, err := exec.StringArg(args, 0)
			if err != nil {
				return nil, err
			}
			items, err := exec.StringsArg(args, 1)
			if err != nil {
				return nil, err
			}
			return strings.Join(items, sep), nil
		},
		"repeat": func(args []any) (any, error) {
			if err := exec.Arity("repeat", args, 2); err != nil {
				return nil, err
			}
			s, err := exec.StringArg(args, 0)
			if err != nil {
				return nil, err
			}
			n, err := exec.IntArg(args, 1)
			if err != nil {
				return nil, err
			}
			if n < 0 {
				return nil, fmt.Errorf("repeat: negative count %d", n)
			}
			return strings.Repeat(s, n), nil
		},
		"now": func(args []any) (any, error) {
			if err := exec.Arity("now", args, 0); err != nil {
				return nil, err
			}
			return time.Now(), nil
		},
		"date": func(args []any) (any, error) {
			if err := exec.Arity("date", args, 2); err != nil {
				return nil, err
			}
			layout, err := exec.StringArg(args, 0)
			if err != nil {
				return nil, err
			}
			t, err := exec.TimeArg(args, 1)
			if err != nil {
				return nil, err
			}
			return t.Format(layout), nil
		},
		"default": func(args []any) (any, error) {
			if err := exec.Arity("default", args, 2); err != nil {
				return nil, err
			}
			if isEmpty(args[1]) {
				return args[0], nil
			}
			return args[1], nil
		},
		"dict": func(pairs []any) (any, error) {
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

// stringFunc wraps a string-to-string function as a template function of one
// string argument.
func stringFunc(name string, fn func(string) string) exec.Func {
	return func(args []any) (any, error) {
		if err := exec.Arity(name, args, 1); err != nil {
			return nil, err
		}
		s, err := exec.StringArg(args, 0)
		if err != nil {
			return nil, err
		}
		return fn(s), nil
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
	data, err := LoadData(o.DataPath, o.Stdin, o.Environ)
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

	tpl := exec.New(filepath.Base(o.Path)).HTML().Funcs(e.execFuncs())
	if _, err := tpl.Parse(string(body)); err != nil {
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

// LoadData reads the data document a template renders against.
//
// Three sources, chosen by the value's shape: a path to a JSON or YAML file,
// "-" for standard input, or "env:PREFIX" for the environment. The format
// follows the extension for a file; a document on stdin is decoded as YAML,
// which also accepts JSON, so a script does not have to say which one it is
// producing.
func LoadData(path string, stdin io.Reader, environ []string) (any, error) {
	if path == "" {
		return nil, nil
	}
	if prefix, ok := EnvPrefixOf(path); ok {
		return DataFromEnv(prefix, environ), nil
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

	// A .json file gets a JSON parse for its error messages — a YAML error
	// about a JSON file names the wrong grammar — but the value the template
	// sees comes from the YAML decode either way. encoding/json turns every
	// number into float64 while YAML keeps integers whole, so the same
	// document would type-check differently by the path it arrived on:
	// `gt .count 0` working from stdin and failing from count.json.
	if strings.EqualFold(filepath.Ext(path), ".json") {
		var probe any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
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

// normalise turns the map[any]any a YAML mapping can decode into something a
// template can index by name.
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
