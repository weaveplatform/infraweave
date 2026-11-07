package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

var (
	t0      = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	errDown = fmt.Errorf("down: %w", kernel.ErrUnavailable)
)

const (
	publicURL = "http://infraweave.test"
	audience  = publicURL + "/api/v1"
	idle      = 12 * time.Hour
	maxAge    = 7 * 24 * time.Hour
)

// store is the ports and tokens in memory; a token is its kind, session
// and audience joined by spaces.
type store struct {
	accounts map[kernel.AccountID]account.Account
	keys     map[kernel.ULID]apikey.Key
	sessions map[kernel.ULID]session.Session
	orgs     map[kernel.Slug]organization.Organization
	creds    map[kernel.Slug]*breakglass.Credential
	trail    []kernel.OrganizationAuditEntry
	admits   map[kernel.MemberID][]session.Admitted
	down     bool
	// full refuses a key's creation as the seats limit would.
	full *featuresapi.LimitError
}

func (s *store) err() error {
	if s.down {
		return errDown
	}
	return nil
}

func (s *store) APIKey(_ context.Context, id kernel.ULID) (apikey.Key, error) {
	k, ok := s.keys[id]
	if !ok {
		return apikey.Key{}, port.ErrNotFound
	}
	return k, s.err()
}

func (s *store) Account(_ context.Context, id kernel.AccountID) (account.Account, error) {
	a, ok := s.accounts[id]
	if !ok {
		return account.Account{}, port.ErrNotFound
	}
	return a, s.err()
}

func (s *store) TouchAPIKey(context.Context, kernel.ULID) error { return s.err() }

// CreateSession first revokes the member's live sessions in ss's channel,
// as the store does.
func (s *store) CreateSession(_ context.Context, ss session.Session) error {
	if err := s.err(); err != nil {
		return err
	}
	if !ss.MemberID.IsZero() && ss.Channel != session.ChannelUnknown {
		for id, o := range s.sessions {
			if o.MemberID == ss.MemberID && o.Channel == ss.Channel && o.RevokedAt.IsZero() {
				o.RevokedAt = t0
				s.sessions[id] = o
			}
		}
	}
	s.sessions[ss.ID] = ss
	return nil
}

func (s *store) SessionBySecret(_ context.Context, hash []byte) (session.Session, error) {
	if err := s.err(); err != nil {
		return session.Session{}, err
	}
	for _, ss := range s.sessions {
		if ss.SecretHash != nil && string(ss.SecretHash) == string(hash) {
			return ss, nil
		}
	}
	return session.Session{}, port.ErrNotFound
}

func (s *store) TouchSession(context.Context, kernel.ULID, time.Time) error { return s.err() }

func (s *store) EndSession(_ context.Context, id kernel.ULID) (session.Session, error) {
	if err := s.err(); err != nil {
		return session.Session{}, err
	}
	ss, ok := s.sessions[id]
	if !ok {
		return session.Session{}, port.ErrNotFound
	}
	ss.RevokedAt = t0
	s.sessions[id] = ss
	return ss, nil
}

func (s *store) BreakGlassCredential(
	_ context.Context,
	slug kernel.Slug,
) (organization.Organization, *breakglass.Credential, error) {
	if err := s.err(); err != nil {
		return organization.Organization{}, nil, err
	}
	o, ok := s.orgs[slug]
	if !ok {
		return organization.Organization{}, nil, port.ErrNotFound
	}
	return o, s.creds[slug], nil
}

func (s *store) SignIn(
	ctx context.Context,
	ss session.Session,
	e kernel.OrganizationAuditEntry,
) error {
	s.trail = append(s.trail, e)
	return s.CreateSession(ctx, ss)
}

func (s *store) RefuseSignIn(_ context.Context, e kernel.OrganizationAuditEntry) error {
	s.trail = append(s.trail, e)
	return s.err()
}

func (s *store) Accounts(
	_ context.Context,
	_ kernel.OrganizationID,
	member kernel.MemberID,
) ([]session.Admitted, error) {
	return s.admits[member], s.err()
}

func (s *store) Admits(
	_ context.Context,
	_ kernel.OrganizationID,
	member kernel.MemberID,
	acct kernel.AccountID,
) (session.Admitted, bool, error) {
	if err := s.err(); err != nil {
		return session.Admitted{}, false, err
	}
	for _, a := range s.admits[member] {
		if a.ID == acct {
			return a, true, nil
		}
	}
	return session.Admitted{}, false, nil
}

