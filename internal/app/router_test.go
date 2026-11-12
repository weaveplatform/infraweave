package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
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
		Platform{},
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
			"/api/v1/stacks/infra/attachable-policies",
			http.StatusNotImplemented,
			"stackAttachablePolicies (GET /stacks/{stackId}/attachable-policies)",
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
			return reflect.DeepEqual(want, p)
		},
	)
}

// TestUnitOperatorRoutes checks that each prefix authenticates with its
// own authenticator: the API's admits a request under /api/v1 alone, and
// with no operator authenticator every operator route answers 401.
func TestUnitOperatorRoutes(t *testing.T) {
	c := config.Defaults().Server
	refused := API(discard(), c, memory(c), Domains{Auth: byAddress{}}, Platform{}, Blobs{})
	rec := request(t, refused, http.MethodGet, OperatorBaseURL+"/organizations")
	wantProblem(t, rec, http.StatusUnauthorized, "no valid credential")
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate %q", got)
	}
	wantProblem(
		t,
		request(t, refused, http.MethodGet, "/api/v1/stacks/infra/attachable-policies"),
		http.StatusNotImplemented,
		"stackAttachablePolicies",
	)

	admitted := API(
		discard(),
		c,
		memory(c),
		Domains{OperatorAuth: byAddress{}},
		Platform{},
		Blobs{},
	)
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

// TestUnitWorkerRoutes checks that the worker API stands behind the
// controls of every surface with its own authenticator: with none, a
// credential the API's authenticator admits answers 401 there, while the
// public registration reaches its handler past the strict body check,
// and /worker/v10 is not under the worker API.
func TestUnitWorkerRoutes(t *testing.T) {
	c := config.Defaults().Server
	h := API(discard(), c, memory(c), Domains{Auth: byAddress{}}, Platform{}, Blobs{})
	rec := request(t, h, http.MethodPost, WorkerBaseURL+"/poll")
	wantProblem(t, rec, http.StatusUnauthorized, "no valid credential")
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate %q", got)
	}
	if rec.Header().Get("X-RateLimit-Limit") != "" {
		t.Error("a refused credential drew from a bucket")
	}
	send := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			WorkerBaseURL+"/register", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}
	rec = send(`{"registration":"x"}`)
	wantProblem(t, rec, http.StatusNotImplemented, "workerRegister")
	if rec.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("the registration drew from no bucket")
	}
	long := `{"registration":"` + strings.Repeat("x", 32769) + `"}`
	for _, body := range []string{`{"registration":"x","colour":"red"}`, `{}`, long} {
		var p problem.Problem
		rec := send(body)
		if json.Unmarshal(rec.Body.Bytes(), &p) != nil || rec.Code != http.StatusBadRequest ||
			p.Type != problem.TypePrefix+problem.InvalidRequest.Slug {
			t.Errorf("%s answered %d %s", body, rec.Code, rec.Body)
		}
	}
	wantProblem(t, request(t, h, http.MethodPost, "/worker/v10/poll"), http.StatusNotFound,
		"/worker/v10/poll")
	if op := apiOperations()[http.MethodPost+" "+WorkerBaseURL+"/poll"]; !op.Worker ||
		op.access() != accessapi.Worker || op.Body == nil || op.Required != nil ||
		op.Gate.Feature != "" {
		t.Errorf("the poll's operation is %+v", op)
	}
	if op := apiOperations()[http.MethodGet+" "+WorkerBaseURL+"/runs/{run}/spec"]; !op.Worker ||
		!op.Runner || op.access() != accessapi.Runner || op.Required != nil {
		t.Errorf("the spec's operation is %+v", op)
	}
	rec = request(t, h, http.MethodGet, WorkerBaseURL+"/runs/"+kernel.NewULID().String()+"/spec")
	wantProblem(t, rec, http.StatusUnauthorized, "no valid credential")
}
