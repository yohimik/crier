//go:build windows

package procutil

import "os/exec"

// configureProcessGroup does nothing on Windows: there is no process group to
// put the child in, and CREATE_NEW_PROCESS_GROUP would only change how Ctrl
// events are delivered, which crier does not use.
func configureProcessGroup(cmd *exec.Cmd) {}

// terminate kills the process. Windows has no SIGTERM, so the polite step of
// the escalation does not exist and Stop's timeout never has anything to wait
// for beyond the kill itself.
func terminate(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
