package schedule_test

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// store keeps runs in memory as both the claim's and the lifecycle's
// store; locked are the runs another transaction holds, which Next
// skips.
type store struct {
	lifecycle.Store
	runs   []lifecycle.Run
	locked map[kernel.ULID]bool
}

func (s *store) find(id kernel.ULID) *lifecycle.Run {
	for i := range s.runs {
		if s.runs[i].ID == id {
			return &s.runs[i]
		}
	}
	return nil
}

func tier(r lifecycle.Run) int { return kernel.RunTier(r.Priority, r.Type, r.DriftDetection) }

func (s *store) Holding(_ context.Context, acct kernel.AccountID, w kernel.ULID) (bool, error) {
	return slices.ContainsFunc(s.runs, func(r lifecycle.Run) bool {
		return r.Account == acct && r.Worker != nil && *r.Worker == w && r.State.OnWorker()
	}), nil
}

func (s *store) Drifting(_ context.Context, acct kernel.AccountID, pool kernel.ULID) (int, error) {
	n := 0
	for _, r := range s.runs {
		if r.Account == acct && *r.WorkerPool == pool && r.DriftDetection && r.State.OnWorker() {
			n++
		}
	}
	return n, nil
}

func (*store) TakeDrift(context.Context, kernel.AccountID, kernel.ULID) error { return nil }

func (s *store) Next(
	_ context.Context,
	acct kernel.AccountID,
	pool kernel.ULID,
	states []kernel.RunState,
	last int,
) (lifecycle.Run, bool, error) {
	var out []lifecycle.Run
	for _, r := range s.runs {
		if r.Account == acct && *r.WorkerPool == pool && slices.Contains(states, r.State) &&
			tier(r) <= last && !s.locked[r.ID] {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return lifecycle.Run{}, false, nil
	}
	return slices.MinFunc(out, func(a, b lifecycle.Run) int {
		return cmp.Or(cmp.Compare(tier(a), tier(b)), a.CreatedAt.Compare(b.CreatedAt),
			bytes.Compare(a.ID[:], b.ID[:]))
	}), true, nil
}

func (s *store) Waiting(
	_ context.Context,
	states []kernel.RunState,
) (map[kernel.ULID]int, error) {
	out := map[kernel.ULID]int{}
	for _, r := range s.runs {
		if r.WorkerPool != nil && slices.Contains(states, r.State) {
			out[*r.WorkerPool]++
		}
	}
	return out, nil
}

func (s *store) Assign(_ context.Context, run lifecycle.Run, w kernel.ULID) error {
	s.find(run.ID).Worker = &w
	return nil
}

func (s *store) Lock(_ context.Context, _ kernel.AccountID, id kernel.ULID) (lifecycle.Run, error) {
	return *s.find(id), nil
}

func (s *store) Move(_ context.Context, run lifecycle.Run, step lifecycle.Step) error {
	r := s.find(run.ID)
	r.State, r.Version = step.State, step.Sequence
	return nil
}

func (*store) Track(context.Context, lifecycle.Run) error { return nil }

func (*store) Projection(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	[]kernel.StackState,
) (kernel.StackState, bool, error) {
	return kernel.StackStateNone, false, nil
}

// stacks answers every stack, and events and db accept everything, as a
// claim's transition reads and writes them.
type (
	stacks struct{ stacksapi.Stacks }
	events struct{}
	db     struct{}
	fakeTx struct{ pgx.Tx }
)

func (stacks) Get(_ context.Context, _ kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error) {
	return stacksapi.Stack{ID: id}, nil
}

func (stacks) SetRunState(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
	kernel.StackState,
	bool,
) error {
	return nil
}

func (events) Append(_ context.Context, ev outbox.Event) (outbox.Event, error) { return ev, nil }

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return fakeTx{}, nil }
func (fakeTx) Commit(context.Context) error                       { return nil }
func (fakeTx) Rollback(context.Context) error                     { return nil }

