package combine

import (
	"encoding/json"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func nativeReceipts(t *testing.T, e *rego.Engine, kind policyapi.Type, bodies ...string) []policyapi.Receipt {
	t.Helper()
	requests := make([]rego.Request, len(bodies))
	for i, body := range bodies {
		requests[i] = rego.Request{Policy: rego.Policy{Type: rego.Type(kind), Engine: rego.V1, Body: "package infraweave\n" + body}, Input: json.RawMessage(`{}`)}
	}
	results := e.EvaluateAll(t.Context(), requests)
	out := make([]policyapi.Receipt, len(results))
	for i, result := range results {
		if result.Err != nil {
			t.Fatalf("native %s program %d: %v", kind, i, result.Err)
		}
		out[i] = policyapi.Receipt{Policy: kernel.NewULID(), Type: kind, Engine: string(rego.V1), Decision: result.Result.Decision}
	}
	return out
}

func TestUnitApprovalCombinationUsesRealOPAPrograms(t *testing.T) {
	e, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		bodies []string
		want   policyapi.Outcome
	}{
		{"policy-free", nil, policyapi.NoPolicies},
		{"attached-default", []string{""}, policyapi.Undecided},
		{"approve", []string{"approve := true"}, policyapi.Approve},
		{"reject", []string{"reject := true"}, policyapi.Reject},
		{"approval-note", []string{`approve_with_note contains "ready" if true`}, policyapi.Approve},
		{"rejection-note", []string{`reject_with_note contains "blocked" if true`}, policyapi.Reject},
		{"per-policy-conflict", []string{"approve := true\nreject := true"}, policyapi.Undecided},
		{"one-reject-defeats-approvals", []string{"approve := true", "approve := true", "reject := true"}, policyapi.Reject},
		{"every-policy-approves", []string{"approve := true", `approve_with_note contains "ready" if true`}, policyapi.Approve},
		{"one-undecided-keeps-waiting", []string{"approve := true", ""}, policyapi.Undecided},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := Approval(nativeReceipts(t, e, policyapi.Approval, test.bodies...))
			if got.Outcome != test.want || got.HasPolicies != (len(test.bodies) > 0) {
				t.Fatalf("approval combination: %+v, want %s", got, test.want)
			}
			if got.HasPolicies && (got.Approval == nil || got.Approval.Approve != (test.want == policyapi.Approve) || got.Approval.Reject != (test.want == policyapi.Reject)) {
				t.Fatalf("typed approval gate disagrees: %+v", got)
			}
		})
	}
}

func TestUnitApprovalErrorCannotBecomeApproval(t *testing.T) {
	for _, receipt := range []policyapi.Receipt{
		{Policy: kernel.NewULID(), Type: policyapi.Approval, Failure: &policyapi.Failure{Code: "TIMEOUT", Message: "Policy evaluation timed out."}},
		{Policy: kernel.NewULID(), Type: policyapi.Approval, Decision: json.RawMessage(`{"approve":"true"}`)},
		{Policy: kernel.NewULID(), Type: policyapi.Approval, Decision: json.RawMessage(`{"approve":null}`)},
	} {
		got := Approval([]policyapi.Receipt{receipt, {Policy: kernel.NewULID(), Type: policyapi.Approval, Decision: json.RawMessage(`{"approve":true}`)}})
		if got.Outcome != policyapi.Undecided || got.Approval == nil || got.Approval.Approve || len(got.Failures) != 1 {
			t.Fatalf("error silently approved: %+v", got)
		}
	}
}
