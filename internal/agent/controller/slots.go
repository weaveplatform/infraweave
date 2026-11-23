package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/executor/kubernetes"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
)

// podOS is the operating system of every run Pod.
const podOS = "linux"

// DefaultPreparing is a pool's preparing timeout when its resource sets
// none, the server's default.
const DefaultPreparing = 15 * time.Minute

// The messages of a slot's ExecutorAvailable condition.
const (
	executorRunning = "The slot's launcher polls for runs and executes each in a Pod of its runner image."
	noStreams       = "This controller has no Kubernetes API configuration to stream into run Pods; its workers claim no runs."
)

// ReasonExecutorRunning is ExecutorAvailable's reason while a slot's
// launcher runs.
const ReasonExecutorRunning = "LauncherRunning"

// slot is a Worker resource's launcher, which polls for runs and executes
// each through its Kubernetes executor, until its context is cancelled.
// The slot is its launcher's launcher.Store: it keeps the launcher's
// state, with its executor's last observation of the run's Pod, in the
// Worker's status.assignment, which the replica that next leads loads.
type slot struct {
	c          *Controller
	pool       types.UID
	worker     metav1.ObjectMeta
	key        string
	runTimeout time.Duration
	l          *launcher.Launcher
	exec       *kubernetes.Executor
	cancel     context.CancelFunc
	done       chan struct{}
	err        error

	// mu serializes the writes of the Worker's assignment, and guards
	// what they write and last wrote.
	mu      sync.Mutex
	st      launcher.State
	obs     kubernetes.Observation
	written *AssignmentStatus
	loaded  bool
}

var _ launcher.Store = (*slot)(nil)

// errReplaced is a slot whose Worker resource was replaced by another of
// its name, whose assignment it neither reads nor writes.
var errReplaced = errors.New("worker controller: the slot's Worker resource was replaced")

// Load reads the Worker's worker ID and the assignment the last launcher
// of its slot recorded, on whichever replica it ran.
func (s *slot) Load(ctx context.Context) (launcher.State, error) {
	u, err := s.c.kube.Dynamic.Resource(WorkerResource).Namespace(s.worker.Namespace).Get(ctx, s.worker.Name, metav1.GetOptions{})
	if err != nil {
		return launcher.State{}, fmt.Errorf("worker controller: read worker %s/%s: %w", s.worker.Namespace, s.worker.Name, err)
	}
	w, err := decode[Worker](u)
	if err != nil {
		return launcher.State{}, err
	}
	if w.UID != s.worker.UID {
		return launcher.State{}, errReplaced
	}
	st := launcher.State{Pool: w.Spec.PoolID, Worker: w.Status.WorkerID}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := w.Status.Assignment; a != nil {
		st.Run, st.Assigned, st.StateVersion = a.RunID, wire.AssignmentState(a.State), a.StateVersion
		st.PreparingDeadline, st.Started, st.Kill = timeOf(a.PreparationDeadline), timeOf(a.StartedAt), a.KillRequested
		if a.ExitReason != "" && a.ExitCode != nil {
			st.Exit = &wire.ContainerExit{Code: *a.ExitCode, Reason: wire.ContainerExitReason(a.ExitReason)}
		}
		if a.PodName != "" {
			s.obs = kubernetes.Observation{Run: a.RunID, Pod: a.PodName, UID: types.UID(a.PodUID), Phase: kubernetes.Phase(a.HandoffPhase)}
		}
		written := *a
		s.written = &written
	}
	s.st, s.loaded = st, true
	return st, nil
}

// Save records st in the Worker's status.
func (s *slot) Save(ctx context.Context, st launcher.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st = st
	return s.write(ctx)
}

// Forget removes the assignment from the Worker's status.
func (s *slot) Forget(ctx context.Context) error {
	return s.Save(ctx, launcher.State{Pool: s.st.Pool, Worker: s.st.Worker})
}

