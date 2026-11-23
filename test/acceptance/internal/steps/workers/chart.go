package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/storage/driver"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
	"github.com/weaveplatform/infraweave/internal/agent/executor/kubernetes"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// chartPath is the infraweave-workers chart, relative to the module root.
var chartPath = filepath.Join("deploy", "helm", "infraweave-workers")

// chartTimeout bounds an install, upgrade or uninstall, each of which
// waits for the release's controllers or its uninstall Job.
const chartTimeout = 4 * time.Minute

// chart is the state of the scenario's infraweave-workers releases: each
// installed through Helm's SDK, as helm install does, with the bootstrap
// image the suite imports into the node as the controller's image, so the
// controllers run the build under test inside the cluster.
type chart struct {
	kube     *clientset.Clientset
	config   *rest.Config
	image    string
	mirror   string
	relay    net.Listener
	releases map[string]*chartRelease
}

// chartRelease is one installed release: its namespace, the values it was
// last installed or upgraded with, and the controller Pods deleted.
type chartRelease struct {
	namespace string
	values    map[string]any
	deleted   map[types.UID]bool
}

func (c *controllerSteps) registerChart(sc *godog.ScenarioContext) {
	c.chart = &chart{releases: map[string]*chartRelease{}}
	sc.Step(`^the worker chart is installed as "(\w+)" in "(\w+)" with the Managed pool "(\w+)" of capacity (\d+) in the space "(\w+)"$`, c.chartInstall)
	sc.Step(`^the worker chart release "(\w+)" eventually has (\d+) ready controllers?$`, c.chartReady)
	sc.Step(`^the controllers of "(\w+)" run the imported image and log its commit and FIPS 140-3 mode$`, c.chartProvenance)
	sc.Step(`^the controller account of "(\w+)" is refused Secrets, WorkerPools and Pods in "(\w+)" and Secret listing in its own$`, c.chartRBAC)
	sc.Step(`^the HTTPS front answers 502 until the worker chart release "(\w+)" has (\d+) ready controllers?$`, c.chartCut)
	sc.Step(`^the slots of the WorkerPool "(\w+)" are eventually idle$`, c.idle)
	sc.Step(`^the worker chart release "(\w+)" is upgraded with the pool "(\w+)" of capacity (\d+) and debug logs$`, c.chartUpgrade)
	sc.Step(`^the controller Pods of "(\w+)" are deleted$`, c.chartRestart)
	sc.Step(`^the worker chart release "(\w+)" is uninstalled$`, c.chartUninstall)
	sc.Step(`^the worker chart release "(\w+)" left only the worker CRDs$`, c.chartGone)
	sc.Step(`^the worker chart install of "(\w+)" in "(\w+)" with the values '(.*)' is refused naming "([^"]+)"$`, c.chartRefused)
	sc.Step(`^the worker chart install of "(\w+)" in "(\w+)" over a pool whose credential Secret does not exist is refused naming "([^"]+)"$`, c.chartMissingPoolSecret)
}

// chartClients makes the scenario's Kubernetes clients and, on first
// use, the clientset Helm's checks read through.
func (c *controllerSteps) chartClients(ctx context.Context) error {
	if err := c.clients(ctx); err != nil {
		return err
	}
	if c.chart.kube != nil {
		return nil
	}
	config, err := clientcmd.BuildConfigFromFlags("", c.cluster.Kubeconfig)
	if err != nil {
		return fmt.Errorf("the lab's kubeconfig: %w", err)
	}
	if c.chart.kube, err = clientset.NewForConfig(config); err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	c.chart.config = config
	return nil
}

// helm is Helm's action configuration for releases in namespace, through
// the lab's kubeconfig, storing releases as Secrets as helm does.
func (c *controllerSteps) helm(namespace string) (*action.Configuration, error) {
	flags := genericclioptions.NewConfigFlags(false)
	flags.KubeConfig, flags.Namespace = &c.cluster.Kubeconfig, &namespace
	cfg := action.NewConfiguration()
	if err := cfg.Init(flags, namespace, "secret"); err != nil {
		return nil, fmt.Errorf("helm: %w", err)
	}
	return cfg, nil
}

