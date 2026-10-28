package specref

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestUnitBuildPlanRoutes(t *testing.T) {
	p, err := BuildPlan(fixtureSchema(), fixtureMappingParsed(t))
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ ID, Method, Path, Style string }
	var got []row
	for _, r := range p.Routes {
		got = append(got, row{r.OperationID, r.Method, r.Path, r.Style})
	}
	want := []row{
		{"stack", "GET", "/stacks/{stackId}", StyleItem},
		{"stacks", "GET", "/stacks", StyleList},
		{"searchStacks", "POST", "/stacks/search", StyleSearch},
		{"searchStacksSuggestions", "POST", "/stacks/search/suggestions", StyleSearch},
		// space is an attribute argument, so it does not nest the search.
		{"searchSpaceStacks", "POST", "/space-stacks/search", StyleSearch},
		{"run", "GET", "/runs/{runId}", StyleItem},
		{"runStack", "GET", "/runs/{runId}/stack", StyleSubRead},
		{"context", "GET", "/contexts/{contextId}", StyleItem},
		{"accountName", "GET", "/account/name", StyleSingleton},
		{"terraformVersions", "GET", "/terraform-versions", StyleRead},
		{"repo", "GET", "/repos/{repoId}", StyleItem},
		{"group", "GET", "/groups/{groupId}", StyleItem},
		{"mirror", "GET", "/mirrors/{mirrorId}", StyleItem},
		{"stackCreate", "POST", "/stacks", StyleCreate},
		{"stackUpdate", "PUT", "/stacks/{stackId}", StyleUpdate},
		{"stackDelete", "DELETE", "/stacks/{stackId}", StyleDelete},
		{"stackLock", "POST", "/stacks/{stackId}/lock", StyleAction},
		{"runCancel", "POST", "/stacks/{stackId}/runs/{runId}/cancel", StyleAction},
		{"runTrigger", "POST", "/stacks/{stackId}/runs", StyleOverride},
		// A nullable run argument means "the blocking run": a collection action.
		{"runDiscard", "POST", "/stacks/{stackId}/runs/discard", StyleAction},
		{"contextCreateV2", "POST", "/contexts", StyleCreate},
		{"contextAttach", "PUT", "/stacks/{stackId}/contexts/{contextId}", StyleAttach},
		{"contextDetach", "DELETE", "/context-attachments/{attachmentId}", StyleDetach},
		{"stackScheduledRunCreate", "POST", "/stacks/{stackId}/scheduled-runs", StyleCreate},
		{
			"stackScheduledRunDelete",
			"DELETE",
			"/stacks/{stackId}/scheduled-runs/{scheduledRunId}",
			StyleDelete,
		},
		{"spaceThingCreate", "POST", "/space-things", StyleCreate},
		{"oidcCreate", "POST", "/oidc", StyleSingleton},
		{"oidcUpdate", "PUT", "/oidc", StyleSingleton},
		{"oidcDelete", "DELETE", "/oidc", StyleSingleton},
		{"oidcRotateSecret", "POST", "/oidc/rotate-secret", StyleSingleton},
		{"accountSetName", "POST", "/account/set-name", StyleSingleton},
		{"doSomethingWeird", "POST", "/do-something-weird", StyleRPC},
		{"logout", "POST", "/auth/logout", StyleOverride},
		{"groupUpdate", "PUT", "/groups/{groupId}", StyleUpdate},
		{"repoUpdate", "PUT", "/repos/{repoId}", StyleUpdate},
		{"mirrorUpdate", "PUT", "/mirrors/{mirrorId}", StyleUpdate},
		{"repoLink", "PUT", "/repos/{repoId}/link", StyleOverride},
		{"runLogs", "GET", "/runs/{runId}/logs", StyleField},
		{"stackRuns", "GET", "/stacks/{stackId}/runs", StyleField},
		{"stackHistory", "GET", "/stacks/{stackId}/history", StyleField},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("routes (-want +got):\n%s", diff)
	}
	excluded := map[string]string{}
	for _, e := range p.Excluded {
		excluded[e.Field] = e.Reason
	}
	wantExcluded := map[string]string{
		"oldStack":      "deprecated upstream: Use stack instead.",
		"contextCreate": "superseded by contextCreateV2",
		"trackEvent":    "UI analytics.",
	}
	if diff := cmp.Diff(wantExcluded, excluded); diff != "" {
		t.Errorf("excluded (-want +got):\n%s", diff)
	}
}

