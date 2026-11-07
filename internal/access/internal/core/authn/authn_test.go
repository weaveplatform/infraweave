package authn_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var (
	t0      = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	errDown = errors.New("down")
)

const (
	audience = "http://infraweave.test/api/v1"
	idle     = 12 * time.Hour
	maxAge   = 7 * 24 * time.Hour
)

// fakes are the ports and tokens in memory. A token is its kind, session
// and audience joined by spaces.
type fakes struct {
	now      time.Time
	keys     map[kernel.ULID]apikey.Key
	sessions map[kernel.ULID]session.Session
	touched  int
	expired  time.Time
	fail     string
	// orgs are the organizations by slug, each with its credential or nil.
	orgs  map[kernel.Slug]orgCred
	trail []kernel.OrganizationAuditEntry
	// admits are the accounts that admit each member.
	admits map[kernel.MemberID][]session.Admitted
	// touches counts TouchSession's writes.
	touches int
	// orgKeys are the organization keys by ID; orgTouched counts their
	// touches.
	orgKeys    map[kernel.ULID]apikey.OrganizationKey
	orgTouched int
}

func (f *fakes) OrganizationKey(_ context.Context, id kernel.ULID) (apikey.OrganizationKey, error) {
	if err := f.down("OrganizationKey"); err != nil {
		return apikey.OrganizationKey{}, err
	}
	k, ok := f.orgKeys[id]
	if !ok {
		return apikey.OrganizationKey{}, port.ErrNotFound
	}
	return k, nil
}

func (f *fakes) TouchOrganizationKey(context.Context, kernel.ULID) error {
	f.orgTouched++
	return f.down("TouchOrganizationKey")
}

type orgCred struct {
	org  organization.Organization
	cred *breakglass.Credential
}

func (f *fakes) down(op string) error {
	if f.fail == op {
		return errDown
	}
	return nil
}

func (f *fakes) APIKey(_ context.Context, id kernel.ULID) (apikey.Key, error) {
	if err := f.down("APIKey"); err != nil {
		return apikey.Key{}, err
	}
	k, ok := f.keys[id]
	if !ok {
		return apikey.Key{}, port.ErrNotFound
	}
	return k, nil
}

func (f *fakes) TouchAPIKey(context.Context, kernel.ULID) error {
	f.touched++
	return f.down("TouchAPIKey")
}

// CreateSession first revokes the member's live sessions in s's channel,
// as the store does.
func (f *fakes) CreateSession(_ context.Context, s session.Session) error {
	if err := f.down("CreateSession"); err != nil {
		return err
	}
	if !s.MemberID.IsZero() && s.Channel != session.ChannelUnknown {
		for id, o := range f.sessions {
			if o.MemberID == s.MemberID && o.Channel == s.Channel && o.RevokedAt.IsZero() {
				o.RevokedAt = f.now
				f.sessions[id] = o
			}
		}
	}
	f.sessions[s.ID] = s
	return nil
}

func (f *fakes) SessionBySecret(_ context.Context, hash []byte) (session.Session, error) {
	if err := f.down("SessionBySecret"); err != nil {
		return session.Session{}, err
	}
	for _, s := range f.sessions {
		if s.SecretHash != nil && string(s.SecretHash) == string(hash) {
			return s, nil
		}
	}
	return session.Session{}, port.ErrNotFound
}

func (f *fakes) TouchSession(_ context.Context, id kernel.ULID, idle time.Time) error {
	if err := f.down("TouchSession"); err != nil {
		return err
	}
	f.touches++
	s := f.sessions[id]
	if s.RevokedAt.IsZero() {
		s.IdleExpiresAt = idle
		f.sessions[id] = s
	}
	return nil
}

func (f *fakes) EndSession(_ context.Context, id kernel.ULID) (session.Session, error) {
	if err := f.down("EndSession"); err != nil {
		return session.Session{}, err
	}
	s, ok := f.sessions[id]
	if !ok {
		return session.Session{}, port.ErrNotFound
	}
	if s.RevokedAt.IsZero() {
		s.RevokedAt = f.now
	}
	f.sessions[id] = s
	return s, nil
}

