package evidence

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// PlanOutcome is the message projection of the PLAN receipts committed
// by one planning report. ID is its first immutable receipt's correlation ID.
// Current is false once a later PLANNING version has started, including while
// its new report has not yet committed any decision.
type PlanOutcome struct {
	ID           kernel.ULID
	StateVersion int64
	CreatedAt    time.Time
	Deny, Warn   []string
	Info         []string
	Current      bool
}

// PlanStore returns only message arrays and correlation metadata. Its adapter
// never selects the raw decision, source, evaluation input or print output.
type PlanStore interface {
	PlanOutcomes(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]PlanOutcome, error)
}
