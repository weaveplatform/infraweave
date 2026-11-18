package intake

import (
	"context"
	"encoding/json"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

type cursor struct {
	After kernel.ULID `json:"after"`
}

const exhausted = "vcs: delivery processing failed"

// Reconciled settles jobs whose final attempt died without returning through the
// handler, such as lease recovery after a crash. Each job checks at most one
// batch; a durable cursor continuation reaches later rows even when earlier
// received deliveries remain pending. The minute task starts another sweep.
func (s *Service) Reconciled(ctx context.Context, j queue.Job) error {
	var c cursor
	if len(j.Payload) > 0 && json.Unmarshal(j.Payload, &c) != nil {
		return queue.Permanent(errPayload) //nolint:wrapcheck // marks the fixed payload refusal permanent
	}
	refs, err := s.d.Store.Pending(ctx, c.After, ReconcileBatch)
	if err != nil {
		return err //nolint:wrapcheck // the store names the bounded read
	}
	//nolint:wrapcheck // each settlement step names its boundary
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		for _, ref := range refs {
			dead, err := s.d.Jobs.DeadByKeyTx(ctx, Process, ref.ID.String(), ref.Account.ULID)
			if err != nil {
				return err //nolint:wrapcheck // the queue names the terminal lookup
			}
			if dead {
				if err := s.d.Store.State(ctx, ref.Account, ref.ID, Failed, exhausted); err != nil {
					return err //nolint:wrapcheck // the store names the metadata write
				}
			}
		}
		if len(refs) == ReconcileBatch {
			after := refs[len(refs)-1].ID
			if _, err := s.d.Jobs.EnqueueTx(ctx, Reconcile, cursor{After: after}, queue.UniqueKey("after:"+after.String())); err != nil {
				return err //nolint:wrapcheck // the queue names the continuation
			}
		}
		return s.d.Jobs.CompleteTx(ctx, j)
	})
}