// prepareChart readies what every release of the scenario shares: the
// front, the trust Secret of the front's and the fakes' authorities in
// each scenario namespace, the bootstrap image imported into the node,
// the tool mirror where Pods reach it, and the record of run Pods.
func (c *controllerSteps) prepareChart(ctx context.Context) error {
	if err := c.chartClients(ctx); err != nil {
		return err
	}
	if c.chart.image != "" {
		return nil
	}
	if _, err := c.frontURL(ctx); err != nil {
		return err
	}
	if err := c.trust(); err != nil {
		return err
	}
	bundle, err := os.ReadFile(c.caFile)
	if err != nil {
		return fmt.Errorf("read the controllers' CA: %w", err)
	}
	for _, ns := range c.order {
		if err := c.secret(ctx, ns, "trust", map[string][]byte{"ca.pem": bundle}); err != nil {
			return err
		}
	}
	host, err := c.w.Suite.Launchers(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names the build
	}
	for _, t := range scenarioTools {
		if !slices.Contains(c.w.Tags, t.tag) || t.image == "" {
			continue
		}
		if err := host.NodeImage(ctx, c.cluster, t.image); err != nil {
			return err //nolint:wrapcheck // names the image
		}
		mirror, err := host.Mirror(ctx, c.w.Suite.Deps, t.tool)
		if err != nil {
			return err //nolint:wrapcheck // names the release
		}
		if c.chart.mirror, err = c.relayMirror(ctx, mirror); err != nil {
			return err
		}
	}
	if c.chart.image, err = host.BootstrapImage(ctx, c.cluster); err != nil {
		return err //nolint:wrapcheck // names the import
	}
	c.watchPods(ctx)
	return nil
}

// relayMirror relays a listener where Pods reach the host to the tool
// mirror, which listens on the host's loopback, and answers its URL as
// Pods reach it.
func (c *controllerSteps) relayMirror(ctx context.Context, mirror string) (string, error) {
	u, err := url.Parse(mirror)
	if err != nil {
		return "", fmt.Errorf("the tool mirror: %w", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(c.pods.Listen, "0"))
	if err != nil {
		return "", fmt.Errorf("the tool mirror's relay: %w", err)
	}
	c.chart.relay = ln
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer in.Close()
				out, err := (&net.Dialer{Timeout: 10 * time.Second}).Dial("tcp", u.Host)
				if err != nil {
					return
				}
				defer out.Close()
				var wg sync.WaitGroup
				wg.Go(func() { _, _ = io.Copy(out, in) })
				_, _ = io.Copy(in, out)
				_ = in.Close()
				wg.Wait()
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return "http://" + net.JoinHostPort(c.pods.Host, port), nil
}

// chartValues are a release's values in namespace: image, pinned by its
// digest, the scenario's trust and mirror, network policies
// that let the controller reach the API server and the host, and its runs
// reach only the host, and the FIPS 140-3 mode of the suite's processes.
func (c *controllerSteps) chartValues(namespace, image string) map[string]any {
	repository, digest, _ := strings.Cut(image, "@")
	mode := "on"
	if strings.Contains(os.Getenv("GODEBUG"), "fips140=only") {
		mode = "only"
	}
	host := map[string]any{"cidr": c.pods.Host + "/32"}
	v := map[string]any{
		"clusterID":       controllerCluster,
		"watchNamespaces": []any{namespace},
		"controller": map[string]any{
			"image":   map[string]any{"repository": repository, "digest": digest},
			"fips140": mode,
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "50m", "memory": "128Mi", "ephemeral-storage": "256Mi"},
				"limits":   map[string]any{"cpu": "1", "memory": "1Gi", "ephemeral-storage": "3Gi"},
			},
			"cacheSizeLimit":        "2Gi",
			"cleanupTimeoutSeconds": 30,
		},
		"managementCredentialsRef": map[string]any{"name": "management", "idKey": "id", "secretKey": "secret"},
		"trust":                    map[string]any{"caSecretRef": map[string]any{"name": "trust", "key": "ca.pem"}},
		"networkPolicy": map[string]any{
			"enabled":   true,
			"dnsEgress": []any{},
			"controllerEgress": []any{
				map[string]any{"cidr": "0.0.0.0/0", "ports": []any{
					map[string]any{"port": 443, "protocol": "TCP"}, map[string]any{"port": 6443, "protocol": "TCP"},
				}},
				host,
			},
			"runEgress": []any{host},
		},
		"pools":     []any{},
		"uninstall": map[string]any{"timeoutSeconds": 120},
	}
	if c.chart.mirror != "" {
		v["toolMirror"] = c.chart.mirror
	}
	return v
}

