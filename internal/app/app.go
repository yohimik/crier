// Package app is crier's command line: it wires the configuration, the
// renderer, the stagers and the publishers together and turns what happens
// into an exit code.
//
// Results go to standard output and logs go to standard error, so a script can
// read one without filtering the other.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/logging"
	"github.com/yohimik/crier/internal/render"
	"github.com/yohimik/crier/internal/version"
)

// App is one invocation of crier.
type App struct {
	// Args are the arguments after the program name.
	Args []string
	// Environ is the process environment. Nil means os.Environ().
	Environ []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	// Dir is where the search for a configuration file starts. Empty means the
	// working directory.
	Dir string
}

// Run executes the command and returns the process exit code.
func (a App) Run(ctx context.Context) int {
	if a.Stdout == nil {
		a.Stdout = os.Stdout
	}
	if a.Stderr == nil {
		a.Stderr = os.Stderr
	}
	if a.Stdin == nil {
		a.Stdin = os.Stdin
	}

	name, args := "", a.Args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}

	switch name {
	case "", "help", "-h", "--help":
		a.usage()
		if name == "" {
			return ExitUsage
		}
		return ExitOK
	case "version":
		return a.report(a.runVersion(args))
	case "render":
		return a.report(a.runRender(ctx, args))
	case "publish":
		return a.report(a.runPublish(ctx, args))
	case "platforms":
		return a.report(a.runPlatforms(args))
	case "config":
		return a.report(a.runConfig(args))
	default:
		fmt.Fprintf(a.Stderr, "crier: unknown command %q\n\n", name)
		a.usage()
		return ExitUsage
	}
}

// report prints an error and turns it into an exit code.
func (a App) report(err error) int {
	if err == nil {
		return ExitOK
	}
	code := codeOf(err)
	if code == ExitOK {
		return ExitOK
	}
	fmt.Fprintf(a.Stderr, "crier: %s\n", err)
	return code
}

func (a App) usage() {
	fmt.Fprint(a.Stderr, `crier renders an HTML template to an image or a video and publishes it.

Usage:
  crier <command> [flags]

Commands:
  render      render the template and write the image or video to a file
  publish     render and post to every enabled platform
  platforms   list the platforms and whether they are configured
  config      print the resolved configuration, with secrets redacted
  version     print the version
  help        print this message

Every configuration key is a flag, an environment variable and a config file
key at once. Run "crier config" to see them all resolved, or "crier render -h"
for the flag list.

Configuration is found by walking up from the working directory, the way git
finds a repository, so running crier inside a project uses that project's
crier.yaml. Relative paths written in that file resolve against it.
`)
}

// setup is the common start of every command that needs configuration: parse
// the flags, load the configuration, build the logger.
type setup struct {
	Config *config.Config
	Log    zerolog.Logger
	Client *httpx.Client
	Result *config.Result
	Args   []string
}

// flagSet builds a FlagSet carrying every configuration flag plus the ones the
// command adds itself.
func (a App) flagSet(name string, extra func(*flag.FlagSet)) (*flag.FlagSet, *config.Flags) {
	fs := flag.NewFlagSet("crier "+name, flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	flags := config.RegisterFlags(fs)
	if extra != nil {
		extra(fs)
	}
	fs.Usage = func() {
		fmt.Fprintf(a.Stderr, "Usage: crier %s [flags]\n\nFlags:\n", name)
		printFlags(a.Stderr, fs)
	}
	return fs, flags
}

// printFlags renders the flag list grouped and sorted, because there are two
// hundred of them and the default one-per-line dump is unreadable.
func printFlags(w io.Writer, fs *flag.FlagSet) {
	var names []string
	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)
	for _, n := range names {
		f := fs.Lookup(n)
		fmt.Fprintf(w, "  --%-38s %s\n", f.Name, f.Usage)
	}
}

// load parses the command line and resolves the configuration.
func (a App) load(name string, args []string, extra func(*flag.FlagSet)) (*setup, error) {
	fs, flags := a.flagSet(name, extra)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, &Error{Code: ExitOK}
		}
		return nil, fail(ExitUsage, err)
	}

	res, err := config.Load(context.Background(), config.Options{
		Path:          flags.ConfigPath(),
		Environ:       a.Environ,
		FlagOverrides: flags.Overrides(),
		Dir:           a.Dir,
	})
	if err != nil {
		return nil, fail(ExitConfig, err)
	}
	if err := config.Validate(&res.Config); err != nil {
		return nil, fail(ExitConfig, err)
	}

	log, err := logging.New(logging.Options{
		Level:  res.Config.Log.Level,
		Format: res.Config.Log.Format,
		Writer: a.Stderr,
	})
	if err != nil {
		return nil, fail(ExitConfig, err)
	}
	// webrender's own loggers write to standard output by default, which is
	// where crier's results go.
	render.CaptureLogs(log)

	if res.File != "" {
		log.Debug().Str("file", res.File).Str("root", res.Dir).Msg("loaded configuration")
	} else {
		log.Debug().Msg("no configuration file was found; using defaults, environment and flags")
	}

	h := res.Config.HTTP
	client := httpx.New(httpx.Options{
		Retry: httpx.RetryPolicy{
			Max:       h.RetryMax,
			BaseDelay: config.Duration(h.RetryBaseDelay),
			MaxDelay:  config.Duration(h.RetryMaxDelay),
			Timeout:   config.Duration(h.Timeout),
		},
		Logger:    log,
		UserAgent: userAgent(),
	})

	return &setup{Config: &res.Config, Log: log, Client: client, Result: res, Args: fs.Args()}, nil
}

// userAgent identifies crier to the platforms.
func userAgent() string {
	return "crier/" + version.Get().Version + " (+https://github.com/yohimik/crier)"
}

func (a App) runVersion(args []string) error {
	fs := flag.NewFlagSet("crier version", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	asJSON := fs.Bool("json", false, "print the version as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fail(ExitUsage, err)
	}
	info := version.Get()
	if *asJSON {
		return writeJSON(a.Stdout, info)
	}
	fmt.Fprintln(a.Stdout, info.String())
	return nil
}
