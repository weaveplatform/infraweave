package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
)

var param = regexp.MustCompile(`\{[^}]+\}`)

// wantTyped checks that rec is a problem of typ.
func wantTyped(t *testing.T, rec *httptest.ResponseRecorder, typ problem.Type) {
	t.Helper()
	var p problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not a problem: %v: %s", err, rec.Body)
	}
	if rec.Code != typ.Status || p.Type != problem.TypePrefix+typ.Slug || p.Title != typ.Title {
		t.Errorf("got %d %+v, want %s", rec.Code, p, typ.Slug)
	}
}

// TestUnitAPIOperations checks that every operation of the API is
// found at the pattern the generated router mounts it at, so each request
// draws from its operation's class rather than falling back to standard.
func TestUnitAPIOperations(t *testing.T) {
	ops := apiOperations()
	if len(ops) < 72 {
		t.Fatalf("%d operations, want every scaffolded one", len(ops))
	}
	for key, want := range map[string]Operation{
		"POST /api/v1/stacks/search":            {Cost: ratelimit.Expensive, Search: true},
		"POST /api/v1/stack-dependencies/batch": {Cost: ratelimit.Expensive},
		"GET /api/v1/stacks":                    {Cost: ratelimit.Standard},
		"POST /api/v1/stacks":                   {Cost: ratelimit.Standard},
	} {
		if ops[key] != want {
			t.Errorf("%s: %+v, want %+v", key, ops[key], want)
		}
	}
	mux, ok := API(discard(), config.Defaults().Server).(*chi.Mux)
	if !ok {
		t.Fatal("API is not a chi router")
	}
	for key := range ops {
		method, pattern, _ := strings.Cut(key, " ")
		path := param.ReplaceAllString(pattern, "x")
		if got := mux.Find(chi.NewRouteContext(), method, path); got != pattern {
			t.Errorf("%s %s routes to %q, want %q", method, path, got, pattern)
		}
	}
}

func TestUnitOperationsOf(t *testing.T) {
	doc := func(path string, op *openapi3.Operation) *openapi3.T {
		return &openapi3.T{
			Paths: openapi3.NewPaths(openapi3.WithPath(path, &openapi3.PathItem{Post: op})),
		}
	}
	ops, err := operationsOf(doc("/things/search", &openapi3.Operation{}), "/v1")
	if err != nil ||
		ops["POST /v1/things/search"] != (Operation{Cost: ratelimit.Standard, Search: true}) {
		t.Errorf("no cost and a /search path: %+v %v", ops, err)
	}
	ops, err = operationsOf(doc("/find", &openapi3.Operation{
		Extensions: map[string]any{"x-infraweave-cost": "expensive", "x-rest-style": "search"},
	}), "")
	if err != nil || ops["POST /find"] != (Operation{Cost: ratelimit.Expensive, Search: true}) {
		t.Errorf("x-rest-style search: %+v %v", ops, err)
	}
	for _, bad := range []any{"pricey", 3} {
		_, err := operationsOf(doc("/things", &openapi3.Operation{
			Extensions: map[string]any{"x-infraweave-cost": bad},
		}), "")
		if !errors.Is(err, errSpec) {
			t.Errorf("x-infraweave-cost %v: %v", bad, err)
		}
	}
}

func TestUnitRoutePath(t *testing.T) {
	for raw, want := range map[string]string{
		"/a/b":   "/a/b",
		"/a%2Fb": "/a%2Fb",
		"":       "/",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := routePath(&http.Request{URL: u}); got != want {
			t.Errorf("routePath(%q) = %q, want %q", raw, got, want)
		}
	}
}

