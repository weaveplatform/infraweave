package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/executor/kubernetes"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
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
type slot struct {
	pool   types.UID
	key    string
	l      *launcher.Launcher
	exec   *kubernetes.Executor
	cancel context.CancelFunc
	done   chan struct{}
	err    error

	mu  sync.Mutex
	obs kubernetes.Observation
}

func (s *slot) observe(o kubernetes.Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = o
}

func (s *slot) observed() kubernetes.Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.obs
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
	prep := preparing(wp)
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
		ActiveDeadline:   prep + lc.RunTimeout + c.settings.CleanupTimeout,
		Preparation:      prep,
		Interval:         c.interval,
		Observe:          s.observe,
	}
}

// start starts w's launcher under ctx, which ends it, as when the
// controller stops leading.
func (c *Controller) start(ctx context.Context, wp WorkerPool, h hosted, w Worker) (*slot, error) {
	lc := c.launcherConfig(h, w)
	s := &slot{pool: wp.UID, key: slotKey(h, wp.Spec.Endpoint), done: make(chan struct{})}
	s.exec = kubernetes.New(c.kube.Core, c.streams, c.executorConfig(wp, w, lc, s))
	slotNumber := w.Spec.Slot
	owner := h.owner
	// Run Pods are Linux whatever the controller runs on, so the worker
	// registers, and its tools are fetched, for Linux.
	l, err := launcher.NewContext(ctx, launcher.Deps{
		Config: lc, Doer: c.slotDoer, Executor: s.exec, Tools: c.tools, Sources: c.sources,
		Log: c.log.With("namespace", w.Namespace, "worker", w.Name),
		Now: c.now, Version: c.version, OS: podOS, Arch: c.arch,
		Endpoint: wp.Spec.Endpoint, Owner: &owner, Slot: &slotNumber, Retain: true,
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
// meanwhile; the launcher claims no run after it.
func (c *Controller) release(w Worker) error {
	c.starting.Lock()
	defer c.starting.Unlock()
	c.mu.Lock()
	s := c.slots[w.UID]
	c.mu.Unlock()
	if s == nil {
		return nil
	}
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

// assignment is the Worker status of what s executes, nil when it is
// idle: the claimed run, its Pod and preparation phase, its deadlines,
// and its exit until the server took it.
func (c *Controller) assignment(wp WorkerPool, s *slot) *AssignmentStatus {
	st := s.l.Status()
	if st.Run == "" || st.StateVersion == 0 {
		return nil
	}
	at := func(t time.Time) *metav1.Time {
		if t.IsZero() {
			return nil
		}
		v := metav1.NewTime(t.Truncate(time.Second))
		return &v
	}
	a := &AssignmentStatus{
		RunID: st.Run, State: string(st.State), StateVersion: st.StateVersion,
		PreparationDeadline: at(st.Assigned.Add(preparing(wp))), StartedAt: at(st.Started),
	}
	if !st.Started.IsZero() {
		a.RunDeadline = at(st.Started.Add(config.Defaults().Launcher.RunTimeout))
	}
	if o := s.observed(); o.Run == st.Run && o.Pod == kubernetes.PodName(st.Run, st.StateVersion) {
		a.PodName, a.PodUID, a.HandoffPhase = o.Pod, string(o.UID), string(o.Phase)
		a.HandoffAcknowledged = o.Phase == kubernetes.PhaseAcknowledged || o.Phase == kubernetes.PhaseFinished
	}
	if st.Exit != nil {
		code := st.Exit.Code
		a.ExitCode, a.ExitReason = &code, string(st.Exit.Reason)
	}
	return a
}
