// Package procutil runs the helper programs crier shells out to: the tunnel
// that exposes the staging server, and the ffmpeg that encodes a video.
//
// Both want the same three things, and both get them wrong in the same ways
// when written twice: their output has to reach the log while also being kept
// for the error message, they have to die with crier rather than outlive it,
// and stopping them has to escalate from "please" to "now".
package procutil

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// DefaultTailLines is how many lines of output are kept for an error message.
const DefaultTailLines = 30

// Options describes a process to run.
type Options struct {
	// Name labels the process in logs and errors: "ngrok", "ffmpeg".
	Name string
	// Bin is the executable, looked up on PATH when it has no separator.
	Bin string
	// Args are the arguments, not including the executable.
	Args []string
	// Dir is the working directory. Empty means crier's own.
	Dir string
	// Env replaces the environment when non-nil.
	Env []string
	// Logger receives every output line at debug level.
	Logger zerolog.Logger
	// WithStdin wires a pipe to the process's standard input, for a program
	// that is fed rather than only watched.
	WithStdin bool
	// OnLine, when set, is called for every line of output, in addition to the
	// logging. It is how the tunnel finds the URL its program printed.
	OnLine func(line string)
	// TailLines caps the retained output. Zero means DefaultTailLines.
	TailLines int
	// StopTimeout is how long a polite termination is given before the process
	// is killed. Zero means five seconds.
	StopTimeout time.Duration
}

// Process is a running helper program.
type Process struct {
	name  string
	cmd   *exec.Cmd
	stdin io.WriteCloser

	stopTimeout time.Duration

	// emitMu serialises the two reader goroutines, so a caller's OnLine and the
	// logger's writer are only ever entered by one of them at a time. It is not
	// the tail's own lock, so an OnLine that reads Tail cannot deadlock.
	emitMu sync.Mutex

	mu   sync.Mutex
	tail []string
	max  int

	wg       sync.WaitGroup
	waitOnce sync.Once
	waitErr  error
	done     chan struct{}
}

// LookPath resolves an executable the way the shell would, so a missing
// prerequisite is reported before anything else is set up.
func LookPath(bin string) (string, error) {
	if strings.TrimSpace(bin) == "" {
		return "", errors.New("no executable given")
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("%s not found: %w", bin, err)
	}
	return path, nil
}

// Start launches the process.
//
// Output is read on background goroutines: a program whose pipe fills up
// blocks forever, and a tunnel that has printed its URL and is then ignored is
// exactly that program.
func Start(ctx context.Context, o Options) (*Process, error) {
	if _, err := LookPath(o.Bin); err != nil {
		return nil, err
	}
	name := o.Name
	if name == "" {
		name = o.Bin
	}
	maxTail := o.TailLines
	if maxTail <= 0 {
		maxTail = DefaultTailLines
	}
	stopTimeout := o.StopTimeout
	if stopTimeout <= 0 {
		stopTimeout = 5 * time.Second
	}

	cmd := exec.CommandContext(ctx, o.Bin, o.Args...)
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	// A cancelled context kills the process; WaitDelay makes sure Wait returns
	// even when the child holds its pipes open after being signalled.
	cmd.WaitDelay = stopTimeout
	configureProcessGroup(cmd)

	p := &Process{
		name:        name,
		cmd:         cmd,
		stopTimeout: stopTimeout,
		max:         maxTail,
		done:        make(chan struct{}),
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if o.WithStdin {
		p.stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	o.Logger.Debug().Str("proc", name).Int("pid", cmd.Process.Pid).
		Strs("args", o.Args).Msg("started helper process")

	p.wg.Add(2)
	go p.read(stdout, "stdout", o)
	go p.read(stderr, "stderr", o)
	// The process is reaped as soon as it exits, without waiting to be asked.
	// Done is what a caller watches while it waits for the program to say
	// something, and a tunnel that dies on startup has to close that channel
	// then rather than at the end of the caller's timeout.
	go func() { _ = p.Wait() }()
	return p, nil
}

func (p *Process) read(r io.Reader, stream string, o Options) {
	defer p.wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		p.emit(sc.Text(), stream, o)
	}
}

// emit records one line, logs it, and hands it to the caller — one line at a
// time however many streams are producing them.
func (p *Process) emit(line, stream string, o Options) {
	p.emitMu.Lock()
	defer p.emitMu.Unlock()
	p.record(line)
	o.Logger.Debug().Str("proc", p.name).Str("stream", stream).Msg(line)
	if o.OnLine != nil {
		o.OnLine(line)
	}
}

func (p *Process) record(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tail = append(p.tail, line)
	if len(p.tail) > p.max {
		p.tail = p.tail[len(p.tail)-p.max:]
	}
}

// Tail is the last lines the process printed, for an error message.
func (p *Process) Tail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.tail, "\n")
}

// Pid is the process id, or zero when it never started.
func (p *Process) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Stdin is the pipe to the process's standard input, nil unless WithStdin was
// set.
func (p *Process) Stdin() io.WriteCloser { return p.stdin }

// CloseStdin closes the input pipe, which is how a program that reads until
// end of input is told there is no more.
func (p *Process) CloseStdin() error {
	if p.stdin == nil {
		return nil
	}
	return p.stdin.Close()
}

// Done is closed once the process has exited and Wait has collected it.
func (p *Process) Done() <-chan struct{} { return p.done }

// Wait waits for the process to exit and returns what it exited with. It is
// safe to call more than once and always returns the same answer.
func (p *Process) Wait() error {
	p.waitOnce.Do(func() {
		// The readers have to finish before Wait closes the pipes.
		p.wg.Wait()
		err := p.cmd.Wait()
		if err != nil {
			tail := p.Tail()
			if tail != "" {
				err = fmt.Errorf("%s exited with %w\n%s", p.name, err, tail)
			} else {
				err = fmt.Errorf("%s exited with %w", p.name, err)
			}
		}
		p.waitErr = err
		close(p.done)
	})
	<-p.done
	return p.waitErr
}

// Stop asks the process to exit, and kills it if it will not.
//
// The signal goes to the whole process group where the platform has one: ngrok
// and ffmpeg both spawn children, and signalling only the parent leaves those
// children holding the port or the output file.
func (p *Process) Stop(ctx context.Context) error {
	if p.cmd.Process == nil {
		return nil
	}
	select {
	case <-p.done:
		return exitIgnored(p.waitErr)
	default:
	}

	terminate(p.cmd)

	waited := make(chan error, 1)
	go func() { waited <- p.Wait() }()

	timer := time.NewTimer(p.stopTimeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		return exitIgnored(err)
	case <-timer.C:
	case <-ctx.Done():
	}

	_ = p.cmd.Process.Kill()
	select {
	case err := <-waited:
		return exitIgnored(err)
	case <-time.After(p.stopTimeout):
		return fmt.Errorf("%s did not exit after being killed", p.name)
	}
}

// exitIgnored drops the error a process reports for having been asked to stop:
// being signalled is the expected outcome of Stop, not a failure.
func exitIgnored(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}
