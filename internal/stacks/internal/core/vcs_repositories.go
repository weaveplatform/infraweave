package core

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// RepointVCSRepository joins a provider event's completing transaction. Source
// resolution follows the canonical Changed event; rename policy belongs to VCS.
func (s *Service) RepointVCSRepository(ctx context.Context, acct kernel.AccountID, u stacksapi.Use, from, to stacksapi.VCSRepository) error {
	if !tx.In(ctx) {
		return stacksapi.ErrNoTransaction
	}
	if from == to {
		return nil
	}
	changed, err := s.store.RepointVCSRepository(ctx, acct, u, from, to)
	if err != nil {
		return err //nolint:wrapcheck // the store names the write
	}
	for _, stack := range changed {
		if err := s.changed(ctx, stack, stacksapi.ChangeUpdated, true); err != nil {
			return err
		}
	}
	return nil
}