func (f *fakes) BreakGlassCredential(
	_ context.Context,
	slug kernel.Slug,
) (organization.Organization, *breakglass.Credential, error) {
	if err := f.down("BreakGlassCredential"); err != nil {
		return organization.Organization{}, nil, err
	}
	o, ok := f.orgs[slug]
	if !ok {
		return organization.Organization{}, nil, port.ErrNotFound
	}
	return o.org, o.cred, nil
}

func (f *fakes) SignIn(
	ctx context.Context,
	s session.Session,
	e kernel.OrganizationAuditEntry,
) error {
	if err := f.down("SignIn"); err != nil {
		return err
	}
	f.trail = append(f.trail, e)
	return f.CreateSession(ctx, s)
}

func (f *fakes) RefuseSignIn(_ context.Context, e kernel.OrganizationAuditEntry) error {
	if err := f.down("RefuseSignIn"); err != nil {
		return err
	}
	f.trail = append(f.trail, e)
	return nil
}

func (f *fakes) Accounts(
	_ context.Context,
	_ kernel.OrganizationID,
	member kernel.MemberID,
) ([]session.Admitted, error) {
	if err := f.down("Accounts"); err != nil {
		return nil, err
	}
	return f.admits[member], nil
}

func (f *fakes) Admits(
	_ context.Context,
	_ kernel.OrganizationID,
	member kernel.MemberID,
	acct kernel.AccountID,
) (session.Admitted, bool, error) {
	if err := f.down("Admits"); err != nil {
		return session.Admitted{}, false, err
	}
	for _, a := range f.admits[member] {
		if a.ID == acct {
			return a, true, nil
		}
	}
	return session.Admitted{}, false, nil
}

func (f *fakes) Session(_ context.Context, id kernel.ULID) (session.Session, error) {
	if err := f.down("Session"); err != nil {
		return session.Session{}, err
	}
	s, ok := f.sessions[id]
	if !ok {
		return session.Session{}, port.ErrNotFound
	}
	return s, nil
}

