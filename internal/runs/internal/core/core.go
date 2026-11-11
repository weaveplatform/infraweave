package core

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Store is the runs domain's storage port.
type Store interface {
	// Blockers reads which run holds the run lock of each of acct's
	// stacks, keyed by stack ID; a stack whose lock no run holds is
	// absent.
	Blockers(
		ctx context.Context,
		acct kernel.AccountID,
		stacks []kernel.ULID,
	) (map[kernel.ULID]kernel.ULID, error)
	// StackOf reads the stack of acct's run, false when acct lacks it.
	StackOf(ctx context.Context, acct kernel.AccountID, run kernel.ULID) (kernel.ULID, bool, error)
	// DeleteStack deletes acct's stack's runs with their transitions,
	// and its run lock, tracked run and outputs, in the context's
	// transaction.
	DeleteStack(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error
}

// Service is the runs domain's core: the stacksapi.StackStatus the stack
// view reads, and the stacksapi.StackGuard a stack's move and delete ask.
type Service struct{ store Store }

// New returns the service over store.
func New(store Store) *Service { return &Service{store: store} }

// Blockers answers the run that holds each of acct's stacks' run lock,
// the run blocking it.
func (s *Service) Blockers(
	ctx context.Context,
	acct kernel.AccountID,
	stacks []kernel.ULID,
) (map[kernel.ULID]kernel.ULID, error) {
	if len(stacks) == 0 {
		return map[kernel.ULID]kernel.ULID{}, nil
	}
	return s.store.Blockers(ctx, acct, stacks) //nolint:wrapcheck // the store names the read
}

// RunStack answers the stack of acct's run; stacksapi.ErrNotFound when
// acct lacks the run.
func (s *Service) RunStack(
	ctx context.Context,
	acct kernel.AccountID,
	run kernel.ULID,
) (kernel.ULID, error) {
	stack, ok, err := s.store.StackOf(ctx, acct, run)
	if err != nil {
		return kernel.ULID{}, err //nolint:wrapcheck // the store names the read
	}
	if !ok {
		return kernel.ULID{}, fmt.Errorf("%w: run %s", stacksapi.ErrNotFound, run)
	}
	return stack, nil
}

// LostOnMove answers nothing: a stack's runs belong to the stack, not to
// its space, so a move keeps them all.
func (s *Service) LostOnMove(context.Context, stacksapi.StackMove) ([]string, error) {
	return nil, nil
}

// Deleting deletes the stack's runs, with their transitions, and its run
// lock, tracked run and outputs, in the delete's transaction, after the
// stack's row is locked, so a run written under a hold that committed
// first goes too.
func (s *Service) Deleting(ctx context.Context, st stacksapi.Stack) error {
	//nolint:wrapcheck // the store names the delete
	return s.store.DeleteStack(ctx, st.Account, st.ID)
}
