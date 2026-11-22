package schedule

import (
	"context"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// OwnerFence preserves space -> stack -> run order without waiting on busy
// targets. CandidateAttempt releases a partial fence before another target.
type OwnerFence struct {
	Spaces accessapi.ClaimSpaces
	Stacks stacksapi.ClaimTargets
}

// HoldClaim validates the same stack owner before and after the fences.
func (f OwnerFence) HoldClaim(ctx context.Context, account kernel.AccountID, stack kernel.ULID) (bool, error) {
	if f.Spaces == nil || f.Stacks == nil {
		return false, kernel.ErrUnavailable
	}
	owner, err := f.Stacks.Get(ctx, account, stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("runs: read claim owner: %w", err)
	}
	held, err := f.Spaces.TryHold(ctx, account, owner.Space)
	if err != nil || !held {
		return false, err //nolint:wrapcheck // the owner names the fence
	}
	current, held, err := f.Stacks.TryLockForRun(ctx, account, stack)
	if err != nil || !held {
		return false, err //nolint:wrapcheck // the owner names the fence
	}
	return current.Space == owner.Space, nil
}
