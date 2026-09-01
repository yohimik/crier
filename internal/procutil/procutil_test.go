package procutil

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// The tests drive real subprocesses, and the program they run is this test
// binary re-invoked with an environment variable. That keeps the fixture in
// the same file as the tests and needs no compiler at test time, which matters
// because the same trick is what the end-to-end tunnel and ffmpeg fakes use.
const helperEnv = "CRIER_PROCUTIL_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		helperMain(mode)
		return
	}
	os.Exit(m.Run())
}

func helperMain(mode string) {
	switch mode {
	case "print":
		fmt.Fprintln(os.Stdout, "hello from stdout")
		fmt.Fprintln(os.Stderr, "hello from stderr")
	case "fail":
		fmt.Fprintln(os.Stderr, "something went wrong")
		os.Exit(3)
	case "sleep":
		fmt.Fprintln(os.Stdout, "ready")
		time.Sleep(time.Minute)
	case "count-stdin":
		n, _ := io.Copy(io.Discard, os.Stdin)
		fmt.Fprintln(os.Stdout, "bytes:"+strconv.FormatInt(n, 10))
	case "spam":
		for i := 0; i < 100; i++ {
			fmt.Fprintln(os.Stdout, "line", i)
		}
	}
	os.Exit(0)
}

func helper(mode string, o Options) Options {
	o.Bin = os.Args[0]
	o.Env = append(os.Environ(), helperEnv+"="+mode)
	if o.Name == "" {
		o.Name = "helper"
	}
	return o
}

func TestStartCapturesBothStreams(t *testing.T) {
	var lines []string
	p, err := Start(context.Background(), helper("print", Options{
		OnLine: func(l string) { lines = append(lines, l) },
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "|")
	if !strings.Contains(joined, "hello from stdout") || !strings.Contains(joined, "hello from stderr") {
		t.Errorf("lines = %q", joined)
	}
	if !strings.Contains(p.Tail(), "hello from") {
		t.Errorf("tail = %q", p.Tail())
	}
	if p.Pid() == 0 {
		t.Error("no pid recorded")
	}
}

func TestWaitReportsExitStatusWithTheOutputTail(t *testing.T) {
	p, err := Start(context.Background(), helper("fail", Options{}))
	if err != nil {
		t.Fatal(err)
	}
	err = p.Wait()
	if err == nil {
		t.Fatal("expected a non-zero exit to be an error")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("the error should carry the output tail, got %v", err)
	}
	if !strings.Contains(err.Error(), "helper exited with") {
		t.Errorf("err = %v", err)
	}
	// Wait is idempotent.
	if again := p.Wait(); again == nil || again.Error() != err.Error() {
		t.Errorf("second Wait = %v", again)
	}
}

func TestStopTerminatesALongRunningProcess(t *testing.T) {
	ready := make(chan struct{})
	var once bool
	p, err := Start(context.Background(), helper("sleep", Options{
		StopTimeout: 2 * time.Second,
		OnLine: func(l string) {
			if !once && strings.Contains(l, "ready") {
				once = true
				close(ready)
			}
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("helper never became ready")
	}

	start := time.Now()
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("Stop took %v", time.Since(start))
	}
	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Error("Done was not closed")
	}
	// Stopping twice is harmless.
	if err := p.Stop(context.Background()); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestStdinIsWiredWhenAsked(t *testing.T) {
	var lines []string
	p, err := Start(context.Background(), helper("count-stdin", Options{
		WithStdin: true,
		OnLine:    func(l string) { lines = append(lines, l) },
	}))
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 1234)
	if _, err := io.WriteString(p.Stdin(), payload); err != nil {
		t.Fatal(err)
	}
	if err := p.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "") != "bytes:1234" {
		t.Errorf("lines = %v", lines)
	}
}

func TestStdinIsNilWithoutTheOption(t *testing.T) {
	p, err := Start(context.Background(), helper("print", Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Stdin() != nil {
		t.Error("Stdin should be nil unless WithStdin was set")
	}
	if err := p.CloseStdin(); err != nil {
		t.Errorf("CloseStdin without a pipe should be harmless: %v", err)
	}
	_ = p.Wait()
}

func TestTailIsBounded(t *testing.T) {
	p, err := Start(context.Background(), helper("spam", Options{TailLines: 5}))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(p.Tail(), "\n")
	if len(lines) != 5 {
		t.Fatalf("tail has %d lines, want 5", len(lines))
	}
	if !strings.Contains(lines[4], "line 99") {
		t.Errorf("tail should keep the last lines, got %q", lines[4])
	}
}

func TestCancellingTheContextStopsTheProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := Start(ctx, helper("sleep", Options{StopTimeout: 2 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan struct{})
	go func() { _ = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the process outlived its context")
	}
}

func TestLookPath(t *testing.T) {
	if _, err := LookPath(""); err == nil {
		t.Error("expected an error for an empty name")
	}
	if _, err := LookPath("crier-definitely-not-a-real-binary"); err == nil {
		t.Error("expected an error for a missing binary")
	}
	if got, err := LookPath(os.Args[0]); err != nil || got == "" {
		t.Errorf("LookPath(self) = %q, %v", got, err)
	}
}

func TestStartRejectsAMissingBinary(t *testing.T) {
	_, err := Start(context.Background(), Options{Bin: "crier-definitely-not-a-real-binary"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoggerReceivesOutput(t *testing.T) {
	var buf strings.Builder
	lg := zerolog.New(&buf).Level(zerolog.DebugLevel)
	p, err := Start(context.Background(), helper("print", Options{Logger: lg}))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "started helper process") {
		t.Errorf("no start record: %q", out)
	}
	if !strings.Contains(out, "hello from stdout") {
		t.Errorf("no output record: %q", out)
	}
}

func TestExitIgnored(t *testing.T) {
	if exitIgnored(nil) != nil {
		t.Error("nil stays nil")
	}
	if err := exitIgnored(context.Canceled); err != nil {
		t.Errorf("a cancelled stop is not a failure: %v", err)
	}
	if err := exitIgnored(io.EOF); err == nil {
		t.Error("an unrelated error is a failure")
	}
}
