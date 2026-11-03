package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/server/problem"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
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
	if p.Status != status || p.Title != http.StatusText(status) ||
		!strings.Contains(p.Detail, detail) {
		t.Errorf("problem %+v, want status %d and a detail containing %q", p, status, detail)
	}
}

func TestUnitAPIRoutes(t *testing.T) {
	api := API(discard(), Defaults())
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
			wantProblem(t, serve(t, api, tc.method, tc.target), tc.status, tc.detail)
		})
	}
}

func TestUnitAdminRoutes(t *testing.T) {
	admin := Admin(discard())
	rec := serve(t, admin, http.MethodGet, "/livez")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Errorf("/livez: %d %q", rec.Code, rec.Body)
	}
	wantProblem(
		t,
		serve(t, admin, http.MethodGet, "/api/v1/stacks"),
		http.StatusNotFound,
		"/api/v1/stacks",
	)
	wantProblem(t, serve(t, admin, http.MethodPost, "/livez"), http.StatusMethodNotAllowed, "POST")
}

func TestUnitRecoverProblem(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	r := newRouter(log)
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("secret internal state") })
	rec := serve(t, r, http.MethodGet, "/boom")
	wantProblem(t, rec, http.StatusInternalServerError, "failed to handle the request")
	if strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("the panic value reached the client: %s", rec.Body)
	}
	if !strings.Contains(logs.String(), "secret internal state") ||
		!strings.Contains(logs.String(), "request_id=") {
		t.Errorf("the panic is not logged with its request ID: %s", logs.String())
	}
}

func TestUnitRecoverProblemAbort(t *testing.T) {
	r := newRouter(discard())
	r.Get("/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	defer func() {
		if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // recover returns the sentinel itself
			t.Errorf("recovered %v, want http.ErrAbortHandler re-raised", v)
		}
	}()
	serve(t, r, http.MethodGet, "/abort")
}
