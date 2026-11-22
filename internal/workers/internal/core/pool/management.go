package pool

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Managed reads a live pool for the exact resource, then applies SPACE_READ to
// its stored space. The identity is a lookup key, never an authorization grant.
func (s *Service) Managed(ctx context.Context, p kernel.Principal, owner kernel.WorkerOwner) (Pool, error) {
	if faults := kernel.CheckWorkerOwner("/management", owner); len(faults) > 0 {
		return Pool{}, &kernel.Invalid{Detail: "The worker pool management identity is refused.", Errors: faults}
	}
	out, err := s.store.Managed(ctx, p.AccountID, owner)
	if err != nil {
		return Pool{}, fmt.Errorf("workers: read managed pool: %w", err)
	}
	if err := s.require(ctx, p, accessapi.ActionSpaceRead, out.Space); err != nil {
		if _, denied := errors.AsType[*Denied](err); denied {
			return Pool{}, fmt.Errorf("%w: managed resource", ErrNotFound)
		}
		return Pool{}, fmt.Errorf("workers: authorize managed pool: %w", err)
	}
	return out, nil
}
