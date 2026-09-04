package exec

import (
	"fmt"
	"time"
)

// The methods a template can call on a value. text/template reaches any
// exported method through reflection; here the set is fixed to the values a
// crier template can hold — a time from the now function or a YAML
// timestamp, a duration, and anything that can describe itself — and every
// entry is a case rather than a lookup.

// hasMethod reports whether the receiver has a method of the name that this
// package knows how to call.
func hasMethod(receiver any, name string) bool {
	switch r := receiver.(type) {
	case time.Time:
		return timeMethods[name]
	case *time.Time:
		return r != nil && timeMethods[name]
	case time.Duration:
		return durationMethods[name]
	case fmt.Stringer:
		return name == "String"
	case error:
		return name == "Error"
	}
	return false
}

var timeMethods = map[string]bool{
	"IsZero": true, "Year": true, "Month": true, "Day": true, "Hour": true, "Minute": true,
	"Second": true, "Nanosecond": true, "YearDay": true, "Weekday": true, "Unix": true,
	"UnixMilli": true, "UnixMicro": true, "UnixNano": true, "UTC": true, "Local": true,
	"String": true, "Format": true, "Before": true, "After": true, "Equal": true,
	"Sub": true, "Add": true, "AddDate": true, "Truncate": true, "Round": true,
}

var durationMethods = map[string]bool{
	"String": true, "Seconds": true, "Minutes": true, "Hours": true,
	"Milliseconds": true, "Microseconds": true, "Nanoseconds": true, "Abs": true,
	"Round": true, "Truncate": true,
}

// callMethod calls the named method with the arguments, checking their
// number and types the way text/template's reflection would.
func callMethod(receiver any, name string, args []any) (any, error) {
	switch r := receiver.(type) {
	case time.Time:
		return callTime(r, name, args)
	case *time.Time:
		return callTime(*r, name, args)
	case time.Duration:
		return callDuration(r, name, args)
	case fmt.Stringer:
		if err := Arity(name, args, 0); err != nil {
			return nil, err
		}
		return r.String(), nil
	case error:
		if err := Arity(name, args, 0); err != nil {
			return nil, err
		}
		return r.Error(), nil
	}
	return nil, fmt.Errorf("no method %s on %s", name, typeName(receiver))
}

func callTime(t time.Time, name string, args []any) (any, error) {
	switch name {
	case "Format":
		if err := Arity(name, args, 1); err != nil {
			return nil, err
		}
		layout, err := StringArg(args, 0)
		if err != nil {
			return nil, err
		}
		return t.Format(layout), nil
	case "Before", "After", "Equal", "Sub":
		if err := Arity(name, args, 1); err != nil {
			return nil, err
		}
		u, err := TimeArg(args, 0)
		if err != nil {
			return nil, err
		}
		switch name {
		case "Before":
			return t.Before(u), nil
		case "After":
			return t.After(u), nil
		case "Equal":
			return t.Equal(u), nil
		default:
			return t.Sub(u), nil
		}
	case "Add", "Truncate", "Round":
		if err := Arity(name, args, 1); err != nil {
			return nil, err
		}
		d, err := durationArg(args, 0)
		if err != nil {
			return nil, err
		}
		switch name {
		case "Add":
			return t.Add(d), nil
		case "Truncate":
			return t.Truncate(d), nil
		default:
			return t.Round(d), nil
		}
	case "AddDate":
		if err := Arity(name, args, 3); err != nil {
			return nil, err
		}
		var n [3]int
		for i := range n {
			v, err := IntArg(args, i)
			if err != nil {
				return nil, err
			}
			n[i] = v
		}
		return t.AddDate(n[0], n[1], n[2]), nil
	}
	if err := Arity(name, args, 0); err != nil {
		return nil, err
	}
	switch name {
	case "IsZero":
		return t.IsZero(), nil
	case "Year":
		return t.Year(), nil
	case "Month":
		return t.Month(), nil
	case "Day":
		return t.Day(), nil
	case "Hour":
		return t.Hour(), nil
	case "Minute":
		return t.Minute(), nil
	case "Second":
		return t.Second(), nil
	case "Nanosecond":
		return t.Nanosecond(), nil
	case "YearDay":
		return t.YearDay(), nil
	case "Weekday":
		return t.Weekday(), nil
	case "Unix":
		return t.Unix(), nil
	case "UnixMilli":
		return t.UnixMilli(), nil
	case "UnixMicro":
		return t.UnixMicro(), nil
	case "UnixNano":
		return t.UnixNano(), nil
	case "UTC":
		return t.UTC(), nil
	case "Local":
		return t.Local(), nil
	case "String":
		return t.String(), nil
	}
	return nil, fmt.Errorf("no method %s on time.Time", name)
}

func durationArg(args []any, i int) (time.Duration, error) {
	switch d := args[i].(type) {
	case time.Duration:
		return d, nil
	}
	if n, ok := toInt(args[i]); ok {
		return time.Duration(n), nil
	}
	return 0, wrongType("time.Duration", args[i])
}

func callDuration(d time.Duration, name string, args []any) (any, error) {
	switch name {
	case "Round", "Truncate":
		if err := Arity(name, args, 1); err != nil {
			return nil, err
		}
		m, err := durationArg(args, 0)
		if err != nil {
			return nil, err
		}
		if name == "Round" {
			return d.Round(m), nil
		}
		return d.Truncate(m), nil
	}
	if err := Arity(name, args, 0); err != nil {
		return nil, err
	}
	switch name {
	case "String":
		return d.String(), nil
	case "Seconds":
		return d.Seconds(), nil
	case "Minutes":
		return d.Minutes(), nil
	case "Hours":
		return d.Hours(), nil
	case "Milliseconds":
		return d.Milliseconds(), nil
	case "Microseconds":
		return d.Microseconds(), nil
	case "Nanoseconds":
		return d.Nanoseconds(), nil
	case "Abs":
		return d.Abs(), nil
	}
	return nil, fmt.Errorf("no method %s on time.Duration", name)
}
