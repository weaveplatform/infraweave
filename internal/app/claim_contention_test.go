package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// claimMiss observes the real scheduler result without replacing its owner/run
// locking. It lets the test release the owner only after a genuine missed claim.
type claimMiss struct {
	workersapi.Scheduler
	missed chan struct{}
	once   sync.Once
}

func (s *claimMiss) Claim(ctx context.Context, c workersapi.Claim) (workersapi.Assignment, bool, error) {
	assignment, ok, err := s.Scheduler.Claim(ctx, c)
	if !ok && err == nil {
		s.once.Do(func() { close(s.missed) })
	}
	return assignment, ok, err
}

func TestAccRunClaimRetriesAfterOwnerTransactionReleasesWithoutNotification(t *testing.T) {
	w := newClaimWorld(t)
	// This real replica has no WatchPolls subscription: an unrelated or
	// initial resync notification cannot disguise a missing contention timer.
	silent, err := newDomains(config.Defaults(), w.db, w.keys, w.blobs.Objects, nil, testEvents(t, w.q), discard())
	if err != nil {
		t.Fatal(err)
	}
	w.replicas[0] = silent
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	pool := w.pool(t, w.acct, nil)
	run := w.run(t, w.acct, pool, runSpec{typ: kernel.RunTypeProposed, p: kernel.RunPriorityNormal}, time.Now())
	worker := w.worker(t, pool, false)
	held, err := w.db.Primary().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.WithoutCancel(ctx))
	if _, err := held.Exec(ctx, `SELECT id FROM stacks WHERE account_id=$1 AND id=$2 FOR UPDATE`, w.acct, w.stacks["one"]); err != nil {
		t.Fatal(err)
	}
	observed := &claimMiss{Scheduler: w.replicas[0].Runs.Scheduler(), missed: make(chan struct{})}
	w.replicas[0].Workers.BindScheduler(observed)
	answer := make(chan polled, 1)
	wg.Go(func() { answer <- w.poll(ctx, 0, worker, false, 5) })
	select {
	case <-observed.missed:
	case got := <-answer:
		t.Fatalf("poll ended before observing owner contention: %+v", got)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// This transaction changes no run and emits no pool notification.
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-answer
	if assigned := claimed(t, got); assigned != run {
		t.Fatalf("released owner left its READY run unclaimed: got %s want %s", assigned, run)
	}
}
