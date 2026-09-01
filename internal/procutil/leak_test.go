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

	// Whether the parent's last line arrived is deliberately not asserted
	// here: the grandchild holds the write end open, so the readers never see
	// EOF and the drain window is a bound rather than a guarantee. Output
	// preservation is asserted below, against a child that closes its pipes.

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

// TestOrdinaryOutputSurvivesTheReordering: reaping before draining must not
// cost the output of a process that closes its pipes, which is every process
// crier actually runs.
func TestOrdinaryOutputSurvivesTheReordering(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "chatty.sh")
	body := "#!/bin/sh\necho first\necho second >&2\necho third\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // a test script has to run
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		proc, err := Start(context.Background(), Options{
			Name:   "chatty",
			Bin:    "/bin/sh",
			Args:   []string{script},
			Logger: zerolog.Nop(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := proc.Wait(); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		tail := proc.Tail()
		for _, want := range []string{"first", "second", "third"} {
			if !strings.Contains(tail, want) {
				t.Fatalf("run %d lost %q from its output: %q", i, want, tail)
			}
		}
	}
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