// observe records the executor's observation of the run's Pod in the
// Worker's status, within requestTimeout, logging a failed write: the
// launcher's next save writes it again.
func (s *slot) observe(o kubernetes.Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = o
	if !s.loaded {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if err := s.write(ctx); err != nil {
		s.c.log.WarnContext(ctx, "record the run Pod's phase", "namespace", s.worker.Namespace,
			"worker", s.worker.Name, "error", err.Error())
	}
}

// write patches the Worker's status with the assignment s holds, unless
// it is the one last written. Every field of the assignment is written,
// absent ones as null, so the patch replaces the whole object, and the
// patch carries the Worker's UID, so it never reaches a replacement of
// the same name.
func (s *slot) write(ctx context.Context) error {
	a := s.assignment()
	if equality.Semantic.DeepEqual(a, s.written) {
		return nil
	}
	status := map[string]any{"assignment": assignmentPatch(a)}
	if s.st.Worker != "" {
		status["workerID"] = s.st.Worker
	}
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{"uid": s.worker.UID}, "status": status})
	if err != nil {
		return fmt.Errorf("worker controller: assignment patch: %w", err)
	}
	_, err = s.c.kube.Dynamic.Resource(WorkerResource).Namespace(s.worker.Namespace).Patch(ctx, s.worker.Name,
		types.MergePatchType, patch, metav1.PatchOptions{FieldValidation: strict}, "status")
	if err != nil {
		return fmt.Errorf("worker controller: record the assignment of worker %s/%s: %w", s.worker.Namespace, s.worker.Name, err)
	}
	s.written = a
	return nil
}

// assignment is the Worker status of what s executes, nil when it is
// idle: the claimed run, its state and version, the preparing deadline
// its claim issued, when its runner started and the run timeout's end,
// whether a kill was asked of it, its Pod and preparation phase, and its
// exit until the server took it.
func (s *slot) assignment() *AssignmentStatus {
	st := s.st
	if st.Run == "" || st.StateVersion == 0 {
		return nil
	}
	a := &AssignmentStatus{
		RunID: st.Run, State: string(st.Assigned), StateVersion: st.StateVersion,
		PreparationDeadline: metaTime(st.PreparingDeadline), StartedAt: metaTime(st.Started),
		KillRequested: st.Kill,
	}
	if !st.Started.IsZero() {
		a.RunDeadline = metaTime(st.Started.Add(s.runTimeout))
	}
	if o := s.obs; o.Run == st.Run && o.Pod == kubernetes.PodName(st.Run, st.StateVersion) {
		a.PodName, a.PodUID, a.HandoffPhase = o.Pod, string(o.UID), string(o.Phase)
		a.HandoffAcknowledged = o.Phase == kubernetes.PhaseAcknowledged || o.Phase == kubernetes.PhaseFinished
	}
	if st.Exit != nil {
		code := st.Exit.Code
		a.ExitCode, a.ExitReason = &code, string(st.Exit.Reason)
	}
	return a
}

// assignmentFields are the JSON names of AssignmentStatus's fields,
// every one of which a merge patch names, so none survives from the
// assignment it replaces.
var assignmentFields = []string{
	"runID", "podUID", "podName", "state", "stateVersion", "preparationDeadline", "runDeadline",
	"logDeadline", "startedAt", "handoffPhase", "exitCode", "exitReason", "handoffAcknowledged",
	"killRequested",
}

// assignmentPatch is a as a merge patch's value: nil, which removes the
// assignment, or every field, absent ones as null.
func assignmentPatch(a *AssignmentStatus) any {
	if a == nil {
		return nil
	}
	b, err := json.Marshal(a)
	out := map[string]any{}
	if err == nil {
		err = json.Unmarshal(b, &out)
	}
	if err != nil {
		return nil
	}
	for _, f := range assignmentFields {
		if _, ok := out[f]; !ok {
			out[f] = nil
		}
	}
	return out
}

func metaTime(t time.Time) *metav1.Time {
	if t.IsZero() {
		return nil
	}
	v := metav1.NewTime(t.Truncate(time.Second))
	return &v
}

