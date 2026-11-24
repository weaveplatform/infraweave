package linking_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/linking"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var errDown = errors.New("database down")

// store keeps one organization's links, providers and identities in
// memory, failing each method that fail names.
type store struct {
	links      map[kernel.ULID]linking.Link
	created    []kernel.OrganizationAuditEntry
	providers  []sso.Provider
	identities map[kernel.MemberID][]linking.Federation
	unlinked   []sso.Kind
	revoked    []kernel.ULID
	fail       map[string]error
	failOn     kernel.MemberID
}

func (s *store) CreateLink(_ context.Context, l linking.Link, e kernel.OrganizationAuditEntry) error {
	if err := s.fail["CreateLink"]; err != nil {
		return err
	}
	s.links[l.ID] = l
	s.created = append(s.created, e)
	return nil
}

func (s *store) Link(_ context.Context, id kernel.ULID) (linking.Link, kernel.Slug, error) {
	l, ok := s.links[id]
	if !ok {
		return linking.Link{}, "", port.ErrNotFound
	}
	return l, "acme", nil
}

func (s *store) Providers(context.Context, kernel.OrganizationID) ([]sso.Provider, error) {
	return s.providers, s.fail["Providers"]
}

func (s *store) Identities(_ context.Context, _ kernel.OrganizationID, m kernel.MemberID) ([]linking.Federation, error) {
	if m == s.failOn {
		return nil, errDown
	}
	return s.identities[m], s.fail["Identities"]
}

func (s *store) UnlinkIdentities(_ context.Context, _ kernel.OrganizationID, m kernel.MemberID, _ kernel.ULID,
	kind sso.Kind, _ kernel.OrganizationAuditEntry,
) ([]linking.Federation, []kernel.ULID, error) {
	if err := s.fail["UnlinkIdentities"]; err != nil {
		return nil, nil, err
	}
	s.unlinked = append(s.unlinked, kind)
	return s.identities[m][:1], s.revoked, nil
}

type admins struct{ err error }

func (a admins) Admit(context.Context, orgadmin.Actor) error { return a.err }

// signIn records the sign-ins it starts.
type signIn struct{ started []sso.Start }

func (s *signIn) Start(_ context.Context, in sso.Start) (sso.Started, error) {
	s.started = append(s.started, in)
	return sso.Started{Location: "https://idp.example/authorize", Cookie: "c"}, nil
}

type world struct {
	svc     *linking.Service
	st      *store
	signIns *signIn
	dropped []kernel.ULID
	org     kernel.OrganizationID
	a, b    kernel.MemberID
	okta    sso.Provider
	nav     sso.Provider
	now     time.Time
}

// newWorld is two members, a at okta and b at nav, both enabled OIDC
// providers, beside a disabled provider and a SAML one a holds identities
// at too.
func newWorld(admit error) *world {
	w := &world{
		org: kernel.NewOrganizationID(), a: kernel.NewMemberID(), b: kernel.NewMemberID(), now: t0,
		signIns: &signIn{},
	}
	w.okta = sso.Provider{ID: kernel.NewULID(), Kind: sso.OIDC, Name: "Okta", EnabledAt: t0}
	w.nav = sso.Provider{ID: kernel.NewULID(), Kind: sso.OIDC, Name: "Nav", EnabledAt: t0}
	off := sso.Provider{ID: kernel.NewULID(), Kind: sso.OIDC, Name: "Off"}
	saml := sso.Provider{ID: kernel.NewULID(), Kind: sso.SAML, Name: "SAML", EnabledAt: t0}
	w.st = &store{
		links:     map[kernel.ULID]linking.Link{},
		providers: []sso.Provider{w.okta, w.nav, off, saml},
		identities: map[kernel.MemberID][]linking.Federation{
			w.a: {
				{ProviderID: w.okta.ID, Kind: sso.OIDC, Subject: "a"},
				{ProviderID: off.ID, Kind: sso.OIDC, Subject: "a-off"},
				{ProviderID: saml.ID, Kind: sso.SAML, Subject: "a-saml"},
			},
			w.b: {{ProviderID: w.nav.ID, Kind: sso.OIDC, Subject: "b"}},
		},
		fail: map[string]error{},
	}
	w.svc = linking.NewService(linking.Deps{
		Store: w.st, Admins: admins{admit}, SignIn: w.signIns,
		Drop: func(id kernel.ULID) { w.dropped = append(w.dropped, id) }, PublicURL: "https://iw.example.com/",
		Now: func() time.Time { return w.now },
	})
	return w
}

func (w *world) admin() orgadmin.Actor {
	return orgadmin.Actor{Principal: kernel.Principal{
		OrganizationID: w.org, Kind: kernel.PrincipalUser, ActorID: w.a.ULID, MemberID: w.a,
	}, RemoteIP: "192.0.2.1"}
}

