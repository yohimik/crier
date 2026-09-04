package exec

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template/parse"
)

// maxExecDepth bounds nested template invocations, as text/template does, so
// a template that includes itself fails rather than exhausting the stack.
const maxExecDepth = 100000

// state is one execution: the template set, the tree being walked, the
// output and the variable stack.
type state struct {
	tmpl  *Template
	tree  *parse.Tree
	name  string
	wr    io.Writer
	node  parse.Node // the node being evaluated, for error positions
	vars  []variable
	depth int
}

// variable is one named value on the stack. "$" is the root data.
type variable struct {
	name  string
	value any
}

// noFinal marks the absence of a pipeline value coming into a command: the
// first command of a pipeline has none, and a nil value is a value.
type noFinalType struct{}

var noFinal any = noFinalType{}

// walkBreak and walkControl are the sentinels break and continue raise, caught
// by the range that owns them.
type walkControl int

const (
	walkBreak walkControl = iota + 1
	walkContinue
)

func (s *state) push(name string, value any) {
	s.vars = append(s.vars, variable{name, value})
}

func (s *state) mark() int { return len(s.vars) }

func (s *state) pop(mark int) { s.vars = s.vars[0:mark] }

// setVar overwrites the top-most variable of the name, which is the one in
// scope.
func (s *state) setVar(name string, value any) {
	for i := s.mark() - 1; i >= 0; i-- {
		if s.vars[i].name == name {
			s.vars[i].value = value
			return
		}
	}
	s.errorf("undefined variable: %s", name)
}

// setTopVar overwrites the n-th variable from the top of the stack, which is
// how range binds its element and index to the variables it declared.
func (s *state) setTopVar(n int, value any) {
	s.vars[len(s.vars)-n].value = value
}

func (s *state) varValue(name string) any {
	for i := s.mark() - 1; i >= 0; i-- {
		if s.vars[i].name == name {
			return s.vars[i].value
		}
	}
	s.errorf("undefined variable: %s", name)
	return nil
}

// at marks the node being evaluated, so an error can say where it happened.
func (s *state) at(node parse.Node) { s.node = node }

// doublePercent escapes a string so it can be a format, the way
// text/template does when it folds a template's own text into an error.
func doublePercent(str string) string { return strings.ReplaceAll(str, "%", "%%") }

// errorf raises an execution error at the current node, in text/template's
// format: the template's name, the line and column, the template being
// executed and the text of the node.
func (s *state) errorf(format string, args ...any) {
	name := doublePercent(s.name)
	if s.node == nil {
		format = fmt.Sprintf("template: %s: %s", name, format)
	} else {
		location, context := s.tree.ErrorContext(s.node)
		format = fmt.Sprintf("template: %s: executing %q at <%s>: %s", location, name, doublePercent(context), format)
	}
	panic(ExecError{Name: s.name, Err: fmt.Errorf(format, args...)})
}

// writeErrorf raises a failed write, which comes back as the writer's own
// error rather than an execution error.
func (s *state) writeError(err error) { panic(writeError{Err: err}) }

// walk executes one node.
func (s *state) walk(dot any, node parse.Node) {
	s.at(node)
	switch node := node.(type) {
	case *parse.ActionNode:
		val := s.evalPipeline(dot, node.Pipe)
		if len(node.Pipe.Decl) == 0 {
			s.printValue(node, node.Pipe, val)
		}
	case *parse.BreakNode:
		panic(walkBreak)
	case *parse.CommentNode:
	case *parse.ContinueNode:
		panic(walkContinue)
	case *parse.IfNode:
		s.walkIfOrWith(parse.NodeIf, dot, node.Pipe, node.List, node.ElseList)
	case *parse.ListNode:
		for _, node := range node.Nodes {
			s.walk(dot, node)
		}
	case *parse.RangeNode:
		s.walkRange(dot, node)
	case *parse.TemplateNode:
		s.walkTemplate(dot, node)
	case *parse.TextNode:
		if _, err := s.wr.Write(node.Text); err != nil {
			s.writeError(err)
		}
	case *parse.WithNode:
		s.walkIfOrWith(parse.NodeWith, dot, node.Pipe, node.List, node.ElseList)
	default:
		s.errorf("unknown node: %s", node)
	}
}

