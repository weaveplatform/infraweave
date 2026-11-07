package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
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
		"POST /api/v1/stacks/search":                       {Cost: ratelimit.Expensive, Search: true, Gate: gate("stacks")},
		"POST /api/v1/stack-dependencies/batch":            {Cost: ratelimit.Expensive, Gate: gate("stacks")},
		"GET /api/v1/stacks":                               {Cost: ratelimit.Standard, Gate: gate("stacks")},
		"POST /api/v1/stacks":                              {Cost: ratelimit.Standard, Gate: gate("stacks")},
		"DELETE /operator/v1/organizations/{organization}": {Cost: ratelimit.Standard, Gate: gate("operator")},
		"GET /api/v1/viewer":                               {Cost: ratelimit.Standard, Organization: true, Gate: gate("viewer")},
		"GET /api/v1/auth/accounts":                        {Cost: ratelimit.Standard, Organization: true, Gate: gate("auth")},
		"GET /admin-login/{organization}":                  {Public: true},
		"POST /admin-login/{organization}":                 {Cost: ratelimit.Expensive, Public: true},
		"GET /blobs/{token}":                               {Public: true},
		"PUT /blobs/{token}":                               {Public: true},
	} {
		if !reflect.DeepEqual(ops[key], want) {
			t.Errorf("%s: %+v, want %+v", key, ops[key], want)
		}
	}
	for key, op := range ops {
		f, known := features.Lookup(op.Gate.Feature)
		switch {
		case strings.HasSuffix(key, " "+accessapi.LoginPattern),
			strings.HasSuffix(key, " "+fs.Pattern):
			if op.Gate.Feature != "" {
				t.Errorf("%s names %s; a route outside the API documents names none",
					key, op.Gate.Feature)
			}
		case !known || f.Kind != features.Capability:
			t.Errorf("%s names %q, not a capability of the catalogue", key, op.Gate.Feature)
		case strings.Contains(key, " "+OperatorBaseURL+"/"):
			if op.Gate.Feature != features.Operator {
				t.Errorf("%s names %s, not operator", key, op.Gate.Feature)
			}
		}
	}
	if g := ops["POST "+BaseURL+"/api-keys"].Gate; !reflect.DeepEqual(g.Options,
		[]featuresapi.OptionGate{{Property: "oidc", Feature: "oidc-api-keys"}}) {
		t.Errorf("apiKeyCreate's options: %+v", g.Options)
	}
	// The fs driver's routes are mounted, so a table pattern the driver
	// does not serve fails the walk below.
	mux, ok := API(discard(), config.Defaults().Server, memory(config.Defaults().Server),
		Domains{Auth: byAddress{}}, Platform{}, Blobs{Routes: (&fs.Bucket{}).Routes}).(*chi.Mux)
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
		if op.Extensions == nil {
			op.Extensions = map[string]any{}
		}
		if _, ok := op.Extensions["x-infraweave-feature"]; !ok {
			op.Extensions["x-infraweave-feature"] = "stacks"
		}
		return &openapi3.T{
			Paths: openapi3.NewPaths(openapi3.WithPath(path, &openapi3.PathItem{Post: op})),
		}
	}
	ops, err := operationsOf(doc("/things/search", &openapi3.Operation{}), "/v1")
	if err != nil ||
		!reflect.DeepEqual(ops["POST /v1/things/search"],
			Operation{Cost: ratelimit.Standard, Search: true, Gate: gate("stacks")}) {
		t.Errorf("no cost and a /search path: %+v %v", ops, err)
	}
	ops, err = operationsOf(doc("/find", &openapi3.Operation{
		Extensions: map[string]any{"x-infraweave-cost": "expensive", "x-rest-style": "search"},
	}), "")
	if err != nil || !reflect.DeepEqual(ops["POST /find"],
		Operation{Cost: ratelimit.Expensive, Search: true, Gate: gate("stacks")}) {
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
	for _, bad := range []any{"", "SCANS", "seats", "oidc-api-keys", 3} {
		_, err := operationsOf(doc("/things", &openapi3.Operation{
			Extensions: map[string]any{"x-infraweave-feature": bad},
		}), "")
		if !errors.Is(err, errSpec) || !errors.Is(err, errFeature) {
			t.Errorf("x-infraweave-feature %v: %v", bad, err)
		}
	}
}

