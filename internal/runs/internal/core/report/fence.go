package report

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

func (s *Service) fence(ctx context.Context, id kernel.ULID) error {
	if !tx.In(ctx) {
		return lifecycle.ErrNoTransaction
	}
	if s.d.Spaces == nil {
		return nil
	}
	owner, err := s.d.Store.ReportOwner(ctx, id)
	if errors.Is(err, lifecycle.ErrNotFound) {
		return fmt.Errorf("%w: %s", workersapi.ErrNotOnWorker, id)
	}
	if err != nil {
		return fmt.Errorf("runs: locate report owner: %w", err)
	}
	st, err := s.d.Stacks.Get(ctx, owner.Account, owner.Stack)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return fmt.Errorf("%w: %s", workersapi.ErrNotOnWorker, id)
	}
	if err != nil {
		return fmt.Errorf("runs: read report stack: %w", err)
	}
	if err := s.d.Spaces.Hold(ctx, owner.Account, st.Space); err != nil {
		return fmt.Errorf("runs: hold report space: %w", err)
	}
	locked, err := s.d.Stacks.LockForRun(ctx, owner.Account, owner.Stack)
	if err != nil {
		return fmt.Errorf("runs: fence report stack: %w", err)
	}
	if locked.Space != st.Space {
		return fmt.Errorf("runs: report stack changed space: %w", kernel.ErrUnavailable)
	}
	return nil
}
