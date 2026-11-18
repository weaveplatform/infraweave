package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
)

// ErrFeedbackTarget reports an invalid feedback job or an identity that
// does not belong to the event being fanned out.
var ErrFeedbackTarget = errors.New("invalid VCS feedback target")

// FeedbackTarget is one stable destination and its VCS-owned job. Payload
// contains identifiers, never credentials. Account and EventID retain the
// event identity so enqueueing cannot cross an account boundary.
type FeedbackTarget struct {
	Account kernel.ULID
	EventID kernel.ULID
	Key     string
	Job     string
	Payload json.RawMessage
}

// Feedback is the Commit checks sink's VCS-owned target selection and
// enqueue path. Enqueue joins the active transaction carried by ctx and
// refuses a call without one. It holds the account owner through that
// transaction, so account deletion and delivery insertion serialize, and
// performs no provider request inline.
type Feedback interface {
	Kinds() []string
	Targets(ctx context.Context, event outbox.Event) ([]FeedbackTarget, error)
	Enqueue(ctx context.Context, target FeedbackTarget) error
}
