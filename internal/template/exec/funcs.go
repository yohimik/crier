package exec

import (
	"fmt"
	"math"
	"net/url"
	"strings"
)

// builtins are text/template's predefined functions, over plain values. The
// executor handles and and or itself, because they short-circuit; their
// entries here exist so the parser knows the names and so a lookup finds
// something to call for a pipeline that hands them the final value.
var builtins = FuncMap{
	"and":      func(args []any) (any, error) { return andOr(true, args) },
	"or":       func(args []any) (any, error) { return andOr(false, args) },
	"call":     builtinCall,
	"html":     func(args []any) (any, error) { return HTMLEscapeString(evalArgs(args)), nil },
	"index":    builtinIndex,
	"slice":    builtinSlice,
	"js":       func(args []any) (any, error) { return JSEscapeString(evalArgs(args)), nil },
	"len":      builtinLen,
	"not":      builtinNot,
	"print":    func(args []any) (any, error) { return fmt.Sprint(args...), nil },
	"printf":   builtinPrintf,
	"println":  func(args []any) (any, error) { return fmt.Sprintln(args...), nil },
	"urlquery": func(args []any) (any, error) { return url.QueryEscape(evalArgs(args)), nil },
	"eq":       builtinEq,
	"ge":       func(args []any) (any, error) { return compare("ge", args) },
	"gt":       func(args []any) (any, error) { return compare("gt", args) },
	"le":       func(args []any) (any, error) { return compare("le", args) },
	"lt":       func(args []any) (any, error) { return compare("lt", args) },
	"ne":       builtinNe,
}

// builtinNames is what the parser is told about the builtins: that they
// exist.
func builtinNames() map[string]any {
	out := make(map[string]any, len(builtins))
	for name := range builtins {
		out[name] = true
	}
	return out
}

// andOr is the eager fallback for and and or; the executor short-circuits
// them before a call ever gets here.
func andOr(isAnd bool, args []any) (any, error) {
	if len(args) < 1 {
		name := "or"
		if isAnd {
			name = "and"
		}
		return nil, &ArgError{fmt.Sprintf("wrong number of args for %s: want at least 1 got 0", name)}
	}
	var v any
	for _, v = range args {
		truth, _ := isTrue(v)
		if truth != isAnd {
			return v, nil
		}
	}
	return v, nil
}

func builtinNot(args []any) (any, error) {
	if err := Arity("not", args, 1); err != nil {
		return nil, err
	}
	truth, _ := isTrue(args[0])
	return !truth, nil
}

// builtinCall would call a function value with arguments. text/template does
// it through reflection; the only function values this package has are its
// own Funcs, which a template has no way to name as a value, so there is
// nothing to call.
func builtinCall(args []any) (any, error) {
	if len(args) < 1 {
		return nil, &ArgError{"wrong number of args for call: want at least 1 got 0"}
	}
	if fn, ok := args[0].(Func); ok && fn != nil {
		return fn(args[1:])
	}
	if args[0] == nil {
		return nil, fmt.Errorf("call of nil")
	}
	return nil, fmt.Errorf("non-function of type %s", typeName(args[0]))
}

func builtinPrintf(args []any) (any, error) {
	if len(args) < 1 {
		return nil, &ArgError{"wrong number of args for printf: want at least 1 got 0"}
	}
	format, ok := args[0].(string)
	if !ok {
		return nil, &ArgError{fmt.Sprintf("wrong type for value; expected string; got %s", typeName(args[0]))}
	}
	return fmt.Sprintf(format, args[1:]...), nil
}

func builtinLen(args []any) (any, error) {
	if err := Arity("len", args, 1); err != nil {
		return nil, err
	}
	if args[0] == nil {
		return nil, fmt.Errorf("len of untyped nil")
	}
	n, ok := length(args[0])
	if !ok {
		return nil, fmt.Errorf("len of type %s", typeName(args[0]))
	}
	return n, nil
}

// evalArgs formats the arguments the way text/template's escapers do: one
// string is itself, anything else is what print would say.
func evalArgs(args []any) string {
	if len(args) == 1 {
		if s, ok := args[0].(string); ok {
			return s
		}
	}
	return fmt.Sprint(args...)
}

// indexArg reads an index and checks it against the length.
func indexArg(index any, capacity int) (int, error) {
	if index == nil {
		return 0, fmt.Errorf("cannot index slice/array with nil")
	}
	x, ok := toInt(index)
	if !ok {
		return 0, fmt.Errorf("cannot index slice/array with type %s", typeName(index))
	}
	if x < 0 || x > capacity {
		return 0, fmt.Errorf("index out of range: %d", x)
	}
	return x, nil
}