// walkIfOrWith executes if and with, which differ only in what dot becomes
// inside the body.
func (s *state) walkIfOrWith(typ parse.NodeType, dot any, pipe *parse.PipeNode, list, elseList *parse.ListNode) {
	defer s.pop(s.mark())
	val := s.evalPipeline(dot, pipe)
	truth, ok := isTrue(val)
	if !ok {
		s.errorf("if/with can't use %v", val)
	}
	if truth {
		if typ == parse.NodeWith {
			s.walk(val, list)
		} else {
			s.walk(dot, list)
		}
	} else if elseList != nil {
		s.walk(dot, elseList)
	}
}

// isTrue reports whether the value is true in text/template's sense: the
// zero of its kind is false, and so is a value that is not there, and an
// empty string, slice or map. The second result says whether the question
// could be answered at all, which for the values this package handles it
// always can be.
func isTrue(val any) (truth, ok bool) {
	switch v := val.(type) {
	case nil:
		return false, true
	case bool:
		return v, true
	case string:
		return v != "", true
	case int:
		return v != 0, true
	case int8:
		return v != 0, true
	case int16:
		return v != 0, true
	case int32:
		return v != 0, true
	case int64:
		return v != 0, true
	case uint:
		return v != 0, true
	case uint8:
		return v != 0, true
	case uint16:
		return v != 0, true
	case uint32:
		return v != 0, true
	case uint64:
		return v != 0, true
	case uintptr:
		return v != 0, true
	case float32:
		return v != 0, true
	case float64:
		return v != 0, true
	case complex64:
		return v != 0, true
	case complex128:
		return v != 0, true
	}
	if n, ok := length(val); ok {
		return n > 0, true
	}
	// A struct, a function, a time: text/template calls every one of these
	// true, and a nil pointer false. There are no pointers in a data
	// document, so nothing here is nil that is not the nil above.
	return true, true
}

// walkRange executes range over a slice, a map, an integer or nothing.
func (s *state) walkRange(dot any, r *parse.RangeNode) {
	s.at(r)
	defer func() {
		if e := recover(); e != nil && e != walkBreak {
			panic(e)
		}
	}()
	defer s.pop(s.mark())
	val := s.evalPipeline(dot, r.Pipe)
	// mark top of stack before any variables in the body are pushed.
	mark := s.mark()
	oneIteration := func(index, elem any) {
		if len(r.Pipe.Decl) > 0 {
			if r.Pipe.IsAssign {
				// With two variables, index comes first.
				// With one, we use the element.
				if len(r.Pipe.Decl) > 1 {
					s.setVar(r.Pipe.Decl[0].Ident[0], index)
				} else {
					s.setVar(r.Pipe.Decl[0].Ident[0], elem)
				}
			} else {
				// Set top var (lexically the second if there
				// are two) to the element.
				s.setTopVar(1, elem)
			}
		}
		if len(r.Pipe.Decl) > 1 {
			if r.Pipe.IsAssign {
				s.setVar(r.Pipe.Decl[1].Ident[0], elem)
			} else {
				// Set next var (lexically the first if there
				// are two) to the index.
				s.setTopVar(2, index)
			}
		}
		defer s.pop(mark)
		defer func() {
			// Consume panic(walkContinue)
			if e := recover(); e != nil && e != walkContinue {
				panic(e)
			}
		}()
		s.walk(elem, r.List)
	}
	if val != nil {
		if items, ok := elements(val); ok {
			if len(items) > 0 {
				for i, item := range items {
					oneIteration(i, item)
				}
				return
			}
		} else if keys, values, ok := entries(val); ok {
			if len(keys) > 0 {
				for i := range keys {
					oneIteration(keys[i], values[i])
				}
				return
			}
		} else if n, ok := toInt(val); ok {
			if len(r.Pipe.Decl) > 1 {
				s.errorf("can't use %v to iterate over more than one variable", val)
			}
			if n > 0 {
				for i := 0; i < n; i++ {
					oneIteration(i, i)
				}
				return
			}
		} else {
			s.errorf("range can't iterate over %v", val)
		}
	}
	if r.ElseList != nil {
		s.walk(dot, r.ElseList)
	}
}

