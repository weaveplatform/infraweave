package sso_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
)

// signings is sso.SignInStore over one organization. fail names the one
// method that answers errDown.
type signings struct {
	org      organization.Organization
	sole     bool
	ps       []sso.Provider
	reqs     []sso.Request
	consumed map[kernel.ULID]bool
	recs     []sso.SignInRecord
	signErr  error
	tokens   map[kernel.ULID]string
	jtis     map[string]bool
	revoked  []string
	expired  time.Time
	fail     string
}

func (s *signings) down(m string) error {
	if s.fail == m {
		return errDown
	}
	return nil
}

func (s *signings) SignInOrganization(_ context.Context, slug kernel.Slug) (organization.Organization, error) {
	if err := s.down("SignInOrganization"); err != nil {
		return organization.Organization{}, err
	}
	if slug != s.org.Slug {
		return organization.Organization{}, port.ErrNotFound
	}
	return s.org, nil
}

func (s *signings) SoleOrganization(context.Context) (kernel.Slug, bool, error) {
	return s.org.Slug, s.sole, s.down("SoleOrganization")
}

func (s *signings) Providers(context.Context, kernel.OrganizationID) ([]sso.Provider, error) {
	return slices.Clone(s.ps), s.down("Providers")
}

func (s *signings) Provider(_ context.Context, _ kernel.OrganizationID, id kernel.ULID) (sso.Provider, error) {
	if err := s.down("Provider"); err != nil {
		return sso.Provider{}, err
	}
	for _, p := range s.ps {
		if p.ID == id {
			return p, nil
		}
	}
	return sso.Provider{}, port.ErrNotFound
}

func (s *signings) ProviderSecret(context.Context, kernel.OrganizationID, kernel.ULID) (crypt.Value, error) {
	return crypt.NewValue([]byte("client-secret")), s.down("ProviderSecret")
}

func (s *signings) CreateSignInRequest(_ context.Context, r sso.Request) error {
	if err := s.down("CreateSignInRequest"); err != nil {
		return err
	}
	s.reqs = append(s.reqs, r)
	return nil
}

func (s *signings) ConsumeSignInRequest(_ context.Context, state, browser []byte) (sso.Request, error) {
	if err := s.down("ConsumeSignInRequest"); err != nil {
		return sso.Request{}, err
	}
	for _, r := range s.reqs {
		if slices.Equal(r.StateHash, state) && slices.Equal(r.BrowserHash, browser) && !s.consumed[r.ID] {
			s.consumed[r.ID] = true
			return r, nil
		}
	}
	return sso.Request{}, port.ErrNotFound
}

func (s *signings) SignInIdentity(_ context.Context, rec sso.SignInRecord) (session.Session, error) {
	if s.signErr != nil {
		return session.Session{}, s.signErr
	}
	s.recs = append(s.recs, rec)
	ss := rec.Session
	ss.MemberID = kernel.NewMemberID()
	return ss, nil
}

func (s *signings) ProvidersByClientID(_ context.Context, ids []string) ([]sso.Provider, error) {
	var out []sso.Provider
	for _, p := range s.ps {
		if slices.Contains(ids, p.Settings.ClientID) {
			out = append(out, p)
		}
	}
	return out, s.down("ProvidersByClientID")
}

func (s *signings) AcceptLogoutToken(_ context.Context, _ kernel.ULID, jti string, _ time.Time) (bool, error) {
	if err := s.down("AcceptLogoutToken"); err != nil {
		return false, err
	}
	fresh := !s.jtis[jti]
	s.jtis[jti] = true
	return fresh, nil
}

func (s *signings) RevokeIdentitySessions(_ context.Context, _ kernel.ULID, sid, subject string) ([]kernel.ULID, error) {
	if err := s.down("RevokeIdentitySessions"); err != nil {
		return nil, err
	}
	s.revoked = append(s.revoked, sid+"|"+subject)
	return []kernel.ULID{kernel.NewULID()}, nil
}

func (s *signings) SessionIDToken(_ context.Context, id kernel.ULID) (crypt.Value, bool, error) {
	tok, ok := s.tokens[id]
	return crypt.NewValue([]byte(tok)), ok, s.down("SessionIDToken")
}

func (s *signings) ExpireSignIns(_ context.Context, before time.Time) (int64, error) {
	s.expired = before
	return 1, s.down("ExpireSignIns")
}

