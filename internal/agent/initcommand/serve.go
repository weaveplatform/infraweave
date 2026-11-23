package initcommand

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// waitDelay bounds how long a helper waits for a command's output pipes
// once the command is ended, as when a child it started still holds them.
// The helper is its container's root process, so its own end takes such
// a child with it.
const waitDelay = 2 * time.Second

// Serve is the helper: it reads one request from in, executes its
// argument vector without a shell, with no standard input and its
// container's own environment, writes the command's output to out as
// frames, and ends them with the result. A command that outlasts its
// request's deadline, or writes more than MaxOutput to either stream, is
// killed and its result says so; output beyond the bound is not written.
// A command ctx ends, as the helper's container stops, is killed and its
// result is StatusStopped, never an exit. A refused request still gets a
// result, and Serve then fails.
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	f := &frames{w: out}
	req, err := ReadRequest(in)
	if err != nil {
		return errors.Join(err, WriteResult(f, Result{Call: req.Call, Status: StatusRefused, Reason: "invalid request"}))
	}
	return WriteResult(f, execute(ctx, req, f))
}

func execute(ctx context.Context, req Request, f *frames) Result {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.TimeoutMillis)*time.Millisecond)
	defer cancel()
	stdout := &stream{f: f, cancel: cancel}
	stderr := &stream{f: f, stderr: true, cancel: cancel}
	cmd := exec.CommandContext(ctx, req.Argv[0], req.Argv[1:]...) //nolint:gosec // the policy's explicit argument vector, in its isolated helper
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = waitDelay
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case stdout.exceeded() || stderr.exceeded():
		return Result{Call: req.Call, Status: StatusOutput}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return Result{Call: req.Call, Status: StatusTimeout}
	case ctx.Err() != nil:
		// The helper's container is stopping, as when its Pod's active
		// deadline ends it: the command, killed or never started, did not
		// end by itself, and its exit code reaching the policy would admit
		// a run a deny on a zero status means to refuse.
		return Result{Call: req.Call, Status: StatusStopped}
	case errors.As(err, &exit):
		return Result{Call: req.Call, Status: StatusExited, ExitCode: exit.ExitCode()}
	case err != nil && cmd.Process == nil:
		// The command never started; as a shell, answer 127 with why.
		_, _ = fmt.Fprintf(stderr, "infraweave: %v\n", err)
		return Result{Call: req.Call, Status: StatusExited, ExitCode: 127}
	case err != nil:
		return Result{Call: req.Call, Status: StatusExited, ExitCode: -1, Reason: "the command's output could not be read"}
	}
	return Result{Call: req.Call, Status: StatusExited}
}

// frames serializes the frames of a command's two streams and its result.
type frames struct {
	mu sync.Mutex
	w  io.Writer
}

func (f *frames) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.w.Write(p) //nolint:wrapcheck // the helper's standard output
}

// stream frames one of a command's output streams, failing, and ending
// the command through cancel, once it would exceed MaxOutput.
type stream struct {
	f      *frames
	stderr bool
	cancel context.CancelFunc

	mu      sync.Mutex
	written int
	over    bool
}

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.over || s.written+len(p) > MaxOutput {
		s.over = true
		s.cancel()
		return 0, errOverflow
	}
	s.written += len(p)
	if err := WriteOutput(s.f, s.stderr, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *stream) exceeded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.over
}