// chartPool is the values entry of a Managed pool named name in namespace,
// whose credential Secret the step writes.
func (c *controllerSteps) chartPool(ctx context.Context, name, namespace, space string, capacity int64) (map[string]any, error) {
	_, key, err := newPoolKey()
	if err != nil {
		return nil, err
	}
	if err := c.secret(ctx, namespace, name+"-credentials", map[string][]byte{controller.PrivateKeyKey: key}); err != nil {
		return nil, err
	}
	u, err := c.pool(ctx, name, namespace, controller.Managed, capacity)
	if err != nil {
		return nil, err
	}
	spec := u.Object["spec"].(map[string]any) //nolint:forcetypeassert // built by pool
	spec["space"], spec["name"] = c.w.Resolve("{"+space+"}"), "Chart "+name
	return map[string]any{"name": name, "namespace": namespace, "spec": spec}, nil
}

// install installs the chart as name in namespace with values, waiting
// until its controllers are ready.
func (c *controllerSteps) install(ctx context.Context, name, namespace string, values map[string]any) error {
	ch, err := loader.Load(filepath.Join(harness.RepoRoot, chartPath))
	if err != nil {
		return fmt.Errorf("load the worker chart: %w", err)
	}
	cfg, err := c.helm(namespace)
	if err != nil {
		return err
	}
	in := action.NewInstall(cfg)
	in.ReleaseName, in.Namespace = name, namespace
	in.WaitStrategy, in.Timeout = kube.StatusWatcherStrategy, chartTimeout
	if _, err := in.RunWithContext(ctx, ch, values); err != nil {
		return fmt.Errorf("helm install %s: %w", name, err)
	}
	return nil
}

func (c *controllerSteps) chartInstall(ctx context.Context, name, in, pool string, capacity int64, space string) error {
	if err := c.prepareChart(ctx); err != nil {
		return err
	}
	ns := c.namespaces[in]
	entry, err := c.chartPool(ctx, pool, ns, space, capacity)
	if err != nil {
		return err
	}
	values := c.chartValues(ns, c.chart.image)
	values["pools"] = []any{entry}
	c.chart.releases[name] = &chartRelease{namespace: ns, values: values, deleted: map[types.UID]bool{}}
	if err := c.install(ctx, name, ns, values); err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	return nil
}

func (c *controllerSteps) release(name string) (*chartRelease, error) {
	r, ok := c.chart.releases[name]
	if !ok {
		return nil, fmt.Errorf("%w: no worker chart release %q", harness.ErrStep, name)
	}
	return r, nil
}

// controllerPods are the release's controller Pods that are running.
func (c *controllerSteps) controllerPods(ctx context.Context, name string, r *chartRelease) ([]corev1.Pod, error) {
	list, err := c.chart.kube.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + name + ",app.kubernetes.io/component=worker-controller",
	})
	if err != nil {
		return nil, fmt.Errorf("list the controller Pods: %w", err)
	}
	return list.Items, nil
}

func podReady(p *corev1.Pod) bool {
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// chartReady waits until exactly n of the release's current controller
// Pods are ready, none of them one a step deleted.
func (c *controllerSteps) chartReady(ctx context.Context, name string, n int) error {
	r, err := c.release(name)
	if err != nil {
		return err
	}
	var seen []string
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, fmt.Sprintf("release %s to have %d ready controllers", name, n), func(ctx context.Context) (bool, error) {
		pods, err := c.controllerPods(ctx, name, r)
		if err != nil {
			return false, err
		}
		ready := 0
		seen = seen[:0]
		for i := range pods {
			p := &pods[i]
			if r.deleted[p.UID] || p.DeletionTimestamp != nil {
				continue
			}
			seen = append(seen, p.Name)
			if podReady(p) {
				ready++
			}
		}
		return ready == n, nil
	})
}