func (s *store) Session(_ context.Context, id kernel.ULID) (session.Session, error) {
	if err := s.err(); err != nil {
		return session.Session{}, err
	}
	ss, ok := s.sessions[id]
	if !ok {
		return session.Session{}, port.ErrNotFound
	}
	return ss, nil
}

func (s *store) live(acct kernel.AccountID) []session.Session {
	var out []session.Session
	for _, ss := range s.sessions {
		if ss.AccountID == acct && ss.RevokedAt.IsZero() {
			out = append(out, ss)
		}
	}
	slices.SortFunc(
		out,
		func(a, b session.Session) int { return strings.Compare(a.ID.String(), b.ID.String()) },
	)
	return out
}

func (s *store) Sessions(
	_ context.Context,
	acct kernel.AccountID,
	after kernel.ULID,
	limit int,
) ([]session.Session, error) {
	var out []session.Session
	for _, ss := range s.live(acct) {
		if ss.ID.String() > after.String() || after.IsZero() {
			out = append(out, ss)
		}
	}
	return out[:min(len(out), limit)], s.err()
}

func (s *store) RevokeSession(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (session.Session, error) {
	if err := s.err(); err != nil {
		return session.Session{}, err
	}
	ss, ok := s.sessions[id]
	if !ok || ss.AccountID != acct {
		return session.Session{}, port.ErrNotFound
	}
	ss.RevokedAt = t0
	s.sessions[id] = ss
	return ss, nil
}

func (s *store) RevokeSessionsExcept(
	_ context.Context,
	acct kernel.AccountID,
	except kernel.ULID,
) ([]session.Session, error) {
	if err := s.err(); err != nil {
		return nil, err
	}
	var out []session.Session
	for _, ss := range s.live(acct) {
		if ss.ID != except {
			ss.RevokedAt = t0
			s.sessions[ss.ID] = ss
			out = append(out, ss)
		}
	}
	return out, nil
}

func (s *store) ExpireSessions(context.Context, time.Time) (int64, error) { return 0, nil }

func (s *store) Issue(_ context.Context, r token.Request) (string, token.Claims, error) {
	return fmt.Sprintf("%s %s %s", r.Kind, r.Session, r.Audience), token.Claims{
		Session: r.Session, Kind: r.Kind, Expiry: t0.Add(r.TTL),
	}, nil
}

func (s *store) Verify(_ context.Context, raw, aud string) (token.Claims, error) {
	parts := strings.Split(raw, " ")
	if len(parts) != 3 || parts[2] != aud {
		return token.Claims{}, token.ErrToken
	}
	id, err := kernel.ParseULID(parts[1])
	if err != nil {
		return token.Claims{}, token.ErrToken
	}
	return token.Claims{Kind: token.Kind(parts[0]), Session: id, Expiry: t0.Add(time.Hour)}, nil
}

// full is the partial with the 501 stubs below it, as internal/app embeds
// it.
type full struct {
	Server
	stubs
}

type stubs struct{ api.UnimplementedStrict }

// world is the adapter over a store holding one secret key.
type world struct {
	st     *store
	h      http.Handler
	acct   kernel.AccountID
	key    kernel.ULID
	secret string
}

// public is the route class that takes no credential.
const public = "/auth/token"

// organizationRoutes are the operations of x-infraweave-access:
// organization in the spec.
var organizationRoutes = []string{
	"/viewer",
	"/auth/logout",
	"/auth/accounts",
	"/auth/session/account",
}

// access classes a request's route as internal/app does from the spec.
func access(r *http.Request) accessapi.Access {
	switch {
	case r.URL.Path == public:
		return accessapi.Public
	case slices.Contains(organizationRoutes, r.URL.Path):
		return accessapi.Organization
	}
	return accessapi.Account
}

func newWorld(t *testing.T) *world {
	t.Helper()
	st := &store{
		keys:     map[kernel.ULID]apikey.Key{},
		sessions: map[kernel.ULID]session.Session{},
		orgs:     map[kernel.Slug]organization.Organization{},
		creds:    map[kernel.Slug]*breakglass.Credential{},
		admits:   map[kernel.MemberID][]session.Admitted{},
	}
	w := &world{
		st:     st,
		acct:   kernel.NewAccountID(),
		key:    kernel.NewULID(),
		secret: apikey.NewSecret(),
	}
	h, _ := apikey.Hash(w.secret)
	org := kernel.NewOrganizationID()
	st.accounts = map[kernel.AccountID]account.Account{
		w.acct: {OrganizationID: org, ID: w.acct, Slug: "acme", Name: "Acme"},
	}
	st.keys[w.key] = apikey.Key{
		OrganizationID: org, AccountID: w.acct, ID: w.key, Name: "ci",
		Type: apikey.TypeSecret, SecretHash: h, Teams: []string{"ops"},
	}
	auth := authn.New(authn.Deps{
		Keys: st, Sessions: st, Tokens: st, Audience: audience, TTL: 10 * time.Hour,
		BreakGlass: st, Admission: st, Idle: idle, MaxAge: maxAge,
		Now: func() time.Time { return t0 },
	})
	keys := apikey.NewService(apikey.Deps{
		Store: st, Drop: auth.Cache().Drop, Now: func() time.Time { return t0 },
	})
	strict := api.NewStrictHandlerWithOptions(
		full{Server: NewServer(auth, keys, st)},
		nil,
		api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  problem.RequestError,
			ResponseErrorHandlerFunc: problem.ResponseError,
		},
	)
	r := chi.NewRouter()
	NewLogin(auth, publicURL).Routes(r)
	r.Mount("/", NewAuthenticator(auth, publicURL).Middleware(access)(api.Handler(strict)))
	w.h = r
	return w
}

