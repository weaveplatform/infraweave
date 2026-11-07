package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestUnitNewRouterProblems(t *testing.T) {
	r := NewRouter(discard())
	r.Get("/thing", func(http.ResponseWriter, *http.Request) {})
	wantProblem(t, serve(t, r, http.MethodGet, "/nope"), http.StatusNotFound, "/nope")
	wantProblem(t, serve(t, r, http.MethodPost, "/thing"), http.StatusMethodNotAllowed, "POST")
}

func TestUnitRecoverProblem(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	r := NewRouter(log)
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
	r := NewRouter(discard())
	r.Get("/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	defer func() {
		if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // recover returns the sentinel itself
			t.Errorf("recovered %v, want http.ErrAbortHandler re-raised", v)
		}
	}()
	serve(t, r, http.MethodGet, "/abort")
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
