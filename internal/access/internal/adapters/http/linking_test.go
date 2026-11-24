package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/linking"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// linkStore keeps links over the sign-in world's organization and its
// provider, answering err from every call when it is set.
type linkStore struct {
	links      map[kernel.ULID]linking.Link
	slug       kernel.Slug
	providers  []sso.Provider
	identities map[kernel.MemberID][]linking.Federation
	err        error
}

func (s *linkStore) CreateLink(_ context.Context, l linking.Link, _ kernel.OrganizationAuditEntry) error {
	if s.err != nil {
		return s.err
	}
	s.links[l.ID] = l
	return nil
}

func (s *linkStore) Link(_ context.Context, id kernel.ULID) (linking.Link, kernel.Slug, error) {
	l, ok := s.links[id]
	if !ok {
		return linking.Link{}, "", port.ErrNotFound
	}
	return l, s.slug, nil
}

func (s *linkStore) Providers(context.Context, kernel.OrganizationID) ([]sso.Provider, error) {
	return s.providers, nil
}

func (s *linkStore) Identities(_ context.Context, _ kernel.OrganizationID, m kernel.MemberID) ([]linking.Federation, error) {
	return s.identities[m], s.err
}

func (s *linkStore) UnlinkIdentities(_ context.Context, _ kernel.OrganizationID, m kernel.MemberID, _ kernel.ULID,
	_ sso.Kind, _ kernel.OrganizationAuditEntry,
) ([]linking.Federation, []kernel.ULID, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.identities[m][:1], nil, nil
}

type linkWorld struct {
	*world
	st     *linkStore
	svc    *linking.Service
	h      http.Handler
	a, b   kernel.MemberID
	link   linking.Link
	admits error
}

func (w *linkWorld) Admit(context.Context, orgadmin.Actor) error { return w.admits }

// newLinkWorld is the sign-in world's organization with members a and b,
// each holding an identity at its one provider, and an open link of them.
func newLinkWorld(t *testing.T) *linkWorld {
	t.Helper()
	base := newWorld(t)
	p := base.sso.ps[0]
	w := &linkWorld{world: base, a: kernel.NewMemberID(), b: kernel.NewMemberID()}
	w.st = &linkStore{
		links: map[kernel.ULID]linking.Link{}, slug: base.sso.org.Slug, providers: base.sso.ps,
		identities: map[kernel.MemberID][]linking.Federation{
			w.a: {{ProviderID: p.ID, Kind: sso.OIDC, Subject: "a", Login: "ada"}},
			w.b: {{ProviderID: p.ID, Kind: sso.OIDC, Subject: "b"}, {ProviderID: kernel.NewULID(), Kind: sso.SAML, Subject: "b-saml"}},
		},
	}
	w.svc = linking.NewService(linking.Deps{
		Store: w.st, Admins: w, SignIn: base.oidc, Drop: base.auth.Cache().Drop,
		PublicURL: publicURL, Now: func() time.Time { return t0 },
	})
	w.link = linking.Link{
		OrganizationID: p.OrganizationID, ID: kernel.NewULID(), MemberID: w.a, OtherMemberID: w.b,
		CreatedAt: t0, ExpiresAt: t0.Add(linking.TTL),
	}
	w.st.links[w.link.ID] = w.link
	r := chi.NewRouter()
	NewLinking(w.svc, base.auth).Routes(r)
	w.h = r
	return w
}

func (w *linkWorld) page(t *testing.T, target string, cookies ...*http.Cookie) answer {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	return answerOf(rec)
}

