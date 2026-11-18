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
// The returned reason names the owner/state, never an opened credential.
func (s *Service) Refresh(ctx context.Context, acct kernel.AccountID, id kernel.ULID, started time.Time, list []port.InstallationState) (string, error) {
	var reason string
	err := tx.Do(ctx, s.DB, tx.Options{}, func(ctx context.Context) error {
		reason = ""
		found, err := s.Store.HoldRepositoryIntegration(ctx, acct, id)
		if err != nil || !found {
			return err
		}
		old, err := s.Store.Installations(ctx, acct, id)
		if err != nil {
			return err
		}
		incoming := make(map[string]port.InstallationState, len(list))
		for _, in := range list {
			incoming[in.ID] = in
		}
		// Missing known installations are deleted. Unknown absence says nothing
		// about a namespace that has never been observed through this App.
		for _, previous := range old {
			if previous.ObservedAt.After(started) {
				delete(incoming, previous.ID)
				continue
			}
			if _, present := incoming[previous.ID]; !present {
				incoming[previous.ID] = port.InstallationState{ID: previous.ID, Owner: previous.Owner, State: "deleted"}
			}
		}
		for _, in := range incoming {
			if err := s.Store.PutInstallation(ctx, acct, id, Installation{ID: in.ID, Owner: in.Owner, State: in.State, ObservedAt: started}); err != nil {
				return err
			}
		}
		current, err := s.Store.Installations(ctx, acct, id)
		if err != nil {
			return err
		}
		for _, in := range current {
			if in.State != "active" {
				reason = fmt.Sprintf("The GitHub App installation on %s is %s.", in.Owner.Login, in.State)
				break
			}
		}
		return nil
	})
	return reason, err
}
