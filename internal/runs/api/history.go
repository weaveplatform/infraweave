package api

import (
	"context"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrHistoryNotFound is a run or transition sequence the account lacks.
var ErrHistoryNotFound = errors.New("runs: run history not found")

// RunHistories reads historical state facts for account-bound event consumers.
// It supplies no policy or deployment eligibility decision.
type RunHistories interface {
	// HistoryAt returns the run's identity and transitions through sequence,
	// oldest first. Sequence is the run's state_version, as carried by a
	// run_updated event. The exact sequence must exist; an absent run, a
	// foreign account or a sequence not yet written is ErrHistoryNotFound.
	HistoryAt(ctx context.Context, account kernel.AccountID, run kernel.ULID, sequence int64) (RunHistory, error)
}

// RunHistory contains the run's identity and a bounded transition history.
// Local reports a local workspace without revealing its object key. No variables,
// runtime configuration, credentials, notes or usernames enter this projection.
type RunHistory struct {
	ID          kernel.ULID     `json:"id"`
	Stack       kernel.ULID     `json:"stack"`
	Type        kernel.RunType  `json:"type"`
	CommitHash  string          `json:"commitHash"`
	Branch      string          `json:"branch"`
	Tag         string          `json:"tag"`
	Local       bool            `json:"local"`
	CreatedAt   time.Time       `json:"createdAt"`
	Through     int64           `json:"through"`
	Transitions []RunTransition `json:"transitions"`
}

// RunTransition is one state fact at the run's state_version and time.
type RunTransition struct {
	Sequence int64           `json:"sequence"`
	State    kernel.RunState `json:"state"`
	At       time.Time       `json:"at"`
}
