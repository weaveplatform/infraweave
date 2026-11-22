package deps

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
)

func fixtureKubernetes(t *testing.T) (*labManifest, map[string]any, *httptest.Server) {
	t.Helper()
	m := fixtureLab(t)
	m.Kubernetes = &labKubernetes{
		Container:  strings.Repeat("d", 64),
		Namespace:  labKubernetesNamespace(m.Owner),
		Kubeconfig: filepath.Join(filepath.Dir(m.Gitea.CAFile), "kubernetes.json"),
	}
	router := chi.NewRouter()
	router.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "ok") })
	router.Get("/api/v1/nodes", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"items":[{"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`)
	})
	router.Get("/api/v1/namespaces/"+m.Kubernetes.Namespace, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).
			Encode(map[string]any{"metadata": map[string]any{"name": m.Kubernetes.Namespace, "annotations": map[string]string{"io.infraweave.lab.owner": m.Owner}}})
	})
	server := httptest.NewTLSServer(router)
	t.Cleanup(server.Close)
	m.Kubernetes.Endpoint = server.URL
	certificate := server.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	b64 := func(kind string, data []byte) string {
		return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}))
	}
	config := map[string]any{
		"current-context": "default",
		"clusters": []any{
			map[string]any{"name": "default", "cluster": map[string]any{"server": server.URL, "certificate-authority-data": b64("CERTIFICATE", certificate.Certificate[0])}},
		},
		"users": []any{
			map[string]any{
				"name": "default",
				"user": map[string]any{"client-certificate-data": b64("CERTIFICATE", certificate.Certificate[0]), "client-key-data": b64("PRIVATE KEY", key)},
			},
		},
		"contexts": []any{map[string]any{"name": "default", "context": map[string]any{"cluster": "default", "user": "default", "namespace": m.Kubernetes.Namespace}}},
	}
	return m, config, server
}

func writeKubernetes(t *testing.T, m *labManifest, config map[string]any) {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Kubernetes.Kubeconfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUnitKubernetesConfigurationAndLiveTLSReadiness(t *testing.T) {
	m, config, server := fixtureKubernetes(t)
	writeKubernetes(t, m, config)
	raw, _ := json.Marshal(m)
	if _, err := readLab(raw); err != nil {
		t.Fatal(err)
	}
	cluster, err := labKubernetesClient(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := cluster.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	d := &Dependencies{lab: m}
	if _, err := d.Kubernetes(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if _, err := d.Kubernetes(t.Context()); !errors.Is(err, ErrLab) {
		t.Fatalf("unreachable cluster accepted: %v", err)
	}
	if _, err := (&Dependencies{}).Kubernetes(t.Context()); !errors.Is(err, ErrLab) {
		t.Fatalf("missing cluster replaced: %v", err)
	}
}

func TestUnitKubernetesReadinessRejectsRedirectWithoutContactingTarget(t *testing.T) {
	var targetCalls atomic.Int64
	targetRouter := chi.NewRouter()
	targetRouter.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		_, _ = fmt.Fprint(w, "ok")
	})
	target := httptest.NewTLSServer(targetRouter)
	t.Cleanup(target.Close)
	m, config, source := fixtureKubernetes(t)
	router := chi.NewRouter()
	router.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/readyz", http.StatusTemporaryRedirect)
	})
	source.Config.Handler = router
	writeKubernetes(t, m, config)
	cluster, err := labKubernetesClient(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := cluster.Ready(t.Context()); !errors.Is(err, ErrLab) || !strings.Contains(err.Error(), "status 307") {
		t.Fatalf("readiness accepted or followed a redirect: %v", err)
	}
	if calls := targetCalls.Load(); calls != 0 {
		t.Fatalf("readiness contacted redirect target %d times", calls)
	}
}

func TestUnitKubernetesRejectsForeignConfiguration(t *testing.T) {
	for _, bad := range []string{"server", "insecure", "context", "namespace", "exec", "key", "ca", "malformed", "oversized", "missing"} {
		t.Run(bad, func(t *testing.T) {
			m, config, _ := fixtureKubernetes(t)
			c := config["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)
			u := config["users"].([]any)[0].(map[string]any)["user"].(map[string]any)
			switch bad {
			case "server":
				c["server"] = "https://127.0.0.1:1"
			case "insecure":
				c["insecure-skip-tls-verify"] = true
			case "context":
				config["current-context"] = "unrelated"
			case "namespace":
				config["contexts"].([]any)[0].(map[string]any)["context"].(map[string]any)["namespace"] = "default"
			case "exec":
				u["exec"] = map[string]string{"command": "secret-command"}
			case "key":
				u["client-key-data"] = "not-base64-secret"
			case "ca":
				c["certificate-authority-data"] = "c2VjcmV0"
			}
			writeKubernetes(t, m, config)
			if bad == "malformed" || bad == "oversized" {
				data := []byte("not-json-secret")
				if bad == "oversized" {
					data = make([]byte, (64<<10)+1)
				}
				if err := os.WriteFile(m.Kubernetes.Kubeconfig, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if bad == "missing" {
				if err := os.Remove(m.Kubernetes.Kubeconfig); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := labKubernetesClient(m); !errors.Is(err, ErrLab) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe configuration accepted/echoed: %v", err)
			}
		})
	}
}

func TestUnitKubernetesManifestRefusesForeignIdentity(t *testing.T) {
	for _, bad := range []string{"container", "endpoint", "fragment", "namespace", "path"} {
		t.Run(bad, func(t *testing.T) {
			m, _, _ := fixtureKubernetes(t)
			switch bad {
			case "container":
				m.Kubernetes.Container = "another-owner"
			case "endpoint":
				m.Kubernetes.Endpoint = "https://example.com:6443"
			case "fragment":
				m.Kubernetes.Endpoint += "#secret"
			case "namespace":
				m.Kubernetes.Namespace = "default"
			case "path":
				m.Kubernetes.Kubeconfig = "/outside/private.json"
			}
			raw, _ := json.Marshal(m)
			if _, err := readLab(raw); !errors.Is(err, ErrLab) {
				t.Fatalf("foreign cluster manifest accepted: %v", err)
			}
		})
	}
}
