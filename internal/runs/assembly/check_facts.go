package assembly

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

// CheckFacts supplies current minimal run facts; the consumer owns repository
// feedback eligibility and uses HistoryAt separately for historical events.
func (d *Domain) CheckFacts() runsapi.CheckFacts { return d }

// Run reads one account's current feedback facts in the caller's transaction.
func (d *Domain) Run(ctx context.Context, account kernel.AccountID, run kernel.ULID) (runsapi.CheckRun, error) {
	return d.store.CheckRun(ctx, account, run) //nolint:wrapcheck // the store names the read
}

// OnCommit reads all current facts for the account's supplied stacks and SHA.
func (d *Domain) OnCommit(ctx context.Context, account kernel.AccountID, stacks []kernel.ULID, sha string) ([]runsapi.CheckRun, error) {
	return d.store.CheckRunsOnCommit(ctx, account, stacks, sha) //nolint:wrapcheck // the store names the read
}

var _ runsapi.CheckFacts = (*Domain)(nil)
