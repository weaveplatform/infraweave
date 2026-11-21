package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
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
	// ProbeCandidate reads the next candidate without locking its run. Owner
	// fences must precede the exact row lock, including after a skipped target.
	ProbeCandidate(
		ctx context.Context,
		acct kernel.AccountID,
		pool kernel.ULID,
		states []kernel.RunState,
		last int,
		after *ClaimCursor,
		excluded []kernel.ULID,
	) (lifecycle.Run, bool, error)
	LockCandidate(ctx context.Context, candidate lifecycle.Run, pool kernel.ULID, states []kernel.RunState, last int) (lifecycle.Run, bool, error)
	// CandidateAttempt rolls back all candidate locks when fn answers false
	// or fails. It retains them on true. fn must make no writes or callbacks.
	CandidateAttempt(ctx context.Context, fn func(context.Context) (bool, error)) (bool, error)
	// WakeClaims wakes contended polls when commit releases candidate owner locks.
	WakeClaims(context.Context, kernel.ULID) error
	// Assign writes worker as run's worker.
	Assign(ctx context.Context, run lifecycle.Run, worker kernel.ULID) error
	// Waiting counts the runs in one of states of each private pool, of
	// every account; a pool with none is absent.
	Waiting(ctx context.Context, states []kernel.RunState) (map[kernel.ULID]int, error)
}

// ClaimCursor follows the queue's stable priority/creation/ID ordering.
type ClaimCursor struct {
	Tier      int
	CreatedAt time.Time
	ID        kernel.ULID
}

// ClaimFence holds the candidate's space then stack before its run row.
type ClaimFence interface {
	HoldClaim(context.Context, kernel.AccountID, kernel.ULID) (bool, error)
}

// InitializationPolicies records a READY run's decision before assignment.
type InitializationPolicies interface {
	Evaluate(context.Context, lifecycle.Run) (policyapi.Evaluation, error)
}

// The tiers a claim reads, as kernel.RunTier gives them: lastTier is the
// last, and driftTier the first that holds only drift detection runs,
// which a pool's drift detection run limit bounds. A high drift run is
// in the first tier, which no limit bounds.
var (
	lastTier  = kernel.RunTier(kernel.RunPriorityLow, kernel.RunTypeTracked, true)
	driftTier = kernel.RunTier(kernel.RunPriorityNormal, kernel.RunTypeTracked, true)
)

// Claims is the claim of the runs domain's workersapi.Scheduler: a
// worker's poll claims its pool's next run through it.
//
// ref: decision 0008 § "Claiming runs"
type Claims struct {
	store          ClaimStore
	lifecycle      *lifecycle.Lifecycle
	fence          ClaimFence
	initialization InitializationPolicies
}

// NewClaims returns the claims over store and l.
func NewClaims(store ClaimStore, l *lifecycle.Lifecycle, fence ClaimFence) *Claims {
	return &Claims{store: store, lifecycle: l, fence: fence}
}

// WithInitialization binds the policy owner during domain assembly.
func (s *Claims) WithInitialization(gate InitializationPolicies) *Claims {
	s.initialization = gate
	return s
}