// TestUnitLinkPages checks a link's page and its starts: the page offers
// each member's provider until that member signs in and tells a completed
// link apart; a start sets the request cookie and sends the browser to the
// provider for a fresh authentication; an expired link, an unknown one and
// a provider the page does not offer are refused as pages.
func TestUnitLinkPages(t *testing.T) {
	w := newLinkWorld(t)
	path := "/auth/link/" + w.link.ID.String()
	p := w.sso.ps[0]
	res := w.page(t, path)
	if res.StatusCode != http.StatusOK || strings.Count(res.Body, path+"/"+p.ID.String()) != 2 ||
		!strings.Contains(res.Body, "as the first member") || !strings.Contains(res.Body, "as the second member") {
		t.Errorf("an open link's page: %d %s", res.StatusCode, res.Body)
	}
	signed := w.link
	signed.OtherMemberSignedAt = t0
	w.st.links[w.link.ID] = signed
	if res := w.page(t, path); strings.Contains(res.Body, "as the second member") ||
		!strings.Contains(res.Body, "as the first member") {
		t.Errorf("after the second member's sign-in: %s", res.Body)
	}

	res = w.page(t, path+"/"+p.ID.String())
	loc, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || loc.Query().Get("prompt") != "login" || loc.Query().Get("max_age") != "0" {
		t.Errorf("a start: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	cookieOf(t, res, sso.RequestCookie)
	if r := w.sso.reqs[len(w.sso.reqs)-1]; r.Purpose != sso.PurposeLink || r.LinkID != w.link.ID || r.ReturnTo != path {
		t.Errorf("stored %+v", r)
	}
	if res := w.page(t, path+"/"+kernel.NewULID().String()); res.StatusCode != http.StatusNotFound {
		t.Errorf("a provider the page does not offer: %d", res.StatusCode)
	}
	if res := w.page(t, "/auth/link/"+kernel.NewULID().String()); res.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown link: %d", res.StatusCode)
	}
	w.st.err = errDown
	if res := w.page(t, path); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("identities down: %d", res.StatusCode)
	}
	w.st.err, w.world.st.down = nil, true
	if res := w.page(t, path+"/"+p.ID.String(), &http.Cookie{Name: session.CookieName, Value: "x"}); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("sessions down at a start: %d", res.StatusCode)
	}
	w.st.err, w.world.st.down = nil, false

	done := signed
	done.MemberSignedAt, done.CompletedAt = t0, t0
	w.st.links[w.link.ID] = done
	if res := w.page(t, path); res.StatusCode != http.StatusOK || !strings.Contains(res.Body, "one member now") ||
		strings.Contains(res.Body, path+"/") {
		t.Errorf("a completed link's page: %d %s", res.StatusCode, res.Body)
	}
	expired := w.link
	expired.ExpiresAt = t0
	w.st.links[w.link.ID] = expired
	if res := w.page(t, path); res.StatusCode != http.StatusGone || !strings.Contains(res.Body, "link-ended") {
		t.Errorf("an expired link's page: %d %s", res.StatusCode, res.Body)
	}
}

// TestUnitMemberLinkCreate answers an organization admin's link with its
// page's URL, and each refusal as its problem.
func TestUnitMemberLinkCreate(t *testing.T) {
	w := newLinkWorld(t)
	s := Server{linking: w.svc}
	admin := kernel.Principal{
		OrganizationID: w.link.OrganizationID, Kind: kernel.PrincipalUser, ActorID: w.a.ULID, MemberID: w.a,
	}
	ctx := kernel.WithPrincipal(t.Context(), admin)
	req := api.OrganizationMemberLinkCreateRequestObject{
		Member: w.a.String(), Body: &api.OrganizationMemberLinkCreateRequest{OtherMemberId: w.b.String()},
	}
	res, err := s.OrganizationMemberLinkCreate(ctx, req)
	got, ok := res.(api.OrganizationMemberLinkCreate201JSONResponse)
	if err != nil || !ok || got.Body.MemberId != w.a.String() || got.Body.OtherMemberId != w.b.String() ||
		got.Body.Url != publicURL+"/auth/link/"+got.Body.Id || got.Body.ExpiresAt != t0.Add(time.Hour).Unix() {
		t.Fatalf("a link = %+v, %v", res, err)
	}
	for name, c := range map[string]struct {
		change func()
		status int
		slug   string
	}{
		"no organization admin": {func() { w.admits = orgadmin.ErrForbidden }, http.StatusForbidden, problem.Forbidden.Slug},
		"an open link":          {func() { w.st.err = linking.ErrLinkExists }, http.StatusConflict, problem.AlreadyExists.Slug},
		"another organization":  {func() { w.st.err = port.ErrNotFound }, http.StatusNotFound, problem.NotFound.Slug},
		"a deactivated member": {func() {
			w.st.err = &organization.FieldError{Field: "memberId", Reason: "is deactivated"}
		}, http.StatusUnprocessableEntity, problem.ValidationFailed.Slug},
	} {
		w.admits, w.st.err = nil, nil
		c.change()
		_, err := s.OrganizationMemberLinkCreate(ctx, req)
		if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != c.status ||
			pe.Problem.Type != problem.TypePrefix+c.slug {
			t.Errorf("%s = %v", name, err)
		}
	}
	if _, err := s.OrganizationMemberLinkCreate(t.Context(), req); err == nil {
		t.Error("a link without a principal")
	}
}

