package control

import (
	"context"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var _ runsapi.Supersede = (*Service)(nil)

// Supersede records the existing worker stop request for older proposals on a
// branch. Row locks recheck eligibility against concurrent worker transitions;
// a repeated notification never replaces a stop or asks for another SIGINT.
func (s *Service) Supersede(ctx context.Context, acct kernel.AccountID, stack kernel.ULID, branch, newHash string) (int, error) {
	if !tx.In(ctx) {
		return 0, runsapi.ErrNoTransaction
	}
	if acct.IsZero() || stack.IsZero() || branch == "" || newHash == "" {
		return 0, runsapi.ErrSupersedeInput
	}
	if s.d.Spaces == nil {
		return 0, fmt.Errorf("%w: run superseding has no owner holder", kernel.ErrUnavailable)
	}
	settings, err := s.d.Stacks.Settings(ctx, acct, stack)
	if err != nil {
		return 0, err //nolint:wrapcheck // the stacks domain names the read
	}
	if err := s.d.Spaces.Hold(ctx, acct, settings.Space); err != nil {
		return 0, err //nolint:wrapcheck // the access domain names the owner refusal
	}
	if err := s.d.Stacks.Hold(ctx, acct, stack); err != nil {
		return 0, err //nolint:wrapcheck // the stacks domain names the lock
	}
	runs, err := s.d.Store.PlanningProposed(ctx, acct, stack, branch, newHash)
	if err != nil {
		return 0, err //nolint:wrapcheck // the store names the read
	}
	sig := lifecycle.Signal{
		At: s.d.Now().UTC().Truncate(time.Microsecond), By: "vcs/commit",
		Note: "superseded by commit " + newHash,
	}
	for _, run := range runs {
		if err := s.d.Store.Request(ctx, run, workersapi.SignalStop, sig); err != nil {
			return 0, err //nolint:wrapcheck // the store names the signal write
		}
	}
	return len(runs), nil
}
