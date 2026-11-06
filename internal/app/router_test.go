package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func request(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, nil))
	return rec
}

// wantProblem checks that rec is a problem detail with status and a detail
// containing detail.
func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, detail string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != problem.ContentType {
		t.Errorf("Content-Type %q, want %q", ct, problem.ContentType)
	}
	var p problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not a problem: %v: %s", err, rec.Body)
	}
	if want := problem.InstancePrefix + rec.Header().
		Get(problem.RequestIDHeader); p.Instance != want {
		t.Errorf("instance %q, want %q", p.Instance, want)
	}
	if p.Status != status || !registered(p) ||
		!strings.Contains(p.Detail, detail) {
		t.Errorf(
			"problem %+v, want a registered type of status %d and a detail containing %q",
			p,
			status,
			detail,
		)
	}
}

func TestUnitAPIRoutes(t *testing.T) {
	api := API(
		discard(),
		config.Defaults().Server,
		memory(config.Defaults().Server),
		Domains{Auth: byAddress{}},
		nil,
		Blobs{},
	)
	cases := []struct {
		name, method, target string
		status               int
		detail               string
	}{
		{
			"scaffolded operation",
			http.MethodGet,
			"/api/v1/stacks",
			http.StatusNotImplemented,
			"stacks (GET /stacks)",
		},
		{"unknown path", http.MethodGet, "/api/v1/nope", http.StatusNotFound, "/api/v1/nope"},
		{"wrong method", http.MethodPatch, "/api/v1/stacks", http.StatusMethodNotAllowed, "PATCH"},
		{"no probe on the API listener", http.MethodGet, "/livez", http.StatusNotFound, "/livez"},
		{"no prefix", http.MethodGet, "/stacks", http.StatusNotFound, "/stacks"},
		{
			"malformed parameter",
			http.MethodGet,
			"/api/v1/stacks?limit=many",
			http.StatusBadRequest,
			"limit",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantProblem(t, request(t, api, tc.method, tc.target), tc.status, tc.detail)
		})
	}
}

// registered reports whether p, but for the instance wantProblem checks,
// is exactly what its register type builds, so an untyped problem, or one
// whose title strays from the register, fails.
func registered(p problem.Problem) bool {
	return slices.ContainsFunc(
		problem.Types(),
		func(t problem.Type) bool {
			want := t.New(p.Detail)
			want.Instance = p.Instance
			return want == p
		},
	)
}

// TestUnitOperatorRoutes checks that each prefix authenticates with its
// own authenticator: the API's admits a request under /api/v1 alone, and
// with no operator authenticator every operator route answers 401.
func TestUnitOperatorRoutes(t *testing.T) {
	c := config.Defaults().Server
	refused := API(discard(), c, memory(c), Domains{Auth: byAddress{}}, nil, Blobs{})
	rec := request(t, refused, http.MethodGet, OperatorBaseURL+"/organizations")
	wantProblem(t, rec, http.StatusUnauthorized, "no valid credential")
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate %q", got)
	}
	wantProblem(
		t,
		request(t, refused, http.MethodGet, "/api/v1/stacks"),
		http.StatusNotImplemented,
		"stacks",
	)

	admitted := API(discard(), c, memory(c), Domains{OperatorAuth: byAddress{}}, nil, Blobs{})
	wantProblem(
		t,
		request(t, admitted, http.MethodGet, OperatorBaseURL+"/nope"),
		http.StatusNotFound,
		"nope",
	)
	wantProblem(
		t,
		request(t, admitted, http.MethodGet, "/api/v1/stacks"),
		http.StatusUnauthorized,
		"no valid credential",
	)
	// /operator/v10 is not under the operator prefix.
	wantProblem(
		t,
		request(t, admitted, http.MethodGet, "/operator/v10"),
		http.StatusUnauthorized,
		"no valid credential",
	)
}