func send(h http.Handler, method, target, remote, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader("{}"))
	r.RemoteAddr = remote
	if key != "" {
		r.Header.Set(idempotency.Header, key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestUnitAPIRateLimits(t *testing.T) {
	c := config.Defaults().Server
	c.RateLimit.Standard = ratelimit.Limit{Rate: 1e-6, Burst: 3}
	c.RateLimit.Expensive = ratelimit.Limit{Rate: 1e-6, Burst: 1}
	api := API(discard(), c)
	const me = "192.0.2.1:1"

	if rec := send(
		api,
		http.MethodPost,
		"/api/v1/stacks/search",
		me,
		"",
	); rec.Code != http.StatusNotImplemented ||
		rec.Header().Get("X-RateLimit-Limit") != "1" {
		t.Errorf("first search: %d %v", rec.Code, rec.Header())
	}
	wantTyped(t, send(api, http.MethodPost, "/api/v1/stacks/search", me, ""),
		problem.RateLimited)
	// The standard budget is separate, and a path no operation serves draws
	// from it too.
	if rec := send(
		api,
		http.MethodGet,
		"/api/v1/stacks",
		me,
		"",
	); rec.Code != http.StatusNotImplemented ||
		rec.Header().Get("X-RateLimit-Limit") != "3" ||
		rec.Header().Get("X-RateLimit-Remaining") != "2" {
		t.Errorf("standard after the search budget ran out: %d %v", rec.Code, rec.Header())
	}
	if rec := send(api, http.MethodGet, "/api/v1/nope", me, ""); rec.Code != http.StatusNotFound ||
		rec.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Errorf("404: %d %v", rec.Code, rec.Header())
	}
	// The admin listener's probes are never limited.
	admin := observe.Probes(discard())
	for range 5 {
		if rec := send(admin, http.MethodGet, "/livez", me, ""); rec.Code != http.StatusOK ||
			rec.Header().Get("X-RateLimit-Limit") != "" {
			t.Fatalf("/livez: %d %v", rec.Code, rec.Header())
		}
	}
}

func TestUnitAPIDisabledCaller(t *testing.T) {
	c := config.Defaults().Server
	c.RateLimit.DisabledCallers = []string{"192.0.2.1"}
	api := API(discard(), c)
	rec := send(api, http.MethodGet, "/api/v1/stacks", "192.0.2.1:1", "")
	wantTyped(t, rec, problem.APIDisabled)
	if rec.Header().Get("Retry-After") == "" || rec.Header().Get("X-RateLimit-Limit") != "" {
		t.Errorf("disabled caller's headers: %v", rec.Header())
	}
	if rec := send(
		api,
		http.MethodGet,
		"/api/v1/stacks",
		"198.51.100.7:1",
		"",
	); rec.Code != http.StatusNotImplemented {
		t.Errorf("another caller: %d", rec.Code)
	}
}

func TestUnitGuardedIdempotency(t *testing.T) {
	runs := 0
	r := Guarded(discard(), config.Defaults().Server, Operations{
		"POST /things":        {Cost: ratelimit.Standard},
		"POST /things/search": {Cost: ratelimit.Expensive, Search: true},
	})
	count := func(w http.ResponseWriter, _ *http.Request) {
		runs++
		w.WriteHeader(http.StatusCreated)
	}
	r.Post("/things", count)
	r.Post("/things/search", count)
	r.Post("/unlisted", count)
	const me = "192.0.2.1:1"
	for _, path := range []string{"/things", "/things/search", "/unlisted", "/nope"} {
		send(r, http.MethodPost, path, me, "k-"+path)
		send(r, http.MethodPost, path, me, "k-"+path)
	}
	// /things runs once; the search and the route no operation names run
	// twice each; /nope never reaches a handler.
	if runs != 5 {
		t.Errorf("handlers ran %d times, want 5", runs)
	}
	if rec := send(r, http.MethodPost, "/nope", me, "k-/nope"); rec.Code != http.StatusNotFound ||
		rec.Header().Get(idempotency.ReplayedHeader) != "" {
		t.Errorf("a 404 was stored: %d %v", rec.Code, rec.Header())
	}
}
