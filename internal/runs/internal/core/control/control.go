package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// ErrBlocking reports a cancel of the run that holds its stack's run
// lock by a caller without RUN_CANCEL_BLOCKING on the stack's space.
var ErrBlocking = errors.New("runs: the run blocks its stack")

// ErrState reports a run whose state the action does not leave from: a
// cancel of a run that has started, a priority of one that has ended, or a
// confirm or discard of one that does not wait for confirmation.
var ErrState = errors.New("runs: the run's state refuses the action")

// ErrNoHolder reports a confirm or discard that names no run, of a stack
// whose run lock no run holds.
var ErrNoHolder = errors.New("runs: no run holds the stack's run lock")

// Store writes what the lifecycle does not. Each method joins the
// transaction its context carries.
type Store interface {
	// Lock reads acct's run id and locks its row FOR UPDATE;
	// lifecycle.ErrNotFound when acct lacks it.
	Lock(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (lifecycle.Run, error)
	// Holder reads the run that holds acct's stack's run lock, false when
	// none does.
	Holder(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) (kernel.ULID, bool, error)
	// SetPriority writes run's priority and its updated_at, at.
	SetPriority(ctx context.Context, run lifecycle.Run, p kernel.RunPriority, at time.Time) error
}

// Deps are what the actions read and write through; Now is the clock,
// time.Now when nil.
type Deps struct {
	DB        tx.Beginner
	Store     Store
	Stacks    stacksapi.Stacks
	Authz     accessapi.Authorizer
	Lifecycle *lifecycle.Lifecycle
	Views     *read.Service
	Now       func() time.Time
}

// Service acts on runs at a caller's request.
type Service struct{ d Deps }

// New returns the service over d.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// Cancel cancels p's run raw of stack slug, which has not started: T4
// from QUEUED, T9 from READY, which also releases the stack's run lock
// when the run holds it. The route has decided RUN_CANCEL on the stack's
// space; the run that holds the lock also needs RUN_CANCEL_BLOCKING
// there, else ErrBlocking. A run of another stack is read.ErrNotFound,
// and one that has started ErrState.
//
// ref: decision 0008 § "Who drives each transition"
func (s *Service) Cancel(
	ctx context.Context,
	p kernel.Principal,
	slug, raw, note string,
) (read.View, error) {
	return s.act(ctx, p, slug, raw, func(ctx context.Context, st stacksapi.Stack, r lifecycle.Run) (
		lifecycle.Run, error,
	) {
		holder, held, err := s.d.Store.Holder(ctx, p.AccountID, st.ID)
		if err != nil {
			return r, err //nolint:wrapcheck // the store names the read
		}
		if held && holder == r.ID {
			ok, err := s.d.Authz.Authorize(ctx, p, accessapi.ActionRunCancelBlocking, st.Space)
			if err != nil {
				return r, err //nolint:wrapcheck // the snapshot's read names itself
			}
			if !ok {
				return r, fmt.Errorf("%w: %s", ErrBlocking, r.ID)
			}
		}
		moved, err := s.d.Lifecycle.Transition(ctx, lifecycle.Request{
			Account: p.AccountID, Run: r.ID, Version: r.Version, To: kernel.RunStateCanceled,
			Driver: machine.DriverAPI, Actor: lifecycle.ActorOf(p), Note: note,
		})
		if errors.Is(err, lifecycle.ErrRefused) {
			return r, fmt.Errorf("%w: %s is %s", ErrState, r.ID, r.State)
		}
		return moved, err //nolint:wrapcheck // the lifecycle names the move
	})
}

// Confirm confirms p's run raw of stack slug, or the run that holds the
// stack's run lock when raw is empty: T26 from UNCONFIRMED to CONFIRMED,
// which a worker's claim then takes to PREPARING_APPLY. The route has
// decided RUN_CONFIRM on the stack's space. A run in another state is
// ErrState, and an empty raw on a stack whose lock no run holds is
// ErrNoHolder.
//
// ref: decision 0008 § "Who drives each transition"
func (s *Service) Confirm(
	ctx context.Context,
	p kernel.Principal,
	slug, raw, note string,
) (read.View, error) {
	return s.decide(ctx, p, slug, raw, note, kernel.RunStateConfirmed, confirmGates...)
}

// confirmGates are the guards of T26 a confirm checks. Approval passes
// until approval policies exist, as it does for the scheduler's T2.
var confirmGates = []machine.Guard{machine.GuardApproved}

// Discard discards p's run raw of stack slug, or the run that holds the
// stack's run lock when raw is empty: T27 from UNCONFIRMED to DISCARDED,
// which ends the run and releases the lock. The route has decided
// RUN_DISCARD on the stack's space; errors are Confirm's.
func (s *Service) Discard(
	ctx context.Context,
	p kernel.Principal,
	slug, raw, note string,
) (read.View, error) {
	return s.decide(ctx, p, slug, raw, note, kernel.RunStateDiscarded)
}

// decide moves a run waiting for confirmation to to, the caller's
// decision on its plan, with checked the edge's guards it has checked.
// The machine holds the only edges, so a run in any other state is
// refused there, not here.
func (s *Service) decide(
	ctx context.Context,
	p kernel.Principal,
	slug, raw, note string,
	to kernel.RunState,
	checked ...machine.Guard,
) (read.View, error) {
	return s.act(ctx, p, slug, raw, func(ctx context.Context, _ stacksapi.Stack, r lifecycle.Run) (
		lifecycle.Run, error,
	) {
		moved, err := s.d.Lifecycle.Transition(ctx, lifecycle.Request{
			Account: p.AccountID, Run: r.ID, Version: r.Version, To: to,
			Driver: machine.DriverAPI, Checked: checked, Actor: lifecycle.ActorOf(p), Note: note,
		})
		if errors.Is(err, lifecycle.ErrRefused) {
			return r, fmt.Errorf("%w: %s is %s", ErrState, r.ID, r.State)
		}
		return moved, err //nolint:wrapcheck // the lifecycle names the move
	})
}

// SetPriority sets p's run raw of stack slug's priority, until it ends;
// ErrState once it has. The route has decided RUN_PRIORITIZE_SET on the
// stack's space.
func (s *Service) SetPriority(
	ctx context.Context,
	p kernel.Principal,
	slug, raw string,
	priority kernel.RunPriority,
) (read.View, error) {
	return s.act(ctx, p, slug, raw, func(ctx context.Context, _ stacksapi.Stack, r lifecycle.Run) (
		lifecycle.Run, error,
	) {
		if r.State.Terminal() {
			return r, fmt.Errorf("%w: %s has ended", ErrState, r.ID)
		}
		r.Priority, r.UpdatedAt = priority, s.d.Now().UTC().Truncate(time.Microsecond)
		return r, s.d.Store.SetPriority(
			ctx,
			r,
			priority,
			r.UpdatedAt,
		) //nolint:wrapcheck // as above
	})
}

// act locks p's run raw of stack slug in one transaction, or the run that
// holds the stack's run lock when raw is empty, hands it to f, and answers
// what f leaves it as.
func (s *Service) act(
	ctx context.Context,
	p kernel.Principal,
	slug, raw string,
	f func(context.Context, stacksapi.Stack, lifecycle.Run) (lifecycle.Run, error),
) (read.View, error) {
	var id kernel.ULID
	if raw != "" {
		var err error
		if id, err = kernel.ParseULID(raw); err != nil {
			return read.View{}, fmt.Errorf("%w: %q", read.ErrNotFound, raw)
		}
	}
	var v read.View
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		st, err := s.d.Stacks.BySlug(ctx, p.AccountID, slug)
		if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		if raw == "" {
			holder, held, err := s.d.Store.Holder(ctx, p.AccountID, st.ID)
			if err != nil {
				return err //nolint:wrapcheck // the store names the read
			}
			if !held {
				return fmt.Errorf("%w: %s", ErrNoHolder, slug)
			}
			id, raw = holder, holder.String()
		}
		r, err := s.d.Store.Lock(ctx, p.AccountID, id)
		if errors.Is(err, lifecycle.ErrNotFound) || err == nil && r.Stack != st.ID {
			return fmt.Errorf("%w: %s on %s", read.ErrNotFound, raw, slug)
		}
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if r, err = f(ctx, st, r); err != nil {
			return err
		}
		v, err = s.d.Views.One(ctx, p, r)
		return err //nolint:wrapcheck // the read names itself
	})
	return v, err //nolint:wrapcheck // each step names itself
}
