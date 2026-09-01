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

	// Housekeeping first, so a run that ends in a failure still tidies up
	// after an earlier update.
	pruneBackup()
	// Before anything renders: a stock macOS terminal exports LC_CTYPE=UTF-8,
	// which the text stack would read as the language "utf-8" and crash on.
	render.NormalizeLocaleEnv()

	name, args := a.dispatch(a.Args)

	switch name {
	case "help":
		a.usage()
		return ExitOK
	case "--version":
		return a.report(a.runVersion(args))
	case "render":
		return a.report(a.runRender(ctx, args))
	case "publish":
		return a.report(a.runPublish(ctx, args))
	case "platforms":
		return a.report(a.runPlatforms(args))
	case "ping":
		return a.report(a.runPing(ctx, args))
	case "config":
		return a.report(a.runConfig(args))
	case "init":
		return a.report(a.runInit(args))
	case "self-update":
		return a.report(a.runSelfUpdate(ctx, args))
	case "semen":
		// An easter egg, and the one word that is not in Commands: it is a
		// signature rather than a feature, so it answers when asked for and is
		// not advertised anywhere. Every other unknown word is still refused.
		fmt.Fprintln(a.Stdout, "semen is sleeping")
		return ExitOK
	default:
		fmt.Fprintf(a.Stderr, "crier: unknown command %q\n\nValid commands: %s\n\n",
			name, strings.Join(Commands, ", "))
		a.usage()
		return ExitUsage
	}
}

// Commands is every command word crier answers to.
//
// It is a list rather than a set of cases scattered through Run, because the
// dispatcher, the usage text and the "unknown command" message all have to
// agree about it and there is no way to notice when three copies stop
// agreeing.
var Commands = []string{"publish", "render", "init", "ping", "platforms", "config", "self-update", "help"}

// dispatch decides which command was asked for, and what is left for it.
//
// publish is the default, so `crier` inside a project directory renders and
// posts — which is the whole point of the per-directory configuration. A
// leading flag belongs to publish too: `crier --dry-run` has to mean what it
// looks like it means.
//
// A help flag is the one exception to that rule. A command line tool whose
// --help does not help is a bug, so it reaches the top-level usage rather than
// publish's flag list.
//
// Nothing here guesses. A leading flag reaches publish, and publish's flag set
// refuses one it does not declare — so `crier --piblish` is a usage error
// naming the flag rather than a run of something nobody asked for.
func (a App) dispatch(args []string) (name string, rest []string) {
	if len(args) == 0 {
		return "publish", nil
	}
	switch args[0] {
	case "-h", "--help", "-help":
		return "help", args[1:]
	case "--version", "-version":
		// Ahead of the leading-flag rule below, which would otherwise hand
		// --version to publish as a flag it has never heard of.
		return "--version", args[1:]
	}
	if strings.HasPrefix(args[0], "-") {
		return "publish", args
	}
	return args[0], args[1:]
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
  crier [command] [flags]

With no command, crier publishes: it renders the template the configuration in
this directory names and posts it to every enabled platform. That is the whole
of the everyday flow — cd into a project, run crier.

Commands:
  publish     render and post to every enabled platform (the default)
  render      render the template and write the image or video to a file
  ping        check every enabled platform's credentials, without posting
  platforms   list the platforms and whether they are configured
  config      print the resolved configuration, with secrets redacted
  init        write a starter configuration file
  self-update replace this binary with the newest release
  help        print this message

Flags:
  --version   print the version and exit

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

// parse reads a command line and refuses everything it was not told about.
//
// Fail closed, the way the configuration decoder does: an unknown flag and a
// stray word are both mistakes, and running anyway means running something
// other than what was asked for. A typo in `--dry-run` that quietly published
// for real is the failure this prevents.
func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		// flag has already printed "flag provided but not defined: -x" to the
		// output it was given, which names the flag.
		return fail(ExitUsage, err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(fs.Output(), "%s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return failf(ExitUsage, "%s takes no positional arguments, and got %q",
			fs.Name(), strings.Join(fs.Args(), " "))
	}
	return nil
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
	if err := parse(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, &Error{Code: ExitOK}
		}
		return nil, err
	}

	overrides, err := flags.Overrides()
	if err != nil {
		return nil, fail(ExitConfig, err)
	}

	res, err := config.Load(context.Background(), config.Options{
		Path:          flags.ConfigPath(),
		Environ:       a.Environ,
		FlagOverrides: overrides,
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
			Max:           h.RetryMax,
			BaseDelay:     config.Duration(h.RetryBaseDelay),
			MaxDelay:      config.Duration(h.RetryMaxDelay),
			Timeout:       config.Duration(h.Timeout),
			UploadTimeout: config.Duration(h.UploadTimeout),
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
	fs := flag.NewFlagSet("crier --version", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	asJSON := fs.Bool("json", false, "print the version as JSON")
	if err := parse(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	info := version.Get()
	if *asJSON {
		return writeJSON(a.Stdout, info)
	}
	fmt.Fprintln(a.Stdout, info.String())
	return nil
}
