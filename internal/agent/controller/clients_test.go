package controller_test

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
)

// This controlled API verifies transport/namespace paths only. The real lab
// owns CRD, authorization, controller and execution acceptance.
func TestUnitKubernetesClientsShareScopedAuthenticatedTransport(t *testing.T) {
	router := chi.NewRouter()
	responses := map[string]string{
		"/api/v1/namespaces/workers/secrets/credentials":                      `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"credentials","namespace":"workers"}}`,
		"/apis/coordination.k8s.io/v1/namespaces/workers/leases/controller":   `{"apiVersion":"coordination.k8s.io/v1","kind":"Lease","metadata":{"name":"controller","namespace":"workers"}}`,
		"/apis/workers.infraweave.io/v1alpha1/namespaces/workers/workerpools": `{"apiVersion":"workers.infraweave.io/v1alpha1","kind":"WorkerPoolList","items":[]}`,
	}
	router.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer controller-unit-credential" {
			t.Error("Kubernetes request lacks the configured identity")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, ok := responses[r.URL.Path]
		if !ok {
			t.Errorf("unexpected or unscoped Kubernetes path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	server := httptest.NewTLSServer(router)
	defer server.Close()
	config := &rest.Config{
		Host:            server.URL,
		BearerToken:     "controller-unit-credential",
		TLSClientConfig: rest.TLSClientConfig{CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})},
	}
	before := rest.CopyConfig(config)
	clients, err := controller.NewClients(config)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := clients.Core.Secrets("workers").Get(t.Context(), "credentials", metav1.GetOptions{})
	if err != nil || secret.Name != "credentials" || secret.Namespace != "workers" {
		t.Fatalf("scoped Secret read: %+v %v", secret, err)
	}
	lease, err := clients.Coordination.Leases("workers").Get(t.Context(), "controller", metav1.GetOptions{})
	if err != nil || lease.Name != "controller" {
		t.Fatalf("scoped Lease read: %+v %v", lease, err)
	}
	pools, err := clients.Dynamic.Resource(controller.PoolResource).Namespace("workers").List(t.Context(), metav1.ListOptions{})
	if err != nil || len(pools.Items) != 0 {
		t.Fatalf("scoped pool list: %+v %v", pools, err)
	}
	if !reflect.DeepEqual(config, before) {
		t.Fatal("client construction changed the caller's Kubernetes configuration")
	}
}

func TestUnitKubernetesClientsRefuseInvalidConfiguration(t *testing.T) {
	for _, config := range []*rest.Config{nil, {Host: "https://127.0.0.1", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("invalid CA")}}, {Host: "https://[invalid"}} {
		if _, err := controller.NewClients(config); err == nil {
			t.Fatal("invalid Kubernetes configuration accepted")
		}
	}
}