// party is sso.RelyingParty answering what it is given and recording the
// exchange.
type party struct {
	ep          sso.Endpoints
	discoverErr error
	exchangeErr error
	client      sso.Client
	code, pkce  string
	claims      func() map[string]any
	verifyErr   error
	logout      sso.LogoutToken
	logoutErr   error
}

func (p *party) Discover(context.Context, string) (sso.Endpoints, error) {
	return p.ep, p.discoverErr
}

func (p *party) Exchange(_ context.Context, _ sso.Endpoints, c sso.Client, code, verifier string) (string, error) {
	p.client, p.code, p.pkce = c, code, verifier
	return "raw-id-token", p.exchangeErr
}

func (p *party) VerifyIDToken(context.Context, string, string, string) (map[string]any, error) {
	if p.verifyErr != nil {
		return nil, p.verifyErr
	}
	return p.claims(), nil
}

func (p *party) VerifyLogout(context.Context, string, string, string) (sso.LogoutToken, error) {
	return p.logout, p.logoutErr
}

// signInWorld is a sign-in over one organization with an enabled OIDC provider
// and a disabled one.
type signInWorld struct {
	st      *signings
	party   *party
	svc     *sso.SignIn
	on, off sso.Provider
	feat    features
	dropped []kernel.ULID
	admin   kernel.MemberID
}

func newSignInWorld(t *testing.T) *signInWorld {
	t.Helper()
	org := organization.Organization{ID: kernel.OrganizationID{ULID: kernel.NewULID()}, Slug: "acme", Name: "Acme"}
	w := &signInWorld{admin: kernel.NewMemberID()}
	w.on = sso.Provider{
		OrganizationID: org.ID, ID: kernel.NewULID(), Kind: sso.OIDC, Name: "Okta",
		Settings:  sso.Settings{IdentityProviderHost: "https://idp.test", ClientID: "client"},
		EnabledAt: t0, AdminLogin: "root",
	}
	w.off = sso.Provider{
		OrganizationID: org.ID, ID: kernel.NewULID(), Kind: sso.OIDC, Name: "Entra",
		Settings: sso.Settings{IdentityProviderHost: "https://entra.test", ClientID: "entra-client"},
	}
	saml := sso.Provider{OrganizationID: org.ID, ID: kernel.NewULID(), Kind: sso.SAML, Name: "ADFS", EnabledAt: t0}
	w.st = &signings{
		org: org, ps: []sso.Provider{w.on, w.off, saml}, consumed: map[kernel.ULID]bool{},
		tokens: map[kernel.ULID]string{}, jtis: map[string]bool{},
	}
	w.party = &party{ep: sso.Endpoints{
		Issuer: "https://idp.test", Authorization: "https://idp.test/authorize",
		Token: "https://idp.test/token", EndSession: "https://idp.test/logout?x=1",
	}}
	w.party.claims = func() map[string]any {
		r := w.st.reqs[len(w.st.reqs)-1]
		return map[string]any{
			"sub": "alice-sub", "aud": "client", "nonce": r.Nonce, "preferred_username": "alice",
			"groups": []any{"dev"}, "sid": "sid-1", "auth_time": float64(t0.Unix()),
		}
	}
	w.svc = sso.NewSignIn(sso.SignInDeps{
		Store: w.st, Party: w.party, Admins: admins{admin: w.admin}, Features: &w.feat,
		Drop: func(id kernel.ULID) { w.dropped = append(w.dropped, id) }, PublicURL: "https://iw.test/",
		Idle: time.Hour, MaxAge: 10 * time.Hour, Now: func() time.Time { return t0 },
	})
	return w
}

func (w *signInWorld) principal(kind kernel.PrincipalKind, m kernel.MemberID) *kernel.Principal {
	return &kernel.Principal{Kind: kind, OrganizationID: w.st.org.ID, MemberID: m, SessionID: kernel.NewULID()}
}

// started starts a sign-in through p and answers the authorization
// request's query and the cookie.
func (w *signInWorld) started(t *testing.T, in sso.Start) (url.Values, string) {
	t.Helper()
	if in.Organization == "" {
		in.Organization = "acme"
	}
	if in.Provider == "" {
		in.Provider = w.on.ID.String()
	}
	st, err := w.svc.Start(context.Background(), in)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	u, err := url.Parse(st.Location)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query(), st.Cookie
}

