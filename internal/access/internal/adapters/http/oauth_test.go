package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// grants is oauth.Store in memory: one grant per code, no rotation
// history beyond the current token.
type grants struct {
	clients  map[kernel.ULID]oauth.Client
	requests map[kernel.ULID]*oauth.Request
	sessions map[string]session.Session
	hosts    map[kernel.AccountID][]string
	down     bool
}

func newGrants() *grants {
	return &grants{
		clients:  map[kernel.ULID]oauth.Client{},
		requests: map[kernel.ULID]*oauth.Request{},
		sessions: map[string]session.Session{},
		hosts:    map[kernel.AccountID][]string{},
	}
}

func (g *grants) CreateClient(_ context.Context, c oauth.Client) error {
	if g.down {
		return errDown
	}
	g.clients[c.ID] = c
	return nil
}

func (g *grants) Client(_ context.Context, id kernel.ULID) (oauth.Client, error) {
	c, ok := g.clients[id]
	if !ok {
		return oauth.Client{}, port.ErrNotFound
	}
	return c, nil
}

func (g *grants) CreateRequest(_ context.Context, r oauth.Request) error {
	g.requests[r.ID] = &r
	return nil
}

func (g *grants) Request(_ context.Context, id kernel.ULID) (oauth.Request, error) {
	if g.down {
		return oauth.Request{}, errDown
	}
	r, ok := g.requests[id]
	if !ok {
		return oauth.Request{}, port.ErrNotFound
	}
	return *r, nil
}

func (g *grants) Decide(_ context.Context, d oauth.Request, _ time.Time) error {
	r, ok := g.requests[d.ID]
	if !ok || r.Decided {
		return port.ErrNotFound
	}
	r.Decided, r.OrganizationID, r.AccountID, r.MemberID = true, d.OrganizationID, d.AccountID,
		d.MemberID
	r.CodeHash, r.CodeExpiresAt = d.CodeHash, d.CodeExpiresAt
	return nil
}

func (g *grants) RedirectHosts(_ context.Context, acct kernel.AccountID) ([]string, error) {
	return g.hosts[acct], nil
}

func (g *grants) Redeem(
	_ context.Context,
	hash []byte,
	grant func(oauth.Request, oauth.Owner) (session.Session, error),
) (session.Session, error) {
	for _, r := range g.requests {
		if string(r.CodeHash) == string(hash) {
			ss, err := grant(*r, oauth.Owner{Login: "alice", Name: "Alice"})
			if err == nil {
				r.CodeHash = nil
				g.sessions[string(ss.SecretHash)] = ss
			}
			return ss, err
		}
	}
	return session.Session{}, port.ErrNotFound
}

func (g *grants) Refresh(
	_ context.Context,
	hash []byte,
	rotate func(session.Session, oauth.Owner) (session.Session, error),
) (session.Session, error) {
	ss, ok := g.sessions[string(hash)]
	if !ok {
		return session.Session{}, port.ErrNotFound
	}
	next, err := rotate(ss, oauth.Owner{Login: "alice", Name: "Alice"})
	if err == nil {
		delete(g.sessions, string(hash))
		g.sessions[string(next.SecretHash)] = next
	}
	return next, err
}

func (g *grants) ExpireOAuth(context.Context, time.Time, time.Time) error { return nil }

// oauthError checks rec is the JSON error of RFC 6749 with status and code.
func oauthError(
	t *testing.T,
	what string,
	rec *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	body := decode[map[string]string](t, rec)
	if rec.Code != status || body["error"] != code || body["error_description"] == "" ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("%s: %d %s", what, rec.Code, rec.Body)
	}
}

func (w *world) form(t *testing.T, target string, v url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return w.send(t, http.MethodPost, target, v.Encode(), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	})
}

const verifier = "a-verifier-of-at-least-forty-three-characters"

// refusedAs checks rec is the problem slug with code, naming what.
func refusedAs(
	t *testing.T,
	what string,
	rec *httptest.ResponseRecorder,
	code int,
	slug string,
) {
	t.Helper()
	if rec.Code != code {
		t.Errorf("%s: %d %s", what, rec.Code, rec.Body)
		return
	}
	wantProblem(t, rec, code, slug)
}

