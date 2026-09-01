// Package version exposes build metadata, injected at link time via -ldflags.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// These variables are overridden at build time with
//
//	-ldflags "-X github.com/yohimik/crier/internal/version.Version=v1.2.3 ..."
var (
	// Version is the semantic version of the build ("dev" for local builds).
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "none"
	// Date is the RFC3339 build timestamp.
	Date = "unknown"
)

// Info is a structured description of the running binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the build information for the running binary.
//
// When the linker flags were not provided (`go run`, `go install` of a tagged
// module), it falls back to the VCS stamps recorded by the Go toolchain.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if info.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "none" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "unknown" {
				info.Date = s.Value
			}
		}
	}
	return info
}

// String renders a one line human readable summary.
func (i Info) String() string {
	return fmt.Sprintf("crier %s (commit %s, built %s, %s, %s)",
		i.Version, i.Commit, i.Date, i.GoVersion, i.Platform)
}
