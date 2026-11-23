package kubernetes

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/weaveplatform/infraweave/internal/agent/bootstrap"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

const (
	ns       = "workers"
	runID    = "01KRUN00000000000000000001"
	workerID = "01KWORKER0000000000000001"
)

var podsResource = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// kubelet is a fake cluster: the fake API server's Pods, whose statuses
// it moves as a kubelet would, and the attach and exec streams of their
// containers, where the bootstrap receives its real stream into a
// temporary Pod's volumes and the runner reads its handoff.
type kubelet struct {
	t    *testing.T
	core *k8sfake.Clientset
	arch string

	mu         sync.Mutex
	volumes    map[string]bootstrap.Paths
	handoffs   map[string][]byte
	exits      map[string]chan int32
	execs      [][]string
	created    []*corev1.Pod
	bootstrap  string // a waiting reason the bootstrap shows instead of running
	runner     string // a waiting reason the runner shows instead of running
	refuse     bool   // the runner's attach fails before reading
	requests   map[string][]byte
	hold       bool // a helper runs until its attach ends
	requested  chan struct{}
	execFails  bool
	oom        bool
	deadline   bool
	failCreate bool
}

func newKubelet(t *testing.T) *kubelet {
	t.Helper()
	k := &kubelet{
		t: t, core: k8sfake.NewClientset(), arch: "arm64", volumes: map[string]bootstrap.Paths{},
		handoffs: map[string][]byte{}, exits: map[string]chan int32{}, requests: map[string][]byte{},
	}
	k.core.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		k.mu.Lock()
		defer k.mu.Unlock()
		if k.failCreate {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("quota"))
		}
		p, _ := a.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		p.UID = types.UID("uid-" + p.Name)
		p.CreationTimestamp = metav1.Now()
		state := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
		if k.bootstrap != "" {
			state = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: k.bootstrap, Message: "refused"}}
		}
		p.Status = corev1.PodStatus{
			Phase:                 corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{{Name: BootstrapContainer, Image: p.Spec.InitContainers[0].Image, State: state}},
		}
		k.created = append(k.created, p.DeepCopy())
		root := k.t.TempDir()
		paths := bootstrap.Paths{Workspace: filepath.Join(root, "workspace"), Tools: filepath.Join(root, "tools"), Binary: filepath.Join(root, "bin", "infraweave")}
		for _, d := range []string{paths.Workspace, paths.Tools, filepath.Dir(paths.Binary)} {
			if err := os.MkdirAll(d, 0o750); err != nil {
				k.t.Error(err)
			}
		}
		k.volumes[p.Name] = paths
		k.exits[p.Name] = make(chan int32, 1)
		return false, nil, nil
	})
	return k
}

func (k *kubelet) pod(ctx context.Context, name string) *corev1.Pod {
	k.t.Helper()
	p, err := k.core.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		k.t.Fatal(err)
	}
	return p
}

func (k *kubelet) setStatus(ctx context.Context, name string, change func(*corev1.Pod)) {
	k.t.Helper()
	p, err := k.core.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return
	}
	change(p)
	if _, err := k.core.CoreV1().Pods(ns).UpdateStatus(ctx, p, metav1.UpdateOptions{}); err != nil {
		k.t.Error(err)
	}
}

func terminated(code int32, reason, message string) corev1.ContainerState {
	return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code, Reason: reason, Message: message}}
}

