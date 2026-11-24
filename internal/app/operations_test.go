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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/internal/platform/observe"
	policiesassembly "github.com/weaveplatform/infraweave/internal/policies/assembly"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	vcsassembly "github.com/weaveplatform/infraweave/internal/vcs/assembly"
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

// wantRefused fails unless rec is 400 invalid-request naming one field
// error, want's pointer and reason, with a detail that begins subject.
func wantRefused(t *testing.T, rec *httptest.ResponseRecorder, subject string, want problem.FieldError) {
	t.Helper()
	wantTyped(t, rec, problem.InvalidRequest)
	var p problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || len(p.Errors) != 1 ||
		p.Errors[0].Pointer != want.Pointer || p.Errors[0].Reason != want.Reason ||
		p.Errors[0].Message == "" || !strings.HasPrefix(p.Detail, subject+" ") {
		t.Errorf("answers %s, want %s at %s from %s", rec.Body, want.Reason, want.Pointer, subject)
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
		"POST /api/v1/policies/check":                              {Cost: ratelimit.Standard, Gate: gate("expressions")},
		"POST /api/v1/stacks/search":                               {Cost: ratelimit.Expensive, Search: true, Gate: gate("stacks")},
		"POST /api/v1/stack-dependencies/batch":                    {Cost: ratelimit.Expensive, Gate: gate("stacks")},
		"GET /api/v1/stacks":                                       {Cost: ratelimit.Standard, Gate: gate("stacks")},
		"GET /api/v1/stacks/{stackId}/search-run-ignored-triggers": {Cost: ratelimit.Expensive, Search: true, Gate: gate("ignored-runs")},
		// A stack input's vendorConfig member needs its vendor's option.
		"POST /api/v1/stacks": {Cost: ratelimit.Standard, Gate: featuresapi.Gate{
			Feature: "stacks", Options: []featuresapi.OptionGate{
				{Pointer: "/input/vendorConfig/opentofu", Feature: "vendor-opentofu"},
				{Pointer: "/input/vendorConfig/terraform", Feature: "vendor-terraform"},
				{Pointer: "/input/vendorConfig/terragrunt", Feature: "vendor-terragrunt"},
			},
		}},
		"DELETE /operator/v1/organizations/{organization}": {Cost: ratelimit.Standard, Gate: gate("operator")},
		"GET /api/v1/viewer":                          {Cost: ratelimit.Standard, Organization: true, Gate: gate("viewer")},
		"GET /api/v1/auth/oauth/requests/{requestId}": {Cost: ratelimit.Standard, Organization: true, Gate: gate("auth")},
		"GET /api/v1/auth/accounts":                   {Cost: ratelimit.Standard, Organization: true, Gate: gate("auth")},
		"GET /admin-login/{organization}":             {Public: true},
		"POST /admin-login/{organization}":            {Cost: ratelimit.Expensive, Public: true},
		"GET /.well-known/oauth-authorization-server": {Public: true},
		"GET /auth/login":                             {Public: true},
		"GET /auth/login/{organization}":              {Public: true},
		"GET /auth/login/{organization}/{providerId}": {Cost: ratelimit.Expensive, Public: true},
		"GET /auth/oidc/callback":                     {Cost: ratelimit.Expensive, Public: true},
		"POST /auth/oidc/backchannel-logout":          {Public: true},
		"GET /.well-known/openid-configuration":       {Public: true, Cost: ratelimit.Standard},
		"HEAD /.well-known/openid-configuration":      {Public: true, Cost: ratelimit.Standard},
		"GET /.well-known/jwks":                       {Public: true, Cost: ratelimit.Standard},
		"HEAD /.well-known/jwks":                      {Public: true, Cost: ratelimit.Standard},
		"GET " + policiesassembly.ContractPattern:     {Cost: ratelimit.Standard, Gate: gate("policies")},
		"POST /oauth/register":                        {Cost: ratelimit.Expensive, Public: true},
		"GET /oauth/authorize":                        {Public: true},
		"POST /oauth/token":                           {Public: true},
		"GET /blobs/{token}":                          {Public: true},
		"PUT /blobs/{token}":                          {Public: true, OwnBodyBound: true},
		"POST " + vcsassembly.WebhookPattern:          {Public: true, Unkeyed: true, OwnBodyBound: true, Cost: ratelimit.Expensive},
		"LOCK /state/v1/stacks/{stack}":               {State: true, OwnBodyBound: true},
		"GET /state/v1/download/{token}":              {Public: true},
		"PUT /state/v1/uploads/{token}":               {Public: true, OwnBodyBound: true},
		"POST /api/v1/state/upload-url":               {Cost: ratelimit.Standard, Unkeyed: true, Gate: gate("stacks")},
		"POST /api/v1/stacks/{stackId}/state/download-url": {
			Cost: ratelimit.Standard, Unkeyed: true, Gate: gate("stacks"),
		},
		"POST /state/v1/stacks/{stack}/workspaces/{workspace}": {
			State: true, OwnBodyBound: true,
		},
	} {
		got := ops[key]
		got.Required, got.Body, got.Path, got.Query = nil, nil, nil, nil
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", key, got, want)
		}
	}
	// Blob/state transfers and webhook intake enforce their protocol's own
	// body bounds; the state URLs' routes are public.
	// ref: decision 0003 § "API surfaces"
	signedState := map[string]bool{
		http.MethodGet + " " + stateapi.DownloadPattern: true,
		http.MethodPut + " " + stateapi.UploadPattern:   true,
	}
	for key, op := range ops {
		state := strings.Contains(key, " "+stateapi.BaseURL+"/") && !signedState[key]
		if op.State != state {
			t.Errorf("%s: State %v", key, op.State)
		}
		ownBound := key == http.MethodPut+" "+fs.Pattern ||
			key == http.MethodPut+" "+stateapi.UploadPattern || state || key == http.MethodPost+" "+vcsassembly.WebhookPattern
		if op.OwnBodyBound != ownBound {
			t.Errorf("%s: OwnBodyBound %v; only blob/state transfers and webhook intake bound their own bodies", key, op.OwnBodyBound)
		}
	}
	// State URL operations and provider-delivery identities ignore Idempotency-Key.
	for key, op := range ops {
		want := key == "POST "+BaseURL+"/state/upload-url" ||
			key == "POST "+BaseURL+"/stacks/{stackId}/state/download-url" || key == http.MethodPost+" "+vcsassembly.WebhookPattern
		if op.Unkeyed != want {
			t.Errorf("%s: Unkeyed %v", key, op.Unkeyed)
		}
	}
	// Every API operation names its action and subject; nothing else does.
	if required := ops["GET "+policiesassembly.ContractPattern].Required; required == nil || required.Action != accessapi.ActionAuthenticated ||
		required.Subject.Kind != accessapi.SubjectAccount {
		t.Errorf("policy contract requires %+v", required)
	}
	if r := ops["POST "+BaseURL+"/stacks"].Required; r == nil ||
		r.Action != accessapi.ActionStackCreate || r.Subject.Field != "input.space" {
		t.Errorf("stackCreate requires %+v", r)
	}
	if r := ops["GET "+BaseURL+"/stacks/{stackId}/search-run-ignored-triggers"].Required; r == nil || r.Action != accessapi.ActionSpaceAdmin ||
		r.Subject.Kind != accessapi.SubjectPath ||
		r.Subject.Param != "stackId" ||
		r.Subject.Entity != "stack" {
		t.Fatalf("ignored-trigger search controls: %+v", r)
	}
	for key, op := range ops {
		if api := strings.Contains(key, " "+BaseURL+"/") || key == "GET "+policiesassembly.ContractPattern; api != (op.Required != nil) {
			t.Errorf("%s requires %+v", key, op.Required)
		}
	}
	for key, op := range ops {
		f, known := features.Lookup(op.Gate.Feature)
		switch {
		case strings.HasSuffix(key, " "+accessapi.LoginPattern),
			key == http.MethodPost+" "+vcsassembly.WebhookPattern,
			strings.HasSuffix(key, " "+fs.Pattern), signedState[key],
			key == "GET "+accessapi.OAuthMetadataPattern, strings.Contains(key, " /oauth/"),
			strings.Contains(key, " /auth/login"), strings.Contains(key, " /auth/oidc/"),
			strings.HasSuffix(key, " "+accessapi.FederationDiscoveryPattern),
			strings.HasSuffix(key, " "+accessapi.FederationJWKSPattern),
			op.Worker, op.State:
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
		[]featuresapi.OptionGate{{Pointer: "/oidc", Feature: "oidc-api-keys"}}) {
		t.Errorf("apiKeyCreate's options: %+v", g.Options)
	}
	for _, path := range []string{
		"/gitlab-integrations", "/bitbucket-cloud-integrations", "/bitbucket-datacenter-integrations",
		"/azure-dev-ops-repo-integrations", "/github-enterprise-integrations",
	} {
		if g, ok := ops["POST "+BaseURL+path]; !ok || !reflect.DeepEqual(g.Gate.Options,
			[]featuresapi.OptionGate{
				{Pointer: "/customInput/isDefault", Feature: "multiple-vcs-integrations", Value: "false"},
				{Pointer: "/customInput/vcsChecks", Feature: "aggregated-vcs-checks", Value: "AGGREGATED"},
				{Pointer: "/customInput/vcsChecks", Feature: "aggregated-vcs-checks", Value: "ALL"},
			}) {
			t.Errorf("%s create's options: %+v, exists=%t", path, g.Gate.Options, ok)
		}
	}
	if g := ops["PUT "+BaseURL+"/stacks/{stackId}"].Gate; !reflect.DeepEqual(g.Options,
		[]featuresapi.OptionGate{
			{Pointer: "/vendorConfig/opentofu", Feature: "vendor-opentofu"},
			{Pointer: "/vendorConfig/terraform", Feature: "vendor-terraform"},
			{Pointer: "/vendorConfig/terragrunt", Feature: "vendor-terragrunt"},
		}) {
		t.Errorf("stackUpdate's options: %+v", g.Options)
	}
	// A PATCH beside an update PUT gates the members its PUT gates.
	if g, put := ops["PATCH "+BaseURL+"/stacks/{stackId}"].Gate,
		ops["PUT "+BaseURL+"/stacks/{stackId}"].Gate; !reflect.DeepEqual(g, put) {
		t.Errorf("stackPatch's gate %+v, stackUpdate's %+v", g, put)
	}
	// The fs driver's routes are mounted, so a table pattern the driver
	// does not serve fails the walk below.
	mux, ok := API(discard(), config.Defaults().Server, memory(config.Defaults().Server),
		Domains{Auth: byAddress{}, Policies: policiesassembly.New(policiesassembly.Deps{})}, Platform{}, Blobs{Routes: (&fs.Bucket{}).Routes}).(*chi.Mux)
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
	ops, err := operationsOf(doc("/things/search", &openapi3.Operation{}), "/v1", false)
	if err != nil ||
		!reflect.DeepEqual(ops["POST /v1/things/search"],
			Operation{Cost: ratelimit.Standard, Search: true, Gate: gate("stacks")}) {
		t.Errorf("no cost and a /search path: %+v %v", ops, err)
	}
	ops, err = operationsOf(doc("/find", &openapi3.Operation{
		Extensions: map[string]any{"x-infraweave-cost": "expensive", "x-rest-style": "search"},
	}), "", false)
	if err != nil || !reflect.DeepEqual(ops["POST /find"],
		Operation{Cost: ratelimit.Expensive, Search: true, Gate: gate("stacks")}) {
		t.Errorf("x-rest-style search: %+v %v", ops, err)
	}
	for _, bad := range []any{"pricey", 3} {
		_, err := operationsOf(doc("/things", &openapi3.Operation{
			Extensions: map[string]any{"x-infraweave-cost": bad},
		}), "", false)
		if !errors.Is(err, errSpec) {
			t.Errorf("x-infraweave-cost %v: %v", bad, err)
		}
	}
	ops, err = operationsOf(doc("/me", &openapi3.Operation{
		Extensions: map[string]any{"x-infraweave-access": "organization"},
	}), "", false)
	if err != nil || !ops["POST /me"].Organization {
		t.Errorf("x-infraweave-access organization: %+v %v", ops, err)
	}
	ops, err = operationsOf(doc("/organization", &openapi3.Operation{
		Extensions: map[string]any{"x-infraweave-access": "organization-admin"},
	}), "", false)
	if op := ops["POST /organization"]; err != nil || !op.OrganizationAdmin || op.Organization {
		t.Errorf("x-infraweave-access organization-admin: %+v %v", ops, err)
	}
	for _, bad := range []any{"account", true} {
		_, err := operationsOf(doc("/things", &openapi3.Operation{
			Extensions: map[string]any{"x-infraweave-access": bad},
		}), "", false)
		if !errors.Is(err, errSpec) {
			t.Errorf("x-infraweave-access %v: %v", bad, err)
		}
	}
	ops, err = operationsOf(doc("/sign", &openapi3.Operation{
		Extensions: map[string]any{"x-infraweave-idempotency": "ignored"},
	}), "", false)
	if err != nil || !ops["POST /sign"].Unkeyed {
		t.Errorf("x-infraweave-idempotency ignored: %+v %v", ops, err)
	}
	for _, bad := range []any{"replayed", false} {
		_, err := operationsOf(doc("/things", &openapi3.Operation{
			Extensions: map[string]any{"x-infraweave-idempotency": bad},
		}), "", false)
		if !errors.Is(err, errSpec) {
			t.Errorf("x-infraweave-idempotency %v: %v", bad, err)
		}
	}
	for _, bad := range []any{"", "SCANS", "seats", "oidc-api-keys", 3} {
		_, err := operationsOf(doc("/things", &openapi3.Operation{
			Extensions: map[string]any{"x-infraweave-feature": bad},
		}), "", false)
		if !errors.Is(err, errSpec) || !errors.Is(err, errFeature) {
			t.Errorf("x-infraweave-feature %v: %v", bad, err)
		}
	}
	// The API's operations each name an action and a subject.
	ops, err = operationsOf(doc("/stacks/{stackId}/lock", &openapi3.Operation{
		Extensions: map[string]any{
			"x-infraweave-action": "STACK_LOCK", "x-infraweave-subject": "path:stackId:stack",
		},
	}), "", true)
	want := &accessapi.Required{Action: accessapi.ActionStackLock, Subject: accessapi.Subject{
		Kind: accessapi.SubjectPath, Param: "stackId", Entity: "stack",
	}}
	if err != nil || !reflect.DeepEqual(ops["POST /stacks/{stackId}/lock"].Required, want) {
		t.Errorf("an action and subject: %+v %v", ops, err)
	}
	ops, err = operationsOf(doc("/me", &openapi3.Operation{
		Extensions: map[string]any{
			"x-infraweave-action": "AUTHENTICATED", "x-infraweave-subject": "organization",
		},
	}), "", true)
	if err != nil || ops["POST /me"].Required.Action != accessapi.ActionAuthenticated {
		t.Errorf("a pseudo-action: %+v %v", ops, err)
	}
	for name, ext := range map[string]map[string]any{
		"no action":       {"x-infraweave-subject": "account"},
		"unknown action":  {"x-infraweave-action": "SPACE_FLY", "x-infraweave-subject": "account"},
		"no subject":      {"x-infraweave-action": "SPACE_READ"},
		"invalid subject": {"x-infraweave-action": "SPACE_READ", "x-infraweave-subject": "space"},
	} {
		if _, err := operationsOf(doc("/things", &openapi3.Operation{Extensions: ext}), "",
			true); !errors.Is(err, errSpec) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// An operation that creates runs is expensive; one that deletes a stack,
	// whose destroy run a parameter asks for, is not held to it.
	trigger := func(cost string) map[string]any {
		ext := map[string]any{
			"x-infraweave-action": "RUN_TRIGGER", "x-infraweave-subject": "path:stackId:stack",
		}
		if cost != "" {
			ext["x-infraweave-cost"] = cost
		}
		return ext
	}
	for _, cost := range []string{"", "standard"} {
		_, err := operationsOf(doc("/stacks/{stackId}/runs",
			&openapi3.Operation{Extensions: trigger(cost)}), "", true)
		if !errors.Is(err, errSpec) || !errors.Is(err, errRunCost) {
			t.Errorf("a run trigger costing %q: %v", cost, err)
		}
	}
	ops, err = operationsOf(doc("/stacks/{stackId}/runs",
		&openapi3.Operation{Extensions: trigger("expensive")}), "", true)
	if err != nil || ops["POST /stacks/{stackId}/runs"].Cost != ratelimit.Expensive {
		t.Errorf("an expensive run trigger: %+v %v", ops, err)
	}
	if _, err := operationsOf(doc("/stacks/{stackId}", &openapi3.Operation{
		Extensions: map[string]any{
			"x-infraweave-action": "STACK_DELETE", "x-infraweave-subject": "path:stackId:stack",
		},
	}), "", true); err != nil {
		t.Errorf("a standard stack deletion: %v", err)
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
	}), "", false)
	want := []featuresapi.OptionGate{
		{Pointer: "/oidc", Feature: "oidc-api-keys"},
		{Pointer: "/vcsChecks", Value: "AGGREGATED", Feature: "aggregated-vcs-checks"},
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
			false,
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
	if ops, err := operationsOf(
		noBody,
		"",
		false,
	); err != nil ||
		ops["POST /x"].Gate.Options != nil {
		t.Errorf("a body with no JSON schema: %+v, %v", ops, err)
	}
}

