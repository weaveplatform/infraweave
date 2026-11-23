package workers

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// controllerCluster is the cluster ID every scenario's controllers name.
const controllerCluster = "acceptance"

// controllerBootstrapImage satisfies worker_controller.bootstrap_image in
// a scenario whose controllers execute no run, so no Pod pulls it.
const controllerBootstrapImage = "ghcr.io/weaveplatform/infraweave@sha256:0000000000000000000000000000000000000000000000000000000000000000"

// controllerSteps run real worker controllers against the owned cluster
// and the scenario's servers. Each scenario has its own namespaces, which
// its cleanup removes, and an HTTPS front that balances the controllers'
// calls over the servers, since a WorkerPool's endpoint is HTTPS.
type controllerSteps struct {
	w       *harness.World
	workers *workerSteps

	cluster    *deps.Kubernetes
	api        dynamic.Interface
	core       corev1client.CoreV1Interface
	marker     string
	namespaces map[string]string
	order      []string
	pods       *harness.PodNetwork
	ln         net.Listener
	front      *http.Server
	frontAt    string
	cut        atomic.Bool
	ca         []byte
	caFile     string
	recorder
	running []*harness.Server
	killed  []*harness.Server
	started int
	kept    map[string][]string
	// initialization is the local policy the controllers started from now
	// on evaluate, and initializationTimeout its budget when set.
	initialization        string
	initializationTimeout string
	// chart is the scenario's worker chart releases.
	chart *chart
}

func (c *controllerSteps) register(sc *godog.ScenarioContext) {
	c.namespaces, c.kept = map[string]string{}, map[string][]string{}
	sc.Step(`^a controller namespace "(\w+)" holds the (home|member) management key$`, c.namespace)
	sc.Step(`^(\d+) worker controllers? watch the controller namespaces$`, c.start)
	sc.Step(`^the leading worker controller is replaced$`, c.replace)
	c.registerRecovery(sc)
	c.registerChart(sc)
	sc.Step(`^the Managed WorkerPool "(\w+)" of capacity (\d+) in the space "(\w+)" is applied in "(\w+)"( for another account)?$`, c.managed)
	sc.Step(`^the Managed WorkerPool "(\w+)" of capacity (\d+) in the space "(\w+)", whose Pods select "(\w+)" nodes, is applied in "(\w+)"$`, c.selecting)
	sc.Step(
		`^the Existing WorkerPool "(\w+)" of capacity (\d+) over the pool of the certificate "(\w+)" with the key of "(\w+)" in the space "(\w+)" is applied in "(\w+)"$`,
		c.existing,
	)
	sc.Step(`^the WorkerPool "(\w+)" eventually reports (\w+) (True|False) "(\w+)"$`, c.reports)
	sc.Step(`^the WorkerPool "(\w+)" eventually has (\d+) registered slots, its pool kept as "(\w+)"$`, c.registered)
	sc.Step(`^the workers of the WorkerPool "(\w+)" are the ones kept$`, c.same)
	sc.Step(`^the WorkerPools "(\w+)" and "(\w+)" hold distinct pools and tokens$`, c.distinct)
	sc.Step(`^the WorkerPool "(\w+)" is deleted$`, c.remove)
	sc.Step(`^the WorkerPool "(\w+)" is eventually gone$`, c.gone)
	sc.Step(`^the credential Secret of "(\w+)" holds a private key that is not PEM$`, c.corrupt)
	sc.Step(`^the management Secret of "(\w+)" is removed$`, c.dropManagement)
	sc.Step(`^the management Secret of "(\w+)" holds the (home|member) management key$`, c.management)
	sc.Step(`^run Pods reach the scenario's servers, and the objects they sign, through the HTTPS front$`, c.signedThroughFront)
	sc.Step(`^the run "(\w+)" ran in Pods of the WorkerPool "(\w+)" from the image "([^"]+)", each hardened and holding no "([^"]+)"$`, c.ran)
	sc.Step(`^no run Pod, Worker resource or worker controller log line holds "([^"]+)"$`, c.clean)
	sc.Step(`^the run "(\w+)" eventually has no Pod$`, c.noPod)
	sc.Step(`^the worker controllers started from now on evaluate the initialization policy(?: within (\d+s))?:$`,
		func(timeout string, body *godog.DocString) {
			c.initialization, c.initializationTimeout = body.Content, timeout
		})
	sc.Step(
		`^the run "(\w+)" ran (\d+) initialization commands? in helper Pods of the WorkerPool "(\w+)" from the image "([^"]+)", each hardened and holding no "([^"]+)"$`,
		c.helpers,
	)
	sc.Step(`^the run "(\w+)" eventually runs an initialization command Pod$`, c.helperRuns)
	sc.Step(`^the run "(\w+)" eventually has no initialization command Pod$`, c.noHelper)
	sc.Step(`^the active deadline of the run "(\w+)"'s initialization command Pod ends it$`, c.deadlineEnds)
	c.registerPhaseInputs(sc)
	sc.After(c.cleanup)
}