func (k *kubelet) Attach(ctx context.Context, namespace, pod, container string, stdin io.Reader, stdout, stderr io.Writer) error {
	k.mu.Lock()
	paths, exits, refuse, runnerWaiting := k.volumes[pod], k.exits[pod], k.refuse, k.runner
	k.mu.Unlock()
	if namespace != ns {
		k.t.Errorf("attach in %s", namespace)
	}
	if container == BootstrapContainer {
		p := k.pod(ctx, pod)
		args := p.Spec.InitContainers[0].Command
		sv, _ := strconv.ParseInt(args[6], 10, 64)
		self := filepath.Join(k.t.TempDir(), "self")
		if err := os.WriteFile(self, []byte("runner"), 0o600); err != nil {
			k.t.Error(err)
		}
		_, err := bootstrap.Receive(stdin, paths, bootstrap.Expect{Run: args[4], StateVersion: sv, Worker: args[8]}, k.arch, self)
		state, code := terminated(0, "Completed", ""), int32(0)
		if err != nil {
			_, _ = io.WriteString(stderr, err.Error())
			state, code = terminated(1, "Error", ""), 1
		}
		k.setStatus(ctx, pod, func(p *corev1.Pod) {
			p.Status.InitContainerStatuses[0].State = state
			if code != 0 {
				p.Status.Phase = corev1.PodFailed
				return
			}
			runner := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
			if runnerWaiting != "" {
				runner = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: runnerWaiting, Message: "manifest unknown"}}
			}
			p.Status.Phase = corev1.PodRunning
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: p.Spec.Containers[0].Name, Image: p.Spec.Containers[0].Image, State: runner}}
		})
		return nil
	}
	if refuse {
		return errors.New("upgrade refused")
	}
	if container == CommandContainer {
		return k.command(ctx, pod, stdin, stdout)
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.handoffs[pod] = b
	k.mu.Unlock()
	_, _ = io.WriteString(stdout, "phase output\n")
	_, _ = io.WriteString(stderr, "phase error\n")
	// A runner's attach also ends when its Pod is deleted or fails.
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			p, err := k.core.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
			if err != nil || p.Status.Phase == corev1.PodFailed {
				return nil
			}
			continue
		case code := <-exits:
			reason := "Completed"
			if k.oom {
				reason = "OOMKilled"
			}
			k.setStatus(ctx, pod, func(p *corev1.Pod) {
				p.Status.ContainerStatuses[0].State = terminated(code, reason, "")
				p.Status.Phase = corev1.PodSucceeded
				if k.deadline {
					p.Status.Phase, p.Status.Reason = corev1.PodFailed, "DeadlineExceeded"
				}
			})
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// command is a helper container: it reads its request and answers it on
// standard output, or runs until its attach ends when k.hold is set.
func (k *kubelet) command(ctx context.Context, pod string, stdin io.Reader, stdout io.Writer) error {
	b, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.requests[pod] = b
	hold, requested := k.hold, k.requested
	k.mu.Unlock()
	if requested != nil {
		close(requested)
	}
	if hold {
		<-ctx.Done()
		return ctx.Err()
	}
	_, _ = stdout.Write(append([]byte("answer:"), b...))
	k.setStatus(ctx, pod, func(p *corev1.Pod) {
		p.Status.ContainerStatuses[0].State = terminated(0, "Completed", "")
		p.Status.Phase = corev1.PodSucceeded
	})
	return nil
}

func (k *kubelet) Exec(_ context.Context, _, pod, container string, command []string, stdout, _ io.Writer) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.execs = append(k.execs, append([]string{pod, container}, command...))
	if k.execFails {
		_, _ = io.WriteString(stdout, "denied")
		return errors.New("forbidden")
	}
	select {
	case k.exits[pod] <- 130:
	default:
	}
	return nil
}

func (k *kubelet) exit(pod string, code int32) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.exits[pod] <- code
}

func (k *kubelet) executor(t *testing.T, change ...func(*Config)) *Executor {
	t.Helper()
	c := Config{
		Namespace: ns, Owner: metav1.OwnerReference{APIVersion: "workers.infraweave.io/v1alpha1", Kind: "Worker", Name: "slot-0", UID: "worker-uid"},
		BootstrapImage: "ghcr.io/weaveplatform/infraweave@sha256:" + strings.Repeat("a", 64),
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
		},
		StorageSize: resource.MustParse("1Gi"), Staging: t.TempDir(), Arch: "arm64",
		Execution: 4320 * time.Second, Preparation: 5 * time.Second, Interval: 5 * time.Millisecond,
	}
	for _, f := range change {
		f(&c)
	}
	return New(k.core.CoreV1(), k, c)
}

