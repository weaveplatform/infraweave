package http

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// ssoStore is sso.SignInStore over the organization acme, whose providers
// are ps. The sessions a sign-in creates go to the world's store, so the
// browser's cookie authenticates.
type ssoStore struct {
	w        *world
	org      organization.Organization
	sole     bool
	ps       []sso.Provider
	reqs     []sso.Request
	consumed map[kernel.ULID]bool
	tokens   map[kernel.ULID]string
	jtis     map[string]bool
	revoked  int
	signErr  error
	down     bool
}

func (s *ssoStore) err() error {
	if s.down {
		return errDown
	}
	return nil
}

func (s *ssoStore) SignInOrganization(_ context.Context, slug kernel.Slug) (organization.Organization, error) {
	if slug != s.org.Slug {
		return organization.Organization{}, port.ErrNotFound
	}
	return s.org, s.err()
}

func (s *ssoStore) SoleOrganization(context.Context) (kernel.Slug, bool, error) {
	return s.org.Slug, s.sole, s.err()
}

func (s *ssoStore) Providers(context.Context, kernel.OrganizationID) ([]sso.Provider, error) {
	return slices.Clone(s.ps), s.err()
}

func (s *ssoStore) Provider(_ context.Context, _ kernel.OrganizationID, id kernel.ULID) (sso.Provider, error) {
	for _, p := range s.ps {
		if p.ID == id {
			return p, s.err()
		}
	}
	return sso.Provider{}, port.ErrNotFound
}

func (s *ssoStore) ProviderSecret(context.Context, kernel.OrganizationID, kernel.ULID) (crypt.Value, error) {
	return crypt.NewValue([]byte("secret")), nil
}

func (s *ssoStore) CreateSignInRequest(_ context.Context, r sso.Request) error {
	s.reqs = append(s.reqs, r)
	return s.err()
}

func (s *ssoStore) ConsumeSignInRequest(_ context.Context, state, browser []byte) (sso.Request, error) {
	for _, r := range s.reqs {
		if slices.Equal(r.StateHash, state) && slices.Equal(r.BrowserHash, browser) && !s.consumed[r.ID] {
			s.consumed[r.ID] = true
			return r, nil
		}
	}
	return sso.Request{}, port.ErrNotFound
}

func (s *ssoStore) SignInIdentity(_ context.Context, rec sso.SignInRecord) (session.Session, error) {
	if s.signErr != nil {
		return session.Session{}, s.signErr
	}
	ss := rec.Session
	ss.MemberID = kernel.NewMemberID()
	ss.ActorID = ss.MemberID.ULID
	s.w.st.sessions[ss.ID] = ss
	s.tokens[ss.ID] = rec.IDToken
	return ss, nil
}

func (s *ssoStore) ProvidersByClientID(context.Context, []string) ([]sso.Provider, error) {
	return slices.Clone(s.ps), s.err()
}

func (s *ssoStore) AcceptLogoutToken(_ context.Context, _ kernel.ULID, jti string, _ time.Time) (bool, error) {
	fresh := !s.jtis[jti]
	s.jtis[jti] = true
	return fresh, nil
}

func (s *ssoStore) RevokeIdentitySessions(context.Context, kernel.ULID, string, string) ([]kernel.ULID, error) {
	s.revoked++
	return nil, nil
}

func (s *ssoStore) SessionIDToken(_ context.Context, id kernel.ULID) (crypt.Value, bool, error) {
	tok, ok := s.tokens[id]
	return crypt.NewValue([]byte(tok)), ok, nil
}

func (s *ssoStore) ExpireSignIns(context.Context, time.Time) (int64, error) { return 0, nil }

// relyingParty answers the nonce of the latest request in its claims.
type relyingParty struct {
	st        *ssoStore
	verifyErr error
}