// TestUnitOperationNestedOptions checks that an option on a property of a
// nested object gates that property's pointer, under each property that
// shares the object, and that a schema containing itself is walked once
// on each path.
func TestUnitOperationNestedOptions(t *testing.T) {
	terraform := openapi3.NewObjectSchema().NewRef()
	terraform.Extensions = map[string]any{
		"x-infraweave-feature": map[string]any{"feature": "vendor-terraform"},
	}
	vendor := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi3.Schemas{
			"terraform": terraform,
		},
	}
	tree := openapi3.NewObjectSchema()
	tree.Properties = openapi3.Schemas{"child": tree.NewRef()}
	body := openapi3.NewRequestBody().WithJSONSchema(&openapi3.Schema{Properties: openapi3.Schemas{
		"vendorConfig": vendor.NewRef(),
		"a/b":          vendor.NewRef(),
		"tree":         tree.NewRef(),
	}})
	doc := &openapi3.T{Paths: openapi3.NewPaths(openapi3.WithPath("/stacks", &openapi3.PathItem{
		Post: &openapi3.Operation{
			Extensions:  map[string]any{"x-infraweave-feature": "stacks"},
			RequestBody: &openapi3.RequestBodyRef{Value: body},
		},
	}))}
	ops, err := operationsOf(doc, "", false)
	want := []featuresapi.OptionGate{
		{Pointer: "/a~1b/terraform", Feature: "vendor-terraform"},
		{Pointer: "/vendorConfig/terraform", Feature: "vendor-terraform"},
	}
	if err != nil || !reflect.DeepEqual(ops["POST /stacks"].Gate.Options, want) {
		t.Errorf("options = %+v, %v", ops["POST /stacks"].Gate.Options, err)
	}
	terraform.Extensions["x-infraweave-feature"] = map[string]any{"feature": "stacks"}
	if _, err := operationsOf(doc, "", false); !errors.Is(err, errFeature) ||
		!strings.Contains(err.Error(), "/a~1b/terraform") {
		t.Errorf("a nested capability: %v", err)
	}
}

