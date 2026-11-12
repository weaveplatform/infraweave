package api

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrNoPool reports a worker pool the account lacks or that the space
// asking for it does not reach.
var ErrNoPool = errors.New("workers: no such worker pool in reach")

// Pools is what other domains call of the workers domain's pools.
type Pools interface {
	// Hold checks that acct's pool id lives in space or in a space space
	// inherits from, through an unbroken chain of inheriting spaces, and
	// holds the pool for the transaction ctx carries, so no delete or move
	// of it commits before the caller's write; ErrNoPool otherwise.
	Hold(ctx context.Context, acct kernel.AccountID, space, id kernel.ULID) error
}

// PoolUse is a stack that names a pool: its slug and the space it lives
// in.
type PoolUse struct {
	Stack string
	Space kernel.ULID
}

// PoolGuard is a domain whose rows name pools. A pool's delete and move
// ask it, after the pool's row is locked, which of its rows name the pool.
type PoolGuard interface {
	// PoolInUse lists the uses of acct's pool id.
	PoolInUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) ([]PoolUse, error)
}

// Claim is a worker's claim of a run: the worker, its account and pool,
// and DriftLimit, the pool's drift detection run limit, nil or negative
// for none.
type Claim struct {
	Account    kernel.AccountID
	Pool       kernel.ULID
	Worker     kernel.ULID
	DriftLimit *int
}

// Assignment is a claimed run and the state the claim moved it to:
// PREPARING, PREPARING_APPLY or PREPARING_REPLAN.
type Assignment struct {
	Run   kernel.ULID
	State kernel.RunState
}

// Scheduler is the runs domain's side of the worker protocol, a back edge
// internal/app binds after it constructs that domain.
//
// ref: decisions 0004 and 0008 § "Claiming runs"
type Scheduler interface {
	// Claim moves the run that has waited longest in the first tier of
	// c's pool to its preparing state for c's worker, in the transaction
	// ctx carries, skipping rows another transaction holds, so two
	// claims never take one run. It claims nothing, and reports false,
	// when no run waits, when the worker holds a run already, or when
	// only drift runs wait and the pool's drift runs on workers reach
	// DriftLimit.
	Claim(ctx context.Context, c Claim) (Assignment, bool, error)
}

// Waiting is the infraweave_runs payload that says run waits in pool,
// which wakes one held poll of the pool. The run's ID keeps two runs made
// ready in one transaction from folding into one notification, which
// PostgreSQL does to identical payloads. A payload of the pool's ID alone
// wakes every held poll of the pool.
func Waiting(pool, run kernel.ULID) string { return pool.String() + "/" + run.String() }
