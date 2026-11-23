package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/weaveplatform/infraweave/internal/agent/bootstrap"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
)

// Phase is how far a run Pod's preparation has come, which a Worker's
// status reports.
type Phase string

// The phases of a run Pod: created and preparing, its stream received,
// its handoff delivered, and its runner ended.
const (
	PhasePending      Phase = "Pending"
	PhaseStaged       Phase = "Staged"
	PhaseAcknowledged Phase = "Acknowledged"
	PhaseFinished     Phase = "Finished"
)

// Observation is a run Pod's identity and phase as the executor last
// saw them. It holds no secret.
type Observation struct {
	Run   string
	Pod   string
	UID   types.UID
	Phase Phase
}

// Config is how the executor creates a worker's run Pods: in Namespace,
// owned by Owner, the worker's resource; with the bootstrap image, the
// pool's resources, workspace storage, placement and pull secrets; with
// tools of Arch, whose nodes a Pod that streams tools selects. Staging
// is a directory private to the worker where a workspace is packed
// before it is streamed. The job's preparing deadline bounds scheduling,
// image pulls, the stream and the handoff together, or Preparation from
// Start for a job with none. Execution is the run timeout and cleanup
// time after that deadline, which with the preparation left makes the
// Pod's own active deadline, counted by Kubernetes from its start.
// Interval is how often a Pod's status is read, a second when zero.
// Observe, when set, is told each phase.
type Config struct {
	Namespace        string
	Owner            metav1.OwnerReference
	BootstrapImage   string
	Resources        corev1.ResourceRequirements
	StorageSize      resource.Quantity
	NodeSelector     map[string]string
	Tolerations      []corev1.Toleration
	ImagePullSecrets []corev1.LocalObjectReference
	Staging          string
	Arch             string
	Execution        time.Duration
	Preparation      time.Duration
	Interval         time.Duration
	Observe          func(Observation)
}

// Executor is the Kubernetes executor.Executor: each run is a Pod of the
// run's image, which a bootstrap init container prepares from a stream
// before its runner starts.
type Executor struct {
	core    corev1client.CoreV1Interface
	streams Streams

	mu sync.Mutex
	c  Config
}

var _ executor.Executor = (*Executor)(nil)

// Errors of a run Pod.
var (
	// ErrPreparation marks a run Pod whose preparation failed: it was not
	// scheduled or started in time, an image cannot be pulled, its
	// bootstrap refused the stream, or its runner did not take the
	// handoff. The Pod is deleted.
	ErrPreparation = errors.New("kubernetes: the run Pod's preparation failed")
	// ErrMount marks a job mount the executor cannot deliver: only
	// verified tool executables are streamed into a Pod.
	ErrMount = errors.New("kubernetes: a run Pod mounts no host path")
)

// New is an executor of Pods through core, streaming through streams.
func New(core corev1client.CoreV1Interface, streams Streams, c Config) *Executor {
	return &Executor{core: core, streams: streams, c: c}
}

// Configure replaces the configuration of the Pods started from now on,
// as when a pool's settings change.
func (e *Executor) Configure(c Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.c = c
}

func (e *Executor) config() Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.c
	if c.Interval <= 0 {
		c.Interval = time.Second
	}
	return c
}

// tools are j's verified tools, each of which must be one of its
// mounts; any other mount is a host path no Pod can see.
func tools(j executor.Job) ([]executor.Tool, error) {
	for _, m := range j.Mounts {
		if !slices.ContainsFunc(j.Tools, func(t executor.Tool) bool {
			return m.ReadOnly && m.Source == t.Path && m.Target == "/bin/"+t.Name
		}) {
			return nil, fmt.Errorf("%w: %s", ErrMount, m.Target)
		}
	}
	return j.Tools, nil
}

