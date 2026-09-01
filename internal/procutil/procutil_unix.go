//go:build !windows

package procutil

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the child in a process group of its own, so the
// whole tree can be signalled at once and so a Ctrl-C in the terminal does not
// reach it before crier has had a chance to clean up.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate asks the process group to exit.
func terminate(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		// The group may already be gone, or the child may never have been put
		// in one; signalling the process itself is the fallback.
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
}