// builtinIndex is index: the item indexed by each of the following
// arguments in turn.
func builtinIndex(args []any) (any, error) {
	if len(args) < 1 {
		return nil, &ArgError{"wrong number of args for index: want at least 1 got 0"}
	}
	item := args[0]
	if item == nil {
		return nil, fmt.Errorf("index of untyped nil")
	}
	for _, index := range args[1:] {
		if item == nil {
			return nil, fmt.Errorf("index of nil pointer")
		}
		if s, ok := item.(string); ok {
			x, err := indexArg(index, len(s))
			if err != nil {
				return nil, err
			}
			if x == len(s) {
				return nil, fmt.Errorf("index out of range: %d", x)
			}
			item = s[x]
			continue
		}
		if items, ok := elements(item); ok {
			x, err := indexArg(index, len(items))
			if err != nil {
				return nil, err
			}
			if x == len(items) {
				return nil, fmt.Errorf("index out of range: %d", x)
			}
			item = items[x]
			continue
		}
		if _, _, isMap := mapValue(item, ""); isMap {
			key, ok := index.(string)
			if !ok {
				return nil, fmt.Errorf("value has type %s; should be string", typeName(index))
			}
			// A missing key is the zero value, which for these maps is
			// nothing.
			item, _, _ = mapValue(item, key)
			continue
		}
		return nil, fmt.Errorf("can't index item of type %s", typeName(item))
	}
	return item, nil
}

// builtinSlice is slice: a string with up to two indices, or a slice with up
// to three.
func builtinSlice(args []any) (any, error) {
	if len(args) < 1 {
		return nil, &ArgError{"wrong number of args for slice: want at least 1 got 0"}
	}
	item := args[0]
	indexes := args[1:]
	if len(indexes) > 3 {
		return nil, fmt.Errorf("too many slice indexes: %d", len(indexes))
	}
	if item == nil {
		return nil, fmt.Errorf("slice of untyped nil")
	}
	var capacity int
	str, isString := item.(string)
	items, isSlice := elements(item)
	switch {
	case isString:
		if len(indexes) == 3 {
			return nil, fmt.Errorf("cannot 3-index slice a string")
		}
		capacity = len(str)
	case isSlice:
		capacity = len(items)
	default:
		return nil, fmt.Errorf("can't slice item of type %s", typeName(item))
	}
	// Set default values for cases where some indexes are omitted.
	lo, hi, max := 0, capacity, capacity
	var err error
	if len(indexes) > 0 {
		if lo, err = indexArg(indexes[0], capacity); err != nil {
			return nil, err
		}
	}
	if len(indexes) > 1 {
		if hi, err = indexArg(indexes[1], capacity); err != nil {
			return nil, err
		}
	}
	if len(indexes) > 2 {
		if max, err = indexArg(indexes[2], capacity); err != nil {
			return nil, err
		}
	}
	// Given a slice of length cap and three indices lo, hi, max, require
	// 0 <= lo <= hi <= max <= cap.
	if lo > hi {
		return nil, fmt.Errorf("invalid slice index: %d > %d", lo, hi)
	}
	if hi > max {
		return nil, fmt.Errorf("invalid slice index: %d > %d", hi, max)
	}
	if isString {
		return str[lo:hi], nil
	}
	if len(indexes) == 3 {
		return reslice(item, lo, hi, max), nil
	}
	return reslice(item, lo, hi, -1), nil
}

// reslice slices a slice value keeping its type, so a []string stays a
// []string for whatever takes it next.
func reslice(item any, lo, hi, max int) any {
	switch v := item.(type) {
	case []any:
		if max >= 0 {
			return v[lo:hi:max]
		}
		return v[lo:hi]
	case []string:
		if max >= 0 {
			return v[lo:hi:max]
		}
		return v[lo:hi]
	case []int:
		if max >= 0 {
			return v[lo:hi:max]
		}
		return v[lo:hi]
	case []float64:
		if max >= 0 {
			return v[lo:hi:max]
		}
		return v[lo:hi]
	case []bool:
		if max >= 0 {
			return v[lo:hi:max]
		}
		return v[lo:hi]
	case []map[string]any:
		if max >= 0 {
			return v[lo:hi:max]
		}
		return v[lo:hi]
	case [][]any:
		if max >= 0 {
			return v[lo:hi:max]
		}
		return v[lo:hi]
	case []byte:
		if max >= 0 {
			return v[lo:hi:max]
		}
		return v[lo:hi]
	}
	items, _ := elements(item)
	if max >= 0 {
		return items[lo:hi:max]
	}
	return items[lo:hi]
}

