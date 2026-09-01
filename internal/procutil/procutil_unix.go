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
func terminate(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGTERM) }

// killGroup ends the whole group without asking.
//
// The group rather than the process: a helper that spawned children and is
// then killed on its own leaves them running, holding the port or the output
// file the next run needs.
func killGroup(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGKILL) }

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		// The group may already be gone, or the child may never have been put
		// in one; signalling the process itself is the fallback.
		_ = cmd.Process.Signal(sig)
	}
}
