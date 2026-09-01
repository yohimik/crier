// Command crier renders an HTML template to an image or a video and publishes
// it to social platforms.
//
// This file is wiring only: the signal handling, the standard streams and the
// exit code. Everything else lives in internal/app.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/yohimik/crier/internal/app"
)

func main() {
	// A Ctrl-C cancels the run rather than killing it, so the deferred cleanup
	// gets its chance to delete what was uploaded and stop what was spawned.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := app.App{
		Args:   os.Args[1:],
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}.Run(ctx)

	os.Exit(code)
}
