package http

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/operator"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// organizationsList names the organization list's cursors.
const organizationsList = "organizations"

// OperatorServer is the access domain's partial of the operator API's
// strict server: the organizations and their break-glass credentials. The
// operator authenticator admits operator principals alone, so every
// caller is one.
type OperatorServer struct{ orgs *operator.Service }

// NewOperatorServer returns the partial serving through orgs.
func NewOperatorServer(orgs *operator.Service) OperatorServer {
	return OperatorServer{orgs: orgs}
}

// Organizations lists the installation's organizations, oldest first.
func (s OperatorServer) Organizations(
	ctx context.Context,
	req opapi.OrganizationsRequestObject,
) (opapi.OrganizationsResponseObject, error) {
	var after kernel.OrganizationID
	if req.Params.Cursor != nil {
		c, err := page.Decode[string](organizationsList, *req.Params.Cursor)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		if after.ULID, err = kernel.ParseULID(c.ID); err != nil {
			//nolint:wrapcheck // a problem, answered as it is
			return nil, problem.InvalidCursor.Error("The cursor's position is not an organization.")
		}
	}
	var limit *int32
	if req.Params.Limit != nil {
		l := *req.Params.Limit
		limit = &l
	}
	n := int(kernel.PageLimit(limit))
	// One more than the page shows whether another follows.
	orgs, err := s.orgs.List(ctx, after, n+1)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	body := opapi.OrganizationPage{Items: make([]opapi.Organization, 0, min(len(orgs), n))}
	for _, o := range orgs[:min(len(orgs), n)] {
		body.Items = append(body.Items, orgView(o))
	}
	if len(orgs) > n {
		last := orgs[n-1].ID.String()
		next, err := page.Encode(organizationsList, kernel.Cursor[string]{Key: last, ID: last})
		if err != nil {
			return nil, err //nolint:wrapcheck // encoding a string cannot fail
		}
		body.NextCursor = &next
	}
	return opapi.Organizations200JSONResponse{Body: body}, nil
}

// OrganizationCreate creates an organization with its first account and,
// when the body names one, its break-glass credential.
func (s OperatorServer) OrganizationCreate(
	ctx context.Context,
	req opapi.OrganizationCreateRequestObject,
) (opapi.OrganizationCreateResponseObject, error) {
	in := operator.Input{
		Slug:        req.Body.Slug,
		Name:        req.Body.Name,
		AccountSlug: req.Body.Account.Slug,
		AccountName: req.Body.Account.Name,
	}
	if bg := req.Body.BreakGlass; bg != nil {
		in.BreakGlass = &operator.BreakGlassInput{
			Login:        bg.Login,
			PasswordHash: deref(bg.PasswordHash),
		}
	}
	o, err := s.orgs.Create(ctx, actor(ctx), in)
	if err != nil {
		return nil, orgError(err, req.Body.Slug)
	}
	return opapi.OrganizationCreate201JSONResponse{Body: orgView(o)}, nil
}

// Organization reads an organization.
func (s OperatorServer) Organization(
	ctx context.Context,
	req opapi.OrganizationRequestObject,
) (opapi.OrganizationResponseObject, error) {
	o, err := s.orgs.Get(ctx, req.Organization)
	if err != nil {
		return nil, orgError(err, req.Organization)
	}
	return opapi.Organization200JSONResponse{Body: orgView(o)}, nil
}

// OrganizationDelete deletes an organization and its accounts, and answers
// it as it was.
func (s OperatorServer) OrganizationDelete(
	ctx context.Context,
	req opapi.OrganizationDeleteRequestObject,
) (opapi.OrganizationDeleteResponseObject, error) {
	o, err := s.orgs.Delete(ctx, actor(ctx), req.Organization)
	if err != nil {
		return nil, orgError(err, req.Organization)
	}
	return opapi.OrganizationDelete200JSONResponse{Body: orgView(o)}, nil
}

// OrganizationBreakGlassSet sets or replaces an organization's break-glass
// credential.
func (s OperatorServer) OrganizationBreakGlassSet(
	ctx context.Context,
	req opapi.OrganizationBreakGlassSetRequestObject,
) (opapi.OrganizationBreakGlassSetResponseObject, error) {
	o, err := s.orgs.SetBreakGlass(ctx, actor(ctx), req.Organization, operator.BreakGlassInput{
		Login: req.Body.Login, PasswordHash: deref(req.Body.PasswordHash),
	})
	if err != nil {
		return nil, orgError(err, req.Organization)
	}
	return opapi.OrganizationBreakGlassSet200JSONResponse{Body: orgView(o)}, nil
}

// OrganizationBreakGlassDelete removes an organization's break-glass
// credential.
func (s OperatorServer) OrganizationBreakGlassDelete(
	ctx context.Context,
	req opapi.OrganizationBreakGlassDeleteRequestObject,
) (opapi.OrganizationBreakGlassDeleteResponseObject, error) {
	o, err := s.orgs.DeleteBreakGlass(ctx, actor(ctx), req.Organization)
	if err != nil {
		return nil, orgError(err, req.Organization)
	}
	return opapi.OrganizationBreakGlassDelete200JSONResponse{Body: orgView(o)}, nil
}

// actor is the request's principal and client address, which the
// operator authenticator recorded.
func actor(ctx context.Context) operator.Actor {
	p, _ := kernel.PrincipalFrom(ctx)
	return operator.Actor{Principal: p, RemoteIP: from(ctx).addr}
}

// orgError is err as the problem it answers: 404 for an organization that
// does not exist, 409 already-exists for a slug in use, 422 for input the
// service refuses, and err itself otherwise, which the store marks when
// the database is unreachable.
func orgError(err error, slug string) error {
	switch {
	case errors.Is(err, port.ErrNotFound):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No organization %q.", slug))
	case errors.Is(err, port.ErrSlugTaken):
		// Only the organization's slug can be taken: its first account's
		// is unique within an organization that is new.
		//nolint:wrapcheck // as above
		return problem.AlreadyExists.Error(
			fmt.Sprintf("Another organization has the slug %q.", slug),
		)
	case errors.Is(err, organization.ErrInvalid), errors.Is(err, breakglass.ErrInvalid):
		return problem.ValidationFailed.Error(err.Error()) //nolint:wrapcheck // as above
	}
	return err
}

// orgView is o in the operator API's Organization shape; the hash is never
// answered.
func orgView(o operator.Organization) opapi.Organization {
	out := opapi.Organization{
		Id:        o.ID.String(),
		Slug:      string(o.Slug),
		Name:      o.Name,
		CreatedAt: o.CreatedAt.Unix(),
		Accounts:  make([]opapi.OrganizationAccount, 0, len(o.Accounts)),
	}
	for _, a := range o.Accounts {
		out.Accounts = append(out.Accounts, opapi.OrganizationAccount{
			Id: a.ID.String(), Slug: string(a.Slug), Name: a.Name, Claimed: a.Claimed(),
		})
	}
	if bg := o.BreakGlass; bg != nil {
		out.BreakGlass = &opapi.BreakGlass{Login: bg.Login, UpdatedAt: bg.UpdatedAt.Unix()}
	}
	return out
}
