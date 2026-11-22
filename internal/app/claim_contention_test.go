package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/test/deps"
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

func TestAccRunClaimClientRepollsAfterContendedHoldExpires(t *testing.T) {
	w := newClaimWorld(t)
	deadline, ok := t.Deadline()
	if !ok {
		t.Fatal("claim acceptance requires a finite test deadline")
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	pool := w.pool(t, w.acct, nil)
	run := w.run(t, w.acct, pool, runSpec{typ: kernel.RunTypeProposed, p: kernel.RunPriorityNormal}, time.Now())
	worker := w.worker(t, pool, false)
	held, err := w.db.Primary().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		if err := held.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("owner transaction cleanup: %v", err)
		}
	})
	if _, err := held.Exec(ctx, `SELECT id FROM stacks WHERE account_id=$1 AND id=$2 FOR UPDATE`, w.acct, w.stacks["one"]); err != nil {
		t.Fatal(err)
	}
	observed := &claimMiss{Scheduler: w.replicas[0].Runs.Scheduler(), missed: make(chan struct{})}
	w.replicas[0].Workers.BindScheduler(observed)
	answer := make(chan []polled, 1)
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	wg.Go(func() { answer <- w.pollAssignments(t, ctx, []kernel.ULID{worker}) })
	select {
	case <-observed.missed:
	case got := <-answer:
		t.Fatalf("client ended before actual owner contention: %+v", got)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var firstSeen time.Time
	if err := held.QueryRow(ctx, `SELECT last_seen_at FROM workers WHERE account_id=$1 AND id=$2`, w.acct, worker).Scan(&firstSeen); err != nil {
		t.Fatal(err)
	}
	// Seen changes once per request. It cannot advance from retries within the
	// first hold, so this observes a genuine client repoll while the owner is
	// still locked, without replacing a scheduler result or emitting a wake.
	err = deps.Eventually(ctx, "the claim client's next real poll after hold expiry", func(ctx context.Context) (bool, error) {
		select {
		case got := <-answer:
			return false, fmt.Errorf("%w: claim client ended after one empty response: %+v", deps.ErrPermanent, got)
		default:
		}
		var seen time.Time
		if err := held.QueryRow(ctx, `SELECT last_seen_at FROM workers WHERE account_id=$1 AND id=$2`, w.acct, worker).Scan(&seen); err != nil {
			return false, fmt.Errorf("%w: observe worker poll: %w", deps.ErrPermanent, err)
		}
		if seen.After(firstSeen) {
			t.Logf("actual client repoll advanced last_seen_at by %s", seen.Sub(firstSeen))
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var (
		state    string
		assigned *kernel.ULID
		version  int64
		busy     bool
	)
	if err := held.QueryRow(ctx, `SELECT r.state, r.worker_id, r.state_version, w.busy FROM runs r
		JOIN workers w ON w.account_id=r.account_id AND w.id=$3
		WHERE r.account_id=$1 AND r.id=$2`, w.acct, run, worker).Scan(&state, &assigned, &version, &busy); err != nil {
		t.Fatal(err)
	}
	if state != string(kernel.RunStateReady) || assigned != nil || version != 1 || busy {
		t.Fatalf("locked owner changed run/worker: state=%s assigned=%v version=%d busy=%t", state, assigned, version, busy)
	}
	// Only a row lock is released: this transaction changes no run and sends
	// no pool notification. The subsequent request must claim the exact run.
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-answer:
		if len(got) != 1 || claimed(t, got[0]) != run {
			t.Fatalf("released owner was not claimed by the next poll: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := w.q.QueryRow(ctx, `SELECT state, worker_id, state_version FROM runs WHERE account_id=$1 AND id=$2`, w.acct, run).Scan(&state, &assigned, &version); err != nil {
		t.Fatal(err)
	}
	if state != string(kernel.RunStatePreparing) || assigned == nil || *assigned != worker || version != 2 {
		t.Fatalf("wrong claim owner: state=%s worker=%v version=%d", state, assigned, version)
	}
}
