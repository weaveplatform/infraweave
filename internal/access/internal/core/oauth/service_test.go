package oauth_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

const public = "https://iw.example.com"

// store is oauth.Store in memory, with the pg store's rules.
type store struct {
	mu       sync.Mutex
	clients  map[kernel.ULID]oauth.Client
	requests map[kernel.ULID]*oauth.Request
	// spentCodes are the redeemed requests, by request, with the grant
	// each issued.
	spentCodes map[kernel.ULID]kernel.ULID
	sessions   map[kernel.ULID]session.Session
	spent      map[string]kernel.ULID
	owners     map[kernel.AccountID]map[kernel.MemberID]oauth.Owner
	hosts      map[kernel.AccountID][]string
	fail       error
	expired    [2]time.Time
}

func newStore() *store {
	return &store{
		clients:    map[kernel.ULID]oauth.Client{},
		requests:   map[kernel.ULID]*oauth.Request{},
		spentCodes: map[kernel.ULID]kernel.ULID{},
		sessions:   map[kernel.ULID]session.Session{},
		spent:      map[string]kernel.ULID{},
		owners:     map[kernel.AccountID]map[kernel.MemberID]oauth.Owner{},
		hosts:      map[kernel.AccountID][]string{},
	}
}

func (s *store) CreateClient(_ context.Context, c oauth.Client) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.clients[c.ID] = c
	return nil
}

func (s *store) Client(_ context.Context, id kernel.ULID) (oauth.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return oauth.Client{}, s.fail
	}
	c, ok := s.clients[id]
	if !ok {
		return oauth.Client{}, port.ErrNotFound
	}
	return c, nil
}

func (s *store) CreateRequest(_ context.Context, r oauth.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[r.ID] = &r
	return nil
}

func (s *store) Request(_ context.Context, id kernel.ULID) (oauth.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.requests[id]
	if !ok {
		return oauth.Request{}, port.ErrNotFound
	}
	return *r, nil
}

func (s *store) Decide(_ context.Context, d oauth.Request, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.requests[d.ID]
	if !ok || r.Decided || !now.Before(r.ExpiresAt) {
		return port.ErrNotFound
	}
	r.Decided = true
	r.OrganizationID, r.AccountID, r.MemberID = d.OrganizationID, d.AccountID, d.MemberID
	r.CodeHash, r.CodeExpiresAt = d.CodeHash, d.CodeExpiresAt
	return nil
}

func (s *store) RedirectHosts(_ context.Context, acct kernel.AccountID) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hosts[acct], nil
}

func (s *store) owner(acct kernel.AccountID, m kernel.MemberID) (oauth.Owner, bool) {
	o, ok := s.owners[acct][m]
	return o, ok
}

func (s *store) revoke(id kernel.ULID) session.Session {
	ss := s.sessions[id]
	if ss.RevokedAt.IsZero() {
		ss.RevokedAt = time.Now()
	}
	s.sessions[id] = ss
	return ss
}

func (s *store) Redeem(
	_ context.Context,
	hash []byte,
	grant func(oauth.Request, oauth.Owner) (session.Session, error),
) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r *oauth.Request
	for _, x := range s.requests {
		if x.CodeHash != nil && hex.EncodeToString(x.CodeHash) == hex.EncodeToString(hash) {
			r = x
		}
	}
	if r == nil {
		return session.Session{}, port.ErrNotFound
	}
	if id, ok := s.spentCodes[r.ID]; ok {
		return s.revoke(id), oauth.ErrReused
	}
	o, ok := s.owner(r.AccountID, r.MemberID)
	if !ok {
		return session.Session{}, port.ErrNotFound
	}
	ss, err := grant(*r, o)
	if err != nil {
		return session.Session{}, err
	}
	s.sessions[ss.ID] = ss
	s.spentCodes[r.ID] = ss.ID
	return ss, nil
}

func (s *store) Refresh(
	_ context.Context,
	hash []byte,
	rotate func(session.Session, oauth.Owner) (session.Session, error),
) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hex.EncodeToString(hash)
	var cur session.Session
	for _, ss := range s.sessions {
		if hex.EncodeToString(ss.SecretHash) == key {
			cur = ss
		}
	}
	if cur.ID.IsZero() {
		if id, ok := s.spent[key]; ok {
			return s.revoke(id), oauth.ErrReused
		}
		return session.Session{}, port.ErrNotFound
	}
	o, ok := s.owner(cur.AccountID, cur.MemberID)
	if !ok {
		return s.revoke(cur.ID), port.ErrNotFound
	}
	next, err := rotate(cur, o)
	if err != nil {
		return session.Session{}, err
	}
	s.sessions[next.ID] = next
	s.spent[key] = cur.ID
	return next, nil
}