// TestUnitOperationOptions checks that a body property's
// x-infraweave-feature, beside its $ref or in its schema, is an option
// gate on that property, and that a name of another kind is refused.
func TestUnitOperationOptions(t *testing.T) {
	op := func(props openapi3.Schemas) *openapi3.T {
		body := openapi3.NewRequestBody().WithJSONSchema(&openapi3.Schema{Properties: props})
		return &openapi3.T{Paths: openapi3.NewPaths(openapi3.WithPath("/keys", &openapi3.PathItem{
			Post: &openapi3.Operation{
				Extensions:  map[string]any{"x-infraweave-feature": "api-keys"},
				RequestBody: &openapi3.RequestBodyRef{Value: body},
			},
		}))}
	}
	beside := openapi3.NewObjectSchema().NewRef()
	beside.Extensions = map[string]any{
		"x-infraweave-feature": map[string]any{"feature": "oidc-api-keys"},
	}
	inside := openapi3.NewStringSchema()
	inside.Extensions = map[string]any{"x-infraweave-feature": map[string]any{
		"feature": "aggregated-vcs-checks", "value": "AGGREGATED",
	}}
	ops, err := operationsOf(op(openapi3.Schemas{
		"oidc": beside, "vcsChecks": inside.NewRef(), "name": openapi3.NewStringSchema().NewRef(),
	}), "")
	want := []featuresapi.OptionGate{
		{Property: "oidc", Feature: "oidc-api-keys"},
		{Property: "vcsChecks", Value: "AGGREGATED", Feature: "aggregated-vcs-checks"},
	}
	if err != nil || !reflect.DeepEqual(ops["POST /keys"].Gate.Options, want) {
		t.Errorf("options = %+v, %v", ops["POST /keys"].Gate, err)
	}
	for _, bad := range []string{"stacks", "SCANS", ""} {
		ref := openapi3.NewObjectSchema().NewRef()
		ref.Extensions = map[string]any{"x-infraweave-feature": map[string]any{"feature": bad}}
		if _, err := operationsOf(
			op(openapi3.Schemas{"oidc": ref}),
			"",
		); !errors.Is(
			err,
			errFeature,
		) {
			t.Errorf("option %q: %v", bad, err)
		}
	}
	noBody := &openapi3.T{Paths: openapi3.NewPaths(openapi3.WithPath("/x", &openapi3.PathItem{
		Post: &openapi3.Operation{
			Extensions:  map[string]any{"x-infraweave-feature": "stacks"},
			RequestBody: &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody()},
		},
	}))}
	if ops, err := operationsOf(noBody, ""); err != nil || ops["POST /x"].Gate.Options != nil {
		t.Errorf("a body with no JSON schema: %+v, %v", ops, err)
	}
}

func TestUnitOperationAccess(t *testing.T) {
	for _, c := range []struct {
		op   Operation
		want accessapi.Access
	}{
		{Operation{}, accessapi.Account},
		{Operation{Organization: true}, accessapi.Organization},
		{Operation{Public: true}, accessapi.Public},
		{Operation{Public: true, Organization: true}, accessapi.Public},
	} {
		if got := c.op.access(); got != c.want {
			t.Errorf("%+v: %v, want %v", c.op, got, c.want)
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
	api := API(discard(), c, memory(c), Domains{Auth: byAddress{}}, Platform{}, Blobs{})
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
	api := API(discard(), c, memory(c), Domains{Auth: byAddress{}}, Platform{}, Blobs{})
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
	}, memory(config.Defaults().Server), byAddress{}, nil)
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
	}, memory(c), nil, nil)
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

// TestUnitSignedURLRoutes checks that the fs driver's routes take no
// bearer credential: with every credential refused, a token that does not
// verify reaches the driver and answers its 403, while a route the API
// does not serve still answers 401.
func TestUnitSignedURLRoutes(t *testing.T) {
	c := config.Defaults().Server
	h := API(discard(), c, memory(c), Domains{}, Platform{}, Blobs{Routes: (&fs.Bucket{}).Routes})
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec := send(h, method, "/blobs/not-a-token", "198.51.100.7:1", "")
		wantTyped(t, rec, problem.Forbidden)
		if rec.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("%s: a Bearer challenge on a signed URL", method)
		}
	}
	wantTyped(t, send(h, http.MethodGet, "/blobs", "198.51.100.7:1", ""), problem.Unauthenticated)
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
	}, memory(config.Defaults().Server), nil, nil)
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

// gate is the gate of an operation that needs the capability feature only.
func gate(feature string) featuresapi.Gate { return featuresapi.Gate{Feature: feature} }
