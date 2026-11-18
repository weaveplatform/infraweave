package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	vcshttp "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
)

type fallbackRoutes struct{ api.UnimplementedStrict }

type conditionalServer struct {
	fallbackRoutes
	vcshttp.Server
}

func TestUnitProviderConditionalRoutes(t *testing.T) {
	f, gh := newFixture(t), newGitHubFixture(t)
	for _, st := range []*store{f.store, gh.store} {
		for id, in := range st.rows {
			in.Version = 7
			st.rows[id] = in
		}
	}
	for _, tt := range []struct {
		path string
		srv  vcshttp.Server
		ctx  context.Context
	}{
		{"/gitlab-integrations/" + f.gitlab, f.srv, f.ctx},
		{"/bitbucket-cloud-integrations/" + f.cloud, f.srv, f.ctx},
		{"/bitbucket-datacenter-integrations/" + f.dc, f.srv, f.ctx},
		{"/azure-dev-ops-repo-integrations/" + f.azure, f.srv, f.ctx},
		{"/github-enterprise-integrations/" + gh.ghe, gh.srv, gh.ctx},
	} {
		t.Run(tt.path, func(t *testing.T) {
			h := api.Handler(api.NewStrictHandlerWithOptions(conditionalServer{Server: tt.srv}, nil,
				api.StrictHTTPServerOptions{ResponseErrorHandlerFunc: problem.ResponseError}))
			read := httptest.NewRecorder()
			h.ServeHTTP(read, httptest.NewRequestWithContext(tt.ctx, http.MethodGet, tt.path, nil))
			if read.Code != http.StatusOK || read.Header().Get("ETag") != `"7"` {
				t.Errorf("read = %d, ETag %q, want 200, ETag 7", read.Code, read.Header().Get("ETag"))
			}
			r := httptest.NewRequestWithContext(tt.ctx, http.MethodPatch, tt.path,
				strings.NewReader(`{"customInput":{"description":"stale"}}`))
			r.Header.Set("Content-Type", "application/merge-patch+json")
			r.Header.Set("If-Match", `"6"`)
			patched := httptest.NewRecorder()
			h.ServeHTTP(patched, r)
			if patched.Code != http.StatusPreconditionFailed {
				t.Errorf("stale patch = %d %s, want 412", patched.Code, patched.Body.String())
			}
		})
	}
}