func (c *controllerSteps) clients(ctx context.Context) error {
	if c.api != nil {
		return nil
	}
	cluster, err := c.w.Suite.Deps.Kubernetes(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names the lab
	}
	config := &rest.Config{Host: cluster.Endpoint}
	if c.api, err = dynamic.NewForConfigAndClient(config, cluster.HTTPClient()); err != nil {
		return fmt.Errorf("kubernetes dynamic client: %w", err)
	}
	if c.core, err = corev1client.NewForConfigAndClient(config, cluster.HTTPClient()); err != nil {
		return fmt.Errorf("kubernetes core client: %w", err)
	}
	c.cluster, c.marker = cluster, strings.ToLower(kernel.NewULID().String())
	return nil
}

// namespace creates the scenario's namespace name and its management
// Secret of the tenant's API key.
func (c *controllerSteps) namespace(ctx context.Context, name, tenant string) error {
	if err := c.clients(ctx); err != nil {
		return err
	}
	ns := "iw-c-" + c.marker[len(c.marker)-12:] + "-" + strings.ToLower(name)
	_, err := c.core.Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: ns, Labels: map[string]string{"io.infraweave.acceptance.owner": c.marker},
	}}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("%w: create namespace %s: %w", harness.ErrStep, ns, err)
	}
	c.namespaces[name] = ns
	c.order = append(c.order, ns)
	return c.management(ctx, name, tenant)
}

func (c *controllerSteps) management(ctx context.Context, name, tenant string) error {
	t := harness.Tenants[tenant]
	return c.secret(ctx, c.namespaces[name], "management", map[string][]byte{
		"id": []byte(t.Key), "secret": []byte(t.Secret()),
	})
}

func (c *controllerSteps) dropManagement(ctx context.Context, name string) error {
	err := c.core.Secrets(c.namespaces[name]).Delete(ctx, "management", metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("delete the management Secret: %w", err)
	}
	return nil
}

func (c *controllerSteps) secret(ctx context.Context, namespace, name string, data map[string][]byte) error {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Data: data}
	_, err := c.core.Secrets(namespace).Create(ctx, s, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = c.core.Secrets(namespace).Update(ctx, s, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("%w: write Secret %s/%s: %w", harness.ErrStep, namespace, name, err)
	}
	return nil
}

// reserve binds the scenario's HTTPS front on first use, where run Pods
// reach it, without serving yet, with a certificate of its own authority.
func (c *controllerSteps) reserve(ctx context.Context) error {
	if c.ln != nil {
		return nil
	}
	if err := c.clients(ctx); err != nil {
		return err
	}
	pods, err := harness.StartPodNetwork(ctx, c.cluster)
	if err != nil {
		return err //nolint:wrapcheck // names the network
	}
	c.pods = pods
	cert, err := frontCertificate(net.ParseIP(pods.Host))
	if err != nil {
		return err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(pods.Listen, "0"))
	if err != nil {
		return fmt.Errorf("the front's listener: %w", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	c.ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	c.frontAt = "https://" + net.JoinHostPort(pods.Host, port)
	c.ca = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	c.caFile = filepath.Join(c.w.Dir, "controller-ca.pem")
	return nil
}

// trust writes the controllers' ca_file: the front's authority and the
// scenario's fakes', such as Gitea's, whose repositories they fetch.
func (c *controllerSteps) trust() error {
	bundle := slices.Clone(c.ca)
	if f := c.w.CAFile(); f != "" {
		fakes, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read the scenario's CA: %w", err)
		}
		bundle = append(bundle, fakes...)
	}
	if err := os.WriteFile(c.caFile, bundle, 0o600); err != nil {
		return fmt.Errorf("write the controllers' CA: %w", err)
	}
	return nil
}

// frontURL starts the scenario's HTTPS front on first use: a reverse
// proxy that sends each request to the next of the servers running then.
func (c *controllerSteps) frontURL(ctx context.Context) (string, error) {
	if c.front != nil {
		return c.frontAt, nil
	}
	if len(c.w.Servers) == 0 {
		return "", fmt.Errorf("%w: no server to front", harness.ErrStep)
	}
	if err := c.reserve(ctx); err != nil {
		return "", err
	}
	var next atomic.Uint64
	servers := slices.Clone(c.w.Servers)
	proxy := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		srv := servers[next.Add(1)%uint64(len(servers))]
		pr.SetURL(&url.URL{Scheme: "http", Host: srv.API})
	}}
	// While cut, the front answers as a gateway that lost its servers.
	front := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.cut.Load() {
			http.Error(w, "the installation is unreachable", http.StatusBadGateway)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	c.front = &http.Server{Handler: front, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = c.front.Serve(c.ln) }()
	return c.frontAt, nil
}

