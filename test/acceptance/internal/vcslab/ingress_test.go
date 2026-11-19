package vcslab

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestUnitLabIngressExposesOnlyCurrentWebhookReceiver(t *testing.T) {
	in, err := StartIngress(t.Context(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := in.Close(t.Context()); err != nil {
			t.Error(err)
		}
	})
	request := func(path string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+in.Address()+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("ingress status=%d want%d", r.StatusCode, want)
		}
	}
	request("/webhooks/vcs/gitlab/one", 503)
	request("/api/v1/stacks", 404)
	request("/webhooks/vcsevil/one", 404)
	r := chi.NewRouter()
	r.Post("/webhooks/vcs/gitlab/one", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(202) })
	srv := httptest.NewServer(r)
	defer srv.Close()
	if err := in.Attach(srv.URL); err != nil {
		t.Fatal(err)
	}
	request("/webhooks/vcs/gitlab/one", 202)
	for _, bad := range []string{"https://127.0.0.1:1", "http://provider.example.com:80", "http://127.0.0.1:1/api", "http://user:secret@127.0.0.1:1"} {
		if err := in.Attach(bad); err == nil {
			t.Fatal("unsafe ingress target accepted")
		}
	}
	request("/webhooks/vcs/gitlab/one", 202)
}
