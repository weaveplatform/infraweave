package core

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

var _ stacksapi.PolicySnapshots = (*Service)(nil)

// PolicySnapshot reads the persisted settings, labels and projected run state
// from the stack's owned record, joining the caller's transaction.
func (s *Service) PolicySnapshot(ctx context.Context, account kernel.AccountID, id kernel.ULID) (stacksapi.PolicySnapshot, error) {
	stack, err := s.store.Read(ctx, account, id, false)
	if err != nil {
		return stacksapi.PolicySnapshot{}, err //nolint:wrapcheck // the owned store names the read
	}
	return stacksapi.PolicySnapshot{Settings: stack.Settings, State: stack.State, Labels: append([]string{}, stack.Labels...)}, nil
}