// elf is a minimal 64-bit little-endian ELF header of the AArch64 machine.
func elf() []byte {
	b := make([]byte, 64)
	copy(b, "\x7fELF")
	b[4], b[5], b[6] = 2, 1, 1
	binary.LittleEndian.PutUint16(b[16:], 2)
	binary.LittleEndian.PutUint16(b[18:], 183)
	binary.LittleEndian.PutUint32(b[20:], 1)
	return b
}

// job is a job of a prepared workspace and one verified tool.
func job(t *testing.T) executor.Job {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "source"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "source", "main.tf"), []byte("# main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "terraform")
	if err := os.WriteFile(bin, elf(), 0o600); err != nil {
		t.Fatal(err)
	}
	return executor.Job{
		Worker: workerID, Run: runID, StateVersion: 4, Image: "ghcr.io/weaveplatform/infraweave-runner-terraform@sha256:" + strings.Repeat("b", 64),
		Workspace: ws, Mounts: []executor.Mount{{Source: bin, Target: "/bin/terraform", ReadOnly: true}},
		Tools: []executor.Tool{{Name: "terraform", Version: "1.17.2", Path: bin}}, Handoff: []byte(`{"secret":"opened"}`),
	}
}

// output is a run's output as the launcher collects it.
type output struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func TestUnitRunPodReceivesItsStreamAndHandoffAndEnds(t *testing.T) {
	k := newKubelet(t)
	var seen []Phase
	var mu sync.Mutex
	e := k.executor(t, func(c *Config) {
		c.Observe = func(o Observation) {
			mu.Lock()
			defer mu.Unlock()
			if o.Run != runID || o.Pod != PodName(runID, 4) {
				t.Errorf("observation %+v", o)
			}
			seen = append(seen, o.Phase)
		}
	})
	j := job(t)
	out := &output{}
	j.Output = out
	c, err := e.Start(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	if c.Run() != runID {
		t.Fatalf("run %s", c.Run())
	}
	name := PodName(runID, 4)
	k.mu.Lock()
	paths, handoff := k.volumes[name], k.handoffs[name]
	k.mu.Unlock()
	main, err := os.ReadFile(filepath.Join(paths.Workspace, "source", "main.tf"))
	if err != nil || string(main) != "# main\n" {
		t.Fatalf("workspace %q %v", main, err)
	}
	if tool, err := os.ReadFile(filepath.Join(paths.Tools, "terraform")); err != nil || !bytes.Equal(tool, elf()) {
		t.Fatalf("tool %v", err)
	}
	if string(handoff) != `{"secret":"opened"}` {
		t.Fatalf("handoff %q", handoff)
	}
	if p := k.pod(t.Context(), name); p.Annotations[AnnotationHandoff] != handedOff {
		t.Fatalf("annotations %v", p.Annotations)
	}
	k.exit(name, 0)
	exit, err := c.Wait(t.Context())
	if err != nil || exit != (executor.Exit{Code: 0, Reason: wire.Exited}) {
		t.Fatalf("%+v %v", exit, err)
	}
	if got := out.String(); !strings.Contains(got, "phase output") || !strings.Contains(got, "phase error") {
		t.Fatalf("output %q", got)
	}
	if err := c.HandBack(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := k.core.CoreV1().Pods(ns).Get(t.Context(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("the Pod remains: %v", err)
	}
	if err := c.Remove(t.Context()); err != nil {
		t.Fatalf("a second removal: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, []Phase{PhasePending, PhaseStaged, PhaseAcknowledged, PhaseFinished}) {
		t.Fatalf("phases %v", seen)
	}
}

func TestUnitRunPodSpecHoldsNoSecretAndNoPrivilege(t *testing.T) {
	k := newKubelet(t)
	e := k.executor(t, func(c *Config) {
		c.NodeSelector = map[string]string{"pool": "runs"}
		c.Tolerations = []corev1.Toleration{{Key: "runs", Operator: corev1.TolerationOpExists}}
		c.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "registry"}}
	})
	j := job(t)
	// The issued deadline leaves 900 seconds of preparation, which the
	// active deadline counts with the run timeout and cleanup after it.
	j.PreparingDeadline = time.Now().Add(900 * time.Second)
	c, err := e.Start(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	k.exit(PodName(runID, 4), 0)
	if _, err := c.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	p := k.created[0]
	spec := p.Spec
	if *spec.AutomountServiceAccountToken || *spec.EnableServiceLinks || *spec.ShareProcessNamespace || spec.HostNetwork || spec.HostPID || spec.HostIPC {
		t.Fatal("the Pod shares the node's or API's identity")
	}
	if d := *spec.ActiveDeadlineSeconds; spec.RestartPolicy != corev1.RestartPolicyNever || d > 5220 || d < 5215 {
		t.Fatalf("restart %s deadline %d", spec.RestartPolicy, d)
	}
	if *spec.SecurityContext.RunAsUser != UID || *spec.SecurityContext.FSGroup != UID || !*spec.SecurityContext.RunAsNonRoot {
		t.Fatalf("security %+v", spec.SecurityContext)
	}
	if spec.NodeSelector[corev1.LabelArchStable] != "arm64" || spec.NodeSelector["pool"] != "runs" || len(spec.Tolerations) != 1 || spec.ImagePullSecrets[0].Name != "registry" {
		t.Fatalf("placement %v %v", spec.NodeSelector, spec.ImagePullSecrets)
	}
	for _, v := range spec.Volumes {
		if v.EmptyDir == nil {
			t.Fatalf("volume %s is not Pod-local", v.Name)
		}
	}
	for _, ctr := range append(slices.Clone(spec.InitContainers), spec.Containers...) {
		sc := ctr.SecurityContext
		if *sc.Privileged || *sc.AllowPrivilegeEscalation || sc.Capabilities.Drop[0] != "ALL" || len(ctr.Env) > 0 || len(ctr.EnvFrom) > 0 {
			t.Fatalf("container %s: %+v", ctr.Name, ctr)
		}
		if strings.Contains(strings.Join(ctr.Command, " "), "opened") || strings.Contains(strings.Join(ctr.Args, " "), "opened") {
			t.Fatalf("container %s carries the handoff", ctr.Name)
		}
		if !ctr.Stdin || !ctr.StdinOnce || ctr.TTY {
			t.Fatalf("container %s stdin", ctr.Name)
		}
	}
	runner := spec.Containers[0]
	if runner.Image != j.Image || runner.Command[0] != BinaryDir+"/infraweave" || runner.WorkingDir != Workspace {
		t.Fatalf("runner %+v", runner)
	}
	tool := runner.VolumeMounts[2]
	if tool.MountPath != "/bin/terraform" || tool.SubPath != "terraform" || !tool.ReadOnly {
		t.Fatalf("tool mount %+v", tool)
	}
	if p.OwnerReferences[0].UID != "worker-uid" || !*p.OwnerReferences[0].Controller {
		t.Fatalf("owner %+v", p.OwnerReferences)
	}
	if p.Labels[LabelWorker] != workerID || p.Labels[LabelRun] != runID {
		t.Fatalf("labels %v", p.Labels)
	}
}

func TestUnitRunPodWithoutToolsOrWorkspaceKeepsTheImagesOwn(t *testing.T) {
	k := newKubelet(t)
	e := k.executor(t, func(c *Config) { c.Execution, c.StorageSize = 0, resource.Quantity{} })
	j := job(t)
	j.Mounts, j.Tools, j.Workspace = nil, nil, ""
	c, err := e.Start(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	p := k.created[0]
	if p.Spec.NodeSelector != nil || p.Spec.ActiveDeadlineSeconds != nil || len(p.Spec.Containers[0].VolumeMounts) != 2 {
		t.Fatalf("spec %+v", p.Spec)
	}
	k.exit(p.Name, 3)
	if exit, err := c.Wait(t.Context()); err != nil || exit.Code != 3 || exit.Reason != wire.Exited {
		t.Fatalf("%+v %v", exit, err)
	}
}

func TestUnitRunPodRefusesHostMountsAndConflictingArchitectures(t *testing.T) {
	k := newKubelet(t)
	j := job(t)
	j.Mounts = append(j.Mounts, executor.Mount{Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"})
	if _, err := k.executor(t).Start(t.Context(), j); !errors.Is(err, ErrMount) {
		t.Fatal(err)
	}
	e := k.executor(t, func(c *Config) { c.NodeSelector = map[string]string{corev1.LabelArchStable: "amd64"} })
	if _, err := e.Start(t.Context(), job(t)); !errors.Is(err, ErrPreparation) {
		t.Fatal(err)
	}
	j = job(t)
	j.Tools[0].Path = filepath.Join(t.TempDir(), "absent")
	j.Mounts[0].Source = j.Tools[0].Path
	if _, err := k.executor(t).Start(t.Context(), j); err == nil {
		t.Fatal("started without its tool")
	}
	if len(k.created) != 0 {
		t.Fatalf("created %d Pods", len(k.created))
	}
}

// gone checks that the preparation left no Pod.
func (k *kubelet) gone(t *testing.T) {
	t.Helper()
	list, err := k.core.CoreV1().Pods(ns).List(t.Context(), metav1.ListOptions{})
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("%d Pods remain: %v", len(list.Items), err)
	}
}

func TestUnitRunPodPreparationFailuresDeleteThePod(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*kubelet, *executor.Job)
		want  string
	}{
		"bootstrap pull":   {func(k *kubelet, _ *executor.Job) { k.bootstrap = "ImagePullBackOff" }, "cannot be pulled"},
		"bootstrap create": {func(k *kubelet, _ *executor.Job) { k.bootstrap = "CreateContainerConfigError" }, "cannot start"},
		"runner pull":      {func(k *kubelet, _ *executor.Job) { k.runner = "ErrImagePull" }, "runner image"},
		"runner attach":    {func(k *kubelet, _ *executor.Job) { k.refuse = true }, "upgrade refused"},
		"pending":          {func(k *kubelet, _ *executor.Job) { k.bootstrap = "ContainerCreating" }, "did not become ready"},
		"issued deadline": {func(k *kubelet, j *executor.Job) {
			k.bootstrap, j.PreparingDeadline = "ContainerCreating", time.Now().Add(100*time.Millisecond)
		}, "did not become ready"},
		"passed deadline": {func(_ *kubelet, j *executor.Job) { j.PreparingDeadline = time.Now().Add(-time.Second) }, "before its Pod was created"},
		"stream refused":  {func(k *kubelet, _ *executor.Job) { k.arch = "amd64" }, "bootstrap exited 1"},
	} {
		t.Run(name, func(t *testing.T) {
			k := newKubelet(t)
			j := job(t)
			tc.setup(k, &j)
			// Only a pending Pod waits out its bound; the others fail first,
			// however slowly the race detector runs them.
			bound := 10 * time.Second
			if name == "pending" {
				bound = 200 * time.Millisecond
			}
			e := k.executor(t, func(c *Config) { c.Preparation = bound })
			_, err := e.Start(t.Context(), j)
			if !errors.Is(err, ErrPreparation) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v", err)
			}
			k.gone(t)
		})
	}
}

func TestUnitRunPodCreationFailureIsReported(t *testing.T) {
	k := newKubelet(t)
	k.failCreate = true
	if _, err := k.executor(t).Start(t.Context(), job(t)); !apierrors.IsForbidden(err) {
		t.Fatal(err)
	}
	if _, err := k.executor(t, func(c *Config) { c.Staging = "/dev/null/staging" }).Start(t.Context(), job(t)); err == nil {
		t.Fatal("staged under a file")
	}
}

func TestUnitRunPodsEndAsTheirRunnersDid(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*kubelet)
		act   func(*testing.T, *kubelet, executor.Container)
		want  executor.Exit
	}{
		"out of memory": {func(k *kubelet) { k.oom = true }, func(_ *testing.T, k *kubelet, _ executor.Container) { k.exit(PodName(runID, 4), 137) }, executor.Exit{Code: 137, Reason: wire.OutOfMemory}},
		"deadline":      {func(k *kubelet) { k.deadline = true }, func(_ *testing.T, k *kubelet, _ executor.Container) { k.exit(PodName(runID, 4), 137) }, executor.Exit{Code: 137, Reason: wire.RunTimeout}},
		"stopped": {func(*kubelet) {}, func(t *testing.T, k *kubelet, c executor.Container) {
			if err := c.Stop(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(k.execs[0][2:], SignalCommand) || k.execs[0][1] != RunnerContainer {
				t.Fatalf("exec %v", k.execs)
			}
		}, executor.Exit{Code: 130, Reason: wire.Exited}},
		"killed": {func(*kubelet) {}, func(t *testing.T, _ *kubelet, c executor.Container) {
			if err := c.Kill(t.Context()); err != nil {
				t.Fatal(err)
			}
		}, executor.Exit{Code: killedCode, Reason: wire.Killed}},
		"deleted": {func(*kubelet) {}, func(t *testing.T, k *kubelet, _ executor.Container) {
			if err := k.core.CoreV1().Pods(ns).Delete(t.Context(), PodName(runID, 4), metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
		}, executor.Exit{Code: executor.MissingCode, Reason: wire.Missing}},
		"evicted": {func(*kubelet) {}, func(_ *testing.T, k *kubelet, _ executor.Container) {
			k.setStatus(t.Context(), PodName(runID, 4), func(p *corev1.Pod) { p.Status.Phase, p.Status.Reason = corev1.PodFailed, "Evicted" })
		}, executor.Exit{Code: executor.MissingCode, Reason: wire.Evicted}},
		"evicted with its runner's code": {func(*kubelet) {}, func(_ *testing.T, k *kubelet, _ executor.Container) {
			k.setStatus(t.Context(), PodName(runID, 4), func(p *corev1.Pod) {
				p.Status.Phase, p.Status.Reason = corev1.PodFailed, "Evicted"
				p.Status.ContainerStatuses[0].State = terminated(143, "Error", "")
			})
		}, executor.Exit{Code: 143, Reason: wire.Evicted}},
		"disrupted then deleted": {func(*kubelet) {}, func(t *testing.T, k *kubelet, c executor.Container) {
			k.setStatus(t.Context(), PodName(runID, 4), func(p *corev1.Pod) {
				p.Status.Conditions = append(p.Status.Conditions, corev1.PodCondition{
					Type: corev1.DisruptionTarget, Status: corev1.ConditionTrue, Reason: "EvictionByEvictionAPI",
				})
			})
			// The executor reads the Pod marked before it goes.
			if _, done, err := c.(*run).ended(t.Context()); done || err != nil {
				t.Fatalf("a disrupted Pod ended: %v %v", done, err)
			}
			if err := k.core.CoreV1().Pods(ns).Delete(t.Context(), PodName(runID, 4), metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
		}, executor.Exit{Code: executor.MissingCode, Reason: wire.Evicted}},
	} {
		t.Run(name, func(t *testing.T) {
			k := newKubelet(t)
			tc.setup(k)
			c, err := k.executor(t).Start(t.Context(), job(t))
			if err != nil {
				t.Fatal(err)
			}
			tc.act(t, k, c)
			exit, err := c.Wait(t.Context())
			if err != nil || exit != tc.want {
				t.Fatalf("%+v %v, want %+v", exit, err, tc.want)
			}
		})
	}
}

func TestUnitStopFailsWhenTheRunnerStillRuns(t *testing.T) {
	k := newKubelet(t)
	c, err := k.executor(t).Start(t.Context(), job(t))
	if err != nil {
		t.Fatal(err)
	}
	k.execFails = true
	if err := c.Stop(t.Context()); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatal(err)
	}
	k.exit(PodName(runID, 4), 0)
	if _, err := c.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatalf("stopping an ended runner: %v", err)
	}
}

func TestUnitWaitEndsWithItsContext(t *testing.T) {
	k := newKubelet(t)
	c, err := k.executor(t).Start(t.Context(), job(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	k.exit(PodName(runID, 4), 0)
}

func TestUnitFindResumesAHandedOffPodAndDiscardsAnUnpreparedOne(t *testing.T) {
	k := newKubelet(t)
	var seen []Observation
	e := k.executor(t, func(c *Config) { c.Observe = func(o Observation) { seen = append(seen, o) } })
	if _, ok, err := e.Find(t.Context(), workerID, nil); ok || err != nil {
		t.Fatalf("found %v %v", ok, err)
	}
	yes := true
	started := func(name string, at time.Time, handed bool, owner types.UID) {
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, UID: types.UID("uid-" + name), CreationTimestamp: metav1.NewTime(at),
			Labels: map[string]string{LabelWorker: workerID, LabelRun: runID},
		}, Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			Name: RunnerContainer, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}}}
		if owner != "" {
			p.OwnerReferences = []metav1.OwnerReference{{Kind: "Worker", Name: "slot", UID: owner, Controller: &yes}}
		}
		if handed {
			p.Annotations = map[string]string{AnnotationHandoff: handedOff}
		}
		if err := k.core.Tracker().Add(p); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	started("old", now.Add(-time.Hour), true, "worker-uid")
	started("new", now, true, "worker-uid")
	// Pods of the worker's label that the worker's resource does not
	// control are another's: newer, they are neither answered nor deleted.
	started("foreign", now.Add(time.Minute), true, "another-uid")
	started("orphan", now.Add(time.Minute), true, "")
	out := &output{}
	c, ok, err := e.Find(t.Context(), workerID, out)
	if !ok || err != nil || c.Run() != runID {
		t.Fatalf("%v %v", ok, err)
	}
	if name, uid := c.(*run).Pod(); name != "new" || uid != "uid-new" {
		t.Fatalf("found %s", name)
	}
	if len(seen) != 1 || seen[0].Pod != "new" || seen[0].Phase != PhaseAcknowledged {
		t.Fatalf("observed %+v", seen)
	}
	if _, err := k.core.CoreV1().Pods(ns).Get(t.Context(), "old", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("the stale Pod remains: %v", err)
	}
	for _, name := range []string{"foreign", "orphan"} {
		if _, err := k.core.CoreV1().Pods(ns).Get(t.Context(), name, metav1.GetOptions{}); err != nil {
			t.Fatalf("%s's Pod was deleted: %v", name, err)
		}
		if err := k.core.Tracker().Delete(podsResource, ns, name); err != nil {
			t.Fatal(err)
		}
	}
	k.setStatus(t.Context(), "new", func(p *corev1.Pod) { p.Status.ContainerStatuses[0].State = terminated(2, "Error", "") })
	if exit, err := c.Wait(t.Context()); err != nil || exit.Code != 2 {
		t.Fatalf("%+v %v", exit, err)
	}
	if !strings.Contains(out.String(), "fake logs") {
		t.Fatalf("output %q", out.String())
	}
	if err := c.Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	started("unprepared", now, false, "worker-uid")
	c, ok, err = e.Find(t.Context(), workerID, nil)
	if !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if exit, err := c.Wait(t.Context()); err != nil || exit.Reason != wire.Missing {
		t.Fatalf("%+v %v", exit, err)
	}
	k.gone(t)

	started("stuck", now, true, "worker-uid")
	started("stuck-older", now.Add(-time.Minute), true, "worker-uid")
	k.core.PrependReactor("delete", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("unavailable")
	})
	if _, _, err := e.Find(t.Context(), workerID, nil); err == nil {
		t.Fatal("a stale Pod that cannot be deleted was ignored")
	}
}

