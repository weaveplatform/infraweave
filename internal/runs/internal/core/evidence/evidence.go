// Package evidence defines the runs-owned persistence of policy evidence. The
// policy domain supplies independent decisions; the run's transaction records
// them and applies each policy's own flag resets before its additions.
package evidence

import (
	"context"
	"errors"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policiesapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

// ErrReceipt identifies evidence that cannot describe a committed evaluation.
var ErrReceipt = errors.New("runs: invalid policy receipt")

// Receipt is readable metadata. Raw decisions stay in storage; input, policy
// source and print output never enter a run projection or notification event.
type Receipt struct {
	ID, Policy                  kernel.ULID
	PolicyName                  string
	PolicyVersion, StateVersion int64
	Type                        policiesapi.Type
	Engine                      string
	Outcome                     policiesapi.Outcome
	Flags, ResetFlags           []string
	SampleRequested             bool
	SampleEvaluation            *kernel.ULID
	Failure                     *policiesapi.Failure
	CreatedAt                   time.Time
}

// Snapshot is the committed evidence and the union of its current flag owners.
type Snapshot struct {
	Receipts []Receipt
	Flags    []string
}

// Empty is a known absence of evidence, expressed as arrays rather than null.
func Empty() Snapshot { return Snapshot{Receipts: []Receipt{}, Flags: []string{}} }

// Store joins the caller's transaction for writes and scopes every read by its
// owning account. StateVersion is the transition under which evidence commits.
type Store interface {
	RecordPolicyEvidence(context.Context, kernel.AccountID, kernel.ULID, int64, []policiesapi.Receipt) error
	PolicyEvidence(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]Snapshot, error)
}
