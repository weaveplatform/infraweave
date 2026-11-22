package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ClaimSpaces fences a candidate's owner without waiting behind another
// mutation. False means absent or locked; the caller may try another target.
type ClaimSpaces interface {
	TryHold(context.Context, kernel.AccountID, kernel.ULID) (bool, error)
}
