package pg_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

func TestAccClaimCandidateAttemptReleasesMissesAndRetainsSuccess(t *testing.T) {
	w := newWorld(t)
	pool := kernel.NewULID()
	run := w.waits(t, w.acct, pool, kernel.RunStateReady, kernel.RunTypeProposed, kernel.RunPriorityNormal, false, w.now)
	probeLock := func() error {
		return tx.Do(t.Context(), w.db, tx.Options{}, func(ctx context.Context) error {
			var id kernel.ULID
			return tx.Q(ctx, w.db).QueryRow(ctx, `SELECT id FROM runs WHERE account_id=$1 AND id=$2 FOR UPDATE NOWAIT`, w.acct, run).Scan(&id)
		})
	}
	if _, err := w.store.CandidateAttempt(t.Context(), func(context.Context) (bool, error) { return true, nil }); !errors.Is(err, lifecycle.ErrNoTransaction) {
		t.Fatalf("unfenced candidate attempt: %v", err)
	}
	if err := w.in(t, func(ctx context.Context) error {
		for _, failure := range []error{nil, errInjected} {
			held, err := w.store.CandidateAttempt(ctx, func(ctx context.Context) (bool, error) {
				if _, err := w.store.Lock(ctx, w.acct, run); err != nil {
					return false, err
				}
				return false, failure
			})
			if held || !errors.Is(err, failure) {
				t.Fatalf("missed candidate outcome: held=%t err=%v", held, err)
			}
			if err := probeLock(); err != nil {
				t.Fatalf("discarded candidate retained its row lock before outer commit: %v", err)
			}
		}
		held, err := w.store.CandidateAttempt(ctx, func(ctx context.Context) (bool, error) {
			_, err := w.store.Lock(ctx, w.acct, run)
			return err == nil, err
		})
		if err != nil || !held {
			t.Fatalf("valid candidate not retained: held=%t err=%v", held, err)
		}
		var locked *pgconn.PgError
		if err := probeLock(); !errors.As(err, &locked) || locked.Code != "55P03" {
			t.Fatalf("retained candidate lost its row lock: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := probeLock(); err != nil {
		t.Fatalf("outer commit retained candidate lock: %v", err)
	}
}

// Only the first two probes pause. A lost/retried candidate must keep advancing
// rather than making a worker wait while another available row exists.
type simultaneousProbes struct {
	schedule.ClaimStore
	count   atomic.Int32
	probed  chan kernel.ULID
	release <-chan struct{}
}

func (s *simultaneousProbes) ProbeCandidate(
	ctx context.Context,
	account kernel.AccountID,
	pool kernel.ULID,
	states []kernel.RunState,
	last int,
	after *schedule.ClaimCursor,
) (lifecycle.Run, bool, error) {
	run, found, err := s.ClaimStore.ProbeCandidate(ctx, account, pool, states, last, after)
	if err == nil && found && after == nil && s.count.Add(1) <= 2 {
		s.probed <- run.ID
		select {
		case <-s.release:
		case <-ctx.Done():
			return lifecycle.Run{}, false, ctx.Err()
		}
	}
	return run, found, err
}

func TestAccClaimsThatProbeTheSameRunTakeDistinctAvailableRuns(t *testing.T) {
	w := newWorld(t)
	pool := kernel.NewULID()
	first := w.waits(t, w.acct, pool, kernel.RunStateReady, kernel.RunTypeProposed, kernel.RunPriorityNormal, false, w.now.Add(-time.Minute))
	second := w.waits(t, w.acct, pool, kernel.RunStateReady, kernel.RunTypeProposed, kernel.RunPriorityNormal, false, w.now)
	release := make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	t.Cleanup(resume)
	store := &simultaneousProbes{ClaimStore: w.store, probed: make(chan kernel.ULID, 2), release: release}
	claims := schedule.NewClaims(store, w.lc, w)
	type result struct {
		assignment workersapi.Assignment
		ok         bool
		err        error
	}
	done := make(chan result, 2)
	for range 2 {
		go func() {
			a, ok, err := w.take(t, claims, pool, kernel.NewULID(), nil)
			done <- result{a, ok, err}
		}()
	}
	for range 2 {
		select {
		case id := <-store.probed:
			if id != first {
				t.Errorf("concurrent probe read %s, expected first %s", id, first)
			}
		case got := <-done:
			t.Fatalf("claim ended before the controlled probe: %+v", got)
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
	resume()
	seen := map[kernel.ULID]bool{}
	for range 2 {
		got := <-done
		if got.err != nil || !got.ok || seen[got.assignment.Run] {
			t.Fatalf("available run skipped or claimed twice: %+v", got)
		}
		seen[got.assignment.Run] = true
	}
	if !seen[first] || !seen[second] {
		t.Fatalf("claims %v did not cover both available rows", seen)
	}
}

func TestAccClaimSkipsALockedFirstRunWithoutWaiting(t *testing.T) {
	w := newWorld(t)
	pool := kernel.NewULID()
	first := w.waits(t, w.acct, pool, kernel.RunStateReady, kernel.RunTypeProposed, kernel.RunPriorityNormal, false, w.now.Add(-time.Minute))
	second := w.waits(t, w.acct, pool, kernel.RunStateReady, kernel.RunTypeProposed, kernel.RunPriorityNormal, false, w.now)
	holder, err := w.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(t.Context()) }()
	var id kernel.ULID
	if err := holder.QueryRow(t.Context(), `SELECT id FROM runs WHERE account_id=$1 AND id=$2 FOR UPDATE`, w.acct, first).Scan(&id); err != nil {
		t.Fatal(err)
	}
	got, ok, err := w.take(t, schedule.NewClaims(w.store, w.lc, w), pool, kernel.NewULID(), nil)
	if err != nil || !ok || got.Run != second {
		t.Fatalf("locked first row prevented available work: %+v %t %v", got, ok, err)
	}
}
