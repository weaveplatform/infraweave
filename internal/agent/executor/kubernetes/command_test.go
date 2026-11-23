package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
)

func helperCommand() executor.Command {
	return executor.Command{
		Worker: workerID, Run: runID, StateVersion: 4, Call: 2,
		Image: "ghcr.io/weaveplatform/infraweave-runner-terraform@sha256:" + strings.Repeat("b", 64), Request: []byte("private-argv"),
	}
}

func (k *kubelet) noPods(t *testing.T) {
	t.Helper()
	list, err := k.core.CoreV1().Pods(ns).List(t.Context(), metav1.ListOptions{})
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("Pods left: %d, %v", len(list.Items), err)
	}
}

// TestUnitCommandPodRunsAnIsolatedHelper checks an initialization
// command's helper Pod on a fake cluster: of the run's image, prepared
// by the bootstrap with nothing but the executable, mounting that alone,
// with a run Pod's security, a deadline beyond the call's remaining time
// and labels no run Pod carries; its standard input alone carries the
// request, and it is deleted once the call ends.
func TestUnitCommandPodRunsAnIsolatedHelper(t *testing.T) {
	k := newKubelet(t)
	e := k.executor(t, func(c *Config) {
		c.NodeSelector = map[string]string{"pool": "build"}
		c.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "pull"}}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := helperCommand()
	cmd.Output = &out
	if err := e.Command(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	if len(k.created) != 1 {
		t.Fatalf("%d Pods", len(k.created))
	}
	p := k.created[0]
	if out.String() != "answer:private-argv" || string(k.requests[p.Name]) != "private-argv" {
		t.Fatalf("output %q, request %q", out.String(), k.requests[p.Name])
	}
	if !strings.HasPrefix(p.Name, "iw-cmd-"+strings.ToLower(runID)+"-4-2-") || p.Labels[LabelCommandWorker] != workerID ||
		p.Labels[LabelCommandRun] != runID || p.Labels[LabelWorker] != "" || p.Labels[LabelRun] != "" ||
		p.OwnerReferences[0].UID != "worker-uid" {
		t.Errorf("metadata %+v", p.ObjectMeta)
	}
	s := p.Spec
	if *s.ActiveDeadlineSeconds < 35 || *s.ActiveDeadlineSeconds > 40 || *s.AutomountServiceAccountToken || *s.EnableServiceLinks ||
		*s.ShareProcessNamespace || *s.SecurityContext.RunAsUser != UID || s.NodeSelector["pool"] != "build" ||
		s.NodeSelector[corev1.LabelArchStable] != "" || s.ImagePullSecrets[0].Name != "pull" || s.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("spec %+v", s)
	}
	for _, v := range s.Volumes {
		if v.EmptyDir == nil {
			t.Errorf("volume %s is no emptyDir", v.Name)
		}
	}
	h := s.Containers[0]
	if h.Name != CommandContainer || h.Image != cmd.Image || !slices.Equal(h.Command, CommandCommand) || len(h.Args) != 0 ||
		len(h.Env) != 0 || len(h.EnvFrom) != 0 || len(h.VolumeMounts) != 1 || !h.VolumeMounts[0].ReadOnly ||
		h.VolumeMounts[0].MountPath != BinaryDir || *h.SecurityContext.AllowPrivilegeEscalation ||
		h.SecurityContext.Capabilities.Drop[0] != "ALL" || !h.Stdin || !h.StdinOnce {
		t.Errorf("helper container %+v", h)
	}
	b := s.InitContainers[0]
	if b.Image != k8sBootstrap(e) || !slices.Contains(b.Command, runID) || !slices.Contains(b.Command, workerID) {
		t.Errorf("bootstrap %+v", b)
	}
	raw, _ := p.Marshal()
	if bytes.Contains(raw, []byte("private-argv")) {
		t.Error("the request reached the Pod's specification")
	}
	k.noPods(t)

	if err := e.Command(t.Context(), executor.Command{Worker: workerID, Run: runID, StateVersion: 4, Call: 1, Image: cmd.Image}); err != nil {
		t.Fatalf("a helper without an output writer and deadline: %v", err)
	}
	if d := *k.created[1].Spec.ActiveDeadlineSeconds; d != int64((commandDeadline + commandDeadlineMargin).Seconds()) {
		t.Errorf("deadline without the call's = %d", d)
	}
}

