package github

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestUnitGitHubRoutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, path        string
		read, deploy, setup bool
	}{
		{"GET", "/app", true, true, true},
		{"POST", "/app/installations/3/access_tokens", true, true, true},
		{"POST", "/app-manifests/code/conversions", false, false, true},
		{"GET", "/repos/acme/infra", true, true, true},
		{"GET", "/repos/acme/infra/commits/feature%2Fone", true, true, true},
		{"GET", "/repos/acme/infra/git/ref/tags%2Fv1", true, true, true},
		{"GET", "/repos/acme/infra/git/trees/feature%2Fone", true, true, true},
		{"GET", "/repos/acme/infra/compare/main...feature%2Fone", true, true, true},
		{"GET", "/repos/acme/infra/contents/infra/main.tf", true, true, true},
		{"GET", "/repos/acme/infra/hooks", false, false, false},
		{"GET", "/repos/acme/infra/deployments", true, true, true},
		{"GET", "/repos/acme/infra/deployments/7/statuses", true, true, true},
		{"POST", "/repos/acme/infra/deployments", false, true, false},
		{"POST", "/repos/acme/infra/deployments/7/statuses", false, true, false},
		{"HEAD", "/repos/acme/infra", false, false, false},
		{"DELETE", "/repos/acme/infra", false, false, false},
		{"PUT", "/repos/acme/infra/hooks/7", false, false, false},
		{"GET", "/repos/acme/infra/issues", false, false, false},
		{"GET", "/repos/acme/infra/contents/../private", false, false, false},
		{"GET", "/repos/acme/infra/contents/%2e%2e/private", false, false, false},
		{"GET", "/repos/acme/infra?x=y", false, false, false},
		{"GET", "https://example.com/app", false, false, false},
		{"GET", "/app#fragment", false, false, false},
		{"GET", "%zz", false, false, false},
		{"GET", "", false, false, false},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			t.Parallel()
			for purpose, want := range map[RoutePurpose]bool{ReadOnly: tc.read, DeploymentWrite: tc.deploy, AppSetup: tc.setup} {
				if got := AllowedFor(purpose, tc.method, tc.path); got != want {
					t.Errorf("purpose=%s got=%t want=%t", purpose, got, want)
				}
			}
			if got := Allowed(tc.method, tc.path); got != (tc.read || tc.deploy || tc.setup) {
				t.Error("all-purpose inventory differs")
			}
			if AllowedFor("unknown", tc.method, tc.path) {
				t.Error("unknown purpose accepted")
			}
		})
	}
}

func TestUnitGitHubRouteTransport(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	r := chi.NewRouter()
	r.HandleFunc("/api/v3/*", func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusNoContent) })
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	h := host{api: srv.URL + "/api/v3/"}
	for _, tc := range []struct {
		method, path string
		purpose      RoutePurpose
		ok           bool
	}{
		{"GET", "/api/v3/repos/acme/infra?per_page=100", ReadOnly, true},
		{"POST", "/api/v3/repos/acme/infra/deployments", ReadOnly, false},
		{"POST", "/api/v3/repos/acme/infra/deployments", DeploymentWrite, true},
		{"POST", "/api/v3/app/installations/3/access_tokens", allRoutes, true},
		{"POST", "/api/v3/app-manifests/code/conversions", AppSetup, true},
		{"GET", "/api/v30/repos/acme/infra", ReadOnly, false},
		{"GET", "/api/v3/repos/acme/infra/contents/%2e%2e/private", ReadOnly, false},
	} {
		req, err := http.NewRequestWithContext(t.Context(), tc.method, srv.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		before := calls.Load()
		resp, err := guarded(h, srv.Client().Transport, tc.purpose).RoundTrip(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if tc.ok {
			if err != nil || calls.Load() != before+1 {
				t.Fatalf("declared request refused: %s %v", tc.path, err)
			}
		} else if !errors.Is(err, ErrRoute) || calls.Load() != before {
			t.Fatalf("undeclared request escaped: %s %v", tc.path, err)
		}
	}
	for _, target := range []string{"http://example.com/api/v3/app", strings.Replace(srv.URL, "http:", "https:", 1) + "/api/v3/app"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		before := calls.Load()
		resp, err := guarded(h, srv.Client().Transport, ReadOnly).RoundTrip(req)
		if resp != nil {
			resp.Body.Close()
		}
		if !errors.Is(err, ErrRoute) || calls.Load() != before {
			t.Fatal("host/scheme scope bypassed", err)
		}
	}
}
