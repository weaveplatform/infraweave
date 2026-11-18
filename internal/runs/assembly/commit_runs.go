package assembly

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

// CommitRuns supplies minimal run facts for a stack's current and historical
// repository heads. The consuming domain applies its own membership and limits.
func (d *Domain) CommitRuns() runsapi.CommitRuns { return d }

// OnCommits reads account-bound run identities in the caller's transaction.
func (d *Domain) OnCommits(ctx context.Context, account kernel.AccountID, stack kernel.ULID, hashes []string) ([]runsapi.CommitRun, error) {
	return d.store.OnCommits(ctx, account, stack, hashes) //nolint:wrapcheck // the store names the read
}

var _ runsapi.CommitRuns = (*Domain)(nil)
