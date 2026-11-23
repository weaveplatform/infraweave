package controller

import (
	"context"
	"crypto/ecdh"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"

	"github.com/weaveplatform/infraweave/internal/agent/executor/kubernetes"
	"github.com/weaveplatform/infraweave/internal/agent/tools"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// kubelet moves run Pods' statuses as a kubelet would and serves their
// streams: a bootstrap takes its whole stream and exits 0, and a runner
// reads its handoff and runs until the test ends it.
type kubelet struct {
	e  *env
	mu sync.Mutex
	// handoffs and ends are each Pod's handoff and the exit code that ends
	// its runner.
	handoffs map[string][]byte
	ends     map[string]chan int32
	execs    int
}

func newKubelet(e *env) *kubelet {
	k := &kubelet{e: e, handoffs: map[string][]byte{}, ends: map[string]chan int32{}}
	e.core.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		p, _ := a.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		p.UID = types.UID(kernel.NewULID().String())
		p.CreationTimestamp = metav1.Now()
		p.Status = corev1.PodStatus{Phase: corev1.PodPending, InitContainerStatuses: []corev1.ContainerStatus{{
			Name: kubernetes.BootstrapContainer, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}}
		return false, nil, nil
	})
	return k
}

func (k *kubelet) end(pod string) chan int32 {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.ends[pod] == nil {
		k.ends[pod] = make(chan int32, 1)
	}
	return k.ends[pod]
}

func (k *kubelet) set(ctx context.Context, namespace, name string, change func(*corev1.Pod)) {
	p, err := k.e.core.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return
	}
	change(p)
	_, _ = k.e.core.CoreV1().Pods(namespace).UpdateStatus(ctx, p, metav1.UpdateOptions{})
}

func (k *kubelet) Attach(ctx context.Context, namespace, pod, container string, stdin io.Reader, _, _ io.Writer) error {
	if container == kubernetes.BootstrapContainer {
		if _, err := io.Copy(io.Discard, stdin); err != nil {
			return err
		}
		k.set(ctx, namespace, pod, func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodRunning
			p.Status.InitContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}}
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{
				Name: kubernetes.RunnerContainer, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}}
		})
		return nil
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.handoffs[pod] = b
	k.mu.Unlock()
	select {
	case code := <-k.end(pod):
		k.set(context.WithoutCancel(ctx), namespace, pod, func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodSucceeded
			p.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code}}
		})
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (k *kubelet) Exec(context.Context, string, string, string, []string, io.Writer, io.Writer) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.execs++
	return nil
}

// emptySources writes an empty source checkout into each workspace.
type emptySources struct{}

func (emptySources) Fetch(_ context.Context, dir string, _ wire.RunSpec, _ *wire.RunSpecSourceCredential) error {
	return os.MkdirAll(filepath.Join(dir, "source"), 0o750)
}

func (emptySources) Restore(context.Context, string, string, *ecdh.PrivateKey, string, wire.RunSpec) error {
	return errors.New("no saved workspace")
}

type noTools struct{}

func (noTools) Binary(context.Context, tools.Release) (string, error) {
	return "", errors.New("no tools")
}

