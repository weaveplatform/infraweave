package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
	"github.com/weaveplatform/infraweave/internal/agent/executor/kubernetes"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// recorder keeps every run Pod the scenario's controllers create, as last
// seen, from the first controller's start to the scenario's end, so a
// step inspects a Pod its controller has since removed.
type recorder struct {
	mu       sync.Mutex
	seen     map[types.UID]*corev1.Pod
	cancel   context.CancelFunc
	watching sync.WaitGroup
}

// watchPods starts recording the run and initialization command helper
// Pods of every scenario namespace.
func (c *controllerSteps) watchPods(ctx context.Context) {
	if c.cancel != nil {
		return
	}
	c.seen = map[types.UID]*corev1.Pod{}
	wctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.cancel = cancel
	for _, ns := range c.order {
		for _, selector := range []string{kubernetes.LabelRun, kubernetes.LabelCommandRun} {
			c.watching.Go(func() { c.record(wctx, ns, selector) })
		}
	}
}

// record records the Pods of ns that selector selects until ctx ends,
// watching again from the last version it saw each time the API server
// ends a watch.
func (c *controllerSteps) record(ctx context.Context, ns, selector string) {
	version := ""
	for ctx.Err() == nil {
		w, err := c.core.Pods(ns).Watch(ctx, metav1.ListOptions{
			LabelSelector: selector, ResourceVersion: version, AllowWatchBookmarks: true,
		})
		if err != nil {
			version = ""
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		for ev := range w.ResultChan() {
			p, ok := ev.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			version = p.ResourceVersion
			if ev.Type == watch.Bookmark {
				continue
			}
			c.mu.Lock()
			c.seen[p.UID] = p.DeepCopy()
			c.mu.Unlock()
		}
		w.Stop()
	}
}

// stopWatch stops recording.
func (c *controllerSteps) stopWatch() {
	if c.cancel != nil {
		c.cancel()
		c.watching.Wait()
	}
}

// recorded is every run Pod recorded of run, or every Pod recorded, run
// and helper Pods alike, when run is empty.
func (c *controllerSteps) recorded(run string) []*corev1.Pod {
	return c.recordedBy(kubernetes.LabelRun, run)
}

// recordedBy is every Pod recorded whose label holds run, or every Pod
// recorded when run is empty.
func (c *controllerSteps) recordedBy(label, run string) []*corev1.Pod {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*corev1.Pod
	for _, p := range c.seen {
		if run == "" || p.Labels[label] == run {
			out = append(out, p)
		}
	}
	return out
}

// executes sets, for a scenario whose controllers execute runs, the
// bootstrap image the suite imports into the cluster's node and the tool
// mirror of the scenario's tool, and records the run Pods.
func (c *controllerSteps) executes(ctx context.Context, settings map[string]any) error {
	var tool *deps.Tool
	for _, t := range scenarioTools {
		if slices.Contains(c.w.Tags, t.tag) && t.image != "" {
			tool = &t.tool
		}
	}
	if tool == nil {
		return nil
	}
	host, err := c.w.Suite.Launchers(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names the build
	}
	mirror, err := host.Mirror(ctx, c.w.Suite.Deps, *tool)
	if err != nil {
		return err //nolint:wrapcheck // names the release
	}
	image, err := host.BootstrapImage(ctx, c.cluster)
	if err != nil {
		return err //nolint:wrapcheck // names the import
	}
	settings["tool_mirror"], settings["bootstrap_image"] = mirror, image
	c.watchPods(ctx)
	return nil
}

// signedThroughFront makes the scenario's servers sign object URLs at
// the HTTPS front, which run Pods reach, keeping their objects on the fs
// driver under the scenario's objects directory; the scenario's steps
// reach those URLs through the pod network. It comes before the servers
// start.
func (c *controllerSteps) signedThroughFront(ctx context.Context) error {
	if err := c.reserve(ctx); err != nil {
		return err
	}
	c.w.Settings["objectstore"] = map[string]any{
		"driver": "fs", "fs": map[string]any{"root": c.w.Objects(), "url": c.frontAt},
	}
	t, err := c.pods.Transport(c.ca)
	if err != nil {
		return err //nolint:wrapcheck // names the trust
	}
	c.w.Signed = &http.Client{Transport: t, Timeout: 30 * time.Second}
	return nil
}

// ran checks the run Pods recorded of the run kept as run: at least one,
// each in the namespace of the WorkerPool pool, owned by one of its
// Workers, its runner started from image, hardened as the executor
// builds every run Pod, and holding secret nowhere in its spec, metadata
// or status.
func (c *controllerSteps) ran(ctx context.Context, run, pool, image, secret string) error {
	wp, err := c.find(ctx, pool)
	if err != nil {
		return err
	}
	workers, err := c.slots(ctx, wp)
	if err != nil {
		return err
	}
	owners := map[types.UID]bool{}
	for _, w := range workers {
		owners[w.UID] = true
	}
	pods := c.recorded(c.w.IDs[run])
	if len(pods) == 0 {
		return fmt.Errorf("%w: no Pod of the run %s was recorded", harness.ErrStep, run)
	}
	for _, p := range pods {
		raw, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("encode Pod %s: %w", p.Name, err)
		}
		if bytes.Contains(raw, []byte(secret)) {
			return fmt.Errorf("%w: the Pod %s holds the secret", harness.ErrStep, p.Name)
		}
		refs := p.OwnerReferences
		if p.Namespace != wp.Namespace || len(refs) != 1 || refs[0].Kind != "Worker" || !owners[refs[0].UID] {
			return fmt.Errorf("%w: the Pod %s in %s is owned by %+v, not a Worker of %s", harness.ErrStep,
				p.Name, p.Namespace, refs, pool)
		}
		if why := hardened(p, c.w.Resolve(image), kubernetes.RunnerContainer); why != "" {
			return fmt.Errorf("%w: the Pod %s %s", harness.ErrStep, p.Name, why)
		}
	}
	return nil
}