// elements is the slice's items as values, for every slice type a data
// document, an environment or a function of this package can produce.
func elements(val any) ([]any, bool) {
	switch v := val.(type) {
	case []any:
		return v, true
	case []string:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = x
		}
		return out, true
	case []int:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = x
		}
		return out, true
	case []float64:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = x
		}
		return out, true
	case []bool:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = x
		}
		return out, true
	case []map[string]any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = x
		}
		return out, true
	case [][]any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = x
		}
		return out, true
	case []byte:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = x
		}
		return out, true
	}
	return nil, false
}

// entries is the map's keys in sorted order with their values, which is the
// order text/template ranges a map in.
func entries(val any) (keys, values []any, ok bool) {
	var names []string
	var get func(string) any
	switch m := val.(type) {
	case map[string]any:
		for k := range m {
			names = append(names, k)
		}
		get = func(k string) any { return m[k] }
	case map[string]string:
		for k := range m {
			names = append(names, k)
		}
		get = func(k string) any { return m[k] }
	case map[string]int:
		for k := range m {
			names = append(names, k)
		}
		get = func(k string) any { return m[k] }
	case map[string]bool:
		for k := range m {
			names = append(names, k)
		}
		get = func(k string) any { return m[k] }
	case map[string]float64:
		for k := range m {
			names = append(names, k)
		}
		get = func(k string) any { return m[k] }
	case map[string][]string:
		for k := range m {
			names = append(names, k)
		}
		get = func(k string) any { return m[k] }
	case map[string][]any:
		for k := range m {
			names = append(names, k)
		}
		get = func(k string) any { return m[k] }
	case map[string]map[string]any:
		for k := range m {
			names = append(names, k)
		}
		get = func(k string) any { return m[k] }
	default:
		return nil, nil, false
	}
	sort.Strings(names)
	keys = make([]any, len(names))
	values = make([]any, len(names))
	for i, k := range names {
		keys[i] = k
		values[i] = get(k)
	}
	return keys, values, true
}

// mapValue looks a key up in any of the map types this package ranges over,
// reporting whether the value is a map at all and whether the key was there.
func mapValue(val any, key string) (v any, found, isMap bool) {
	switch m := val.(type) {
	case map[string]any:
		v, found = m[key]
	case map[string]string:
		v, found = m[key]
	case map[string]int:
		v, found = m[key]
	case map[string]bool:
		v, found = m[key]
	case map[string]float64:
		v, found = m[key]
	case map[string][]string:
		v, found = m[key]
	case map[string][]any:
		v, found = m[key]
	case map[string]map[string]any:
		v, found = m[key]
	case map[any]any:
		v, found = m[key]
	default:
		return nil, false, false
	}
	return v, found, true
}

// length is the value's length, for strings, slices and maps.
func length(val any) (int, bool) {
	switch v := val.(type) {
	case string:
		return len(v), true
	case []byte:
		return len(v), true
	}
	if items, ok := elements(val); ok {
		return len(items), true
	}
	switch m := val.(type) {
	case map[string]any:
		return len(m), true
	case map[string]string:
		return len(m), true
	case map[string]int:
		return len(m), true
	case map[string]bool:
		return len(m), true
	case map[string]float64:
		return len(m), true
	case map[string][]string:
		return len(m), true
	case map[string][]any:
		return len(m), true
	case map[string]map[string]any:
		return len(m), true
	case map[any]any:
		return len(m), true
	}
	return 0, false
}

