package config

import (
	"flag"
	"io"
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
