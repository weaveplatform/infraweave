package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/review"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// ErrBlocking reports an action on the run that holds its stack's run
// lock by a caller without the action's blocking variant on the stack's
// space; a *Blocked names the variant.
var ErrBlocking = errors.New("runs: the run blocks its stack")

// ErrState reports a run whose state refuses the action: a cancel of a run
// that has started, a priority of one that has ended, a confirm or discard
// of one that does not wait for confirmation, a stop of one that neither
// initializes nor plans, or a kill of one no stop was asked of on a
// worker. A *Refused carries the run's state.
var ErrState = errors.New("runs: the run's state refuses the action")

// Blocked is an ErrBlocking that names the blocking variant of the action
// the caller lacks.
type Blocked struct {
	Run    kernel.ULID
	Action accessapi.Action
}

func (e *Blocked) Error() string {
	return fmt.Sprintf("%v: %s needs %s", ErrBlocking, e.Run, e.Action)
}

func (e *Blocked) Unwrap() error { return ErrBlocking }

// Refused is an ErrState with the run's state now and what the action
// needs, which the refusal answers.
//
// ref: report 08 § "6. Run actions" (C36)
type Refused struct {
	Run   kernel.ULID
	State kernel.RunState
	Needs string
}

func (e *Refused) Error() string {
	return fmt.Sprintf("%v: %s is %s; %s", ErrState, e.Run, e.State, e.Needs)
}

func (e *Refused) Unwrap() error { return ErrState }

// What each action needs of a run, which its refusal answers.
const (
	needsCancel   = "a cancel needs a run that has not started"
	needsDecision = "a confirm or discard needs a run that waits for confirmation"
	needsPriority = "a priority needs a run that has not ended"
	needsStop     = "a stop needs a run that initializes, or a proposed or tracked run that plans"
	needsKill     = "a kill needs a run on a worker that a stop was asked of"
	needsReplan   = "a targeted replan needs a tracked run that waits for confirmation"
)

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
	// Request records sig as run's request of kind, and its updated_at,
	// sig.At, and notifies the held polls of run's pool on commit, so its
	// worker's poll delivers it.
	Request(ctx context.Context, run lifecycle.Run, kind workersapi.Signal, sig lifecycle.Signal) error
	// Overrides opens run's environment variable overrides, none for a run
	// without.
	Overrides(ctx context.Context, run lifecycle.Run) ([]lifecycle.Override, error)
	// Comment records c on run.
	Comment(ctx context.Context, run lifecycle.Run, c read.Comment) error
	// SetTargets records targets as the addresses of run's targeted
	// replan.
	SetTargets(ctx context.Context, run lifecycle.Run, targets []string) error
	// Discardable reads acct's stack's runs in QUEUED, READY or
	// UNCONFIRMED, oldest first.
	Discardable(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) ([]lifecycle.Run, error)
	// PlanningProposed locks this branch's older proposed runs still planning,
	// without a stop already requested, in ID order.
	PlanningProposed(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, branch, newHash string) ([]lifecycle.Run, error)
}

// Deps are what the actions read and write through; Now is the clock,
// time.Now when nil. Spaces holds the owner while an internal superseding
// request records worker signals.
type Deps struct {
	DB             tx.Beginner
	Store          Store
	Stacks         stacksapi.Stacks
	Spaces         accessapi.Spaces
	Authz          accessapi.Authorizer
	Lifecycle      *lifecycle.Lifecycle
	Views          *read.Service
	Now            func() time.Time
	Reviews        review.Store
	ReviewMetadata accessapi.ReviewMetadataReader
	Scheduler      *schedule.Scheduler
}

// Service acts on runs at a caller's request.
type Service struct {
	d       Deps
	mu      sync.RWMutex
	sources stacksapi.StackSources
}

// New returns the service over d. A retry is unavailable until
// BindSources binds the vcs domain's resolution of a commit.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// BindSources binds the vcs domain's resolution of a commit, which a
// retry checks its run's commit with.
func (s *Service) BindSources(src stacksapi.StackSources) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources = src
}