// kind is the comparison class of a value, as text/template's basicKind
// draws it: every signed integer is one class, every unsigned another.
type kind int

const (
	invalidKind kind = iota
	boolKind
	complexKind
	intKind
	floatKind
	stringKind
	uintKind
	otherKind
)

// basicKind classifies a value for comparison, with the integer value widened
// to int64 or uint64 and the float to float64.
func basicKind(v any) (k kind, i int64, u uint64, f float64, c complex128, s string, b bool, err error) {
	switch x := v.(type) {
	case nil:
		return invalidKind, 0, 0, 0, 0, "", false, errBadComparisonType
	case bool:
		return boolKind, 0, 0, 0, 0, "", x, nil
	case int:
		return intKind, int64(x), 0, 0, 0, "", false, nil
	case int8:
		return intKind, int64(x), 0, 0, 0, "", false, nil
	case int16:
		return intKind, int64(x), 0, 0, 0, "", false, nil
	case int32:
		return intKind, int64(x), 0, 0, 0, "", false, nil
	case int64:
		return intKind, x, 0, 0, 0, "", false, nil
	case uint:
		return uintKind, 0, uint64(x), 0, 0, "", false, nil
	case uint8:
		return uintKind, 0, uint64(x), 0, 0, "", false, nil
	case uint16:
		return uintKind, 0, uint64(x), 0, 0, "", false, nil
	case uint32:
		return uintKind, 0, uint64(x), 0, 0, "", false, nil
	case uint64:
		return uintKind, 0, x, 0, 0, "", false, nil
	case uintptr:
		return uintKind, 0, uint64(x), 0, 0, "", false, nil
	case float32:
		return floatKind, 0, 0, float64(x), 0, "", false, nil
	case float64:
		return floatKind, 0, 0, x, 0, "", false, nil
	case complex64:
		return complexKind, 0, 0, 0, complex128(x), "", false, nil
	case complex128:
		return complexKind, 0, 0, 0, x, "", false, nil
	case string:
		return stringKind, 0, 0, 0, 0, x, false, nil
	}
	return otherKind, 0, 0, 0, 0, "", false, errBadComparisonType
}

// comparable reports whether == is defined on the value's type, which for
// the values this package handles rules out slices and maps.
func comparable(v any) bool {
	if _, ok := elements(v); ok {
		return false
	}
	if _, _, isMap := mapValue(v, ""); isMap {
		return false
	}
	switch v.(type) {
	case []byte, Func, func(args []any) (any, error):
		return false
	}
	return true
}

// builtinEq is eq: whether the first argument equals any of the rest.
func builtinEq(args []any) (any, error) {
	if len(args) < 1 {
		return nil, &ArgError{"wrong number of args for eq: want at least 1 got 0"}
	}
	arg1 := args[0]
	if len(args) == 1 {
		return false, errNoComparison
	}
	k1, i1, u1, f1, c1, s1, b1, _ := basicKind(arg1)
	for _, arg := range args[1:] {
		k2, i2, u2, f2, c2, s2, b2, _ := basicKind(arg)
		truth := false
		if k1 != k2 {
			// Special case: Can compare integer values regardless of type's sign.
			switch {
			case k1 == intKind && k2 == uintKind:
				truth = i1 >= 0 && uint64(i1) == u2
			case k1 == uintKind && k2 == intKind:
				truth = i2 >= 0 && u1 == uint64(i2)
			default:
				if arg1 != nil && arg != nil {
					return false, fmt.Errorf("%w: %s and %s", errBadComparison, typeName(arg1), typeName(arg))
				}
			}
		} else {
			switch k1 {
			case boolKind:
				truth = b1 == b2
			case complexKind:
				truth = c1 == c2
			case floatKind:
				truth = f1 == f2
			case intKind:
				truth = i1 == i2
			case stringKind:
				truth = s1 == s2
			case uintKind:
				truth = u1 == u2
			case invalidKind:
				// Both are nothing, and nothing equals nothing.
				truth = true
			default:
				if !comparable(arg1) {
					return false, fmt.Errorf("non-comparable type %v: %s", arg1, typeName(arg1))
				}
				if !comparable(arg) {
					return false, fmt.Errorf("non-comparable type %v: %s", arg, typeName(arg))
				}
				truth = arg1 == arg
			}
		}
		if truth {
			return true, nil
		}
	}
	return false, nil
}