// Start packs j's workspace, creates its Pod, streams the workspace and
// tools to the Pod's bootstrap container, and once that has exited 0
// hands the runner j.Handoff on its standard input, whose output then
// goes to j.Output, all before j's preparing deadline. A Pod whose
// preparation fails is deleted, so no prepared Pod is left without its
// runner.
func (e *Executor) Start(ctx context.Context, j executor.Job) (executor.Container, error) {
	c := e.config()
	deadline := j.PreparingDeadline
	if deadline.IsZero() {
		deadline = time.Now().Add(c.Preparation)
	}
	ts, err := tools(j)
	if err != nil {
		return nil, err
	}
	if c.NodeSelector[corev1.LabelArchStable] != "" && len(ts) > 0 && c.NodeSelector[corev1.LabelArchStable] != c.Arch {
		return nil, fmt.Errorf("%w: the pool selects %s nodes for tools built for %s", ErrPreparation,
			c.NodeSelector[corev1.LabelArchStable], c.Arch)
	}
	staged, m, err := stage(c, j)
	if err != nil {
		return nil, err
	}
	defer os.Remove(staged)
	m.Tools = make([]bootstrap.Tool, 0, len(ts))
	for _, t := range ts {
		d, err := describe(t)
		if err != nil {
			return nil, err
		}
		m.Tools = append(m.Tools, d)
	}
	pctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if pctx.Err() != nil {
		return nil, fmt.Errorf("%w: run %s's preparing deadline passed before its Pod was created", ErrPreparation, j.Run)
	}
	var active time.Duration
	if c.Execution > 0 {
		active = time.Until(deadline) + c.Execution
	}
	p, err := e.core.Pods(c.Namespace).Create(ctx, pod(c, j, ts, active), metav1.CreateOptions{FieldValidation: metav1.FieldValidationStrict})
	if err != nil {
		return nil, fmt.Errorf("kubernetes: create run %s's Pod: %w", j.Run, err)
	}
	r := &run{e: e, c: c, name: p.Name, uid: p.UID, run: j.Run, copied: closed()}
	r.observe(PhasePending)
	if err := e.prepare(pctx, r, m, staged, ts); err != nil {
		return nil, e.abandon(ctx, r, err)
	}
	r.observe(PhaseStaged)
	if err := e.handOff(pctx, r, j); err != nil {
		return nil, e.abandon(ctx, r, err)
	}
	r.observe(PhaseAcknowledged)
	return r, nil
}

// stage packs j's workspace, an empty one when j has none, into a file
// of c.Staging and answers its path and the manifest naming it.
func stage(c Config, j executor.Job) (string, bootstrap.Manifest, error) {
	m := bootstrap.Manifest{Version: bootstrap.Version, Run: j.Run, StateVersion: j.StateVersion, Worker: j.Worker}
	if err := os.MkdirAll(c.Staging, 0o700); err != nil {
		return "", m, fmt.Errorf("kubernetes: the staging directory: %w", err)
	}
	dir := j.Workspace
	if dir == "" {
		empty, err := os.MkdirTemp(c.Staging, "empty-*")
		if err != nil {
			return "", m, fmt.Errorf("kubernetes: the staging directory: %w", err)
		}
		defer os.Remove(empty)
		dir = empty
	}
	f, err := os.CreateTemp(c.Staging, "workspace-*")
	if err != nil {
		return "", m, fmt.Errorf("kubernetes: the staging directory: %w", err)
	}
	m.Workspace, err = bootstrap.Pack(f, dir)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", m, fmt.Errorf("kubernetes: stage run %s's workspace: %w", j.Run, err)
	}
	return f.Name(), m, nil
}

// describe answers t's manifest entry from its executable's length and
// hash.
func describe(t executor.Tool) (bootstrap.Tool, error) {
	f, err := os.Open(t.Path)
	if err != nil {
		return bootstrap.Tool{}, fmt.Errorf("kubernetes: tool %s: %w", t.Name, err)
	}
	defer f.Close()
	d := newDigest()
	n, err := io.Copy(d, f)
	if err != nil {
		return bootstrap.Tool{}, fmt.Errorf("kubernetes: tool %s: %w", t.Name, err)
	}
	return bootstrap.Tool{Name: t.Name, Version: t.Version, Size: n, SHA256: d.hex()}, nil
}

// prepare waits for r's bootstrap container to run, streams it m and its
// payloads, and waits for it to exit 0.
func (e *Executor) prepare(ctx context.Context, r *run, m bootstrap.Manifest, staged string, ts []executor.Tool) error {
	if err := e.await(ctx, r, running(BootstrapContainer)); err != nil {
		return err
	}
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	for _, path := range append([]string{staged}, paths(ts)...) {
		f, err := os.Open(path) //nolint:gosec // the executor's own staged and cached files
		if err != nil {
			return fmt.Errorf("kubernetes: open run %s's payload: %w", r.run, err)
		}
		files = append(files, f)
	}
	readers := make([]io.Reader, 0, len(ts))
	for _, f := range files[1:] {
		readers = append(readers, f)
	}
	pr, pw := io.Pipe()
	sent := make(chan error, 1)
	go func() {
		err := bootstrap.Send(pw, m, files[0], readers)
		pw.CloseWithError(err)
		sent <- err
	}()
	diag := &tail{max: 4 << 10}
	err := e.streams.Attach(ctx, r.c.Namespace, r.name, BootstrapContainer, pr, diag, diag)
	_ = pr.CloseWithError(io.ErrClosedPipe)
	if serr := <-sent; serr != nil && !errors.Is(serr, io.ErrClosedPipe) {
		return fmt.Errorf("%w: stream run %s's preparation: %w", ErrPreparation, r.run, serr)
	}
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("%w: stream run %s's preparation: %w", ErrPreparation, r.run, context.Cause(ctx))
	}
	// The attach ends with the container, or failed before it ran; either
	// way its exit decides.
	return e.await(ctx, r, func(p *corev1.Pod) (bool, error) {
		s := status(p, BootstrapContainer)
		if s == nil || s.State.Terminated == nil {
			return false, nil
		}
		if t := s.State.Terminated; t.ExitCode != 0 {
			why := t.Message
			if why == "" {
				why = diag.String()
			}
			return false, fmt.Errorf("%w: run %s's bootstrap exited %d: %s", ErrPreparation, r.run, t.ExitCode, truncate(why))
		}
		return true, nil
	})
}

