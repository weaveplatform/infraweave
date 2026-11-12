package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// pool reads acct's pool id; pool.ErrNotFound when acct lacks it.
func (s *Service) pool(ctx context.Context, acct kernel.AccountID, id string) (pool.Pool, error) {
	ulid, err := kernel.ParseULID(id)
	if err != nil {
		return pool.Pool{}, fmt.Errorf("%w: %s", pool.ErrNotFound, id)
	}
	return s.store.Get(ctx, acct, ulid, false) //nolint:wrapcheck // the store names the read
}

// DrainSet drains or undrains acct's worker id of the pool named, and
// answers it. A drained worker takes no new run and finishes its current
// one; its held poll answers at once. The route has decided
// WORKER_DRAIN_SET on the pool's space; a worker the pool lacks is a
// *pool.Missing.
//
// ref: decision 0008 § "Worker status, drain, cycle and reset"
func (s *Service) DrainSet(
	ctx context.Context,
	acct kernel.AccountID,
	poolID, id string,
	drain bool,
) (Worker, error) {
	p, err := s.pool(ctx, acct, poolID)
	if err != nil {
		return Worker{}, err
	}
	ulid, err := kernel.ParseULID(id)
	if err != nil {
		return Worker{}, &pool.Missing{Kind: "worker", ID: id}
	}
	w, err := s.store.Drain(ctx, acct, p.ID, ulid, drain)
	if errors.Is(err, ErrNotFound) {
		return Worker{}, &pool.Missing{Kind: "worker", ID: id}
	}
	return w, err //nolint:wrapcheck // the store names the write
}

// Cycle asks every worker of acct's pool named to exit: an idle launcher
// at once, a busy one after its run. The route has decided
// WORKER_POOL_CYCLE on the pool's space.
func (s *Service) Cycle(ctx context.Context, acct kernel.AccountID, poolID string) error {
	p, err := s.pool(ctx, acct, poolID)
	if err != nil {
		return err
	}
	return s.store.Cycle(ctx, acct, p.ID) //nolint:wrapcheck // the store names the write
}

// Search reads a page of the workers of acct's pool named. The route has
// decided SPACE_READ on the pool's space.
func (s *Service) Search(
	ctx context.Context,
	acct kernel.AccountID,
	poolID string,
	in search.Input,
) (search.Result[Worker], error) {
	p, err := s.pool(ctx, acct, poolID)
	if err != nil {
		return search.Result[Worker]{}, err
	}
	//nolint:wrapcheck // the store names the read; a refusal is answered as it is
	return s.store.SearchWorkers(ctx, acct, p.ID, p.Space, in)
}

// Suggest lists the values the workers of acct's pool named hold. The
// route has decided SPACE_READ on the pool's space.
func (s *Service) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	poolID string,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	p, err := s.pool(ctx, acct, poolID)
	if err != nil {
		return search.Suggestions{}, err
	}
	//nolint:wrapcheck // as above
	return s.store.SuggestWorkers(ctx, acct, p.ID, p.Space, in)
}

// OfPools reads the workers of acct's pools, by pool, oldest first.
func (s *Service) OfPools(
	ctx context.Context,
	acct kernel.AccountID,
	pools []kernel.ULID,
) (map[kernel.ULID][]Worker, error) {
	return s.store.OfPools(ctx, acct, pools) //nolint:wrapcheck // the store names the read
}