func TestUnitOperationAccess(t *testing.T) {
	for _, c := range []struct {
		op   Operation
		want accessapi.Access
	}{
		{Operation{}, accessapi.Account},
		{Operation{Organization: true}, accessapi.Organization},
		{Operation{OrganizationAdmin: true}, accessapi.OrganizationAdmin},
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

	// The first search passes the limiter, whatever its operation then
	// answers; the second finds the expensive budget spent.
	if rec := send(
		api,
		http.MethodPost,
		"/api/v1/space-contexts/search",
		me,
		"",
	); rec.Code == http.StatusTooManyRequests ||
		rec.Header().Get("X-RateLimit-Limit") != "1" {
		t.Errorf("first search: %d %v", rec.Code, rec.Header())
	}
	wantTyped(t, send(api, http.MethodPost, "/api/v1/space-contexts/search", me, ""),
		problem.RateLimited)
	// The standard budget is separate, and a path no operation serves draws
	// from it too.
	if rec := send(
		api,
		http.MethodGet,
		scaffoldedStackPath,
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
	rec := send(
		api,
		http.MethodGet,
		scaffoldedStackPath,
		"192.0.2.1:1",
		"",
	)
	wantTyped(t, rec, problem.APIDisabled)
	if rec.Header().Get("Retry-After") == "" || rec.Header().Get("X-RateLimit-Limit") != "" {
		t.Errorf("disabled caller's headers: %v", rec.Header())
	}
	if rec := send(
		api,
		http.MethodGet,
		scaffoldedStackPath,
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
	}, memory(config.Defaults().Server), byAddress{}, nil, nil)
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
	// /things runs once and the search twice; the route no operation
	// names is refused, since authorization denies by default, and /nope
	// never reaches a handler.
	// ref: decision 0007
	if runs != 3 {
		t.Errorf("handlers ran %d times, want 3", runs)
	}
	rec := send(r, http.MethodPost, "/unlisted", me, "")
	wantTyped(t, rec, problem.Forbidden)
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
	}, memory(c), nil, nil, nil)
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

