package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/schedule"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// ScanKind fans account changes into bounded pages of stack-owned jobs. A
// deletion has no policy row left to consult, so selection reads waiting runs.
var ScanKind = queue.Kind{Name: "run.approval.scan", Queue: queue.Default, Policy: queue.Coalesce}

// ReevaluateKind coalesces bursts while ensuring a change during evaluation
// schedules one more evaluation against the latest committed policy snapshot.
var ReevaluateKind = queue.Kind{Name: "run.approval.reevaluate", Queue: queue.Default, Policy: queue.Coalesce}

// ReconcileStore lists account-owned waiting work without reaching policy rows.
type ReconcileStore interface {
	WaitingApprovalStacks(context.Context, kernel.AccountID, kernel.ULID, int) ([]kernel.ULID, error)
	WaitingApprovals(context.Context, kernel.AccountID, kernel.ULID) ([]lifecycle.Run, error)
	Lock(context.Context, kernel.AccountID, kernel.ULID) (lifecycle.Run, error)
}

// ReconcileDeps shares the same fenced lifecycle used by public review actions.
type ReconcileDeps struct {
	DB        tx.Beginner
	Store     ReconcileStore
	Stacks    stacksapi.Stacks
	Spaces    accessapi.Spaces
	Jobs      schedule.Queue
	Lifecycle *lifecycle.Lifecycle
	Scheduler *schedule.Scheduler
}

var errPayload = errors.New("runs: invalid approval change payload")

// Reconciler durably reevaluates waiting runs after policy or owner changes.
type Reconciler struct{ d ReconcileDeps }

// NewReconciler constructs the change consumer and its queued handlers.
func NewReconciler(d ReconcileDeps) *Reconciler { return &Reconciler{d: d} }

type scanPage struct {
	After kernel.ULID `json:"after"`
}

// Changed writes follow-up work and acknowledges the source event atomically.
func (s *Reconciler) Changed(ctx context.Context, ev outbox.Event, j queue.Job) error {
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // owners identify failures
		account := kernel.AccountID{ULID: ev.Account}
		switch ev.Kind {
		case policyapi.ChangedKind:
			var ch policyapi.Changed
			if err := json.Unmarshal(ev.Document, &ch); err != nil || ch.Policy.IsZero() {
				return queue.Permanent(fmt.Errorf("%w: policy change", errPayload))
			}
			if err := s.scan(ctx, account, kernel.ULID{}, "account:"+account.String()); err != nil {
				return err
			}
		case stacksapi.ChangedKind:
			var ch stacksapi.Changed
			if err := json.Unmarshal(ev.Document, &ch); err != nil || ch.Stack.IsZero() {
				return queue.Permanent(fmt.Errorf("%w: stack change", errPayload))
			}
			if ch.Change != stacksapi.ChangeDeleted {
				if err := s.stack(ctx, account, ch.Stack); err != nil {
					return err
				}
			}
		default:
			return queue.Permanent(fmt.Errorf("%w: unsupported change kind", errPayload))
		}
		return s.d.Jobs.CompleteTx(ctx, j) //nolint:wrapcheck // queue names completion
	})
}

func (s *Reconciler) scan(ctx context.Context, account kernel.AccountID, after kernel.ULID, key string) error {
	_, err := s.d.Jobs.EnqueueTx(ctx, ScanKind, scanPage{After: after}, queue.Account(account.ULID), queue.UniqueKey(key))
	return err //nolint:wrapcheck // queue names enqueue
}

func (s *Reconciler) stack(ctx context.Context, account kernel.AccountID, stack kernel.ULID) error {
	_, err := s.d.Jobs.EnqueueTx(ctx, ReevaluateKind, lifecycle.Schedule{Stack: stack}, queue.Account(account.ULID), queue.UniqueKey("stack:"+stack.String()))
	return err //nolint:wrapcheck // queue names enqueue
}