func (p *relyingParty) Discover(context.Context, string) (sso.Endpoints, error) {
	return sso.Endpoints{
		Issuer: "https://idp.test", Authorization: "https://idp.test/authorize",
		Token: "https://idp.test/token", EndSession: "https://idp.test/logout",
	}, nil
}

func (p *relyingParty) Exchange(context.Context, sso.Endpoints, sso.Client, string, string) (string, error) {
	return "id-token", nil
}

func (p *relyingParty) VerifyIDToken(context.Context, string, string, string) (map[string]any, error) {
	if p.verifyErr != nil {
		return nil, p.verifyErr
	}
	return map[string]any{
		"sub": "alice-sub", "aud": "client", "nonce": p.st.reqs[len(p.st.reqs)-1].Nonce,
		"preferred_username": "alice",
	}, nil
}

func (p *relyingParty) VerifyLogout(context.Context, string, string, string) (sso.LogoutToken, error) {
	return sso.LogoutToken{ID: "jti-1", SID: "sid-1", Expiry: t0.Add(time.Minute)}, nil
}

// ssoAdmins admit the break-glass session alone.
type ssoAdmins struct{}

func (ssoAdmins) Admit(_ context.Context, by orgadmin.Actor) error {
	if by.Principal.Kind != kernel.PrincipalBreakGlass {
		return orgadmin.ErrForbidden
	}
	return nil
}

// newSignIn gives w an organization acme with one enabled OIDC provider
// and the sign-in over it.
func (w *world) newSignIn(auth *authn.Service) *sso.SignIn {
	org := organization.Organization{ID: kernel.NewOrganizationID(), Slug: "acme", Name: "Acme"}
	w.sso = &ssoStore{
		w: w, org: org, consumed: map[kernel.ULID]bool{}, tokens: map[kernel.ULID]string{},
		jtis: map[string]bool{},
		ps: []sso.Provider{{
			OrganizationID: org.ID, ID: kernel.NewULID(), Kind: sso.OIDC, Name: "Okta",
			Settings:  sso.Settings{IdentityProviderHost: "https://idp.test", ClientID: "client"},
			EnabledAt: t0, AdminLogin: "root",
		}},
	}
	w.party = &relyingParty{st: w.sso}
	return sso.NewSignIn(sso.SignInDeps{
		Store: w.sso, Party: w.party, Admins: ssoAdmins{}, Drop: auth.Cache().Drop,
		PublicURL: publicURL, Idle: idle, MaxAge: maxAge, Now: func() time.Time { return t0 },
	})
}

// answer is a recorded answer: its status, header and body.
type answer struct {
	StatusCode int
	Header     http.Header
	Body       string
}

func answerOf(rec *httptest.ResponseRecorder) answer {
	return answer{StatusCode: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
}

// Cookies are the cookies the answer sets.
func (a answer) Cookies() []*http.Cookie {
	return (&http.Response{Header: a.Header}).Cookies()
}

// get sends GET target with the cookies.
func (w *world) get(t *testing.T, target string, cookies ...*http.Cookie) answer {
	t.Helper()
	return answerOf(w.send(t, http.MethodGet, target, "", func(req *http.Request) {
		for _, c := range cookies {
			req.AddCookie(c)
		}
	}))
}

func cookieOf(t *testing.T, res answer, name string) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", name, res.Header["Set-Cookie"])
	return nil
}

