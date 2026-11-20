package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
)

type approvalEvaluation func(context.Context, lifecycle.Run) (policyapi.Evaluation, error)

func (f approvalEvaluation) Evaluate(ctx context.Context, r lifecycle.Run) (policyapi.Evaluation, error) {
	return f(ctx, r)
}

// boundApprovals uses controlled decision outcomes to verify the lifecycle and
// PostgreSQL transaction boundary independently of the real OPA API scenarios.
func (w *world) boundApprovals(evaluate func(lifecycle.Run) policyapi.Evaluation) *lifecycle.Lifecycle {
	return lifecycle.New(lifecycle.Deps{
		Store: w.store, Stacks: w.stacks, Events: w.events, Jobs: w.jobs, Evidence: w.store,
		Now: func() time.Time { return w.now }, Approvals: approvalEvaluation(func(ctx context.Context, r lifecycle.Run) (policyapi.Evaluation, error) {
			out := evaluate(r)
			if err := w.store.RecordApproval(ctx, r.Account, r.ID, evidence.Approval{
				State: r.State, StateVersion: r.Version,
				HasPolicies: out.HasPolicies, Outcome: out.Outcome, At: w.now,
			}); err != nil {
				return policyapi.Evaluation{}, err
			}
			if err := w.store.RecordPolicyEvidence(ctx, r.Account, r.ID, r.Version, out.Receipts); err != nil {
				return policyapi.Evaluation{}, err
			}
			return out, nil
		}),
	})
}

func approvalOutcome(w *world, outcome policyapi.Outcome) policyapi.Evaluation {
	return policyapi.Evaluation{
		Type: policyapi.Approval, HasPolicies: true, Outcome: outcome,
		Receipts: []policyapi.Receipt{{
			ID: kernel.NewULID(), Policy: kernel.NewULID(), PolicyName: "Approval gate", PolicyVersion: 1,
			Type: policyapi.Approval, Engine: "REGO_V1", Outcome: outcome, CreatedAt: w.now,
		}},
	}
}

func TestAccApprovalSchedulerDoesNotAcquireWaitingRunLocks(t *testing.T) {
	w := newWorld(t)
	proposed := mustCreate(t, w, kernel.RunTypeProposed, "approval-proposed")
	tracked := mustCreate(t, w, kernel.RunTypeTracked, "approval-tracked")
	task := mustCreate(t, w, kernel.RunTypeTask, "approval-task")
	decisions := map[kernel.ULID]policyapi.Outcome{proposed.ID: policyapi.Undecided, tracked.ID: policyapi.Undecided, task.ID: policyapi.Undecided}
	w.lc = w.boundApprovals(func(r lifecycle.Run) policyapi.Evaluation { return approvalOutcome(w, decisions[r.ID]) })
	s := schedule.New(schedule.Deps{DB: w.db, Store: w.store, Stacks: w.stacks, Lifecycle: w.lc, Queue: w.jobs})
	walk := func() {
		t.Helper()
		if err := w.in(t, func(ctx context.Context) error { return s.Schedule(ctx, w.acct, w.stack) }); err != nil {
			t.Fatal(err)
		}
	}
	walk()
	if _, held, err := w.store.Holder(t.Context(), w.acct, w.stack); err != nil || held {
		t.Fatalf("waiting run retained stack lock: %v %v", held, err)
	}
	for _, r := range []lifecycle.Run{proposed, tracked, task} {
		stored, err := w.store.Get(t.Context(), w.acct, r.ID)
		if err != nil || stored.State != kernel.RunStateQueued || stored.Version != 1 {
			t.Fatalf("undecided run advanced: %+v %v", stored, err)
		}
		decisions[r.ID] = policyapi.Approve
	}
	walk()
	if holder, held, err := w.store.Holder(t.Context(), w.acct, w.stack); err != nil || !held || holder != tracked.ID {
		t.Fatalf("approved lock ordering: %s %v %v", holder, held, err)
	}
	for _, r := range []lifecycle.Run{proposed, tracked} {
		stored, err := w.store.Get(t.Context(), w.acct, r.ID)
		if err != nil || stored.State != kernel.RunStateReady {
			t.Fatalf("approved run did not advance: %+v %v", stored, err)
		}
	}
	decisions[task.ID] = policyapi.Reject
	walk()
	stored, err := w.store.Get(t.Context(), w.acct, task.ID)
	if err != nil || stored.State != kernel.RunStateFailed {
		t.Fatalf("rejected queued task: %+v %v", stored, err)
	}
	history, err := w.store.Histories(t.Context(), w.acct, []kernel.ULID{task.ID})
	if err != nil || len(history[task.ID]) != 2 || history[task.ID][1].Note == nil || *history[task.ID][1].Note != "Run rejected by approval policies" {
		t.Fatalf("rejection lost captured note: %+v %v", history, err)
	}
}