// chartProvenance checks that each controller runs the imported image by
// its digest and logs, as it starts, the commit the suite built and the
// FIPS 140-3 mode the release sets.
func (c *controllerSteps) chartProvenance(ctx context.Context, name string) error {
	r, err := c.release(name)
	if err != nil {
		return err
	}
	host, err := c.w.Suite.Launchers(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names the build
	}
	_, digest, _ := strings.Cut(c.chart.image, "@")
	mode := r.values["controller"].(map[string]any)["fips140"] //nolint:forcetypeassert // built by chartValues
	pods, err := c.controllerPods(ctx, name, r)
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		return fmt.Errorf("%w: the release %s runs no controller", harness.ErrStep, name)
	}
	for _, p := range pods {
		if len(p.Status.ContainerStatuses) != 1 || !strings.HasSuffix(p.Status.ContainerStatuses[0].ImageID, digest) {
			return fmt.Errorf("%w: the controller %s runs %+v, not %s", harness.ErrStep, p.Name, p.Status.ContainerStatuses, digest)
		}
		raw, err := c.chart.kube.CoreV1().Pods(r.namespace).GetLogs(p.Name, &corev1.PodLogOptions{Container: "worker-controller"}).DoRaw(ctx)
		if err != nil {
			return fmt.Errorf("read the log of %s: %w", p.Name, err)
		}
		var started map[string]any
		for line := range bytes.Lines(raw) {
			var entry map[string]any
			if json.Unmarshal(line, &entry) == nil && entry["msg"] == "worker controller starting" {
				started = entry
				break
			}
		}
		if started == nil || started["commit"] != host.Commit || started["fips140"] != mode || started["fips140_module"] == "" {
			return fmt.Errorf("%w: the controller %s started with %v, not commit %s in FIPS 140-3 mode %v", harness.ErrStep,
				p.Name, started, host.Commit, mode)
		}
	}
	return nil
}

// chartRBAC acts as the release's controller service account through a
// token the API server issues it, and checks what the API server refuses.
func (c *controllerSteps) chartRBAC(ctx context.Context, name, other string) error {
	r, err := c.release(name)
	if err != nil {
		return err
	}
	account := name + "-worker-controller"
	tr, err := c.chart.kube.CoreV1().ServiceAccounts(r.namespace).CreateToken(ctx, account, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: new(int64(600))},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("a token of %s: %w", account, err)
	}
	config := rest.AnonymousClientConfig(c.chart.config)
	config.BearerToken = tr.Status.Token
	core, err := clientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	api, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	own, theirs := r.namespace, c.namespaces[other]
	checks := []struct {
		what    string
		allowed bool
		call    func() error
	}{
		{"read its management Secret", true, func() error {
			_, err := core.CoreV1().Secrets(own).Get(ctx, "management", metav1.GetOptions{})
			return err
		}},
		{"list its WorkerPools", true, func() error {
			_, err := api.Resource(controller.PoolResource).Namespace(own).List(ctx, metav1.ListOptions{})
			return err
		}},
		{"list its Pods", true, func() error {
			_, err := core.CoreV1().Pods(own).List(ctx, metav1.ListOptions{})
			return err
		}},
		{"list its Secrets", false, func() error {
			_, err := core.CoreV1().Secrets(own).List(ctx, metav1.ListOptions{})
			return err
		}},
		{"read another namespace's management Secret", false, func() error {
			_, err := core.CoreV1().Secrets(theirs).Get(ctx, "management", metav1.GetOptions{})
			return err
		}},
		{"list another namespace's WorkerPools", false, func() error {
			_, err := api.Resource(controller.PoolResource).Namespace(theirs).List(ctx, metav1.ListOptions{})
			return err
		}},
		{"list another namespace's Pods", false, func() error {
			_, err := core.CoreV1().Pods(theirs).List(ctx, metav1.ListOptions{})
			return err
		}},
		{"list Namespaces", false, func() error {
			_, err := core.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			return err
		}},
	}
	for _, check := range checks {
		err := check.call()
		if check.allowed && err != nil {
			return fmt.Errorf("%w: the controller cannot %s: %w", harness.ErrStep, check.what, err)
		}
		if !check.allowed && !apierrors.IsForbidden(err) {
			return fmt.Errorf("%w: the controller may %s: %w", harness.ErrStep, check.what, err)
		}
	}
	return nil
}

// chartCut cuts the front until n of the release's controllers are ready.
func (c *controllerSteps) chartCut(ctx context.Context, name string, n int) error {
	c.cut.Store(true)
	defer c.cut.Store(false)
	return c.chartReady(ctx, name, n)
}

// idle waits until no Worker of the pool records an assignment: each
// slot's launcher has reported its run's exit and holds no run.
func (c *controllerSteps) idle(ctx context.Context, pool string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "the slots of "+pool+" to be idle", func(ctx context.Context) (bool, error) {
		wp, err := c.find(ctx, pool)
		if err != nil {
			return false, err
		}
		workers, err := c.slots(ctx, wp)
		if err != nil {
			return false, err
		}
		for _, w := range workers {
			if w.Status.Assignment != nil {
				return false, nil
			}
		}
		return true, nil
	})
}