// TestUnitSignInPages checks the organization's choice and its providers'
// list: each sends the browser on when there is one, and asks otherwise.
func TestUnitSignInPages(t *testing.T) {
	w := newWorld(t)
	start := "/auth/login/acme/" + w.sso.ps[0].ID.String()
	for target, loc := range map[string]string{
		"/auth/login?organization=acme":                     "/auth/login/acme",
		"/auth/login?organization=acme&return_to=%2Fstacks": "/auth/login/acme?return_to=%2Fstacks",
		"/auth/login/acme":                                  start,
		"/auth/login/acme?return_to=%2Fstacks":              start + "?return_to=%2Fstacks",
	} {
		res := w.get(t, target)
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != loc {
			t.Errorf("GET %s: %d to %q, want %q", target, res.StatusCode, res.Header.Get("Location"), loc)
		}
	}
	res := w.get(t, "/auth/login?return_to=/stacks")
	body := res.Body
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `name="organization"`) ||
		!strings.Contains(body, `value="/stacks"`) || res.Header.Get("Cache-Control") != "no-store" ||
		!strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("the organization's choice: %d %v %s", res.StatusCode, res.Header, body)
	}
	w.sso.sole = true
	if res := w.get(t, "/auth/login"); res.Header.Get("Location") != "/auth/login/acme" {
		t.Errorf("one organization: %d %q", res.StatusCode, res.Header.Get("Location"))
	}

	second := w.sso.ps[0]
	second.ID, second.Name = kernel.NewULID(), "Entra <prod>"
	disabled := second
	disabled.ID, disabled.EnabledAt = kernel.NewULID(), time.Time{}
	w.sso.ps = append(w.sso.ps, second, disabled)
	res = w.get(t, "/auth/login/acme")
	body = res.Body
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `href="`+start+`"`) ||
		!strings.Contains(body, "Entra &lt;prod&gt;") || strings.Contains(body, disabled.ID.String()) {
		t.Errorf("two providers: %d %s", res.StatusCode, body)
	}
	w.sso.ps = nil
	if res := w.get(t, "/auth/login/acme"); res.StatusCode != http.StatusOK ||
		!strings.Contains(res.Body, "no identity provider") {
		t.Errorf("no provider: %d", res.StatusCode)
	}
	if res := w.get(t, "/auth/login/other"); res.StatusCode != http.StatusNotFound ||
		!strings.Contains(res.Body, "not-found") {
		t.Errorf("another organization: %d", res.StatusCode)
	}
	w.sso.down, w.sso.sole = true, false
	for _, target := range []string{"/auth/login", "/auth/login/acme"} {
		if res := w.get(t, target); res.StatusCode != http.StatusServiceUnavailable ||
			!strings.Contains(res.Body, "unavailable") {
			t.Errorf("GET %s, the store down: %d", target, res.StatusCode)
		}
	}
}

