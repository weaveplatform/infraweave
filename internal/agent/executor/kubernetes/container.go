package kubernetes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// SignalCommand is the fixed helper a stop runs beside the runner, which
// sends the runner, the container's PID 1, SIGINT.
var SignalCommand = []string{BinaryDir + "/infraweave", "runner", "signal", "interrupt"}

// The codes of a Pod's end the kubelet reports no code for.
const (
	// killedCode is SIGKILL's exit code, of a Pod Kill deleted.
	killedCode = 137
	// drainOutput bounds how long Wait waits for the runner's output once
	// the runner ended, so a stream the API server holds open cannot keep
	// a run from ending.
	drainOutput = 30 * time.Second
	// killGrace is the grace a killed Pod's containers get before the
	// kubelet sends SIGKILL.
	killGrace = int64(1)
)

// run is a run Pod.
type run struct {
	e    *Executor
	c    Config
	name string
	uid  types.UID
	run  string
	// gone marks a Pod deleted before it was handed off, which ends as
	// missing. killed holds once Kill deleted the Pod, and evicted once
	// the cluster marked it for eviction or preemption, which it may then
	// delete before its end is read.
	gone    bool
	killed  atomic.Bool
	evicted atomic.Bool

	mu     sync.Mutex
	copied chan struct{}
	stop   context.CancelFunc
}

var _ executor.Container = (*run)(nil)

func (r *run) Run() string { return r.run }

// Pod is the Pod's name and UID.
func (r *run) Pod() (string, types.UID) { return r.name, r.uid }

func (r *run) observe(p Phase) {
	if r.c.Observe != nil {
		r.c.Observe(Observation{Run: r.run, Pod: r.name, UID: r.uid, Phase: p})
	}
}

// Wait reads the Pod each interval until its runner ends, then waits for
// its output, and answers the runner's exit: killed after Kill,
// run-timeout when the Pod's active deadline ended it, evicted when the
// kubelet evicted the Pod or the cluster marked it as a disruption
// target, such as an eviction through the API, a preemption or a node's
// taint, out-of-memory when the kubelet ended the runner for memory, and
// missing when the Pod is gone or ended before its runner did. A watch
// beside the reads catches a disruption the Pod shows only between its
// eviction and its deletion, which can be shorter than an interval.
func (r *run) Wait(ctx context.Context) (executor.Exit, error) {
	if r.gone {
		return executor.Exit{Code: executor.MissingCode, Reason: wire.Missing}, nil
	}
	t := time.NewTicker(r.c.Interval)
	defer t.Stop()
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	wake := make(chan struct{}, 1)
	go r.follow(wctx, wake)
	for {
		exit, done, err := r.ended(ctx)
		if err != nil {
			return executor.Exit{}, err
		}
		if done {
			r.observe(PhaseFinished)
			r.drain(ctx)
			return exit, nil
		}
		select {
		case <-ctx.Done():
			return executor.Exit{}, fmt.Errorf("kubernetes: wait for run %s: %w", r.run, context.Cause(ctx))
		case <-t.C:
		case <-wake:
		}
	}
}

// follow watches the Pod until ctx ends, marking it evicted from the
// first update that shows a disruption and waking Wait on each update. A
// watch that cannot open, or that ends, opens again after an interval.
func (r *run) follow(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		w, err := r.e.core.Pods(r.c.Namespace).Watch(ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + r.name})
		if err == nil {
			for ev := range w.ResultChan() {
				if p, ok := ev.Object.(*corev1.Pod); ok && p.UID == r.uid && disrupted(p) {
					r.evicted.Store(true)
				}
				select {
				case wake <- struct{}{}:
				default:
				}
			}
			w.Stop()
		}
		select {
		case <-ctx.Done():
		case <-time.After(r.c.Interval):
		}
	}
}