// hardened is why p is not a run or helper Pod as the executor builds
// it, or "": its one container, named container, started from image, no
// service account token, host namespace or host path, a fixed non-root
// user, the runtime's seccomp profile, and containers that drop every
// capability, cannot escalate and take nothing from the environment or
// arguments but the run's identity.
func hardened(p *corev1.Pod, image, container string) string {
	s := p.Spec
	switch {
	case len(s.Containers) != 1 || s.Containers[0].Name != container:
		return "has containers other than the " + container
	case s.Containers[0].Image != image:
		return "runs " + s.Containers[0].Image + ", not " + image
	case len(s.InitContainers) != 1 || s.InitContainers[0].Name != kubernetes.BootstrapContainer:
		return "has init containers other than the bootstrap"
	case s.AutomountServiceAccountToken == nil || *s.AutomountServiceAccountToken:
		return "mounts a service account token"
	case s.HostNetwork || s.HostPID || s.HostIPC:
		return "shares a host namespace"
	case s.RestartPolicy != corev1.RestartPolicyNever:
		return "restarts"
	case s.SecurityContext == nil || s.SecurityContext.RunAsUser == nil || *s.SecurityContext.RunAsUser != kubernetes.UID ||
		s.SecurityContext.RunAsNonRoot == nil || !*s.SecurityContext.RunAsNonRoot ||
		s.SecurityContext.SeccompProfile == nil || s.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault:
		return "runs without the fixed user or the runtime's seccomp profile"
	}
	for _, v := range s.Volumes {
		if v.EmptyDir == nil {
			return "mounts the volume " + v.Name + ", which is not an emptyDir"
		}
	}
	for _, ct := range slices.Concat(s.InitContainers, s.Containers) {
		sc := ct.SecurityContext
		switch {
		case sc == nil || sc.Privileged == nil || *sc.Privileged:
			return ct.Name + " may be privileged"
		case sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation:
			return ct.Name + " may escalate"
		case sc.Capabilities == nil || !slices.Equal(sc.Capabilities.Drop, []corev1.Capability{"ALL"}) || len(sc.Capabilities.Add) > 0:
			return ct.Name + " keeps capabilities"
		case len(ct.Env) > 0 || len(ct.EnvFrom) > 0 || len(ct.Args) > 0:
			return ct.Name + " takes its environment or arguments"
		}
	}
	return ""
}

// clean checks that no run Pod recorded, Worker resource of the scenario
// or line a worker controller logged holds secret.
func (c *controllerSteps) clean(ctx context.Context, secret string) error {
	for _, p := range c.recorded("") {
		raw, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("encode Pod %s: %w", p.Name, err)
		}
		if bytes.Contains(raw, []byte(secret)) {
			return fmt.Errorf("%w: the Pod %s holds the secret", harness.ErrStep, p.Name)
		}
	}
	for _, ns := range c.order {
		list, err := c.api.Resource(controller.WorkerResource).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list workers: %w", err)
		}
		raw, err := json.Marshal(list)
		if err != nil {
			return fmt.Errorf("encode workers: %w", err)
		}
		if bytes.Contains(raw, []byte(secret)) {
			return fmt.Errorf("%w: a Worker of %s holds the secret", harness.ErrStep, ns)
		}
	}
	for i, srv := range c.running {
		if bytes.Contains(srv.Stdout(), []byte(secret)) || bytes.Contains(srv.Stderr(), []byte(secret)) {
			return fmt.Errorf("%w: worker controller %d logged the secret", harness.ErrStep, i+1)
		}
	}
	return nil
}

// noPod waits until no Pod of the run kept as run is left in the
// scenario's namespaces.
func (c *controllerSteps) noPod(ctx context.Context, run string) error {
	var left []string
	err := deps.Eventually(ctx, "the run "+run+" to have no Pod", func(ctx context.Context) (bool, error) {
		left = nil
		for _, ns := range c.order {
			list, err := c.core.Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: kubernetes.LabelRun + "=" + c.w.IDs[run]})
			if err != nil {
				return false, fmt.Errorf("list Pods: %w", err)
			}
			for _, p := range list.Items {
				left = append(left, p.Name)
			}
		}
		return len(left) == 0, nil
	})
	if err != nil {
		return fmt.Errorf("%w (left %s)", err, strings.Join(left, ", "))
	}
	return nil
}