// start starts n worker controllers watching every namespace the
// scenario created.
func (c *controllerSteps) start(ctx context.Context, n int) error {
	if _, err := c.frontURL(ctx); err != nil {
		return err
	}
	for range n {
		if err := c.launch(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (c *controllerSteps) launch(ctx context.Context) error {
	c.started++
	dir := filepath.Join(c.w.Dir, fmt.Sprintf("worker-controller-%d", c.started))
	if err := os.MkdirAll(filepath.Join(dir, "cache"), 0o750); err != nil {
		return fmt.Errorf("controller dir: %w", err)
	}
	settings := map[string]any{
		"watch_namespaces": c.order,
		"cluster_id":       controllerCluster,
		"management_credentials_ref": map[string]any{
			"name": "management", "id_key": "id", "secret_key": "secret",
		},
		"leader_election": map[string]any{"enabled": true, "name": "iw-" + c.marker, "namespace": c.order[0]},
		"bootstrap_image": controllerBootstrapImage,
		"cache_dir":       filepath.Join(dir, "cache"),
		"ca_file":         c.caFile,
		"admin_listen":    "127.0.0.1:0",
		"cleanup_timeout": "30s",
		"log":             map[string]any{"format": "json"},
	}
	if c.initialization != "" {
		path := filepath.Join(dir, "initialization.rego")
		if err := os.WriteFile(path, []byte(c.initialization), 0o600); err != nil {
			return fmt.Errorf("write the controller's initialization policy: %w", err)
		}
		settings["initialization_policy"] = path
		if c.initializationTimeout != "" {
			settings["initialization_timeout"] = c.initializationTimeout
		}
	}
	if err := c.trust(); err != nil {
		return err
	}
	if err := c.executes(ctx, settings); err != nil {
		return err
	}
	config, err := yaml.Marshal(map[string]any{"worker_controller": settings})
	if err != nil {
		return fmt.Errorf("controller config: %w", err)
	}
	bin, err := harness.Binary(ctx)
	if err != nil {
		return err //nolint:wrapcheck // names the build
	}
	srv, err := harness.StartWorkerController(ctx, bin, dir, c.w.Suite.CoverDir, config, c.cluster.Kubeconfig,
		c.pods.Environment()...)
	if err != nil {
		return err //nolint:wrapcheck // names the process
	}
	c.running = append(c.running, srv)
	return nil
}

// replace stops the controller that leads, which releases its Lease, and
// starts another in its place.
func (c *controllerSteps) replace(ctx context.Context) error {
	leader, err := c.leader(ctx)
	if err != nil {
		return err
	}
	c.running = slices.DeleteFunc(c.running, func(s *harness.Server) bool { return s == leader })
	if err := leader.Stop(ctx); err != nil {
		return err //nolint:wrapcheck // names the process
	}
	return c.launch(ctx)
}

// leader waits for the running controller that leads.
func (c *controllerSteps) leader(ctx context.Context) (*harness.Server, error) {
	var leader *harness.Server
	err := deps.Eventually(ctx, "a worker controller to lead", func(context.Context) (bool, error) {
		for _, srv := range c.running {
			if bytes.Contains(srv.Stdout(), []byte(`"msg":"leading worker controller"`)) {
				leader = srv
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // names what it waited for
	}
	return leader, nil
}

// frontCertificate is a self-signed P-256 certificate for 127.0.0.1 and
// host, which a FIPS 140-only process accepts.
func frontCertificate(host net.IP) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("front key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "infraweave acceptance front"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("front certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

func newPoolKey() (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("pool key: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("pool key: %w", err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func (c *controllerSteps) pool(ctx context.Context, name, namespace, mode string, capacity int64) (*unstructured.Unstructured, error) {
	endpoint, err := c.frontURL(ctx)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": controller.Group + "/" + controller.Version,
		"kind":       "WorkerPool",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec": map[string]any{
			"endpoint":       endpoint,
			"account":        harness.Home.Account,
			"mode":           mode,
			"credentialsRef": map[string]any{"name": name + "-credentials"},
			"capacity":       capacity,
			"pod": map[string]any{
				"storageSize": "1Gi",
				"resources": map[string]any{
					"requests": map[string]any{"cpu": "100m", "memory": "128Mi", "ephemeral-storage": "1Gi"},
					"limits":   map[string]any{"cpu": "1", "memory": "512Mi", "ephemeral-storage": "1Gi"},
				},
			},
		},
	}}, nil
}

func (c *controllerSteps) apply(ctx context.Context, u *unstructured.Unstructured) error {
	_, err := c.api.Resource(controller.PoolResource).Namespace(u.GetNamespace()).Create(ctx, u, metav1.CreateOptions{FieldValidation: metav1.FieldValidationStrict})
	if err != nil {
		return fmt.Errorf("%w: apply WorkerPool %s: %w", harness.ErrStep, u.GetName(), err)
	}
	return nil
}

// managed applies a Managed WorkerPool whose credential Secret holds only
// a fresh private key, for the home account or another one.
func (c *controllerSteps) managed(ctx context.Context, name string, capacity int64, space, in, other string) error {
	return c.managedOn(ctx, name, capacity, space, in, other, nil)
}

// selecting applies a Managed WorkerPool as managed does, whose Pods
// select nodes of the architecture arch.
func (c *controllerSteps) selecting(ctx context.Context, name string, capacity int64, space, arch, in string) error {
	return c.managedOn(ctx, name, capacity, space, in, "", func(_, pod map[string]any) {
		pod["nodeSelector"] = map[string]any{corev1.LabelArchStable: arch}
	})
}

// managedOn applies a Managed WorkerPool, for another account when other
// is set, whose spec and pod settings change edits when it is set.
func (c *controllerSteps) managedOn(ctx context.Context, name string, capacity int64, space, in, other string, change func(spec, pod map[string]any)) error {
	ns := c.namespaces[in]
	_, key, err := newPoolKey()
	if err != nil {
		return err
	}
	if err := c.secret(ctx, ns, name+"-credentials", map[string][]byte{controller.PrivateKeyKey: key}); err != nil {
		return err
	}
	u, err := c.pool(ctx, name, ns, controller.Managed, capacity)
	if err != nil {
		return err
	}
	spec := u.Object["spec"].(map[string]any) //nolint:forcetypeassert // built above
	spec["space"], spec["name"] = c.w.Resolve("{"+space+"}"), "Controller "+name
	if other != "" {
		spec["account"] = kernel.NewAccountID().String()
	}
	if change != nil {
		change(spec, spec["pod"].(map[string]any)) //nolint:forcetypeassert // built by pool
	}
	return c.apply(ctx, u)
}

// existing applies an Existing WorkerPool over the pool of cert, whose
// credential Secret holds that pool's token and key.
func (c *controllerSteps) existing(ctx context.Context, name string, capacity int64, cert, key, space, in string) error {
	pc, ok := c.workers.certs[cert]
	signer, isEC := c.w.Keys[key].(*ecdsa.PrivateKey)
	if !ok || !isEC {
		return fmt.Errorf("%w: no certificate %q or P-256 key %q", harness.ErrStep, cert, key)
	}
	der, err := x509.MarshalECPrivateKey(signer)
	if err != nil {
		return fmt.Errorf("pool key: %w", err)
	}
	token, err := json.Marshal(map[string]any{
		"version": 1, "endpoint": c.w.Base("A"), "pool": pc.pool,
		"certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pc.cert.Raw})),
	})
	if err != nil {
		return fmt.Errorf("pool token: %w", err)
	}
	ns := c.namespaces[in]
	if err := c.secret(ctx, ns, name+"-credentials", map[string][]byte{
		controller.PrivateKeyKey: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}),
		controller.TokenKey:      []byte(base64.StdEncoding.EncodeToString(token)),
	}); err != nil {
		return err
	}
	u, err := c.pool(ctx, name, ns, controller.Existing, capacity)
	if err != nil {
		return err
	}
	spec := u.Object["spec"].(map[string]any) //nolint:forcetypeassert // built above
	spec["space"], spec["poolID"] = c.w.Resolve("{"+space+"}"), pc.pool
	return c.apply(ctx, u)
}

// find reads the WorkerPool name from whichever scenario namespace holds
// it.
func (c *controllerSteps) find(ctx context.Context, name string) (controller.WorkerPool, error) {
	for _, ns := range c.order {
		u, err := c.api.Resource(controller.PoolResource).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return controller.WorkerPool{}, fmt.Errorf("read WorkerPool %s: %w", name, err)
		}
		var wp controller.WorkerPool
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &wp); err != nil {
			return wp, fmt.Errorf("decode WorkerPool %s: %w", name, err)
		}
		return wp, nil
	}
	return controller.WorkerPool{}, fmt.Errorf("%w: no WorkerPool %q: %w", harness.ErrStep, name, errNoPool)
}

var errNoPool = errors.New("not found")

func (c *controllerSteps) slots(ctx context.Context, wp controller.WorkerPool) ([]controller.Worker, error) {
	list, err := c.api.Resource(controller.WorkerResource).Namespace(wp.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: controller.PoolLabel + "=" + string(wp.UID),
	})
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	out := make([]controller.Worker, 0, len(list.Items))
	for i := range list.Items {
		var w controller.Worker
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(list.Items[i].Object, &w); err != nil {
			return nil, fmt.Errorf("decode worker: %w", err)
		}
		out = append(out, w)
	}
	slices.SortFunc(out, func(a, b controller.Worker) int { return int(a.Spec.Slot - b.Spec.Slot) })
	return out, nil
}

