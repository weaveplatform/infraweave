package vcslab

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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

func TestUnitLabIngressRejectsCanonicalizationAndPreservesSignedBytes(t *testing.T) {
	var calls atomic.Int32
	body := "{\"text\":\"line\\n\"}\r\n"
	h := hmac.New(sha256.New, []byte("lab-only-signature-key-at-least-32-bytes"))
	_, _ = h.Write([]byte(body))
	signature := "sha256=" + hex.EncodeToString(h.Sum(nil))
	up := chi.NewRouter()
	up.Use(middleware.CleanPath)
	up.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		got, err := io.ReadAll(r.Body)
		if err != nil || string(got) != body || r.Header.Get("X-Hub-Signature-256") != signature {
			t.Error("signed bytes or signature header changed")
		}
		w.WriteHeader(202)
	}))
	srv := httptest.NewServer(up)
	defer srv.Close()
	in, err := StartIngress(t.Context(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := in.Close(t.Context()); err != nil {
			t.Error(err)
		}
	})
	if err := in.Attach(srv.URL); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/webhooks/vcs/gitlab/one"},
		{"PUT", "/webhooks/vcs/gitlab/one"},
		{"POST", "/webhooks/vcs/../../api/v1/stacks"},
		{"POST", "/webhooks/vcs/../admin"},
		{"POST", "/webhooks/vcs/%2e%2e/%2e%2e/api/v1/stacks"},
		{"POST", "/webhooks/vcs/%252e%252e/%252e%252e/api/v1/stacks"},
		{"POST", "/webhooks/vcs/gitlab%2fone"},
		{"POST", "/webhooks/vcs/gitlab//one"},
		{"POST", "/webhooks/vcs/gitlab/one/../../admin"},
		{"POST", "/webhooks/vcs/gitlab/%6fne"},
	} {
		req, err := http.NewRequestWithContext(t.Context(), tc.method, "http://"+in.Address()+tc.path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatalf("noncanonical ingress status=%d", r.StatusCode)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("upstream canonicalizer saw an invalid lab ingress request")
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+in.Address()+"/webhooks/vcs/github_enterprise/one", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Hub-Signature-256", signature)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != 202 || calls.Load() != 1 {
		t.Fatal("canonical signed delivery did not reach exactly one receiver")
	}
}
