package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

func TestAccHoldPolicySampleLinkFencesExactRunReceiptAgainstDeletion(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "retained-link-fence")
	receipt := policyReceipt(w, kernel.NewULID())
	evaluation := kernel.NewULID()
	receipt.SampleEvaluation = &evaluation
	if err := w.in(t, func(ctx context.Context) error {
		return w.store.RecordPolicyEvidence(ctx, w.acct, run.ID, run.Version, []policyapi.Receipt{receipt})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.HoldSampleLink(t.Context(), w.acct, w.stack, run.ID, receipt.ID); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("unfenced sample link: %v", err)
	}
	other, err := pgxpool.NewWithConfig(t.Context(), w.db.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	err = w.in(t, func(ctx context.Context) error {
		link, err := w.store.HoldSampleLink(ctx, w.acct, w.stack, run.ID, receipt.ID)
		if err != nil || link.Policy != receipt.Policy || link.Evaluation != evaluation {
			t.Fatalf("exact immutable sample link: %+v %v", link, err)
		}
		for _, table := range []string{"run_policy_receipts", "runs"} {
			//nolint:contextcheck // The competing connection must start a separate transaction instead of joining the held owner fence.
			err := tx.Do(t.Context(), other, tx.Options{NoRetry: true}, func(ctx context.Context) error {
				if _, err := tx.Q(ctx, other).Exec(ctx, "SET LOCAL lock_timeout='100ms'"); err != nil {
					return err
				}
				_, err := tx.Q(ctx, other).
					Exec(ctx, "DELETE FROM "+table+" WHERE account_id=$1 AND id=$2", w.acct, map[string]kernel.ULID{"runs": run.ID, "run_policy_receipts": receipt.ID}[table])
				return err
			})
			var blocked *pgconn.PgError
			if !errors.As(err, &blocked) || blocked.Code != "55P03" {
				t.Fatalf("%s deletion bypassed retained owner fence: %v", table, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		account             kernel.AccountID
		stack, run, receipt kernel.ULID
	}{{kernel.NewAccountID(), w.stack, run.ID, receipt.ID}, {w.acct, kernel.NewULID(), run.ID, receipt.ID}, {w.acct, w.stack, kernel.NewULID(), receipt.ID}, {w.acct, w.stack, run.ID, kernel.NewULID()}} {
		err := w.in(t, func(ctx context.Context) error {
			_, err := w.store.HoldSampleLink(ctx, test.account, test.stack, test.run, test.receipt)
			return err
		})
		if !errors.Is(err, policyapi.ErrSampleMissing) {
			t.Fatalf("foreign/mismatched receipt owner admitted: %v", err)
		}
	}
	if _, err := w.db.Exec(t.Context(), `DELETE FROM run_policy_receipts WHERE account_id=$1 AND run_id=$2 AND id=$3`, w.acct, run.ID, receipt.ID); err != nil {
		t.Fatalf("completed read retained deletion lock: %v", err)
	}
}