// TestUnitMemberLinking walks a link through the service: an organization
// admin opens it, its page offers each member's enabled OIDC providers
// until that member signs in, each start asks for a link sign-in that
// returns to the page, and a completed link shows without providers.
// Nothing links two members without an admin's link, and the link names
// members by ID, never by e-mail address.
//
// ref: decision 0006 § "Members and identity providers"
func TestUnitMemberLinking(t *testing.T) {
	ctx := t.Context()
	w := newWorld(nil)
	// Until an admin links them, each identity signs in as its own
	// member: one no member holds is a new member, whatever e-mail
	// address it asserts, and one a member holds is that member.
	if res, m, err := sso.ResolveMember(nil, nil); err != nil || res != sso.ResolveCreate || !m.IsZero() {
		t.Errorf("an identity no member holds = %v, %s, %v", res, m, err)
	}
	if res, m, err := sso.ResolveMember(&sso.Candidate{ID: w.b, Status: member.Active}, nil); err != nil ||
		res != sso.ResolveExisting || m != w.b {
		t.Errorf("b's identity = %v, %s, %v", res, m, err)
	}
	l, err := w.svc.Create(ctx, w.admin(), w.a.String(), w.b.String())
	if err != nil {
		t.Fatal(err)
	}
	if l.MemberID != w.a || l.OtherMemberID != w.b || l.ExpiresAt != t0.Add(time.Hour) ||
		l.CreatedBy != "user:"+w.a.String() || l.OrganizationID != w.org {
		t.Errorf("link = %+v", l)
	}
	if e := w.st.created[0]; e.Action != linking.ActionCreate || e.AffectedResource.ID != w.a.String() ||
		e.RemoteIP != "192.0.2.1" || e.ActorID != w.a.String() {
		t.Errorf("entry = %+v", e)
	}
	if got := w.svc.URL(l); got != "https://iw.example.com/auth/link/"+l.ID.String() {
		t.Errorf("URL = %s", got)
	}

	pg, err := w.svc.Page(ctx, l.ID.String())
	if err != nil || pg.Organization != "acme" || len(pg.Member) != 1 || pg.Member[0].ID != w.okta.ID ||
		len(pg.Other) != 1 || pg.Other[0].ID != w.nav.ID {
		t.Fatalf("page = %+v, %v", pg, err)
	}
	by := &kernel.Principal{MemberID: w.b}
	st, err := w.svc.Start(ctx, l.ID.String(), w.nav.ID.String(), by)
	if err != nil || st.Location == "" {
		t.Fatalf("Start = %+v, %v", st, err)
	}
	if in := w.signIns.started[0]; in.Purpose != string(sso.PurposeLink) || in.Link != l.ID ||
		in.Provider != w.nav.ID.String() || in.Organization != "acme" || in.By != by ||
		in.ReturnTo != "/auth/link/"+l.ID.String() {
		t.Errorf("started %+v", in)
	}
	for name, provider := range map[string]string{
		"a disabled provider": w.st.providers[2].ID.String(),
		"a SAML provider":     w.st.providers[3].ID.String(),
		"an unknown provider": "nope",
	} {
		if _, err := w.svc.Start(ctx, l.ID.String(), provider, nil); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s = %v", name, err)
		}
	}

	done := l
	done.MemberSignedAt, done.OtherMemberSignedAt, done.CompletedAt = t0, t0, t0
	w.st.links[l.ID] = done
	if pg, err := w.svc.Page(ctx, l.ID.String()); err != nil || pg.Member != nil || pg.Other != nil {
		t.Errorf("a completed link's page = %+v, %v", pg, err)
	}
	if _, err := w.svc.Start(ctx, l.ID.String(), w.okta.ID.String(), nil); !errors.Is(err, linking.ErrEnded) {
		t.Errorf("a completed link's start = %v", err)
	}
	w.st.links[l.ID] = l
	w.now = t0.Add(time.Hour)
	if _, err := w.svc.Page(ctx, l.ID.String()); !errors.Is(err, linking.ErrEnded) {
		t.Errorf("an expired link's page = %v", err)
	}
	if _, err := w.svc.Start(ctx, l.ID.String(), w.okta.ID.String(), nil); !errors.Is(err, linking.ErrEnded) {
		t.Errorf("an expired link's start = %v", err)
	}
	if len(w.signIns.started) != 1 {
		t.Errorf("started %d sign-ins, want 1", len(w.signIns.started))
	}
}