func TestUnitOAuthProtocol(t *testing.T) {
	w := newWorld(t)
	rec := w.do(t, http.MethodGet, "/.well-known/oauth-authorization-server", "", "")
	m := decode[map[string]any](t, rec)
	if rec.Code != http.StatusOK || m["issuer"] != publicURL ||
		m["token_endpoint"] != publicURL+"/oauth/token" {
		t.Errorf("metadata = %d %s", rec.Code, rec.Body)
	}

	oauthError(t, "a registration that is not JSON",
		w.do(t, http.MethodPost, "/oauth/register", "", "redirect_uris"),
		http.StatusBadRequest, oauth.CodeInvalidClientMetadata)
	oauthError(t, "an http redirect", w.do(t, http.MethodPost, "/oauth/register", "",
		`{"redirect_uris":["http://app.example.com/cb"]}`),
		http.StatusBadRequest, oauth.CodeInvalidRedirectURI)
	rec = w.do(
		t,
		http.MethodPost,
		"/oauth/register",
		"",
		`{"redirect_uris":["http://127.0.0.1/cb","https://app.example.com/cb"],"client_name":"Editor","scope":"ignored"}`,
	)
	reg := decode[map[string]any](t, rec)
	client, _ := reg["client_id"].(string)
	if rec.Code != http.StatusCreated || client == "" || reg["client_name"] != "Editor" ||
		reg["token_endpoint_auth_method"] != "none" || reg["client_id_issued_at"] != float64(t0.Unix()) {
		t.Fatalf("registration = %d %s", rec.Code, rec.Body)
	}

	authorize := func(redirect, method string) *httptest.ResponseRecorder {
		q := url.Values{
			"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect},
			"code_challenge": {oauth.Challenge(verifier)}, "code_challenge_method": {method},
			"resource": {publicURL + "/mcp"}, "state": {"xyz"}, "scope": {"mcp:write"},
		}
		return w.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
	}
	oauthError(t, "an unknown client", w.do(t, http.MethodGet,
		"/oauth/authorize?client_id=x", "", ""), http.StatusUnauthorized, oauth.CodeInvalidClient)
	rec = authorize("http://127.0.0.1:4000/cb", "plain")
	if loc, _ := url.Parse(rec.Header().Get("Location")); rec.Code != http.StatusFound ||
		loc.Query().Get("error") != oauth.CodeInvalidRequest {
		t.Errorf("plain PKCE = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = authorize("http://127.0.0.1:4000/cb", oauth.MethodS256)
	id, ok := strings.CutPrefix(rec.Header().Get("Location"), publicURL+"/consent/")
	if rec.Code != http.StatusFound || !ok || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authorize = %d %s", rec.Code, rec.Header().Get("Location"))
	}

	member, secret := w.member(w.acct, session.Admitted{ID: w.acct})
	path := "/auth/oauth/requests/" + id
	rec = w.browse(t, http.MethodGet, path, secret, "", "")
	r := decode[api.AuthOAuthRequest](t, rec)
	if rec.Code != http.StatusOK || r.ClientName != "Editor" || r.RedirectHost != "127.0.0.1" ||
		r.Resource != publicURL+"/mcp" || strings.Join(r.Scopes, " ") != "mcp:read mcp:write" ||
		r.ExpiresAt != t0.Add(oauth.RequestTTL).Unix() {
		t.Errorf("the request = %d %s", rec.Code, rec.Body)
	}
	rec = w.browse(t, http.MethodPost, path+"/approve", secret, publicURL,
		`{"account":"`+w.acct.String()+`"}`)
	d := decode[api.AuthOAuthDecision](t, rec)
	loc, err := url.Parse(d.RedirectTo)
	if rec.Code != http.StatusOK || err != nil || loc.Query().Get("state") != "xyz" {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body)
	}
	if member.MemberID.IsZero() {
		t.Fatal("no member")
	}

	oauthError(t, "a body that is not a form", w.send(t, http.MethodPost, "/oauth/token", "%zz",
		func(r *http.Request) {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}), http.StatusBadRequest, oauth.CodeInvalidRequest)
	oauthError(t, "a wrong verifier", w.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client},
		"code": {loc.Query().Get("code")}, "code_verifier": {strings.Repeat("w", 43)},
	}), http.StatusBadRequest, oauth.CodeInvalidGrant)
	rec = w.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client},
		"code": {loc.Query().Get("code")}, "code_verifier": {verifier},
		"redirect_uri": {"http://127.0.0.1:4000/cb"},
	})
	tok := decode[map[string]any](t, rec)
	refresh, _ := tok["refresh_token"].(string)
	if rec.Code != http.StatusOK || tok["token_type"] != "Bearer" || refresh == "" ||
		tok["expires_in"] != float64(3600) || rec.Header().Get("Cache-Control") != "no-store" ||
		!strings.HasSuffix(tok["access_token"].(string), " "+publicURL+"/mcp") {
		t.Fatalf("the code's tokens = %d %s", rec.Code, rec.Body)
	}
	rec = w.form(t, "/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh},
	})
	if next := decode[map[string]any](t, rec); rec.Code != http.StatusOK ||
		next["refresh_token"] == refresh {
		t.Errorf("a refresh = %d %s", rec.Code, rec.Body)
	}
	oauthError(t, "a spent refresh token", w.form(t, "/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh},
	}), http.StatusBadRequest, oauth.CodeInvalidGrant)

	w.grants.down = true
	rec = w.do(
		t,
		http.MethodPost,
		"/oauth/register",
		"",
		`{"redirect_uris":["http://localhost/cb"]}`,
	)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a registration the database refuses = %d %s", rec.Code, rec.Body)
	}
}