func digest(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func TestUnitSignInStart(t *testing.T) {
	w := newSignInWorld(t)
	q, cookie := w.started(t, sso.Start{ReturnTo: "/stacks"})
	r := w.st.reqs[0]
	for k, want := range map[string]string{
		"client_id": "client", "redirect_uri": "https://iw.test/auth/oidc/callback", "response_type": "code",
		"scope": "openid email profile groups", "nonce": r.Nonce, "code_challenge_method": "S256",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if q.Has("max_age") || q.Has("prompt") {
		t.Errorf("a sign-in asks for a fresh authentication: %v", q)
	}
	if err := oauth.CheckChallenge(q.Get("code_challenge"), q.Get("code_challenge_method")); err != nil ||
		!oauth.VerifyPKCE(q.Get("code_challenge"), r.Verifier) {
		t.Errorf("the challenge %q is not the stored verifier's: %v", q.Get("code_challenge"), err)
	}
	if !slices.Equal(r.StateHash, digest(q.Get("state"))) || !slices.Equal(r.BrowserHash, digest(cookie)) ||
		r.Purpose != sso.PurposeSignIn || r.ReturnTo != "/stacks" || r.ProviderID != w.on.ID ||
		r.OrganizationID != w.st.org.ID || !r.ExpiresAt.Equal(t0.Add(sso.RequestTTL)) || r.Nonce == "" ||
		r.Nonce == q.Get("state") || cookie == q.Get("state") {
		t.Errorf("stored %+v for state %q and cookie %q", r, q.Get("state"), cookie)
	}
	if !strings.HasPrefix(w.party.ep.Authorization, "https://idp.test/authorize") {
		t.Fatal(w.party.ep)
	}

	q, _ = w.started(t, sso.Start{Purpose: "reauthenticate", By: w.principal(kernel.PrincipalUser, kernel.NewMemberID())})
	if q.Get("max_age") != "0" || q.Get("prompt") != "login" {
		t.Errorf("a re-authentication's request: %v", q)
	}
	w.started(t, sso.Start{
		Purpose: "enable", Provider: w.off.ID.String(), By: w.principal(kernel.PrincipalUser, w.admin),
	})
	w.started(t, sso.Start{
		Purpose: "enable", Provider: w.off.ID.String(), By: w.principal(kernel.PrincipalBreakGlass, kernel.MemberID{}),
	})
}

func TestUnitSignInStartRefusals(t *testing.T) {
	member := kernel.NewMemberID()
	for name, c := range map[string]struct {
		in     func(*signInWorld) sso.Start
		change func(*signInWorld)
		want   error
	}{
		"malformed organization": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "Not A Slug!", Provider: w.on.ID.String()}
		}, want: port.ErrNotFound},
		"unknown organization": {in: func(w *signInWorld) sso.Start { return sso.Start{Organization: "other", Provider: w.on.ID.String()} }, want: port.ErrNotFound},
		"malformed provider":   {in: func(*signInWorld) sso.Start { return sso.Start{Organization: "acme", Provider: "x"} }, want: port.ErrNotFound},
		"unknown provider": {in: func(*signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: kernel.NewULID().String()}
		}, want: port.ErrNotFound},
		"unknown purpose": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: w.on.ID.String(), Purpose: "x"}
		}, want: sso.ErrNotOffered},
		"link": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: w.on.ID.String(), Purpose: "link"}
		}, want: sso.ErrNotOffered},
		"SAML": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: w.st.ps[2].ID.String()}
		}, want: sso.ErrNotOffered},
		"disabled": {in: func(w *signInWorld) sso.Start { return sso.Start{Organization: "acme", Provider: w.off.ID.String()} }, want: sso.ErrNotOffered},
		"enable without a session": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: w.off.ID.String(), Purpose: "enable"}
		}, want: sso.ErrForbidden},
		"enable by a member": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: w.off.ID.String(), Purpose: "enable", By: w.principal(kernel.PrincipalUser, member)}
		}, want: sso.ErrForbidden},
		"enable by another organization's admin": {in: func(w *signInWorld) sso.Start {
			by := w.principal(kernel.PrincipalUser, w.admin)
			by.OrganizationID = kernel.OrganizationID{ULID: kernel.NewULID()}
			return sso.Start{Organization: "acme", Provider: w.off.ID.String(), Purpose: "enable", By: by}
		}, want: sso.ErrForbidden},
		"reauthenticate without a session": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: w.on.ID.String(), Purpose: "reauthenticate"}
		}, want: sso.ErrForbidden},
		"reauthenticate as break-glass": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: w.on.ID.String(), Purpose: "reauthenticate", By: w.principal(kernel.PrincipalBreakGlass, kernel.MemberID{})}
		}, want: sso.ErrForbidden},
		"reauthenticate through a disabled provider": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: w.off.ID.String(), Purpose: "reauthenticate", By: w.principal(kernel.PrincipalUser, member)}
		}, want: sso.ErrNotOffered},
		"another host": {in: func(w *signInWorld) sso.Start {
			return sso.Start{Organization: "acme", Provider: w.on.ID.String(), ReturnTo: "https://evil.test/"}
		}, want: sso.ErrNotOffered},
		"store down": {
			in:     func(w *signInWorld) sso.Start { return sso.Start{Organization: "acme", Provider: w.on.ID.String()} },
			change: func(w *signInWorld) { w.st.fail = "CreateSignInRequest" }, want: errDown,
		},
		"provider unreachable": {
			in:     func(w *signInWorld) sso.Start { return sso.Start{Organization: "acme", Provider: w.on.ID.String()} },
			change: func(w *signInWorld) { w.party.discoverErr = errDown }, want: sso.ErrProvider,
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newSignInWorld(t)
			if c.change != nil {
				c.change(w)
			}
			if _, err := w.svc.Start(context.Background(), c.in(w)); !errors.Is(err, c.want) {
				t.Errorf("Start = %v, want %v", err, c.want)
			}
			if !errors.Is(c.want, errDown) && len(w.st.reqs) != 0 {
				t.Errorf("a refused start stored %+v", w.st.reqs)
			}
		})
	}
	w := newSignInWorld(t)
	w.feat.refused = []string{"oidc-sso"}
	_, err := w.svc.Start(context.Background(), sso.Start{Organization: "acme", Provider: w.on.ID.String()})
	if r, ok := errors.AsType[*featuresapi.Refusal](err); !ok || r.Feature != "oidc-sso" {
		t.Errorf("a tier without oidc-sso: %v", err)
	}
	// No features checks none.
	w.svc = sso.NewSignIn(sso.SignInDeps{
		Store: w.st, Party: w.party, Admins: admins{}, PublicURL: "https://iw.test", Now: func() time.Time { return t0 },
	})
	if _, err := w.svc.Start(context.Background(), sso.Start{Organization: "acme", Provider: w.on.ID.String()}); err != nil {
		t.Errorf("no features: %v", err)
	}
}