func (w *world) do(t *testing.T, method, target, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	return w.send(t, method, target, body, func(req *http.Request) {
		if bearer != "" {
			req.Header.Set("Authorization", bearer)
		}
	})
}

// browse sends a JSON request with the session cookie secret and, when
// it is not empty, the Origin header origin.
func (w *world) browse(
	t *testing.T,
	method, target, secret, origin, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	return w.send(t, method, target, body, func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: secret})
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
	})
}

func (w *world) send(
	t *testing.T,
	method, target, body string,
	set func(*http.Request),
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.9:4000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	set(req)
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	return v
}

// exchange is a token of the world's key.
func (w *world) exchange(t *testing.T) api.User {
	t.Helper()
	rec := w.do(t, http.MethodPost, public, "",
		fmt.Sprintf(`{"id":%q,"secret":%q}`, w.key, w.secret))
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange: %d %s", rec.Code, rec.Body)
	}
	return decode[api.User](t, rec)
}

func wantUnauthenticated(t *testing.T, rec *httptest.ResponseRecorder, challenge bool) {
	t.Helper()
	p := decode[problem.Problem](t, rec)
	if rec.Code != http.StatusUnauthorized || p.Type != problem.Unauthenticated.New("").Type ||
		p.Detail != refused {
		t.Errorf("%d %+v, want unauthenticated", rec.Code, p)
	}
	if got := rec.Header().Get("WWW-Authenticate"); (got == "Bearer") != challenge {
		t.Errorf("WWW-Authenticate %q", got)
	}
}

func TestUnitExchange(t *testing.T) {
	w := newWorld(t)
	u := w.exchange(t)
	if u.Id != "api::"+w.key.String() || u.Name != "ci" || u.Teams[0] != "ops" ||
		u.ValidUntil != int32(t0.Add(10*time.Hour).Unix()) || u.Jwt == "" ||
		u.Actions == nil || u.ApiKeys == nil || u.SecurityKeys == nil {
		t.Errorf("User = %+v", u)
	}
	for _, ss := range w.st.sessions {
		if ss.IPAddress != "192.0.2.9" {
			t.Errorf("the session's address is %q", ss.IPAddress)
		}
	}
	// Every refusal is the one problem, without a challenge.
	for name, body := range map[string]string{
		"unknown key":  fmt.Sprintf(`{"id":%q,"secret":%q}`, kernel.NewULID(), w.secret),
		"wrong secret": fmt.Sprintf(`{"id":%q,"secret":%q}`, w.key, apikey.NewSecret()),
	} {
		t.Run(name, func(t *testing.T) {
			wantUnauthenticated(t, w.do(t, http.MethodPost, public, "", body), false)
		})
	}
	w.st.down = true
	if rec := w.do(
		t,
		http.MethodPost,
		public,
		"",
		fmt.Sprintf(
			`{"id":%q,"secret":%q}`,
			w.key,
			w.secret,
		),
	); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a store down: %d %s", rec.Code, rec.Body)
	}
}

