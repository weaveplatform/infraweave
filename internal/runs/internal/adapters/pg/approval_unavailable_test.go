package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

func TestAccUnavailableApprovalStorageCannotBecomePolicyFree(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeProposed, "unavailable-approval")
	gate := evidence.Approval{State: run.State, StateVersion: run.Version, HasPolicies: true, Outcome: policyapi.Approve, At: w.now}
	if err := w.store.RecordApproval(t.Context(), w.acct, run.ID, gate); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatal("approval escaped its caller transaction")
	}
	invalid := gate
	invalid.Outcome = policyapi.NoPolicies
	if err := w.in(t, func(ctx context.Context) error { return w.store.RecordApproval(ctx, w.acct, run.ID, invalid) }); !errors.Is(err, evidence.ErrReceipt) {
		t.Fatal("contradictory approval aggregate was accepted")
	}
	if err := w.in(t, func(ctx context.Context) error {
		return w.store.RecordApproval(ctx, w.acct, run.ID, gate)
	}); err != nil {
		t.Fatal(err)
	}
	// An admitted PostgreSQL timestamp can still be an invalid Go zero time.
	// Neither current nor replay reads may turn corrupt evidence into approval.
	if _, err := w.db.Exec(t.Context(), `UPDATE run_approval_gates SET evaluated_at='0001-01-01T00:00:00Z'
		WHERE account_id=$1 AND run_id=$2`, w.acct, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := w.store.ApprovalAt(t.Context(), w.acct, run.ID, run.Version); !errors.Is(err, kernel.ErrUnavailable) || found {
		t.Fatal("corrupt historical approval was accepted")
	}
	if gates, err := w.store.Approvals(t.Context(), w.acct, []kernel.ULID{run.ID}); !errors.Is(err, kernel.ErrUnavailable) || gates != nil {
		t.Fatal("corrupt current approval was accepted")
	}
	w.db.Close()
	for name, call := range map[string]func() error{
		"historical report gate": func() error {
			_, _, err := w.store.ApprovalAt(t.Context(), w.acct, run.ID, run.Version)
			return err
		},
		"current gates": func() error {
			_, err := w.store.Approvals(t.Context(), w.acct, []kernel.ULID{run.ID})
			return err
		},
		"durable scan": func() error {
			_, err := w.store.WaitingApprovalStacks(t.Context(), w.acct, kernel.ULID{}, 25)
			return err
		},
		"waiting runs": func() error {
			_, err := w.store.WaitingApprovals(t.Context(), w.acct, w.stack)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatalf("unavailable approval storage returned a policy-free result: %v", err)
			}
		})
	}
}
