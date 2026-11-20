package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

// Committed receipt fixtures isolate the projection boundary; real OPA and
// worker planning reports are covered by features/policies/plan.feature.
func TestAccPlanMessagesRemainHistoricalAcrossPolicyFreeReplans(t *testing.T) {
	w := newWorld(t)
	w.lc = lifecycle.New(lifecycle.Deps{
		Store: w.store, Stacks: w.stacks, Events: w.events, Jobs: w.jobs, Evidence: w.store, Plans: w.store,
		Now: func() time.Time { return w.now },
	})
	run := w.unconfirmed(t, "plan-messages")
	first, second := policyReceipt(w, kernel.NewULID()), policyReceipt(w, kernel.NewULID())
	first.Type, second.Type = policyapi.Plan, policyapi.Plan
	first.Outcome, second.Outcome = policyapi.Allow, policyapi.Allow
	first.Decision = json.RawMessage(`{"deny":[],"warn":["z","common"],"info":["informational"],"private_field":"private-plan-marker"}`)
	second.Decision = json.RawMessage(`{"deny":[],"warn":["a","common"],"info":[]}`)
	unrelated := policyReceipt(w, kernel.NewULID())
	unrelated.Decision = json.RawMessage(`{"warn":"private-plan-marker"}`)
	if err := w.in(t, func(ctx context.Context) error {
		if err := w.store.RecordPolicyEvidence(ctx, w.acct, run.ID, run.Version, []policyapi.Receipt{first, unrelated, second}); err != nil {
			return err
		}
		return w.lc.Refresh(ctx, run)
	}); err != nil {
		t.Fatal(err)
	}
	read := func() {
		t.Helper()
		outcomes, err := w.store.PlanOutcomes(t.Context(), w.acct, []kernel.ULID{run.ID})
		if err != nil || len(outcomes[run.ID]) != 1 {
			t.Fatalf("missing committed outcome group: %v", err)
		}
		out := outcomes[run.ID][0]
		if out.ID != first.ID || out.StateVersion != 6 || !reflect.DeepEqual(out.Warn, []string{"a", "common", "z"}) ||
			!reflect.DeepEqual(out.Info, []string{"informational"}) || len(out.Deny) != 0 {
			t.Fatalf("plan grouping lost identity/messages: %+v", out)
		}
	}
	read()
	updates := w.updates(t, run.ID)
	if last := updates[len(updates)-1].Doc.PlanPolicyDecision; last == nil || !reflect.DeepEqual(last.Warn, []string{"a", "common", "z"}) {
		t.Fatal("notification missed committed plan messages")
	}
	raw, err := json.Marshal(updates[len(updates)-1].Doc)
	if err != nil || strings.Contains(string(raw), "private-plan-marker") {
		t.Fatal("raw decision escaped its public notification projection")
	}
	if foreign, err := w.store.PlanOutcomes(t.Context(), kernel.NewAccountID(), []kernel.ULID{run.ID}); err != nil || len(foreign) != 0 {
		t.Fatal("plan messages crossed an account")
	}
	replan := func() {
		t.Helper()
		run = w.mustMove(t, run, kernel.RunStateReplanRequested, machine.DriverAPI)
		run = w.mustMove(t, run, kernel.RunStatePreparingReplan, machine.DriverWorkerClaim)
		run = w.mustMove(t, run, kernel.RunStatePlanning, machine.DriverWorkerReport)
	}
	replan()
	outcomes, err := w.store.PlanOutcomes(t.Context(), w.acct, []kernel.ULID{run.ID})
	if err != nil || len(outcomes[run.ID]) != 1 || outcomes[run.ID][0].Current {
		t.Fatal("a new planning version inherited the previous policy decision")
	}
	run = w.mustMove(t, run, kernel.RunStateUnconfirmed, machine.DriverDecision)
	updates = w.updates(t, run.ID)
	if updates[len(updates)-1].Doc.PlanPolicyDecision != nil {
		t.Fatal("policy-free replan retained an earlier notification warning")
	}
	read()
	replan()
	failure := policyReceipt(w, kernel.NewULID())
	failure.Type, failure.Outcome = policyapi.Plan, policyapi.Deny
	failure.Decision = json.RawMessage(`{"deny":"private-plan-marker","warn":42}`)
	failure.Failure = &policyapi.Failure{Code: "RESULT", Message: "Policy returned an invalid decision."}
	if err := w.in(t, func(ctx context.Context) error {
		var err error
		run, err = w.lc.Transition(ctx, lifecycle.Request{
			Account: w.acct, Run: run.ID, Version: run.Version, To: kernel.RunStateFailed, Driver: machine.DriverDecision,
			Note: "denied by plan policy", Receipts: []policyapi.Receipt{failure},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	outcomes, err = w.store.PlanOutcomes(t.Context(), w.acct, []kernel.ULID{run.ID})
	if err != nil || len(outcomes[run.ID]) != 2 || outcomes[run.ID][0].Current || !outcomes[run.ID][1].Current ||
		!reflect.DeepEqual(outcomes[run.ID][1].Deny, []string{failure.Failure.Message}) || len(outcomes[run.ID][1].Warn) != 0 {
		t.Fatalf("failed program did not expose only its safe diagnostic: %+v %v", outcomes, err)
	}
	updates = w.updates(t, run.ID)
	if last := updates[len(updates)-1].Doc.PlanPolicyDecision; last == nil || !reflect.DeepEqual(last.Deny, []string{failure.Failure.Message}) {
		t.Fatal("failed program diagnostic was not committed with its notification")
	}
	if _, err := w.db.Exec(t.Context(), `UPDATE run_policy_receipts SET decision='{"deny":null,"warn":[],"info":[]}' WHERE account_id=$1 AND id=$2`, w.acct, first.ID); err != nil {
		t.Fatal(err)
	}
	if partial, err := w.store.PlanOutcomes(t.Context(), w.acct, []kernel.ULID{run.ID}); !errors.Is(err, kernel.ErrUnavailable) || partial != nil {
		t.Fatal("corrupt message projection returned partial history")
	}
	w.db.Close()
	if _, err := w.store.PlanOutcomes(t.Context(), w.acct, []kernel.ULID{run.ID}); err == nil {
		t.Fatal("unavailable message storage looked like a policy-free run")
	}
}
