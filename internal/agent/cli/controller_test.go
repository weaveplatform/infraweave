package cli_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"k8s.io/client-go/rest"

	"github.com/weaveplatform/infraweave/internal/agent/cli"
)

const bootstrapImage = "ghcr.io/weaveplatform/infraweave@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func controllerEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_CLUSTER_ID", "cluster")
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_WATCH_NAMESPACES", "workers")
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_LEADER_ELECTION_NAMESPACE", "workers")
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_BOOTSTRAP_IMAGE", bootstrapImage)
}

func TestUnitWorkerControllerRefusesBeforeItStarts(t *testing.T) {
	errKube := errors.New("no cluster")
	kube := func() (*rest.Config, error) { return nil, errKube }
	for name, setup := range map[string]func(t *testing.T) []string{
		"invalid configuration": func(*testing.T) []string { return nil },
		"invalid log level": func(t *testing.T) []string {
			controllerEnvironment(t)
			return []string{"--log-level", "loud"}
		},
		"no Kubernetes configuration": func(t *testing.T) []string {
			controllerEnvironment(t)
			return nil
		},
		"an unreadable ca_file": func(t *testing.T) []string {
			controllerEnvironment(t)
			t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_CA_FILE", filepath.Join(t.TempDir(), "absent.pem"))
			kube = func() (*rest.Config, error) { return &rest.Config{Host: "http://127.0.0.1:1"}, nil }
			return nil
		},
		"an unbindable admin listener": func(t *testing.T) []string {
			controllerEnvironment(t)
			kube = func() (*rest.Config, error) { return &rest.Config{Host: "http://127.0.0.1:1"}, nil }
			return []string{"--admin-listen", "256.0.0.1:0"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			kube = func() (*rest.Config, error) { return nil, errKube }
			args := setup(t)
			cmd := cli.WorkerControllerCmd(func() (*rest.Config, error) { return kube() })
			cmd.SetArgs(args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if err := cmd.ExecuteContext(t.Context()); err == nil {
				t.Fatal("the controller started")
			}
		})
	}
}

func TestUnitWorkerControllerRunsUntilItsContextEnds(t *testing.T) {
	controllerEnvironment(t)
	api := httptest.NewServer(http.NotFoundHandler())
	defer api.Close()
	cmd := cli.WorkerControllerCmd(func() (*rest.Config, error) { return &rest.Config{Host: api.URL}, nil })
	cmd.SetArgs([]string{"--admin-listen", "127.0.0.1:0"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if !strings.Contains(out.String(), `"msg":"worker controller starting"`) || !strings.Contains(out.String(), `"fips140":`) {
		t.Fatalf("the controller did not log its build: %s", out.String())
	}
}

func TestUnitWorkerControllerUninstallWaitsForItsPools(t *testing.T) {
	controllerEnvironment(t)
	r := chi.NewRouter()
	r.Get("/apis/workers.infraweave.io/v1alpha1/namespaces/workers/workerpools", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"workers.infraweave.io/v1alpha1","kind":"WorkerPoolList","metadata":{},"items":[]}`))
	})
	api := httptest.NewServer(r)
	defer api.Close()
	kube := func() (*rest.Config, error) { return &rest.Config{Host: api.URL}, nil }
	for name, tc := range map[string]struct {
		args []string
		kube func() (*rest.Config, error)
		ok   bool
	}{
		"no pools left": {[]string{"uninstall", "--selector", "app.kubernetes.io/instance=alpha"}, kube, true},
		"no selector":   {[]string{"uninstall"}, kube, false},
		"no Kubernetes": {[]string{"uninstall", "--selector", "a=b"}, func() (*rest.Config, error) { return nil, errors.New("no cluster") }, false},
		"no Kubernetes client": {[]string{"uninstall", "--selector", "a=b"}, func() (*rest.Config, error) {
			// A CA that holds no certificate is a transport client-go refuses.
			return &rest.Config{Host: "https://kubernetes.invalid", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("not a certificate")}}, nil
		}, false},
		"an invalid configuration": {[]string{"uninstall", "--selector", "a=b", "--log-level", "loud"}, kube, false},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := cli.WorkerControllerCmd(tc.kube)
			cmd.SetArgs(tc.args)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			if err := cmd.ExecuteContext(t.Context()); (err == nil) != tc.ok {
				t.Fatalf("uninstall = %v: %s", err, out.String())
			}
		})
	}
	t.Setenv("INFRAWEAVE_WORKER_CONTROLLER_WATCH_NAMESPACES", "")
	cmd := cli.WorkerControllerCmd(kube)
	cmd.SetArgs([]string{"uninstall", "--selector", "a=b"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.ExecuteContext(t.Context()); err == nil {
		t.Fatal("an uninstallation without watch namespaces ran")
	}
}

func TestUnitWorkerControllerReadsKubeconfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(`apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://127.0.0.1:6443"}}]
users: [{name: u, user: {token: t}}]
contexts: [{name: c, context: {cluster: c, user: u}}]
current-context: c
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	c, err := cli.KubeConfig()
	if err != nil || c.Host != "https://127.0.0.1:6443" {
		t.Fatalf("kubeconfig = %+v, %v", c, err)
	}
	if err := os.WriteFile(path, []byte("not: [yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.KubeConfig(); err == nil {
		t.Fatal("a broken kubeconfig was read")
	}
	t.Setenv("KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if _, err := cli.KubeConfig(); err == nil {
		t.Fatal("an in-cluster configuration was found outside a cluster")
	}
}

func TestUnitWorkerControllerIsNamed(t *testing.T) {
	if cli.WorkerController().Name() != "worker-controller" {
		t.Fatal("the worker controller command is misnamed")
	}
}
