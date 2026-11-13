package core

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Store is the runs domain's storage port.
type Store interface {
	// Blockers reads which run holds the run lock of each of acct's
	// stacks, keyed by stack ID, with its delta while it waits in
	// UNCONFIRMED; a stack whose lock no run holds is absent.
	Blockers(
		ctx context.Context,
		acct kernel.AccountID,
		stacks []kernel.ULID,
	) (map[kernel.ULID]stacksapi.Blocker, error)
	// StackOf reads the stack of acct's run, false when acct lacks it.
	StackOf(ctx context.Context, acct kernel.AccountID, run kernel.ULID) (kernel.ULID, bool, error)
	// RunOf reads run's account, stack and state, whichever account has
	// it, without locking its row; false when no account does.
	RunOf(ctx context.Context, run kernel.ULID) (Run, bool, error)
	// DeleteStack deletes acct's stack's runs with their transitions,
	// and its run lock, tracked run and outputs, in the context's
	// transaction.
	DeleteStack(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error
}

// Run is a run as Access reads it.
type Run struct {
	Account kernel.AccountID
	Stack   kernel.ULID
	State   kernel.RunState
}

// Service is the runs domain's core: the stacksapi.StackStatus the stack
// view reads, the stacksapi.StackGuard a stack's move and delete ask, and
// the runsapi.StateAccess the state domain asks.
type Service struct{ store Store }

// New returns the service over store.
func New(store Store) *Service { return &Service{store: store} }

// Blockers answers the run that holds each of acct's stacks' run lock,
// the run blocking it.
func (s *Service) Blockers(
	ctx context.Context,
	acct kernel.AccountID,
	stacks []kernel.ULID,
) (map[kernel.ULID]stacksapi.Blocker, error) {
	if len(stacks) == 0 {
		return map[kernel.ULID]stacksapi.Blocker{}, nil
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

// grants are the run states that grant their run access to its stack's
// state: reading and a shared lock while the run plans, and writing under
// an exclusive lock while it applies, destroys or performs a task. Every
// other state, terminal or not, grants nothing.
//
// ref: decision 0010 § "Who may call what"
var grants = map[kernel.RunState]runsapi.StateGrant{
	kernel.RunStateInitializing: {Read: true, Lock: runsapi.StateLockShared},
	kernel.RunStatePlanning:     {Read: true, Lock: runsapi.StateLockShared},
	kernel.RunStateApplying:     {Read: true, Lock: runsapi.StateLockExclusive, Write: true},
	kernel.RunStateDestroying:   {Read: true, Lock: runsapi.StateLockExclusive, Write: true},
	kernel.RunStatePerforming:   {Read: true, Lock: runsapi.StateLockExclusive, Write: true},
}

// Access answers run's grant on stack's state from the state it reads the
// run in: the zero grant when no account has the run, and the run's
// account alone when it is another stack's or grants holds no entry for
// its state.
func (s *Service) Access(ctx context.Context, run, stack kernel.ULID) (runsapi.StateGrant, error) {
	r, ok, err := s.store.RunOf(ctx, run)
	if err != nil || !ok {
		return runsapi.StateGrant{}, err //nolint:wrapcheck // the store names the read
	}
	g := grants[r.State]
	if r.Stack != stack {
		g = runsapi.StateGrant{}
	}
	g.Account = r.Account
	return g, nil
}
