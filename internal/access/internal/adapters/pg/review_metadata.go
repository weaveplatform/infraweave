package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// ReviewSubject reads the optional subject recorded on this exact session.
func (s *Store) ReviewSubject(ctx context.Context, id kernel.ULID) (*string, error) {
	var subject *string
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT idp_subject FROM sessions WHERE id=$1`, id).Scan(&subject)
	return subject, read(err, "review session", id)
}

// ReviewIdentity verifies admission and reads only this member's actual linked
// provider subjects. A key ID never stands in for a person's identity.
func (s *Store) ReviewIdentity(ctx context.Context, p kernel.Principal) (accessapi.ReviewIdentity, error) {
	var out accessapi.ReviewIdentity
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT m.id
  FROM account_users u JOIN members m ON m.id=u.member_id
  JOIN accounts a ON a.id=u.account_id
  WHERE u.account_id=$1 AND m.organization_id=$2 AND a.organization_id=$2
  AND m.id=$3 AND m.status='active'`, p.AccountID, p.OrganizationID, p.MemberID).Scan(&out.Member)
	if err != nil {
		return out, read(err, "review member", p.MemberID.ULID)
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT identity_provider_id,subject FROM member_identities
  WHERE organization_id=$1 AND member_id=$2 ORDER BY identity_provider_id,subject`, p.OrganizationID, out.Member)
	if err != nil {
		return out, fmt.Errorf("access: read review federations: %w", sql.Unavailable(err))
	}
	out.Federations, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (accessapi.ReviewFederation, error) {
		var f accessapi.ReviewFederation
		return f, r.Scan(&f.Provider, &f.Subject)
	})
	if err != nil {
		return out, fmt.Errorf("access: scan review federations: %w", sql.Unavailable(err))
	}
	return out, nil
}