func (c *controllerSteps) reports(ctx context.Context, name, typ, status, reason string) error {
	var last *metav1.Condition
	err := deps.Eventually(ctx, fmt.Sprintf("WorkerPool %s to report %s %s %s", name, typ, status, reason),
		func(ctx context.Context) (bool, error) {
			wp, err := c.find(ctx, name)
			if err != nil {
				return false, err
			}
			last = meta.FindStatusCondition(wp.Status.Conditions, typ)
			return last != nil && string(last.Status) == status && last.Reason == reason, nil
		})
	if err != nil {
		return fmt.Errorf("%w (last %+v)", err, last)
	}
	return nil
}

// registered waits until n of name's slots hold a registered worker, then
// keeps its pool's ID as pool and its workers' IDs.
func (c *controllerSteps) registered(ctx context.Context, name string, n int, pool string) error {
	var status controller.PoolStatus
	err := deps.Eventually(ctx, fmt.Sprintf("WorkerPool %s to register %d slots", name, n),
		func(ctx context.Context) (bool, error) {
			wp, err := c.find(ctx, name)
			if err != nil {
				return false, err
			}
			status = wp.Status
			if wp.Status.Registered != int32(n) || wp.Status.PoolID == "" { //nolint:gosec // a scenario's small count
				return false, nil
			}
			workers, err := c.slots(ctx, wp)
			if err != nil {
				return false, err
			}
			var ids []string
			for _, w := range workers {
				if w.Status.WorkerID != "" {
					ids = append(ids, w.Status.WorkerID)
				}
			}
			if len(ids) != n {
				return false, nil
			}
			c.kept[name], c.w.IDs[pool] = ids, wp.Status.PoolID
			return true, nil
		})
	if err != nil {
		return fmt.Errorf("%w (last status %+v)", err, status)
	}
	return nil
}

