package integration

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// EventUse is the integration's current effective binding for trusted jobs.
// It reads no credentials and treats a deleted integration as having no users.
func (s *Service) EventUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	return s.eventUse(ctx, acct, id, false)
}

// HeldEventUse reads effective binding metadata under the integration row lock,
// in the caller's completion transaction, without opening any credential.
func (s *Service) HeldEventUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	if !tx.In(ctx) {
		return stacksapi.Use{}, false, queue.ErrNoTransaction
	}
	return s.eventUse(ctx, acct, id, true)
}

func (s *Service) eventUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID, held bool) (stacksapi.Use, bool, error) {
	in, err := s.store.Get(ctx, acct, id, held)
	if errors.Is(err, ErrNotFound) {
		return stacksapi.Use{}, false, nil
	}
	if err != nil {
		return stacksapi.Use{}, false, err //nolint:wrapcheck // the store names the metadata read
	}
	return stackUse(in), true, nil
}