// chartUpgrade upgrades the release with name's pool at capacity and the
// controller logging at debug, waiting until its controllers are ready.
func (c *controllerSteps) chartUpgrade(ctx context.Context, name, pool string, capacity int64) error {
	r, err := c.release(name)
	if err != nil {
		return err
	}
	values := r.values
	for _, entry := range values["pools"].([]any) { //nolint:forcetypeassert // built by chartInstall
		p := entry.(map[string]any) //nolint:forcetypeassert // built by chartPool
		if p["name"] == pool {
			p["spec"].(map[string]any)["capacity"] = capacity //nolint:forcetypeassert // built by pool
		}
	}
	values["controller"].(map[string]any)["log"] = map[string]any{"level": "debug", "format": "json"} //nolint:forcetypeassert // built by chartValues
	ch, err := loader.Load(filepath.Join(harness.RepoRoot, chartPath))
	if err != nil {
		return fmt.Errorf("load the worker chart: %w", err)
	}
	cfg, err := c.helm(r.namespace)
	if err != nil {
		return err
	}
	up := action.NewUpgrade(cfg)
	up.Namespace, up.WaitStrategy, up.Timeout = r.namespace, kube.StatusWatcherStrategy, chartTimeout
	if _, err := up.RunWithContext(ctx, name, ch, values); err != nil {
		return fmt.Errorf("%w: helm upgrade %s: %w", harness.ErrStep, name, err)
	}
	return nil
}

// chartRestart deletes every controller Pod of the release, as a node
// drain or a crash would, and keeps them as deleted.
func (c *controllerSteps) chartRestart(ctx context.Context, name string) error {
	r, err := c.release(name)
	if err != nil {
		return err
	}
	pods, err := c.controllerPods(ctx, name, r)
	if err != nil {
		return err
	}
	for _, p := range pods {
		r.deleted[p.UID] = true
		if err := c.chart.kube.CoreV1().Pods(r.namespace).Delete(ctx, p.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete the controller %s: %w", p.Name, err)
		}
	}
	return nil
}

// uninstall uninstalls the release, which waits for its uninstall Job and
// for every object it deletes to be gone.
func (c *controllerSteps) uninstall(ctx context.Context, name string, timeout time.Duration) error {
	r, err := c.release(name)
	if err != nil {
		return err
	}
	cfg, err := c.helm(r.namespace)
	if err != nil {
		return err
	}
	un := action.NewUninstall(cfg)
	un.WaitStrategy, un.Timeout = kube.StatusWatcherStrategy, timeout
	if _, err := un.Run(name); err != nil {
		return fmt.Errorf("helm uninstall %s: %w; its uninstall Job said: %s", name, err, c.uninstallLog(ctx, name, r.namespace))
	}
	delete(c.chart.releases, name)
	c.chart.releases[name+" (uninstalled)"] = r
	return nil
}

// uninstallLog is the last lines the release's uninstall Job logged, which
// a failed Job keeps: the pools that remain and why.
func (c *controllerSteps) uninstallLog(ctx context.Context, name, namespace string) string {
	pods, err := c.chart.kube.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + name + ",app.kubernetes.io/component=worker-uninstall",
	})
	if err != nil || len(pods.Items) == 0 {
		return fmt.Sprintf("no log (%v)", err)
	}
	tail := int64(3)
	raw, err := c.chart.kube.CoreV1().Pods(namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{TailLines: &tail}).DoRaw(ctx)
	if err != nil {
		return fmt.Sprintf("no log (%v)", err)
	}
	return string(bytes.TrimSpace(raw))
}

func (c *controllerSteps) chartUninstall(ctx context.Context, name string) error {
	if err := c.uninstall(ctx, name, chartTimeout); err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	return nil
}