func TestUnitFindReportsAnUnreadableNamespace(t *testing.T) {
	k := newKubelet(t)
	k.core.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("unavailable")
	})
	if _, _, err := k.executor(t).Find(t.Context(), workerID, nil); err == nil {
		t.Fatal("found through a failing API")
	}
	k.core.PrependReactor("delete", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("unavailable")
	})
	r := &run{e: k.executor(t), c: k.executor(t).config(), name: "p", uid: "u", run: runID}
	if err := r.Kill(t.Context()); err == nil {
		t.Fatal("killed through a failing API")
	}
}

func TestUnitConfigureAppliesToLaterPods(t *testing.T) {
	k := newKubelet(t)
	e := k.executor(t)
	c := e.config()
	c.BootstrapImage = "registry.example/infraweave@sha256:" + strings.Repeat("c", 64)
	c.Interval = 0
	e.Configure(c)
	if got := e.config(); got.BootstrapImage != c.BootstrapImage || got.Interval != time.Second {
		t.Fatalf("%+v", got)
	}
}

func TestUnitHelpersBoundWhatTheyKeep(t *testing.T) {
	tl := &tail{max: 4}
	_, _ = tl.Write([]byte("abcdef"))
	if tl.String() != "cdef" {
		t.Fatal(tl.String())
	}
	long := strings.Repeat("é", maxDetail)
	if got := truncate(long); len(got) > maxDetail || !strings.HasPrefix(long, got) {
		t.Fatalf("%d", len(got))
	}
	l := &locked{}
	if n, err := l.Write([]byte("x")); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	p := &corev1.Pod{
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "0/1 nodes"}}},
	}
	if got := pending(p); got != "Unschedulable: 0/1 nodes" {
		t.Fatal(got)
	}
	if _, err := running(BootstrapContainer)(&corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}}); !errors.Is(err, ErrPreparation) {
		t.Fatal(err)
	}
	if _, err := running(
		BootstrapContainer,
	)(
		&corev1.Pod{Status: corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{{Name: BootstrapContainer, State: terminated(1, "Error", "boom")}}}},
	); !errors.Is(
		err,
		ErrPreparation,
	) {
		t.Fatal(err)
	}
}

