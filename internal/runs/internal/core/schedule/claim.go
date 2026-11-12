package schedule

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// ClaimStore is the claim's storage port. Each method joins the
// transaction its context carries.
type ClaimStore interface {
	// Holding reports whether acct's worker holds a run: one it claimed
	// last that is in a state on a worker.
	Holding(ctx context.Context, acct kernel.AccountID, worker kernel.ULID) (bool, error)
	// Drifting counts acct's pool's drift detection runs in a state on a
	// worker.
	Drifting(ctx context.Context, acct kernel.AccountID, pool kernel.ULID) (int, error)
	// TakeDrift waits for acct's pool's drift lock and holds it until the
	// transaction ends.
	TakeDrift(ctx context.Context, acct kernel.AccountID, pool kernel.ULID) error
	// Next reads acct's pool's run in one of states with a priority tier
	// at most last that comes first by tier, then creation, then ID,
	// among the rows no other transaction locks, and locks its row FOR
	// UPDATE; false when there is none.
	Next(
		ctx context.Context,
		acct kernel.AccountID,
		pool kernel.ULID,
		states []kernel.RunState,
		last int,
	) (lifecycle.Run, bool, error)
	// Assign writes worker as run's worker.
	Assign(ctx context.Context, run lifecycle.Run, worker kernel.ULID) error
}

// The tiers a claim reads, as kernel.RunTier gives them: lastTier is the
// last, and driftTier the first that holds only drift detection runs,
// which a pool's drift detection run limit bounds. A high drift run is
// in the first tier, which no limit bounds.
var (
	lastTier  = kernel.RunTier(kernel.RunPriorityLow, kernel.RunTypeTracked, true)
	driftTier = kernel.RunTier(kernel.RunPriorityNormal, kernel.RunTypeTracked, true)
)

// Claims is the runs domain's workersapi.Scheduler: a worker's poll
// claims its pool's next run through it.
//
// ref: decision 0008 § "Claiming runs"
type Claims struct {
	store     ClaimStore
	lifecycle *lifecycle.Lifecycle
}

var _ workersapi.Scheduler = (*Claims)(nil)

// NewClaims returns the claims over store and l.
func NewClaims(store ClaimStore, l *lifecycle.Lifecycle) *Claims {
	return &Claims{store: store, lifecycle: l}
}

// Claim moves c's pool's next run to its preparing state for c's worker,
// as workersapi.Scheduler's Claim says, in the transaction ctx carries.
// The worker's own claims are serialized by its caller, which locks the
// worker's row first, so a worker never holds two runs.
func (s *Claims) Claim(
	ctx context.Context,
	c workersapi.Claim,
) (workersapi.Assignment, bool, error) {
	if !tx.In(ctx) {
		return workersapi.Assignment{}, false, lifecycle.ErrNoTransaction
	}
	holding, err := s.store.Holding(ctx, c.Account, c.Worker)
	if err != nil || holding {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the store names the read
	}
	run, ok, err := s.store.Next(ctx, c.Account, c.Pool, machine.Claimable(), lastTier)
	if err != nil || !ok {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the store names the read
	}
	if s.limited(c) && kernel.RunTier(run.Priority, run.Type, run.DriftDetection) >= driftTier {
		if run, ok, err = s.drift(ctx, c, run); err != nil || !ok {
			return workersapi.Assignment{}, false, err
		}
	}
	// Every claimable state has a claim edge for each type the machine
	// lets into it.
	to, _ := machine.Claimed(run.Type, run.State)
	moved, err := s.lifecycle.Transition(ctx, lifecycle.Request{
		Account: c.Account, Run: run.ID, Version: run.Version, To: to,
		Driver: machine.DriverWorkerClaim,
	})
	if err != nil {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the lifecycle names the move
	}
	if err := s.store.Assign(ctx, moved, c.Worker); err != nil {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the store names the write
	}
	return workersapi.Assignment{Run: moved.ID, State: moved.State}, true, nil
}

// limited reports whether c's pool bounds its drift detection runs.
func (s *Claims) limited(c workersapi.Claim) bool {
	return c.DriftLimit != nil && *c.DriftLimit >= 0
}

// drift is the run c claims when the next is a drift run: run itself while
// the pool's drift runs on workers are fewer than its limit, otherwise
// the next run of an earlier tier. The pool's drift claims take turns
// under its drift lock, so each counts the ones committed before it and
// two cannot pass a limit of one together.
func (s *Claims) drift(
	ctx context.Context,
	c workersapi.Claim,
	run lifecycle.Run,
) (lifecycle.Run, bool, error) {
	if err := s.store.TakeDrift(ctx, c.Account, c.Pool); err != nil {
		return lifecycle.Run{}, false, err //nolint:wrapcheck // the store names the lock
	}
	n, err := s.store.Drifting(ctx, c.Account, c.Pool)
	switch {
	case err != nil:
		return lifecycle.Run{}, false, err //nolint:wrapcheck // the store names the read
	case n < *c.DriftLimit:
		return run, true, nil
	}
	//nolint:wrapcheck // the store names the read
	return s.store.Next(ctx, c.Account, c.Pool, machine.Claimable(), driftTier-1)
}
