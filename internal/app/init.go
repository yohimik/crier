package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yohimik/crier/internal/configgen"
	"github.com/yohimik/crier/internal/logging"
)

// runInit writes a configuration file to start from.
//
// It deliberately does not load a configuration first, unlike every other
// command: the whole point is that there is not one yet.
func (a App) runInit(args []string) error {
	fs := flag.NewFlagSet("crier init", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var (
		full   = fs.Bool("full", false, "write every option with its default, rather than a starter")
		force  = fs.Bool("force", false, "overwrite an existing configuration file")
		format = fs.String("format", "yaml", "file format: yaml, json or toml")
		out    = fs.String("output", "", "where to write; the default is crier.<format> here")
	)
	fs.Usage = func() {
		fmt.Fprint(a.Stderr, `Usage: crier init [flags]

Writes a configuration file to start from. With --full it writes every option
crier has, with its default, which is a reference rather than a starting point.

Flags:
`)
		printFlags(a.Stderr, fs)
	}
	if err := parse(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	f, err := configgen.ParseFormat(*format)
	if err != nil {
		return failf(ExitUsage, "--format: %v", err)
	}

	path := *out
	if path == "" {
		path = filepath.Join(a.Dir, "crier"+f.Ext())
	}
	// Absolute, so what is printed can be pasted into an editor from anywhere
	// — App.Dir is empty for a real process, which would leave a bare
	// "crier.yaml" that means nothing to whatever reads the output.
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if _, err := os.Stat(path); err == nil && !*force {
		return failf(ExitConfig,
			"%s already exists; pass --force to overwrite it, or --output to write elsewhere", path)
	}

	body, err := configgen.Sample(configgen.Options{Format: f, Full: *full})
	if err != nil {
		return fail(ExitConfig, err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fail(ExitConfig, err)
		}
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fail(ExitConfig, err)
	}

	// init runs before there is a configuration to take a log level from, so
	// the logger is built with the defaults.
	log, err := logging.New(logging.Options{Writer: a.Stderr})
	if err != nil {
		return fail(ExitConfig, err)
	}
	log.Info().Str("file", path).Bool("full", *full).Msg("wrote a configuration file")
	fmt.Fprintln(a.Stdout, path)

	if !*full {
		fmt.Fprint(a.Stderr, strings.Join([]string{
			"",
			"Next:",
			"  1. write the template it names, and the data file beside it",
			"  2. crier render          # see the picture",
			"  3. crier --dry-run       # see what would be posted, no network",
			"  4. crier                 # post it",
			"",
			"`crier init --full` writes every option there is.",
			"",
		}, "\n"))
	}
	return nil
}
