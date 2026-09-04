// Package exec executes Go templates without reflection.
//
// It parses with text/template/parse — the standard grammar, whitespace
// trimming, comments, define, block and template — and walks the tree itself
// over plain values: maps, slices, strings, numbers, booleans and times. That
// is every value a crier template ever sees, because a data document is YAML
// or JSON and the function set is crier's own, so the reflection text/template
// uses to reach into arbitrary structs and call arbitrary functions has
// nothing to do here. Not needing it is what lets a TinyGo toolchain, whose
// reflect has no function introspection, build and run crier.
//
// The semantics are text/template's, and the package's tests hold it to them
// by running the same templates through both: the same truth rules, the same
// missing-key options, the same comparison functions with the same errors, the
// same printing (`<no value>` for a value that is not there), the same
// variable scoping and the same range order over a map's sorted keys. Where
// text/template would call a method through reflection, this package knows a
// fixed set — the value methods of time.Time and time.Duration, and String or
// Error on anything that has one — and refuses the rest with the error
// text/template gives for a field that does not exist.
//
// HTML escaping is one flag rather than html/template's context machinery:
// with it on, every action's output is escaped the way html/template escapes
// text content, and a pipeline ending in the html function is left as
// html/template leaves it. html/template's URL, CSS and JavaScript context
// rules are not implemented — the templates this package exists for are laid
// out by crier's own renderer, never served to a browser — and the difference
// is written down in the template documentation.
//
// Functions take their arguments as a slice and return a value and an error.
// The helpers in args.go read typed arguments out of the slice with the
// errors text/template would have raised, so a function is a few lines of
// checking around the call it wraps.
package exec

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/template/parse"
)

// Func is a template function: the arguments in order, the pipeline's value
// last when the function closes one, and the result with an error.
type Func func(args []any) (any, error)

// FuncMap is the function set a template can call, by name.
type FuncMap map[string]Func

// Template is a named template and the set of templates it was parsed with.
// Every Parse adds to the same set, which is how an overlay's define replaces
// a layout's block.
type Template struct {
	name    string
	trees   map[string]*parse.Tree
	funcs   FuncMap
	escape  bool
	missing missingKeyAction
	left    string
	right   string
}

// missingKeyAction is what a map lookup that finds nothing does, as
// text/template's missingkey option spells it.
type missingKeyAction int

const (
	// missingKeyInvalid prints "<no value>" (or nothing, under HTML escaping),
	// which is text/template's default.
	missingKeyInvalid missingKeyAction = iota
	// missingKeyZero prints the zero value, which for the untyped maps a data
	// document decodes into is the same as invalid.
	missingKeyZero
	// missingKeyError fails the execution.
	missingKeyError
)

// New returns a template with the given name and nothing parsed yet.
func New(name string) *Template {
	return &Template{name: name, trees: map[string]*parse.Tree{}, funcs: FuncMap{}}
}

// Name is the template's name.
func (t *Template) Name() string { return t.name }

// Funcs adds the functions to the template's set. It has to be called before
// Parse, because the parser refuses a function it has not heard of.
func (t *Template) Funcs(m FuncMap) *Template {
	for name, fn := range m {
		t.funcs[name] = fn
	}
	return t
}

// HTML turns on HTML escaping of every action's output. See the package
// comment for what that is and is not.
func (t *Template) HTML() *Template {
	t.escape = true
	return t
}

// Delims sets the action delimiters, for a template whose text is full of
// the default ones. Empty strings keep the defaults.
func (t *Template) Delims(left, right string) *Template {
	t.left, t.right = left, right
	return t
}

// Option sets options in text/template's spelling. The one that exists is
// missingkey: "missingkey=default" or "missingkey=invalid" prints "<no value>"
// for a map key that is not there, "missingkey=zero" prints the zero value,
// and "missingkey=error" fails the execution. An unknown option panics, as it
// does in text/template, because it is a programming error and not an input.
func (t *Template) Option(opts ...string) *Template {
	for _, opt := range opts {
		key, value, found := strings.Cut(opt, "=")
		if !found || key != "missingkey" {
			panic("unrecognized option: " + opt)
		}
		switch value {
		case "invalid", "default":
			t.missing = missingKeyInvalid
		case "zero":
			t.missing = missingKeyZero
		case "error":
			t.missing = missingKeyError
		default:
			panic("unrecognized option: " + opt)
		}
	}
	return t
}

// Parse parses text as the body of the template and adds every define in it
// to the set. Templates can be redefined in successive calls to Parse, before
// the first use of Execute; a definition whose body is only white space and
// comments does not replace an existing one, and neither does an empty text
// replace the template's own body. Those two rules are what let an overlay
// file carry nothing but defines.
func (t *Template) Parse(text string) (*Template, error) {
	trees, err := parse.Parse(t.name, text, t.left, t.right, t.funcNames(), builtinNames())
	if err != nil {
		return nil, err
	}
	for name, tree := range trees {
		if existing, ok := t.trees[name]; ok && existing != nil && parse.IsEmptyTree(tree.Root) {
			continue
		}
		t.trees[name] = tree
	}
	return t, nil
}

