package initcommand

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
)

// program is the test binary acting as a helper's command, so Serve runs
// a real process on every OS.
const program = "initcommand-program"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == program {
		os.Exit(run(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// run is the command: echo its arguments to stdout and stderr, exit with
// a code, write n bytes to a stream, sleep, or say it is ready and sleep.
func run(args []string) int {
	switch args[0] {
	case "echo":
		fmt.Fprint(os.Stdout, strings.Join(args[1:], " "))
		fmt.Fprint(os.Stderr, "err:"+strings.Join(args[1:], " "))
		return 0
	case "exit":
		code, _ := strconv.Atoi(args[1])
		return code
	case "write":
		n, _ := strconv.Atoi(args[2])
		w := os.Stdout
		if args[1] == "stderr" {
			w = os.Stderr
		}
		_, _ = w.Write(bytes.Repeat([]byte("x"), n))
		return 0
	case "sleep":
		<-time.After(time.Minute)
	case "ready":
		fmt.Fprint(os.Stdout, "ready")
		<-time.After(time.Minute)
	}
	return 2
}

func self(t *testing.T, args ...string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return append([]string{exe, program}, args...)
}

func request(t *testing.T, call int, argv []string, timeout time.Duration) []byte {
	t.Helper()
	b, err := EncodeRequest(Request{Version: Version, Call: call, Argv: argv, TimeoutMillis: timeout.Milliseconds()})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func serve(t *testing.T, req []byte) (*output, error) {
	t.Helper()
	var out bytes.Buffer
	err := Serve(t.Context(), bytes.NewReader(req), &out)
	call := 1
	if err != nil {
		// A refused request's call is unknown.
		call = 0
	}
	o, rerr := readOutput(&out, call, MaxCombined)
	if rerr != nil {
		t.Fatalf("the helper's frames: %v", rerr)
	}
	return o, err
}

func TestUnitServeRunsTheCommandAndFramesItsOutput(t *testing.T) {
	o, err := serve(t, request(t, 1, self(t, "echo", "a b", "$HOME"), time.Minute))
	if err != nil || *o.result != (Result{Call: 1, Status: StatusExited}) || o.stdout.String() != "a b $HOME" || o.stderr.String() != "err:a b $HOME" {
		t.Fatalf("echo: %+v %q %q, %v", o.result, o.stdout.String(), o.stderr.String(), err)
	}
	o, err = serve(t, request(t, 1, self(t, "exit", "7"), time.Minute))
	if err != nil || *o.result != (Result{Call: 1, Status: StatusExited, ExitCode: 7}) {
		t.Fatalf("exit 7: %+v, %v", o.result, err)
	}
	o, err = serve(t, request(t, 1, []string{"infraweave-no-such-command"}, time.Minute))
	if err != nil || o.result.ExitCode != 127 || !strings.Contains(o.stderr.String(), "infraweave: ") {
		t.Fatalf("a missing command: %+v %q, %v", o.result, o.stderr.String(), err)
	}
	o, err = serve(t, request(t, 1, self(t, "write", "stdout", strconv.Itoa(3*MaxChunk)), time.Minute))
	if err != nil || o.result.Status != StatusExited || o.stdout.Len() != 3*MaxChunk {
		t.Fatalf("3 chunks: %+v %d, %v", o.result, o.stdout.Len(), err)
	}
}

func TestUnitServeEndsACommandAtItsBounds(t *testing.T) {
	started := time.Now()
	o, err := serve(t, request(t, 1, self(t, "sleep"), 200*time.Millisecond))
	if err != nil || o.result.Status != StatusTimeout || time.Since(started) > 30*time.Second {
		t.Fatalf("a sleeping command: %+v after %s, %v", o.result, time.Since(started), err)
	}
	for _, stream := range []string{"stdout", "stderr"} {
		o, err := serve(t, request(t, 1, self(t, "write", stream, strconv.Itoa(MaxOutput+1)), time.Minute))
		if err != nil || o.result.Status != StatusOutput || o.size() > MaxOutput {
			t.Fatalf("%s beyond its bound: %+v %d, %v", stream, o.result, o.size(), err)
		}
	}
	o, err = serve(t, []byte("\x00\x00\x00\x02{}"))
	if !errors.Is(err, ErrFrame) || o.result.Status != StatusRefused {
		t.Fatalf("a refused request: %+v, %v", o.result, err)
	}
}

// stopper is a helper's standard output that stops the helper, as its
// container's runtime does, once the command has written its first frame.
type stopper struct {
	bytes.Buffer
	stop context.CancelFunc
}

func (s *stopper) Write(p []byte) (int, error) {
	s.stop()
	return s.Buffer.Write(p) //nolint:wrapcheck // a bytes.Buffer
}

// TestUnitServeNeverAnswersAStoppedCommandAsAnExit checks that a command
// the helper's stop kills, or never starts, is StatusStopped: a killed
// command's -1, or the 127 of one that cannot start, would reach a policy
// that denies only on a zero status as an ordinary exit.
func TestUnitServeNeverAnswersAStoppedCommandAsAnExit(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	out := &stopper{stop: stop}
	if err := Serve(ctx, bytes.NewReader(request(t, 1, self(t, "ready"), time.Minute)), out); err != nil {
		t.Fatal(err)
	}
	o, err := readOutput(&out.Buffer, 1, MaxCombined)
	if err != nil || *o.result != (Result{Call: 1, Status: StatusStopped}) || o.stdout.String() != "ready" {
		t.Fatalf("a command stopped while it runs: %+v %q, %v", o.result, o.stdout.String(), err)
	}

	var b bytes.Buffer
	if err := Serve(ctx, bytes.NewReader(request(t, 1, self(t, "exit", "0"), time.Minute)), &b); err != nil {
		t.Fatal(err)
	}
	o, err = readOutput(&b, 1, MaxCombined)
	if err != nil || *o.result != (Result{Call: 1, Status: StatusStopped}) {
		t.Fatalf("a command of a stopped helper: %+v, %v", o.result, err)
	}
}

func TestUnitRequestFramesAreStrictAndBounded(t *testing.T) {
	ok := Request{Version: Version, Call: 1, Argv: []string{"verify", "x"}, TimeoutMillis: 1000}
	b, err := EncodeRequest(ok)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadRequest(bytes.NewReader(b))
	if err != nil || got.Call != 1 || strings.Join(got.Argv, " ") != "verify x" {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	bad := []Request{
		{Version: 2, Call: 1, Argv: []string{"a"}, TimeoutMillis: 1},
		{Version: 1, Call: 0, Argv: []string{"a"}, TimeoutMillis: 1},
		{Version: 1, Call: MaxCalls + 1, Argv: []string{"a"}, TimeoutMillis: 1},
		{Version: 1, Call: 1, Argv: []string{"a"}, TimeoutMillis: 0},
		{Version: 1, Call: 1, Argv: []string{"a"}, TimeoutMillis: CallTimeout.Milliseconds() + 1},
		{Version: 1, Call: 1, TimeoutMillis: 1},
		{Version: 1, Call: 1, Argv: []string{""}, TimeoutMillis: 1},
		{Version: 1, Call: 1, Argv: make([]string, MaxArgs+1), TimeoutMillis: 1},
		{Version: 1, Call: 1, Argv: []string{strings.Repeat("a", MaxArg+1)}, TimeoutMillis: 1},
		{Version: 1, Call: 1, Argv: []string{"a\x00"}, TimeoutMillis: 1},
		{Version: 1, Call: 1, Argv: append([]string{"a"}, slicesOf(strings.Repeat("a", MaxArg), 8)...), TimeoutMillis: 1},
	}
	for i, r := range bad {
		if _, err := EncodeRequest(r); !errors.Is(err, ErrFrame) {
			t.Errorf("request %d encoded: %v", i, err)
		}
	}
	frame := func(body string) []byte {
		return append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...) //nolint:gosec // test sizes
	}
	for name, raw := range map[string][]byte{
		"empty":         nil,
		"zero length":   frame(""),
		"oversized":     binary.BigEndian.AppendUint32(nil, MaxRequest+1),
		"truncated":     frame(`{"version":1}`)[:8],
		"trailing":      append(bytes.Clone(b), 'x'),
		"unknown field": frame(`{"version":1,"call":1,"argv":["a"],"timeoutMillis":1,"env":[]}`),
		"trailing json": frame(`{"version":1,"call":1,"argv":["a"],"timeoutMillis":1}{}`),
		"invalid":       frame(`{"version":1,"call":1,"argv":[],"timeoutMillis":1}`),
	} {
		if _, err := ReadRequest(bytes.NewReader(raw)); !errors.Is(err, ErrFrame) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func slicesOf(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestUnitOutputFramesAreBoundedBeforeTheyAreKept(t *testing.T) {
	result := func(w io.Writer, call int) {
		_ = WriteResult(w, Result{Call: call, Status: StatusExited, Reason: strings.Repeat("r", 300)})
	}
	var b bytes.Buffer
	_ = WriteOutput(&b, false, []byte("out"))
	_ = WriteOutput(&b, true, []byte("err"))
	result(&b, 2)
	o, err := readOutput(&b, 2, MaxCombined)
	if err != nil || o.stdout.String() != "out" || o.stderr.String() != "err" || len(o.result.Reason) != 256 {
		t.Fatalf("frames: %+v, %v", o, err)
	}
	cases := map[string]func(w *bytes.Buffer){
		"no result":       func(w *bytes.Buffer) { _ = WriteOutput(w, false, []byte("x")) },
		"another call":    func(w *bytes.Buffer) { result(w, 3) },
		"after result":    func(w *bytes.Buffer) { result(w, 2); _ = WriteOutput(w, false, []byte("x")) },
		"unknown kind":    func(w *bytes.Buffer) { _ = writeFrame(w, 9, []byte("x")) },
		"empty chunk":     func(w *bytes.Buffer) { _ = writeFrame(w, kindStdout, nil) },
		"oversized chunk": func(w *bytes.Buffer) { _ = writeFrame(w, kindStdout, make([]byte, MaxChunk+1)) },
		"truncated chunk": func(w *bytes.Buffer) { _ = writeFrame(w, kindStdout, []byte("xy")); w.Truncate(w.Len() - 1) },
		"empty result":    func(w *bytes.Buffer) { _ = writeFrame(w, kindResult, nil) },
		"bad result":      func(w *bytes.Buffer) { _ = writeFrame(w, kindResult, []byte(`{"call":2,"x":1}`)) },
		"cut result":      func(w *bytes.Buffer) { _ = writeFrame(w, kindResult, []byte(`{"call":2}`)); w.Truncate(w.Len() - 1) },
	}
	for name, write := range cases {
		var w bytes.Buffer
		write(&w)
		if _, err := readOutput(&w, 2, MaxCombined); !errors.Is(err, ErrFrame) {
			t.Errorf("%s: %v", name, err)
		}
	}
	var w bytes.Buffer
	_ = WriteOutput(&w, true, make([]byte, MaxOutput+1))
	if _, err := readOutput(&w, 2, MaxCombined); !errors.Is(err, errOverflow) {
		t.Errorf("stderr beyond its bound: %v", err)
	}
	w.Reset()
	_ = WriteOutput(&w, false, make([]byte, 10))
	if o, err := readOutput(&w, 2, 9); !errors.Is(err, errOverflow) || o.size() != 0 {
		t.Errorf("beyond the evaluation's budget: %v", err)
	}
	if err := WriteOutput(failing{}, false, []byte("x")); err == nil {
		t.Error("a failed write succeeded")
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// helper is a fake executor.Commander: it serves each request in-process,
// as the helper container would, or does what its hook says.
type helper struct {
	mu   sync.Mutex
	jobs []executor.Command
	hook func(ctx context.Context, c executor.Command) error
	done []error
}

func (h *helper) Command(ctx context.Context, c executor.Command) error {
	h.mu.Lock()
	h.jobs = append(h.jobs, c)
	hook := h.hook
	h.mu.Unlock()
	var err error
	if hook != nil {
		err = hook(ctx, c)
	} else {
		err = Serve(ctx, bytes.NewReader(c.Request), c.Output)
	}
	h.mu.Lock()
	h.done = append(h.done, ctx.Err())
	h.mu.Unlock()
	return err
}

func TestUnitSessionRunsEachCallInAFreshHelper(t *testing.T) {
	h := &helper{}
	s := NewSession(h, executor.Command{Worker: "w", Run: "r", StateVersion: 3, Image: "img"})
	got, err := s.Exec(t.Context(), self(t, "echo", "hello"))
	if err != nil || got.Status != 0 || got.Stdout != "hello" || got.Stderr != "err:hello" {
		t.Fatalf("echo = %+v, %v", got, err)
	}
	got, err = s.Exec(t.Context(), self(t, "exit", "4"))
	if err != nil || got.Status != 4 {
		t.Fatalf("exit 4 = %+v, %v", got, err)
	}
	if len(h.jobs) != 2 || h.jobs[1].Call != 2 || h.jobs[1].Worker != "w" || h.jobs[1].Run != "r" || h.jobs[1].StateVersion != 3 || h.jobs[1].Image != "img" {
		t.Fatalf("jobs = %+v", h.jobs)
	}
	req, err := ReadRequest(bytes.NewReader(h.jobs[0].Request))
	if err != nil || req.TimeoutMillis > CallTimeout.Milliseconds() || req.TimeoutMillis < CallTimeout.Milliseconds()-5000 {
		t.Fatalf("request = %+v, %v", req, err)
	}
	if _, err := s.Exec(t.Context(), nil); !errors.Is(err, ErrArguments) || err.Error() != ErrArguments.Error() {
		t.Fatalf("no arguments: %v", err)
	}
	for range MaxCalls - 3 {
		if _, err := s.Exec(t.Context(), self(t, "exit", "0")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Exec(t.Context(), self(t, "exit", "0")); !errors.Is(err, ErrCalls) {
		t.Fatalf("the ninth call: %v", err)
	}
	if len(h.jobs) != MaxCalls-1 {
		t.Fatalf("%d helpers for %d calls", len(h.jobs), MaxCalls)
	}
}

func TestUnitSessionFailsClosedAtItsBounds(t *testing.T) {
	s := NewSession(&helper{}, executor.Command{})
	if _, err := s.Exec(t.Context(), self(t, "write", "stdout", strconv.Itoa(MaxOutput+1))); !errors.Is(err, ErrOutput) {
		t.Fatalf("stdout beyond its bound: %v", err)
	}
	deadline, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if _, err := s.Exec(deadline, self(t, "sleep")); !errors.Is(err, ErrTimeout) {
		t.Fatalf("a sleeping command: %v", err)
	}

	// The combined budget counts every call's output, also a failed one's.
	h := &helper{hook: func(_ context.Context, c executor.Command) error {
		if err := WriteOutput(c.Output, false, make([]byte, MaxOutput)); err != nil {
			return err
		}
		return WriteResult(c.Output, Result{Call: c.Call, Status: StatusExited})
	}}
	s = NewSession(h, executor.Command{})
	for i := range MaxCombined / MaxOutput {
		if _, err := s.Exec(t.Context(), []string{"x"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if _, err := s.Exec(t.Context(), []string{"x"}); !errors.Is(err, ErrOutput) {
		t.Fatalf("beyond the combined budget: %v", err)
	}
	if err := h.done[len(h.done)-1]; err == nil {
		t.Fatal("the overflowing helper was not ended")
	}

	for name, c := range map[string]struct {
		hook func(context.Context, executor.Command) error
		want error
	}{
		"image": {func(context.Context, executor.Command) error {
			return fmt.Errorf("pull ghcr.io/x: %w", executor.ErrImage)
		}, ErrImage},
		"helper": {func(context.Context, executor.Command) error { return errors.New("create refused") }, ErrHelper},
		"no result": {func(_ context.Context, c executor.Command) error {
			return WriteOutput(c.Output, false, []byte("x"))
		}, ErrHelper},
		"helper timeout": {func(_ context.Context, c executor.Command) error {
			return WriteResult(c.Output, Result{Call: c.Call, Status: StatusTimeout})
		}, ErrTimeout},
		"helper output": {func(_ context.Context, c executor.Command) error {
			return WriteResult(c.Output, Result{Call: c.Call, Status: StatusOutput})
		}, ErrOutput},
		"stopped helper": {func(_ context.Context, c executor.Command) error {
			return WriteResult(c.Output, Result{Call: c.Call, Status: StatusStopped})
		}, ErrHelper},
		"refused": {func(_ context.Context, c executor.Command) error {
			return WriteResult(c.Output, Result{Call: c.Call, Status: StatusRefused, Reason: "invalid request"})
		}, ErrHelper},
	} {
		_, err := NewSession(&helper{hook: c.hook}, executor.Command{}).Exec(t.Context(), []string{"x"})
		if !errors.Is(err, c.want) || strings.Contains(err.Error(), "ghcr.io") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUnitSessionCancellationEndsTheHelper(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	h := &helper{hook: func(ctx context.Context, _ executor.Command) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	go func() {
		<-started
		cancel()
	}()
	_, err := NewSession(h, executor.Command{}).Exec(ctx, []string{"x"})
	if !errors.Is(err, ErrCancelled) || len(h.done) != 1 || h.done[0] == nil {
		t.Fatalf("a cancelled call: %v, helper ended %v", err, h.done)
	}
}

// TestUnitSessionFailsACallItsHelperStopped checks that a call whose
// helper is stopped while its command runs, as a Pod's active deadline
// stops it, fails the evaluation instead of answering the killed
// command's status to the policy.
func TestUnitSessionFailsACallItsHelperStopped(t *testing.T) {
	h := &helper{hook: func(ctx context.Context, c executor.Command) error {
		hctx, stop := context.WithCancel(ctx)
		defer stop()
		out := &stopper{stop: stop}
		err := Serve(hctx, bytes.NewReader(c.Request), out)
		_, _ = c.Output.Write(out.Bytes())
		return err
	}}
	got, err := NewSession(h, executor.Command{}).Exec(t.Context(), self(t, "ready"))
	if !errors.Is(err, ErrHelper) || got.Status != 0 || got.Stdout != "" {
		t.Fatalf("a call whose helper stopped = %+v, %v", got, err)
	}
}