// TestUnitMiddleware is the credentials × route classes table.
func TestUnitMiddleware(t *testing.T) {
	w := newWorld(t)
	good := "Bearer " + w.exchange(t).Jwt
	var sid kernel.ULID
	for id := range w.st.sessions {
		sid = id
	}
	other := w.st.sessions[sid]
	other.ID, other.AccountID = kernel.NewULID(), kernel.AccountID{}
	w.st.sessions[other.ID] = other
	for name, c := range map[string]struct {
		bearer string
		want   int
	}{
		"none":               {"", http.StatusUnauthorized},
		"basic":              {"Basic dXNlcjpwYXNz", http.StatusUnauthorized},
		"empty bearer":       {"Bearer ", http.StatusUnauthorized},
		"garbage":            {"Bearer garbage", http.StatusUnauthorized},
		"wrong audience":     {strings.Replace(good, audience, "http://worker.test/worker/v1", 1), http.StatusUnauthorized},
		"no account":         {fmt.Sprintf("Bearer api_key %s %s", other.ID, audience), http.StatusUnauthorized},
		"good":               {good, http.StatusOK},
		"good, lower scheme": {"bearer" + strings.TrimPrefix(good, "Bearer"), http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			rec := w.do(t, http.MethodGet, "/sessions", c.bearer, "")
			if rec.Code != c.want {
				t.Fatalf("%d %s, want %d", rec.Code, rec.Body, c.want)
			}
			if c.want == http.StatusUnauthorized {
				wantUnauthenticated(t, rec, true)
			}
		})
	}
	// An organization route admits a session of no account.
	if rec := w.do(t, http.MethodGet, "/viewer",
		fmt.Sprintf("Bearer api_key %s %s", other.ID, audience), ""); rec.Code != http.StatusOK {
		t.Errorf("no account on an organization route: %d", rec.Code)
	}
	// A public route takes no credential and ignores a bad one.
	if rec := w.do(t, http.MethodPost, public, "Bearer garbage",
		fmt.Sprintf(`{"id":%q,"secret":%q}`, w.key, w.secret)); rec.Code != http.StatusOK {
		t.Errorf("public route with a bad token: %d", rec.Code)
	}
	// A store down is 503, so a client retries rather than drops its token.
	uncached := "Bearer " + w.exchange(t).Jwt
	w.st.down = true
	if rec := w.do(
		t,
		http.MethodGet,
		"/viewer",
		uncached,
		"",
	); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a store down: %d %s", rec.Code, rec.Body)
	}
}

func TestUnitViewer(t *testing.T) {
	w := newWorld(t)
	tok := w.exchange(t).Jwt
	u := decode[api.User](t, w.do(t, http.MethodGet, "/viewer", "Bearer "+tok, ""))
	if u.Jwt != tok || u.Id != "api::"+w.key.String() ||
		u.ValidUntil != int32(t0.Add(time.Hour).Unix()) {
		t.Errorf("viewer = %+v", u)
	}
}