func (c *controllerSteps) same(ctx context.Context, name string) error {
	wp, err := c.find(ctx, name)
	if err != nil {
		return err
	}
	workers, err := c.slots(ctx, wp)
	if err != nil {
		return err
	}
	var ids []string
	for _, w := range workers {
		ids = append(ids, w.Status.WorkerID)
	}
	if !slices.Equal(ids, c.kept[name]) {
		return fmt.Errorf("%w: workers of %s are %v, kept %v", harness.ErrStep, name, ids, c.kept[name])
	}
	return nil
}

func (c *controllerSteps) distinct(ctx context.Context, a, b string) error {
	var pools, tokens []string
	for _, name := range []string{a, b} {
		wp, err := c.find(ctx, name)
		if err != nil {
			return err
		}
		s, err := c.core.Secrets(wp.Namespace).Get(ctx, wp.Spec.CredentialsRef.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read the credential Secret: %w", err)
		}
		pools, tokens = append(pools, wp.Status.PoolID), append(tokens, string(s.Data[controller.TokenKey]))
	}
	if pools[0] == "" || pools[0] == pools[1] || tokens[0] == "" || tokens[0] == tokens[1] {
		return fmt.Errorf("%w: %s and %s share a pool or token: %v", harness.ErrStep, a, b, pools)
	}
	return nil
}

