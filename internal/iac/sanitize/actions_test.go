package sanitize_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

func actionFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../test/fixtures/plan-payloads/terraform-actions/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func actionDocument(t *testing.T, raw []byte) (map[string]any, map[string]any) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, doc["action_invocations"].([]any)[0].(map[string]any)
}

func TestUnitActualActionPlan(t *testing.T) {
	raw := actionFixture(t)
	for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
		t.Run(string(mode), func(t *testing.T) {
			var out bytes.Buffer
			if err := sanitize.Plan(&out, bytes.NewReader(raw), mode); err != nil {
				t.Fatal(err)
			}
			if mode == sanitize.None && !bytes.Equal(raw, out.Bytes()) {
				t.Fatal("NONE changed the actual tool document")
			}
			_, original := actionDocument(t, raw)
			_, got := actionDocument(t, out.Bytes())
			values := got["config_values"].(map[string]any)
			args := values["arguments"].([]any)
			env := values["environment"].(map[string]any)
			secret, public := "dummy-action-secret-675", "visible-675"
			if mode != sanitize.None {
				secret = sanitize.Value(secret).(string)
			}
			if mode == sanitize.Default {
				public = sanitize.Value(public).(string)
			}
			if values["stdin"] != secret || args[1] != secret || env["private"] != secret || env["copy"] != secret {
				t.Fatal("sensitive action strings or their unmarked copies escaped sanitization")
			}
			if args[0] != public || env["public"] != public || args[2] != nil || values["working_directory"] != nil {
				t.Fatal("public values or unknown/null slots changed incorrectly")
			}
			delete(original, "config_values")
			delete(got, "config_values")
			if !reflect.DeepEqual(original, got) {
				t.Fatal("action identity, trigger metadata or boolean masks changed")
			}
		})
	}
}

func TestUnitActionRefusals(t *testing.T) {
	const secret = "dummy-action-secret-675"
	cases := map[string]func(map[string]any){
		"future value field":       func(a map[string]any) { a[secret] = secret },
		"invalid values root":      func(a map[string]any) { a["config_values"] = secret },
		"invalid identity":         func(a map[string]any) { a["provider_name"] = false },
		"invalid sensitivity":      func(a map[string]any) { a["config_sensitive"] = secret },
		"invalid unknown mask":     func(a map[string]any) { a["config_unknown"] = secret },
		"wrong mask array":         func(a map[string]any) { a["config_sensitive"] = map[string]any{"arguments": []any{true}} },
		"trigger value":            func(a map[string]any) { a["lifecycle_action_trigger"] = secret },
		"future trigger field":     func(a map[string]any) { a["lifecycle_action_trigger"].(map[string]any)[secret] = secret },
		"trigger string index":     func(a map[string]any) { a["lifecycle_action_trigger"].(map[string]any)["actions_list_index"] = secret },
		"trigger fractional index": func(a map[string]any) { a["lifecycle_action_trigger"].(map[string]any)["actions_list_index"] = 1.5 },
		"trigger negative index":   func(a map[string]any) { a["lifecycle_action_trigger"].(map[string]any)["actions_list_index"] = -1 },
		"trigger metadata type":    func(a map[string]any) { a["lifecycle_action_trigger"].(map[string]any)["on_failure"] = true },
		"invoke type":              func(a map[string]any) { a["invoke_action_trigger"] = secret },
		"invoke identity type":     func(a map[string]any) { a["invoke_action_trigger"] = map[string]any{"calling_resource_address": 1} },
		"future invoke field":      func(a map[string]any) { a["invoke_action_trigger"] = map[string]any{secret: secret} },
	}
	for name, mutate := range cases {
		for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
			t.Run(name+"/"+string(mode), func(t *testing.T) {
				doc, a := actionDocument(t, actionFixture(t))
				mutate(a)
				raw, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				err = sanitize.Plan(&out, bytes.NewReader(raw), mode)
				if !errors.Is(err, sanitize.ErrMalformed) && !errors.Is(err, sanitize.ErrUnsupported) {
					t.Fatalf("invalid action was accepted: %v", err)
				}
				if out.Len() != 0 || strings.Contains(err.Error(), secret) {
					t.Fatal("refusal wrote or echoed raw action values")
				}
			})
		}
	}
}
