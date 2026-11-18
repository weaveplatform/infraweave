package pullrequest

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Pruned shares the inbox retention horizon, while open requests remain live.
func (s *Service) Pruned(ctx context.Context) error {
	before := s.d.Now().Add(-s.d.Retention)
	return s.prune(ctx, nil, &before)
}

// Purge deletes only the deleted account's requests and cascading head history.
func (s *Service) Purge(ctx context.Context, acct kernel.AccountID) error {
	return s.prune(ctx, &acct, nil)
}

func (s *Service) prune(ctx context.Context, acct *kernel.AccountID, before *time.Time) error {
	for {
		n, err := s.d.Store.Prune(ctx, acct, before, Batch)
		if err != nil {
			return err
		}
		if n < Batch {
			return nil
		}
	}
}
