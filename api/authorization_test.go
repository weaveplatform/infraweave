package api_test

import (
	"fmt"
	"slices"
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
)

// authorizationViolations checks decision 0007's enforcement point on doc:
// every operation names an action of the catalogue, or a pseudo-action,
// in x-infraweave-action and where its space comes from in
// x-infraweave-subject, and both agree with x-infraweave-access and the
// action's own rule.
func authorizationViolations(doc *openapi3.T) []string {
	var out []string
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			where := method + " " + path
			out = append(out, operationViolations(where, op)...)
		}
	}
	sort.Strings(out)
	return out
}

func operationViolations(where string, op *openapi3.Operation) []string {
	action, _ := op.Extensions["x-infraweave-action"].(string)
	raw, _ := op.Extensions["x-infraweave-subject"].(string)
	if action == "" || raw == "" {
		return []string{where + " lacks x-infraweave-action or x-infraweave-subject"}
	}
	var out []string
	a := accessapi.Action(action)
	info, inCatalogue := accessapi.ActionOf(a)
	pseudo := slices.Contains([]accessapi.Action{
		accessapi.ActionAccountAdmin,
		accessapi.ActionOrganizationAdmin,
		accessapi.ActionAuthenticated,
	}, a)
	if !inCatalogue && !pseudo {
		out = append(out, fmt.Sprintf("%s names %s, which the catalogue lacks", where, action))
	}
	s, err := accessapi.ParseSubject(raw)
	if err != nil {
		return append(out, fmt.Sprintf("%s: %v", where, err))
	}
	access, _ := op.Extensions["x-infraweave-access"].(string)
	public := op.Security != nil && len(*op.Security) == 0
	switch {
	case (access == "organization-admin") != (a == accessapi.ActionOrganizationAdmin):
		out = append(out, fmt.Sprintf("%s: access %q with %s", where, access, action))
	case (access == "organization" || public) && a != accessapi.ActionAuthenticated:
		out = append(out, fmt.Sprintf("%s: access %q, public %t with %s, not AUTHENTICATED",
			where, access, public, action))
	}
	if (s.Kind == accessapi.SubjectOrganization) != (access != "" || public) {
		out = append(out, fmt.Sprintf("%s: subject %s with access %q, public %t",
			where, raw, access, public))
	}
	switch {
	case info.Anywhere && s.Kind != accessapi.SubjectAny:
		out = append(out, fmt.Sprintf("%s: %s is decided anywhere but the subject is %s",
			where, action, raw))
	case info.Infraweave && s.Kind != accessapi.SubjectAccount && s.Kind != accessapi.SubjectAny:
		out = append(out, fmt.Sprintf("%s: account action %s on subject %s", where, action, raw))
	case s.Kind == accessapi.SubjectPath && op.Parameters.GetByInAndName("path", s.Param) == nil:
		out = append(out, fmt.Sprintf("%s: subject %s names no path parameter", where, raw))
	case s.Kind == accessapi.SubjectQuery && op.Parameters.GetByInAndName("query", s.Param) == nil:
		out = append(out, fmt.Sprintf("%s: subject %s names no query parameter", where, raw))
	case s.Kind == accessapi.SubjectBody && op.RequestBody == nil:
		out = append(out, fmt.Sprintf("%s: subject %s with no request body", where, raw))
	}
	return out
}

// TestUnitAPIAuthorization fails when any served operation lacks its
// action or subject, names an action the catalogue lacks, or disagrees
// with its access, so no route reaches the server without a decision.
func TestUnitAPIAuthorization(t *testing.T) {
	doc := loadServed(t)
	for _, v := range authorizationViolations(doc) {
		t.Error(v)
	}
	// The served enum is the catalogue in its order, less the values it
	// supersedes, and holds every one of infraweave's account actions,
	// which allowedActions lists.
	actions := accessapi.Actions()
	catalogue := make([]string, 0, len(actions))
	for _, a := range actions {
		catalogue = append(catalogue, string(a.Action))
		if a.Infraweave &&
			!slices.Contains(doc.Components.Schemas["Action"].Value.Enum, any(string(a.Action))) {
			t.Errorf("the served Action enum lacks %s", a.Action)
		}
	}
	at := -1
	for _, v := range doc.Components.Schemas["Action"].Value.Enum {
		i := slices.Index(catalogue, fmt.Sprint(v))
		if i <= at {
			t.Errorf("the served Action enum's %v is out of the catalogue's order, or not in it", v)
		}
		at = max(at, i)
	}
}

