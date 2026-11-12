package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// Id answers the ID of the caller's account.
//
//nolint:revive // the generated interface names it
func (Server) Id(ctx context.Context, _ api.IdRequestObject) (api.IdResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	return api.Id200JSONResponse{Body: p.AccountID.String()}, nil
}

// Name answers the display name of the caller's account.
func (s Server) Name(ctx context.Context, _ api.NameRequestObject) (api.NameResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.accounts.Account(ctx, p.AccountID)
	if err != nil {
		// A credential outlives no account, so not found is as unexpected
		// as an unreachable database, which the store marks.
		return nil, err //nolint:wrapcheck // the store names the read
	}
	return api.Name200JSONResponse{Body: a.Name}, nil
}

// Type answers ORG: the reference's AccountType is a GitHub account's,
// and an infraweave account belongs to an organization, never to one
// person.
func (Server) Type(ctx context.Context, _ api.TypeRequestObject) (api.TypeResponseObject, error) {
	if _, err := principal(ctx); err != nil {
		return nil, err
	}
	return api.Type200JSONResponse{Body: api.AccountTypeORG}, nil
}