// executing is a controller whose slots run launchers and execute their
// runs through k, under ctx.
func (e *env) executing(k *kubelet) *Controller {
	e.t.Helper()
	c, err := New(Deps{
		Settings: e.settings(), Clients: Clients{Dynamic: e.dyn, Core: e.core.CoreV1(), Coordination: e.core.CoordinationV1()},
		HTTP: e.in.srv.Client(), Now: time.Now, Version: "test", Arch: "arm64", Identity: "replica",
		Streams: k, Tools: noTools{}, PollTimeout: time.Second,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	c.settings.CacheDir, c.settings.BootstrapImage = e.t.TempDir(), "ghcr.io/weaveplatform/infraweave@sha256:"+strings.Repeat("a", 64)
	c.sources, c.interval = emptySources{}, 5*time.Millisecond
	return c
}

// until passes c, under the test's context, until cond holds of the
// pool name, for at most ten seconds.
func (e *env) until(c *Controller, name string, cond func(WorkerPool, []Worker) bool) {
	e.t.Helper()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c.Pass(e.t.Context())
		var ws []Worker
		if !e.gone(name) {
			ws = e.workers(name)
			if cond(e.pool(name), ws) {
				return
			}
		} else if cond(WorkerPool{}, nil) {
			return
		}
		<-tick.C
	}
	e.t.Fatalf("pool %s did not converge", name)
}

func TestUnitSlotsExecuteRunsInPods(t *testing.T) {
	e := newEnv(t)
	k := newKubelet(e)
	c := e.executing(k)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		c.stopSlots(nil)
	})
	_, remote := e.hosted("build", Existing, 1)
	e.change("build", func(wp *WorkerPool) {
		wp.Spec.Pod.Resources = Resources{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
			Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
		}
		wp.Spec.Pod.StorageSize = resource.MustParse("2Gi")
	})
	e.until(c, "build", func(wp WorkerPool, ws []Worker) bool {
		return len(ws) == 1 && ws[0].Status.WorkerID != "" && wp.Status.Ready == 1 && len(e.in.pollRequests()) > 0
	})
	wp := e.pool("build")
	expect(t, wp.Status.Conditions, ConditionExecutor, metav1.ConditionTrue, ReasonExecutorRunning)
	w := e.workers("build")[0]
	expect(t, w.Status.Conditions, ConditionExecutor, metav1.ConditionTrue, ReasonExecutorRunning)
	regs := e.in.registrations()
	last := regs[len(regs)-1]
	if last.Slot == nil || *last.Slot != 0 || last.Owner != wp.Owner(cluster) {
		t.Fatalf("the slot's launcher registered %+v", last)
	}

	run := kernel.NewULID().String()
	deadline := time.Now().Add(900 * time.Second).Truncate(time.Second)
	e.in.assign(remote.id, wire.Assignment{
		Run: run, RunToken: "run-token", State: wire.AssignmentStatePREPARING, StateVersion: 2, PreparingDeadline: deadline.Unix(),
	},
		wire.RunSpec{}, wire.RunSpecSecrets{ApiToken: "opened-secret"})
	name := kubernetes.PodName(run, 2)
	e.until(c, "build", func(wp WorkerPool, ws []Worker) bool {
		a := ws[0].Status.Assignment
		return wp.Status.Busy == 1 && a != nil && a.HandoffAcknowledged
	})
	a := e.workers("build")[0].Status.Assignment
	if a.RunID != run || a.StateVersion != 2 || a.State != string(wire.AssignmentStatePREPARING) || a.PodName != name ||
		a.HandoffPhase != string(
			kubernetes.PhaseAcknowledged,
		) || a.StartedAt == nil || a.RunDeadline == nil || a.PreparationDeadline == nil || !a.PreparationDeadline.Time.Equal(deadline) {
		t.Fatalf("assignment %+v", a)
	}
	p, err := e.core.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.OwnerReferences[0].UID != w.UID || p.Spec.Containers[0].Resources.Requests.Cpu().String() != "100m" ||
		p.Spec.Volumes[2].EmptyDir.SizeLimit.String() != "2Gi" || *p.Spec.ActiveDeadlineSeconds > 5220 || *p.Spec.ActiveDeadlineSeconds < 5200 {
		t.Fatalf("pod %+v", p.Spec)
	}
	k.mu.Lock()
	handoff := string(k.handoffs[name])
	k.mu.Unlock()
	if !strings.Contains(handoff, "opened-secret") {
		t.Fatal("the runner was not handed its secrets")
	}

	e.change("build", func(wp *WorkerPool) { wp.Spec.Capacity = 0 })
	c.Pass(ctx)
	retiring := e.workers("build")
	if len(retiring) != 1 {
		t.Fatalf("a busy slot was retired: %d", len(retiring))
	}
	expect(t, retiring[0].Status.Conditions, ConditionTerminating, metav1.ConditionFalse, ReasonCleanupBlocked)

	k.end(name) <- 0
	e.until(c, "build", func(_ WorkerPool, ws []Worker) bool { return len(ws) == 0 })
	if _, err := e.core.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
		t.Fatal("the ended run's Pod remains")
	}
	var reported bool
	for _, r := range e.in.pollRequests() {
		if r.Exit != nil && r.Exit.Code == 0 && r.Run != nil && *r.Run == run {
			reported = true
		}
	}
	if !reported || len(e.in.live()[0].slots) != 0 {
		t.Fatalf("the exit was not reported, or the worker remains: %v", e.in.live()[0].slots)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.slots) != 0 {
		t.Fatalf("slots remain: %d", len(c.slots))
	}
}

func TestUnitSlotsEndWithTheirPoolOrLeadership(t *testing.T) {
	e := newEnv(t)
	c := e.executing(newKubelet(e))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e.hosted("build", Existing, 2)
	e.until(c, "build", func(wp WorkerPool, _ []Worker) bool { return wp.Status.Ready == 2 })
	c.mu.Lock()
	n := len(c.slots)
	c.mu.Unlock()
	if n != 2 {
		t.Fatalf("slots %d", n)
	}
	// A pool removed without its finalizer, as by force, ends its slots.
	wp := e.pool("build")
	if err := e.dyn.Resource(PoolResource).Namespace(namespace).Delete(ctx, wp.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.setFinalizers(ctx, PoolResource, e.pool("build").ObjectMeta, nil); err != nil {
		t.Fatal(err)
	}
	c.Pass(ctx)
	c.mu.Lock()
	n = len(c.slots)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("slots of a removed pool: %d", n)
	}

	e.hosted("deploy", Existing, 1)
	lctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.lead(lctx)
	}()
	e.until(c, "deploy", func(wp WorkerPool, _ []Worker) bool { return wp.Status.Ready == 1 })
	stop()
	<-done
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.slots) != 0 {
		t.Fatalf("slots after leading: %d", len(c.slots))
	}
}

func TestUnitControllerWithoutStreamsRunsNoExecutor(t *testing.T) {
	e := newEnv(t)
	e.hosted("build", Existing, 1)
	c := e.controller()
	e.pass(c)
	expect(t, e.pool("build").Status.Conditions, ConditionExecutor, metav1.ConditionFalse, ReasonExecutorUnavailable)
	if w := e.workers("build")[0]; w.Status.Assignment != nil || w.Status.WorkerID == "" {
		t.Fatalf("worker %+v", w.Status)
	}
	if len(e.in.pollRequests()) != 0 {
		t.Fatal("a controller without an executor polled")
	}
}