func timeOf(t *metav1.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Time
}

// alive reports whether the slot's launcher still runs.
func (s *slot) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// stop cancels the slot's launcher and waits for it to end.
func (s *slot) stop() {
	s.cancel()
	<-s.done
}

// slotKey identifies what a slot's launcher was started with: a change of
// the pool's credentials or endpoint starts it again once it is idle.
func slotKey(h hosted, endpoint string) string {
	sum := sha256.New()
	for _, part := range []string{h.token, string(h.key), endpoint, fmt.Sprint(h.public)} {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// preparing is wp's preparing timeout.
func preparing(wp WorkerPool) time.Duration {
	if t := wp.Spec.PreparingTimeoutSeconds; t != nil && *t > 0 {
		return time.Duration(*t) * time.Second
	}
	return DefaultPreparing
}

// launcherConfig is the launcher configuration of w's slot: the pool's
// token and key, a cache directory private to the slot, and the
// controller's trust, tool mirror and initialization policy.
func (c *Controller) launcherConfig(h hosted, w Worker) config.Launcher {
	lc := config.Defaults().Launcher
	lc.Token, lc.PrivateKey = h.token, observe.Secret(h.key)
	lc.CacheDir = c.slotDir(w.UID)
	lc.CAFile, lc.ToolMirror = c.settings.CAFile, c.settings.ToolMirror
	lc.InitializationPolicy, lc.InitializationPolicyEngine = c.settings.InitializationPolicy, rego.V1
	lc.InitializationTimeout = c.settings.InitializationTimeout
	lc.Metadata = map[string]string{"kubernetes.namespace": w.Namespace, "kubernetes.worker": w.Name}
	if c.pollTimeout > 0 {
		lc.PollTimeout = c.pollTimeout
	}
	return lc
}

func (c *Controller) slotDir(uid types.UID) string {
	return filepath.Join(c.settings.CacheDir, "slots", string(uid))
}

// executorConfig is how w's slot creates its run Pods, from wp's pod
// settings.
func (c *Controller) executorConfig(wp WorkerPool, w Worker, lc config.Launcher, s *slot) kubernetes.Config {
	pod := wp.Spec.Pod
	return kubernetes.Config{
		Namespace: w.Namespace,
		Owner: metav1.OwnerReference{
			APIVersion: Group + "/" + Version, Kind: "Worker", Name: w.Name, UID: w.UID,
		},
		BootstrapImage: c.settings.BootstrapImage,
		Resources: corev1.ResourceRequirements{
			Requests: pod.Resources.Requests, Limits: pod.Resources.Limits,
		},
		StorageSize: pod.StorageSize, NodeSelector: pod.NodeSelector, Tolerations: pod.Tolerations,
		ImagePullSecrets: pod.ImagePullSecrets,
		Staging:          filepath.Join(lc.CacheDir, "staging"),
		Arch:             c.arch,
		Execution:        lc.RunTimeout + c.settings.CleanupTimeout,
		Preparation:      preparing(wp),
		Interval:         c.interval,
		Observe:          s.observe,
	}
}

// start starts w's launcher under ctx, which ends it, as when the
// controller stops leading.
func (c *Controller) start(ctx context.Context, wp WorkerPool, h hosted, w Worker) (*slot, error) {
	lc := c.launcherConfig(h, w)
	s := &slot{
		c: c, pool: wp.UID, worker: w.ObjectMeta, key: slotKey(h, wp.Spec.Endpoint),
		runTimeout: lc.RunTimeout, done: make(chan struct{}),
	}
	s.exec = kubernetes.New(c.kube.Core, c.streams, c.executorConfig(wp, w, lc, s))
	slotNumber := w.Spec.Slot
	owner := h.owner
	// Run Pods are Linux whatever the controller runs on, so the worker
	// registers, and its tools are fetched, for Linux.
	l, err := launcher.NewContext(ctx, launcher.Deps{
		Config: lc, Doer: c.slotDoer, Executor: s.exec, Tools: c.tools, Sources: c.sources,
		Log: c.log.With("namespace", w.Namespace, "worker", w.Name),
		Now: c.now, Version: c.version, OS: podOS, Arch: c.arch,
		Endpoint: wp.Spec.Endpoint, Owner: &owner, Slot: &slotNumber, Retain: true, Store: s,
	})
	if err != nil {
		return nil, fmt.Errorf("worker controller: the slot's launcher: %w", err)
	}
	s.l = l
	lctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go func() {
		defer close(s.done)
		defer cancel()
		s.err = l.Run(lctx)
		if s.err != nil {
			c.log.WarnContext(ctx, "slot launcher ended", "namespace", w.Namespace, "worker", w.Name, "error", s.err.Error())
		}
	}()
	return s, nil
}

// errNoExecutor is a slot of a controller with no Kubernetes streams,
// which runs no launcher.
var errNoExecutor = errors.New("worker controller: no Kubernetes executor")

// running is w's live slot, replacing a slot whose launcher ended or
// whose credentials changed while it was idle. It fails with
// errNoExecutor when the controller has no executor, or why the slot
// cannot start.
func (c *Controller) running(ctx context.Context, wp WorkerPool, h hosted, w Worker) (*slot, error) {
	if c.streams == nil {
		return nil, errNoExecutor
	}
	c.starting.Lock()
	defer c.starting.Unlock()
	c.mu.Lock()
	s := c.slots[w.UID]
	c.mu.Unlock()
	if s != nil && s.alive() && s.key == slotKey(h, wp.Spec.Endpoint) {
		s.exec.Configure(c.executorConfig(wp, w, c.launcherConfig(h, w), s))
		return s, nil
	}
	if s != nil && s.alive() && s.l.Status().Run != "" {
		return s, nil
	}
	if s != nil {
		s.stop()
	}
	s, err := c.start(ctx, wp, h, w)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		delete(c.slots, w.UID)
		return nil, err
	}
	c.slots[w.UID] = s
	return s, nil
}

// errBusy blocks a slot's retirement while its run executes.
var errBusy = errors.New("worker controller: the slot's run executes")

// release ends w's launcher before its worker is deregistered: an idle
// launcher at once, a busy one once its run ends, which errBusy reports
// meanwhile; the launcher claims no run after it. A Worker whose status
// records an assignment but has no launcher on this replica, as after a
// change of leader, gets one, which resumes the run to its end before
// the slot retires. h is nil when the remote pool is gone, and so is the
// run's worker.
func (c *Controller) release(ctx context.Context, wp WorkerPool, h *hosted, w Worker) error {
	c.starting.Lock()
	c.mu.Lock()
	s := c.slots[w.UID]
	c.mu.Unlock()
	if s == nil {
		c.starting.Unlock()
		if w.Status.Assignment == nil || h == nil || c.streams == nil {
			return nil
		}
		// The launcher outlives this pass's context, which a pool's cleanup
		// bounds; losing the lead or the pool ends it.
		s, err := c.running(context.WithoutCancel(ctx), wp, *h, w)
		if err != nil {
			return err
		}
		s.l.Retire()
		return errBusy
	}
	defer c.starting.Unlock()
	if s.alive() && s.l.Status().Run != "" {
		s.l.Retire()
		return errBusy
	}
	s.stop()
	c.mu.Lock()
	delete(c.slots, w.UID)
	c.mu.Unlock()
	return nil
}

// stopSlots ends every slot's launcher but those keep holds, as when the
// controller stops leading; a busy slot leaves its Pod for the next
// leader to resume.
func (c *Controller) stopSlots(keep func(*slot) bool) {
	c.starting.Lock()
	defer c.starting.Unlock()
	c.mu.Lock()
	var stopping []*slot
	for uid, s := range c.slots {
		if keep != nil && keep(s) {
			continue
		}
		stopping = append(stopping, s)
		delete(c.slots, uid)
	}
	c.mu.Unlock()
	for _, s := range stopping {
		s.stop()
	}
}