func (f *fakes) Sessions(
	_ context.Context,
	acct kernel.AccountID,
	_ kernel.ULID,
	_ int,
) ([]session.Session, error) {
	var out []session.Session
	for _, s := range f.sessions {
		if s.AccountID == acct {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakes) RevokeSession(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (session.Session, error) {
	if err := f.down("RevokeSession"); err != nil {
		return session.Session{}, err
	}
	s, ok := f.sessions[id]
	if !ok || s.AccountID != acct {
		return session.Session{}, port.ErrNotFound
	}
	s.RevokedAt = f.now
	f.sessions[id] = s
	return s, nil
}

func (f *fakes) RevokeSessionsExcept(
	_ context.Context,
	acct kernel.AccountID,
	except kernel.ULID,
) ([]session.Session, error) {
	if err := f.down("RevokeSessionsExcept"); err != nil {
		return nil, err
	}
	var out []session.Session
	for id, s := range f.sessions {
		if s.AccountID == acct && id != except && s.RevokedAt.IsZero() {
			s.RevokedAt = f.now
			f.sessions[id] = s
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakes) ExpireSessions(_ context.Context, before time.Time) (int64, error) {
	f.expired = before
	return 0, nil
}

func (f *fakes) Issue(_ context.Context, r token.Request) (string, token.Claims, error) {
	if err := f.down("Issue"); err != nil {
		return "", token.Claims{}, err
	}
	now := f.now.Truncate(time.Second)
	return fmt.Sprintf("%s %s %s", r.Kind, r.Session, r.Audience), token.Claims{
		Subject: r.Subject, Session: r.Session, Kind: r.Kind, Audience: r.Audience,
		IssuedAt: now, Expiry: now.Add(r.TTL),
	}, nil
}

func (f *fakes) Verify(_ context.Context, raw, aud string) (token.Claims, error) {
	if err := f.down("Verify"); err != nil {
		return token.Claims{}, err
	}
	parts := strings.Split(raw, " ")
	if len(parts) != 3 || parts[2] != aud {
		return token.Claims{}, token.ErrToken
	}
	id, err := kernel.ParseULID(parts[1])
	if err != nil {
		return token.Claims{}, token.ErrToken
	}
	return token.Claims{Kind: token.Kind(parts[0]), Session: id}, nil
}

// world is a service over fakes holding one secret key of account acct.
type world struct {
	f      *fakes
	s      *authn.Service
	acct   kernel.AccountID
	key    kernel.ULID
	secret string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	f := &fakes{
		now:      t0,
		keys:     map[kernel.ULID]apikey.Key{},
		sessions: map[kernel.ULID]session.Session{},
		orgs:     map[kernel.Slug]orgCred{},
		admits:   map[kernel.MemberID][]session.Admitted{},
		orgKeys:  map[kernel.ULID]apikey.OrganizationKey{},
	}
	w := &world{
		f:      f,
		acct:   kernel.NewAccountID(),
		key:    kernel.NewULID(),
		secret: apikey.NewSecret(),
	}
	h, _ := apikey.Hash(w.secret)
	f.keys[w.key] = apikey.Key{
		OrganizationID: kernel.NewOrganizationID(), AccountID: w.acct, ID: w.key, Name: "ci",
		Type: apikey.TypeSecret, SecretHash: h, Teams: []string{"ops"},
	}
	w.s = w.replica()
	return w
}

// replica is another replica's service on the same stores, with a cache
// of its own.
func (w *world) replica() *authn.Service {
	f := w.f
	return authn.New(authn.Deps{
		Keys: f, OrganizationKeys: f, Sessions: f, Tokens: f, Audience: audience,
		TTL:        10 * time.Hour,
		BreakGlass: f, Admission: f, Idle: idle, MaxAge: maxAge,
		Now: func() time.Time { return f.now },
	})
}

func (w *world) exchange(t *testing.T) (string, token.Claims) {
	t.Helper()
	g, err := w.s.Exchange(t.Context(), w.key.String(), w.secret, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Session.ID != g.Claims.Session {
		t.Errorf("the grant's session %s is not its token's %s", g.Session.ID, g.Claims.Session)
	}
	return g.Token, g.Claims
}

// authenticate is the principal raw acts as through s.
func authenticate(t *testing.T, s *authn.Service, raw string) (kernel.Principal, error) {
	t.Helper()
	p, _, err := s.Authenticate(t.Context(), raw, authn.ReachAccount)
	return p, err
}

func TestUnitExchange(t *testing.T) {
	w := newWorld(t)
	raw, c := w.exchange(t)
	if c.Kind != token.KindAPIKey || c.Subject != "api::"+w.key.String() ||
		c.Audience != audience || !c.Expiry.Equal(t0.Add(10*time.Hour)) {
		t.Errorf("claims = %+v", c)
	}
	s := w.f.sessions[c.Session]
	if s.ActorKind != kernel.PrincipalAPIKey || s.ActorID != w.key || s.AccountID != w.acct ||
		s.Channel != session.ChannelUnknown || s.Login != w.key.String() || s.Name != "ci" ||
		s.IPAddress != "192.0.2.1" || !s.ExpiresAt.Equal(c.Expiry) || w.f.touched != 1 {
		t.Errorf("session = %+v", s)
	}
	p, got, err := w.s.Authenticate(t.Context(), raw, authn.ReachAccount)
	if err != nil || p.SessionID != c.Session || p.Caller() != "api_key:"+w.key.String() ||
		got.Session != c.Session {
		t.Errorf("Authenticate = %+v, %+v, %v", p, got, err)
	}

	// Two tokens of one key are two sessions with one caller.
	raw2, c2 := w.exchange(t)
	p2, err := authenticate(t, w.s, raw2)
	if err != nil || c2.Session == c.Session || p2.Caller() != p.Caller() {
		t.Errorf("a second token = %+v, %v", p2, err)
	}
}

func TestUnitExchangeExpiry(t *testing.T) {
	w := newWorld(t)
	k := w.f.keys[w.key]
	k.ExpiresAt = t0.Add(90*time.Minute + 500*time.Millisecond)
	w.f.keys[w.key] = k
	if _, c := w.exchange(t); !c.Expiry.Equal(t0.Add(90 * time.Minute)) {
		t.Errorf("exp = %s, want the key's expiry in whole seconds", c.Expiry)
	}
	k.ExpiresAt = t0.Add(500 * time.Millisecond)
	w.f.keys[w.key] = k
	if _, err := w.s.Exchange(
		t.Context(),
		w.key.String(),
		w.secret,
		"",
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("a key with under a second left = %v", err)
	}
}

func TestUnitExchangeRefused(t *testing.T) {
	w := newWorld(t)
	spent := func(f func(*apikey.Key)) string {
		k := w.f.keys[w.key]
		f(&k)
		id := kernel.NewULID()
		k.ID = id
		w.f.keys[id] = k
		return id.String()
	}
	for name, c := range map[string]struct{ id, secret string }{
		"not a ULID":   {"nope", w.secret},
		"unknown":      {kernel.NewULID().String(), w.secret},
		"wrong secret": {w.key.String(), apikey.NewSecret()},
		"not a secret": {w.key.String(), "hunter2"},
		"expired":      {spent(func(k *apikey.Key) { k.ExpiresAt = t0 }), w.secret},
		"disabled":     {spent(func(k *apikey.Key) { k.DisabledAt = t0 }), w.secret},
		"deleted":      {spent(func(k *apikey.Key) { k.DeletedAt = t0 }), w.secret},
		"an OIDC key":  {spent(func(k *apikey.Key) { k.Type = apikey.TypeOIDC }), w.secret},
	} {
		if _, err := w.s.Exchange(
			t.Context(),
			c.id,
			c.secret,
			"",
		); err != authn.ErrUnauthenticated { //nolint:errorlint // the identical error
			t.Errorf("%s = %v", name, err)
		}
	}
	if len(w.f.sessions) != 0 || w.f.touched != 0 {
		t.Errorf(
			"a refused exchange left %d sessions and %d touches",
			len(w.f.sessions),
			w.f.touched,
		)
	}
}

func TestUnitExchangeDown(t *testing.T) {
	for _, op := range []string{"APIKey", "TouchAPIKey", "Issue", "CreateSession"} {
		w := newWorld(t)
		w.f.fail = op
		_, err := w.s.Exchange(t.Context(), w.key.String(), w.secret, "")
		if !errors.Is(err, errDown) {
			t.Errorf("%s down = %v", op, err)
		}
	}
}

func TestUnitAuthenticate(t *testing.T) {
	w := newWorld(t)
	raw, c := w.exchange(t)
	sess := func(f func(*session.Session)) string {
		s := w.f.sessions[c.Session]
		s.ID = kernel.NewULID()
		f(&s)
		w.f.sessions[s.ID] = s
		return fmt.Sprintf("%s %s %s", token.KindAPIKey, s.ID, audience)
	}
	for name, tok := range map[string]string{
		"garbage":          "garbage",
		"wrong audience":   fmt.Sprintf("%s %s %s", token.KindAPIKey, c.Session, "http://elsewhere/api/v1"),
		"no session":       fmt.Sprintf("%s %s %s", token.KindAPIKey, kernel.NewULID(), audience),
		"revoked":          sess(func(s *session.Session) { s.RevokedAt = t0 }),
		"ended":            sess(func(s *session.Session) { s.ExpiresAt = t0 }),
		"another kind":     sess(func(s *session.Session) { s.ActorKind = kernel.PrincipalUser }),
		"no account":       sess(func(s *session.Session) { s.AccountID = kernel.AccountID{} }),
		"organization key": sess(func(s *session.Session) { s.ActorKind = kernel.PrincipalOrganization }),
		"unknown kind":     strings.Replace(raw, string(token.KindAPIKey), string(token.KindOAuth), 1),
	} {
		if _, err := authenticate(
			t,
			w.s,
			tok,
		); err != authn.ErrUnauthenticated { //nolint:errorlint // the identical error
			t.Errorf("%s = %v", name, err)
		}
	}
	// An organization key's token on an account route has no account.
	org := sess(func(s *session.Session) {
		s.ActorKind, s.AccountID = kernel.PrincipalOrganization, kernel.AccountID{}
	})
	org = strings.Replace(org, string(token.KindAPIKey), string(token.KindOrganizationKey), 1)
	if _, err := authenticate(t, w.s, org); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("organization principal = %v", err)
	}
	// The viewer's and the switcher's routes refuse it too; only the
	// Organization tag's accept it, as the organization's.
	if _, _, err := w.s.Authenticate(
		t.Context(), org, authn.ReachOrganization,
	); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("organization principal on an organization route = %v", err)
	}
	if p, _, err := w.s.Authenticate(t.Context(), org, authn.ReachOrganizationAdmin); err != nil ||
		p.Kind != kernel.PrincipalOrganization {
		t.Errorf("organization principal on an organization admin route = %+v, %v", p, err)
	}
	// An API key's token works in an account, which the tag's routes
	// refuse.
	if _, _, err := w.s.Authenticate(
		t.Context(), raw, authn.ReachOrganizationAdmin,
	); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("API key on an organization admin route = %v", err)
	}

	for _, op := range []string{"Verify", "Session"} {
		w.s.Cache().Flush()
		w.f.fail = op
		if _, err := authenticate(t, w.s, raw); !errors.Is(err, errDown) {
			t.Errorf("%s down = %v", op, err)
		}
	}
}

func TestUnitRevocation(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	other := w.replica()
	raw1, _ := w.exchange(t)
	raw2, c2 := w.exchange(t)
	raw3, c3 := w.exchange(t)
	p1, err := authenticate(t, w.s, raw1)
	if err != nil {
		t.Fatal(err)
	}
	if ss, err := w.s.Sessions(ctx, p1, kernel.ULID{}, 10); err != nil || len(ss) != 3 {
		t.Errorf("Sessions = %d, %v", len(ss), err)
	}
	for _, raw := range []string{raw2, raw3} {
		if _, err := authenticate(t, other, raw); err != nil {
			t.Fatal(err)
		}
	}

	// Revoke drops the session from this replica's cache at once.
	if ss, err := w.s.Revoke(ctx, p1, c3.Session); err != nil || ss.ID != c3.Session {
		t.Fatalf("Revoke = %+v, %v", ss, err)
	}
	if _, err := authenticate(t, w.s, raw3); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("a revoked token = %v", err)
	}
	if _, err := w.s.Revoke(
		ctx,
		kernel.Principal{AccountID: kernel.NewAccountID()},
		c2.Session,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("revoke across accounts = %v", err)
	}

	// Another replica, whose message was lost, sees the revocation once
	// its cache entry ages out.
	if _, err := authenticate(t, other, raw3); err != nil {
		t.Errorf("the other replica before CacheTTL: %v", err)
	}
	w.f.now = w.f.now.Add(session.CacheTTL)
	if _, err := authenticate(t, other, raw3); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("the other replica after CacheTTL: %v", err)
	}

	if ss, err := w.s.RevokeOthers(ctx, p1); err != nil || len(ss) != 1 || ss[0].ID != c2.Session {
		t.Fatalf("RevokeOthers = %+v, %v", ss, err)
	}
	if _, err := authenticate(t, w.s, raw2); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("after RevokeOthers: %v", err)
	}
	if _, err := authenticate(t, w.s, raw1); err != nil {
		t.Errorf("the caller's own session: %v", err)
	}

	if ss, err := w.s.Logout(ctx, p1); err != nil || ss.ID != p1.SessionID {
		t.Fatalf("Logout = %+v, %v", ss, err)
	}
	if _, err := authenticate(t, w.s, raw1); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("after logout: %v", err)
	}
	if err := w.s.Expire(ctx); err != nil || !w.f.expired.Equal(w.f.now.Add(-authn.Retention)) {
		t.Errorf("Expire = %v, before %s", err, w.f.expired)
	}

	for _, op := range []string{"RevokeSession", "RevokeSessionsExcept"} {
		w.f.fail = op
		_, err1 := w.s.Revoke(ctx, p1, p1.SessionID)
		_, err2 := w.s.RevokeOthers(ctx, p1)
		if !errors.Is(err1, errDown) && !errors.Is(err2, errDown) {
			t.Errorf("%s down = %v, %v", op, err1, err2)
		}
	}
}