// TestUnitClaimOrder checks that claims take a pool's waiting runs by
// tier, then creation, then ID, skipping runs another transaction holds,
// and that a pool's drift runs below the first tier wait while its drift
// runs on workers reach its limit, while its other tiers, and a high
// drift run, do not; nil and negative limits bound nothing, and a worker
// holding a run claims none.
func TestUnitClaimOrder(t *testing.T) {
	acct, pool := kernel.NewAccountID(), kernel.NewULID()
	base := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	s := &store{locked: map[kernel.ULID]bool{}}
	run := func(typ kernel.RunType, p kernel.RunPriority, drift bool, at time.Duration) kernel.ULID {
		r := lifecycle.Run{
			Account: acct, ID: kernel.NewULID(), Type: typ, State: kernel.RunStateReady,
			Version: 1, Priority: p, DriftDetection: drift, WorkerPool: &pool,
			CreatedAt: base.Add(at),
		}
		s.runs = append(s.runs, r)
		return r.ID
	}
	proposed, normal, low := kernel.RunTypeProposed, kernel.RunPriorityNormal, kernel.RunPriorityLow
	drift := run(proposed, normal, true, 0)
	lowDrift := run(proposed, low, true, 0)
	tied := []kernel.ULID{run(proposed, normal, false, time.Minute), run(proposed, normal, false,
		time.Minute)}
	slices.SortFunc(tied, func(a, b kernel.ULID) int { return bytes.Compare(a[:], b[:]) })
	older := run(proposed, normal, false, 0)
	tracked := run(kernel.RunTypeTracked, normal, false, time.Hour)
	high := run(proposed, kernel.RunPriorityHigh, true, time.Hour)
	held := run(proposed, kernel.RunPriorityHigh, false, 0)
	s.locked[held] = true
	lowRun := run(proposed, low, false, 0)

	l := lifecycle.New(lifecycle.Deps{Store: s, Stacks: stacks{}, Events: events{}})
	c := schedule.NewClaims(s, l)
	one, unlimited := 1, -1
	claim := func(worker kernel.ULID, limit *int) (kernel.ULID, bool) {
		t.Helper()
		var (
			a  workersapi.Assignment
			ok bool
		)
		if err := tx.Do(t.Context(), db{}, tx.Options{}, func(ctx context.Context) error {
			var err error
			a, ok, err = c.Claim(ctx, workersapi.Claim{
				Account: acct, Pool: pool, Worker: worker, DriftLimit: limit,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if ok && (a.State != kernel.RunStatePreparing || a.StateVersion != 2) {
			t.Errorf("run %s claimed into %s at %d", a.Run, a.State, a.StateVersion)
		}
		return a.Run, ok
	}
	if got, err := c.Queued(t.Context()); err != nil || got[pool] != len(s.runs) ||
		len(got) != 1 {
		t.Errorf("Queued = %v, %v, want %d in %s", got, err, len(s.runs), pool)
	}
	first := kernel.NewULID()
	for i, want := range []kernel.ULID{high, tracked, older, tied[0], tied[1], lowRun} {
		w := kernel.NewULID()
		if i == 0 {
			w = first
		}
		if got, ok := claim(w, &one); !ok || got != want {
			t.Errorf("claim %d took %s, %v, want %s", i, got, ok, want)
		}
	}
	if got, ok := claim(kernel.NewULID(), &one); ok {
		t.Errorf("a drift run past the limit the high drift run reached: %s", got)
	}
	if got, ok := claim(first, &unlimited); ok {
		t.Errorf("a worker holding a run claimed %s", got)
	}
	if got, ok := claim(kernel.NewULID(), &unlimited); !ok || got != drift {
		t.Errorf("under a negative limit: %s, %v, want %s", got, ok, drift)
	}
	if got, ok := claim(kernel.NewULID(), nil); !ok || got != lowDrift {
		t.Errorf("under no limit: %s, %v, want %s", got, ok, lowDrift)
	}
	if got, ok := claim(kernel.NewULID(), nil); ok {
		t.Errorf("claimed %s, which another transaction holds", got)
	}
}
