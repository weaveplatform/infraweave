package trigger

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

var _ runsapi.Creator = (*Service)(nil)

// Create is a trusted domain trigger, joining its caller's fenced transaction.
// It preserves the source branch and requires an explicit deduplication mode;
// the human Trigger operation keeps its existing authorization and reuse rules.
func (s *Service) Create(ctx context.Context, in runsapi.CreateRequest) (runsapi.CreatedRun, error) {
	if !tx.In(ctx) {
		return runsapi.CreatedRun{}, runsapi.ErrNoTransaction
	}
	if in.Account.IsZero() || in.Stack.IsZero() || in.Commit.Hash == "" || in.TriggeredBy == "" ||
		(in.Type != kernel.RunTypeTracked && in.Type != kernel.RunTypeProposed) ||
		(in.Mode != runsapi.None && in.Mode != runsapi.OncePerCommit) {
		return runsapi.CreatedRun{}, runsapi.ErrCreationInput
	}
	if s.d.Spaces == nil {
		return runsapi.CreatedRun{}, fmt.Errorf("%w: run creation has no owner holder", kernel.ErrUnavailable)
	}
	settings, err := s.d.Stacks.Settings(ctx, in.Account, in.Stack)
	if err != nil {
		return runsapi.CreatedRun{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	if err := s.d.Spaces.Hold(ctx, in.Account, settings.Space); err != nil {
		return runsapi.CreatedRun{}, err //nolint:wrapcheck // the access domain names the owner refusal
	}
	run, created, err := s.d.Lifecycle.Create(ctx, lifecycle.NewRun{
		Account: in.Account, Stack: in.Stack, Type: in.Type, Commit: stacksapi.Commit(in.Commit),
		Actor:        lifecycle.Actor{Login: in.TriggeredBy},
		AlwaysCreate: in.Mode == runsapi.None, OncePerCommit: in.Mode == runsapi.OncePerCommit,
		BeforeCreate: in.BeforeCreate,
	})
	if errors.Is(err, lifecycle.ErrDisabled) || errors.Is(err, lifecycle.ErrDeleting) || errors.Is(err, lifecycle.ErrLocked) {
		return runsapi.CreatedRun{}, fmt.Errorf("%w: %w", runsapi.ErrCreationRefused, err)
	}
	if err != nil {
		return runsapi.CreatedRun{}, err //nolint:wrapcheck // the lifecycle or admission check names the refusal
	}
	return runsapi.CreatedRun{ID: run.ID, Created: created}, nil
}
