package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
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
		"POST /api/v1/stacks/search":                       {Cost: ratelimit.Expensive, Search: true},
		"POST /api/v1/stack-dependencies/batch":            {Cost: ratelimit.Expensive},
		"GET /api/v1/stacks":                               {Cost: ratelimit.Standard},
		"POST /api/v1/stacks":                              {Cost: ratelimit.Standard},
		"DELETE /operator/v1/organizations/{organization}": {Cost: ratelimit.Standard},
		"GET /api/v1/viewer":                               {Cost: ratelimit.Standard, Organization: true},
		"GET /api/v1/auth/accounts":                        {Cost: ratelimit.Standard, Organization: true},
		"GET /admin-login/{organization}":                  {Public: true},
		"POST /admin-login/{organization}":                 {Cost: ratelimit.Expensive, Public: true},
	} {
		if ops[key] != want {
			t.Errorf("%s: %+v, want %+v", key, ops[key], want)
		}
	}
	mux, ok := API(discard(), config.Defaults().Server, memory(config.Defaults().Server), Domains{Auth: byAddress{}}, nil, Blobs{}).(*chi.Mux)
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
	ops, err = operationsOf(doc("/me", &openapi3.Operation{
		Extensions: map[string]any{"x-infraweave-access": "organization"},
	}), "")
	if err != nil || !ops["POST /me"].Organization {
		t.Errorf("x-infraweave-access organization: %+v %v", ops, err)
	}
	for _, bad := range []any{"account", true} {
		_, err := operationsOf(doc("/things", &openapi3.Operation{
			Extensions: map[string]any{"x-infraweave-access": bad},
		}), "")
		if !errors.Is(err, errSpec) {
			t.Errorf("x-infraweave-access %v: %v", bad, err)
		}
	}
}

func TestUnitOperationAccess(t *testing.T) {
	for op, want := range map[Operation]accessapi.Access{
		{}:                                 accessapi.Account,
		{Organization: true}:               accessapi.Organization,
		{Public: true}:                     accessapi.Public,
		{Public: true, Organization: true}: accessapi.Public,
	} {
		if got := op.access(); got != want {
			t.Errorf("%+v: %v, want %v", op, got, want)
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
	r := httptest.NewRequestWithContext(
		context.Background(),
		method,
		target,
		strings.NewReader("{}"),
	)
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
	api := API(discard(), c, memory(c), Domains{Auth: byAddress{}}, nil, Blobs{})
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
	admin := observe.Probes(discard(), observe.NewHealth(time.Now))
	for range 5 {
		if rec := send(admin, http.MethodGet, "/livez", me, ""); rec.Code != http.StatusOK ||
			rec.Header().Get("X-RateLimit-Limit") != "" {
			t.Fatalf("/livez: %d %v", rec.Code, rec.Header())
		}
	}
}

func TestUnitAPIDisabledCaller(t *testing.T) {
	c := config.Defaults().Server
	c.RateLimit.DisabledCallers = []string{addressCaller("192.0.2.1")}
	api := API(discard(), c, memory(c), Domains{Auth: byAddress{}}, nil, Blobs{})
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
	}, memory(config.Defaults().Server), byAddress{})
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

// TestUnitGuardedAuthentication checks the route classes: without an
// authenticator every route that needs a credential answers 401 with a
// Bearer challenge, a 404 included, and a public one passes, keyed on
// the client's address.
func TestUnitGuardedAuthentication(t *testing.T) {
	ops := apiOperations()
	if !ops["POST "+BaseURL+"/auth/token"].Public || ops["GET "+BaseURL+"/viewer"].Public ||
		ops["POST "+BaseURL+"/auth/logout"].Public {
		t.Fatalf("Public: token %v, viewer %v, logout %v", ops["POST "+BaseURL+"/auth/token"],
			ops["GET "+BaseURL+"/viewer"], ops["POST "+BaseURL+"/auth/logout"])
	}
	c := config.Defaults().Server
	c.RateLimit.DisabledCallers = []string{"192.0.2.1"}
	r := Guarded(discard(), c, Operations{
		"POST /open":   {Cost: ratelimit.Standard, Public: true},
		"GET /private": {Cost: ratelimit.Standard},
	}, memory(c), nil)
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	r.Post("/open", ok)
	r.Get("/private", ok)
	for _, path := range []string{"/private", "/nope"} {
		rec := send(r, http.MethodGet, path, "198.51.100.7:1", "")
		wantTyped(t, rec, problem.Unauthenticated)
		if rec.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: WWW-Authenticate %q", path, rec.Header().Get("WWW-Authenticate"))
		}
	}
	if rec := send(r, http.MethodPost, "/open", "198.51.100.7:1", ""); rec.Code != http.StatusOK {
		t.Errorf("a public route: %d %s", rec.Code, rec.Body)
	}
	// The public route's caller is the client's address.
	wantTyped(t, send(r, http.MethodPost, "/open", "192.0.2.1:1", ""), problem.APIDisabled)
}