// authorizationFixture has one operation of each violation, and one
// operation that holds.
const authorizationFixture = `
openapi: 3.0.3
info: {title: fixture, version: "1"}
paths:
  /fine/{id}:
    get:
      x-infraweave-action: SPACE_READ
      x-infraweave-subject: path:id:stack
      parameters: [{name: id, in: path, required: true, schema: {type: string}}]
      responses: {"200": {description: ok}}
  /bare:
    get:
      responses: {"200": {description: ok}}
  /unknown:
    get:
      x-infraweave-action: SPACE_FLY
      x-infraweave-subject: account
      responses: {"200": {description: ok}}
  /grammar:
    get:
      x-infraweave-action: SPACE_READ
      x-infraweave-subject: space
      responses: {"200": {description: ok}}
  /admin:
    get:
      x-infraweave-access: organization-admin
      x-infraweave-action: AUTHENTICATED
      x-infraweave-subject: organization
      responses: {"200": {description: ok}}
  /viewer:
    get:
      x-infraweave-access: organization
      x-infraweave-action: ACCOUNT_ADMIN
      x-infraweave-subject: organization
      responses: {"200": {description: ok}}
  /orgless:
    get:
      x-infraweave-action: AUTHENTICATED
      x-infraweave-subject: organization
      responses: {"200": {description: ok}}
  /keys:
    get:
      x-infraweave-action: API_KEY_READ
      x-infraweave-subject: account
      responses: {"200": {description: ok}}
  /sessions/{id}:
    delete:
      x-infraweave-action: SESSION_DELETE
      x-infraweave-subject: path:id:session
      parameters: [{name: id, in: path, required: true, schema: {type: string}}]
      responses: {"200": {description: ok}}
  /noparam:
    get:
      x-infraweave-action: SPACE_READ
      x-infraweave-subject: path:id:stack
      responses: {"200": {description: ok}}
  /noquery:
    get:
      x-infraweave-action: SPACE_READ
      x-infraweave-subject: query:space
      parameters: [{name: space, in: path, required: true, schema: {type: string}}]
      responses: {"200": {description: ok}}
  /nobody:
    post:
      x-infraweave-action: STACK_CREATE
      x-infraweave-subject: body:space
      responses: {"200": {description: ok}}
`

func TestUnitAPIAuthorizationFixture(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData([]byte(authorizationFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`GET /admin: access "organization-admin" with AUTHENTICATED`,
		"GET /bare lacks x-infraweave-action or x-infraweave-subject",
		`GET /grammar: invalid x-infraweave-subject "space"`,
		"GET /keys: API_KEY_READ is decided anywhere but the subject is account",
		"GET /noparam: subject path:id:stack names no path parameter",
		"GET /noquery: subject query:space names no query parameter",
		`GET /orgless: subject organization with access "", public false`,
		"GET /unknown names SPACE_FLY, which the catalogue lacks",
		`GET /viewer: access "organization", public false with ACCOUNT_ADMIN, not AUTHENTICATED`,
		"POST /nobody: subject body:space with no request body",
		"DELETE /sessions/{id}: account action SESSION_DELETE on subject path:id:session",
	}
	sort.Strings(want)
	if got := authorizationViolations(doc); !slices.Equal(got, want) {
		t.Errorf("violations:\n%s\nwant:\n%s", fmt.Sprint(got), fmt.Sprint(want))
	}
}