// Cancel cancels p's run raw of stack slug, which has not started: T4
// from QUEUED, T9 from READY, which also releases the stack's run lock
// when the run holds it. The route has decided RUN_CANCEL on the stack's
// space; the run that holds the lock also needs RUN_CANCEL_BLOCKING
// there, else ErrBlocking. A run of another stack is read.ErrNotFound,
// and one that has started ErrState.
//
// ref: decision 0008 § "Who drives each transition"
func (s *Service) Cancel(ctx context.Context, p kernel.Principal, slug, raw, note string) (read.View, error) {
	return s.act(ctx, p, slug, raw, func(ctx context.Context, st stacksapi.Stack, r lifecycle.Run) (lifecycle.Run, error) {
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
				return r, &Blocked{Run: r.ID, Action: accessapi.ActionRunCancelBlocking}
			}
		}
		moved, err := s.d.Lifecycle.Transition(ctx, lifecycle.Request{
			Account: p.AccountID, Run: r.ID, Version: r.Version, To: kernel.RunStateCanceled,
			Driver: machine.DriverAPI, Actor: lifecycle.ActorOf(p), Note: note,
		})
		if errors.Is(err, lifecycle.ErrRefused) {
			return r, &Refused{Run: r.ID, State: r.State, Needs: needsCancel}
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
func (s *Service) Confirm(ctx context.Context, p kernel.Principal, slug, raw, note string) (read.View, error) {
	var refusal *Refused
	v, err := s.act(ctx, p, slug, raw, func(ctx context.Context, _ stacksapi.Stack, r lifecycle.Run) (lifecycle.Run, error) {
		if r.State != kernel.RunStateUnconfirmed {
			return r, &Refused{Run: r.ID, State: r.State, Needs: needsDecision}
		}
		result, err := s.d.Lifecycle.EvaluateApproval(ctx, r)
		if err != nil {
			return r, err //nolint:wrapcheck // the owner names the failing operation
		}
		moved, err := s.d.Lifecycle.ApplyApproval(ctx, r, result)
		if err != nil {
			return r, err //nolint:wrapcheck // the owner names the failing operation
		}
		if !lifecycle.Approved(result) {
			refusal = &Refused{Run: r.ID, State: moved.State, Needs: "confirmation needs approval from every applicable policy"}
			if moved.Version == r.Version {
				err = s.d.Lifecycle.Refresh(ctx, moved)
			}
			return moved, err //nolint:wrapcheck // the owner names the failing operation
		}
		return s.d.Lifecycle.Transition(ctx, lifecycle.Request{
			Account: p.AccountID, Run: r.ID, Version: r.Version,
			To: kernel.RunStateConfirmed, Driver: machine.DriverAPI, Checked: confirmGates, Actor: lifecycle.ActorOf(p), Note: note,
		})
	})
	if err != nil {
		return read.View{}, err
	}
	// Refusal is emitted after commit: returning it inside act would erase
	// the receipts and any captured rejection transition.
	if refusal != nil {
		return v, refusal
	}
	return v, nil
}

// confirmGates names the guard checked by the fresh approval evaluation.
var confirmGates = []machine.Guard{machine.GuardApproved}

// Discard discards p's run raw of stack slug, or the run that holds the
// stack's run lock when raw is empty: T27 from UNCONFIRMED to DISCARDED,
// which ends the run and releases the lock. The route has decided
// RUN_DISCARD on the stack's space; errors are Confirm's.
func (s *Service) Discard(ctx context.Context, p kernel.Principal, slug, raw, note string) (read.View, error) {
	return s.decide(ctx, p, slug, raw, note, kernel.RunStateDiscarded)
}

// Replan plans p's run raw of stack slug again against targets: T28 from
// UNCONFIRMED to REPLAN_REQUESTED, which a worker's claim takes to
// PREPARING_REPLAN with targets in the run's specification. The route has
// decided RUN_TARGETED_REPLAN on the stack's space. Targets that
// kernel.CheckTargets refuses are a *kernel.Invalid, and a run in another
// state, or of another type, a *Refused. The move comes before the
// targets' write, which runs' CHECK holds to a tracked run.
//
// ref: decision 0008 § "Who drives each transition"
func (s *Service) Replan(ctx context.Context, p kernel.Principal, slug, raw string, targets []string) (read.View, error) {
	if errs := kernel.CheckTargets("/targets", targets); errs != nil {
		return read.View{}, &kernel.Invalid{
			Detail: "The replan's targets are refused.",
			Errors: errs,
		}
	}
	return s.act(ctx, p, slug, raw, func(ctx context.Context, _ stacksapi.Stack, r lifecycle.Run) (lifecycle.Run, error) {
		moved, err := s.d.Lifecycle.Transition(ctx, lifecycle.Request{
			Account: p.AccountID, Run: r.ID, Version: r.Version,
			To: kernel.RunStateReplanRequested, Driver: machine.DriverAPI,
			Actor: lifecycle.ActorOf(p),
		})
		if errors.Is(err, lifecycle.ErrRefused) {
			return r, &Refused{Run: r.ID, State: r.State, Needs: needsReplan}
		}
		if err != nil {
			return r, err //nolint:wrapcheck // the lifecycle names the move
		}
		moved.ReplanTargets = targets
		return moved, s.d.Store.SetTargets(ctx, moved, targets) //nolint:wrapcheck // as above
	})
}

// decide moves a run waiting for confirmation to to, the caller's
// decision on its plan, with checked the edge's guards it has checked.
// The machine holds the only edges, so a run in any other state is
// refused there, not here.
func (s *Service) decide(ctx context.Context, p kernel.Principal, slug, raw, note string, to kernel.RunState, checked ...machine.Guard) (read.View, error) {
	return s.act(ctx, p, slug, raw, func(ctx context.Context, _ stacksapi.Stack, r lifecycle.Run) (lifecycle.Run, error) {
		moved, err := s.d.Lifecycle.Transition(ctx, lifecycle.Request{
			Account: p.AccountID, Run: r.ID, Version: r.Version, To: to,
			Driver: machine.DriverAPI, Checked: checked, Actor: lifecycle.ActorOf(p), Note: note,
		})
		if errors.Is(err, lifecycle.ErrRefused) {
			return r, &Refused{Run: r.ID, State: r.State, Needs: needsDecision}
		}
		return moved, err //nolint:wrapcheck // the lifecycle names the move
	})
}

// SetPriority sets p's run raw of stack slug's priority, until it ends;
// ErrState once it has. The route has decided RUN_PRIORITIZE_SET on the
// stack's space.
func (s *Service) SetPriority(ctx context.Context, p kernel.Principal, slug, raw string, priority kernel.RunPriority) (read.View, error) {
	return s.act(ctx, p, slug, raw, func(ctx context.Context, _ stacksapi.Stack, r lifecycle.Run) (lifecycle.Run, error) {
		if r.State.Terminal() {
			return r, &Refused{Run: r.ID, State: r.State, Needs: needsPriority}
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

// Stop asks the worker of p's run raw of stack slug to stop it: the
// launcher sends the runner SIGINT, which passes it to the running
// command, and the runner reports stopped once the command ends (T16,
// T19). A run that initializes, or a proposed or tracked one that plans,
// may be stopped; the route has decided RUN_STOP on the stack's space,
// and the run that holds the stack's run lock also needs
// RUN_STOP_BLOCKING there, else a *Blocked. A run in another state, or in
// a state other than state when state is not empty, is a *Refused. A run
// a stop was asked of already is answered as it is, so its worker gets
// one SIGINT.
//
// ref: decision 0008 § "Stop and kill"
func (s *Service) Stop(ctx context.Context, p kernel.Principal, slug, raw, note string, state kernel.RunState) (read.View, error) {
	return s.signal(ctx, p, slug, raw, note, state, workersapi.SignalStop)
}

// Kill asks the worker of p's run raw of stack slug to kill its
// container, after a stop that did not end it: liveness then fails the
// run with the note "killed", through the edge out of the state it is in.
// A run on a worker that a stop was asked of may be killed; the route has
// decided RUN_KILL on the stack's space, and the run that holds the
// stack's run lock also needs RUN_KILL_BLOCKING there. A repeated kill,
// and refusals, are as Stop's.
//
// ref: decision 0008 § "Stop and kill"
func (s *Service) Kill(ctx context.Context, p kernel.Principal, slug, raw, note string, state kernel.RunState) (read.View, error) {
	return s.signal(ctx, p, slug, raw, note, state, workersapi.SignalKill)
}

// signal records p's request of kind for the run, as Stop and Kill say.
func (s *Service) signal(ctx context.Context, p kernel.Principal, slug, raw, note string, state kernel.RunState, kind workersapi.Signal) (read.View, error) {
	return s.act(ctx, p, slug, raw, func(ctx context.Context, st stacksapi.Stack, r lifecycle.Run) (lifecycle.Run, error) {
		asked, ok, needs, blocking := r.Stop, r.Stoppable(), needsStop,
			accessapi.ActionRunStopBlocking
		if kind == workersapi.SignalKill {
			asked, ok, needs, blocking = r.Kill, r.Killable(), needsKill,
				accessapi.ActionRunKillBlocking
		}
		switch {
		case state != "" && state != r.State:
			return r, &Refused{Run: r.ID, State: r.State, Needs: "the request names " +
				string(state)}
		case !ok:
			return r, &Refused{Run: r.ID, State: r.State, Needs: needs}
		case asked != nil:
			return r, nil
		}
		holder, held, err := s.d.Store.Holder(ctx, p.AccountID, st.ID)
		if err != nil {
			return r, err //nolint:wrapcheck // the store names the read
		}
		if held && holder == r.ID {
			ok, err := s.d.Authz.Authorize(ctx, p, blocking, st.Space)
			if err != nil {
				return r, err //nolint:wrapcheck // the snapshot's read names itself
			}
			if !ok {
				return r, &Blocked{Run: r.ID, Action: blocking}
			}
		}
		sig := lifecycle.Signal{
			At: s.d.Now().UTC().Truncate(time.Microsecond), By: p.Login, Note: note,
		}
		if err := s.d.Store.Request(ctx, r, kind, sig); err != nil {
			return r, err //nolint:wrapcheck // the store names the write
		}
		r.UpdatedAt = sig.At
		if kind == workersapi.SignalKill {
			r.Kill = &sig
		} else {
			r.Stop = &sig
		}
		return r, nil
	})
}

// act locks p's run raw of stack slug in one transaction, or the run that
// holds the stack's run lock when raw is empty, hands it to f, and answers
// the run f answers: the run as f leaves it, or another f made.
func (s *Service) act(
	ctx context.Context,
	p kernel.Principal,
	slug, raw string,
	f func(context.Context, stacksapi.Stack, lifecycle.Run) (lifecycle.Run, error),
) (read.View, error) {
	var v read.View
	err := s.in(ctx, p, slug, raw, func(ctx context.Context, st stacksapi.Stack, r lifecycle.Run) error {
		r, err := f(ctx, st, r)
		if err != nil {
			return err
		}
		v, err = s.d.Views.One(ctx, p, r)
		return err //nolint:wrapcheck // the read names itself
	})
	return v, err
}

// in locks p's run raw of stack slug in one transaction, or the run that
// holds the stack's run lock when raw is empty, and hands it to f.
func (s *Service) in(ctx context.Context, p kernel.Principal, slug, raw string, f func(context.Context, stacksapi.Stack, lifecycle.Run) error) error {
	var id kernel.ULID
	if raw != "" {
		var err error
		if id, err = kernel.ParseULID(raw); err != nil {
			return fmt.Errorf("%w: %q", read.ErrNotFound, raw)
		}
	}
	err := tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		st, err := s.d.Stacks.BySlug(ctx, p.AccountID, slug)
		if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		if s.d.Spaces != nil {
			if err := s.d.Spaces.Hold(ctx, p.AccountID, st.Space); err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
		}
		locked, err := s.d.Stacks.LockForRun(ctx, p.AccountID, st.ID)
		if err != nil {
			return err //nolint:wrapcheck // the owner names the failing operation
		}
		if locked.Space != st.Space {
			return kernel.ErrUnavailable
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
		return f(ctx, st, r)
	})
	return err //nolint:wrapcheck // each step names itself
}