// walkTemplate executes {{template "name" pipeline}}: the named template of
// the set, with dot set to the pipeline's value and a fresh variable stack.
func (s *state) walkTemplate(dot any, t *parse.TemplateNode) {
	s.at(t)
	tree := s.tmpl.trees[t.Name]
	if tree == nil {
		s.errorf("template %q not defined", t.Name)
	}
	if s.depth == maxExecDepth {
		s.errorf("exceeded maximum template depth (%v)", maxExecDepth)
	}
	// Variables declared by the pipeline persist.
	dot = s.evalPipeline(dot, t.Pipe)
	newState := *s
	newState.depth++
	newState.tree = tree
	newState.name = t.Name
	// No dynamic scoping: template invocations inherit no variables.
	newState.vars = []variable{{"$", dot}}
	newState.walk(dot, tree.Root)
}

// evalPipeline runs the commands of a pipeline in order, each one's value the
// last argument of the next, and binds the result to the variables the
// pipeline declares or assigns.
func (s *state) evalPipeline(dot any, pipe *parse.PipeNode) (value any) {
	if pipe == nil {
		return nil
	}
	s.at(pipe)
	value = noFinal
	for _, cmd := range pipe.Cmds {
		value = s.evalCommand(dot, cmd, value)
	}
	if value == noFinal {
		value = nil
	}
	for _, variable := range pipe.Decl {
		if pipe.IsAssign {
			s.setVar(variable.Ident[0], value)
		} else {
			s.push(variable.Ident[0], value)
		}
	}
	return value
}

// notAFunction refuses arguments given to something that cannot take them.
func (s *state) notAFunction(args []parse.Node, final any) {
	if len(args) > 1 || final != noFinal {
		s.errorf("can't give argument to non-function %s", args[0])
	}
}

// evalCommand evaluates one command of a pipeline: a function call, a field
// or variable chain, a parenthesised pipeline, or a constant.
func (s *state) evalCommand(dot any, cmd *parse.CommandNode, final any) any {
	firstWord := cmd.Args[0]
	switch n := firstWord.(type) {
	case *parse.FieldNode:
		return s.evalFieldNode(dot, n, cmd.Args, final)
	case *parse.ChainNode:
		return s.evalChainNode(dot, n, cmd.Args, final)
	case *parse.IdentifierNode:
		// Must be a function.
		return s.evalFunction(dot, n, cmd, cmd.Args, final)
	case *parse.PipeNode:
		// Parenthesized pipeline. The arguments are all inside the pipeline;
		// final must be absent.
		s.notAFunction(cmd.Args, final)
		return s.evalPipeline(dot, n)
	case *parse.VariableNode:
		return s.evalVariableNode(dot, n, cmd.Args, final)
	}
	s.at(firstWord)
	s.notAFunction(cmd.Args, final)
	switch word := firstWord.(type) {
	case *parse.BoolNode:
		return word.True
	case *parse.DotNode:
		return dot
	case *parse.NilNode:
		s.errorf("nil is not a command")
	case *parse.NumberNode:
		return s.idealConstant(word)
	case *parse.StringNode:
		return word.Text
	}
	s.errorf("can't evaluate command %q", firstWord)
	return nil
}

// idealConstant is the value of a number literal, typed the way
// text/template types it: a float when it is written as one, an int when it
// fits, and an error when it does not.
func (s *state) idealConstant(constant *parse.NumberNode) any {
	s.at(constant)
	switch {
	case constant.IsComplex:
		return constant.Complex128
	case constant.IsFloat &&
		!isHexInt(constant.Text) && !isRuneInt(constant.Text) &&
		strings.ContainsAny(constant.Text, ".eEpP"):
		return constant.Float64
	case constant.IsInt:
		n := int(constant.Int64)
		if int64(n) != constant.Int64 {
			s.errorf("%s overflows int", constant.Text)
		}
		return n
	case constant.IsUint:
		s.errorf("%s overflows int", constant.Text)
	}
	return nil
}

func isRuneInt(s string) bool { return len(s) > 0 && s[0] == '\'' }

func isHexInt(s string) bool {
	return len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') && !strings.ContainsAny(s, "pP")
}

func (s *state) evalFieldNode(dot any, field *parse.FieldNode, args []parse.Node, final any) any {
	s.at(field)
	return s.evalFieldChain(dot, dot, field, field.Ident, args, final)
}

