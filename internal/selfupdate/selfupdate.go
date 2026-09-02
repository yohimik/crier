// Package selfupdate replaces the running crier binary with one downloaded
// from a GitHub release.
//
// It knows nothing about crier's configuration. The releases it looks at are
// crier's own, never anything a crier.yaml names, so nothing here reads a
// config file: everything it needs arrives as a field.
//
// The package prints nothing. The app layer turns what it returns into log
// events and text.
//
// The design is dispat's, ported rather than imported: the asset naming, the
// two-rename swap, the digest verification and the backup rotation are the
// same, because they are the same problem and dispat's answers were arrived at
// the hard way. The differences are crier's — its own owner, repo, tag prefix
// and version line.
package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Where crier's own releases live. Overridable per invocation for GitHub
// Enterprise and for the tests, which point the whole thing at an httptest
// server.
const (
	DefaultOwner     = "yohimik"
	DefaultRepo      = "crier"
	DefaultTagPrefix = "v"
)

const (
	// BackupSuffix names the outgoing binary an update keeps behind.
	BackupSuffix = ".backup"
	// BackupTTL is how long that copy is kept. A week is long enough to notice
	// a bad update and roll back, and short enough that a stale copy of a
	// 30 MiB binary does not sit in /usr/local/bin forever.
	BackupTTL = 7 * 24 * time.Hour
)

// ErrNoBackup is what a rollback with nothing to roll back to reports.
var ErrNoBackup = errors.New("no backup to roll back to")

// ErrNotWritable is a directory that will not take the file an install has to
// stage in it.
//
// Named rather than left as text because the advice the caller adds ("re-run
// with the rights to replace ...") is about this failure and no other, and
// matching on a message to decide that is how the advice goes missing the day
// the message is reworded.
var ErrNotWritable = errors.New("the install directory is not writable")

// ErrBackupNotKept reports a restore that happened while the binary it
// replaced could not be kept as the new backup.
//
// It is the one partial outcome a restore has: what was asked for is done, and
// only the copy that would have made it reversible is missing, so a caller
// reports a success with a caveat rather than a failure.
var ErrBackupNotKept = errors.New("the replaced binary was not kept as the new backup")

// Origin is how the running binary was produced, which decides how it can be
// updated: a release binary is replaced in place, a `go install` build is
// replaced by another `go install`, and a local build is not replaced at all.
type Origin int

const (
	// OriginRelease is a binary built by the release pipeline, carrying the
	// version stamped in by ldflags.
	OriginRelease Origin = iota
	// OriginGoInstall is a binary the Go toolchain installed from a module
	// version, which stamps the version into the build info instead.
	OriginGoInstall
	// OriginDev is a plain local build, which has no version at all.
	OriginDev
)

// Build is what the running binary knows about itself.
type Build struct {
	// Version is what --version reports: a semver for the first two origins,
	// "dev" for a local build.
	Version string
	Origin  Origin
}

// Describe works out which of the three builds is running.
//
// stamped is the version the release pipeline injects with -ldflags, "dev"
// when it injected nothing; the Go toolchain records a module version in the
// build info instead, which is what `go install .../cmd/crier@v1.2.3` leaves
// behind, and a plain local build has neither.
func Describe(stamped string) Build {
	if stamped != "" && stamped != "dev" {
		return Build{Version: strings.TrimPrefix(stamped, "v"), Origin: OriginRelease}
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return Build{Version: strings.TrimPrefix(v, "v"), Origin: OriginGoInstall}
		}
	}
	return Build{Version: "dev", Origin: OriginDev}
}

// GoInstallCommand is the one way to update a `go install` build.
const GoInstallCommand = "go install github.com/yohimik/crier/cmd/crier@latest"

// Executable is the path of the running binary with every symlink resolved, so
// an update replaces the real file rather than the link pointing at it.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// A binary whose path cannot be resolved (deleted while running, an
		// exotic filesystem) is still worth reporting by its unresolved path:
		// the caller's next step will fail with something more specific.
		return exe, nil
	}
	return resolved, nil
}

// AssetName is what a release calls the binary for a platform.
//
// It mirrors the Dockerfile's cross-compile loop, which is the other half of
// this contract — and install.sh, install.ps1 and `dispat install` are three more halves of the same one. Renaming an
// asset breaks all four at once.
func AssetName(goos, goarch string) string {
	name := "crier-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// CurrentAssetName is AssetName for the platform this binary runs on.
func CurrentAssetName() string { return AssetName(runtime.GOOS, runtime.GOARCH) }

// tempPattern is the name a downloaded or displaced binary is parked under,
// next to the binary it will replace.
//
// The extension matters: Windows will not execute a file that does not carry
// one, and both the download and the rollback verify the file by running it.
func tempPattern(exe, label string) string {
	pattern := "crier-" + label + "-*"
	if ext := filepath.Ext(exe); ext != "" {
		pattern += ext
	}
	return pattern
}

// BackupPath is where an update keeps the binary it replaced.
//
// On Windows the .exe extension is preserved (crier.exe becomes
// crier.backup.exe) because a file Windows refuses to execute can be neither
// verified before a rollback nor run by hand after one.
func BackupPath(exe string) string {
	if ext := filepath.Ext(exe); ext != "" {
		return strings.TrimSuffix(exe, ext) + BackupSuffix + ext
	}
	return exe + BackupSuffix
}

// PruneBackup deletes the backup an update left behind once it is older than
// BackupTTL, and reports whether it deleted one.
//
// It runs at the top of every crier invocation, so its cost matters: with no
// backup present, which is every run after the first week, it is one stat of a
// path that does not exist. It writes nothing else, so a read-only install
// directory is fine, and it reports no errors because housekeeping must never
// be the reason a command fails.
func PruneBackup(exe string, now time.Time) bool {
	if exe == "" {
		return false
	}
	backup := BackupPath(exe)
	info, err := os.Stat(backup)
	if err != nil || info.IsDir() {
		return false
	}
	if now.Sub(info.ModTime()) < BackupTTL {
		return false
	}
	return os.Remove(backup) == nil
}

// commandOr is the command word a Source or an Installer names in what it
// reports: the word the operator typed rather than the package doing the work.
func commandOr(command string) string {
	if command == "" {
		return "self-update"
	}
	return command
}