func TestUnitBuildPlanParams(t *testing.T) {
	p, err := BuildPlan(fixtureSchema(), fixtureMappingParsed(t))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*Route{}
	for _, r := range p.Routes {
		byID[r.OperationID] = r
	}
	tests := map[string][]PathParam{
		"runCancel":  {{Name: "stackId", Arg: "stack"}, {Name: "runId", Arg: "run"}},
		"runTrigger": {{Name: "stackId", Arg: "stack"}},
		"contextAttach": {
			{Name: "stackId", Arg: "stack"},
			{Name: "contextId", Arg: "id"},
		},
		"contextDetach": {{Name: "attachmentId", Arg: "id"}},
		"stackScheduledRunDelete": {
			{Name: "stackId", Arg: "stack"},
			{Name: "scheduledRunId", Arg: "scheduledRun"},
		},
		"runStack": {{Name: "runId", Arg: "runId"}},
		// The parent object, not an argument of the field, fills the path.
		"stackRuns": {{Name: "stackId", Arg: ""}},
		// Ids inside inputs bind by their path through the arguments.
		"groupUpdate":  {{Name: "groupId", Arg: "input.id"}},
		"repoUpdate":   {{Name: "repoId", Arg: "custom.id"}},
		"mirrorUpdate": {{Name: "mirrorId", Arg: "input.custom.id"}},
		"repoLink":     {{Name: "repoId", Arg: "link.repo"}},
	}
	for id, want := range tests {
		if diff := cmp.Diff(want, byID[id].Params); diff != "" {
			t.Errorf("%s params (-want +got):\n%s", id, diff)
		}
	}
	if !byID["logout"].Public {
		t.Error("logout override should be public")
	}
	if !byID["repoLink"].Created || byID["logout"].Created {
		t.Error("only the repoLink override answers 201")
	}
	if got := p.ItemTypes["Stack"].OperationID; got != "stack" {
		t.Errorf("Stack item route = %s", got)
	}
}

func TestUnitBuildPlanErrors(t *testing.T) {
	tests := []struct {
		name    string
		mapping string
		mutate  func(*Schema)
		want    error
		detail  string
	}{
		{
			name:    "rpc budget",
			mapping: "rpcBudget: 0\nsingletons: [account, oidc]\nattributeArgs: [space]\noperations: {logout: {method: POST, path: /auth/logout}}\nexclude: {trackEvent: x}",
			want:    ErrRPCBudget,
		},
		{
			name:    "stale override",
			mapping: "rpcBudget: 9\noperations: {gone: {method: GET, path: /gone}}",
			want:    ErrStaleMapping,
			detail:  "operations.gone",
		},
		{
			name:    "stale exclusion",
			mapping: "rpcBudget: 9\nexclude: {gone: reason}",
			want:    ErrStaleMapping,
			detail:  "exclude.gone",
		},
		{
			name:    "override and exclusion",
			mapping: "rpcBudget: 9\noperations: {logout: {method: POST, path: /x}}\nexclude: {logout: reason}",
			want:    ErrStaleMapping,
			detail:  "also excluded",
		},
		{
			name:    "unbound parameter",
			mapping: "rpcBudget: 9\noperations: {logout: {method: POST, path: '/x/{nope}'}}",
			want:    ErrUnboundParameter,
		},
		{
			name:    "dotted parameter through a scalar",
			mapping: "rpcBudget: 9\noperations: {repoLink: {method: PUT, path: '/x/{a}', params: {a: link.url.x}}}",
			want:    ErrUnboundParameter,
		},
		{
			name:    "dotted parameter through a list",
			mapping: "rpcBudget: 9\noperations: {repoUpdate: {method: PUT, path: '/x/{a}', params: {a: custom.labels.x}}}",
			want:    ErrUnboundParameter,
		},
		{
			name:    "dotted parameter naming no field",
			mapping: "rpcBudget: 9\noperations: {repoLink: {method: PUT, path: '/x/{a}', params: {a: link.nope}}}",
			want:    ErrUnboundParameter,
		},
		{
			name:    "collision",
			mapping: "rpcBudget: 9\noperations: {logout: {method: GET, path: /stacks}}",
			want:    ErrRouteCollision,
			detail:  "stacks and logout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := ParseMapping([]byte(tt.mapping))
			if err != nil {
				t.Fatal(err)
			}
			_, err = BuildPlan(fixtureSchema(), m)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.detail) {
				t.Errorf("err = %v, want it to mention %q", err, tt.detail)
			}
		})
	}
}

