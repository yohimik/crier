// Package logging builds the application's zerolog logger.
//
// Level discipline used across crier:
//
//	trace — very chatty internals (per-glyph / per-pixel loops, raw payloads)
//	debug — internal steps: HTTP attempts, subprocess output, render phases
//	info  — user visible results: rendered file, published post URLs, tunnel URL
//	warn  — soft issues that do not fail the run: retried request, cleanup failure
//	error — operations that failed and change the exit code
package logging

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Format selects the log encoding.
type Format string

const (
	// FormatConsole is a human readable, coloured (when supported) format.
	FormatConsole Format = "console"
	// FormatJSON is one JSON object per line, for machine consumption.
	FormatJSON Format = "json"
)

// Options configures New.
type Options struct {
	// Level is a zerolog level name: trace, debug, info, warn, error, fatal,
	// panic, disabled. Empty means "info".
	Level string
	// Format is console or json. Empty means "console".
	Format string
	// Writer receives the log records. Logs always go to stderr in the CLI so
	// that stdout stays a clean, scriptable channel.
	Writer io.Writer
	// NoColor disables ANSI colouring of the console format.
	NoColor bool
}

// ParseLevel converts a level name into a zerolog level.
func ParseLevel(s string) (zerolog.Level, error) {
	if strings.TrimSpace(s) == "" {
		return zerolog.InfoLevel, nil
	}
	lvl, err := zerolog.ParseLevel(strings.ToLower(strings.TrimSpace(s)))
	if err != nil {
		return zerolog.NoLevel, fmt.Errorf("invalid log level %q: %w", s, err)
	}
	return lvl, nil
}

// ParseFormat validates and normalises a log format name.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(FormatConsole):
		return FormatConsole, nil
	case string(FormatJSON):
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("invalid log format %q (want console or json)", s)
	}
}

// New builds a logger from the options. It returns an error when the level or
// the format cannot be parsed, so that misconfiguration surfaces as a config
// error rather than being silently ignored.
func New(o Options) (zerolog.Logger, error) {
	lvl, err := ParseLevel(o.Level)
	if err != nil {
		return zerolog.Nop(), err
	}
	format, err := ParseFormat(o.Format)
	if err != nil {
		return zerolog.Nop(), err
	}
	w := o.Writer
	if w == nil {
		return zerolog.Nop(), fmt.Errorf("logging: Writer is required")
	}
	if format == FormatConsole {
		w = zerolog.ConsoleWriter{
			Out:        w,
			NoColor:    o.NoColor,
			TimeFormat: time.RFC3339,
		}
	}
	return zerolog.New(w).Level(lvl).With().Timestamp().Logger(), nil
}

// Nop returns a logger that discards everything; handy in tests and as a
// zero-value fallback so no call site has to nil-check a logger.
func Nop() zerolog.Logger { return zerolog.Nop() }
