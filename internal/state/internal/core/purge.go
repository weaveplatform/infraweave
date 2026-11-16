package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// PurgeKind is the job that purges a deleted stack's state, which its
// delete enqueues. Its unique key is the stack, so a stack's purge is
// enqueued once. It is housekeeping that no caller waits on, so it runs
// on the maintenance queue.
var PurgeKind = queue.Kind{Name: "state.purge", Queue: queue.Maintenance, Policy: queue.Ignore}

// PurgeBatch is how many rows one statement of Purge deletes, so no
// transaction holds a stack's or an account's every version at once.
const PurgeBatch = 500

// purge is a PurgeKind job's payload.
type purge struct {
	Account kernel.ULID `json:"account"`
	Stack   kernel.ULID `json:"stack"`
}

// LostOnMove answers nothing: a stack's state moves with it.
func (s *Service) LostOnMove(context.Context, stacksapi.StackMove) ([]string, error) {
	return nil, nil
}

// Deleting enqueues PurgeKind for st in the delete's transaction, so the
// purge runs once the delete commits and never when it rolls back. A
// write under the stack's hold commits before the delete locks the stack,
// and none can hold it after, so the purge finds every row.
//
// ref: decision 0010 § "Rollback and retention"
func (s *Service) Deleting(ctx context.Context, st stacksapi.Stack) error {
	_, err := s.jobs.EnqueueTx(ctx, PurgeKind, purge{Account: st.Account.ULID, Stack: st.ID},
		queue.UniqueKey(st.ID.String()), queue.Account(st.Account.ULID))
	return err //nolint:wrapcheck // EnqueueTx names the kind
}

// Purged is PurgeKind's handler: it purges the job's stack. A payload
// that does not decode never will, so its job dies at once.
func (s *Service) Purged(ctx context.Context, j queue.Job) error {
	var p purge
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		//nolint:wrapcheck // marks it
		return queue.Permanent(fmt.Errorf("state: %s: %w", PurgeKind.Name, err))
	}
	return s.Purge(ctx, kernel.AccountID{ULID: p.Account}, &p.Stack)
}

// Purge deletes acct's state, of stack alone when it is set, PurgeBatch
// rows a statement, each in a transaction of its own that hands each
// deleted version's object to Objects, so the object goes once the row
// that names it has. A purge stopped part way finishes when it runs
// again.
func (s *Service) Purge(ctx context.Context, acct kernel.AccountID, stack *kernel.ULID) error {
	for {
		var done bool
		err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
			keys, last, err := s.store.Purge(ctx, acct, stack, PurgeBatch)
			if err != nil {
				return err //nolint:wrapcheck // the store names the purge
			}
			done = last
			for _, k := range keys {
				if err := s.objects.Delete(ctx, objectstore.States, k); err != nil {
					return err //nolint:wrapcheck // blobs names the object
				}
			}
			return nil
		})
		if err != nil || done {
			return err //nolint:wrapcheck // each step in the transaction names itself
		}
	}
}
