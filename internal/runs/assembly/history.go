package assembly

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

// HistoryAt exposes account-bound state facts to internal event consumers.
func (d *Domain) HistoryAt(ctx context.Context, acct kernel.AccountID, run kernel.ULID, sequence int64) (runsapi.RunHistory, error) {
	return d.store.HistoryAt(ctx, acct, run, sequence) //nolint:wrapcheck // the store names the read
}

var _ runsapi.RunHistories = (*Domain)(nil)