func TestUnitSignInCallback(t *testing.T) {
	ctx := context.Background()
	w := newSignInWorld(t)
	q, cookie := w.started(t, sso.Start{ReturnTo: "/stacks"})
	in, err := w.svc.Callback(ctx, sso.Callback{State: q.Get("state"), Code: "code-1", Cookie: cookie, IP: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if w.party.code != "code-1" || w.party.pkce != w.st.reqs[0].Verifier || w.party.client != (sso.Client{
		ID: "client", Secret: "client-secret", RedirectURI: "https://iw.test/auth/oidc/callback",
	}) {
		t.Errorf("exchanged %q, %q with %+v", w.party.code, w.party.pkce, w.party.client)
	}
	rec := w.st.recs[0]
	ss := in.Session
	if in.ReturnTo != "/stacks" || in.Secret == "" || !slices.Equal(ss.SecretHash, session.HashSecret(in.Secret)) ||
		rec.IDToken != "raw-id-token" || rec.Enable != nil || !rec.Member.IsZero() ||
		rec.Identity.Subject != "alice-sub" || rec.Provider.ID != w.on.ID {
		t.Errorf("signed in %+v from %+v", in, rec)
	}
	if ss.Channel != session.ChannelWeb || ss.ActorKind != kernel.PrincipalUser || !ss.AccountID.IsZero() ||
		ss.Login != "alice" || !slices.Equal(ss.Teams, []string{"dev"}) || !slices.Equal(ss.OriginalTeams, []string{"dev"}) ||
		!ss.OriginalTeamsKnown || ss.OriginalMember == nil || !*ss.OriginalMember || ss.IdentityProviderID != w.on.ID ||
		ss.IDPSubject == nil || *ss.IDPSubject != "alice-sub" || ss.IDPSID != "sid-1" || ss.IPAddress != "192.0.2.1" ||
		!ss.ExpiresAt.Equal(t0.Add(10*time.Hour)) || !ss.IdleExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("session %+v", ss)
	}
	if _, err := w.svc.Callback(ctx, sso.Callback{State: q.Get("state"), Code: "code-1", Cookie: cookie}); !errors.Is(err, sso.ErrRequest) {
		t.Errorf("a second callback: %v", err)
	}
}

func TestUnitSignInCallbackPurposes(t *testing.T) {
	ctx := context.Background()
	w := newSignInWorld(t)
	by := w.principal(kernel.PrincipalUser, w.admin)
	q, cookie := w.started(t, sso.Start{Purpose: "enable", Provider: w.off.ID.String(), By: by})
	w.party.claims = func() map[string]any {
		return map[string]any{"sub": "root-sub", "aud": "entra-client", "nonce": w.st.reqs[len(w.st.reqs)-1].Nonce, "preferred_username": "root", "auth_time": float64(t0.Unix())}
	}
	if _, err := w.svc.Callback(ctx, sso.Callback{State: q.Get("state"), Code: "c", Cookie: cookie, IP: "192.0.2.9"}); err != nil {
		t.Fatal(err)
	}
	e := w.st.recs[0].Enable
	var b map[string]any
	if e == nil || e.Action != sso.ActionEnable || e.AffectedResource.ID != w.off.ID.String() || e.RemoteIP != "192.0.2.9" ||
		json.Unmarshal(e.Body, &b) != nil || b["adminLogin"] != "root" {
		t.Errorf("enable entry %+v", e)
	}

	member := w.principal(kernel.PrincipalUser, kernel.NewMemberID())
	q, cookie = w.started(t, sso.Start{Purpose: "reauthenticate", By: member})
	if _, err := w.svc.Callback(ctx, sso.Callback{State: q.Get("state"), Cookie: cookie, By: member}); err != nil {
		t.Fatal(err)
	}
	if got := w.st.recs[1].Member; got != member.MemberID {
		t.Errorf("re-authentication's member %v, want %v", got, member.MemberID)
	}
	q, cookie = w.started(t, sso.Start{Purpose: "reauthenticate", By: member})
	if _, err := w.svc.Callback(ctx, sso.Callback{State: q.Get("state"), Cookie: cookie}); !errors.Is(err, sso.ErrForbidden) {
		t.Errorf("a re-authentication's callback without the session: %v", err)
	}
}

func TestUnitSignInCallbackRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		change func(*signInWorld, *sso.Callback)
		want   error
	}{
		"no state":         {change: func(_ *signInWorld, in *sso.Callback) { in.State = "" }, want: sso.ErrRequest},
		"no cookie":        {change: func(_ *signInWorld, in *sso.Callback) { in.Cookie = "" }, want: sso.ErrRequest},
		"another browser":  {change: func(_ *signInWorld, in *sso.Callback) { in.Cookie = "other" }, want: sso.ErrRequest},
		"another state":    {change: func(_ *signInWorld, in *sso.Callback) { in.State = "other" }, want: sso.ErrRequest},
		"store down":       {change: func(w *signInWorld, _ *sso.Callback) { w.st.fail = "ConsumeSignInRequest" }, want: errDown},
		"provider down":    {change: func(w *signInWorld, _ *sso.Callback) { w.st.fail = "Provider" }, want: errDown},
		"provider deleted": {change: func(w *signInWorld, _ *sso.Callback) { w.st.ps = w.st.ps[1:] }, want: sso.ErrNotOffered},
		"provider disabled": {change: func(w *signInWorld, _ *sso.Callback) {
			w.st.ps[0].EnabledAt = time.Time{}
		}, want: sso.ErrNotOffered},
		"provider's error":   {change: func(_ *signInWorld, in *sso.Callback) { in.Error = "access_denied" }, want: sso.ErrProvider},
		"discovery fails":    {change: func(w *signInWorld, _ *sso.Callback) { w.party.discoverErr = errDown }, want: sso.ErrProvider},
		"secret unreadable":  {change: func(w *signInWorld, _ *sso.Callback) { w.st.fail = "ProviderSecret" }, want: errDown},
		"exchange fails":     {change: func(w *signInWorld, _ *sso.Callback) { w.party.exchangeErr = errDown }, want: sso.ErrProvider},
		"token unverifiable": {change: func(w *signInWorld, _ *sso.Callback) { w.party.verifyErr = errDown }, want: sso.ErrToken},
		"wrong nonce": {change: func(w *signInWorld, _ *sso.Callback) {
			w.party.claims = func() map[string]any { return map[string]any{"sub": "s", "nonce": "other"} }
		}, want: sso.ErrToken},
		"no mapped subject": {change: func(w *signInWorld, _ *sso.Callback) {
			w.st.ps[0].Settings.ClaimMapping = []sso.ClaimMappingEntry{{Name: "subject", Value: "oid"}}
		}, want: sso.ErrToken},
		"seats full": {change: func(w *signInWorld, _ *sso.Callback) {
			w.st.signErr = &featuresapi.LimitError{Limit: "seats"}
		}, want: &featuresapi.LimitError{}},
	} {
		t.Run(name, func(t *testing.T) {
			w := newSignInWorld(t)
			q, cookie := w.started(t, sso.Start{})
			in := sso.Callback{State: q.Get("state"), Code: "c", Cookie: cookie}
			c.change(w, &in)
			_, err := w.svc.Callback(context.Background(), in)
			le := &featuresapi.LimitError{}
			if errors.As(c.want, &le) {
				if _, ok := errors.AsType[*featuresapi.LimitError](err); !ok {
					t.Errorf("Callback = %v, want %T", err, le)
				}
			} else if !errors.Is(err, c.want) {
				t.Errorf("Callback = %v, want %v", err, c.want)
			}
			if len(w.st.recs) != 0 {
				t.Errorf("a refused callback signed in %+v", w.st.recs)
			}
		})
	}
}