// TestUnitCommandPodOutlastsItsCall checks that a helper Pod's active
// deadline is strictly beyond its call's remaining time, by the margin
// that lets the call's own deadline end it first, also for a call whose
// time is already spent.
func TestUnitCommandPodOutlastsItsCall(t *testing.T) {
	c := newKubelet(t).executor(t).config()
	for _, remaining := range []time.Duration{-time.Second, 0, time.Millisecond, 44*time.Second + 999*time.Millisecond, 45 * time.Second, commandDeadline} {
		d := time.Duration(*commandPod(c, helperCommand(), remaining).Spec.ActiveDeadlineSeconds) * time.Second
		if d < max(remaining, 0)+commandDeadlineMargin || d >= max(remaining, 0)+commandDeadlineMargin+time.Second {
			t.Errorf("a call of %s has a Pod deadline of %s", remaining, d)
		}
	}
}

func k8sBootstrap(e *Executor) string { return e.config().BootstrapImage }

func TestUnitCommandPodIsDeletedHoweverItEnds(t *testing.T) {
	k := newKubelet(t)
	k.runner = "ErrImagePull"
	err := k.executor(t).Command(t.Context(), helperCommand())
	if !errors.Is(err, executor.ErrImage) || !errors.Is(err, ErrPreparation) {
		t.Fatalf("an unpullable image: %v", err)
	}
	k.noPods(t)

	k = newKubelet(t)
	k.bootstrap = "ImagePullBackOff"
	if err := k.executor(t).Command(t.Context(), helperCommand()); errors.Is(err, executor.ErrImage) || !errors.Is(err, ErrPreparation) {
		t.Fatalf("an unpullable bootstrap: %v", err)
	}
	k.noPods(t)

	k = newKubelet(t)
	k.refuse = true
	if err := k.executor(t).Command(t.Context(), helperCommand()); err == nil || !strings.Contains(err.Error(), "initialization command") {
		t.Fatalf("a refused attach: %v", err)
	}
	k.noPods(t)

	k = newKubelet(t)
	k.hold, k.requested = true, make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-k.requested
		cancel()
	}()
	if err := k.executor(t).Command(ctx, helperCommand()); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled helper: %v", err)
	}
	k.noPods(t)

	k = newKubelet(t)
	k.failCreate = true
	if err := k.executor(t).Command(t.Context(), helperCommand()); !apierrors.IsForbidden(err) {
		t.Fatalf("a refused creation: %v", err)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := newKubelet(t).executor(t, func(c *Config) { c.Staging = blocked }).Command(t.Context(), helperCommand()); err == nil {
		t.Fatal("staged in a file")
	}
}

// TestUnitFindDeletesLeftHelperPods checks that a restarted controller's
// Find deletes its worker's helper Pods and answers none as its run.
func TestUnitFindDeletesLeftHelperPods(t *testing.T) {
	k := newKubelet(t)
	e := k.executor(t)
	for _, worker := range []string{workerID, "other"} {
		p := commandPod(e.config(), executor.Command{Worker: worker, Run: runID, StateVersion: 1, Call: 1, Image: "img"}, time.Minute)
		if _, err := k.core.CoreV1().Pods(ns).Create(t.Context(), p, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := e.Find(t.Context(), workerID, nil); ok || err != nil {
		t.Fatalf("Find = %v, %v", ok, err)
	}
	list, err := k.core.CoreV1().Pods(ns).List(t.Context(), metav1.ListOptions{})
	if err != nil || len(list.Items) != 1 || list.Items[0].Labels[LabelCommandWorker] != "other" {
		t.Fatalf("left %+v, %v", list.Items, err)
	}
}