// TestUnitSignInFlow starts a sign-in, finishes it, and logs out: the
// start binds the request to the browser with __Host-infraweave_sso, the
// callback sets the session's cookie and clears the request's, and the
// logout answers the provider's end-session URL.
func TestUnitSignInFlow(t *testing.T) {
	w := newWorld(t)
	res := w.get(t, "/auth/login/acme/"+w.sso.ps[0].ID.String()+"?return_to=%2Fstacks")
	loc, err := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || err != nil || loc.Host != "idp.test" {
		t.Fatalf("start: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	bind := cookieOf(t, res, sso.RequestCookie)
	h := sha256.Sum256([]byte(bind.Value))
	if !bind.HttpOnly || !bind.Secure || bind.SameSite != http.SameSiteLaxMode || bind.Path != "/" ||
		bind.MaxAge != int(sso.RequestTTL.Seconds()) || !slices.Equal(w.sso.reqs[0].BrowserHash, h[:]) {
		t.Errorf("request cookie %+v", bind)
	}
	state := loc.Query().Get("state")

	// Another browser's callback is refused, and the request is still
	// there for its own.
	res = w.get(t, "/auth/oidc/callback?code=c&state="+state, &http.Cookie{Name: sso.RequestCookie, Value: "other"})
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(res.Body, "invalid-request") ||
		cookieOf(t, res, sso.RequestCookie).MaxAge >= 0 {
		t.Errorf("another browser: %d", res.StatusCode)
	}
	res = w.get(t, "/auth/oidc/callback?code=c&state="+state, bind)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/stacks" {
		t.Fatalf("callback: %d %q %s", res.StatusCode, res.Header.Get("Location"), res.Body)
	}
	if cookieOf(t, res, sso.RequestCookie).MaxAge >= 0 {
		t.Error("the callback kept the request's cookie")
	}
	sess := cookieOf(t, res, session.CookieName)
	if !sess.HttpOnly || !sess.Secure || sess.Value == "" {
		t.Errorf("session cookie %+v", sess)
	}
	if res := w.get(t, "/auth/oidc/callback?code=c&state="+state, bind); res.StatusCode != http.StatusBadRequest {
		t.Errorf("a second callback: %d", res.StatusCode)
	}

	out := decode[api.Session](t, w.browse(t, http.MethodPost, "/auth/logout", sess.Value, publicURL, ""))
	end, err := url.Parse(deref(out.LogoutUrl))
	if err != nil || end.Host != "idp.test" || end.Query().Get("id_token_hint") != "id-token" ||
		end.Query().Get("post_logout_redirect_uri") != publicURL {
		t.Errorf("logoutUrl %v", out.LogoutUrl)
	}
	// A session of no provider answers none.
	_, secret := w.member(w.acct)
	if out := decode[api.Session](t, w.browse(t, http.MethodPost, "/auth/logout", secret, publicURL, "")); out.LogoutUrl != nil {
		t.Errorf("a session of no provider: logoutUrl %q", *out.LogoutUrl)
	}
}

// TestUnitSignInStartPurposes checks that an enable needs an organization
// admin's browser session, and that a cookie naming no session is none.
func TestUnitSignInStartPurposes(t *testing.T) {
	w := newWorld(t)
	p := w.sso.ps[0]
	enable := "/auth/login/acme/" + p.ID.String() + "?purpose=enable"
	if res := w.get(t, enable); res.StatusCode != http.StatusForbidden || !strings.Contains(res.Body, "forbidden") {
		t.Errorf("enable without a session: %d", res.StatusCode)
	}
	if res := w.get(t, enable, &http.Cookie{Name: session.CookieName, Value: "nobody"}); res.StatusCode != http.StatusForbidden {
		t.Errorf("enable with a stale cookie: %d", res.StatusCode)
	}
	secret, hash := session.NewSecret()
	bg := session.Session{
		ID: kernel.NewULID(), OrganizationID: p.OrganizationID, Channel: session.ChannelWeb,
		ActorKind: kernel.PrincipalBreakGlass, ActorID: p.OrganizationID.ULID, Login: "admin",
		CreatedAt: t0, ExpiresAt: t0.Add(maxAge), IdleExpiresAt: t0.Add(idle), SecretHash: hash,
	}
	w.st.sessions[bg.ID] = bg
	if res := w.get(t, enable, &http.Cookie{Name: session.CookieName, Value: secret}); res.StatusCode != http.StatusFound {
		t.Errorf("enable by the break-glass admin: %d %s", res.StatusCode, res.Body)
	}
	if res := w.get(t, "/auth/login/acme/"+p.ID.String()+"?purpose=link"); res.StatusCode != http.StatusNotFound {
		t.Errorf("link: %d", res.StatusCode)
	}
	w.st.down = true
	if res := w.get(t, enable, &http.Cookie{Name: session.CookieName, Value: "x"}); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("sessions down: %d", res.StatusCode)
	}
	if res := w.get(t, "/auth/oidc/callback?state=s", &http.Cookie{Name: session.CookieName, Value: "x"}); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("sessions down at the callback: %d", res.StatusCode)
	}
}

