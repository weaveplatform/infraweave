package specref

import (
	"encoding/json"
	"testing"
)

// Type constructors for hand-built schemas.
func named(kind, name string) TypeRef { return TypeRef{Kind: kind, Name: name} }
func nn(t TypeRef) TypeRef            { return TypeRef{Kind: KindNonNull, OfType: &t} }
func list(t TypeRef) TypeRef          { return TypeRef{Kind: KindList, OfType: &t} }

var (
	tID      = named(KindScalar, "ID")
	tString  = named(KindScalar, "String")
	tInt     = named(KindScalar, "Int")
	tFloat   = named(KindScalar, "Float")
	tBoolean = named(KindScalar, "Boolean")
)

func obj(name string) TypeRef   { return named(KindObject, name) }
func input(name string) TypeRef { return named(KindInputObject, name) }

func arg(name string, t TypeRef) InputValue { return InputValue{Name: name, Type: t} }

func field(name string, t TypeRef, args ...InputValue) Field {
	return Field{Name: name, Type: t, Args: args, Description: "The " + name + " field."}
}

func deprecated(f Field, reason string) Field {
	f.IsDeprecated, f.DeprecationReason = true, reason
	return f
}

func strPtr(s string) *string { return &s }

// fixtureSchema exercises every routing rule and schema mapping in a few
// types: CRUD, parents, actions, attach/detach, singletons, search, lists,
// reads, sub-reads, field routes, overrides, exclusions, superseded and
// deprecated fields, unions, enums, defaults and embedded references.
func fixtureSchema() *Schema {
	s := &Schema{
		QueryType:    &NamedRef{Name: "Query"},
		MutationType: &NamedRef{Name: "Mutation"},
		Types: []*Type{
			{Kind: KindObject, Name: "Query", Fields: []Field{
				field("stack", obj("Stack"), arg("id", nn(tID))),
				field("stacks", nn(list(nn(obj("Stack"))))),
				field(
					"searchStacks",
					nn(obj("SearchStacksOutput")),
					arg("input", nn(input("SearchInput"))),
				),
				field(
					"searchStacksSuggestions",
					nn(obj("SearchStacksOutput")),
					arg("input", nn(input("SearchInput"))),
				),
				field(
					"searchSpaceStacks",
					nn(obj("SearchStacksOutput")),
					arg("space", nn(tID)),
					arg("input", nn(input("SearchInput"))),
				),
				field("run", obj("Run"), arg("id", nn(tID))),
				field("runStack", obj("Stack"), arg("runId", nn(tID))),
				field("context", obj("Context"), arg("id", nn(tID))),
				field("accountName", tString),
				field(
					"terraformVersions",
					nn(list(nn(tString))),
					arg("filter", input("SearchInput")),
					arg("limit", tInt),
				),
				deprecated(
					field("oldStack", obj("Stack"), arg("id", nn(tID))),
					"Use stack instead.",
				),
				field("repo", obj("Repo"), arg("id", nn(tID))),
				field("group", obj("Group"), arg("id", nn(tID))),
				field("mirror", obj("Mirror"), arg("id", nn(tID))),
			}},
			{Kind: KindObject, Name: "Mutation", Fields: []Field{
				field(
					"stackCreate",
					nn(obj("Stack")),
					arg("input", nn(input("StackInput"))),
					arg("manageState", nn(tBoolean)),
				),
				field(
					"stackUpdate",
					nn(obj("Stack")),
					arg("id", nn(tID)),
					arg("input", nn(input("StackInput"))),
				),
				field("stackDelete", obj("Stack"), arg("id", nn(tID)), arg("force", tBoolean)),
				field("stackLock", nn(obj("Stack")), arg("id", nn(tID)), arg("note", tString)),
				field(
					"runCancel",
					nn(obj("Run")),
					arg("stack", nn(tID)),
					arg("run", nn(tID)),
					arg("note", tString),
				),
				field("runTrigger", nn(obj("Run")), arg("stack", nn(tID))),
				field("runDiscard", nn(obj("Run")), arg("stack", nn(tID)), arg("run", tID)),
				field("contextCreate", nn(obj("Context")), arg("input", nn(input("ContextInput")))),
				field(
					"contextCreateV2",
					nn(obj("Context")),
					arg("input", nn(input("ContextInput"))),
				),
				field(
					"contextAttach",
					nn(obj("Attachment")),
					arg("id", nn(tID)),
					arg("stack", nn(tID)),
					arg("priority", nn(tInt)),
				),
				field("contextDetach", obj("Attachment"), arg("id", nn(tID))),
				field(
					"stackScheduledRunCreate",
					obj("ScheduledRun"),
					arg("stack", nn(tID)),
					arg("input", nn(input("ScheduledRunInput"))),
				),
				field(
					"stackScheduledRunDelete",
					obj("ScheduledRun"),
					arg("stack", nn(tID)),
					arg("scheduledRun", nn(tID)),
				),
				field(
					"spaceThingCreate",
					obj("Context"),
					arg("space", nn(tID)),
					arg("name", tString),
				),
				field("oidcCreate", nn(tBoolean), arg("clientId", nn(tString))),
				field("oidcUpdate", nn(tBoolean), arg("clientId", nn(tString))),
				field("oidcDelete", tBoolean),
				field("oidcRotateSecret", tBoolean),
				field("accountSetName", tString, arg("name", nn(tString))),
				field("doSomethingWeird", tBoolean, arg("x", tString)),
				field("trackEvent", tBoolean, arg("name", tString)),
				field("logout", tBoolean),
				// Updates whose id sits inside an input: one input argument,
				// several arguments, and two levels down.
				field("groupUpdate", nn(obj("Group")), arg("input", nn(input("GroupUpdateInput")))),
				field(
					"repoUpdate",
					nn(obj("Repo")),
					arg("token", tString),
					arg("custom", input("CustomUpdateInput")),
				),
				field(
					"mirrorUpdate",
					nn(obj("Mirror")),
					arg("input", nn(input("MirrorUpdateInput"))),
				),
				field("repoLink", nn(obj("Repo")), arg("link", nn(input("LinkInput")))),
			}},
			{Kind: KindObject, Name: "Stack", Description: "A stack.", Fields: []Field{
				field("id", nn(tID)),
				field("name", nn(tString)),
				field("labels", nn(list(nn(tString)))),
				field("state", nn(named(KindEnum, "StackState"))),
				field("runs", nn(list(nn(obj("Run")))), arg("before", tString)),
				field("history", nn(list(nn(obj("Run"))))),
				field("owner", named(KindUnion, "Owner")),
				field("latestRun", obj("Run")),
				deprecated(field("legacy", tString), "gone"),
				field("score", tFloat),
				field("count", tInt),
				field("enabled", tBoolean),
			}},
			{Kind: KindObject, Name: "Run", Fields: []Field{
				field("id", nn(tID)),
				field("stack", nn(obj("Stack"))),
				field("logs", tString, arg("tail", tInt)),
			}},
			{
				Kind:          KindUnion,
				Name:          "Owner",
				Description:   "Who owns a stack.",
				PossibleTypes: []TypeRef{obj("User"), obj("Team")},
			},
			{Kind: KindObject, Name: "User", Fields: []Field{field("name", nn(tString))}},
			{Kind: KindObject, Name: "Team", Fields: []Field{field("name", nn(tString))}},
			{
				Kind:   KindObject,
				Name:   "Context",
				Fields: []Field{field("id", nn(tID)), field("name", nn(tString))},
			},
			{
				Kind:   KindObject,
				Name:   "Attachment",
				Fields: []Field{field("id", nn(tID)), field("context", nn(obj("Context")))},
			},
			{Kind: KindObject, Name: "ScheduledRun", Fields: []Field{field("id", nn(tID))}},
			{
				Kind:   KindObject,
				Name:   "Repo",
				Fields: []Field{field("id", nn(tID)), field("name", nn(tString))},
			},
			{
				Kind:   KindObject,
				Name:   "Group",
				Fields: []Field{field("id", nn(tID)), field("name", nn(tString))},
			},
			{Kind: KindObject, Name: "Mirror", Fields: []Field{field("id", nn(tID))}},
			{
				Kind: KindInputObject,
				Name: "GroupUpdateInput",
				InputFields: []InputValue{
					{Name: "id", Type: nn(tID), Description: "The group to update."},
					{Name: "name", Type: tString},
				},
			},
			{
				Kind: KindInputObject,
				Name: "CustomUpdateInput",
				InputFields: []InputValue{
					{Name: "id", Type: nn(tID), Description: "The integration to update."},
					{Name: "space", Type: nn(tID)},
					{Name: "labels", Type: list(nn(tString))},
				},
			},
			{
				Kind: KindInputObject,
				Name: "MirrorUpdateInput",
				InputFields: []InputValue{
					{Name: "email", Type: nn(tString)},
					{Name: "custom", Type: input("CustomUpdateInput")},
				},
			},
			{
				Kind: KindInputObject,
				Name: "LinkInput",
				InputFields: []InputValue{
					{Name: "repo", Type: nn(tID), Description: "The repo to link."},
					{Name: "url", Type: nn(tString)},
				},
			},
			{
				Kind:   KindObject,
				Name:   "SearchStacksOutput",
				Fields: []Field{field("edges", nn(list(nn(obj("Stack")))))},
			},
			{
				Kind:        KindInputObject,
				Name:        "StackInput",
				Description: "Stack settings.",
				InputFields: []InputValue{
					{Name: "name", Type: nn(tString), Description: "The stack name."},
					{Name: "space", Type: tID},
					{Name: "workerPool", Type: tID, Description: "Worker pool ID."},
					{Name: "autodeploy", Type: nn(tBoolean), DefaultValue: strPtr("false")},
					{
						Name:         "state",
						Type:         named(KindEnum, "StackState"),
						DefaultValue: strPtr("READY"),
					},
					{Name: "vendor", Type: input("VendorInput")},
				},
			},
			{
				Kind:        KindInputObject,
				Name:        "VendorInput",
				InputFields: []InputValue{{Name: "terraformVersion", Type: tString}},
			},
			{
				Kind:        KindInputObject,
				Name:        "ContextInput",
				InputFields: []InputValue{{Name: "name", Type: nn(tString)}},
			},
			{
				Kind:        KindInputObject,
				Name:        "ScheduledRunInput",
				InputFields: []InputValue{{Name: "cron", Type: list(nn(tString))}},
			},
			{
				Kind:        KindInputObject,
				Name:        "SearchInput",
				InputFields: []InputValue{{Name: "query", Type: tString}},
			},
			{
				Kind:        KindEnum,
				Name:        "StackState",
				Description: "Stack state.",
				EnumValues: []EnumValue{
					{Name: "READY", Description: "Ready to run."},
					{Name: "OLD", IsDeprecated: true, DeprecationReason: "unused"},
				},
			},
			{Kind: KindScalar, Name: "Boolean"},
			{Kind: KindScalar, Name: "Float"},
			{Kind: KindScalar, Name: "ID"},
			{Kind: KindScalar, Name: "Int"},
			{Kind: KindScalar, Name: "String"},
		},
	}
	s.Normalize()
	return s
}