// TestUnitPatchBodies checks the body a PATCH beside an update PUT is
// checked against: a merge patch of the PUT's input, in which every member
// is optional and null resets it, and which refuses the members the input
// refuses, at any depth.
func TestUnitPatchBodies(t *testing.T) {
	rb := apiOperations()["PATCH "+BaseURL+"/stacks/{stackId}"].Body
	if rb == nil || rb.Content.Get("application/merge-patch+json") == nil {
		t.Fatalf("stackPatch's body = %+v", rb)
	}
	schema := rb.Content.Get("application/merge-patch+json").Schema.Value
	for _, tc := range []struct {
		doc string
		ok  bool
	}{
		{`{}`, true},
		{`{"labels":null,"name":null,"branch":"dev"}`, true},
		{`{"vendorConfig":{"terraform":{"version":null,"workspace":"w"}}}`, true},
		{`{"bogus":1}`, false},
		{`{"labels":"a"}`, false},
		{`{"vendorConfig":{"terraform":{"bogus":1}}}`, false},
	} {
		var v any
		if err := json.Unmarshal([]byte(tc.doc), &v); err != nil {
			t.Fatal(err)
		}
		if err := schema.VisitJSON(v); (err == nil) != tc.ok {
			t.Errorf("%s: %v, want ok %v", tc.doc, err, tc.ok)
		}
	}
	put := apiOperations()["PUT "+BaseURL+"/stacks/{stackId}"].Body.Content.Get("application/json")
	if len(put.Schema.Value.Required) == 0 || put.Schema.Value.Nullable {
		t.Error("deriving the patch changed the PUT's input")
	}
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
// an unknown field, a missing required one or a wrong type, each named by
// its pointer and reason, and for a body that is empty, and passes a good
// body with or without a Content-Type; a lenient operation's body is not
// checked. The served apiKeyCreate is strict.
func TestUnitStrictBodies(t *testing.T) {
	ops := apiOperations()
	if ops["POST "+BaseURL+"/api-keys"].Body == nil {
		t.Error("apiKeyCreate's body is not strict")
	}
	for _, key := range []string{
		"POST /stacks/search", "PATCH /organization", "PATCH /stacks/{stackId}",
	} {
		method, path, _ := strings.Cut(key, " ")
		if ops[method+" "+BaseURL+path].Body == nil {
			t.Errorf("%s's body is not checked", key)
		}
	}
	no := false
	strict := &openapi3.RequestBody{
		Required: true,
		Content: openapi3.NewContentWithJSONSchema(&openapi3.Schema{
			Type:     &openapi3.Types{"object"},
			Required: []string{"name"},
			Properties: openapi3.Schemas{
				"name": openapi3.NewStringSchema().NewRef(),
				"meta": (&openapi3.Schema{
					Type:                 &openapi3.Types{"object"},
					AdditionalProperties: openapi3.AdditionalProperties{Has: &no},
				}).NewRef(),
			},
			AdditionalProperties: openapi3.AdditionalProperties{Has: &no},
		}),
	}
	runs := 0
	r := Guarded(discard(), config.Defaults().Server, Operations{
		"POST /strict":  {Cost: ratelimit.Standard, Public: true, Body: strict},
		"POST /lenient": {Cost: ratelimit.Standard, Public: true},
	}, memory(config.Defaults().Server), nil, nil, nil)
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
	wantTyped(t, post("/strict", ``, "application/json"), problem.InvalidRequest)
	for body, want := range map[string]problem.FieldError{
		`{"name":"k","admin":false}`:     {Pointer: "/admin", Reason: kernel.ReasonUnknownField},
		`{"name":"k","meta":{"a/b~":1}}`: {Pointer: "/meta/a~1b~0", Reason: kernel.ReasonUnknownField},
		`{}`:                             {Pointer: "/name", Reason: kernel.ReasonRequired},
		`{"name":7}`:                     {Pointer: "/name", Reason: kernel.ReasonType},
	} {
		wantRefused(t, post("/strict", body, "application/json"), "The body", want)
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

// TestUnitStrictQueries checks that an operation's query parameters are
// checked against their schemas before its handler: 400 invalid-request,
// naming the value by its pointer under the parameter and the reason, for
// an unknown member of a closed JSON object, a value its enum lacks, a
// wrong type, a missing required parameter or member, a bound, an int32
// out of range and a pattern, never quoting the value; JSON that does not
// parse is refused naming nothing. An open object takes members its
// schema does not name, a good request passes, and an operation's
// parameters are its own. The served runChangesV3 and
// stackSearchManagedEntities check their input and runLogs its state.
func TestUnitStrictQueries(t *testing.T) {
	ops := apiOperations()
	names := func(key string) []string {
		method, path, _ := strings.Cut(key, " ")
		q := ops[method+" "+BaseURL+path].Query
		out := make([]string, 0, len(q))
		for _, p := range q {
			out = append(out, p.Value.Name)
		}
		return out
	}
	for key, want := range map[string]string{
		"GET /runs/{runId}/changes-v3":                  "input",
		"GET /stacks/{stackId}/search-managed-entities": "input",
		"GET /runs/{runId}/logs":                        "state",
	} {
		if !slices.Contains(names(key), want) {
			t.Errorf("%s checks %v, not %s", key, names(key), want)
		}
	}
	no := false
	object := func(closed bool) *openapi3.Schema {
		s := &openapi3.Schema{
			Type:     &openapi3.Types{"object"},
			Required: []string{"phase"},
			Properties: openapi3.Schemas{
				"phase": openapi3.NewStringSchema().WithEnum("PLAN", "APPLY").NewRef(),
				"count": openapi3.NewInt32Schema().NewRef(),
			},
		}
		if closed {
			s.AdditionalProperties = openapi3.AdditionalProperties{Has: &no}
		}
		return s
	}
	input := func(closed bool) *openapi3.ParameterRef {
		return &openapi3.ParameterRef{Value: &openapi3.Parameter{
			Name: "input", In: openapi3.ParameterInQuery, Required: true,
			Content: openapi3.NewContentWithJSONSchema(object(closed)),
		}}
	}
	scalar := func(name string, s *openapi3.Schema) *openapi3.ParameterRef {
		return &openapi3.ParameterRef{Value: &openapi3.Parameter{
			Name: name, In: openapi3.ParameterInQuery, Schema: s.NewRef(),
		}}
	}
	strict := strictParams(&openapi3.Operation{Parameters: openapi3.Parameters{
		input(true),
		scalar("state", openapi3.NewStringSchema().WithEnum("NEW", "DONE")),
		scalar("limit", openapi3.NewInt32Schema().WithMin(1).WithMax(100)),
		scalar("slug", openapi3.NewStringSchema().WithPattern("^[a-z]+$")),
		{Value: openapi3.NewQueryParameter("bare")},
		{Value: openapi3.NewPathParameter("stackId").WithSchema(openapi3.NewStringSchema())},
	}}, openapi3.ParameterInQuery)
	if len(strict) != 4 {
		t.Fatalf("strictParams = %d parameters, want input, state, limit and slug", len(strict))
	}
	runs := 0
	r := Guarded(discard(), config.Defaults().Server, Operations{
		"GET /strict": {Cost: ratelimit.Standard, Public: true, Query: strict},
		"GET /open": {
			Cost:   ratelimit.Standard,
			Public: true,
			Query: strictParams(&openapi3.Operation{
				Parameters: openapi3.Parameters{input(false)},
			}, openapi3.ParameterInQuery),
		},
		"GET /plain": {Cost: ratelimit.Standard, Public: true},
	}, memory(config.Defaults().Server), nil, nil, nil)
	ok := func(w http.ResponseWriter, r *http.Request) {
		runs++
		w.WriteHeader(http.StatusOK)
	}
	r.Get("/strict", ok)
	r.Get("/open", ok)
	r.Get("/plain", ok)
	get := func(path string, q url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path+"?"+q.Encode(), nil)
		req.RemoteAddr = "192.0.2.1:1"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	good := `{"phase":"PLAN"}`
	refused := func(pointer, reason string) problem.FieldError {
		return problem.FieldError{Pointer: pointer, Reason: reason}
	}
	for _, c := range []struct {
		q    url.Values
		want problem.FieldError
	}{
		{url.Values{"input": {`{"phase":"PLAN","after":"x"}`}}, refused("#/query/input/after", kernel.ReasonUnknownField)},
		{url.Values{"input": {`{"phase":"LATER"}`}}, refused("#/query/input/phase", kernel.ReasonEnum)},
		{url.Values{"input": {`{"phase":"PLAN","count":"x"}`}}, refused("#/query/input/count", kernel.ReasonType)},
		{url.Values{"input": {`{}`}}, refused("#/query/input/phase", kernel.ReasonRequired)},
		{url.Values{}, refused("#/query/input", kernel.ReasonRequired)},
		{url.Values{"input": {good}, "state": {"LATER"}}, refused("#/query/state", kernel.ReasonEnum)},
		{url.Values{"input": {good}, "limit": {"many"}}, refused("#/query/limit", kernel.ReasonType)},
		{url.Values{"input": {good}, "limit": {"0"}}, refused("#/query/limit", kernel.ReasonMinimum)},
		{url.Values{"input": {good}, "limit": {"101"}}, refused("#/query/limit", kernel.ReasonMaximum)},
		{url.Values{"input": {good}, "limit": {"3000000000"}}, refused("#/query/limit", kernel.ReasonFormat)},
		{url.Values{"input": {good}, "slug": {"S3CR3T"}}, refused("#/query/slug", kernel.ReasonPattern)},
	} {
		rec := get("/strict", c.q)
		name, _, _ := strings.Cut(strings.TrimPrefix(c.want.Pointer, "#/query/"), "/")
		wantRefused(t, rec, "The query parameter "+name, c.want)
		for _, vs := range c.q {
			if v := vs[0]; v != good && len(v) > 4 && strings.Contains(rec.Body.String(), v) {
				t.Errorf("the refusal of %s quotes it: %s", v, rec.Body)
			}
		}
	}
	rec := get("/strict", url.Values{"input": {`{"phase"`}})
	wantTyped(t, rec, problem.InvalidRequest)
	if strings.Contains(rec.Body.String(), `"errors"`) {
		t.Errorf("JSON that does not parse names a field: %s", rec.Body)
	}
	wantRefused(t, get("/open", url.Values{"input": {`{"phase":"LATER","after":"x"}`}}),
		"The query parameter input", refused("#/query/input/phase", kernel.ReasonEnum))
	if runs != 0 {
		t.Fatalf("a refused query reached the handler %d times", runs)
	}
	if rec := get("/strict", url.Values{
		"input": {good}, "state": {"DONE"}, "limit": {"100"}, "slug": {"infra"}, "bare": {"x"},
	}); rec.Code != http.StatusOK {
		t.Errorf("a good query: %d %s", rec.Code, rec.Body)
	}
	if rec := get(
		"/open",
		url.Values{"input": {`{"phase":"PLAN","after":"x"}`}},
	); rec.Code != http.StatusOK {
		t.Errorf("an open object's unnamed member: %d %s", rec.Code, rec.Body)
	}
	if rec := get(
		"/plain",
		url.Values{"input": {`{"anything":1}`}},
	); rec.Code != http.StatusOK ||
		runs != 3 {
		t.Errorf("another operation's parameter: %d, %d runs", rec.Code, runs)
	}
}

// TestUnitStrictPaths checks that every path parameter with a schema is
// collected, the worker API's run ID among them, and that a value its
// maxLength, pattern, enum or format refuses, or not of its type, answers
// 400 at #/path/<name> with the keyword's reason, never quoting it and
// never reaching the handler; that a value is checked unescaped, as the
// binding decodes it; and that good values and another operation's pass.
func TestUnitStrictPaths(t *testing.T) {
	ops := apiOperations()
	for key, want := range map[string]string{
		"POST " + WorkerBaseURL + "/runs/{run}/reports": "run",
		"GET " + BaseURL + "/stacks/{stackId}":          "stackId",
	} {
		names := make([]string, 0, len(ops[key].Path))
		for _, p := range ops[key].Path {
			names = append(names, p.Value.Name)
		}
		if !slices.Equal(names, []string{want}) {
			t.Errorf("%s checks %v, not %s", key, names, want)
		}
	}
	param := func(name string, s *openapi3.Schema) *openapi3.ParameterRef {
		return &openapi3.ParameterRef{Value: openapi3.NewPathParameter(name).WithSchema(s)}
	}
	strict := strictParams(&openapi3.Operation{Parameters: openapi3.Parameters{
		param("run", openapi3.NewStringSchema().WithMaxLength(26)),
		param("slug", openapi3.NewStringSchema().WithPattern("^[a-z-]+$")),
		param("id", openapi3.NewInt64Schema()),
		param("state", openapi3.NewStringSchema().WithEnum("NEW", "DONE")),
		{Value: openapi3.NewQueryParameter("q").WithSchema(openapi3.NewStringSchema())},
	}}, openapi3.ParameterInPath)
	if len(strict) != 4 {
		t.Fatalf("strictParams = %d parameters, want run, slug, id and state", len(strict))
	}
	runs := 0
	r := Guarded(discard(), config.Defaults().Server, Operations{
		"GET /strict/{run}/{slug}/{id}/{state}": {
			Cost: ratelimit.Standard, Public: true, Path: strict,
		},
		"GET /plain/{run}": {Cost: ratelimit.Standard, Public: true},
	}, memory(config.Defaults().Server), nil, nil, nil)
	ok := func(w http.ResponseWriter, r *http.Request) {
		runs++
		w.WriteHeader(http.StatusOK)
	}
	r.Get("/strict/{run}/{slug}/{id}/{state}", ok)
	r.Get("/plain/{run}", ok)
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.RemoteAddr = "192.0.2.1:1"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	run := strings.Repeat("A", 26)
	for _, c := range []struct {
		path, quoted string
		want         problem.FieldError
	}{
		{"/strict/" + run + "A/infra/7/NEW", run + "A", problem.FieldError{
			Pointer: "#/path/run", Reason: kernel.ReasonMaxLength,
		}},
		{"/strict/" + run + "/S3CR3T/7/NEW", "S3CR3T", problem.FieldError{
			Pointer: "#/path/slug", Reason: kernel.ReasonPattern,
		}},
		{"/strict/" + run + "/a%2Fb/7/NEW", "", problem.FieldError{
			Pointer: "#/path/slug", Reason: kernel.ReasonPattern,
		}},
		{"/strict/" + run + "/infra/seven/NEW", "seven", problem.FieldError{
			Pointer: "#/path/id", Reason: kernel.ReasonType,
		}},
		{"/strict/" + run + "/infra/9223372036854775808/NEW", "", problem.FieldError{
			Pointer: "#/path/id", Reason: kernel.ReasonFormat,
		}},
		{"/strict/" + run + "/infra/7/LATER", "LATER", problem.FieldError{
			Pointer: "#/path/state", Reason: kernel.ReasonEnum,
		}},
	} {
		rec := get(c.path)
		name := strings.TrimPrefix(c.want.Pointer, "#/path/")
		wantRefused(t, rec, "The path parameter "+name, c.want)
		if c.quoted != "" && strings.Contains(rec.Body.String(), c.quoted) {
			t.Errorf("the refusal of %s quotes it: %s", c.quoted, rec.Body)
		}
	}
	if runs != 0 {
		t.Fatalf("a refused path reached the handler %d times", runs)
	}
	for _, path := range []string{
		"/strict/" + run + "/infra-net/7/DONE",
		"/strict/" + run[:25] + "%41/infra/7/NEW",
		"/plain/" + run + "A",
	} {
		if rec := get(path); rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if runs != 3 {
		t.Errorf("good paths reached the handler %d times, want 3", runs)
	}
}

// gate is the gate of an operation that needs the capability feature only.
func gate(feature string) featuresapi.Gate { return featuresapi.Gate{Feature: feature} }
