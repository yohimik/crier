package config

import (
	"flag"
	"io"
	"testing"

	dispat "github.com/yohimik/dispat/pkg/config"
)

type flagSetHarness struct {
	fs *flag.FlagSet
}

// newSilentFlagSet builds a FlagSet that does not print to stderr and does not
// exit the test process when parsing fails.
func newSilentFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("crier-test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// mustOverrides is Flags.Overrides for a test that expects the flags to be
// valid; the error path has tests of its own.
func mustOverrides(t *testing.T, f *Flags) dispat.Overrides {
	t.Helper()
	out, err := f.Overrides()
	if err != nil {
		t.Fatalf("Overrides: %v", err)
	}
	return out
}
