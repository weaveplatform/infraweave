package source

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Native facts are already resolved. The metadata hold keeps canonicalization
// and pointer initialization on one effective binding, including default swaps.
func (s *Service) trackRepository(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	st stacksapi.Settings,
	repo port.Repository,
	resolved port.Resolution,
	at time.Time,
) error {
	bindings, ok := s.integrations.(interface {
		HeldEventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error)
	})
	if !ok || s.db == nil {
		return fmt.Errorf("%w: vcs repository binding unavailable", kernel.ErrUnavailable)
	}
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // the callback names each failed boundary
		use, found, err := bindings.HeldEventUse(ctx, acct, repo.Integration.ID)
		if err != nil || !found {
			return err //nolint:wrapcheck // integration names the held metadata read
		}
		if use.Provider != repo.Integration.Provider {
			return fmt.Errorf("%w: vcs repository provider changed", kernel.ErrUnavailable)
		}
		if st.VCSIntegration == nil && !use.Default {
			return nil
		}
		if err := s.identities.SeedReferenceAt(ctx, acct, repo.Integration.ID, use,
			stacksapi.VCSRepository{Namespace: repo.Namespace, Name: repo.Name}, resolved.Repository, at); err != nil {
			return err //nolint:wrapcheck // lifecycle names the source association
		}
		head := stackCommit(resolved.Commit)
		return s.stacks.SetTracked(ctx, acct, stack, &head, &head, "infraweave") //nolint:wrapcheck // stacks names the pointer write
	})
}
