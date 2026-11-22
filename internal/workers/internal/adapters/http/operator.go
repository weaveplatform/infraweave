package http

import (
	"context"
	"errors"
	"strings"

	operatorapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// OperatorServer owns installation public-pool credential operations. The
// composition root authenticates the operator audience before these handlers.
type OperatorServer struct{ svc *pool.Service }

// NewOperatorServer binds the workers-owned installation credential lifecycle.
func NewOperatorServer(svc *pool.Service) OperatorServer { return OperatorServer{svc: svc} }

// PublicWorkerPoolCredentials reads the public token and current credential tag.
func (s OperatorServer) PublicWorkerPoolCredentials(
	ctx context.Context,
	req operatorapi.PublicWorkerPoolCredentialsRequestObject,
) (operatorapi.PublicWorkerPoolCredentialsResponseObject, error) {
	if s.svc == nil {
		return operatorapi.UnimplementedStrict{}.PublicWorkerPoolCredentials(ctx, req) //nolint:wrapcheck // the generated response is already an API problem
	}
	p, err := s.svc.Public(ctx)
	if err != nil {
		return nil, publicPoolError(err)
	}
	tag := strict.ETag(p.Version)
	return operatorapi.PublicWorkerPoolCredentials200JSONResponse{Body: s.view(p), Headers: operatorapi.PublicWorkerPoolCredentials200ResponseHeaders{ETag: &tag}}, nil
}

// PublicWorkerPoolCreate creates the singleton from the supplied public-key CSR.
func (s OperatorServer) PublicWorkerPoolCreate(ctx context.Context, req operatorapi.PublicWorkerPoolCreateRequestObject) (operatorapi.PublicWorkerPoolCreateResponseObject, error) {
	if s.svc == nil {
		return operatorapi.UnimplementedStrict{}.PublicWorkerPoolCreate(ctx, req) //nolint:wrapcheck // the generated response is already an API problem
	}
	p, err := s.svc.CreatePublic(ctx, req.Body.CertificateSigningRequest)
	if err != nil {
		return nil, publicPoolError(err)
	}
	tag := strict.ETag(p.Version)
	return operatorapi.PublicWorkerPoolCreate201JSONResponse{Body: s.view(p), Headers: operatorapi.PublicWorkerPoolCreate201ResponseHeaders{ETag: &tag}}, nil
}

// PublicWorkerPoolReset requires a credential precondition before rotating it.
func (s OperatorServer) PublicWorkerPoolReset(ctx context.Context, req operatorapi.PublicWorkerPoolResetRequestObject) (operatorapi.PublicWorkerPoolResetResponseObject, error) {
	if s.svc == nil {
		return operatorapi.UnimplementedStrict{}.PublicWorkerPoolReset(ctx, req) //nolint:wrapcheck // the generated response is already an API problem
	}
	if req.Params.IfMatch == nil || strings.TrimSpace(*req.Params.IfMatch) == "" {
		return nil, problem.PreconditionRequired.Error("Public pool credential reset requires If-Match.") //nolint:wrapcheck // an API problem is answered as it is
	}
	release := req.Body.ReleaseBinding != nil && *req.Body.ReleaseBinding
	p, err := s.svc.ResetPublic(ctx, strict.IfMatch(req.Params.IfMatch), req.Body.CertificateSigningRequest, release)
	if err != nil {
		return nil, publicPoolError(err)
	}
	tag := strict.ETag(p.Version)
	return operatorapi.PublicWorkerPoolReset200JSONResponse{Body: s.view(p), Headers: operatorapi.PublicWorkerPoolReset200ResponseHeaders{ETag: &tag}}, nil
}

func (s OperatorServer) view(p pool.Pool) operatorapi.PublicWorkerPoolCredentials {
	return operatorapi.PublicWorkerPoolCredentials{Id: p.ID.String(), Config: s.svc.Token(p), Version: p.Version}
}

//nolint:wrapcheck // API problems are the final HTTP errors, not internal causes.
func publicPoolError(err error) error {
	switch {
	case errors.Is(err, pool.ErrNotFound):
		return problem.NotFound.Error("The installation public pool has not been created.")
	case errors.Is(err, pool.ErrPublicExists):
		return problem.InvalidState.Error("The installation public pool already exists.")
	case errors.Is(err, pool.ErrPublicBusy):
		return problem.InvalidState.Error("The public pool still has registered workers or active assignments.")
	case errors.Is(err, pool.ErrPublicCondition):
		return problem.PreconditionRequired.Error("Public pool credential reset requires If-Match.")
	default:
		return err
	}
}
