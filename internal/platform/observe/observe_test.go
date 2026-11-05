package observe

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
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

func TestUnitProbes(t *testing.T) {
	admin := Probes(discard())
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

// registered reports whether p is exactly what its register type builds, so
// an untyped problem, or one whose title strays from the register, fails.
func registered(p problem.Problem) bool {
	return slices.ContainsFunc(
		problem.Types(),
		func(t problem.Type) bool { return t.New(p.Detail) == p },
	)
}
