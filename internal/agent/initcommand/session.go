package initcommand

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

// The reasons a call produces no result for its policy. Their text is
// what an operator and the run's failure note see, so it names no
// argument, output or image.
var (
	ErrCalls     = errors.New("the initialization policy ran more than 8 commands")
	ErrArguments = errors.New("the initialization command's arguments exceed their bounds")
	ErrTimeout   = errors.New("the initialization command did not finish within its deadline")
	ErrCancelled = errors.New("the initialization command was cancelled")
	ErrOutput    = errors.New("the initialization command's output exceeded its bounds")
	ErrImage     = errors.New("the initialization command's runner image is unavailable")
	ErrHelper    = errors.New("the initialization command's helper failed")
)

type failure struct {
	kind  error
	cause error
}

func (f *failure) Error() string   { return f.kind.Error() }
func (f *failure) Unwrap() []error { return []error{f.kind, f.cause} }

// Session runs one evaluation's exec calls, each in a fresh helper the
// executor starts from the job's image, one at a time, at most MaxCalls,
// each within CallTimeout and the evaluation's deadline, and with at
// most MaxCombined bytes of output across them. It is the policy's
// rego.Commands for that evaluation alone.
type Session struct {
	commander executor.Commander
	job       executor.Command

	mu     sync.Mutex
	calls  int
	output int64
}

var _ rego.Commands = (*Session)(nil)

// NewSession is a session whose helpers c starts for job's worker, run,
// state version and image.
func NewSession(c executor.Commander, job executor.Command) *Session {
	return &Session{commander: c, job: job}
}

// Exec runs argv in a fresh helper and answers its exit status and
// output. A call beyond the session's bounds, a helper that cannot
// start, and a command that times out, is cancelled or exceeds its
// output bounds fail it, ending the helper; partial output is never a
// result.
func (s *Session) Exec(ctx context.Context, argv []string) (rego.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls >= MaxCalls {
		return rego.Command{}, ErrCalls
	}
	s.calls++
	if err := checkArgv(argv); err != nil {
		return rego.Command{}, &failure{ErrArguments, err}
	}
	cctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	deadline, _ := cctx.Deadline()
	req, err := EncodeRequest(Request{
		Version: Version, Call: s.calls, Argv: argv,
		TimeoutMillis: max(time.Until(deadline).Milliseconds(), 1),
	})
	if err != nil {
		return rego.Command{}, &failure{ErrArguments, err}
	}
	pr, pw := io.Pipe()
	job := s.job
	job.Call, job.Request, job.Output = s.calls, req, pw
	ran := make(chan error, 1)
	go func() {
		err := s.commander.Command(cctx, job)
		pw.CloseWithError(err)
		ran <- err
	}()
	out, rerr := readOutput(pr, s.calls, MaxCombined-s.output)
	if rerr != nil {
		// Cancelling ends the helper; ending the read then fails the write
		// it blocks in. In this order the helper's context is done before
		// any of its writes fails, so the helper ends for the cancellation
		// even when its executor returns as soon as a write fails.
		cancel()
		_ = pr.CloseWithError(rerr)
	}
	err = <-ran
	s.output += out.size()
	if err := classify(ctx, cctx, out, rerr, err); err != nil {
		return rego.Command{}, err
	}
	return rego.Command{Status: out.result.ExitCode, Stdout: out.stdout.String(), Stderr: out.stderr.String()}, nil
}

// classify answers why a call has no result for its policy, or nil when
// its helper reported its command's exit.
func classify(ctx, cctx context.Context, out *output, read, ran error) error {
	switch {
	case errors.Is(read, errOverflow):
		return &failure{ErrOutput, read}
	case errors.Is(ctx.Err(), context.Canceled):
		return &failure{ErrCancelled, ctx.Err()}
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		// A read that failed cancels cctx itself, which is no timeout.
		return &failure{ErrTimeout, cctx.Err()}
	case errors.Is(ran, executor.ErrImage):
		return &failure{ErrImage, ran}
	case ran != nil:
		return &failure{ErrHelper, ran}
	case read != nil:
		return &failure{ErrHelper, read}
	}
	switch out.result.Status {
	case StatusExited:
		return nil
	case StatusTimeout:
		return ErrTimeout
	case StatusOutput:
		return ErrOutput
	}
	return &failure{ErrHelper, errors.New(out.result.Status + ": " + out.result.Reason)} //nolint:err113 // the helper's own reason
}