func (s *store) ExpireOAuth(_ context.Context, requests, clients time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expired = [2]time.Time{requests, clients}
	return nil
}

// issuer signs nothing: a token names its session, audience and scope.
type issuer struct{ fail error }

func (i issuer) Issue(_ context.Context, r token.Request) (string, token.Claims, error) {
	if i.fail != nil {
		return "", token.Claims{}, i.fail
	}
	now := time.Now().UTC().Truncate(time.Second)
	return fmt.Sprintf("%s|%s|%s|%s", r.Session, r.Audience, r.Scope, r.Subject), token.Claims{
		Session: r.Session, Audience: r.Audience, Scope: r.Scope, Kind: r.Kind,
		IssuedAt: now, Expiry: now.Add(r.TTL),
	}, nil
}

// admission admits by the store's owners.
type admission struct{ s *store }

func (a admission) Accounts(
	context.Context,
	kernel.OrganizationID,
	kernel.MemberID,
) ([]session.Admitted, error) {
	return nil, nil
}

func (a admission) Admits(
	_ context.Context,
	_ kernel.OrganizationID,
	m kernel.MemberID,
	acct kernel.AccountID,
) (session.Admitted, bool, error) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	_, ok := a.s.owner(acct, m)
	return session.Admitted{ID: acct}, ok, nil
}

// fixture is a service over a store holding one member admitted to one
// account, and a client registered with a loopback and an https redirect.
type fixture struct {
	svc     *oauth.Service
	st      *store
	dropped []kernel.ULID
	p       kernel.Principal
	acct    kernel.AccountID
	client  oauth.Client
}