// builtinNe is ne: the opposite of eq over exactly two arguments.
func builtinNe(args []any) (any, error) {
	if err := Arity("ne", args, 2); err != nil {
		return nil, err
	}
	equal, err := builtinEq(args)
	if err != nil {
		return false, err
	}
	return !equal.(bool), nil
}

// lt reports whether arg1 < arg2, as text/template orders values: within a
// kind, and across the two integer kinds.
func lt(arg1, arg2 any) (bool, error) {
	k1, i1, u1, f1, _, s1, _, err := basicKind(arg1)
	if err != nil {
		return false, err
	}
	k2, i2, u2, f2, _, s2, _, err := basicKind(arg2)
	if err != nil {
		return false, err
	}
	truth := false
	if k1 != k2 {
		// Special case: Can compare integer values regardless of type's sign.
		switch {
		case k1 == intKind && k2 == uintKind:
			truth = i1 < 0 || uint64(i1) < u2
		case k1 == uintKind && k2 == intKind:
			truth = i2 >= 0 && u1 < uint64(i2)
		default:
			return false, fmt.Errorf("%w: %s and %s", errBadComparison, typeName(arg1), typeName(arg2))
		}
	} else {
		switch k1 {
		case boolKind, complexKind, otherKind:
			return false, errBadComparisonType
		case floatKind:
			truth = f1 < f2
		case intKind:
			truth = i1 < i2
		case stringKind:
			truth = s1 < s2
		case uintKind:
			truth = u1 < u2
		}
	}
	return truth, nil
}

// compare is lt, le, gt and ge, each written in terms of lt and eq as
// text/template writes them, so the errors are the same too.
func compare(name string, args []any) (any, error) {
	if err := Arity(name, args, 2); err != nil {
		return nil, err
	}
	arg1, arg2 := args[0], args[1]
	switch name {
	case "lt":
		return lt(arg1, arg2)
	case "le":
		lessThan, err := lt(arg1, arg2)
		if lessThan || err != nil {
			return lessThan, err
		}
		equal, err := builtinEq([]any{arg1, arg2})
		if err != nil {
			return false, err
		}
		return equal.(bool), nil
	case "gt":
		lessOrEqual, err := compare("le", args)
		if err != nil {
			return false, err
		}
		return !lessOrEqual.(bool), nil
	case "ge":
		lessThan, err := lt(arg1, arg2)
		if err != nil {
			return false, err
		}
		return !lessThan, nil
	}
	return nil, fmt.Errorf("unknown comparison %s", name)
}

// toInt reads any integer kind as an int, which is what an index or a range
// bound wants. A value that does not fit is not an int, and is refused rather
// than wrapped.
func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int8:
		return int(x), true
	case int16:
		return int(x), true
	case int32:
		return int(x), true
	case int64:
		if x < math.MinInt || x > math.MaxInt {
			return 0, false
		}
		return int(x), true
	case uint:
		if uint64(x) > math.MaxInt {
			return 0, false
		}
		return int(x), true //nolint:gosec // bounded by the check above
	case uint8:
		return int(x), true
	case uint16:
		return int(x), true
	case uint32:
		if uint64(x) > math.MaxInt {
			return 0, false
		}
		return int(x), true //nolint:gosec // bounded by the check above
	case uint64:
		if x > math.MaxInt {
			return 0, false
		}
		return int(x), true //nolint:gosec // bounded by the check above
	case uintptr:
		if uint64(x) > math.MaxInt {
			return 0, false
		}
		return int(x), true //nolint:gosec // bounded by the check above
	}
	return 0, false
}

// toFloat reads any numeric kind as a float64.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	}
	if n, ok := toInt(v); ok {
		return float64(n), true
	}
	return 0, false
}

// HTMLEscapeString returns the escaped HTML equivalent of the plain text data
// s, exactly as text/template's html function does: the five characters
// HTML gives meaning to, and NUL as the replacement character.
func HTMLEscapeString(s string) string {
	// Avoid allocation if we can.
	if !strings.ContainsAny(s, "'\"&<>\000") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString("&#34;")
		case '\'':
			b.WriteString("&#39;")
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\000':
			b.WriteString("�")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
