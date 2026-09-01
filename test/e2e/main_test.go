//go:build e2e

// Package e2e drives the real crier binary from the outside.
//
// Every other test in the repository calls Go functions; these run the program
// a user would run, with a configuration file on disk, an environment, and
// fake platforms on the other end of the network. That is the only way to
// check the things that only exist at the edges: exit codes, what lands on
// standard output, and whether the config a project directory carries is the
// one that gets used.
package e2e

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// crierBin is the binary under test, built once by TestMain — or, when
// binaryEnv names one, the prebuilt binary itself.
var crierBin string

// binaryEnv points the suite at a binary that already exists instead of
// building one.
//
// It is what lets the release build run these tests against the exact bytes it
// is about to upload. A binary that only compiled has been proven to compile;
// running the suite against the artifact itself is what proves the artifact
// works.
const binaryEnv = "CRIER_E2E_BINARY"

// smokeSubset is the -run pattern the release build uses. Every test named
// TestSmoke... is in it, and the set is deliberately small: a render, the
// configuration precedence, the publish fan-out, and the version stamp.
const smokeSubset = "^TestSmoke"

// coverDir collects the coverage the subprocesses produce, so a black-box run
// counts towards the same profile the unit tests do.
var coverDir string

// helperEnv makes this test binary stand in for a helper program: a tunnel, or
// ffmpeg. crier spawns it like any other executable.
const (
	helperEnv     = "CRIER_E2E_HELPER"
	helperURLEnv  = "CRIER_E2E_HELPER_URL"
	helperFailEnv = "CRIER_E2E_HELPER_FAIL"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		helperMain(mode)
		return
	}
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e setup:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	root, err := repoRoot()
	if err != nil {
		return 1, err
	}
	work, err := os.MkdirTemp("", "crier-e2e-")
	if err != nil {
		return 1, err
	}
	defer func() { _ = os.RemoveAll(work) }()

	if prebuilt := os.Getenv(binaryEnv); prebuilt != "" {
		// A released binary is not instrumented, so there is no coverage to
		// collect and GOCOVERDIR must not be set: an uninstrumented binary
		// with GOCOVERDIR set writes nothing, and a stale directory would be
		// merged into the profile as if it had.
		abs, err := filepath.Abs(prebuilt)
		if err != nil {
			return 1, err
		}
		if _, err := os.Stat(abs); err != nil {
			return 1, fmt.Errorf("%s: %w", binaryEnv, err)
		}
		crierBin = abs
		fmt.Fprintln(os.Stderr, "e2e: testing the prebuilt binary", abs)
		return m.Run(), nil
	}

	crierBin = filepath.Join(work, "crier")
	if runtime.GOOS == "windows" {
		crierBin += ".exe"
	}
	// -cover instruments the binary; GOCOVERDIR at run time is where it writes.
	// atomic rather than the default set mode: the profile is merged with the
	// unit suite's, and covdata refuses to merge two modes.
	build := exec.Command("go", "build", "-cover", "-covermode=atomic",
		"-coverpkg=github.com/yohimik/crier/...", "-o", crierBin, "./cmd/crier")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return 1, fmt.Errorf("building crier: %v\n%s", err, out)
	}

	coverDir = os.Getenv("GOCOVERDIR")
	if coverDir == "" {
		coverDir = filepath.Join(work, "covdata")
	}
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		return 1, err
	}
	return m.Run(), nil
}

// repoRoot finds the module root from this test file's own location.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate the test source")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

// helperMain is the stand-in program.
func helperMain(mode string) {
	switch mode {
	case "tunnel":
		if msg := os.Getenv(helperFailEnv); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "tunnel ready url=%s\n", os.Getenv(helperURLEnv))
		time.Sleep(2 * time.Minute)
	case "ffmpeg-poster":
		// Extracting a poster reads a file rather than a stream, so this
		// helper writes the JPEG the caller asked for and reads nothing.
		out := os.Args[len(os.Args)-1]
		_, _ = copyAll(os.Stdin)
		_ = os.WriteFile(out, jpegBytes(), 0o600)
		fmt.Fprintln(os.Stderr, "fake ffmpeg wrote a poster")
	case "ffmpeg":
		// Read the raw frames, then write a file that stands in for the clip.
		// A GIF gets the real magic bytes, so a test can assert that what
		// reached a platform is an animation rather than a video.
		n, _ := copyAll(os.Stdin)
		out := os.Args[len(os.Args)-1]
		body := "fake mp4 from " + strconv.FormatInt(n, 10) + " bytes"
		if strings.HasSuffix(out, ".gif") {
			body = "GIF89a fake gif from " + strconv.FormatInt(n, 10) + " bytes"
		}
		_ = os.WriteFile(out, []byte(body), 0o600)
		_ = os.WriteFile(out+".bytes", []byte(strconv.FormatInt(n, 10)), 0o600)
		fmt.Fprintln(os.Stderr, "fake ffmpeg wrote", n, "bytes")
	}
	os.Exit(0)
}

// jpegBytes is a real one-pixel JPEG, so what the fake ffmpeg writes can be
// decoded by whatever reads it back.
func jpegBytes() []byte {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil)
	return buf.Bytes()
}

func copyAll(f *os.File) (int64, error) {
	var total int64
	buf := make([]byte, 1<<16)
	for {
		n, err := f.Read(buf)
		total += int64(n)
		if err != nil {
			return total, nil
		}
	}
}

// result is one crier run.
type result struct {
	Code   int
	Stdout string
	Stderr string
}

// crierCmd builds the command without running it, for a test that needs to
// wire standard input.
func crierCmd(t *testing.T, dir string, env []string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(crierBin, args...)
	cmd.Dir = dir
	// The helper environment is deliberately passed through: when crier spawns
	// a tunnel or an ffmpeg it inherits this environment, and that is what
	// turns this same binary into the helper.
	extra := env
	if coverDir != "" {
		extra = append([]string{"GOCOVERDIR=" + coverDir}, env...)
	}
	cmd.Env = append(os.Environ(), extra...)
	return cmd
}

// runCmd runs a prepared command and collects the result.
func runCmd(t *testing.T, cmd *exec.Cmd) result {
	t.Helper()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		if !asExitError(err, &exitErr) {
			t.Fatalf("running crier: %v\n%s", err, stderr.String())
		}
		code = exitErr.ExitCode()
	}
	res := result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
	t.Logf("crier %s -> %d", strings.Join(cmd.Args[1:], " "), code)
	if testing.Verbose() {
		t.Logf("stdout:\n%s", res.Stdout)
		t.Logf("stderr:\n%s", res.Stderr)
	}
	return res
}

// crier runs the binary in dir with the given environment additions.
func crier(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	return runCmd(t, crierCmd(t, dir, env, args...))
}

func asExitError(err error, out **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*out = e
	}
	return ok
}

// selfPath is this test binary, which crier is pointed at as a helper program.
func selfPath(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self
}