// TestUnitStrictBodies checks that an operation whose body schema forbids
// unknown properties answers 400 invalid-request, before its handler, for
// an unknown field, a missing required one or a wrong type, and passes a
// good body with or without a Content-Type; a lenient operation's body is
// not checked. The served apiKeyCreate is strict.
func TestUnitStrictBodies(t *testing.T) {
	ops := apiOperations()
	if ops["POST "+BaseURL+"/api-keys"].Body == nil {
		t.Error("apiKeyCreate's body is not strict")
	}
	if ops["POST "+BaseURL+"/stacks/search"].Body != nil {
		t.Error("a lenient body is checked")
	}
	no := false
	strict := &openapi3.RequestBody{
		Required: true,
		Content: openapi3.NewContentWithJSONSchema(&openapi3.Schema{
			Type:                 &openapi3.Types{"object"},
			Required:             []string{"name"},
			Properties:           openapi3.Schemas{"name": openapi3.NewStringSchema().NewRef()},
			AdditionalProperties: openapi3.AdditionalProperties{Has: &no},
		}),
	}
	runs := 0
	r := Guarded(discard(), config.Defaults().Server, Operations{
		"POST /strict":  {Cost: ratelimit.Standard, Public: true, Body: strict},
		"POST /lenient": {Cost: ratelimit.Standard, Public: true},
	}, memory(config.Defaults().Server), nil)
	ok := func(w http.ResponseWriter, r *http.Request) {
		runs++
		w.WriteHeader(http.StatusOK)
	}
	r.Post("/strict", ok)
	r.Post("/lenient", ok)
	post := func(path, body, ct string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			path,
			strings.NewReader(body),
		)
		req.RemoteAddr = "192.0.2.1:1"
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	for name, body := range map[string]string{
		"unknown field": `{"name":"k","admin":false}`,
		"missing":       `{}`,
		"wrong type":    `{"name":7}`,
		"empty":         ``,
	} {
		rec := post("/strict", body, "application/json")
		wantTyped(t, rec, problem.InvalidRequest)
		if name == "unknown field" && !strings.Contains(rec.Body.String(), "admin") {
			t.Errorf("the unknown field's problem does not name it: %s", rec.Body)
		}
	}
	if runs != 0 {
		t.Fatalf("a refused body reached the handler %d times", runs)
	}
	for _, ct := range []string{"application/json", ""} {
		if rec := post("/strict", `{"name":"k"}`, ct); rec.Code != http.StatusOK {
			t.Errorf("a good body with Content-Type %q: %d %s", ct, rec.Code, rec.Body)
		}
	}
	if rec := post("/lenient", `{"anything":1}`, ""); rec.Code != http.StatusOK || runs != 3 {
		t.Errorf("a lenient body: %d, %d runs", rec.Code, runs)
	}
}
