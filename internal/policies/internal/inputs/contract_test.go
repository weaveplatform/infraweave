package inputs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func jsonValue(t *testing.T, b []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// namespace maps object keys recursively, including objects in arrays; string
// values such as triggered_by are deliberately untouched.
func namespace(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, value := range v {
			if k == "spacelift" {
				k = "infraweave"
			}
			out[k] = namespace(value)
		}
		return out
	case []any:
		for i := range v {
			v[i] = namespace(v[i])
		}
	}
	return v
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../../../test/fixtures/policy-inputs", name))
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(namespace(jsonValue(t, b)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func contractCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	c, err := Contract()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://infraweave.test/policy-contract.json", jsonValue(t, b)); err != nil {
		t.Fatal(err)
	}
	return compiler
}

func compile(t *testing.T, c *jsonschema.Compiler, name string) *jsonschema.Schema {
	t.Helper()
	schema, err := c.Compile("https://infraweave.test/policy-contract.json#/$defs/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func fixtureType(name string) reflect.Type {
	decision := strings.HasSuffix(name, ".decision.json")
	switch {
	case strings.HasPrefix(name, "push-"):
		if decision {
			return reflect.TypeFor[PushDecision]()
		}
		return reflect.TypeFor[PushInput]()
	case strings.HasPrefix(name, "approval-"):
		return reflect.TypeFor[ApprovalInput]()
	case strings.HasPrefix(name, "plan-"):
		if decision {
			return reflect.TypeFor[PlanDecision]()
		}
		return reflect.TypeFor[PlanInput]()
	case strings.HasPrefix(name, "trigger-"):
		return reflect.TypeFor[TriggerInput]()
	case strings.HasPrefix(name, "notification-"):
		return reflect.TypeFor[NotificationInput]()
	}
	return nil
}

func TestUnitPolicyContract(t *testing.T) {
	c := contractCompiler(t)
	files, err := filepath.Glob("../../../../test/fixtures/policy-inputs/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 21 {
		t.Fatalf("found %d documents, want all 21 recorded inputs and decisions", len(files))
	}
	for _, file := range files {
		name := filepath.Base(file)
		t.Run(name, func(t *testing.T) {
			typ := fixtureType(name)
			if typ == nil {
				t.Fatal("fixture has no typed contract")
			}
			data := fixture(t, name)
			schema := compile(t, c, typ.Name())
			if err := schema.Validate(jsonValue(t, data)); err != nil {
				t.Fatalf("fixture disagrees with generated schema: %v", err)
			}
			value := reflect.New(typ).Interface()
			if err := decode(data, value); err != nil {
				t.Fatalf("fixture disagrees with typed input: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(jsonValue(t, data), jsonValue(t, encoded)) {
				t.Fatalf("typed round trip loses keys, types or presence\nwant %s\n got %s", data, encoded)
			}
		})
	}
}

func TestUnitPolicyContractRefusals(t *testing.T) {
	c := contractCompiler(t)
	for _, tc := range []struct {
		name, file string
		typ        reflect.Type
		mutate     func(map[string]any)
	}{
		{"missing nested field", "plan-tracked-warn.json", reflect.TypeFor[PlanInput](), func(v map[string]any) { delete(v["infraweave"].(map[string]any)["stack"].(map[string]any), "id") }},
		{"null required id", "plan-tracked-warn.json", reflect.TypeFor[PlanInput](), func(v map[string]any) { v["infraweave"].(map[string]any)["stack"].(map[string]any)["id"] = nil }},
		{"wrong nested type", "trigger-finished.json", reflect.TypeFor[TriggerInput](), func(v map[string]any) { v["stacks"].([]any)[0].(map[string]any)["queued_runs"] = "0" }},
		{"unknown nested field", "approval-queued-approve.json", reflect.TypeFor[ApprovalInput](), func(v map[string]any) {
			v["reviews"].(map[string]any)["current"].(map[string]any)["approvals"].([]any)[0].(map[string]any)["request"].(map[string]any)["unmodeled"] = true
		}},
		{"missing state version", "approval-queued-approve.json", reflect.TypeFor[ApprovalInput](), func(v map[string]any) {
			delete(v["reviews"].(map[string]any)["current"].(map[string]any)["approvals"].([]any)[0].(map[string]any), "state_version")
		}},
		{"unknown decision rule", "push-cancel.decision.json", reflect.TypeFor[PushDecision](), func(v map[string]any) { v["unmodeled"] = true }},
		{"wrong sensitivity mask", "plan-tracked-warn.json", reflect.TypeFor[PlanInput](), func(v map[string]any) {
			v["terraform"].(map[string]any)["resource_changes"].([]any)[0].(map[string]any)["change"].(map[string]any)["before_sensitive"] = "secret"
		}},
		{"wrong decision rule type", "plan-tracked-warn.decision.json", reflect.TypeFor[PlanDecision](), func(v map[string]any) { v["warn"] = true }},
		{"upstream namespace", "plan-tracked-warn.json", reflect.TypeFor[PlanInput](), func(v map[string]any) { v["spacelift"] = v["infraweave"]; delete(v, "infraweave") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := jsonValue(t, fixture(t, tc.file)).(map[string]any)
			tc.mutate(value)
			if err := compile(t, c, tc.typ.Name()).Validate(value); err == nil {
				t.Fatal("generated schema accepts an invalid contract")
			}
			// Unknown keys and JSON type errors must fail the typed decoder too;
			// required-key/null rules belong to the schema because Field retains them.
			if strings.HasPrefix(tc.name, "unknown") || strings.HasPrefix(tc.name, "wrong") || tc.name == "upstream namespace" {
				b, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := decode(b, reflect.New(tc.typ).Interface()); err == nil {
					t.Fatal("typed decode accepts unknown key/type")
				}
			}
		})
	}
}

func TestUnitPolicyContractPresence(t *testing.T) {
	for _, previous := range []string{"", `,"previous_run":null`} {
		var p PlanInput
		b := []byte(`{"infraweave":{"commit":{},"request":{},"run":{},"stack":{}` + previous + `},"terraform":null}`)
		if err := decode(b, &p); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(jsonValue(t, b), jsonValue(t, out)) {
			t.Fatalf("presence changed: %s", out)
		}
		if bytes.Contains(out, []byte("infracost")) || bytes.Contains(out, []byte("terragrunt")) {
			t.Fatalf("future values synthesized: %s", out)
		}
	}
	var p PushInput
	if err := decode(fixture(t, "push-pull-request-opened.json"), &p); err != nil {
		t.Fatal(err)
	}
	if p.VCSIntegration.Present() {
		t.Fatal("PR event gained absent vcs_integration")
	}
	// A schema key rewrite must not alter marker strings inside a document.
	v := namespace(map[string]any{"items": []any{map[string]any{"spacelift": map[string]any{"triggered_by": "policy/stack/run"}}}})
	if got := v.(map[string]any)["items"].([]any)[0].(map[string]any)["infraweave"].(map[string]any)["triggered_by"]; got != "policy/stack/run" {
		t.Fatal(got)
	}
}

func TestUnitPolicyContractFutureInputs(t *testing.T) {
	c := contractCompiler(t)
	for typ, doc := range map[string]string{
		"LoginInput":        `{"request":{"timestamp_ns":1},"session":{"machine":false,"teams":[],"member":true},"spaces":[],"roles":[]}`,
		"AccessInput":       `{"request":{},"session":{"machine":false,"teams":[]},"module":{"administrative":false,"branch":"main","id":"mod","name":"mod","namespace":"org","project_root":"","repository":"repo","terraform_provider":"aws"}}`,
		"TaskInput":         `{"request":{"command":"echo hello","timestamp_ns":1},"session":{"machine":false,"teams":[]},"stack":{"administrative":false,"autodeploy":false,"branch":"main","id":"stack","name":"stack","namespace":"org","project_root":"","repository":"repo","state":"FINISHED"}}`,
		"IntentInput":       `{"resource":{"resource_id":"a","provider":"hashicorp/aws","resource_type":"aws_instance","operation":"create","current_state":null,"proposed_state":{"arbitrary":true}},"reviews":{"allow":[{"author":"alice","request":{"timestamp_ns":1},"session":{"machine":false,"teams":[]}}],"deny":[]}}`,
		"NotificationInput": `{"account":{"name":"org"},"webhook_endpoints":[],"worker_usage":{"severity":"WARNING","message":"limit","contracted_limit":1,"contracted_percentile":95,"concurrency_at_contracted_percentile":1,"previous_concurrency_at_contracted_percentile":1,"limit_ratio":1.5,"previous_limit_ratio":1,"burst_budget_minutes":1,"burst_used_minutes":1,"burst_remaining_minutes":0,"burst_ratio":1,"previous_burst_used_minutes":0,"previous_burst_ratio":0,"cycle_start":1,"cycle_end":2}}`,
	} {
		t.Run(typ, func(t *testing.T) {
			if err := compile(t, c, typ).Validate(jsonValue(t, []byte(doc))); err != nil {
				t.Fatal(err)
			}
		})
	}
	contract, err := Contract()
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.PolicyTypes) != 10 {
		t.Fatalf("missing typed future contracts: %v", contract.PolicyTypes)
	}
	for _, name := range []string{"Module", "Version", "WorkerUsage", "IntentResource", "CloudFormationPlan", "OpenTofuPlan", "PulumiPlan", "AnsiblePlan", "TerragruntProject", "ThirdPartyMetadata"} {
		if contract.Defs[name] == nil {
			t.Errorf("missing %s", name)
		}
	}
	// Exclusivity is part of the schema, rather than a permissive merged map.
	for typ, doc := range map[string]string{"LoginInput": `{"request":{},"session":{},"slack":{}}`, "NotificationInput": `{"account":{"name":"a"},"webhook_endpoints":[],"internal_error":{},"run_updated":{}}`, "TriggerInput": `{"stacks":[]}`} {
		if err := compile(t, c, typ).Validate(jsonValue(t, []byte(doc))); err == nil {
			t.Errorf("%s accepts conflicting/incomplete alternatives", typ)
		}
	}
}