// TestUnitWaitSeesAnEvictionBetweenItsReads checks that a Pod marked as
// a disruption target and deleted between two reads still ends evicted,
// from the watch beside the reads.
func TestUnitWaitSeesAnEvictionBetweenItsReads(t *testing.T) {
	k := newKubelet(t)
	watching := make(chan struct{})
	var once sync.Once
	// The watch is open, its events delivered, before the test moves on.
	k.core.PrependWatchReactor("pods", func(k8stesting.Action) (bool, watch.Interface, error) {
		w, err := k.core.Tracker().Watch(podsResource, ns)
		once.Do(func() { close(watching) })
		return true, w, err
	})
	c, err := k.executor(t).Start(t.Context(), job(t))
	if err != nil {
		t.Fatal(err)
	}
	r := c.(*run)
	r.c.Interval = time.Hour
	done := make(chan executor.Exit, 1)
	go func() {
		exit, err := c.Wait(t.Context())
		if err != nil {
			t.Error(err)
		}
		done <- exit
	}()
	<-watching
	k.setStatus(t.Context(), PodName(runID, 4), func(p *corev1.Pod) {
		p.Status.Conditions = append(p.Status.Conditions, corev1.PodCondition{Type: corev1.DisruptionTarget, Status: corev1.ConditionTrue})
	})
	if err := k.core.CoreV1().Pods(ns).Delete(t.Context(), PodName(runID, 4), metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case exit := <-done:
		if exit != (executor.Exit{Code: executor.MissingCode, Reason: wire.Evicted}) {
			t.Fatalf("exit %+v", exit)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not see the eviction")
	}
}
