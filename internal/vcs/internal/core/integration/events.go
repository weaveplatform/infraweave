package integration

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// EventUse is the integration's current effective binding for trusted jobs.
// It reads no credentials and treats a deleted integration as having no users.
func (s *Service) EventUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	in, err := s.store.Get(ctx, acct, id, false)
	if errors.Is(err, ErrNotFound) {
		return stacksapi.Use{}, false, nil
	}
	if err != nil {
		return stacksapi.Use{}, false, err //nolint:wrapcheck // the store names the metadata read
	}
	return stackUse(in), true, nil
}
