package api

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// CommitRun is the identity and creation time of a run on a repository commit.
// It contains no run configuration, credentials or private workspace metadata.
type CommitRun struct {
	ID        kernel.ULID `json:"id"`
	CommitSHA string      `json:"commitSha"`
	CreatedAt time.Time   `json:"createdAt"`
}

// CommitRuns reads every run of an account's stack on the requested commit
// hashes, regardless of type, state or trigger. Results are newest first by
// creation time, then ID descending, without a limit; repeated hashes do not
// repeat runs. An empty hash set returns no runs. Reads join the transaction
// carried by ctx, including its uncommitted writes.
type CommitRuns interface {
	OnCommits(ctx context.Context, account kernel.AccountID, stack kernel.ULID, hashes []string) ([]CommitRun, error)
}
