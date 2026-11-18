package integration

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// canonicalProvider keeps the public GITHUB alias on the account's App
// integration path, including named integrations and default lookups.
func canonicalProvider(provider string) string {
	if provider == GitHub {
		return GitHubEnterprise
	}
	return provider
}

// DefaultAvailable checks the write-time default without opening credentials.
// A stack retains its omitted integration and resolves the default again at use.
func (s *Service) DefaultAvailable(ctx context.Context, acct kernel.AccountID, provider string) (bool, error) {
	provider = canonicalProvider(provider)
	in, err := s.store.Default(ctx, acct, provider)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err //nolint:wrapcheck // the store names the lookup
	}
	return in.Provider == provider, nil
}