// chartGone checks the uninstalled release left nothing in its namespace
// but what the scenario made: no object of its, no WorkerPool, Worker or
// run Pod, and the worker CRDs, which Helm never deletes, still served.
func (c *controllerSteps) chartGone(ctx context.Context, name string) error {
	r, ok := c.chart.releases[name+" (uninstalled)"]
	if !ok {
		return fmt.Errorf("%w: the release %s was not uninstalled", harness.ErrStep, name)
	}
	ns := r.namespace
	labelled := metav1.ListOptions{LabelSelector: "app.kubernetes.io/instance=" + name}
	k := c.chart.kube
	counts := map[string]func() (int, error){
		"Deployment": func() (int, error) { l, err := k.AppsV1().Deployments(ns).List(ctx, labelled); return lenOf(l, err) },
		"Pod":        func() (int, error) { l, err := k.CoreV1().Pods(ns).List(ctx, labelled); return lenOf(l, err) },
		"run Pod": func() (int, error) {
			l, err := k.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: kubernetes.LabelRun})
			return lenOf(l, err)
		},
		"ServiceAccount": func() (int, error) {
			l, err := k.CoreV1().ServiceAccounts(ns).List(ctx, labelled)
			return lenOf(l, err)
		},
		"ConfigMap":   func() (int, error) { l, err := k.CoreV1().ConfigMaps(ns).List(ctx, labelled); return lenOf(l, err) },
		"Role":        func() (int, error) { l, err := k.RbacV1().Roles(ns).List(ctx, labelled); return lenOf(l, err) },
		"RoleBinding": func() (int, error) { l, err := k.RbacV1().RoleBindings(ns).List(ctx, labelled); return lenOf(l, err) },
		"NetworkPolicy": func() (int, error) {
			l, err := k.NetworkingV1().NetworkPolicies(ns).List(ctx, labelled)
			return lenOf(l, err)
		},
		"PodDisruptionBudget": func() (int, error) {
			l, err := k.PolicyV1().PodDisruptionBudgets(ns).List(ctx, labelled)
			return lenOf(l, err)
		},
		"Job":   func() (int, error) { l, err := k.BatchV1().Jobs(ns).List(ctx, labelled); return lenOf(l, err) },
		"Lease": func() (int, error) { l, err := k.CoordinationV1().Leases(ns).List(ctx, labelled); return lenOf(l, err) },
		"WorkerPool": func() (int, error) {
			l, err := c.api.Resource(controller.PoolResource).Namespace(ns).List(ctx, metav1.ListOptions{})
			return lenOf(l, err)
		},
		"Worker": func() (int, error) {
			l, err := c.api.Resource(controller.WorkerResource).Namespace(ns).List(ctx, metav1.ListOptions{})
			return lenOf(l, err)
		},
	}
	// Helm waits for the objects it deletes, and the garbage collector
	// then removes what they owned, such as the controller's Pods.
	left := ""
	err := deps.Eventually(ctx, "the release "+name+" to leave nothing", func(context.Context) (bool, error) {
		left = ""
		for _, kind := range slices.Sorted(maps.Keys(counts)) {
			n, err := counts[kind]()
			if err != nil {
				return false, fmt.Errorf("list %s: %w", kind, err)
			}
			if n != 0 {
				left += fmt.Sprintf(" %d %s", n, kind)
			}
		}
		return left == "", nil
	})
	if err != nil {
		return fmt.Errorf("%w: the release %s left%s in %s: %w", harness.ErrStep, name, left, ns, err)
	}
	for _, crd := range []string{"workerpools.workers.infraweave.io", "workers.workers.infraweave.io"} {
		if _, err := c.api.Resource(definitionsResource).Get(ctx, crd, metav1.GetOptions{}); err != nil {
			return fmt.Errorf("%w: the CRD %s: %w", harness.ErrStep, crd, err)
		}
	}
	return nil
}

func lenOf(l runtime.Object, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	return meta.LenList(l), nil
}

// chartRefused tries to install the chart as name in namespace with the
// values of a release there merged with patch, as a JSON merge patch
// whose "{namespace}" names resolve to the scenario's namespaces, and
// checks Helm refuses it naming want and that nothing was installed.
func (c *controllerSteps) chartRefused(ctx context.Context, name, in, patch, want string) error {
	values, err := c.refusable(ctx, in)
	if err != nil {
		return err
	}
	for short, ns := range c.namespaces {
		patch = strings.ReplaceAll(patch, "{"+short+"}", ns)
	}
	var change map[string]any
	if err := json.Unmarshal([]byte(c.w.Resolve(patch)), &change); err != nil {
		return fmt.Errorf("%w: the values patch: %w", harness.ErrStep, err)
	}
	return c.refuse(ctx, name, c.namespaces[in], mergePatch(values, change), want)
}