// funcNames is what the parser needs to know about the functions: that they
// exist. The values are placeholders; the parser reads only the keys.
func (t *Template) funcNames() map[string]any {
	out := make(map[string]any, len(t.funcs))
	for name := range t.funcs {
		out[name] = true
	}
	return out
}

// Lookup reports whether a template of the name is in the set.
func (t *Template) Lookup(name string) bool {
	tree, ok := t.trees[name]
	return ok && tree != nil
}

// DefinedTemplates lists the set the way text/template does in its errors:
// `; defined templates are: "a", "b"`, or nothing when there are none.
func (t *Template) DefinedTemplates() string {
	if len(t.trees) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("; defined templates are: ")
	first := true
	for name, tree := range t.trees {
		if tree == nil {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		fmt.Fprintf(&b, "%q", name)
	}
	return b.String()
}

// Execute applies the template to data and writes the output to w.
func (t *Template) Execute(w io.Writer, data any) error {
	return t.ExecuteTemplate(w, t.name, data)
}

// ExecuteTemplate applies the named template of the set to data and writes
// the output to w.
func (t *Template) ExecuteTemplate(w io.Writer, name string, data any) (err error) {
	tree := t.trees[name]
	if tree == nil || tree.Root == nil {
		return fmt.Errorf("template: %s: %q is an incomplete or empty template", t.name, name)
	}
	if t.escape {
		if err := t.checkEscapers(); err != nil {
			return err
		}
	}
	s := &state{
		tmpl: t,
		tree: tree,
		name: name,
		wr:   w,
		vars: []variable{{"$", data}},
	}
	defer s.recover(&err)
	s.walk(data, tree.Root)
	return nil
}

// checkEscapers enforces html/template's one rule about the predefined
// escapers: html and urlquery may end a pipeline and nothing else, because
// an escaper in the middle of one would be undone by whatever follows it.
// The check runs over every template of the set before an execution, as
// html/template's does, and the error reads as html/template's reads.
func (t *Template) checkEscapers() error {
	for name, tree := range t.trees {
		if tree == nil || tree.Root == nil {
			continue
		}
		if err := checkEscapersIn(name, tree.Root); err != nil {
			return err
		}
	}
	return nil
}

func checkEscapersIn(name string, node parse.Node) error {
	check := func(pipe *parse.PipeNode) error {
		if pipe == nil {
			return nil
		}
		for pos, cmd := range pipe.Cmds {
			ident, ok := cmd.Args[0].(*parse.IdentifierNode)
			if !ok {
				continue
			}
			if (ident.Ident == "html" || ident.Ident == "urlquery") && pos < len(pipe.Cmds)-1 {
				return fmt.Errorf("html/template:%s:%d: predefined escaper %q disallowed in template", name, pipe.Line, ident.Ident)
			}
		}
		return nil
	}
	var walk func(parse.Node) error
	walk = func(n parse.Node) error {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return nil
			}
			for _, child := range n.Nodes {
				if err := walk(child); err != nil {
					return err
				}
			}
		case *parse.ActionNode:
			return check(n.Pipe)
		case *parse.IfNode:
			return checkBranch(&n.BranchNode, check, walk)
		case *parse.RangeNode:
			return checkBranch(&n.BranchNode, check, walk)
		case *parse.WithNode:
			return checkBranch(&n.BranchNode, check, walk)
		case *parse.TemplateNode:
			return check(n.Pipe)
		}
		return nil
	}
	return walk(node)
}

func checkBranch(b *parse.BranchNode, check func(*parse.PipeNode) error, walk func(parse.Node) error) error {
	if err := check(b.Pipe); err != nil {
		return err
	}
	if b.List != nil {
		if err := walk(b.List); err != nil {
			return err
		}
	}
	if b.ElseList != nil {
		if err := walk(b.ElseList); err != nil {
			return err
		}
	}
	return nil
}

// ExecError is the error an execution returns, carrying the name of the
// template that was executing when it happened.
type ExecError struct {
	Name string
	Err  error
}

func (e ExecError) Error() string { return e.Err.Error() }

// Unwrap returns the underlying error.
func (e ExecError) Unwrap() error { return e.Err }

// recover turns a panic raised by errorf or a write failure back into the
// returned error. Anything else — a real runtime panic in a function — is
// re-raised, as text/template re-raises it.
func (s *state) recover(errp *error) {
	e := recover()
	if e == nil {
		return
	}
	switch err := e.(type) {
	case ExecError:
		*errp = err
	case writeError:
		*errp = err.Err
	default:
		panic(e)
	}
}

// writeError is a failed write to the output, kept apart from an execution
// error so it comes back as the writer's own error.
type writeError struct{ Err error }

// errNoComparison and its neighbours are the comparison functions' errors,
// spelled as text/template spells them.
var (
	errBadComparisonType = errors.New("invalid type for comparison")
	errBadComparison     = errors.New("incompatible types for comparison")
	errNoComparison      = errors.New("missing argument for comparison")
)