// TestUnitBackchannelLogout checks the back-channel logout's answers: 200
// for a token it accepts, 400 invalid_request for one it refuses, a replay
// among them, and 503 while the store is down.
func TestUnitBackchannelLogout(t *testing.T) {
	w := newWorld(t)
	payload, _ := json.Marshal(map[string]any{"iss": "https://idp.test", "aud": "client"})
	raw := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	post := func(body string) answer {
		return answerOf(w.send(t, http.MethodPost, "/auth/oidc/backchannel-logout", body, func(req *http.Request) {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}))
	}
	if res := post("logout_token=" + raw); res.StatusCode != http.StatusOK || w.sso.revoked != 1 ||
		res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("a logout: %d, revoked %d", res.StatusCode, w.sso.revoked)
	}
	for name, body := range map[string]string{
		"a replay": "logout_token=" + raw, "no token": "", "not a form": "%zz",
	} {
		res := post(body)
		var e map[string]string
		if res.StatusCode != http.StatusBadRequest || json.Unmarshal([]byte(res.Body), &e) != nil ||
			e["error"] != "invalid_request" {
			t.Errorf("%s: %d %v", name, res.StatusCode, e)
		}
	}
	w.sso.down = true
	if res := post("logout_token=" + raw); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("the store down: %d", res.StatusCode)
	}
}

// TestUnitSignInRefusals checks each refusal's page: its status and code.
func TestUnitSignInRefusals(t *testing.T) {
	for err, want := range map[error]refusal{
		sso.ErrRequest:                            {status: http.StatusBadRequest, code: "invalid-request"},
		sso.ErrToken:                              {status: http.StatusUnauthorized, code: "unauthenticated"},
		sso.ErrDeactivated:                        {status: http.StatusForbidden, code: "member-deactivated"},
		sso.ErrOtherMember:                        {status: http.StatusForbidden, code: "other-member"},
		sso.ErrForbidden:                          {status: http.StatusForbidden, code: "forbidden"},
		&featuresapi.LimitError{Limit: "seats"}:   {status: http.StatusForbidden, code: "limit-reached"},
		&featuresapi.Refusal{Feature: "oidc-sso"}: {status: http.StatusForbidden, code: "feature-not-in-tier"},
		sso.ErrNotOffered:                         {status: http.StatusNotFound, code: "not-found"},
		port.ErrNotFound:                          {status: http.StatusNotFound, code: "not-found"},
		sso.ErrProvider:                           {status: http.StatusBadGateway, code: "identity-provider-failed"},
		errDown:                                   {status: http.StatusServiceUnavailable, code: "unavailable"},
		errors.New("boom"):                        {status: http.StatusInternalServerError, code: "internal"},
		fmt.Errorf("wrapped: %w", sso.ErrDeactivated): {status: http.StatusForbidden, code: "member-deactivated"},
	} {
		if got := refusalOf(err); got.status != want.status || got.code != want.code || got.message == "" {
			t.Errorf("refusalOf(%v) = %+v, want %+v", err, got, want)
		}
	}
	w := newWorld(t)
	w.party.verifyErr = errors.New("bad signature")
	res := w.get(t, "/auth/login/acme/"+w.sso.ps[0].ID.String())
	bind := cookieOf(t, res, sso.RequestCookie)
	loc, _ := url.Parse(res.Header.Get("Location"))
	res = w.get(t, "/auth/oidc/callback?code=c&state="+loc.Query().Get("state"), bind)
	if body := res.Body; res.StatusCode != http.StatusUnauthorized || strings.Contains(body, "bad signature") {
		t.Errorf("an unverifiable token: %d %s", res.StatusCode, body)
	}
	w.party.verifyErr = nil
	w.sso.signErr = &featuresapi.LimitError{Limit: "seats"}
	res = w.get(t, "/auth/login/acme/"+w.sso.ps[0].ID.String())
	bind = cookieOf(t, res, sso.RequestCookie)
	loc, _ = url.Parse(res.Header.Get("Location"))
	res = w.get(t, "/auth/oidc/callback?code=c&state="+loc.Query().Get("state"), bind)
	if body := res.Body; res.StatusCode != http.StatusForbidden || !strings.Contains(body, "limit-reached") {
		t.Errorf("seats full: %d %s", res.StatusCode, body)
	}
}
