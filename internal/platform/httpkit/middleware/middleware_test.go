package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
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

// TestUnitLimitBodies checks that a Content-Length over the bound answers
// 413 before the handler runs, that a chunked body stops at the bound with
// an *http.MaxBytesError while one at the bound reads whole, and that an
// exempt request and one with no body pass untouched.
func TestUnitLimitBodies(t *testing.T) {
	var (
		read int
		err  error
	)
	h := RequestID(LimitBodies(8, func(r *http.Request) bool { return r.URL.Path == "/exempt" })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b []byte
			b, err = io.ReadAll(r.Body)
			read = len(b)
			w.WriteHeader(http.StatusNoContent)
		})))
	send := func(target, body string, chunked bool) *httptest.ResponseRecorder {
		read, err = -1, nil
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target,
			strings.NewReader(body))
		if chunked {
			r.ContentLength = -1
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	rec := send("/", "123456789", false)
	wantProblem(t, rec, http.StatusRequestEntityTooLarge, "at most 8 bytes")
	if read != -1 {
		t.Error("a Content-Length over the bound reached the handler")
	}
	send("/", "123456789", true)
	if mbe, ok := errors.AsType[*http.MaxBytesError](err); !ok || mbe.Limit != 8 {
		t.Errorf("a chunked body past the bound read %d bytes, %v", read, err)
	}
	for _, chunked := range []bool{false, true} {
		if rec := send("/", "12345678", chunked); rec.Code != http.StatusNoContent ||
			read != 8 || err != nil {
			t.Errorf("a body at the bound, chunked %v: %d, read %d, %v", chunked, rec.Code, read,
				err)
		}
	}
	if rec := send("/exempt", "123456789", false); rec.Code != http.StatusNoContent ||
		read != 9 || err != nil {
		t.Errorf("an exempt body: %d, read %d, %v", rec.Code, read, err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent || read != 0 {
		t.Errorf("no body: %d, read %d", rec.Code, read)
	}
}

// TestUnitUncacheable checks that an answer behind Uncacheable carries
// both marks, a panic's 500 from a router whose guards it leads included.
func TestUnitUncacheable(t *testing.T) {
	r := NewRouter(discard(), Uncacheable)
	r.Get("/ok", func(http.ResponseWriter, *http.Request) {})
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	for path, status := range map[string]int{
		"/ok": http.StatusOK, "/boom": http.StatusInternalServerError, "/nope": http.StatusNotFound,
	} {
		rec := serve(t, r, http.MethodGet, path)
		if rec.Code != status || rec.Header().Get("Cache-Control") != "no-store" ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %d %v", path, rec.Code, rec.Header())
		}
	}
}
