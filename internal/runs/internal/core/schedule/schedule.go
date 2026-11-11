package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Store is the scheduler's storage port. Each method joins the transaction
// its context carries.
type Store interface {
	// Queued reads acct's stack's QUEUED runs, oldest first.
	Queued(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) ([]lifecycle.Run, error)
	// TakeLock makes run hold its stack's run lock when no run holds it,
	// and reports whether it does.
	TakeLock(ctx context.Context, run lifecycle.Run) (bool, error)
}

// Queue completes the scheduling job and enqueues another, each in the
// transaction ctx carries.
type Queue interface {
	lifecycle.Jobs
	CompleteTx(ctx context.Context, j queue.Job) error
}

// Deps are what the scheduler reads and writes through.
type Deps struct {
	DB        tx.Beginner
	Store     Store
	Stacks    stacksapi.Stacks
	Lifecycle *lifecycle.Lifecycle
	Queue     Queue
}

// Scheduler moves a stack's queued runs to READY.
//
// ref: decision 0008 § "From QUEUED to READY"
type Scheduler struct{ d Deps }

// New returns the scheduler over d.
func New(d Deps) *Scheduler { return &Scheduler{d: d} }

// Consumer is the outbox consumer that schedules a stack once it is
// unlocked or enabled.
const Consumer = "runs.schedule"

// gates are the guards of T2 the scheduler checks. Approval passes until
// approval policies exist, and the upstream and external dependency gates
// until stack dependencies do.
var gates = []machine.Guard{
	machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished,
	machine.GuardExternalSettled, machine.GuardStackEnabled,
}

// Handle runs a lifecycle.ScheduleKind job: it schedules the job's stack
// and completes the job in one transaction. A payload that does not decode
// never will, so its job dies at once.
func (s *Scheduler) Handle(ctx context.Context, j queue.Job) error {
	var p lifecycle.Schedule
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		//nolint:wrapcheck // marks it
		return queue.Permanent(fmt.Errorf("runs: %s %d: %w", j.Kind, j.ID, err))
	}
	//nolint:wrapcheck // each step names itself
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if err := s.Schedule(ctx, kernel.AccountID{ULID: j.Account}, p.Stack); err != nil {
			return err
		}
		return s.d.Queue.CompleteTx(ctx, j)
	})
}

// Schedule walks acct's stack's QUEUED runs oldest first in the
// transaction ctx carries, after locking the stack's row, so two walks of
// one stack, or a walk and a creation, never interleave. A disabled stack
// keeps every run waiting, and a deleted one has none. Each other run
// takes T2 to READY, a blocking run only once it takes the stack's run
// lock; a run that cannot stays QUEUED, its blocker the lock's holder.
func (s *Scheduler) Schedule(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error {
	st, err := s.d.Stacks.LockForRun(ctx, acct, stack)
	switch {
	case errors.Is(err, stacksapi.ErrNotFound):
		return nil
	case err != nil:
		return err //nolint:wrapcheck // the stacks domain names the read
	case st.Disabled:
		return nil
	}
	runs, err := s.d.Store.Queued(ctx, acct, stack)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	locked, held := false, false
	for _, run := range runs {
		if run.Type.Blocking() {
			if held {
				continue
			}
			ok, err := s.d.Store.TakeLock(ctx, run)
			if err != nil {
				return err //nolint:wrapcheck // the store names the write
			}
			held = true
			if !ok {
				continue
			}
			locked = true
		}
		if _, err := s.d.Lifecycle.Transition(ctx, lifecycle.Request{
			Account: acct, Run: run.ID, Version: run.Version, To: kernel.RunStateReady,
			Driver: machine.DriverScheduler, Checked: gates,
		}); err != nil {
			return err //nolint:wrapcheck // the lifecycle names the move
		}
	}
	if locked {
		return s.d.Lifecycle.Project(ctx, acct, stack) //nolint:wrapcheck // as above
	}
	return nil
}

// StackChanged is the outbox handler that enqueues a stack's scheduling
// when a stacksapi.Changed unlocks or enables it, completing its job in
// the same transaction; a document that does not decode never will, so
// its job dies at once.
func (s *Scheduler) StackChanged(ctx context.Context, ev outbox.Event, j queue.Job) error {
	var ch stacksapi.Changed
	if err := json.Unmarshal(ev.Document, &ch); err != nil {
		//nolint:wrapcheck // marks it
		return queue.Permanent(fmt.Errorf("runs: %s %s: %w", ev.Kind, ev.ID, err))
	}
	if ch.Change != stacksapi.ChangeUnlocked && ch.Change != stacksapi.ChangeEnabled {
		return nil
	}
	//nolint:wrapcheck // each step names itself
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		if _, err := s.d.Queue.EnqueueTx(ctx, lifecycle.ScheduleKind,
			lifecycle.Schedule{Stack: ch.Stack}, queue.Account(ev.Account),
			queue.UniqueKey("stack:"+ch.Stack.String())); err != nil {
			return err
		}
		return s.d.Queue.CompleteTx(ctx, j)
	})
}