func TestUnitSignInOrganization(t *testing.T) {
	ctx := context.Background()
	w := newSignInWorld(t)
	org, ps, err := w.svc.Organization(ctx, "acme")
	if err != nil || org.ID != w.st.org.ID || len(ps) != 1 || ps[0].ID != w.on.ID {
		t.Errorf("Organization = %+v, %+v, %v", org, ps, err)
	}
	if _, _, err := w.svc.Organization(ctx, "nope"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown organization: %v", err)
	}
	w.st.fail = "Providers"
	if _, _, err := w.svc.Organization(ctx, "acme"); !errors.Is(err, errDown) {
		t.Errorf("providers down: %v", err)
	}
	w.st.fail, w.st.sole = "", true
	if slug, ok, err := w.svc.SoleOrganization(ctx); slug != "acme" || !ok || err != nil {
		t.Errorf("SoleOrganization = %q, %t, %v", slug, ok, err)
	}
	if err := w.svc.Expire(ctx); err != nil || !w.st.expired.Equal(t0) {
		t.Errorf("Expire = %v, before %v", err, w.st.expired)
	}
}

func TestUnitSignInLogoutURL(t *testing.T) {
	ctx := context.Background()
	w := newSignInWorld(t)
	ss := session.Session{ID: kernel.NewULID(), OrganizationID: w.st.org.ID, IdentityProviderID: w.on.ID}
	w.st.tokens[ss.ID] = "raw-id-token"
	u, err := url.Parse(w.svc.LogoutURL(ctx, ss))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "idp.test" || u.Path != "/logout" || q.Get("x") != "1" || q.Get("id_token_hint") != "raw-id-token" ||
		q.Get("client_id") != "client" || q.Get("post_logout_redirect_uri") != "https://iw.test/" {
		t.Errorf("LogoutURL = %s", u)
	}
	delete(w.st.tokens, ss.ID)
	if u := w.svc.LogoutURL(ctx, ss); strings.Contains(u, "id_token_hint") || u == "" {
		t.Errorf("no ID token: %s", u)
	}
	for name, change := range map[string]func(*signInWorld, *session.Session){
		"no provider":       func(_ *signInWorld, ss *session.Session) { ss.IdentityProviderID = kernel.ULID{} },
		"provider gone":     func(_ *signInWorld, ss *session.Session) { ss.IdentityProviderID = kernel.NewULID() },
		"not OIDC":          func(w *signInWorld, ss *session.Session) { ss.IdentityProviderID = w.st.ps[2].ID },
		"unreachable":       func(w *signInWorld, _ *session.Session) { w.party.discoverErr = errDown },
		"no end session":    func(w *signInWorld, _ *session.Session) { w.party.ep.EndSession = "" },
		"malformed end URL": func(w *signInWorld, _ *session.Session) { w.party.ep.EndSession = "https://idp.test/%zz" },
	} {
		w := newSignInWorld(t)
		ss := session.Session{ID: kernel.NewULID(), OrganizationID: w.st.org.ID, IdentityProviderID: w.on.ID}
		change(w, &ss)
		if u := w.svc.LogoutURL(ctx, ss); u != "" {
			t.Errorf("%s: LogoutURL = %s", name, u)
		}
	}
}

