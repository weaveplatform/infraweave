package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/review"
)

// RunReview records a state-scoped review under RUN_REVIEW on the stack space.
func (s Server) RunReview(ctx context.Context, req api.RunReviewRequestObject) (api.RunReviewResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, problem.ValidationFailed.Error("A review decision is required.") //nolint:wrapcheck // the owner names the failing operation
	} //nolint:wrapcheck // shared problem response
	b := req.Body
	r, err := s.control.Review(ctx, p, req.StackId, deref(b.Run), review.Decision(b.Decision), b.Note, b.StateVersion)
	if err != nil {
		return nil, runError(err, req.StackId, deref(b.Run))
	}
	return api.RunReview200JSONResponse{Body: reviewOf(r)}, nil
}