func paths(ts []executor.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Path)
	}
	return out
}

// handOff waits for r's runner to run, marks the Pod handed off, and
// writes j.Handoff to the runner's standard input, whose end closes it.
// The attach outlives Start: it carries the runner's output to j.Output
// until the runner ends.
func (e *Executor) handOff(ctx context.Context, r *run, j executor.Job) error {
	if err := e.await(ctx, r, running(RunnerContainer)); err != nil {
		return err
	}
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{
		"uid": r.uid, "annotations": map[string]string{AnnotationHandoff: handedOff},
	}})
	if err != nil {
		return fmt.Errorf("kubernetes: mark run %s's handoff: %w", r.run, err)
	}
	if _, err := e.core.Pods(r.c.Namespace).Patch(ctx, r.name, types.MergePatchType, patch, metav1.PatchOptions{FieldValidation: metav1.FieldValidationStrict}); err != nil {
		return fmt.Errorf("%w: mark run %s's handoff: %w", ErrPreparation, r.run, err)
	}
	actx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	in := &signalled{r: bytesReader(j.Handoff), done: make(chan struct{})}
	out := &locked{w: j.Output}
	attached := make(chan error, 1)
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		attached <- e.streams.Attach(actx, r.c.Namespace, r.name, RunnerContainer, in, out, out)
	}()
	select {
	case <-in.done:
	case err := <-attached:
		cancel()
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("%w: hand run %s over: %w", ErrPreparation, r.run, err)
	case <-ctx.Done():
		cancel()
		<-copied
		return fmt.Errorf("%w: hand run %s over: %w", ErrPreparation, r.run, context.Cause(ctx))
	}
	r.mu.Lock()
	r.copied, r.stop = copied, cancel
	r.mu.Unlock()
	return nil
}

// abandon deletes r's Pod, which failed its preparation, and answers err.
func (e *Executor) abandon(ctx context.Context, r *run, err error) error {
	return errors.Join(err, r.delete(context.WithoutCancel(ctx), 0))
}

// Find answers worker's newest run Pod, running or ended but not removed,
// copying what its runner writes from now on to out, once it has deleted
// any initialization command helper Pod of worker a stopped controller
// left. Only a Pod that carries the worker's label and is controlled by
// the executor's owner, the worker's resource, is the worker's: a Pod of
// the same label that another resource controls is neither answered nor
// deleted. The worker's older Pods, whose removal failed before, are
// deleted. A Pod that was never handed its handoff cannot be resumed,
// since only the executor that prepared it held the handoff: it is
// deleted and answered as a missing container.
func (e *Executor) Find(ctx context.Context, worker string, out io.Writer) (executor.Container, bool, error) {
	c := e.config()
	if err := e.sweep(ctx, c, worker); err != nil {
		return nil, false, err
	}
	list, err := e.core.Pods(c.Namespace).List(ctx, metav1.ListOptions{LabelSelector: LabelWorker + "=" + worker})
	if err != nil {
		return nil, false, fmt.Errorf("kubernetes: list worker %s's Pods: %w", worker, err)
	}
	owned := slices.DeleteFunc(list.Items, func(p corev1.Pod) bool { return !controlled(&p, c) })
	if len(owned) == 0 {
		return nil, false, nil
	}
	slices.SortFunc(owned, func(a, b corev1.Pod) int {
		return b.CreationTimestamp.Compare(a.CreationTimestamp.Time)
	})
	p := owned[0]
	for _, stale := range owned[1:] {
		old := &run{e: e, c: c, name: stale.Name, uid: stale.UID, run: stale.Labels[LabelRun]}
		if err := old.delete(ctx, killGrace); err != nil {
			return nil, false, err
		}
	}
	r := &run{e: e, c: c, name: p.Name, uid: p.UID, run: p.Labels[LabelRun], copied: closed()}
	if p.Annotations[AnnotationHandoff] != handedOff {
		r.gone = true
		if err := r.delete(ctx, 0); err != nil {
			return nil, false, err
		}
		return r, true, nil
	}
	r.observe(PhaseAcknowledged)
	if out == nil {
		out = io.Discard
	}
	tail := int64(0)
	lctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	logs, err := e.core.Pods(c.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{
		Container: RunnerContainer, Follow: true, TailLines: &tail,
	}).Stream(lctx)
	if err != nil {
		// A runner that has ended or never started has no more output; its
		// status answers how it ended.
		cancel()
		return r, true, nil //nolint:nilerr // Wait reads the Pod's end
	}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		defer logs.Close()
		_, _ = io.Copy(out, logs)
	}()
	r.copied, r.stop = copied, cancel
	return r, true, nil
}

