//go:build !windows

package procutil

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// TestWaitReturnsWhenAGrandchildHoldsThePipes is a regression test.
//
// Wait used to drain the output readers before reaping the process. A script
// that ends in `something &` leaves that background grandchild holding the
// write end of stdout, so the readers never see EOF — and because cmd.Wait was
// never reached, exec's WaitDelay never force-closed anything either. A custom
// platform whose script backgrounds a child hung `crier publish` forever, past
// every timeout crier has.
func TestWaitReturnsWhenAGrandchildHoldsThePipes(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "leak.sh")
	// The grandchild inherits stdout and outlives the script that started it.
	body := "#!/bin/sh\n" +
		"sleep 120 &\n" +
		"echo $! > " + marker + "\n" +
		"echo parent done\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // a test script has to run
		t.Fatal(err)
	}

	proc, err := Start(context.Background(), Options{
		Name:        "leaky",
		Bin:         "/bin/sh",
		Args:        []string{script},
		Logger:      zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
		StopTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the script exited 0: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Wait never returned: the grandchild still holds the pipes")
	}

	// The parent's own output still arrived, which is what the drain window is
	// for.
	if !strings.Contains(proc.Tail(), "parent done") {
		t.Errorf("the script's output was lost: %q", proc.Tail())
	}

	// And the grandchild is reachable through the group, so Stop can end it.
	pid := grandchildPID(t, marker)
	if pid > 0 {
		if err := proc.Stop(context.Background()); err != nil {
			t.Errorf("Stop: %v", err)
		}
		// Killing the group is what ends a grandchild the parent left behind.
		killGroup(proc.cmd)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !alive(pid) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Errorf("the grandchild %d survived the group kill", pid)
	}
}

// TestStopKillsTheWholeGroup covers the escalation path: a process that ignores
// SIGTERM is killed, and so is everything it started.
func TestStopKillsTheWholeGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "stubborn.sh")
	body := "#!/bin/sh\n" +
		"trap '' TERM\n" +
		"sleep 120 &\n" +
		"echo $! > " + marker + "\n" +
		"wait\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // a test script has to run
		t.Fatal(err)
	}

	proc, err := Start(context.Background(), Options{
		Name:        "stubborn",
		Bin:         "/bin/sh",
		Args:        []string{script},
		Logger:      zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel),
		StopTimeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	pid := grandchildPID(t, marker)
	if pid == 0 {
		t.Skip("the grandchild never reported its pid")
	}

	start := time.Now()
	if err := proc.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Stop took %s", elapsed)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("the grandchild %d outlived the group it was in", pid)
}

// grandchildPID waits for the script to report the pid it backgrounded.
func grandchildPID(t *testing.T, marker string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if body, err := os.ReadFile(marker); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(body))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0
}

// alive reports whether a pid is still running. Signal 0 is the portable way
// to ask: it delivers nothing and answers whether it could have.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
