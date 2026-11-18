package http

import (
	"context"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// VcsEventTriggeredRunsLimit reads the authenticated account's optional cap.
func (s Server) VcsEventTriggeredRunsLimit(ctx context.Context, _ api.VcsEventTriggeredRunsLimitRequestObject) (api.VcsEventTriggeredRunsLimitResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if s.limits == nil {
		return nil, kernel.ErrUnavailable
	}
	limit, err := s.limits.Get(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the service names the read
	}
	return api.VcsEventTriggeredRunsLimit200JSONResponse{Body: limitView(limit)}, nil
}

// AccountUpdateVCSEventTriggeredRunsLimit writes the account cap and audit in
// one transaction. The route has decided ACCOUNT_ADMIN on this account.
func (s Server) AccountUpdateVCSEventTriggeredRunsLimit(
	ctx context.Context,
	req api.AccountUpdateVCSEventTriggeredRunsLimitRequestObject,
) (api.AccountUpdateVCSEventTriggeredRunsLimitResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if s.limits == nil {
		return nil, kernel.ErrUnavailable
	}
	var limit *int
	if req.Body != nil && req.Body.Limit != nil {
		limit = new(int(*req.Body.Limit))
	}
	value, err := s.limits.Set(ctx, p, limit, accessapi.ClientAddr(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // the service names validation and write failures
	}
	return api.AccountUpdateVCSEventTriggeredRunsLimit200JSONResponse{Body: limitView(value)}, nil
}

func limitView(limit *int) *int32 {
	if limit == nil {
		return nil
	}
	// The stored account value is an int32 in the served API and PostgreSQL.
	return new(int32(*limit)) //nolint:gosec // a value originates from the int32 request or integer column
}