// evidence writes each controller's output and configuration to
// worker-controller-<n>/ of the scenario's evidence directory, and the
// run Pods recorded to run-pods.json.
func (c *controllerSteps) evidence() error {
	dir := filepath.Join(c.w.Suite.Out, harness.Slug(c.w.Name))
	var err error
	for i, srv := range c.running {
		d := filepath.Join(dir, fmt.Sprintf("worker-controller-%d", i+1))
		if mkErr := os.MkdirAll(d, 0o750); mkErr != nil {
			err = errors.Join(err, mkErr)
			continue
		}
		err = errors.Join(err, srv.Evidence(d))
	}
	pods, jsonErr := json.MarshalIndent(c.recorded(""), "", "  ")
	err = errors.Join(err, jsonErr, os.MkdirAll(dir, 0o750))
	return errors.Join(err, os.WriteFile(filepath.Join(dir, "run-pods.json"), pods, 0o600))
}

// helpers checks the initialization command helper Pods recorded of the
// run kept as run: exactly n, one per call, each in the namespace of the
// WorkerPool pool, owned by one of its Workers, its helper started from
// image with the fixed entrypoint and no arguments, hardened as a run
// Pod, holding secret nowhere and never labelled as a run Pod.
func (c *controllerSteps) helpers(ctx context.Context, run string, n int, pool, image, secret string) error {
	wp, err := c.find(ctx, pool)
	if err != nil {
		return err
	}
	workers, err := c.slots(ctx, wp)
	if err != nil {
		return err
	}
	owners := map[types.UID]bool{}
	for _, w := range workers {
		owners[w.UID] = true
	}
	pods := c.recordedBy(kubernetes.LabelCommandRun, c.w.IDs[run])
	if len(pods) != n {
		return fmt.Errorf("%w: %d helper Pods of the run %s were recorded, not %d", harness.ErrStep, len(pods), run, n)
	}
	for _, p := range pods {
		raw, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("encode Pod %s: %w", p.Name, err)
		}
		refs := p.OwnerReferences
		switch {
		case bytes.Contains(raw, []byte(secret)):
			return fmt.Errorf("%w: the helper Pod %s holds %q", harness.ErrStep, p.Name, secret)
		case p.Namespace != wp.Namespace || len(refs) != 1 || refs[0].Kind != "Worker" || !owners[refs[0].UID]:
			return fmt.Errorf("%w: the helper Pod %s in %s is owned by %+v, not a Worker of %s", harness.ErrStep, p.Name, p.Namespace, refs, pool)
		case p.Labels[kubernetes.LabelRun] != "" || p.Labels[kubernetes.LabelWorker] != "":
			return fmt.Errorf("%w: the helper Pod %s is labelled as a run Pod", harness.ErrStep, p.Name)
		case !slices.Equal(p.Spec.Containers[0].Command, kubernetes.CommandCommand):
			return fmt.Errorf("%w: the helper Pod %s starts %q", harness.ErrStep, p.Name, p.Spec.Containers[0].Command)
		case len(p.Spec.Containers[0].VolumeMounts) != 1 || !p.Spec.Containers[0].VolumeMounts[0].ReadOnly:
			return fmt.Errorf("%w: the helper Pod %s mounts more than its read-only executable", harness.ErrStep, p.Name)
		}
		if why := hardened(p, c.w.Resolve(image), kubernetes.CommandContainer); why != "" {
			return fmt.Errorf("%w: the helper Pod %s %s", harness.ErrStep, p.Name, why)
		}
	}
	return nil
}

// helperRuns waits until a helper Pod of the run kept as run runs its
// command.
func (c *controllerSteps) helperRuns(ctx context.Context, run string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "an initialization command Pod of "+run+" to run", func(ctx context.Context) (bool, error) {
		for _, ns := range c.order {
			list, err := c.core.Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: kubernetes.LabelCommandRun + "=" + c.w.IDs[run]})
			if err != nil {
				return false, fmt.Errorf("list Pods: %w", err)
			}
			for _, p := range list.Items {
				for _, s := range p.Status.ContainerStatuses {
					if s.Name == kubernetes.CommandContainer && s.State.Running != nil {
						return true, nil
					}
				}
			}
		}
		return false, nil
	})
}

// noHelper waits until no helper Pod of the run kept as run is left in
// the scenario's namespaces.
func (c *controllerSteps) noHelper(ctx context.Context, run string) error {
	var left []string
	err := deps.Eventually(ctx, "the run "+run+" to have no initialization command Pod", func(ctx context.Context) (bool, error) {
		left = nil
		for _, ns := range c.order {
			list, err := c.core.Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: kubernetes.LabelCommandRun + "=" + c.w.IDs[run]})
			if err != nil {
				return false, fmt.Errorf("list Pods: %w", err)
			}
			for _, p := range list.Items {
				left = append(left, p.Name)
			}
		}
		return len(left) == 0, nil
	})
	if err != nil {
		return fmt.Errorf("%w (left %s)", err, strings.Join(left, ", "))
	}
	return nil
}
