package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ accessapi.LoginPolicyChanges = (*Store)(nil)

// HoldAccount orders policy mutations with account entry and scheme changes.
// Its caller's transaction retains the fence through the policy write/event.
func (s *Store) HoldAccount(ctx context.Context, account kernel.AccountID) error {
	_, err := lockAccount(ctx, tx.Q(ctx, s.db), account)
	return err
}

// LoginPolicyChanged preserves inactive configuration without ending sessions.
// An active change revokes peers and invalidates grants in the same transaction
// as the policy's accepted version; the exact editor retains existing grants.
func (s *Store) LoginPolicyChanged(ctx context.Context, p kernel.Principal) error {
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		q := tx.Q(ctx, s.db)
		scheme, err := lockAccount(ctx, q, p.AccountID)
		if err != nil {
			return err
		}
		if scheme != accessapi.LoginPolicies {
			return nil
		}
		ss, err := scanSession(q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions
			WHERE account_id=$1 AND id=$2 FOR UPDATE`, p.AccountID, p.SessionID))
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, port.ErrNotFound) {
			return accessapi.ErrLoginPolicyEditor
		}
		if err != nil {
			return read(err, "LOGIN policy editor", p.SessionID)
		}
		if !ss.Live(time.Now()) || ss.OrganizationID != p.OrganizationID || ss.ActorKind != p.Kind ||
			ss.ActorID != p.ActorID || ss.MemberID != p.MemberID {
			return accessapi.ErrLoginPolicyEditor
		}
		data, err := s.AuthzData(ctx, p.AccountID)
		if err != nil {
			return err
		}
		if !authz.Build(data).Authorize(authz.WhoOf(p), accessapi.ActionSpaceAdmin, p.AccountID.ULID) {
			return accessapi.ErrLoginPolicyEditor
		}
		peers, err := revoke(ctx, q, `account_id=$1 AND id<>$2`, p.AccountID, p.SessionID)
		if err != nil {
			return err
		}
		if err := s.bumpAuthz(ctx, q, p.AccountID); err != nil {
			return err
		}
		if s.dropSession != nil {
			for _, peer := range peers {
				tx.AfterCommit(ctx, func(context.Context) { s.dropSession(peer.ID) })
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("invalidate sessions after LOGIN policy change: %w", sql.Unavailable(err))
	}
	return nil
}
