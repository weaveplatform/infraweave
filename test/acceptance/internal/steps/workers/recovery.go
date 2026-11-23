package workers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cucumber/godog"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
	"github.com/weaveplatform/infraweave/internal/agent/executor/kubernetes"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// absentLabel is a node label no node of the owned cluster has, so a Pod
// that selects it stays pending.
const absentLabel = "acceptance.infraweave.io/absent"

// registerRecovery registers the steps that disrupt a controller's runs:
// killing the leading controller, deleting or evicting a run's Pod,
// cutting the HTTPS front, and draining a slot's worker.
func (c *controllerSteps) registerRecovery(sc *godog.ScenarioContext) {
	sc.Step(
		`^the Managed WorkerPool "(\w+)" of capacity (\d+) in the space "(\w+)", whose Pods no node accepts(?:, preparing for (\d+) seconds)?, is applied in "(\w+)"$`,
		c.unschedulable,
	)
	sc.Step(`^the leading worker controller is killed$`, c.kill)
	sc.Step(`^the run "(\w+)" eventually has a (pending|handed-off) Pod$`, c.hasPod)
	sc.Step(`^the run "(\w+)"'s Pod is (deleted|evicted)$`, c.disrupt)
	sc.Step(`^the run "(\w+)" ran in (\d+) Pods?$`, c.ranIn)
	sc.Step(`^the run "(\w+)" gets no Pod for (\d+) seconds$`, c.noPodFor)
	sc.Step(`^a Pod of the WorkerPool "(\w+)"'s worker that no Worker owns is created as "(\w+)"$`, c.foreign)
	sc.Step(`^the Pod "(\w+)" remains$`, c.remains)
	sc.Step(`^the worker of the WorkerPool "(\w+)" is kept as "(\w+)"$`, c.keepWorker)
	sc.Step(`^the Worker of the WorkerPool "(\w+)" eventually reports (\w+) (True|False) "(\w+)"$`, c.workerReports)
	sc.Step(`^the HTTPS front answers 502 for (\d+) seconds$`, c.cutFront)
}

// unschedulable applies a Managed WorkerPool whose Pods select a node
// label no node has, with a preparing timeout of seconds when it is set.
func (c *controllerSteps) unschedulable(ctx context.Context, name string, capacity int64, space, seconds, in string) error {
	return c.managedOn(ctx, name, capacity, space, in, "", func(spec, pod map[string]any) {
		pod["nodeSelector"] = map[string]any{absentLabel: "true"}
		if n, err := strconv.ParseInt(seconds, 10, 32); err == nil {
			spec["preparingTimeoutSeconds"] = n
		}
	})
}

// kill sends the leading controller SIGKILL, so it neither releases its
// Lease nor ends its slots; a standby leads once the Lease expires.
func (c *controllerSteps) kill(ctx context.Context) error {
	leader, err := c.leader(ctx)
	if err != nil {
		return err
	}
	if err := leader.Signal(syscall.SIGKILL); err != nil {
		return err //nolint:wrapcheck // names the process
	}
	if _, err := leader.ExitCode(ctx); err != nil {
		return err //nolint:wrapcheck // names what it waited for
	}
	c.running = deleteServer(c.running, leader)
	c.killed = append(c.killed, leader)
	return nil
}

func deleteServer(list []*harness.Server, s *harness.Server) []*harness.Server {
	out := make([]*harness.Server, 0, len(list))
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// runPods lists the Pods of the run kept as run in the scenario's
// namespaces.
func (c *controllerSteps) runPods(ctx context.Context, run string) ([]corev1.Pod, error) {
	var out []corev1.Pod
	for _, ns := range c.order {
		list, err := c.core.Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: kubernetes.LabelRun + "=" + c.w.IDs[run]})
		if err != nil {
			return nil, fmt.Errorf("list Pods: %w", err)
		}
		out = append(out, list.Items...)
	}
	return out, nil
}

// hasPod waits until the run kept as run has a Pod: a pending one, or
// one whose runner runs after its handoff.
func (c *controllerSteps) hasPod(ctx context.Context, run, kind string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "the run "+run+" to have a "+kind+" Pod", func(ctx context.Context) (bool, error) {
		pods, err := c.runPods(ctx, run)
		if err != nil {
			return false, err
		}
		for _, p := range pods {
			if kind == "pending" && p.Status.Phase == corev1.PodPending {
				return true, nil
			}
			if p.Annotations[kubernetes.AnnotationHandoff] == "" {
				continue
			}
			for _, s := range p.Status.ContainerStatuses {
				if s.Name == kubernetes.RunnerContainer && s.State.Running != nil {
					return true, nil
				}
			}
		}
		return false, nil
	})
}

// disrupt deletes the run's Pod at once, which kills its processes, or
// evicts it through the Eviction API, as a node drain does.
func (c *controllerSteps) disrupt(ctx context.Context, run, how string) error {
	pods, err := c.runPods(ctx, run)
	if err != nil {
		return err
	}
	if len(pods) != 1 {
		return fmt.Errorf("%w: the run %s has %d Pods", harness.ErrStep, run, len(pods))
	}
	p := pods[0]
	if how == "deleted" {
		err = c.core.Pods(p.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{GracePeriodSeconds: new(int64)})
	} else {
		err = c.core.Pods(p.Namespace).EvictV1(ctx, &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace}})
	}
	if err != nil {
		return fmt.Errorf("%s the Pod %s: %w", how, p.Name, err)
	}
	return nil
}

