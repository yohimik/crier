package exec

import (
	"fmt"
	"time"
)

// ArgError is a complaint about a function's arguments — the wrong number, or
// the wrong type — as opposed to a failure of the function itself. The
// executor reports it as text/template reports its own argument checks,
// without the "error calling" prefix a function's failure gets.
type ArgError struct{ Msg string }

func (e *ArgError) Error() string { return e.Msg }

func isArgError(err error) bool {
	_, ok := err.(*ArgError)
	return ok
}

// Arity checks that a function got exactly n arguments.
func Arity(name string, args []any, n int) error {
	if len(args) != n {
		return &ArgError{fmt.Sprintf("wrong number of args for %s: want %d got %d", name, n, len(args))}
	}
	return nil
}

// MinArity checks that a function got at least n arguments.
func MinArity(name string, args []any, n int) error {
	if len(args) < n {
		return &ArgError{fmt.Sprintf("wrong number of args for %s: want at least %d got %d", name, n, len(args))}
	}
	return nil
}

func wrongType(expected string, got any) error {
	return &ArgError{fmt.Sprintf("wrong type for value; expected %s; got %s", expected, typeName(got))}
}

// StringArg reads argument i as a string.
func StringArg(args []any, i int) (string, error) {
	s, ok := args[i].(string)
	if !ok {
		return "", wrongType("string", args[i])
	}
	return s, nil
}

// IntArg reads argument i as an int. Any integer kind will do, which is a
// little wider than text/template, where a data value has to match the
// parameter exactly; nothing that worked before works differently.
func IntArg(args []any, i int) (int, error) {
	n, ok := toInt(args[i])
	if !ok {
		return 0, wrongType("int", args[i])
	}
	return n, nil
}

// FloatArg reads argument i as a float64. An integer is accepted too, as
// text/template accepts an integer literal for a float parameter.
func FloatArg(args []any, i int) (float64, error) {
	f, ok := toFloat(args[i])
	if !ok {
		return 0, wrongType("float64", args[i])
	}
	return f, nil
}

// TimeArg reads argument i as a time.Time.
func TimeArg(args []any, i int) (time.Time, error) {
	switch t := args[i].(type) {
	case time.Time:
		return t, nil
	case *time.Time:
		if t != nil {
			return *t, nil
		}
	}
	return time.Time{}, wrongType("time.Time", args[i])
}

// StringsArg reads argument i as a list of strings. A list from a data
// document arrives as []any, and one whose items are all strings is accepted
// as the []string it is; that is what makes `join ", " .tags` work over a
// YAML list, which text/template refused.
func StringsArg(args []any, i int) ([]string, error) {
	switch list := args[i].(type) {
	case []string:
		return list, nil
	case []any:
		out := make([]string, len(list))
		for j, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, &ArgError{fmt.Sprintf("wrong type for value; expected []string; got a list whose item %d is %s", j, typeName(item))}
			}
			out[j] = s
		}
		return out, nil
	}
	return nil, wrongType("[]string", args[i])
}