func TestUnitBuildPlanOverrideParamInference(t *testing.T) {
	m, err := ParseMapping(
		[]byte(
			"rpcBudget: 9\noperations:\n  runCancel: {method: DELETE, path: '/runs/{runId}/in/{stackId}'}\n  stackLock: {method: PUT, path: '/locks/{id}'}\n",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(fixtureSchema(), m)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Routes {
		switch r.OperationID {
		case "runCancel":
			want := []PathParam{{Name: "runId", Arg: "run"}, {Name: "stackId", Arg: "stack"}}
			if diff := cmp.Diff(want, r.Params); diff != "" {
				t.Errorf("runCancel (-want +got):\n%s", diff)
			}
		case "stackLock":
			if diff := cmp.Diff([]PathParam{{Name: "id", Arg: "id"}}, r.Params); diff != "" {
				t.Errorf("stackLock (-want +got):\n%s", diff)
			}
		}
	}
}

func TestUnitRouteBodyAndBinding(t *testing.T) {
	r := &Route{Method: "GET", Params: []PathParam{{Name: "stackId", Arg: "stack"}}}
	if r.HasBody() || !r.Bound("stack") || r.Bound("id") {
		t.Error("GET route: no body, stack bound, id not")
	}
	for _, m := range []string{"POST", "PUT", "PATCH"} {
		if !(&Route{Method: m}).HasBody() {
			t.Errorf("%s should carry a body", m)
		}
	}
}

func TestUnitSupersededVersions(t *testing.T) {
	s := &Schema{
		QueryType:    &NamedRef{Name: "Query"},
		MutationType: &NamedRef{Name: "Mutation"},
		Types: []*Type{
			{Kind: KindObject, Name: "Query"},
			{Kind: KindObject, Name: "Mutation", Fields: []Field{
				field(
					"thingCreate",
					tBoolean,
				),
				field("thingCreateV3", tBoolean),
				field("thingCreateV2", tBoolean),
				deprecated(field("otherCreate", tBoolean), "x"),
				field("otherCreateV2", tBoolean),
			}},
		},
	}
	s.Normalize()
	want := map[string]string{"thingCreate": "thingCreateV3", "thingCreateV2": "thingCreateV3"}
	if diff := cmp.Diff(want, supersededVersions(s)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestUnitVerbClass(t *testing.T) {
	for verb, want := range map[string]string{
		"Create": StyleCreate, "Add": StyleCreate, "Update": StyleUpdate, "Delete": StyleDelete,
		"Remove": StyleDelete, "Attach": StyleAttach, "Detach": StyleDetach, "Lock": StyleAction,
	} {
		if got := verbClass(verb); got != want {
			t.Errorf("verbClass(%s) = %s, want %s", verb, got, want)
		}
	}
}

func TestUnitNestedID(t *testing.T) {
	s := fixtureSchema()
	s.Types = append(s.Types, &Type{
		Kind:        KindInputObject,
		Name:        "Deep",
		InputFields: []InputValue{{Name: "next", Type: input("Deep")}},
	})
	s.Normalize()
	tests := map[string]struct {
		f    Field
		want string
	}{
		"one input": {
			field("x", tBoolean, arg("input", nn(input("GroupUpdateInput")))),
			"input.id",
		},
		"two levels": {
			field("x", tBoolean, arg("input", input("MirrorUpdateInput"))),
			"input.custom.id",
		},
		"scalars only":  {field("x", tBoolean, arg("id", tID)), ""},
		"lists skipped": {field("x", tBoolean, arg("all", list(input("GroupUpdateInput")))), ""},
		"too deep":      {field("x", tBoolean, arg("d", input("Deep"))), ""},
		"two candidates": {
			field(
				"x",
				tBoolean,
				arg("a", input("GroupUpdateInput")),
				arg("b", input("CustomUpdateInput")),
			),
			"",
		},
		// The shallower id wins over one further down.
		"shallowest": {
			field(
				"x",
				tBoolean,
				arg("a", input("GroupUpdateInput")),
				arg("b", input("MirrorUpdateInput")),
			),
			"a.id",
		},
	}
	for name, tt := range tests {
		if got := s.nestedID(tt.f); got != tt.want {
			t.Errorf("%s: nestedID = %q, want %q", name, got, tt.want)
		}
	}
}
