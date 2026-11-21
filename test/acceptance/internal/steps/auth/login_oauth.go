package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func (s *oauthSteps) loginGrant(ctx context.Context) error {
	hash := sha256.Sum256([]byte(s.refresh))
	var id, account, grants string
	var original *bool
	var known, admin, independent bool
	var upstream, effective []string
	var admissions int
	if err := s.w.Row(ctx, `SELECT s.id::text,s.account_id::text,s.original_member,
		s.original_teams_known,s.original_teams,s.teams,s.admin,
		s.parent_session_id IS NULL AND s.expires_at>s.created_at+interval '10 hours',
		(SELECT count(*) FROM account_users u WHERE u.account_id=s.account_id AND u.member_id=s.member_id),
		coalesce((SELECT string_agg(g.origin||':'||sp.slug||':'||r.slug,',' ORDER BY sp.slug,r.slug,g.origin)
		FROM session_grants g JOIN spaces sp ON sp.account_id=g.account_id AND sp.id=g.space_id
		JOIN roles r ON r.account_id=g.account_id AND r.id=g.role_id
		WHERE g.account_id=s.account_id AND g.session_id=s.id),'')
		FROM sessions s WHERE s.secret_hash=$1 AND s.oauth_client_id=$2 AND s.revoked_at IS NULL`,
		[]any{hash[:], s.client}, &id, &account, &original, &known, &upstream, &effective,
		&admin, &independent, &admissions, &grants); err != nil {
		return err
	}
	if account != harness.UUIDOf(harness.Home.Account) || original == nil || *original || !known ||
		!slices.Equal(upstream, []string{"upstream"}) || !slices.Equal(effective, []string{"effective"}) ||
		admin || !independent || admissions != 0 || grants != "ASSIGNED_ROLE:legacy:space-reader" {
		return fmt.Errorf("%w: OAuth LOGIN facts/grants differ: account=%s original=%v known=%t upstream=%v teams=%v admin=%t independent=%t admissions=%d grants=%s",
			harness.ErrStep, account, original, known, upstream, effective, admin, independent, admissions, grants)
	}
	if prior := s.w.IDs["oauth-login-session"]; prior != "" && prior != id {
		return fmt.Errorf("%w: refresh replaced the OAuth session", harness.ErrStep)
	}
	s.w.IDs["oauth-login-session"] = id
	return nil
}

func (s *oauthSteps) unspentLoginCode(ctx context.Context) error {
	var unspent bool
	var grants int
	if err := s.w.Row(ctx, `SELECT r.redeemed_at IS NULL AND r.session_id IS NULL,
		(SELECT count(*) FROM sessions s WHERE s.oauth_client_id=r.client_id::text)
		FROM oauth_requests r WHERE r.id=$1`, []any{harness.UUIDOf(s.request)}, &unspent, &grants); err != nil {
		return err
	}
	if !unspent || grants != 0 {
		return fmt.Errorf("%w: refused OAuth redemption consumed code or created %d grants", harness.ErrStep, grants)
	}
	return nil
}
