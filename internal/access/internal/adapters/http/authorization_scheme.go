package http

import (
	"context"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// AuthorizationScheme reads only the authenticated caller's account.
func (s Server) AuthorizationScheme(ctx context.Context, _ api.AuthorizationSchemeRequestObject) (api.AuthorizationSchemeResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	return api.AuthorizationScheme200JSONResponse{Body: api.AuthorizationScheme(st.AuthorizationScheme)}, nil
}

// AccountUpdateAuthorizationScheme switches the caller's account atomically.
func (s Server) AccountUpdateAuthorizationScheme(
	ctx context.Context,
	req api.AccountUpdateAuthorizationSchemeRequestObject,
) (api.AccountUpdateAuthorizationSchemeResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	value, err := s.settings.SetAuthorizationScheme(ctx, p, accessapi.AuthorizationScheme(req.Body.Schema), from(ctx).addr)
	if err != nil {
		return nil, settingError(err)
	}
	return api.AccountUpdateAuthorizationScheme200JSONResponse{Body: api.AuthorizationScheme(value)}, nil
}
