package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// PolicyInputCaptures lists safe current-head summaries without opening inputs.
func (s Server) PolicyInputCaptures(ctx context.Context, req api.PolicyInputCapturesRequestObject) (api.PolicyInputCapturesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := search.Input{First: req.Params.Limit}
	if req.Params.Cursor != nil {
		in.After = *req.Params.Cursor
	}
	res, err := s.svc.Captures(ctx, p, req.Params.Space, string(req.Params.Type), in)
	if err != nil {
		return nil, policyError(err)
	}
	out := api.PolicyInputCaptureSummaryPage{Items: []api.PolicyInputCaptureSummary{}}
	for _, edge := range res.Edges {
		out.Items = append(
			out.Items,
			api.PolicyInputCaptureSummary{Id: edge.Node.ID.String(), Kind: api.PolicyInputCaptureKind(edge.Node.Kind), CapturedAt: unix(edge.Node.At.Unix())},
		)
	}
	if res.HasNext {
		out.NextCursor = &res.End
	}
	return api.PolicyInputCaptures200JSONResponse{Body: out}, nil
}

// PolicyInputCapture opens only an owned, current capture for a root admin.
func (s Server) PolicyInputCapture(ctx context.Context, req api.PolicyInputCaptureRequestObject) (api.PolicyInputCaptureResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.ReadCapture(ctx, p, req.PolicyInputCaptureId)
	if err != nil {
		return nil, policyError(err)
	}
	return api.PolicyInputCapture200JSONResponse{
		Body: api.PolicyInputCapture{Id: out.ID.String(), Kind: api.PolicyInputCaptureKind(out.Kind), CapturedAt: unix(out.At.Unix()), Input: out.Input},
	}, nil
}
