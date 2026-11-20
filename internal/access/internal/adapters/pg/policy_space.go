package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ accessapi.PolicySpaceReader = (*Store)(nil)

// PolicySpace reads labels alongside the space's current public description,
// leaving unconsumed settings outside the policy metadata projection.
func (s *Store) PolicySpace(ctx context.Context, account kernel.AccountID, id kernel.ULID) (accessapi.PolicySpace, error) {
	var out accessapi.PolicySpace
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id,slug,name,labels FROM spaces WHERE account_id=$1 AND id=$2`, account, id).
		Scan(&out.ID, &out.Slug, &out.Name, &out.Labels)
	if errors.Is(err, pgx.ErrNoRows) {
		return accessapi.PolicySpace{}, accessapi.ErrNoSpace
	}
	if err != nil {
		return accessapi.PolicySpace{}, fmt.Errorf("access: read policy space facts: %w", sql.Unavailable(err))
	}
	return out, nil
}
