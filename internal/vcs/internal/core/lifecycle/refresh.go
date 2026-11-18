package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Refresh applies an authoritative App installation list. Newer webhook facts
// win over a read begun earlier, including a missing installation's deletion.
// Historical installation IDs never poison the current owner's availability.
func (s *Service) Refresh(ctx context.Context, acct kernel.AccountID, id kernel.ULID, started time.Time, list []port.InstallationState) (string, error) {
	var reason string
	err := tx.Do(ctx, s.DB, tx.Options{}, func(ctx context.Context) error {
		reason = ""
		found, err := s.Store.HoldRepositoryIntegration(ctx, acct, id)
		if err != nil || !found {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		} //nolint:wrapcheck // store names the read
		old, err := s.Store.Installations(ctx, acct, id)
		if err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		} //nolint:wrapcheck // store names the read
		present := make(map[string]bool, len(list))
		protected := map[string]bool{}
		for _, in := range list {
			present[in.ID] = true
		}
		for _, previous := range old {
			if previous.Current && previous.ObservedAt.After(started) {
				protected[previous.Owner.ID] = true
			}
		}
		for _, previous := range old {
			if !previous.Current || present[previous.ID] || protected[previous.Owner.ID] {
				continue
			}
			previous.State, previous.ObservedAt = "deleted", started
			if err := s.Store.PutInstallation(ctx, acct, id, previous); err != nil {
				return err //nolint:wrapcheck // the called boundary names the failed operation
			} //nolint:wrapcheck // store names the write
		}
		for _, in := range list {
			if protected[in.Owner.ID] {
				continue
			}
			if err := s.Store.PutInstallation(ctx, acct, id, Installation{ID: in.ID, Owner: in.Owner, State: in.State, Current: true, ObservedAt: started}); err != nil {
				return err //nolint:wrapcheck // the called boundary names the failed operation
			} //nolint:wrapcheck // store names the write
		}
		current, err := s.Store.Installations(ctx, acct, id)
		if err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		} //nolint:wrapcheck // store names the read
		for _, in := range current {
			if in.Current && in.State != "active" {
				reason = fmt.Sprintf("The GitHub App installation on %s is %s.", in.Owner.Login, in.State)
				break
			}
		}
		return nil
	})
	return reason, err //nolint:wrapcheck // callback names the failure
}
