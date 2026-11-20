package pg_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccEvaluationMixedRegoDialectsKeepSelectedVersionSnapshot(t *testing.T) {
	f := newFixture(t)
	old, err := f.svc.Create(
		t.Context(),
		f.principal,
		core.Input{Name: "Classic dialect", Type: rego.Plan, Engine: rego.V0, Labels: []string{"autoattach:*"}, Body: `package infraweave
helper := "classic"
warn[helper] { sanitized("hello") == "938b9824" }
flag["classic-owner"] { true }`},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(
		t.Context(),
		f.principal,
		core.Input{Name: "Current dialect", Type: rego.Plan, Engine: rego.V1, Labels: []string{"autoattach:*"}, Body: `package infraweave
helper := "current"
deny contains helper if true
flag contains "current-owner" if true`},
	); err != nil {
		t.Fatal(err)
	}
	req := fixtureEvaluationRequest(f, policyapi.Plan, nil)
	raw := evaluationInput(t, "plan-tracked-warn.json")
	req.BuildInput = func(ctx context.Context) (json.RawMessage, error) {
		_, err := f.svc.Update(
			ctx,
			f.principal,
			old.ID.String(),
			kernel.Expect{},
			core.Input{
				Name:   "Changed while building",
				Type:   rego.Plan,
				Engine: rego.V1,
				Labels: []string{"autoattach:*"},
				Body:   "package infraweave\nwarn contains \"updated\" if true",
			},
		)
		return raw, err
	}
	got, err := f.svc.Evaluate(t.Context(), req)
	if err != nil || got.Outcome != policyapi.Deny || len(got.Failures) != 0 || len(got.Receipts) != 2 || len(got.Plan.Warn) != 1 || got.Plan.Warn[0] != "classic" ||
		got.Plan.Deny[0] != "current" {
		t.Fatalf("mixed native dialect decisions: %+v %v", got, err)
	}
	if got.Receipts[0].Policy != old.ID || got.Receipts[0].Engine != string(rego.V0) || got.Receipts[0].PolicyVersion != 1 || got.Receipts[0].PolicyName != old.Name ||
		got.Receipts[1].Engine != string(rego.V1) {
		t.Fatalf("selected metadata drifted during input building: %+v", got.Receipts)
	}
	stored, err := f.store.Get(t.Context(), old.Account, old.ID, false)
	if err != nil || stored.Engine != rego.V1 || stored.Version != 2 {
		t.Fatalf("concurrent version did not commit: %+v %v", stored, err)
	}
	req.BuildInput = nil
	req.Input = raw
	got, err = f.svc.Evaluate(t.Context(), req)
	if err != nil || len(got.Failures) != 0 || got.Plan.Warn[0] != "updated" || got.Receipts[0].PolicyVersion != 2 || got.Receipts[0].Engine != string(rego.V1) {
		t.Fatalf("next evaluation did not select the new version: %+v %v", got, err)
	}
}
