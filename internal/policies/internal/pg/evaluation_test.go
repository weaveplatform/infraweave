package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func evaluationInput(t *testing.T, file string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("../../../../test/fixtures/policy-inputs/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if v, ok := doc["spacelift"]; ok {
		doc["infraweave"] = v
		delete(doc, "spacelift")
	}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAccEvaluationStoredProgramsAndCallerRollback(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: "Deny with evidence", Type: rego.Plan, Body: `package infraweave
deny contains "blocked" if true
flag contains "seen" if true
sample := true`, Labels: []string{"autoattach:*"}})
	if err != nil {
		t.Fatal(err)
	}
	req := policyapi.EvaluationRequest{
		Account: p.Account,
		Type:    policyapi.Plan,
		Target:  policyapi.Target{Kind: policyapi.StackTarget, ID: f.stacks.stack.ID},
		Input:   evaluationInput(t, "plan-tracked-warn.json"),
	}
	before := f.count(t, "events")
	abort := errors.New("caller rolls back")
	err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		got, err := f.svc.Evaluate(ctx, req)
		if err != nil {
			return err
		}
		if got.Outcome != policyapi.Deny || len(got.Receipts) != 1 || got.Receipts[0].Policy != p.ID || got.Receipts[0].PolicyName != p.Name ||
			got.Receipts[0].PolicyVersion != p.Version ||
			len(got.Receipts[0].Flags) != 1 {
			return fmt.Errorf("stored policy was not evaluated: %+v", got)
		}
		var n int
		if err := tx.Q(ctx, f.db).QueryRow(ctx, "SELECT count(*) FROM events WHERE account_id=$1", p.Account).Scan(&n); err != nil {
			return err
		}
		if n <= before {
			return errors.New("evaluation did not append a transactional event")
		}
		return abort
	})
	if !errors.Is(err, abort) || f.count(t, "events") != before {
		t.Fatalf("caller rollback lost: %v", err)
	}
	got, err := f.svc.Evaluate(t.Context(), req)
	if err != nil || got.Outcome != policyapi.Deny || f.count(t, "events") != before+1 {
		t.Fatalf("committed evaluation: %+v %v", got, err)
	}
	if got.Receipts[0].ID.IsZero() || got.Receipts[0].CreatedAt != f.now {
		t.Fatalf("receipt identity/time missing: %+v", got.Receipts[0])
	}
	wrong := req
	wrong.Account = kernel.NewAccountID()
	if _, err := f.svc.Evaluate(t.Context(), wrong); err == nil {
		t.Fatal("foreign stack target was accepted")
	}
}
