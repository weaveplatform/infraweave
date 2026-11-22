package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// ErrOwnership reports a bind, release or registration that the pool's
// hosting binding refuses: another owner holds it, workers registered
// before it, or work remains on the pool's workers.
var ErrOwnership = errors.New("workers: the pool's hosting ownership refuses this request")

// BindAssignments binds the runs domain's reader of runs on a pool's
// workers, which bind and release consult; until then both refuse with
// kernel.ErrUnavailable rather than release a pool whose work is unknown.
func (s *Service) BindAssignments(a workersapi.PoolAssignments) { s.assignments = a }

// Bind records the owner a pool-signed proof names as the only controller
// hosting its pool. The same owner binding again succeeds. A pool already
// bound to another owner, managed for another resource, holding workers or
// work registered without a binding, or an owner that hosts or managed
// another pool is ErrOwnership.
func (s *Service) Bind(ctx context.Context, raw string) error {
	now := s.now()
	c, leaf, id, err := s.proof(ctx, raw, now)
	if err != nil {
		return err
	}
	owner, err := ownerOf(c)
	if err != nil {
		return err
	}
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // each step names itself
		p, err := s.admit(ctx, c, leaf, id, false, now)
		if err != nil {
			return err
		}
		bound, ok, err := s.store.Binding(ctx, p.ID)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if ok {
			if bound != owner {
				return fmt.Errorf("%w: pool %s is bound to another owner", ErrOwnership, p.ID)
			}
			return nil
		}
		if p.Management != nil && *p.Management != owner {
			return fmt.Errorf("%w: pool %s is managed for another resource", ErrOwnership, p.ID)
		}
		managed, reserved, err := s.store.ManagedPool(ctx, owner)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if reserved && (managed == nil || *managed != p.ID) {
			return fmt.Errorf("%w: the owner manages another pool", ErrOwnership)
		}
		if err := s.idle(ctx, p.ID); err != nil {
			return err
		}
		return s.store.Bind(ctx, p, owner) //nolint:wrapcheck // the store names the write
	})
}

// Release removes the hosting binding the proof's owner holds once its pool
// has no registered worker and no run on a worker. A pool with no binding
// succeeds; another owner's binding, a remaining worker or a run on a
// worker is ErrOwnership. The pool, its settings and its certificate stay.
func (s *Service) Release(ctx context.Context, raw string) error {
	now := s.now()
	c, leaf, id, err := s.proof(ctx, raw, now)
	if err != nil {
		return err
	}
	owner, err := ownerOf(c)
	if err != nil {
		return err
	}
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // each step names itself
		p, err := s.admit(ctx, c, leaf, id, false, now)
		if err != nil {
			return err
		}
		bound, ok, err := s.store.Binding(ctx, p.ID)
		if err != nil || !ok {
			return err //nolint:wrapcheck // the store names the read
		}
		if bound != owner {
			return fmt.Errorf("%w: pool %s is bound to another owner", ErrOwnership, p.ID)
		}
		if err := s.idle(ctx, p.ID); err != nil {
			return err
		}
		return s.store.Unbind(ctx, p.ID) //nolint:wrapcheck // the store names the write
	})
}

// hosted admits a registration naming owner to the locked binding of p:
// a bound pool takes only its owner's registrations, and an owner cannot
// register to a pool it has not bound.
func (s *Service) hosted(ctx context.Context, p pool.Pool, owner *kernel.WorkerOwner) error {
	bound, ok, err := s.store.Binding(ctx, p.ID)
	switch {
	case err != nil:
		return err //nolint:wrapcheck // the store names the read
	case ok && owner == nil:
		return fmt.Errorf("%w: pool %s is hosted by a controller", ErrOwnership, p.ID)
	case ok && *owner != bound:
		return fmt.Errorf("%w: pool %s is bound to another owner", ErrOwnership, p.ID)
	case !ok && owner != nil:
		return fmt.Errorf("%w: pool %s has no hosting binding", ErrOwnership, p.ID)
	}
	return nil
}

// idle refuses, as ErrOwnership, a pool that still has a registered
// worker or a run on a worker, which a deregistration can leave until run
// recovery ends it.
func (s *Service) idle(ctx context.Context, id kernel.ULID) error {
	busy, err := s.store.HasWorkers(ctx, id)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	if busy {
		return fmt.Errorf("%w: pool %s has registered workers", ErrOwnership, id)
	}
	if s.assignments == nil {
		return fmt.Errorf("workers: pool assignment reader is not bound: %w", kernel.ErrUnavailable)
	}
	busy, err = s.assignments.ActivePool(ctx, id)
	if err != nil {
		return err //nolint:wrapcheck // the runs domain names the read
	}
	if busy {
		return fmt.Errorf("%w: pool %s has runs on workers", ErrOwnership, id)
	}
	return nil
}

// ownerOf is the owner an ownership proof names, which it must name.
func ownerOf(c claims) (kernel.WorkerOwner, error) {
	if c.Owner == nil {
		return kernel.WorkerOwner{}, &kernel.Invalid{
			Detail: "An ownership proof names its owner.",
			Errors: []kernel.FieldError{{Pointer: "/proof/owner", Reason: kernel.ReasonRequired, Message: "Name the hosting owner."}},
		}
	}
	return *c.Owner, nil
}