// TestUnitIdentityFederationUnlink answers the identities left, and each
// refusal as its problem: 403 for a principal that names no member, 404
// for a federation provider no identity provider is, 422 for an unlink the
// service refuses.
func TestUnitIdentityFederationUnlink(t *testing.T) {
	w := newLinkWorld(t)
	s := Server{linking: w.svc}
	member := kernel.Principal{
		OrganizationID: w.link.OrganizationID, Kind: kernel.PrincipalUser, ActorID: w.b.ULID, MemberID: w.b,
	}
	ctx := kernel.WithPrincipal(t.Context(), member)
	res, err := s.IdentityFederationUnlink(ctx, api.IdentityFederationUnlinkRequestObject{Provider: "SAML"})
	got, ok := res.(api.IdentityFederationUnlink200JSONResponse)
	if err != nil || !ok || got.Body.Id != w.b.String() || len(got.Body.Federations) != 1 ||
		got.Body.Federations[0].ExternalID != "b" || got.Body.Federations[0].ExternalUsername != nil {
		t.Fatalf("an unlink = %+v, %v", res, err)
	}
	breakGlass := kernel.WithPrincipal(t.Context(), kernel.Principal{
		OrganizationID: w.link.OrganizationID, Kind: kernel.PrincipalBreakGlass,
	})
	for name, c := range map[string]struct {
		ctx      context.Context
		provider string
		err      error
		status   int
	}{
		"no member":      {breakGlass, "OIDC", nil, http.StatusForbidden},
		"GOOGLE_OAUTH":   {ctx, "GOOGLE_OAUTH", nil, http.StatusNotFound},
		"none held":      {ctx, "GITHUB_OAUTH", linking.ErrNoIdentity, http.StatusNotFound},
		"its own alone":  {ctx, "OIDC", &organization.FieldError{Field: "provider", Reason: "x"}, http.StatusUnprocessableEntity},
		"not a provider": {ctx, "nope", nil, http.StatusUnprocessableEntity},
	} {
		w.st.err = c.err
		_, err := s.IdentityFederationUnlink(c.ctx, api.IdentityFederationUnlinkRequestObject{Provider: c.provider})
		if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != c.status {
			t.Errorf("%s = %v", name, err)
		}
	}
	w.st.err = errDown
	if _, err := s.IdentityFederationUnlink(ctx, api.IdentityFederationUnlinkRequestObject{Provider: "OIDC"}); !errors.Is(err, errDown) {
		t.Errorf("a store down = %v", err)
	}
	if _, err := s.IdentityFederationUnlink(t.Context(), api.IdentityFederationUnlinkRequestObject{Provider: "OIDC"}); err == nil {
		t.Error("an unlink without a principal")
	}
}

// TestUnitUserIdentity sets the viewer's identity to its member's
// identities, the asserted login as externalUsername, and leaves it unset
// for a principal that names no member or without a linking service.
func TestUnitUserIdentity(t *testing.T) {
	w := newLinkWorld(t)
	s := Server{linking: w.svc}
	var u api.User
	if err := s.identityOf(t.Context(), kernel.Principal{MemberID: w.a}, &u); err != nil || u.Identity == nil ||
		u.Identity.Id != w.a.String() || len(u.Identity.Federations) != 1 ||
		u.Identity.Federations[0].Provider != api.FederationProviderOIDC ||
		u.Identity.Federations[0].ExternalUsername == nil || *u.Identity.Federations[0].ExternalUsername != "ada" {
		t.Errorf("a member's identity = %+v, %v", u.Identity, err)
	}
	u = api.User{}
	if err := s.identityOf(t.Context(), kernel.Principal{}, &u); err != nil || u.Identity != nil {
		t.Errorf("no member's identity = %+v, %v", u.Identity, err)
	}
	if err := (Server{}).identityOf(t.Context(), kernel.Principal{MemberID: w.a}, &u); err != nil || u.Identity != nil {
		t.Errorf("no linking service = %+v, %v", u.Identity, err)
	}
	w.st.err = errDown
	if err := s.identityOf(t.Context(), kernel.Principal{MemberID: w.a}, &u); !errors.Is(err, errDown) {
		t.Errorf("a store down = %v", err)
	}
}
