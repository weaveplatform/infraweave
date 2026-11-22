package pg

import (
	"context"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// lockAccount orders admission and authorization writers before their child
// row locks. Admissions install grants and bump this same row's version, so a
// shared lock would deadlock two admissions when both upgrade it at commit.
func lockAccount(ctx context.Context, q tx.Querier, acct kernel.AccountID) (accessapi.AuthorizationScheme, error) {
	var scheme accessapi.AuthorizationScheme
	err := q.QueryRow(ctx, `SELECT authorization_scheme FROM accounts
		WHERE id = $1 FOR NO KEY UPDATE`, acct).Scan(&scheme)
	return scheme, read(err, "account", acct.ULID)
}
