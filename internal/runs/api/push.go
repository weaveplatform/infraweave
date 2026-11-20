package api

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ErrPushCancellationInput refuses an incomplete or foreign replacement.
var ErrPushCancellationInput = errors.New("runs: invalid push cancellation request")

// PushCancellations applies best-effort push-policy cancellation after a caller
// creates a new replacement. It joins that creation and queue-completion
// transaction. A deduplicated creation must never call this port.
type PushCancellations interface {
	CancelForPush(context.Context, kernel.AccountID, kernel.ULID, kernel.ULID, []kernel.ULID) (int, error)
}