func (c *controllerSteps) chartMissingPoolSecret(ctx context.Context, name, in, want string) error {
	values, err := c.refusable(ctx, in)
	if err != nil {
		return err
	}
	pool := values["pools"].([]any)[0].(map[string]any)                                //nolint:forcetypeassert // built by refusable
	pool["spec"].(map[string]any)["credentialsRef"] = map[string]any{"name": "absent"} //nolint:forcetypeassert // built by pool
	return c.refuse(ctx, name, c.namespaces[in], values, want)
}

// refusable are valid values of a release in namespace in, but for an
// image that is never pulled and an endpoint nothing serves, with a
// Managed pool "build" whose credential Secret exists.
func (c *controllerSteps) refusable(ctx context.Context, in string) (map[string]any, error) {
	if err := c.chartClients(ctx); err != nil {
		return nil, err
	}
	if err := c.reserve(ctx); err != nil {
		return nil, err
	}
	ns := c.namespaces[in]
	_, err := c.core.Secrets(ns).Get(ctx, "trust", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		err = c.secret(ctx, ns, "trust", map[string][]byte{"ca.pem": c.ca})
	}
	if err != nil {
		return nil, fmt.Errorf("the trust Secret: %w", err)
	}
	_, key, err := newPoolKey()
	if err != nil {
		return nil, err
	}
	if err := c.secret(ctx, ns, "build-credentials", map[string][]byte{controller.PrivateKeyKey: key}); err != nil {
		return nil, err
	}
	values := c.chartValues(ns, controllerBootstrapImage)
	values["pools"] = []any{map[string]any{"name": "build", "namespace": ns, "spec": map[string]any{
		"endpoint": "https://infraweave.invalid", "account": harness.Home.Account, "space": "team", "mode": controller.Managed,
		"name": "Build", "credentialsRef": map[string]any{"name": "build-credentials"}, "capacity": 1,
		"pod": map[string]any{
			"storageSize": "1Gi",
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "100m", "memory": "128Mi", "ephemeral-storage": "1Gi"},
				"limits":   map[string]any{"cpu": "1", "memory": "512Mi", "ephemeral-storage": "1Gi"},
			},
		},
	}}}
	return values, nil
}

func (c *controllerSteps) refuse(ctx context.Context, name, ns string, values map[string]any, want string) error {
	err := c.install(ctx, name, ns, values)
	if err == nil {
		c.chart.releases[name] = &chartRelease{namespace: ns, values: values, deleted: map[types.UID]bool{}}
		return fmt.Errorf("%w: the worker chart installed as %s", harness.ErrStep, name)
	}
	if !strings.Contains(err.Error(), want) {
		return fmt.Errorf("%w: the install was refused with %w, not naming %q", harness.ErrStep, err, want)
	}
	cfg, herr := c.helm(ns)
	if herr != nil {
		return herr
	}
	if _, herr := action.NewHistory(cfg).Run(name); !errors.Is(herr, driver.ErrReleaseNotFound) {
		return fmt.Errorf("%w: the refused install left a release record: %w", harness.ErrStep, herr)
	}
	if _, herr := c.chart.kube.AppsV1().Deployments(ns).Get(ctx, name+"-worker-controller", metav1.GetOptions{}); !apierrors.IsNotFound(herr) {
		return fmt.Errorf("%w: the refused install left its controller: %w", harness.ErrStep, herr)
	}
	return nil
}

// mergePatch applies the JSON merge patch patch to v.
func mergePatch(v, patch map[string]any) map[string]any {
	for key, value := range patch {
		sub, isMap := value.(map[string]any)
		cur, curMap := v[key].(map[string]any)
		switch {
		case value == nil:
			delete(v, key)
		case isMap && curMap:
			v[key] = mergePatch(cur, sub)
		default:
			v[key] = value
		}
	}
	return v
}

// uninstallCharts uninstalls each release the scenario left installed,
// while its controllers still run to finalize their pools.
func (c *controllerSteps) uninstallCharts(ctx context.Context) error {
	var result error
	for _, name := range slices.Sorted(maps.Keys(c.chart.releases)) {
		if strings.HasSuffix(name, " (uninstalled)") {
			continue
		}
		result = errors.Join(result, c.uninstall(ctx, name, time.Minute))
	}
	if c.chart.relay != nil {
		result = errors.Join(result, c.chart.relay.Close())
	}
	return result
}