// ranIn checks that the run Pods recorded of the run kept as run are n.
func (c *controllerSteps) ranIn(run string, n int) error {
	if got := len(c.recorded(c.w.IDs[run])); got != n {
		return fmt.Errorf("%w: the run %s ran in %d Pods, not %d", harness.ErrStep, run, got, n)
	}
	return nil
}

// noPodFor checks, for seconds, that no Pod of the run kept as run is
// created.
func (c *controllerSteps) noPodFor(ctx context.Context, run string, seconds int) error {
	end := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(end) {
		pods, err := c.runPods(ctx, run)
		if err != nil {
			return err
		}
		if len(pods) > 0 {
			return fmt.Errorf("%w: the run %s got the Pod %s", harness.ErrStep, run, pods[0].Name)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("watch the run %s's Pods: %w", run, ctx.Err())
		case <-time.After(time.Second):
		}
	}
	return nil
}

// foreign creates, in the namespace of the WorkerPool pool, a pending Pod
// labelled with its slot's worker and a run of its own, which no Worker
// controls, and keeps its name as key.
func (c *controllerSteps) foreign(ctx context.Context, pool, key string) error {
	wp, err := c.find(ctx, pool)
	if err != nil {
		return err
	}
	workers, err := c.slots(ctx, wp)
	if err != nil || len(workers) == 0 {
		return fmt.Errorf("%w: no Worker of %s: %w", harness.ErrStep, pool, err)
	}
	name := "foreign-" + c.marker[len(c.marker)-8:]
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wp.Namespace, Labels: map[string]string{
			kubernetes.LabelWorker: workers[0].Status.WorkerID, kubernetes.LabelRun: name,
		}},
		Spec: corev1.PodSpec{
			NodeSelector:                 map[string]string{absentLabel: "true"},
			AutomountServiceAccountToken: new(bool),
			Containers:                   []corev1.Container{{Name: "foreign", Image: controllerBootstrapImage}},
		},
	}
	if _, err := c.core.Pods(wp.Namespace).Create(ctx, p, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("%w: create the Pod %s: %w", harness.ErrStep, name, err)
	}
	c.w.IDs[key] = wp.Namespace + "/" + name
	return nil
}

// remains checks that the Pod kept as key exists, with no owner.
func (c *controllerSteps) remains(ctx context.Context, key string) error {
	ns, name, ok := strings.Cut(c.w.IDs[key], "/")
	if !ok {
		return fmt.Errorf("%w: no Pod kept as %s", harness.ErrStep, key)
	}
	p, err := c.core.Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("%w: the Pod %s was deleted", harness.ErrStep, name)
	}
	if err != nil {
		return fmt.Errorf("read the Pod %s: %w", name, err)
	}
	if len(p.OwnerReferences) != 0 || p.DeletionTimestamp != nil {
		return fmt.Errorf("%w: the Pod %s was taken: owners %+v, deleting %v", harness.ErrStep, name, p.OwnerReferences, p.DeletionTimestamp)
	}
	return nil
}

// keepWorker keeps the worker ID of the WorkerPool name's first slot as
// key.
func (c *controllerSteps) keepWorker(ctx context.Context, name, key string) error {
	w, err := c.firstWorker(ctx, name)
	if err != nil {
		return err
	}
	if w.Status.WorkerID == "" {
		return fmt.Errorf("%w: the Worker of %s holds no worker", harness.ErrStep, name)
	}
	c.w.IDs[key] = w.Status.WorkerID
	return nil
}

func (c *controllerSteps) firstWorker(ctx context.Context, name string) (controller.Worker, error) {
	wp, err := c.find(ctx, name)
	if err != nil {
		return controller.Worker{}, err
	}
	workers, err := c.slots(ctx, wp)
	if err != nil {
		return controller.Worker{}, err
	}
	if len(workers) == 0 {
		return controller.Worker{}, fmt.Errorf("%w: %s has no Worker: %w", harness.ErrStep, name, errNoPool)
	}
	return workers[0], nil
}

// workerReports waits until the first Worker of the WorkerPool name
// reports the condition typ with status and reason.
func (c *controllerSteps) workerReports(ctx context.Context, name, typ, status, reason string) error {
	var last *metav1.Condition
	err := deps.Eventually(ctx, fmt.Sprintf("the Worker of %s to report %s %s %s", name, typ, status, reason),
		func(ctx context.Context) (bool, error) {
			w, err := c.firstWorker(ctx, name)
			if errors.Is(err, errNoPool) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			last = meta.FindStatusCondition(w.Status.Conditions, typ)
			return last != nil && string(last.Status) == status && last.Reason == reason, nil
		})
	if err != nil {
		return fmt.Errorf("%w (last %+v)", err, last)
	}
	return nil
}

// cutFront makes the HTTPS front, the controllers' and run Pods' only
// way to the servers, answer 502 for seconds.
func (c *controllerSteps) cutFront(ctx context.Context, seconds int) error {
	c.cut.Store(true)
	defer c.cut.Store(false)
	select {
	case <-ctx.Done():
		return fmt.Errorf("cut the front: %w", ctx.Err())
	case <-time.After(time.Duration(seconds) * time.Second):
		return nil
	}
}
