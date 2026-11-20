package combine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func fixtureDocument(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../../../test/fixtures", path))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixtureValue(t *testing.T, b []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func recordedCombination(kind policyapi.Type, raw json.RawMessage) policyapi.Evaluation {
	r := []policyapi.Receipt{{Policy: kernel.NewULID(), Type: kind, Engine: string(rego.V1), Decision: raw}}
	switch kind {
	case policyapi.Plan:
		return Plan(r)
	case policyapi.Approval:
		return Approval(r)
	case policyapi.Push:
		return Push(r)
	case policyapi.Trigger:
		return Trigger(r)
	case policyapi.Notification:
		return Notification(r)
	case policyapi.Login:
		return Login(r)
	default:
		return Negative(kind, r)
	}
}

func TestUnitRecordedDecisions(t *testing.T) {
	e, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	const root = "vendor-documents/opa/"
	paths, err := filepath.Glob("../../../../test/fixtures/" + root + "eval/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 16 {
		t.Fatalf("recorded OPA inventory changed: %d", len(paths))
	}
	for _, path := range paths {
		t.Run("opa/"+filepath.Base(path), func(t *testing.T) {
			name := strings.TrimSuffix(filepath.Base(path), ".json")
			prefix, _, _ := strings.Cut(name, "-")
			kind := map[string]policyapi.Type{"plan": policyapi.Plan, "approval": policyapi.Approval, "push": policyapi.Push, "trigger": policyapi.Trigger, "notification": policyapi.Notification}[prefix]
			body := fixtureDocument(t, root+"policies/"+prefix+".rego")
			body = bytes.ReplaceAll(body, []byte("spacelift"), []byte("infraweave"))
			inputPath := "policy-inputs/" + name + ".json"
			if name == "plan-proposed-deny" {
				inputPath = root + "inputs/" + name + ".json"
			}
			input := bytes.ReplaceAll(fixtureDocument(t, inputPath), []byte(`"spacelift"`), []byte(`"infraweave"`))
			result, err := e.Evaluate(t.Context(), rego.Request{Policy: rego.Policy{Type: rego.Type(kind), Engine: rego.V1, Body: string(body)}, Input: input})
			if err != nil {
				t.Fatal(err)
			}
			var vendor struct {
				Result []struct {
					Expressions []struct {
						Value map[string]json.RawMessage `json:"value"`
					} `json:"expressions"`
				} `json:"result"`
			}
			if err := json.Unmarshal(fixtureDocument(t, root+"eval/"+name+".json"), &vendor); err != nil {
				t.Fatal(err)
			}
			if len(vendor.Result) != 1 || len(vendor.Result[0].Expressions) != 1 {
				t.Fatal("unexpected recorded OPA result")
			}
			want := vendor.Result[0].Expressions[0].Value
			got := fixtureValue(t, result.Decision).(map[string]any)
			for key, value := range want {
				if actual, rule := got[key]; rule && !reflect.DeepEqual(actual, fixtureValue(t, value)) {
					t.Fatalf("OPA rule %s differs: got %v want %s", key, actual, value)
				}
			}
			if _, leaked := got["resources"]; leaked {
				t.Fatal("helper rule escaped preamble result")
			}
			combined := recordedCombination(kind, result.Decision)
			if len(combined.Failures) != 0 || combined.Outcome == policyapi.NoPolicies || !bytes.Equal(combined.Receipts[0].Decision, result.Decision) {
				t.Fatalf("recorded OPA decision lost: %+v", combined)
			}
			if name == "plan-tracked-warn" || name == "plan-proposed-deny" || name == "push-cancel" {
				expected := fixtureDocument(t, "policy-inputs/"+name+".decision.json")
				if !reflect.DeepEqual(got, fixtureValue(t, expected)) {
					t.Fatalf("full Spacelift decision differs: %s / %s", result.Decision, expected)
				}
			}
		})
	}
	for _, path := range []string{"rego/preamble-simulations.json", "rego/login-role-forms.json", "policy-inputs/provenance/trigger-defaults.json"} {
		var root struct {
			Exchanges []struct {
				Request struct {
					Variables struct {
						Input struct {
							Type   policyapi.Type  `json:"type"`
							Engine rego.EngineType `json:"engineType"`
							Body   string          `json:"body"`
							Input  string          `json:"input"`
						} `json:"input"`
					} `json:"variables"`
				} `json:"request"`
				Response struct {
					Data struct {
						Result string `json:"policySimulatev2"`
					} `json:"data"`
					Errors []json.RawMessage `json:"errors"`
				} `json:"response"`
			} `json:"exchanges"`
		}
		if err := json.Unmarshal(fixtureDocument(t, path), &root); err != nil {
			t.Fatal(err)
		}
		// The trigger provenance is one exchange rather than an exchange array.
		if len(root.Exchanges) == 0 {
			var one struct {
				Request  json.RawMessage `json:"request"`
				Response json.RawMessage `json:"response"`
			}
			if err := json.Unmarshal(fixtureDocument(t, path), &one); err != nil {
				t.Fatal(err)
			}
			wrapped := append([]byte(`{"exchanges":[`), fixtureDocument(t, path)...)
			wrapped = append(wrapped, ']', '}')
			if err := json.Unmarshal(wrapped, &root); err != nil {
				t.Fatal(err)
			}
		}
		for i, exchange := range root.Exchanges {
			t.Run(path+"/"+string(rune('a'+i)), func(t *testing.T) {
				in := exchange.Request.Variables.Input
				policy := rego.Policy{Type: rego.Type(in.Type), Engine: in.Engine, Body: strings.ReplaceAll(in.Body, "spacelift", "infraweave")}
				if len(exchange.Response.Errors) > 0 {
					if err := e.Check(t.Context(), policy); err == nil {
						t.Fatal("accepted live-rejected rule shape")
					}
					return
				}
				result, err := e.Evaluate(t.Context(), rego.Request{Policy: policy, Input: json.RawMessage(in.Input)})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(fixtureValue(t, result.Decision), fixtureValue(t, []byte(exchange.Response.Data.Result))) {
					t.Fatalf("captured full preamble decision differs: %s", result.Decision)
				}
				if in.Type == policyapi.Type("ACCESS") {
					return
				}
				got := recordedCombination(in.Type, result.Decision)
				if len(got.Failures) != 0 {
					t.Fatalf("captured decision rejected by combination: %+v", got)
				}
			})
		}
	}
}