// Claim moves c's pool's next run to its preparing state for c's worker,
// as workersapi.Scheduler's Claim says, in the transaction ctx carries.
// The worker's own claims are serialized by its caller, which locks the
// worker's row first, so a worker never holds two runs.
func (s *Claims) Claim(ctx context.Context, c workersapi.Claim) (workersapi.Assignment, bool, error) {
	if !tx.In(ctx) {
		return workersapi.Assignment{}, false, lifecycle.ErrNoTransaction
	}
	holding, err := s.store.Holding(ctx, c.Account, c.Worker)
	if err != nil || holding {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the store names the read
	}
	last, err := s.allowedTier(ctx, c)
	if err != nil {
		return workersapi.Assignment{}, false, err
	}
	run, ok, err := s.candidate(ctx, c, last)
	if err != nil || !ok {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the store names the read
	}
	if run.State == kernel.RunStateReady && s.initialization != nil {
		refused, err := s.initialize(ctx, run)
		if err == nil && refused {
			err = s.store.WakeClaims(ctx, c.Pool)
		}
		if err != nil || refused {
			// A refusal must commit before selecting another stack. The worker
			// repeats selection in a fresh transaction without becoming busy.
			return workersapi.Assignment{Retry: refused && err == nil}, false, err //nolint:wrapcheck // the gate/store names the failed evaluation or wake
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
	if err := s.store.WakeClaims(ctx, c.Pool); err != nil {
		return workersapi.Assignment{}, false, err //nolint:wrapcheck // the store names the notification
	}
	return workersapi.Assignment{Run: moved.ID, State: moved.State, StateVersion: moved.Version},
		true, nil
}

func (s *Claims) initialize(ctx context.Context, run lifecycle.Run) (bool, error) {
	result, err := s.initialization.Evaluate(ctx, run)
	if err != nil {
		return false, err //nolint:wrapcheck // the evaluator names the operational failure
	}
	if result.Type != policyapi.Initialization {
		return false, evidence.ErrReceipt
	}
	if !result.HasPolicies && result.Outcome == policyapi.NoPolicies || result.HasPolicies && result.Outcome == policyapi.Allow {
		return false, nil
	}
	denied := 0
	for _, receipt := range result.Receipts {
		if receipt.Outcome == policyapi.Deny {
			denied++
		}
	}
	if !result.HasPolicies || result.Outcome != policyapi.Deny || denied == 0 {
		return false, evidence.ErrReceipt
	}
	noun := "policy"
	if denied != 1 {
		noun = "policies"
	}
	_, err = s.lifecycle.Transition(ctx, lifecycle.Request{
		Account: run.Account, Run: run.ID, Version: run.Version, To: kernel.RunStateFailed,
		Driver: machine.DriverInitialization,
		Note:   fmt.Sprintf("job assignment failed: denied by %d initialization %s", denied, noun),
	})
	return err == nil, err //nolint:wrapcheck // the lifecycle names the refusal transition
}

// limited reports whether c's pool bounds its drift detection runs.
func (s *Claims) limited(c workersapi.Claim) bool {
	return c.DriftLimit != nil && *c.DriftLimit >= 0
}

// allowedTier takes the pool drift fence before any candidate owner/run lock.
// High-priority drift remains eligible even when the bounded tiers are full.
func (s *Claims) allowedTier(ctx context.Context, c workersapi.Claim) (int, error) {
	if !s.limited(c) {
		return lastTier, nil
	}
	if err := s.store.TakeDrift(ctx, c.Account, c.Pool); err != nil {
		return 0, err //nolint:wrapcheck // the store names the lock
	}
	n, err := s.store.Drifting(ctx, c.Account, c.Pool)
	switch {
	case err != nil:
		return 0, err //nolint:wrapcheck // the store names the read
	case n < *c.DriftLimit:
		return lastTier, nil
	}
	return driftTier - 1, nil
}

func (s *Claims) candidate(ctx context.Context, c workersapi.Claim, last int) (lifecycle.Run, bool, error) {
	var after *ClaimCursor
	var excluded []kernel.ULID
	for {
		probe, found, err := s.store.ProbeCandidate(ctx, c.Account, c.Pool, machine.Claimable(), last, after, excluded)
		if err != nil || !found {
			return lifecycle.Run{}, false, err //nolint:wrapcheck // the store names the read
		}
		var run lifecycle.Run
		var fenced bool
		held, err := s.store.CandidateAttempt(ctx, func(ctx context.Context) (bool, error) {
			var err error
			fenced, err = s.fence.HoldClaim(ctx, probe.Account, probe.Stack)
			if err != nil || !fenced {
				return false, err //nolint:wrapcheck // the owner names the fence
			}
			var ok bool
			run, ok, err = s.store.LockCandidate(ctx, probe, c.Pool, machine.Claimable(), last)
			return ok, err //nolint:wrapcheck // the store names revalidation
		})
		if err != nil || held {
			return run, held, err //nolint:wrapcheck // the attempt names rollback/release failures
		}
		if !fenced {
			// Never revisit this owner midway through the same selection: its
			// fence freeing must not let a lower-priority sibling overtake it.
			excluded = append(excluded, probe.Stack)
		}
		// The unsuccessful attempt released every owner/run lock. Advancing
		// cannot introduce a cross-stack lock order or retry a contended row.
		after = &ClaimCursor{Tier: kernel.RunTier(probe.Priority, probe.Type, probe.DriftDetection), CreatedAt: probe.CreatedAt, ID: probe.ID}
	}
}

// Queued counts the runs waiting for a worker of each private pool, as
// workersapi.Scheduler's Queued says: the runs in a state a claim takes a
// run out of.
func (s *Claims) Queued(ctx context.Context) (map[kernel.ULID]int, error) {
	return s.store.Waiting(ctx, machine.Claimable()) //nolint:wrapcheck // the store names the read
}