func TestAccApprovalConfirmRefusalCommitsEvidenceAndRejection(t *testing.T) {
	w := newWorld(t)
	r := w.unconfirmed(t, "approval-confirm")
	outcome := policyapi.Undecided
	w.lc = w.boundApprovals(func(lifecycle.Run) policyapi.Evaluation { return approvalOutcome(w, outcome) })
	c := w.controller(w.access())
	if _, err := c.Confirm(t.Context(), w.alice(), "network", r.ID.String(), ""); !errors.Is(err, control.ErrState) {
		t.Fatalf("undecided confirm: %v", err)
	}
	got, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{r.ID})
	if err != nil || len(got[r.ID].Receipts) != 1 || got[r.ID].Receipts[0].StateVersion != r.Version {
		t.Fatalf("refusal erased evidence: %+v %v", got, err)
	}
	outcome = policyapi.Reject
	if _, err := c.Confirm(t.Context(), w.alice(), "network", r.ID.String(), ""); !errors.Is(err, control.ErrState) {
		t.Fatalf("rejected confirm: %v", err)
	}
	stored, err := w.store.Get(t.Context(), w.acct, r.ID)
	if err != nil || stored.State != kernel.RunStateFailed || stored.Version != r.Version+1 {
		t.Fatalf("refusal erased rejection: %+v %v", stored, err)
	}
	got, err = w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{r.ID})
	if err != nil || len(got[r.ID].Receipts) != 2 {
		t.Fatalf("rejection evidence: %+v %v", got, err)
	}
	if _, held, err := w.store.Holder(t.Context(), w.acct, w.stack); err != nil || held {
		t.Fatalf("rejection retained lock: %v %v", held, err)
	}
}

func TestAccApprovalEntryReturnsFinalStateWithoutConfirming(t *testing.T) {
	for _, outcome := range []policyapi.Outcome{policyapi.Undecided, policyapi.Approve, policyapi.Reject} {
		t.Run(string(outcome), func(t *testing.T) {
			w := newWorld(t)
			r := w.unconfirmed(t, "entry")
			r = w.mustMove(t, r, kernel.RunStateReplanRequested, machine.DriverAPI)
			r = w.mustMove(t, r, kernel.RunStatePreparingReplan, machine.DriverWorkerClaim)
			r = w.mustMove(t, r, kernel.RunStatePlanning, machine.DriverWorkerReport)
			w.lc = w.boundApprovals(func(lifecycle.Run) policyapi.Evaluation { return approvalOutcome(w, outcome) })
			moved := w.mustMove(t, r, kernel.RunStateUnconfirmed, machine.DriverDecision)
			want := kernel.RunStateUnconfirmed
			if outcome == policyapi.Reject {
				want = kernel.RunStateFailed
			}
			if moved.State != want {
				t.Fatalf("entry returned %s, want %s", moved.State, want)
			}
			got, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{r.ID})
			if err != nil || len(got[r.ID].Receipts) != 1 || got[r.ID].Receipts[0].StateVersion != r.Version+1 {
				t.Fatalf("entry evidence wrong state version: %+v %v", got, err)
			}
		})
	}
}
