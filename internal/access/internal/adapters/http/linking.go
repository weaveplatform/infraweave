package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/linking"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// OrganizationMemberLinkCreate opens a link that merges the body's
// member into the path's, and answers the page where the person proves
// both.
func (s Server) OrganizationMemberLinkCreate(ctx context.Context, req api.OrganizationMemberLinkCreateRequestObject) (api.OrganizationMemberLinkCreateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	l, err := s.linking.Create(ctx, by, req.Member, req.Body.OtherMemberId)
	if errors.Is(err, linking.ErrLinkExists) {
		return nil, problem.AlreadyExists.Error( //nolint:wrapcheck // a problem, answered as it is
			"The two members have an open link already; it ends an hour after it was opened.")
	}
	if err != nil {
		return nil, orgAdminError(err, fmt.Sprintf("member %q or %q", req.Member, req.Body.OtherMemberId))
	}
	return api.OrganizationMemberLinkCreate201JSONResponse{Body: api.OrganizationMemberLink{
		Id: l.ID.String(), MemberId: l.MemberID.String(), OtherMemberId: l.OtherMemberID.String(),
		Url: s.linking.URL(l), ExpiresAt: l.ExpiresAt.Unix(),
	}}, nil
}

// IdentityFederationUnlink removes the caller's member's identities of a
// kind but the one its session signed in through, and answers those that
// remain. A federation provider that is no identity provider kind, such
// as GOOGLE_OAUTH, names no identity the member can hold.
func (s Server) IdentityFederationUnlink(ctx context.Context, req api.IdentityFederationUnlinkRequestObject) (api.IdentityFederationUnlinkResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := sso.ParseKind(req.Provider); err != nil && api.FederationProvider(req.Provider).Valid() {
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.NotFound.Error(fmt.Sprintf("The member has no %s identity.", req.Provider))
	}
	id, err := s.linking.Unlink(ctx, p, from(ctx).addr, req.Provider)
	switch {
	case errors.Is(err, linking.ErrNoMember):
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.Forbidden.Error(
			"Only a member's session or personal API key holds identities to unlink.")
	case errors.Is(err, linking.ErrNoIdentity):
		//nolint:wrapcheck // as above
		return nil, problem.NotFound.Error(fmt.Sprintf("The member has no %s identity.", req.Provider))
	case errors.Is(err, organization.ErrInvalid):
		return nil, problem.ValidationFailed.Error(err.Error()) //nolint:wrapcheck // as above
	case err != nil:
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	return api.IdentityFederationUnlink200JSONResponse{Body: identityView(id)}, nil
}

// identityView is a member's identities in the API's Identity shape:
// GitHub OAuth's kind is the federation provider GITHUB_OAUTH, and a
// login the provider never asserted no externalUsername.
func identityView(id linking.Identity) api.Identity {
	out := api.Identity{Id: id.MemberID.String(), Federations: make([]api.IdentityFederation, 0, len(id.Federations))}
	for _, f := range id.Federations {
		v := api.IdentityFederation{Provider: api.FederationProvider(f.Kind), ExternalID: f.Subject}
		if f.Login != "" {
			login := f.Login
			v.ExternalUsername = &login
		}
		out.Federations = append(out.Federations, v)
	}
	return out
}

// identityOf sets u's identity to p's member's identities, and leaves it
// unset for a principal that names no member and when no linking service
// serves.
func (s Server) identityOf(ctx context.Context, p kernel.Principal, u *api.User) error {
	if s.linking == nil {
		return nil
	}
	id, err := s.linking.Identity(ctx, p)
	if err != nil || id == nil {
		return err //nolint:wrapcheck // the store marks an unreachable database
	}
	v := identityView(*id)
	u.Identity = &v
	return nil
}

// Linking serves a link's page beside the APIs: the person who holds
// both linked members signs in through an identity provider of each, and
// every refusal is a page, as a sign-in's is.
//
// ref: decision 0006 § "Members and identity providers"
type Linking struct {
	svc  *linking.Service
	auth *authn.Service
}

// NewLinking returns the pages over svc, reading a browser's session
// through auth.
func NewLinking(svc *linking.Service, auth *authn.Service) Linking {
	return Linking{svc: svc, auth: auth}
}

// Routes mounts the link's page and its sign-in starts on r.
func (l Linking) Routes(r chi.Router) {
	r.Get(accessapi.LinkPattern, l.page)
	r.Get(accessapi.LinkProviderPattern, l.start)
}

// page answers the link's providers to sign in through, each member's
// until that member has signed in, or that the link has completed.
func (l Linking) page(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "linkId")
	pg, err := l.svc.Page(r.Context(), id)
	if err != nil {
		refuse(w, r, err)
		return
	}
	out := signInPage{Title: "Link two members", Organization: string(pg.Organization)}
	if !pg.Link.CompletedAt.IsZero() {
		out.Message = "The two members are one member now."
		render(w, http.StatusOK, out)
		return
	}
	out.Message = "Sign in through an identity provider of each member before the link expires."
	for _, side := range []struct {
		name   string
		signed bool
		ps     []sso.Provider
	}{
		{"the first member", !pg.Link.MemberSignedAt.IsZero(), pg.Member},
		{"the second member", !pg.Link.OtherMemberSignedAt.IsZero(), pg.Other},
	} {
		if side.signed {
			continue
		}
		for _, p := range side.ps {
			out.Providers = append(out.Providers, link{
				Name: p.Name + ", as " + side.name,
				Href: linking.LinkPathPrefix + url.PathEscape(id) + "/" + url.PathEscape(p.ID.String()),
			})
		}
	}
	render(w, http.StatusOK, out)
}

// start starts the link's sign-in through the provider and sends the
// browser to its authorization endpoint, with the cookie that binds the
// request to the browser.
func (l Linking) start(w http.ResponseWriter, r *http.Request) {
	by, err := SignIn{auth: l.auth}.browser(r)
	if err != nil {
		refuse(w, r, err)
		return
	}
	st, err := l.svc.Start(r.Context(), chi.URLParam(r, "linkId"), chi.URLParam(r, "providerId"), by)
	if err != nil {
		refuse(w, r, err)
		return
	}
	w.Header().Add("Set-Cookie", requestCookie(st.Cookie, int(sso.RequestTTL.Seconds())))
	redirect(w, r, st.Location)
}