func TestUnitSessions(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	w.exchange(t)
	w.exchange(t)

	// Pages of two, then one.
	page := decode[api.SessionPage](t, w.do(t, http.MethodGet, "/sessions?limit=2", me, ""))
	if len(page.Items) != 2 || page.NextCursor == nil || !page.Items[0].Current ||
		page.Items[1].Current || page.Items[0].Type != "API_KEY" || !page.Items[0].IsMachineUser ||
		page.Items[0].Channel != api.SessionChannelUNKNOWN || page.Items[0].Teams[0] != "ops" {
		t.Fatalf("first page = %+v", page)
	}
	rest := decode[api.SessionPage](
		t,
		w.do(t, http.MethodGet, "/sessions?limit=2&cursor="+*page.NextCursor, me, ""),
	)
	if len(rest.Items) != 1 || rest.NextCursor != nil || rest.Items[0].Id == page.Items[1].Id {
		t.Errorf("second page = %+v", rest)
	}
	if rec := w.do(
		t,
		http.MethodGet,
		"/sessions?cursor=nope",
		me,
		"",
	); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad cursor: %d", rec.Code)
	}
	if rec := w.do(
		t,
		http.MethodGet,
		"/sessions?cursor=eyJ2IjoxLCJsIjoic2Vzc2lvbnMiLCJrIjoieCIsImlkIjoieCJ9",
		me,
		"",
	); rec.Code != http.StatusBadRequest {
		t.Errorf("a cursor at no session: %d %s", rec.Code, rec.Body)
	}

	// Another account's session, and no session at all, are 404.
	alien := session.Session{
		ID:        kernel.NewULID(),
		AccountID: kernel.NewAccountID(),
		ExpiresAt: t0.Add(time.Hour),
	}
	w.st.sessions[alien.ID] = alien
	for _, id := range []string{alien.ID.String(), "nope"} {
		if rec := w.do(
			t,
			http.MethodDelete,
			"/sessions/"+id,
			me,
			"",
		); rec.Code != http.StatusNotFound {
			t.Errorf("delete %s: %d", id, rec.Code)
		}
	}
	victim := rest.Items[0].Id
	got := decode[api.Session](t, w.do(t, http.MethodDelete, "/sessions/"+victim, me, ""))
	if got.Id != victim || got.Current {
		t.Errorf("sessionDelete = %+v", got)
	}

	all := decode[[]api.Session](t, w.do(t, http.MethodDelete, "/sessions", me, ""))
	if len(all) != 1 || all[0].Id != page.Items[1].Id {
		t.Errorf("sessionDeleteAll = %+v", all)
	}
	if page := decode[api.SessionPage](
		t,
		w.do(t, http.MethodGet, "/sessions", me, ""),
	); len(
		page.Items,
	) != 1 {
		t.Errorf("after deleting all but mine: %+v", page)
	}

	out := decode[api.Session](t, w.do(t, http.MethodPost, "/auth/logout", me, ""))
	if !out.Current {
		t.Errorf("logout = %+v", out)
	}
	wantUnauthenticated(t, w.do(t, http.MethodGet, "/sessions", me, ""), true)
}

func TestUnitSessionsDown(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	// Authenticate once, so the session is cached and the store fails
	// only behind the middleware.
	w.do(t, http.MethodGet, "/viewer", me, "")
	w.st.down = true
	for _, r := range [][2]string{
		{http.MethodGet, "/sessions"},
		{http.MethodDelete, "/sessions/" + kernel.NewULID().String()},
		{http.MethodDelete, "/sessions"},
		{http.MethodPost, "/auth/logout"},
	} {
		if rec := w.do(t, r[0], r[1], me, ""); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: %d %s", r[0], r[1], rec.Code, rec.Body)
		}
	}
}

func TestUnitNoPrincipal(t *testing.T) {
	ctx := context.Background()
	s := Server{}
	_, e1 := s.Viewer(ctx, api.ViewerRequestObject{})
	_, e2 := s.Sessions(ctx, api.SessionsRequestObject{})
	_, e3 := s.SessionDelete(ctx, api.SessionDeleteRequestObject{})
	_, e4 := s.SessionDeleteAll(ctx, api.SessionDeleteAllRequestObject{})
	_, e5 := s.Logout(ctx, api.LogoutRequestObject{})
	_, e6 := s.CliConfirmationToken(ctx, api.CliConfirmationTokenRequestObject{})
	_, e7 := s.AuthAccounts(ctx, api.AuthAccountsRequestObject{})
	_, e8 := s.AuthSessionAccount(ctx, api.AuthSessionAccountRequestObject{})
	for i, err := range []error{e1, e2, e3, e4, e5, e6, e7, e8} {
		if pe, ok := errors.AsType[*problem.Error](
			err,
		); !ok ||
			pe.Problem.Status != http.StatusUnauthorized {
			t.Errorf("handler %d without a principal = %v", i, err)
		}
	}
}

func TestUnitUnix(t *testing.T) {
	if unix(time.Unix(1<<40, 0)) != 1<<31-1 || unix(time.Time{}) != 0 {
		t.Error("unix does not saturate")
	}
}
