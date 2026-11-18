package pg_test

import (
	"context"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/queue/queuetest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// The second committed delivery uses the real port after the first claim has
// completed. Its any-state check must suppress all creation consequences, not
// merely return an existing ID while appending another event or schedule job.
func TestAccEventCreationConsumerContract(t *testing.T) {
	w := newWorld(t)
	creator, _ := w.internalCreator()
	request := creationRequest(w, "delivered-commit")
	kind := queue.Kind{Name: "vcs.evaluate_event", Queue: queue.VCSInbox, Policy: queue.Ignore}
	var run kernel.ULID
	err := queuetest.ConsumeTwice(t.Context(), w.jobs, kind, w.acct.ULID,
		func(ctx context.Context, job queue.Job) error {
			return tx.Do(ctx, w.db, tx.Options{}, func(ctx context.Context) error {
				if err := w.jobs.HoldTx(ctx, job); err != nil {
					return err
				}
				created, err := creator.Create(ctx, request)
				if err != nil {
					return err
				}
				if !run.IsZero() && created.ID != run {
					return queuetest.ErrContract
				}
				run = created.ID
				return w.jobs.CompleteTx(ctx, job)
			})
		})
	if err != nil {
		t.Fatal(err)
	}
	if count(t, w.db, "runs", w.acct) != 1 || count(t, w.db, "events", w.acct) != 1 {
		t.Fatal("committed redelivery repeated run or outbox effects")
	}
	var scheduling, consumers int
	if err := w.db.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM jobs WHERE account_id=$1 AND kind='run.schedule'),
		(SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2 AND state='succeeded')`, w.acct, kind.Name).Scan(&scheduling, &consumers); err != nil || scheduling != 1 || consumers != 2 {
		t.Fatal("redelivery lost its fence or repeated scheduling", scheduling, consumers, err)
	}
}
