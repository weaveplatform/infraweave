package integration

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Binding resolves the account's provider binding without opening credentials.
// Feedback selection uses its ID; only its queued provider job calls Use.
func (s *Service) Binding(ctx context.Context, acct kernel.AccountID, provider string, id *kernel.ULID) (port.Integration, bool, error) {
	canonical := canonicalProvider(provider)
	if !slices.Contains(s.providers, canonical) {
		return port.Integration{}, false, nil
	}
	var in Integration
	var err error
	if id == nil {
		in, err = s.store.Default(ctx, acct, canonical)
	} else {
		in, err = s.store.Get(ctx, acct, *id, false)
	}
	if errors.Is(err, ErrNotFound) {
		return port.Integration{}, false, nil
	}
	if err != nil {
		return port.Integration{}, false, err //nolint:wrapcheck // the store names the read
	}
	if in.Provider != canonical {
		return port.Integration{}, false, fmt.Errorf("%w: %s %s", ErrNotFound, canonical, in.ID)
	}
	return port.Integration{ID: in.ID, Provider: in.Provider, Settings: in.Settings, Checks: port.CheckPolicy{
		Mode: in.Checks.Mode, FailuresOnly: in.Checks.IndividualFailuresOnly, SkipUnchanged: in.Checks.IndividualSkipUnchanged,
	}}, true, nil
}
