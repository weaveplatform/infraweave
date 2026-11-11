package http

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// RunStack answers the stack a run of the caller's account belongs to,
// which the runs domain names through the bound stacksapi.StackStatus.
// The route has decided SPACE_READ on the stack's space, and answered 404
// for a run the caller cannot see.
func (s Server) RunStack(
	ctx context.Context,
	req api.RunStackRequestObject,
) (api.RunStackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id, err := kernel.ParseULID(req.RunId)
	if err != nil {
		return nil, noRun(req.RunId)
	}
	stack, err := s.edges.status.RunStack(ctx, p.AccountID, id)
	if err != nil {
		return nil, runStackError(err, req.RunId)
	}
	ref, err := s.stacks.Get(ctx, p.AccountID, stack)
	if err != nil {
		return nil, runStackError(err, req.RunId)
	}
	st, err := s.stacks.Stack(ctx, p.AccountID, ref.Slug)
	if err != nil {
		return nil, runStackError(err, req.RunId)
	}
	v, err := s.view(ctx, p, st)
	if err != nil {
		return nil, err
	}
	return api.RunStack200JSONResponse{Body: v}, nil
}

// runStackError answers a run or a stack that is gone as a run that does
// not exist, and any other failure as it is.
func runStackError(err error, run string) error {
	if errors.Is(err, stacksapi.ErrNotFound) {
		return noRun(run)
	}
	return err //nolint:wrapcheck // the runs and stacks domains name the reads
}

func noRun(run string) error {
	//nolint:wrapcheck // a problem, answered as it is
	return problem.NotFound.Error(fmt.Sprintf("No run %q in this account.", run))
}