const fixtureMapping = `
rpcBudget: 1
plurals: {}
singletons: [account, oidc]
attributeArgs: [space]
operations:
  runTrigger:
    method: POST
    path: /stacks/{stackId}/runs
    params: {stackId: stack}
  logout:
    method: POST
    path: /auth/logout
    public: true
    tag: Auth
  repoLink:
    method: PUT
    path: /repos/{repoId}/link
    params: {repoId: link.repo}
    created: true
exclude:
  trackEvent: UI analytics.
terraform:
  spacelift_stack:
    enable_thing: noSuchField
`

func fixtureMappingParsed(t *testing.T) *Mapping {
	t.Helper()
	m, err := ParseMapping([]byte(fixtureMapping))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func fixtureBruno() *BrunoInventory {
	return &BrunoInventory{
		Folders: []BrunoFolder{
			{Path: "Stacks", Name: "Stacks", Docs: "Stack management."},
			{Path: "Advanced/SSO", Name: "SSO", Docs: "Single sign-on."},
		},
		Requests: []BrunoOperation{
			{
				Path:       "Stacks/Create Stack.bru",
				Folder:     "Stacks",
				Name:       "Create Stack",
				Operation:  "mutation",
				RootFields: []string{"stackCreate"},
				Docs:       "Create a stack.",
				Variables: json.RawMessage(
					`{"input":{"name":"demo","autodeploy":false},"manageState":true}`,
				),
			},
			{
				Path:       "Stacks/Get Stack.bru",
				Folder:     "Stacks",
				Name:       "Get Stack",
				Operation:  "query",
				RootFields: []string{"stack"},
			},
			{
				Path:       "Cookbook/Recipe.bru",
				Folder:     "Cookbook",
				Name:       "Recipe",
				Operation:  "query",
				RootFields: []string{"stack", "run"},
			},
			{
				Path:       "Danger Zone/Delete Stack.bru",
				Folder:     "Danger Zone",
				Name:       "Delete Stack",
				Operation:  "mutation",
				RootFields: []string{"stackDelete"},
			},
			{
				Path:       "Advanced/SSO/Create OIDC.bru",
				Folder:     "Advanced",
				Subfolder:  "SSO",
				Name:       "Create OIDC",
				Operation:  "mutation",
				RootFields: []string{"oidcCreate"},
			},
			{
				Path:       "Runs/Cancel Run.bru",
				Folder:     "Runs",
				Name:       "Cancel Run",
				Operation:  "mutation",
				RootFields: []string{"runCancel"},
				Variables:  json.RawMessage(`{"stack":"s","run":"r","note":"stop"}`),
			},
			{
				Path:       "Stacks/Schedule.bru",
				Folder:     "Stacks",
				Name:       "Schedule Run",
				Operation:  "mutation",
				RootFields: []string{"stackScheduledRunCreate"},
				Variables:  json.RawMessage(`{"stack":"s","input":{"cron":"0 9 * * 1"}}`),
			},
			{
				Path:       "Contexts/Attach.bru",
				Folder:     "Contexts",
				Name:       "Attach",
				Operation:  "mutation",
				RootFields: []string{"contextAttach"},
				Variables:  json.RawMessage(`{"other":1}`),
			},
			{
				Path:       "Groups/Update.bru",
				Folder:     "Groups",
				Name:       "Update Group",
				Operation:  "mutation",
				RootFields: []string{"groupUpdate"},
				Variables:  json.RawMessage(`{"input":{"id":"g1","name":"ops"}}`),
			},
			{
				Path:       "Repos/Update.bru",
				Folder:     "Repos",
				Name:       "Update Repo",
				Operation:  "mutation",
				RootFields: []string{"repoUpdate"},
				Variables:  json.RawMessage(`{"token":"t","custom":{"id":"r1","space":"root"}}`),
			},
		},
	}
}

func fixtureTerraform() (*TerraformSchema, *TerraformOperations) {
	tf := &TerraformSchema{
		Resources: map[string]TFBlock{
			"spacelift_stack": {Attributes: map[string]TFAttribute{
				"id":                {Computed: true},
				"name":              {Required: true, Description: "Stack name."},
				"space_id":          {Optional: true, Description: "Space of the stack."},
				"worker_pool_id":    {Optional: true},
				"enable_thing":      {Optional: true},
				"computed_only":     {Computed: true},
				"secret_wo_version": {Optional: true},
				"manage_state":      {Optional: true, Description: "Manage state."},
			}, BlockTypes: map[string]TFBlockType{"vendor": {NestingMode: "list"}}},
			"spacelift_context_attachment": {Attributes: map[string]TFAttribute{
				"context_id": {Required: true},
				"stack_id":   {Required: true, Description: "Stack to attach to."},
				"priority":   {Optional: true},
			}},
		},
		DataSources: map[string]TFBlock{"spacelift_stack": {}},
	}
	ops := &TerraformOperations{
		Resources: map[string][]string{
			"spacelift_stack":              {"stack", "stackCreate", "stackDelete", "stackUpdate"},
			"spacelift_context_attachment": {"contextAttach", "contextDetach"},
			"spacelift_not_in_schema":      {"stack"},
		},
		DataSources: map[string][]string{"spacelift_stack": {"stack"}},
	}
	return tf, ops
}

func fixtureLock() *Lock {
	return &Lock{
		Introspection: LockIntrospection{
			Endpoint:  "https://example.test/graphql",
			SHA256:    "abc",
			FetchedAt: "2026-10-28T06:58:57Z",
		},
		Bruno: LockRepo{
			Repository: "spacelift-io/spacelift-api-bruno",
			Ref:        "7cad4574954b569df083b2a809b68371fc385e6d",
			SHA256:     "def",
		},
		Provider: LockRepo{
			Repository: "spacelift-io/terraform-provider-spacelift",
			Ref:        "v1.57.0",
			SHA256:     "0123",
		},
	}
}

func fixtureSources(t *testing.T) *Sources {
	t.Helper()
	tf, ops := fixtureTerraform()
	return &Sources{
		Schema:       fixtureSchema(),
		Mapping:      fixtureMappingParsed(t),
		Bruno:        fixtureBruno(),
		Terraform:    tf,
		TerraformOps: ops,
		Lock:         fixtureLock(),
	}
}
