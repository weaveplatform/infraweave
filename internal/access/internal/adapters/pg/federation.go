package pg

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ port.FederationSubjects = (*Store)(nil)

// FederationSubject reads, in one statement, acct's slug, its subject
// template, empty when it set none, and the slugs of space's ancestors
// from the top space down, space last, from space_closure's depths. A
// space acct lacks, or an account that does not exist, is
// port.ErrNotFound.
func (s *Store) FederationSubject(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (port.FederationSubject, error) {
	var out port.FederationSubject
	err := tx.Q(ctx, s.db).QueryRow(ctx, `
		SELECT a.slug, coalesce(st.oidc_subject_template, ''), path.slugs
		FROM accounts a
		LEFT JOIN account_settings st ON st.account_id = a.id
		CROSS JOIN LATERAL (
			SELECT array_agg(up.slug ORDER BY c.depth DESC) AS slugs
			FROM space_closure c JOIN spaces up ON up.id = c.ancestor_id
			WHERE c.account_id = a.id AND c.descendant_id = $2
		) path
		WHERE a.id = $1 AND path.slugs IS NOT NULL`, acct, space).
		Scan(&out.Account, &out.Template, &out.Spaces)
	return out, read(err, "space", space)
}