func (s *state) evalChainNode(dot any, chain *parse.ChainNode, args []parse.Node, final any) any {
	s.at(chain)
	if len(chain.Field) == 0 {
		s.errorf("internal error: no fields in evalChainNode")
	}
	if chain.Node.Type() == parse.NodeNil {
		s.errorf("indirection through explicit nil in %s", chain)
	}
	// (pipe).Field1.Field2 has pipe as .Node, fields as .Field. Eval the pipeline, then the fields.
	pipe := s.evalArg(dot, chain.Node)
	return s.evalFieldChain(dot, pipe, chain, chain.Field, args, final)
}

func (s *state) evalVariableNode(dot any, variable *parse.VariableNode, args []parse.Node, final any) any {
	// $x.Field has $x as the first ident, Field as the second. Eval the var, then the fields.
	s.at(variable)
	value := s.varValue(variable.Ident[0])
	if len(variable.Ident) == 1 {
		s.notAFunction(args, final)
		return value
	}
	return s.evalFieldChain(dot, value, variable, variable.Ident[1:], args, final)
}

// evalFieldChain evaluates .X.Y.Z possibly followed by arguments.
// dot is the environment in which to evaluate arguments, while
// receiver is the value being walked along the chain.
func (s *state) evalFieldChain(dot, receiver any, node parse.Node, ident []string, args []parse.Node, final any) any {
	n := len(ident)
	for i := 0; i < n-1; i++ {
		receiver = s.evalField(dot, ident[i], node, nil, noFinal, receiver)
	}
	// Now if it's a method, it gets the arguments.
	return s.evalField(dot, ident[n-1], node, args, final, receiver)
}

func (s *state) evalFunction(dot any, node *parse.IdentifierNode, cmd parse.Node, args []parse.Node, final any) any {
	s.at(node)
	name := node.Ident
	function, isBuiltin, ok := s.findFunction(name)
	if !ok {
		s.errorf("%q is not a defined function", name)
	}
	return s.evalCall(dot, function, isBuiltin, cmd, name, args, final)
}

// findFunction looks a function up: the template's own set first, then the
// builtins.
func (s *state) findFunction(name string) (fn Func, isBuiltin, ok bool) {
	if fn, ok := s.tmpl.funcs[name]; ok && fn != nil {
		return fn, false, true
	}
	if fn, ok := builtins[name]; ok {
		return fn, true, true
	}
	return nil, false, false
}

// evalField evaluates an expression like (.Field) or (.Field arg1 arg2).
// The 'final' argument represents the return value from the preceding
// value of the pipeline, if any.
func (s *state) evalField(dot any, fieldName string, node parse.Node, args []parse.Node, final, receiver any) any {
	if receiver == nil {
		if s.tmpl.missing == missingKeyError {
			s.errorf("nil data; no entry for key %q", fieldName)
		}
		return nil
	}
	hasArgs := len(args) > 1 || final != noFinal
	// A method takes precedence over a map key of the same name, as it does
	// under reflection.
	if hasMethod(receiver, fieldName) {
		var argv []any
		if hasArgs {
			for _, arg := range args[1:] {
				argv = append(argv, s.evalArg(dot, arg))
			}
			if final != noFinal {
				argv = append(argv, final)
			}
		}
		result, err := callMethod(receiver, fieldName, argv)
		if err != nil {
			s.at(node)
			if isArgError(err) {
				s.errorf("%s", err)
			}
			s.errorf("error calling %s: %v", fieldName, err)
		}
		return result
	}
	if value, found, isMap := mapValue(receiver, fieldName); isMap {
		if hasArgs {
			s.errorf("%s is not a method but has arguments", fieldName)
		}
		if !found {
			switch s.tmpl.missing {
			case missingKeyInvalid, missingKeyZero:
				return nil
			case missingKeyError:
				s.errorf("map has no entry for key %q", fieldName)
			}
		}
		return value
	}
	s.errorf("can't evaluate field %s in type %s", fieldName, typeName(receiver))
	return nil
}

