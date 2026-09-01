package app

import (
	"errors"
	"fmt"
)

// Exit codes. They are part of crier's contract with the scripts that run it,
// so each one names a category an operator can branch on rather than a
// particular failure.
const (
	// ExitOK means everything worked.
	ExitOK = 0
	// ExitConfig means the configuration is wrong or incomplete.
	ExitConfig = 1
	// ExitUsage means the command line was wrong.
	ExitUsage = 2
	// ExitRender means the template or the rendering failed.
	ExitRender = 3
	// ExitPartial means some platforms took the post and others did not.
	ExitPartial = 4
	// ExitPublish means no platform took the post.
	ExitPublish = 5
	// ExitStaging means the image could not be made reachable.
	ExitStaging = 6
)

// ExitName is the label a code is reported under.
func ExitName(code int) string {
	switch code {
	case ExitOK:
		return "ok"
	case ExitConfig:
		return "config error"
	case ExitUsage:
		return "usage error"
	case ExitRender:
		return "render error"
	case ExitPartial:
		return "partial publish failure"
	case ExitPublish:
		return "publish failure"
	case ExitStaging:
		return "staging error"
	default:
		return "error"
	}
}

// Error carries an exit code alongside a failure.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return ExitName(e.Code)
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// fail wraps an error with an exit code.
func fail(code int, err error) error {
	if err == nil {
		return nil
	}
	var already *Error
	if errors.As(err, &already) {
		return err
	}
	return &Error{Code: code, Err: err}
}

// failf wraps a formatted message with an exit code.
func failf(code int, format string, args ...any) error {
	return &Error{Code: code, Err: fmt.Errorf(format, args...)}
}

// codeOf is the exit code an error asks for, defaulting to a config error
// because that is what most failures before the first request are.
func codeOf(err error) int {
	if err == nil {
		return ExitOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ExitConfig
}