func (c *controllerSteps) remove(ctx context.Context, name string) error {
	wp, err := c.find(ctx, name)
	if err != nil {
		return err
	}
	uid := wp.UID
	err = c.api.Resource(controller.PoolResource).Namespace(wp.Namespace).Delete(ctx, name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid},
	})
	if err != nil {
		return fmt.Errorf("delete WorkerPool %s: %w", name, err)
	}
	return nil
}

func (c *controllerSteps) gone(ctx context.Context, name string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "WorkerPool "+name+" to be gone", func(ctx context.Context) (bool, error) {
		_, err := c.find(ctx, name)
		if errors.Is(err, errNoPool) {
			return true, nil
		}
		return false, err
	})
}

func (c *controllerSteps) corrupt(ctx context.Context, name string) error {
	wp, err := c.find(ctx, name)
	if err != nil {
		return err
	}
	return c.secret(ctx, wp.Namespace, wp.Spec.CredentialsRef.Name, map[string][]byte{controller.PrivateKeyKey: []byte("not a key")})
}

// cleanup stops the scenario's controllers and its front, releases every
// finalizer left in its namespaces, and deletes them. A failed scenario
// leaves each controller's output and the run Pods it recorded beside
// its servers' evidence.
func (c *controllerSteps) cleanup(ctx context.Context, _ *godog.Scenario, failed error) (context.Context, error) {
	if c.api == nil {
		return ctx, nil
	}
	// A release's controllers finalize its pools as it uninstalls, so its
	// releases go before the controllers, front and namespaces they need.
	charts, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	result := c.uninstallCharts(charts)
	stop()
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	for _, srv := range c.running {
		result = errors.Join(result, srv.Stop(cleanup))
	}
	if failed != nil {
		result = errors.Join(result, c.evidence())
	}
	if c.front != nil {
		result = errors.Join(result, c.front.Shutdown(cleanup))
	} else if c.ln != nil {
		result = errors.Join(result, c.ln.Close())
	}
	if c.pods != nil {
		result = errors.Join(result, c.pods.Close(cleanup))
	}
	c.stopWatch()
	release := []byte(`{"metadata":{"finalizers":null}}`)
	for _, ns := range c.order {
		for _, gvr := range []struct {
			resource interface {
				List(context.Context, metav1.ListOptions) (*unstructured.UnstructuredList, error)
				Patch(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*unstructured.Unstructured, error)
			}
		}{{c.api.Resource(controller.WorkerResource).Namespace(ns)}, {c.api.Resource(controller.PoolResource).Namespace(ns)}} {
			list, err := gvr.resource.List(cleanup, metav1.ListOptions{})
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			for _, item := range list.Items {
				if _, err := gvr.resource.Patch(cleanup, item.GetName(), types.MergePatchType, release, metav1.PatchOptions{}); err != nil && !apierrors.IsNotFound(err) {
					result = errors.Join(result, err)
				}
			}
		}
		if err := c.core.Namespaces().Delete(cleanup, ns, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			result = errors.Join(result, err)
		}
	}
	return ctx, result
}
