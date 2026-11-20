package combine

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func TestUnitPlanCombinationUsesRealOPAPrograms(t *testing.T) {
	e, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	programs := []string{
		"package infraweave\ndeny contains \"blocked\" if true\nwarn contains \"review\" if true\nflag contains \"owner-a\" if true\n",
		"package infraweave\nwarn contains \"review\" if true\ninfo contains \"checked\" if true\nreset_flag contains \"old\" if true\nsample := true\n",
	}
	receipts := make([]policyapi.Receipt, 0, len(programs))
	for _, body := range programs {
		out, err := e.Evaluate(t.Context(), rego.Request{Policy: rego.Policy{Type: rego.Plan, Engine: rego.V1, Body: body}, Input: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, policyapi.Receipt{Policy: kernel.NewULID(), Type: policyapi.Plan, Decision: out.Decision, Engine: string(rego.V1)})
	}
	got := Plan(receipts)
	if !got.HasPolicies || got.Outcome != policyapi.Deny || got.Plan == nil || !slices.Equal(got.Plan.Deny, []string{"blocked"}) ||
		!slices.Equal(got.Plan.Warn, []string{"review"}) ||
		!slices.Equal(got.Plan.Info, []string{"checked"}) {
		t.Fatalf("one deny did not defeat independent successful programs: %+v", got)
	}
	if len(got.Receipts) != 2 || !slices.Equal(got.Receipts[0].Flags, []string{"owner-a"}) || !slices.Equal(got.Receipts[1].ResetFlags, []string{"old"}) ||
		!got.Receipts[1].Sample {
		t.Fatalf("policy ownership or sample decision lost: %+v", got.Receipts)
	}
}

func TestUnitPlanNoPoliciesDiffersFromNoMatchingRule(t *testing.T) {
	empty := Plan(nil)
	attached := Plan(
		[]policyapi.Receipt{
			{Policy: kernel.NewULID(), Type: policyapi.Plan, Decision: json.RawMessage(`{"deny":[],"warn":[],"info":[],"flag":[],"reset_flag":[],"sample":false}`)},
		},
	)
	if empty.HasPolicies || empty.Outcome != policyapi.NoPolicies || len(empty.Receipts) != 0 {
		t.Fatalf("policy-free behavior was replaced: %+v", empty)
	}
	if !attached.HasPolicies || attached.Outcome != policyapi.Allow || attached.Plan == nil {
		t.Fatalf("attached default decision: %+v", attached)
	}
}

func TestUnitPlanErrorsNeverBecomeAllow(t *testing.T) {
	for _, raw := range []string{"", `null`, `[]`, `{"deny":[false]}`, `{"warn":null}`, `{"sample":null}`, `{"unknown":true}`, `{} {}`} {
		got := Plan([]policyapi.Receipt{{Policy: kernel.NewULID(), Type: policyapi.Plan, Decision: json.RawMessage(raw)}})
		if !got.HasPolicies || got.Outcome != policyapi.Deny || len(got.Failures) != 1 || got.Receipts[0].Failure == nil || got.Receipts[0].Failure.Code != "RESULT" {
			t.Fatalf("invalid result became allow: %q %+v", raw, got)
		}
	}
	got := Plan(
		[]policyapi.Receipt{
			{
				Policy:     kernel.NewULID(),
				Type:       policyapi.Plan,
				Flags:      []string{"stale"},
				ResetFlags: []string{"stale-reset"},
				Failure:    &policyapi.Failure{Code: "TIMEOUT", Message: "Policy evaluation timed out."},
			},
		},
	)
	if got.Outcome != policyapi.Deny || len(got.Failures) != 1 || got.Failures[0].Code != "TIMEOUT" || len(got.Receipts[0].Flags) != 0 || len(got.Receipts[0].ResetFlags) != 0 {
		t.Fatalf("timeout was replaced or allowed: %+v", got)
	}
}

func TestUnitPlanCombinationDoesNotAliasCallerEvidence(t *testing.T) {
	a, b := kernel.NewULID(), kernel.NewULID()
	raw := json.RawMessage(`{"flag":["generated"],"deny":["z","a","a"],"warn":["w"]}`)
	input := []policyapi.Receipt{{Policy: b, Type: policyapi.Plan, Decision: raw, Flags: []string{"original"}}, {Policy: a, Type: policyapi.Plan, Decision: json.RawMessage(`{}`)}}
	got := Plan(input)
	if got.Receipts[0].Policy != a || !slices.Equal(got.Plan.Deny, []string{"a", "z"}) {
		t.Fatalf("unstable combination: %+v", got)
	}
	got.Receipts[1].Decision[0] = '['
	got.Receipts[1].Flags[0] = "changed"
	if raw[0] != '{' || input[0].Flags[0] != "original" || input[0].Outcome != "" {
		t.Fatalf("combination changed caller evidence: %+v", input)
	}
}
