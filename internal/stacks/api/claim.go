package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ClaimTargets lets worker assignment fence a stack before locking its run.
// A locked or absent target answers false so another candidate can proceed.
type ClaimTargets interface {
	Get(context.Context, kernel.AccountID, kernel.ULID) (Stack, error)
	TryLockForRun(context.Context, kernel.AccountID, kernel.ULID) (Settings, bool, error)
}
