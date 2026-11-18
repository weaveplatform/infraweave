package integration

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Binding resolves the account's provider binding without opening credentials.
// Feedback selection uses its ID; only its queued provider job calls Use.
func (s *Service) Binding(ctx context.Context, acct kernel.AccountID, provider string, id *kernel.ULID) (kernel.ULID, bool, error) {
	canonical := canonicalProvider(provider)
	if !slices.Contains(s.providers, canonical) {
		return kernel.ULID{}, false, nil
	}
	var in Integration
	var err error
	if id == nil {
		in, err = s.store.Default(ctx, acct, canonical)
	} else {
		in, err = s.store.Get(ctx, acct, *id, false)
	}
	if errors.Is(err, ErrNotFound) {
		return kernel.ULID{}, false, nil
	}
	if err != nil {
		return kernel.ULID{}, false, err //nolint:wrapcheck // the store names the read
	}
	if in.Provider != canonical {
		return kernel.ULID{}, false, fmt.Errorf("%w: %s %s", ErrNotFound, canonical, in.ID)
	}
	return in.ID, true, nil
}
