package api

import (
	"context"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrCheckRunNotFound reports a run the account lacks.
var ErrCheckRunNotFound = errors.New("runs: check run not found")

// CheckRun contains current run facts for repository feedback. It contains no
// configuration, credentials, workspace object key, notes or user identities.
// Delta is nil until a plan is recorded; a non-nil zero delta is a known plan
// with no changes. These are current facts, not a historical delta snapshot.
type CheckRun struct {
	ID             kernel.ULID
	Stack          kernel.ULID
	Type           kernel.RunType
	State          kernel.RunState
	Sequence       int64
	CommitSHA      string
	Branch         string
	Tag            string
	Local          bool
	DriftDetection bool
	Delta          *kernel.PlanDelta
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CheckFacts supplies current facts, leaving feedback eligibility and provider
// behavior to the consuming domain. Reads join the transaction carried by ctx.
// HistoryAt remains the reader for facts through an exact event sequence.
type CheckFacts interface {
	// Run returns one account's current run facts. Missing and foreign-account
	// identities return ErrCheckRunNotFound.
	Run(ctx context.Context, account kernel.AccountID, run kernel.ULID) (CheckRun, error)
	// OnCommit reads every matching run of the account's supplied stacks on
	// sha, regardless of type, state or trigger, with no hidden limit. Results
	// are newest first by creation time, then ID descending. Duplicate stack
	// IDs do not repeat runs; an empty stack set returns no runs.
	OnCommit(ctx context.Context, account kernel.AccountID, stacks []kernel.ULID, sha string) ([]CheckRun, error)
}
