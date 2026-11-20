package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// PolicyCheck dispatches editor compilation to the registered policy owner.
func (s Server) PolicyCheck(ctx context.Context, req api.PolicyCheckRequestObject) (api.PolicyCheckResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if s.registry == nil {
		return nil, kernel.ErrUnavailable
	}
	surface, ok := s.registry.Surface("policy")
	if !ok || surface.CheckPolicy == nil {
		return nil, kernel.ErrUnavailable
	}
	body := req.Body
	in := expr.PolicySource{Type: string(body.Type), Body: body.Body}
	if body.EngineType != nil {
		in.Engine = string(*body.EngineType)
	}
	fields, err := surface.CheckPolicy(ctx, p, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // the surface owner supplies the public failure
	}
	diagnostics := make([]api.ProblemFieldError, 0, len(fields))
	for _, f := range fields {
		d := api.ProblemFieldError{Pointer: f.Pointer, Reason: api.ProblemFieldErrorReason(f.Reason), Message: f.Message}
		if f.Range != nil {
			r := sourceRange(*f.Range)
			d.Range = &r
		}
		diagnostics = append(diagnostics, d)
	}
	return api.PolicyCheck200JSONResponse{Body: api.PolicyCheck{Diagnostics: diagnostics}}, nil
}
