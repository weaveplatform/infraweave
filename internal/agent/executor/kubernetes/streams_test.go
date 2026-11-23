package kubernetes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestUnitStreamsReachThePodsSubresources(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		http.Error(w, "streams are refused", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	config := &rest.Config{Host: srv.URL}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStreams(config, client.CoreV1().RESTClient())
	if err := s.Attach(t.Context(), ns, "p", RunnerContainer, strings.NewReader("in"), &tail{max: 1}, nil); err == nil {
		t.Fatal("attached through a refusing server")
	}
	if err := s.Exec(t.Context(), ns, "p", RunnerContainer, SignalCommand, &tail{max: 1}, &tail{max: 1}); err == nil {
		t.Fatal("ran a command through a refusing server")
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(paths, "\n")
	for _, want := range []string{"/api/v1/namespaces/workers/pods/p/attach", "container=runner", "/pods/p/exec", "command=signal"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests %s lack %s", joined, want)
		}
	}
}
