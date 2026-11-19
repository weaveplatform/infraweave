package feedback

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func (*Service) LostOnMove(context.Context, stacksapi.StackMove) ([]string, error) { return nil, nil }

// Deleting removes stack-owned requests while the stacks domain holds its
// owner lock. Native resource cursors stay historical to fence older runs.
func (s *Service) Deleting(ctx context.Context, stack stacksapi.Stack) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	if s.checks == nil {
		return nil
	}
	return s.checks.Store.DeleteStackChecks(ctx, stack.Account, stack.ID) //nolint:wrapcheck // store names the delete
}

// PurgeChecks removes account metadata after its live owner fence is closed.
func (s *Service) PurgeChecks(ctx context.Context, account kernel.AccountID, batch int) (bool, error) {
	if s.checks == nil {
		return true, nil
	}
	return s.checks.Store.PurgeChecks(ctx, account, batch) //nolint:wrapcheck // store names the purge
}
