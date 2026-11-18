package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/queue/queuetest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
)

func TestAccCommitCheckFencedConsumerCoalescesCurrentState(t *testing.T) {
	w := newCheckWorld(t)
	w.state(w.b.Run, kernel.RunStatePlanning, 3)
	consumer := queue.Kind{Name: "vcs.check_contract", Queue: queue.Events, Policy: queue.Ignore}
	sequence := int64(3)
	err := queuetest.ConsumeTwice(t.Context(), w.jobs, consumer, w.b.Account.ULID, func(ctx context.Context, job queue.Job) error {
		target := w.target(t, w.b, "infraweave/infra", false, sequence)
		sequence = 2
		return tx.Do(ctx, w.f.db, tx.Options{}, func(ctx context.Context) error {
			if err := w.jobs.HoldTx(ctx, job); err != nil {
				return err
			}
			if err := w.service.Enqueue(ctx, target); err != nil {
				return err
			}
			return w.jobs.CompleteTx(ctx, job)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	j := w.claim(t)
	var request feedback.CheckRequest
	if err := json.Unmarshal(j.Payload, &request); err != nil || request.Sequence != 3 {
		t.Fatal("late delivery replaced the newer pending check", request, err)
	}
	var order string
	if err := w.f.db.QueryRow(t.Context(), `SELECT ordering_key FROM jobs WHERE id=$1`, j.ID).
		Scan(&order); err != nil ||
		order != "vcs.check.run:"+w.b.Account.String()+":"+w.b.Key {
		t.Fatal("individual states did not share their run's ordering lane", order, err)
	}
	w.state(w.b.Run, kernel.RunStateFinished, 5)
	w.enqueue(t, w.b, 5)
	blocked, err := w.jobs.Claim(t.Context(), queue.Deliveries, []string{feedback.ReportCheck.Name}, 10, time.Minute, "concurrent-check-worker")
	if err != nil || len(blocked) != 0 {
		t.Fatal("coalesced state ran concurrently with its claimed predecessor", blocked, err)
	}
	if err := w.service.Checked(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if err := w.jobs.Complete(t.Context(), j); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatal("cursor and rerun acknowledgement were not atomic", err)
	}
	next := w.claim(t)
	if next.ID != j.ID || next.Generation <= j.Generation || json.Unmarshal(next.Payload, &request) != nil || request.Sequence != 5 {
		t.Fatal("running coalescence lost its final requested state", next, request)
	}
	if err := w.service.Checked(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	writes := w.fake.CheckWrites("acme", "infra")
	if len(writes) != 1 || writes[0].Status != "success" || w.checks.calls != 1 {
		t.Fatal("duplicate delivery/rerun duplicated or regressed the current native effect", writes, w.checks.calls)
	}
	var settled int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE kind IN ($1,$2) AND state='succeeded'`, consumer.Name, feedback.ReportCheck.Name).
		Scan(&settled); err != nil ||
		settled != 3 {
		t.Fatal("fenced consumers or coalesced report remained unfinished", settled, err)
	}
}