// evalCall evaluates the arguments and calls the function.
func (s *state) evalCall(dot any, fun Func, isBuiltin bool, node parse.Node, name string, args []parse.Node, final any) any {
	if args != nil {
		args = args[1:] // Zeroth arg is function name/node; not passed to function.
	}
	numIn := len(args)
	if final != noFinal {
		numIn++
	}
	// Special case for builtin and/or, which short-circuit.
	if isBuiltin && (name == "and" || name == "or") {
		if numIn < 1 {
			s.errorf("wrong number of args for %s: want at least 1 got 0", name)
		}
		var v any
		for _, arg := range args {
			v = s.evalArg(dot, arg)
			truth, _ := isTrue(v)
			if truth == (name == "or") {
				return v
			}
		}
		if final != noFinal {
			// The last argument to and/or is coming from the pipeline. We
			// didn't short circuit on an earlier argument, so we are going
			// to return this one.
			v = final
		}
		return v
	}
	argv := make([]any, 0, numIn)
	for _, arg := range args {
		argv = append(argv, s.evalArg(dot, arg))
	}
	if final != noFinal {
		argv = append(argv, final)
	}
	result, err := fun(argv)
	if err != nil {
		s.at(node)
		if isArgError(err) {
			// An arity or type complaint is the caller's mistake, reported
			// as text/template reports its own checks: without the "error
			// calling" prefix a function's failure gets.
			s.errorf("%s", err)
		}
		s.errorf("error calling %s: %v", name, err)
	}
	return result
}

// evalArg evaluates one argument node to a value.
func (s *state) evalArg(dot any, n parse.Node) any {
	s.at(n)
	switch arg := n.(type) {
	case *parse.DotNode:
		return dot
	case *parse.NilNode:
		return nil
	case *parse.FieldNode:
		return s.evalFieldNode(dot, arg, []parse.Node{n}, noFinal)
	case *parse.VariableNode:
		return s.evalVariableNode(dot, arg, nil, noFinal)
	case *parse.PipeNode:
		return s.evalPipeline(dot, arg)
	case *parse.IdentifierNode:
		return s.evalFunction(dot, arg, arg, nil, noFinal)
	case *parse.ChainNode:
		return s.evalChainNode(dot, arg, nil, noFinal)
	case *parse.BoolNode:
		return arg.True
	case *parse.NumberNode:
		return s.idealConstant(arg)
	case *parse.StringNode:
		return arg.Text
	}
	s.errorf("can't handle %s for arg", n)
	return nil
}

// printValue writes the value of an action: what fmt prints, "<no value>"
// for a value that is not there, and under HTML escaping the escaped form of
// either, unless the pipeline ends in the html function.
func (s *state) printValue(n parse.Node, pipe *parse.PipeNode, v any) {
	s.at(n)
	var str string
	switch {
	case v == nil && s.tmpl.escape:
		str = ""
	case v == nil:
		str = "<no value>"
	default:
		if !printable(v) {
			s.errorf("can't print %s of type %s", n, typeName(v))
		}
		str = fmt.Sprint(v)
	}
	if s.tmpl.escape && !endsInHTMLFunc(pipe) {
		str = escapeHTML(str)
	}
	if _, err := io.WriteString(s.wr, str); err != nil {
		s.writeError(err)
	}
}

// endsInHTMLFunc reports whether the pipeline's last command is the html
// function, whose output html/template leaves as it is rather than escaping
// a second time.
func endsInHTMLFunc(pipe *parse.PipeNode) bool {
	if pipe == nil || len(pipe.Cmds) == 0 {
		return false
	}
	last := pipe.Cmds[len(pipe.Cmds)-1]
	if len(last.Args) == 0 {
		return false
	}
	ident, ok := last.Args[0].(*parse.IdentifierNode)
	return ok && ident.Ident == "html"
}

// printable reports whether fmt can print the value meaningfully. A function
// is the one thing text/template refuses to print; nothing in a data document
// is one, and a function value only gets here through a FuncMap entry
// returned as a value.
func printable(v any) bool {
	switch v.(type) {
	case Func, func(args []any) (any, error):
		return false
	}
	return true
}

// typeName is the value's type as text/template would name it in an error.
func typeName(v any) string {
	if v == nil {
		return "interface {}"
	}
	return fmt.Sprintf("%T", v)
}