// ended reads the Pod and answers its runner's exit once there is one.
func (r *run) ended(ctx context.Context) (executor.Exit, bool, error) {
	p, err := r.e.core.Pods(r.c.Namespace).Get(ctx, r.name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err) || err == nil && p.UID != r.uid:
		switch {
		case r.killed.Load():
			return executor.Exit{Code: killedCode, Reason: wire.Killed}, true, nil
		case r.evicted.Load():
			return executor.Exit{Code: executor.MissingCode, Reason: wire.Evicted}, true, nil
		}
		return executor.Exit{Code: executor.MissingCode, Reason: wire.Missing}, true, nil
	case err != nil && ctx.Err() != nil:
		return executor.Exit{}, false, fmt.Errorf("kubernetes: wait for run %s: %w", r.run, err)
	case err != nil:
		// A read the API server refused is retried at the next interval.
		return executor.Exit{}, false, nil
	}
	if disrupted(p) {
		r.evicted.Store(true)
	}
	reason := func(fallback wire.ContainerExitReason) wire.ContainerExitReason {
		switch {
		case r.killed.Load():
			return wire.Killed
		case p.Status.Reason == "DeadlineExceeded":
			return wire.RunTimeout
		case r.evicted.Load():
			return wire.Evicted
		}
		return fallback
	}
	if s := status(p, RunnerContainer); s != nil && s.State.Terminated != nil {
		t := s.State.Terminated
		why := wire.Exited
		if t.Reason == "OOMKilled" {
			why = wire.OutOfMemory
		}
		return executor.Exit{Code: int(t.ExitCode), Reason: reason(why)}, true, nil
	}
	if p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded || p.DeletionTimestamp != nil && r.killed.Load() {
		code := executor.MissingCode
		if r.killed.Load() {
			code = killedCode
		}
		return executor.Exit{Code: code, Reason: reason(wire.Missing)}, true, nil
	}
	return executor.Exit{}, false, nil
}

// disrupted reports whether the cluster evicted p, or marked it as a
// disruption target it is about to delete.
func disrupted(p *corev1.Pod) bool {
	if p.Status.Reason == "Evicted" {
		return true
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.DisruptionTarget && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// drain waits for the runner's output to be copied, at most drainOutput,
// then closes its stream.
func (r *run) drain(ctx context.Context) {
	r.mu.Lock()
	copied, stop := r.copied, r.stop
	r.mu.Unlock()
	t := time.NewTimer(drainOutput)
	defer t.Stop()
	select {
	case <-copied:
	case <-t.C:
	case <-ctx.Done():
	}
	if stop != nil {
		stop()
	}
}

// Stop runs SignalCommand in the runner's container. A runner that is no
// longer running is not an error.
func (r *run) Stop(ctx context.Context) error {
	var out bytes.Buffer
	err := r.e.streams.Exec(ctx, r.c.Namespace, r.name, RunnerContainer, SignalCommand, &out, &out)
	if err == nil {
		return nil
	}
	if _, done, rerr := r.ended(ctx); rerr == nil && done {
		return nil
	}
	return fmt.Errorf("kubernetes: stop run %s: %w: %s", r.run, err, truncate(out.String()))
}

// Kill deletes the Pod with a one-second grace, after which the kubelet
// ends its containers with SIGKILL; its exit is killed.
func (r *run) Kill(ctx context.Context) error {
	r.killed.Store(true)
	return r.delete(ctx, killGrace)
}

// HandBack has nothing to do: a Pod's workspace is its own volume, which
// goes with it.
func (r *run) HandBack(context.Context) error { return nil }

// Remove closes the runner's output stream and deletes the Pod. A Pod
// that is gone is not an error.
func (r *run) Remove(ctx context.Context) error {
	r.mu.Lock()
	stop := r.stop
	r.mu.Unlock()
	if stop != nil {
		stop()
	}
	return r.delete(ctx, -1)
}

// delete deletes the Pod, only while it is this Pod, with grace seconds,
// or the Pod's own when grace is negative.
func (r *run) delete(ctx context.Context, grace int64) error {
	uid := r.uid
	background := metav1.DeletePropagationBackground
	o := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}, PropagationPolicy: &background}
	if grace >= 0 {
		o.GracePeriodSeconds = &grace
	}
	err := r.e.core.Pods(r.c.Namespace).Delete(ctx, r.name, o)
	if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
		return fmt.Errorf("kubernetes: delete run %s's Pod: %w", r.run, err)
	}
	return nil
}

// digest is a SHA-256 that answers its sum in lowercase hex.
type digest struct{ hash.Hash }

func newDigest() digest      { return digest{sha256.New()} }
func (d digest) hex() string { return hex.EncodeToString(d.Sum(nil)) }

// tail keeps the last max bytes written to it, a failed bootstrap's
// diagnostic.
type tail struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// signalled closes done once r is read to its end: the stream holds the
// whole handoff.
type signalled struct {
	r    io.Reader
	done chan struct{}
	once sync.Once
}

func (s *signalled) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil {
		s.once.Do(func() { close(s.done) })
	}
	return n, err //nolint:wrapcheck // the reader's own end
}

// bytesReader reads a copy of b, so the caller may clear b once Start
// returns.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(bytes.Clone(b)) }

// locked serializes the runner's standard output and error into one
// writer.
type locked struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *locked) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w == nil {
		return len(p), nil
	}
	return l.w.Write(p) //nolint:wrapcheck // the output's own error
}