// controlled reports whether c's owner, the worker's resource, controls
// p: only such a Pod is the worker's to adopt or delete.
func controlled(p *corev1.Pod, c Config) bool {
	ref := metav1.GetControllerOfNoCopy(p)
	return ref != nil && c.Owner.UID != "" && ref.UID == c.Owner.UID
}

func closed() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// await reads r's Pod each interval until cond holds, the Pod is gone or
// replaced, or ctx ends, which fails with what the Pod last showed.
func (e *Executor) await(ctx context.Context, r *run, cond func(*corev1.Pod) (bool, error)) error {
	t := time.NewTicker(r.c.Interval)
	defer t.Stop()
	var last string
	for {
		p, err := e.core.Pods(r.c.Namespace).Get(ctx, r.name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err) || err == nil && p.UID != r.uid:
			return fmt.Errorf("%w: run %s's Pod is gone", ErrPreparation, r.run)
		case err == nil:
			last = pending(p)
			done, err := cond(p)
			if err != nil || done {
				return err
			}
		case ctx.Err() == nil:
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: run %s's Pod did not become ready in time (%s): %w", ErrPreparation, r.run, last, context.Cause(ctx))
		case <-t.C:
		}
	}
}

// The waiting reasons of a container that cannot start.
var (
	pullFailures  = []string{"ErrImagePull", "ImagePullBackOff", "InvalidImageName", "ErrImageNeverPull", "RegistryUnavailable"}
	startFailures = []string{"CreateContainerConfigError", "CreateContainerError", "RunContainerError", "PreCreateHookError", "PreStartHookError"}
)

// running is the condition that container runs; its end, a Pod that
// failed, an image that cannot be pulled or a container that cannot be
// created fails it.
func running(container string) func(*corev1.Pod) (bool, error) {
	return func(p *corev1.Pod) (bool, error) {
		if p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded {
			return false, fmt.Errorf("%w: the Pod ended before its %s ran: %s %s", ErrPreparation, container, p.Status.Reason, truncate(p.Status.Message))
		}
		s := status(p, container)
		switch {
		case s == nil:
			return false, nil
		case s.State.Running != nil:
			return true, nil
		case s.State.Terminated != nil:
			t := s.State.Terminated
			return false, fmt.Errorf("%w: %s exited %d before its stream: %s", ErrPreparation, container, t.ExitCode, truncate(t.Message))
		case s.State.Waiting != nil && slices.Contains(pullFailures, s.State.Waiting.Reason):
			err := fmt.Errorf("%w: the %s image %s cannot be pulled: %s: %s", ErrPreparation, container, s.Image, s.State.Waiting.Reason, truncate(s.State.Waiting.Message))
			if container == BootstrapContainer {
				return false, err
			}
			return false, pullError{err}
		case s.State.Waiting != nil && slices.Contains(startFailures, s.State.Waiting.Reason):
			return false, fmt.Errorf("%w: %s cannot start: %s: %s", ErrPreparation, container, s.State.Waiting.Reason, truncate(s.State.Waiting.Message))
		}
		return false, nil
	}
}

// pullError is a run's image that cannot be pulled, which is
// executor.ErrImage while keeping the kubelet's own message.
type pullError struct{ err error }

func (p pullError) Error() string        { return p.err.Error() }
func (p pullError) Unwrap() error        { return p.err }
func (p pullError) Is(target error) bool { return target == executor.ErrImage } //nolint:errorlint // a sentinel

// status is container's status in p, nil before the kubelet reports it.
func status(p *corev1.Pod, container string) *corev1.ContainerStatus {
	for _, list := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses} {
		for i := range list {
			if list[i].Name == container {
				return &list[i]
			}
		}
	}
	return nil
}

// pending describes why p is not yet running: its unsatisfied
// scheduling condition, or its phase.
func pending(p *corev1.Pod) string {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status != corev1.ConditionTrue {
			return truncate(c.Reason + ": " + c.Message)
		}
	}
	return string(p.Status.Phase)
}

// maxDetail bounds a Pod's message an error carries, so a report's
// details keep their bound.
const maxDetail = 512

func truncate(s string) string {
	if len(s) <= maxDetail {
		return s
	}
	n := maxDetail
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
