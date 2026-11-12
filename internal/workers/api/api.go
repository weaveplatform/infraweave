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
