package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/yohimik/crier/internal/logging"
	"github.com/yohimik/crier/internal/selfupdate"
	"github.com/yohimik/crier/internal/version"
)

// UpdateReport is what `crier self-update` prints.
type UpdateReport struct {
	// Current is the version that was running.
	Current string `json:"current"`
	// Latest is the version that was found, empty when nothing was looked up.
	Latest string `json:"latest,omitempty"`
	// Updated says the binary on disk changed.
	Updated bool `json:"updated"`
	// Backup is where the replaced binary was kept.
	Backup string `json:"backup,omitempty"`
	// Notes is the release's own notes, when there are any.
	Notes string `json:"notes,omitempty"`
}

// runSelfUpdate replaces the running binary with a released one.
//
// It reads no configuration. The releases it looks at are crier's own, never
// anything a crier.yaml names, so it works in a directory that holds no project
// at all — which is where somebody upgrading a tool usually is.
func (a App) runSelfUpdate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("crier self-update", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var (
		release    = fs.String("release", "", "install this version rather than the newest")
		check      = fs.Bool("check", false, "report whether a newer release exists, and change nothing")
		rollback   = fs.Bool("rollback", false, "put the binary the last update replaced back")
		prerelease = fs.Bool("prerelease", false, "consider release candidates too")
		apiURL     = fs.String("api-url", selfupdate.DefaultAPIURL, "GitHub API base URL")
		tokenEnv   = fs.String("token-env", "GITHUB_TOKEN",
			"environment variable holding a GitHub token, sent only to github.com")
		asJSON = fs.Bool("json", false, "print the result as JSON")
	)
	fs.Usage = func() {
		fmt.Fprint(a.Stderr, `Usage: crier self-update [flags]

Replaces this binary with one downloaded from a crier release. The download is
verified against the sha256 digest GitHub publishes for it and run once before
anything is moved; the binary it replaces is kept beside it as crier`+
			selfupdate.BackupSuffix+` for a
week, so `+"`crier self-update --rollback`"+` can put it back.

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
	if *check && *rollback {
		return failf(ExitUsage, "--check and --rollback ask for different things; pass one")
	}

	log, err := logging.New(logging.Options{Writer: a.Stderr})
	if err != nil {
		return fail(ExitConfig, err)
	}

	info := version.Get()
	build := selfupdate.Describe(info.Version)
	rep := UpdateReport{Current: build.Version}

	exe, err := selfupdate.Executable()
	if err != nil {
		return failf(ExitConfig, "locating the running binary: %v", err)
	}

	if *rollback {
		from, to, err := selfupdate.Rollback(ctx, exe)
		switch {
		case errors.Is(err, selfupdate.ErrBackupNotKept):
			// The rollback happened; only the copy that would make it
			// reversible is missing. That is a success with a caveat.
			log.Warn().Err(err).Msg("rolled back, but the replaced binary was not kept")
		case err != nil:
			return fail(ExitConfig, err)
		}
		rep.Latest, rep.Updated = to, true
		if from != "" {
			rep.Current = from
		}
		log.Info().Str("from", from).Str("to", to).Str("file", exe).Msg("rolled back")
		return a.printUpdate(rep, *asJSON, "rolled back to "+to)
	}

	// A build that was not installed from a release cannot be replaced by one:
	// saying so is more use than downloading something that would overwrite a
	// binary the Go toolchain owns.
	if build.Origin == selfupdate.OriginGoInstall && !*check {
		return failf(ExitConfig, "this crier was installed with `go install`; update it the same way:\n  %s",
			selfupdate.GoInstallCommand)
	}

	src := &selfupdate.Source{
		APIURL:     *apiURL,
		Prerelease: *prerelease,
		Command:    "self-update",
		Token:      selfupdate.TokenFrom(a.Environ, *tokenEnv, *apiURL),
		Log:        log,
	}

	var rel selfupdate.Release
	if *release != "" {
		rel, err = src.At(ctx, *release)
	} else {
		rel, err = src.Latest(ctx)
	}
	if err != nil {
		if errors.Is(err, selfupdate.ErrNoRelease) && !*prerelease {
			return fail(ExitConfig, fmt.Errorf("%w (pass --prerelease to consider release candidates)", err))
		}
		return fail(ExitConfig, err)
	}
	rep.Latest = rel.Version.String()
	rep.Notes = strings.TrimSpace(rel.Body)

	same := rep.Latest == rep.Current
	if *check {
		if same {
			log.Info().Str("version", rep.Current).Msg("this is the newest release")
			return a.printUpdate(rep, *asJSON, "crier "+rep.Current+" is the newest release")
		}
		log.Info().Str("current", rep.Current).Str("latest", rep.Latest).Msg("a newer release is out")
		if err := a.printUpdate(rep, *asJSON, "crier "+rep.Latest+" is out; you have "+rep.Current); err != nil {
			return err
		}
		// --check is a question, and "no, you are not current" is an answer a
		// script has to be able to branch on.
		return &Error{Code: ExitConfig}
	}
	if same && *release == "" {
		log.Info().Str("version", rep.Current).Msg("already up to date")
		return a.printUpdate(rep, *asJSON, "already at "+rep.Current)
	}

	asset, ok := rel.Asset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return failf(ExitConfig, "%s carries no %s; it has: %s",
			rel.Tag, selfupdate.CurrentAssetName(), strings.Join(rel.AssetNames(), ", "))
	}

	log.Info().Str("version", rep.Latest).Str("asset", asset.Name).
		Int64("bytes", asset.Size).Msg("downloading")

	inst := &selfupdate.Installer{
		Exe:     exe,
		Want:    rep.Latest,
		Command: "self-update",
		Log:     log,
	}
	backup, err := inst.Install(ctx, asset)
	if err != nil {
		return fail(ExitConfig, err)
	}
	rep.Updated, rep.Backup = true, backup

	log.Info().Str("from", rep.Current).Str("to", rep.Latest).Str("file", exe).
		Str("backup", backup).Msg("updated")
	return a.printUpdate(rep, *asJSON, "updated "+rep.Current+" to "+rep.Latest)
}

func (a App) printUpdate(rep UpdateReport, asJSON bool, line string) error {
	if asJSON {
		return writeJSON(a.Stdout, rep)
	}
	fmt.Fprintln(a.Stdout, line)
	return nil
}

// pruneBackup is the housekeeping every run does: a binary an update replaced
// is kept for a week and then removed.
//
// It runs before the command rather than after it, so a run that ends in a
// failure still tidies up, and it reports nothing: housekeeping must never be
// the reason a command fails.
func pruneBackup() {
	exe, err := selfupdate.Executable()
	if err != nil {
		return
	}
	selfupdate.PruneBackup(exe, time.Now())
}
