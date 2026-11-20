package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

var _ runsapi.PushCancellations = (*Service)(nil)

// CancelForPush rechecks each target under its run lock. Its caller creates a
// replacement under the same stack lock, so errors roll back replacement,
// cancellations, receipts and queue completion together.
func (s *Service) CancelForPush(ctx context.Context, account kernel.AccountID, stack, replacement kernel.ULID, targets []kernel.ULID) (int, error) {
	if !tx.In(ctx) {
		return 0, runsapi.ErrNoTransaction
	}
	if account.IsZero() || stack.IsZero() || replacement.IsZero() {
		return 0, runsapi.ErrPushCancellationInput
	}
	if s.d.Spaces == nil {
		return 0, fmt.Errorf("%w: push cancellation owner holder", kernel.ErrUnavailable)
	}
	settings, err := s.d.Stacks.Settings(ctx, account, stack)
	if err != nil {
		return 0, err //nolint:wrapcheck // the stack owner names the read
	}
	if err := s.d.Spaces.Hold(ctx, account, settings.Space); err != nil {
		return 0, err //nolint:wrapcheck // the space owner names the hold
	}
	if _, err := s.d.Stacks.LockForRun(ctx, account, stack); err != nil {
		return 0, err //nolint:wrapcheck // the stack owner names the creation fence
	}
	newRun, err := s.d.Store.Lock(ctx, account, replacement)
	if errors.Is(err, lifecycle.ErrNotFound) {
		return 0, runsapi.ErrPushCancellationInput
	}
	if err != nil {
		return 0, err //nolint:wrapcheck // the run owner names the replacement read
	}
	if newRun.Stack != stack || newRun.Type != kernel.RunTypeTracked && newRun.Type != kernel.RunTypeProposed {
		return 0, runsapi.ErrPushCancellationInput
	}
	ids := slices.Clone(targets)
	slices.SortFunc(ids, func(a, b kernel.ULID) int { return bytes.Compare(a[:], b[:]) })
	ids = slices.Compact(ids)
	changed := 0
	for _, id := range ids {
		if id.IsZero() || id == replacement {
			continue
		}
		run, err := s.d.Store.Lock(ctx, account, id)
		if errors.Is(err, lifecycle.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, err //nolint:wrapcheck // storage failures are not best-effort omissions
		}
		if run.Stack != stack || run.Type != newRun.Type {
			continue
		}
		to := kernel.RunStateCanceled
		switch run.State {
		case kernel.RunStateQueued, kernel.RunStateReady:
		case kernel.RunStateUnconfirmed:
			if run.Type != kernel.RunTypeTracked {
				continue
			}
			to = kernel.RunStateDiscarded
		default:
			continue
		}
		if _, err := s.d.Lifecycle.Transition(ctx, lifecycle.Request{
			Account: account, Run: run.ID, Version: run.Version, To: to, Driver: machine.DriverScheduler,
			Note: "canceled by push policy in favor of run " + replacement.String(),
		}); err != nil {
			return 0, err //nolint:wrapcheck // the lifecycle owns state and consequence fences
		}
		changed++
	}
	return changed, nil
}
