package pg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Approve records original authentication facts with the code, under the same
// account fence as policy changes. The browser's grants are never transferred.
// A member that only the account's managed user groups admit gains its
// admission in the same transaction, refused with a *featuresapi.LimitError
// when its seat does not fit.
func (s *Store) Approve(ctx context.Context, p kernel.Principal, d oauth.Request, now time.Time) error {
	var denied *session.LoginDenied
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		denied = nil
		q := tx.Q(ctx, s.db)
		scheme, err := lockAccount(ctx, q, d.AccountID)
		if errors.Is(err, port.ErrNotFound) {
			return oauth.ErrNotAdmitted
		}
		if err != nil {
			return err
		}
		candidate := session.Session{
			ID: p.SessionID, OrganizationID: p.OrganizationID, AccountID: d.AccountID,
			ActorKind: p.Kind, ActorID: p.ActorID, MemberID: p.MemberID,
			CreatedAt: now, ExpiresAt: now, IPAddress: accessapi.ClientAddr(ctx),
		}
		admitted, err := entryMember(ctx, q, &candidate, scheme)
		if errors.Is(err, session.ErrNotAdmitted) {
			return oauth.ErrNotAdmitted
		}
		if err != nil {
			return err
		}
		if err := entrySource(ctx, q, &candidate, p.SessionID); err != nil {
			if errors.Is(err, port.ErrCredentialChanged) {
				return oauth.ErrNotAdmitted
			}
			return err
		}
		// The code's redemption reads the member's admission, so a member
		// that only the account's groups admit gains it here, as an entry
		// would.
		if !admitted {
			err := s.admitThroughGroups(ctx, q, candidate, true)
			if errors.Is(err, session.ErrNotAdmitted) {
				return oauth.ErrNotAdmitted
			}
			if err != nil {
				return err
			}
		}
		r, err := scanRequest(q.QueryRow(ctx, `SELECT `+requestColumns+requestFrom+`
			WHERE r.id=$1 FOR UPDATE OF r`, d.ID))
		if err != nil {
			return read(err, "OAuth consent request", d.ID)
		}
		if r.Decided || !now.Before(r.ExpiresAt) {
			return port.ErrNotFound
		}
		hosts, err := s.RedirectHosts(ctx, d.AccountID)
		if err != nil {
			return err
		}
		if !oauth.AllowedRedirect(r.RedirectURI, hosts) {
			return oauth.ErrRedirectHost
		}
		if scheme == accessapi.LoginPolicies {
			_, refused, err := s.loginEntry(ctx, &candidate, nil)
			denied = refused
			if err != nil || denied != nil {
				return err
			}
		}
		d.OrganizationID, d.MemberID = candidate.OrganizationID, candidate.MemberID
		d.OriginalMember, d.OriginalTeamsKnown = candidate.OriginalMember, candidate.OriginalTeamsKnown
		d.OriginalTeams = slices.Clone(candidate.OriginalTeams)
		d.IdentityProviderID, d.IDPSubject = candidate.IdentityProviderID, candidate.IDPSubject
		// Decide checks wall-clock expiry at the final write; transaction-start
		// now() or the request clock cannot extend consent across lock waits.
		return s.Decide(ctx, d, now)
	})
	if err != nil {
		return fmt.Errorf("approve OAuth account entry: %w", sql.Unavailable(err))
	}
	if denied != nil {
		return denied
	}
	return nil
}