func TestUnitOAuthConsent(t *testing.T) {
	w := newWorld(t)
	c, err := oauth.NewService(oauth.Deps{Store: w.grants, Now: func() time.Time { return t0 }}).
		Register(t.Context(), oauth.Registration{
			RedirectURIs: []string{"https://app.example.com/cb"},
		})
	if err != nil {
		t.Fatal(err)
	}
	request := func() string {
		id := kernel.NewULID()
		w.grants.requests[id] = &oauth.Request{
			ID: id, Client: c, RedirectURI: "https://app.example.com/cb",
			Resource: publicURL + "/mcp", Scopes: []string{"mcp:read"}, State: "s",
			Challenge: oauth.Challenge(verifier), CreatedAt: t0,
			ExpiresAt: t0.Add(oauth.RequestTTL),
		}
		return "/auth/oauth/requests/" + id.String()
	}
	path := request()
	other := kernel.NewAccountID()
	_, secret := w.member(w.acct, session.Admitted{ID: w.acct})
	approve := func(body string) *httptest.ResponseRecorder {
		return w.browse(t, http.MethodPost, path+"/approve", secret, publicURL, body)
	}

	tok := w.exchange(t)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"an API key's token reads": w.do(t, http.MethodGet, path, "Bearer "+tok.Jwt, ""),
		"an API key's token denies": w.do(t, http.MethodPost, path+"/deny",
			"Bearer "+tok.Jwt, ""),
		"an account outside the member's": approve(`{"account":"` + other.String() + `"}`),
		"an account that is not an ID":    approve(`{"account":"acme"}`),
		"a host outside the allowlist":    approve(`{"account":"` + w.acct.String() + `"}`),
	} {
		refusedAs(t, name, rec, http.StatusForbidden, "forbidden")
	}
	w.grants.hosts[w.acct] = []string{"app.example.com"}
	rec := approve(`{"account":"` + w.acct.String() + `"}`)
	if d := decode[api.AuthOAuthDecision](t, rec); rec.Code != http.StatusOK ||
		!strings.HasPrefix(d.RedirectTo, "https://app.example.com/cb?code=") {
		t.Errorf("approve = %d %s", rec.Code, rec.Body)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"a decided request":    w.browse(t, http.MethodGet, path, secret, "", ""),
		"approved again":       approve(`{"account":"` + w.acct.String() + `"}`),
		"a request not a ULID": w.browse(t, http.MethodGet, "/auth/oauth/requests/x", secret, "", ""),
		"denied when not a ULID": w.browse(t, http.MethodPost, "/auth/oauth/requests/x/deny",
			secret, publicURL, ""),
		"approved when not a ULID": w.browse(t, http.MethodPost,
			"/auth/oauth/requests/x/approve", secret, publicURL, `{"account":"x"}`),
	} {
		refusedAs(t, name, rec, http.StatusNotFound, "not-found")
	}
	path = request()
	rec = w.browse(t, http.MethodPost, path+"/deny", secret, publicURL, "")
	if d := decode[api.AuthOAuthDecision](t, rec); rec.Code != http.StatusOK ||
		!strings.Contains(d.RedirectTo, "error=access_denied") {
		t.Errorf("deny = %d %s", rec.Code, rec.Body)
	}
	refusedAs(t, "denied twice", w.browse(t, http.MethodPost, path+"/deny", secret, publicURL,
		""), http.StatusNotFound, "not-found")

	org := w.withBreakGlass("acme", "admin")
	rec = w.signIn(t, "acme", "admin", bgPassword, publicURL)
	var glass string
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == session.CookieName {
			glass = ck.Value
		}
	}
	if org.ID.IsZero() || glass == "" {
		t.Fatalf("no break-glass session: %d %s", rec.Code, rec.Body)
	}
	refusedAs(t, "a break-glass session", w.browse(t, http.MethodGet, request(), glass, "", ""),
		http.StatusForbidden, "forbidden")
	w.grants.down = true
	if rec := w.browse(t, http.MethodGet, request(), secret, "", ""); rec.Code !=
		http.StatusServiceUnavailable {
		t.Errorf("a request the database cannot read = %d %s", rec.Code, rec.Body)
	}
}