const verifier = "a-verifier-of-at-least-forty-three-characters"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{st: newStore(), acct: kernel.AccountID{ULID: kernel.NewULID()}}
	f.p = kernel.Principal{
		Kind: kernel.PrincipalUser, OrganizationID: kernel.OrganizationID{ULID: kernel.NewULID()},
		MemberID: kernel.NewMemberID(), Login: "alice",
	}
	f.st.owners[f.acct] = map[kernel.MemberID]oauth.Owner{
		f.p.MemberID: {Login: "alice", Name: "Alice", Teams: []string{"dev"}},
	}
	f.svc = oauth.NewService(oauth.Deps{
		Store: f.st, Tokens: issuer{}, Admission: admission{f.st},
		Drop:      func(id kernel.ULID) { f.dropped = append(f.dropped, id) },
		PublicURL: public + "/", AccessTTL: time.Hour, RefreshTTL: 30 * 24 * time.Hour,
		CodeTTL: time.Minute, Now: time.Now,
	})
	c, err := f.svc.Register(t.Context(), oauth.Registration{
		RedirectURIs: []string{"http://127.0.0.1/callback", "https://app.example.com/cb"},
		ClientName:   "Editor",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.client = c
	return f
}

// params are a valid authorization request's, for the loopback redirect
// on port 49152.
func (f *fixture) params() oauth.AuthorizeParams {
	return oauth.AuthorizeParams{
		ResponseType: "code", ClientID: f.client.ID.String(),
		RedirectURI: "http://127.0.0.1:49152/callback", Scope: "mcp:write", State: "xyz",
		CodeChallenge: oauth.Challenge(verifier), CodeChallengeMethod: oauth.MethodS256,
		Resource: public + "/mcp",
	}
}

// request authorizes a and answers the stored request's ID.
func (f *fixture) request(t *testing.T, a oauth.AuthorizeParams) kernel.ULID {
	t.Helper()
	loc, err := f.svc.Authorize(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := strings.CutPrefix(loc, public+oauth.ConsentPath)
	if !ok {
		t.Fatalf("Authorize answered %s, not the consent page", loc)
	}
	id, err := kernel.ParseULID(raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// code authorizes and approves a valid request and answers its code.
func (f *fixture) code(t *testing.T) string {
	t.Helper()
	loc, err := f.svc.Approve(t.Context(), f.p, f.request(t, f.params()), f.acct)
	if err != nil {
		t.Fatal(err)
	}
	return query(t, loc).Get("code")
}

func query(t *testing.T, loc string) url.Values {
	t.Helper()
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func (f *fixture) redeem(t *testing.T, code string) (oauth.Tokens, error) {
	t.Helper()
	return f.svc.Token(t.Context(), oauth.TokenParams{
		GrantType: oauth.GrantAuthorizationCode, ClientID: f.client.ID.String(), Code: code,
		RedirectURI: "http://127.0.0.1:49152/callback", CodeVerifier: verifier,
	}, "192.0.2.1")
}

func (f *fixture) refresh(t *testing.T, rt string) (oauth.Tokens, error) {
	t.Helper()
	return f.svc.Token(t.Context(), oauth.TokenParams{
		GrantType: oauth.GrantRefreshToken, ClientID: f.client.ID.String(), RefreshToken: rt,
	}, "")
}

// isCode checks err is an *oauth.Error of code.
func isCode(t *testing.T, what string, err error, code string) {
	t.Helper()
	var e *oauth.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Errorf("%s: %v, want %s", what, err, code)
	}
}

func (f *fixture) grant(t *testing.T, tok oauth.Tokens) session.Session {
	t.Helper()
	id, err := kernel.ParseULID(strings.Split(tok.AccessToken, "|")[0])
	if err != nil {
		t.Fatal(err)
	}
	f.st.mu.Lock()
	defer f.st.mu.Unlock()
	return f.st.sessions[id]
}

func TestUnitRefreshRotation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		late := f.code(t)
		<-time.After(time.Minute)
		_, err := f.redeem(t, late)
		isCode(t, "a code a minute old", err, oauth.CodeInvalidGrant)

		first, err := f.redeem(t, f.code(t))
		if err != nil {
			t.Fatal(err)
		}
		g := f.grant(t, first)
		if first.TokenType != "Bearer" || first.ExpiresIn != 3600 ||
			first.Scope != "mcp:read mcp:write" || first.RefreshToken == "" ||
			!strings.HasSuffix(first.AccessToken, "|"+public+"/mcp|mcp:read mcp:write|alice") {
			t.Errorf("the code's tokens = %+v", first)
		}
		if g.MemberID != f.p.MemberID || g.AccountID != f.acct || g.ActorKind !=
			kernel.PrincipalUser || g.ActorID != f.p.MemberID.ULID ||
			g.Channel != session.ChannelUnknown || g.ClientID != f.client.ID.String() ||
			g.Resource != public+"/mcp" || g.Name != "Alice" ||
			!slices.Equal(g.Teams, []string{"dev"}) || g.IPAddress != "192.0.2.1" ||
			!slices.Equal(g.SecretHash, session.HashSecret(first.RefreshToken)) ||
			!g.ExpiresAt.Equal(time.Now().Add(30*24*time.Hour)) {
			t.Errorf("the grant = %+v", g)
		}

		<-time.After(29 * 24 * time.Hour)
		f.st.owners[f.acct][f.p.MemberID] = oauth.Owner{Login: "alice", Name: "Alice B"}
		second, err := f.refresh(t, first.RefreshToken)
		if err != nil {
			t.Fatal(err)
		}
		g = f.grant(t, second)
		if second.RefreshToken == first.RefreshToken || second.ExpiresIn != 3600 ||
			!g.ExpiresAt.Equal(time.Now().Add(30*24*time.Hour)) || g.Name != "Alice B" ||
			len(g.Teams) != 0 {
			t.Errorf("the refresh = %+v, grant %+v", second, g)
		}
		narrowed, err := f.svc.Token(t.Context(), oauth.TokenParams{
			GrantType: oauth.GrantRefreshToken, ClientID: f.client.ID.String(),
			RefreshToken: second.RefreshToken, Scope: "mcp:read", Resource: public + "/mcp",
		}, "")
		if err != nil || narrowed.Scope != "mcp:read" {
			t.Fatalf("a narrowed refresh = %+v, %v", narrowed, err)
		}
		if g := f.grant(t, narrowed); !slices.Equal(g.Scopes, []string{"mcp:read", "mcp:write"}) {
			t.Errorf("narrowing one token narrowed the grant: %v", g.Scopes)
		}

		_, err = f.refresh(t, first.RefreshToken)
		isCode(t, "a spent refresh token", err, oauth.CodeInvalidGrant)
		if g := f.grant(t, narrowed); g.RevokedAt.IsZero() ||
			!slices.Equal(f.dropped, []kernel.ULID{g.ID}) {
			t.Errorf("the spent token left the grant live: %+v, dropped %v", g, f.dropped)
		}
		_, err = f.refresh(t, narrowed.RefreshToken)
		isCode(t, "the revoked grant's current token", err, oauth.CodeInvalidGrant)

		// A grant that outlives its refresh lifetime unused ends.
		idle, err := f.redeem(t, f.code(t))
		if err != nil {
			t.Fatal(err)
		}
		<-time.After(30 * 24 * time.Hour)
		_, err = f.refresh(t, idle.RefreshToken)
		isCode(t, "a grant unused for 30 days", err, oauth.CodeInvalidGrant)
	})
}

func TestUnitOAuthServer(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	m := f.svc.Metadata()
	if m.Issuer != public || m.AuthorizationEndpoint != public+"/oauth/authorize" ||
		m.TokenEndpoint != public+"/oauth/token" ||
		m.RegistrationEndpoint != public+"/oauth/register" ||
		!slices.Equal(m.ResponseTypesSupported, []string{"code"}) ||
		!slices.Equal(m.GrantTypesSupported, []string{"authorization_code", "refresh_token"}) ||
		!slices.Equal(m.CodeChallengeMethodsSupported, []string{"S256"}) ||
		!slices.Equal(m.TokenEndpointAuthMethodsSupported, []string{"none"}) ||
		!slices.Equal(m.ScopesSupported, []string{"mcp:read", "mcp:write"}) {
		t.Errorf("metadata = %+v", m)
	}

	for name, r := range map[string]oauth.Registration{
		"no redirect URI":   {},
		"a plain http host": {RedirectURIs: []string{"http://app.example.com/cb"}},
		"too many redirect URIs": {
			RedirectURIs: slices.Repeat([]string{"http://localhost/cb"}, 11),
		},
		"a long redirect URI": {
			RedirectURIs: []string{"https://app.example.com/" + strings.Repeat("x", 2000)},
		},
	} {
		_, err := f.svc.Register(ctx, r)
		isCode(t, name, err, oauth.CodeInvalidRedirectURI)
	}
	ok := []string{"http://localhost/cb"}
	for name, r := range map[string]oauth.Registration{
		"a secret": {RedirectURIs: ok, TokenEndpointAuthMethod: "client_secret_basic"},
		"implicit": {RedirectURIs: ok, GrantTypes: []string{"implicit"}},
		"a token":  {RedirectURIs: ok, ResponseTypes: []string{"token"}},
		"a long name": {
			RedirectURIs: ok, ClientName: strings.Repeat("n", 201),
		},
	} {
		_, err := f.svc.Register(ctx, r)
		isCode(t, name, err, oauth.CodeInvalidClientMetadata)
	}
	full, err := f.svc.Register(ctx, oauth.Registration{
		RedirectURIs: ok, TokenEndpointAuthMethod: "none",
		GrantTypes:    []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"},
	})
	if err != nil || full.ID.IsZero() || !slices.Equal(full.RedirectURIs, ok) {
		t.Errorf("a full registration = %+v, %v", full, err)
	}

	// Refusals before the redirect URI is known good are shown, not sent.
	shown := map[string]func(*oauth.AuthorizeParams){
		"an unknown client":      func(a *oauth.AuthorizeParams) { a.ClientID = kernel.NewULID().String() },
		"a client_id not a ULID": func(a *oauth.AuthorizeParams) { a.ClientID = "x" },
		"an unregistered redirect": func(a *oauth.AuthorizeParams) {
			a.RedirectURI = "https://evil.example.com/cb"
		},
		"no redirect of a client with two": func(a *oauth.AuthorizeParams) { a.RedirectURI = "" },
	}
	for name, change := range shown {
		a := f.params()
		change(&a)
		loc, err := f.svc.Authorize(ctx, a)
		var e *oauth.Error
		if loc != "" || !errors.As(err, &e) {
			t.Errorf("%s: %s, %v", name, loc, err)
		}
	}
	sent := map[string]struct {
		change func(*oauth.AuthorizeParams)
		code   string
	}{
		"plain PKCE": {func(a *oauth.AuthorizeParams) {
			a.CodeChallenge, a.CodeChallengeMethod = verifier, "plain"
		}, oauth.CodeInvalidRequest},
		"no challenge": {
			func(a *oauth.AuthorizeParams) { a.CodeChallenge = "" },
			oauth.CodeInvalidRequest,
		},
		"a token response": {
			func(a *oauth.AuthorizeParams) { a.ResponseType = "token" },
			oauth.CodeUnsupportedResponseType,
		},
		"an unknown scope": {
			func(a *oauth.AuthorizeParams) { a.Scope = "admin" },
			oauth.CodeInvalidScope,
		},
		"no resource": {
			func(a *oauth.AuthorizeParams) { a.Resource = "" },
			oauth.CodeInvalidRequest,
		},
		"the API as resource": {
			func(a *oauth.AuthorizeParams) { a.Resource = public + "/api/v1" },
			"invalid_target",
		},
	}
	for name, c := range sent {
		a := f.params()
		c.change(&a)
		loc, err := f.svc.Authorize(ctx, a)
		q := query(t, loc)
		if err != nil || !strings.HasPrefix(loc, "http://127.0.0.1:49152/callback?") ||
			q.Get("error") != c.code || q.Get("state") != "xyz" {
			t.Errorf("%s: %s, %v", name, loc, err)
		}
	}
	one, err := f.svc.Register(ctx, oauth.Registration{RedirectURIs: []string{"http://[::1]/cb"}})
	if err != nil {
		t.Fatal(err)
	}
	a := f.params()
	a.ClientID, a.RedirectURI, a.Scope = one.ID.String(), "", ""
	id := f.request(t, a)
	r, err := f.svc.ConsentRequest(ctx, f.p, id)
	if err != nil || r.RedirectURI != "http://[::1]/cb" ||
		!slices.Equal(r.Scopes, []string{"mcp:read"}) || r.Client.ID != one.ID {
		t.Errorf("a request of a client's one redirect = %+v, %v", r, err)
	}

	// Consent: a member, an account that admits them, an allowed host.
	https := f.params()
	https.RedirectURI = "https://app.example.com/cb"
	hid := f.request(t, https)
	breakGlass := kernel.Principal{
		Kind:           kernel.PrincipalBreakGlass,
		OrganizationID: f.p.OrganizationID,
	}
	if _, err := f.svc.Approve(ctx, breakGlass, hid, f.acct); !errors.Is(err, oauth.ErrNotMember) {
		t.Errorf("a break-glass approval: %v", err)
	}
	if _, err := f.svc.Approve(ctx, f.p, hid,
		kernel.AccountID{ULID: kernel.NewULID()}); !errors.Is(err, oauth.ErrNotAdmitted) {
		t.Errorf("an account that does not admit the member: %v", err)
	}
	if _, err := f.svc.Approve(ctx, f.p, hid, f.acct); !errors.Is(err, oauth.ErrRedirectHost) {
		t.Errorf("a host outside the allowlist: %v", err)
	}
	f.st.hosts[f.acct] = []string{"*.example.com"}
	loc, err := f.svc.Approve(ctx, f.p, hid, f.acct)
	if q := query(t, loc); err != nil || q.Get("code") == "" || q.Get("state") != "xyz" ||
		!strings.HasPrefix(loc, "https://app.example.com/cb?") {
		t.Errorf("an allowed host's approval = %s, %v", loc, err)
	}
	if _, err := f.svc.Approve(ctx, f.p, hid, f.acct); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a second decision: %v", err)
	}
	if _, err := f.svc.Deny(ctx, f.p, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown request: %v", err)
	}
	denied := f.request(t, f.params())
	loc, err = f.svc.Deny(ctx, f.p, denied)
	if q := query(t, loc); err != nil || q.Get("error") != oauth.CodeAccessDenied ||
		q.Get("state") != "xyz" {
		t.Errorf("a denial = %s, %v", loc, err)
	}
	if _, err := f.svc.Deny(ctx, breakGlass, denied); !errors.Is(err, oauth.ErrNotMember) {
		t.Errorf("a break-glass denial: %v", err)
	}

	// The token endpoint.
	for name, p := range map[string]oauth.TokenParams{
		"no grant type": {},
		"no code":       {GrantType: oauth.GrantAuthorizationCode, ClientID: f.client.ID.String()},
		"no verifier": {
			GrantType: oauth.GrantAuthorizationCode, ClientID: f.client.ID.String(), Code: "c",
		},
		"no refresh token": {GrantType: oauth.GrantRefreshToken},
	} {
		_, err := f.svc.Token(ctx, p, "")
		isCode(t, name, err, oauth.CodeInvalidRequest)
	}
	_, err = f.svc.Token(ctx, oauth.TokenParams{GrantType: "password"}, "")
	isCode(t, "a password grant", err, oauth.CodeUnsupportedGrantType)
	_, err = f.svc.Token(ctx, oauth.TokenParams{GrantType: oauth.GrantAuthorizationCode}, "")
	isCode(t, "no client", err, oauth.CodeInvalidClient)
	_, err = f.redeem(t, "not a code")
	isCode(t, "an unknown code", err, oauth.CodeInvalidGrant)

	code := f.code(t)
	for name, p := range map[string]oauth.TokenParams{
		"another client's": {ClientID: one.ID.String()},
		"another redirect": {RedirectURI: "http://127.0.0.1:1/other"},
		"a wrong verifier": {CodeVerifier: strings.Repeat("w", 43)},
	} {
		p.GrantType, p.Code = oauth.GrantAuthorizationCode, code
		if p.ClientID == "" {
			p.ClientID = f.client.ID.String()
		}
		if p.CodeVerifier == "" {
			p.CodeVerifier = verifier
		}
		_, err := f.svc.Token(ctx, p, "")
		isCode(t, name, err, oauth.CodeInvalidGrant)
	}
	_, err = f.svc.Token(ctx, oauth.TokenParams{
		GrantType: oauth.GrantAuthorizationCode, ClientID: f.client.ID.String(), Code: code,
		CodeVerifier: verifier, Resource: public + "/flows/mcp",
	}, "")
	isCode(t, "another resource", err, "invalid_target")

	tok, err := f.redeem(t, code)
	if err != nil {
		t.Fatalf("the code after refusals that leave it unspent: %v", err)
	}
	_, err = f.redeem(t, code)
	isCode(t, "a code used twice", err, oauth.CodeInvalidGrant)
	if g := f.grant(t, tok); g.RevokedAt.IsZero() {
		t.Error("a code used twice left its grant live")
	}

	tok, err = f.redeem(t, f.code(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]oauth.TokenParams{
		"another client's": {ClientID: one.ID.String()},
		"another resource": {Resource: public + "/flows/mcp"},
		"a wider scope":    {Scope: "mcp:write"},
		"an unknown scope": {Scope: "admin"},
	} {
		p.GrantType, p.RefreshToken = oauth.GrantRefreshToken, tok.RefreshToken
		if p.ClientID == "" {
			p.ClientID = f.client.ID.String()
		}
		g := f.grant(t, tok)
		if name == "a wider scope" {
			g.Scopes = []string{"mcp:read"}
			f.st.mu.Lock()
			f.st.sessions[g.ID] = g
			f.st.mu.Unlock()
		}
		_, err := f.svc.Token(ctx, p, "")
		var e *oauth.Error
		if !errors.As(err, &e) || e.Code == "" {
			t.Errorf("%s: %v", name, err)
		}
	}
	pending := f.code(t)
	delete(f.st.owners[f.acct], f.p.MemberID)
	_, err = f.refresh(t, tok.RefreshToken)
	isCode(t, "a removed member's grant", err, oauth.CodeInvalidGrant)
	if g := f.grant(t, tok); g.RevokedAt.IsZero() || !slices.Contains(f.dropped, g.ID) {
		t.Errorf("a removed member's grant lives: %+v", g)
	}
	_, err = f.redeem(t, pending)
	isCode(t, "a code approved before the member's removal", err, oauth.CodeInvalidGrant)

	if err := f.svc.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if ago := time.Since(f.st.expired[0]); ago < 24*time.Hour || ago > 25*time.Hour {
		t.Errorf("requests expire %s after their end", ago)
	}
	if ago := time.Since(
		f.st.expired[1],
	); ago < 30*24*time.Hour ||
		ago > 30*24*time.Hour+time.Hour {
		t.Errorf("clients expire %s after their last use", ago)
	}
}

func TestUnitOAuthFailures(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	broken := errors.New("unreachable")
	code := f.code(t)
	f.st.fail = broken
	if _, err := f.svc.Register(ctx, oauth.Registration{
		RedirectURIs: []string{"http://localhost/cb"},
	}); !errors.Is(err, broken) {
		t.Errorf("a registration the store refuses: %v", err)
	}
	if _, err := f.svc.Authorize(ctx, f.params()); !errors.Is(err, broken) {
		t.Errorf("an authorization the store cannot read: %v", err)
	}
	f.st.fail = nil
	f.svc = oauth.NewService(oauth.Deps{
		Store: f.st, Tokens: issuer{fail: broken}, Admission: admission{f.st},
		Drop: func(kernel.ULID) {}, PublicURL: public, AccessTTL: time.Hour,
		RefreshTTL: time.Hour, CodeTTL: time.Minute, Now: time.Now,
	})
	if _, err := f.redeem(t, code); !errors.Is(err, broken) {
		t.Errorf("a code whose token cannot be signed: %v", err)
	}
}