// TestUnitLinkRefusals refuses a link a member who is no organization
// admin opens, IDs that name no member and links that name no link, and
// passes the store's refusals and failures on.
func TestUnitLinkRefusals(t *testing.T) {
	ctx := t.Context()
	w := newWorld(orgadmin.ErrForbidden)
	if _, err := w.svc.Create(ctx, w.admin(), w.a.String(), w.b.String()); !errors.Is(err, orgadmin.ErrForbidden) {
		t.Errorf("a member's link = %v", err)
	}
	w = newWorld(nil)
	for _, ids := range [][2]string{{"alice@example.com", w.b.String()}, {w.a.String(), "bob@example.com"}} {
		if _, err := w.svc.Create(ctx, w.admin(), ids[0], ids[1]); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("Create(%s, %s) = %v", ids[0], ids[1], err)
		}
	}
	w.st.fail["CreateLink"] = linking.ErrLinkExists
	if _, err := w.svc.Create(ctx, w.admin(), w.a.String(), w.b.String()); !errors.Is(err, linking.ErrLinkExists) {
		t.Errorf("a second open link = %v", err)
	}
	for _, id := range []string{"nope", kernel.NewULID().String()} {
		if _, err := w.svc.Page(ctx, id); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("Page(%s) = %v", id, err)
		}
	}
	l := linking.Link{ID: kernel.NewULID(), MemberID: w.a, OtherMemberID: w.b, ExpiresAt: t0.Add(time.Hour)}
	w.st.links[l.ID] = l
	for _, method := range []string{"Identities", "Providers"} {
		w.st.fail = map[string]error{method: errDown}
		if _, err := w.svc.Page(ctx, l.ID.String()); !errors.Is(err, errDown) {
			t.Errorf("Page with %s down = %v", method, err)
		}
	}
	w.st.fail, w.st.failOn = map[string]error{}, w.b
	if _, err := w.svc.Page(ctx, l.ID.String()); !errors.Is(err, errDown) {
		t.Errorf("Page with the other member's identities unread = %v", err)
	}
	w.st.failOn = kernel.MemberID{}
	other := map[kernel.MemberID][]linking.Federation{w.a: w.st.identities[w.a]}
	w.st.fail, w.st.identities = map[string]error{}, other
	if pg, err := w.svc.Page(ctx, l.ID.String()); err != nil || len(pg.Other) != 0 {
		t.Errorf("a member without an identity left = %+v, %v", pg, err)
	}
}

// TestUnitUnlink removes the caller's member's identities of a kind and
// drops the sessions signed in through them, answers the identities left,
// and refuses a principal that names no member and a provider that is no
// identity provider kind.
func TestUnitUnlink(t *testing.T) {
	ctx := t.Context()
	w := newWorld(nil)
	revoked := kernel.NewULID()
	w.st.revoked = []kernel.ULID{revoked}
	p := kernel.Principal{OrganizationID: w.org, Kind: kernel.PrincipalUser, ActorID: w.a.ULID, MemberID: w.a}
	id, err := w.svc.Unlink(ctx, p, "192.0.2.1", "SAML")
	if err != nil || id.MemberID != w.a || len(id.Federations) != 1 || w.st.unlinked[0] != sso.SAML ||
		len(w.dropped) != 1 || w.dropped[0] != revoked {
		t.Errorf("Unlink = %+v, %v; unlinked %v, dropped %v", id, err, w.st.unlinked, w.dropped)
	}
	if _, err := w.svc.Unlink(ctx, kernel.Principal{OrganizationID: w.org, Kind: kernel.PrincipalBreakGlass}, "",
		"OIDC"); !errors.Is(err, linking.ErrNoMember) {
		t.Errorf("the break-glass admin = %v", err)
	}
	var fe *organization.FieldError
	if _, err := w.svc.Unlink(ctx, p, "", "GOOGLE_OAUTH"); !errors.As(err, &fe) || fe.Field != "provider" {
		t.Errorf("GOOGLE_OAUTH = %v", err)
	}
	w.st.fail["UnlinkIdentities"] = errDown
	if _, err := w.svc.Unlink(ctx, p, "", "OIDC"); !errors.Is(err, errDown) {
		t.Errorf("a store down = %v", err)
	}

	if got, err := w.svc.Identity(ctx, p); err != nil || got == nil || len(got.Federations) != 3 {
		t.Errorf("Identity = %+v, %v", got, err)
	}
	if got, err := w.svc.Identity(ctx, kernel.Principal{OrganizationID: w.org}); err != nil || got != nil {
		t.Errorf("no member's identity = %+v, %v", got, err)
	}
	w.st.fail["Identities"] = errDown
	if _, err := w.svc.Identity(ctx, p); !errors.Is(err, errDown) {
		t.Errorf("Identity with the store down = %v", err)
	}
}