// jwt is an unsigned JWT of claims, which the party fake verifies as told.
func jwt(claims map[string]any) string {
	b, _ := json.Marshal(claims) //nolint:errchkjson // strings only
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

func TestUnitBackchannelLogout(t *testing.T) {
	ctx := context.Background()
	w := newSignInWorld(t)
	raw := jwt(map[string]any{"iss": "https://idp.test", "aud": "client"})
	w.party.logout = sso.LogoutToken{ID: "jti-1", SID: "sid-1", Subject: "alice-sub", Expiry: t0.Add(time.Minute)}
	if err := w.svc.BackchannelLogout(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.st.revoked, []string{"sid-1|alice-sub"}) || len(w.dropped) != 1 {
		t.Errorf("revoked %v, dropped %v", w.st.revoked, w.dropped)
	}
	if err := w.svc.BackchannelLogout(ctx, raw); !errors.Is(err, sso.ErrLogoutToken) {
		t.Errorf("a replay: %v", err)
	}
	for name, c := range map[string]struct {
		raw    string
		change func(*signInWorld)
		want   error
	}{
		"not a JWT":       {raw: "x.y", want: sso.ErrLogoutToken},
		"not base64":      {raw: "a.!!.c", want: sso.ErrLogoutToken},
		"not JSON":        {raw: "a." + base64.RawURLEncoding.EncodeToString([]byte("[")) + ".c", want: sso.ErrLogoutToken},
		"no aud":          {raw: jwt(map[string]any{"iss": "https://idp.test"}), want: sso.ErrLogoutToken},
		"unknown client":  {raw: jwt(map[string]any{"iss": "https://idp.test", "aud": []any{"nobody"}}), want: sso.ErrLogoutToken},
		"another issuer":  {raw: jwt(map[string]any{"iss": "https://evil.test", "aud": "client"}), want: sso.ErrLogoutToken},
		"unreachable":     {raw: raw, change: func(w *signInWorld) { w.party.discoverErr = errDown }, want: sso.ErrLogoutToken},
		"unverifiable":    {raw: raw, change: func(w *signInWorld) { w.party.logoutErr = errDown }, want: sso.ErrLogoutToken},
		"providers down":  {raw: raw, change: func(w *signInWorld) { w.st.fail = "ProvidersByClientID" }, want: errDown},
		"jtis down":       {raw: raw, change: func(w *signInWorld) { w.st.fail = "AcceptLogoutToken" }, want: errDown},
		"revocation down": {raw: raw, change: func(w *signInWorld) { w.st.fail = "RevokeIdentitySessions" }, want: errDown},
	} {
		t.Run(name, func(t *testing.T) {
			w := newSignInWorld(t)
			w.party.logout = sso.LogoutToken{ID: "jti-1", Subject: "alice-sub", Expiry: t0}
			if c.change != nil {
				c.change(w)
			}
			if err := w.svc.BackchannelLogout(ctx, c.raw); !errors.Is(err, c.want) {
				t.Errorf("BackchannelLogout = %v, want %v", err, c.want)
			}
			if errors.Is(c.want, sso.ErrLogoutToken) && len(w.st.revoked) != 0 {
				t.Errorf("a refused token revoked %v", w.st.revoked)
			}
		})
	}
}