// Scan holds no run locks while fanning one bounded page into durable jobs.
func (s *Reconciler) Scan(ctx context.Context, j queue.Job) error {
	var page scanPage
	if err := json.Unmarshal(j.Payload, &page); err != nil {
		return queue.Permanent(fmt.Errorf("runs: invalid approval scan payload: %w", err)) //nolint:wrapcheck // the owner names the failing operation
	}
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // owners identify failures
		account := kernel.AccountID{ULID: j.Account}
		const batch = 25
		stacks, err := s.d.Store.WaitingApprovalStacks(ctx, account, page.After, batch+1)
		if err != nil {
			return err //nolint:wrapcheck // the owner names the failing operation
		}
		for _, id := range stacks[:min(batch, len(stacks))] {
			if err := s.stack(ctx, account, id); err != nil {
				return err
			}
		}
		if len(stacks) > batch {
			after := stacks[batch-1]
			if err := s.scan(ctx, account, after, "after:"+after.String()); err != nil {
				return err
			}
		}
		return s.d.Jobs.CompleteTx(ctx, j) //nolint:wrapcheck // queue names completion
	})
}

// Reevaluate rechecks current state after taking space, stack and run fences.
func (s *Reconciler) Reevaluate(ctx context.Context, j queue.Job) error {
	var payload lifecycle.Schedule
	if err := json.Unmarshal(j.Payload, &payload); err != nil || payload.Stack.IsZero() {
		return queue.Permanent(fmt.Errorf("%w: reevaluation", errPayload)) //nolint:wrapcheck // the owner names the failing operation
	}
	return tx.Do(ctx, s.d.DB, tx.Options{Serializable: true}, func(ctx context.Context) error { //nolint:wrapcheck // owners identify failures
		account := kernel.AccountID{ULID: j.Account}
		st, err := s.d.Stacks.Get(ctx, account, payload.Stack)
		if errors.Is(err, stacksapi.ErrNotFound) {
			return s.d.Jobs.CompleteTx(ctx, j)
		}
		if err != nil {
			return err //nolint:wrapcheck // the owner names the failing operation
		}
		if err := s.d.Spaces.Hold(ctx, account, st.Space); err != nil {
			return err //nolint:wrapcheck // the owner names the failing operation
		}
		locked, err := s.d.Stacks.LockForRun(ctx, account, st.ID)
		if err != nil {
			return err //nolint:wrapcheck // the owner names the failing operation
		}
		if locked.Space != st.Space {
			return kernel.ErrUnavailable
		}
		if err := s.d.Scheduler.Schedule(ctx, account, st.ID); err != nil {
			return err //nolint:wrapcheck // the owner names the failing operation
		}
		runs, err := s.d.Store.WaitingApprovals(ctx, account, st.ID)
		if err != nil {
			return err //nolint:wrapcheck // the owner names the failing operation
		}
		for _, r := range runs {
			current, err := s.d.Store.Lock(ctx, account, r.ID)
			if err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
			if current.State != r.State || current.Version != r.Version {
				continue
			}
			result, err := s.d.Lifecycle.EvaluateApproval(ctx, current)
			if err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
			moved, err := s.d.Lifecycle.ApplyApproval(ctx, current, result)
			if err != nil {
				return err //nolint:wrapcheck // the owner names the failing operation
			}
			if moved.Version == current.Version {
				if err := s.d.Lifecycle.Refresh(ctx, moved); err != nil {
					return err //nolint:wrapcheck // the owner names the failing operation
				}
			}
		}
		return s.d.Jobs.CompleteTx(ctx, j) //nolint:wrapcheck // queue names completion
	})
}

// SpaceInUse and LostOnMove leave ownership with stacks; runs do not hold space IDs.
func (*Reconciler) SpaceInUse(context.Context, kernel.AccountID, kernel.ULID) (bool, error) {
	return false, nil
}

// LostOnMove permits the owner tree change; the versioned job reads its result.
func (*Reconciler) LostOnMove(context.Context, accessapi.SpaceMove) ([]accessapi.LostReach, error) {
	return nil, nil
}

// SpaceChanging commits follow-up with the tree update. Version keys prevent an
// old in-flight scan from swallowing a newer inheritance or parent change.
func (s *Reconciler) SpaceChanging(ctx context.Context, ch accessapi.SpaceChange) error {
	return s.scan(ctx, ch.Account, kernel.ULID{}, fmt.Sprintf("space:%s:%d", ch.Space, ch.Version))
}
