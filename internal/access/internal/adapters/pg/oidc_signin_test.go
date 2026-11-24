package pg_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func hash(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// signInProvider creates an enabled OIDC provider of x's organization,
// whose admin login is root.
func signInProvider(t *testing.T, x seeded, s *pg.Store) sso.Provider {
	t.Helper()
	p := x.provider(sso.OIDC, "okta", time.Now().UTC().Truncate(time.Second))
	p, err := s.CreateProvider(t.Context(), p, []byte("client-secret"), byAdmin(x, sso.ActionCreate))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(t.Context(), `UPDATE identity_providers SET enabled_at = now(),
		admin_login = 'root' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	return p
}

// signInRequest is a started sign-in through p, bound to state and
// browser, expiring at expires.
func signInRequest(x seeded, p sso.Provider, state, browser string, expires time.Time) sso.Request {
	return sso.Request{
		ID: kernel.NewULID(), OrganizationID: x.org, ProviderID: p.ID, Purpose: sso.PurposeSignIn,
		StateHash: hash(state), Nonce: "nonce-" + state, Verifier: "verifier-" + state,
		BrowserHash: hash(browser), ReturnTo: "/stacks", CreatedAt: time.Now(), ExpiresAt: expires,
	}
}

// TestAccSignInRequests checks a started sign-in's row: its verifier
// sealed, consumed once even by callbacks racing each other, only by its
// browser and only before it expires, and deleted once it has ended.
//
// ref: decision 0006 § "Human login"
func TestAccSignInRequests(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "pied-piper")
	s := x.s.WithSealer(keyring(t, db))
	p := signInProvider(t, x, s)
	soon := time.Now().Add(sso.RequestTTL)

	r := signInRequest(x, p, "state-1", "browser-1", soon)
	if err := s.CreateSignInRequest(ctx, r); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := db.QueryRow(ctx, `SELECT pkce_verifier_sealed FROM sso_requests WHERE id = $1`, r.ID).
		Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(r.Verifier)) {
		t.Errorf("pkce_verifier_sealed = %q, %v", sealed, err)
	}
	if _, err := s.ConsumeSignInRequest(ctx, hash("state-1"), hash("browser-2")); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("another browser's callback = %v", err)
	}
	var wg sync.WaitGroup
	got := make(chan sso.Request, 8)
	for range 8 {
		wg.Go(func() {
			if r, err := s.ConsumeSignInRequest(ctx, hash("state-1"), hash("browser-1")); err == nil {
				got <- r
			} else if !errors.Is(err, port.ErrNotFound) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(got)
	var won []sso.Request
	for r := range got {
		won = append(won, r)
	}
	if len(won) != 1 || won[0].Verifier != r.Verifier || won[0].Nonce != r.Nonce || won[0].ReturnTo != "/stacks" ||
		won[0].Purpose != sso.PurposeSignIn || won[0].ProviderID != p.ID || !won[0].ExpiresAt.Equal(r.ExpiresAt.Truncate(time.Microsecond)) {
		t.Fatalf("racing callbacks consumed %+v", won)
	}

	old := signInRequest(x, p, "state-2", "browser-1", time.Now().Add(-time.Second))
	if err := s.CreateSignInRequest(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeSignInRequest(ctx, hash("state-2"), hash("browser-1")); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an expired request = %v", err)
	}
	live := signInRequest(x, p, "state-3", "browser-1", soon)
	if err := s.CreateSignInRequest(ctx, live); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ExpireSignIns(ctx, time.Now()); err != nil || n != 2 {
		t.Errorf("ExpireSignIns = %d, %v; want the consumed and the expired request", n, err)
	}
	if _, err := s.ConsumeSignInRequest(ctx, hash("state-3"), hash("browser-1")); err != nil {
		t.Errorf("the live request after expiry = %v", err)
	}

	// A verifier sealed to another row does not open.
	moved := signInRequest(x, p, "state-4", "browser-1", soon)
	if err := s.CreateSignInRequest(ctx, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE sso_requests SET pkce_verifier_sealed = $2 WHERE id = $1`,
		moved.ID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeSignInRequest(ctx, hash("state-4"), hash("browser-1")); err == nil || errors.Is(err, port.ErrNotFound) {
		t.Errorf("another row's verifier = %v", err)
	}

	// Deleting the provider deletes its requests.
	stray := signInRequest(x, p, "state-5", "browser-1", soon)
	if err := s.CreateSignInRequest(ctx, stray); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DeleteProvider(ctx, x.org, p.ID, byAdmin(x, sso.ActionDelete)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM sso_requests`).Scan(&n); err != nil || n != 0 {
		t.Errorf("requests after the provider's deletion = %d, %v", n, err)
	}
}

// signInRecord is a sign-in through p of the identity login and subject,
// as the service builds it.
func signInRecord(x seeded, p sso.Provider, login, subject string) sso.SignInRecord {
	member, sub := true, subject
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, h := session.NewSecret()
	return sso.SignInRecord{
		Provider: p,
		Identity: sso.Identity{Subject: subject, Login: login, Name: login, Groups: []string{"dev"}, SID: "sid-" + subject},
		Session: session.Session{
			ID: kernel.NewULID(), OrganizationID: x.org, Channel: session.ChannelWeb,
			ActorKind: kernel.PrincipalUser, Login: login, Name: login, Teams: []string{"dev"},
			OriginalMember: &member, OriginalTeamsKnown: true, OriginalTeams: []string{"dev"},
			IdentityProviderID: p.ID, IDPSubject: &sub, IDPSID: "sid-" + subject,
			CreatedAt: now, ExpiresAt: now.Add(10 * time.Hour), IdleExpiresAt: now.Add(time.Hour), SecretHash: h,
		},
		IDToken: "raw-id-token-" + subject,
	}
}

func memberStatus(t *testing.T, x seeded, login string) (kernel.MemberID, string, *time.Time) {
	t.Helper()
	var (
		m      kernel.MemberID
		status string
		seen   *time.Time
	)
	if err := x.db.QueryRow(t.Context(), `SELECT id, status, last_seen_at FROM members
		WHERE organization_id = $1 AND login = $2`, x.org, login).Scan(&m, &status, &seen); err != nil {
		t.Fatal(err)
	}
	return m, status, seen
}

// TestAccSignInIdentity checks a sign-in's member and session: an unknown
// subject creates an active member and records its identity, a second
// sign-in replaces the first session, a pending member invited under the
// login becomes active, a deactivated member is refused, and the ID token
// is sealed beside the session and carried to the session an account
// switch creates.
//
// ref: decision 0006 § "Human login"
func TestAccSignInIdentity(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "hooli")
	s := x.s.WithSealer(keyring(t, db))
	p := signInProvider(t, x, s)
	l := listen(t, db)

	first, err := s.SignInIdentity(ctx, signInRecord(x, p, "carol", "carol-sub"))
	if err != nil {
		t.Fatal(err)
	}
	carol, status, seen := memberStatus(t, x, "carol")
	if first.MemberID != carol || first.ActorID != carol.ULID || status != "active" || seen == nil {
		t.Errorf("first sign-in: session %+v, member %s %s %v", first, carol, status, seen)
	}
	read, err := s.Session(ctx, first.ID)
	if err != nil || read.IDPSID != "sid-carol-sub" || read.IdentityProviderID != p.ID || !read.AccountID.IsZero() {
		t.Errorf("stored session %+v, %v", read, err)
	}
	var sealed []byte
	if err := db.QueryRow(ctx, `SELECT idp_id_token_sealed FROM sessions WHERE id = $1`, first.ID).
		Scan(&sealed); err != nil || len(sealed) == 0 || bytes.Contains(sealed, []byte("raw-id-token")) {
		t.Errorf("idp_id_token_sealed = %q, %v", sealed, err)
	}
	if tok, ok, err := s.SessionIDToken(ctx, first.ID); err != nil || !ok || string(tok.Reveal()) != "raw-id-token-carol-sub" {
		t.Errorf("SessionIDToken = %t, %v", ok, err)
	}
	if _, ok, err := s.SessionIDToken(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) || ok {
		t.Errorf("an unknown session's ID token = %t, %v", ok, err)
	}

	second, err := s.SignInIdentity(ctx, signInRecord(x, p, "carol", "carol-sub"))
	if err != nil || second.MemberID != carol {
		t.Fatalf("second sign-in = %+v, %v", second, err)
	}
	if old, err := s.Session(ctx, first.ID); err != nil || old.Live(time.Now()) {
		t.Errorf("the first session after the second sign-in: %+v, %v", old, err)
	}
	if got := messages(t, l); !slices.Contains(got, session.Key(first.ID)) {
		t.Errorf("messages = %v, want the first session's", got)
	}

	bob := addMember(t, x, "bob", "pending")
	if ss, err := s.SignInIdentity(ctx, signInRecord(x, p, "bob", "bob-sub")); err != nil || ss.MemberID != bob {
		t.Errorf("a pending member's sign-in = %+v, %v", ss, err)
	}
	if _, status, _ := memberStatus(t, x, "bob"); status != "active" {
		t.Errorf("bob is %s", status)
	}
	if _, err := db.Exec(ctx, `UPDATE members SET status = 'deactivated' WHERE id = $1`, bob); err != nil {
		t.Fatal(err)
	}
	refused := signInRecord(x, p, "bob", "bob-sub")
	if _, err := s.SignInIdentity(ctx, refused); !errors.Is(err, sso.ErrDeactivated) {
		t.Errorf("a deactivated member's sign-in = %v", err)
	}
	if _, err := s.Session(ctx, refused.Session.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a refused sign-in left a session: %v", err)
	}

	// A re-authentication must resolve to the member it names.
	again := signInRecord(x, p, "carol", "carol-sub")
	again.Member = bob
	if _, err := s.SignInIdentity(ctx, again); !errors.Is(err, sso.ErrOtherMember) {
		t.Errorf("another member's re-authentication = %v", err)
	}

	// An account switch carries the ID token to the entered session.
	admit(t, x, carol)
	candidate := second
	candidate.ID, candidate.AccountID, candidate.CreatedAt = kernel.NewULID(), x.acct, time.Now()
	_, candidate.SecretHash = session.NewSecret()
	entered, err := s.Enter(ctx, candidate, nil, second.ID)
	if err != nil || entered.IDPSID != "sid-carol-sub" {
		t.Fatalf("Enter = %+v, %v", entered, err)
	}
	if tok, ok, err := s.SessionIDToken(ctx, entered.ID); err != nil || !ok || string(tok.Reveal()) != "raw-id-token-carol-sub" {
		t.Errorf("the entered session's ID token = %t, %v", ok, err)
	}
}

// TestAccSignInEnable checks an enabling sign-in: it enables the provider
// with the login as its admin login and records its trail entry, and that
// login's sessions through the provider hold organization-admin without a
// row in organization_admins.
//
// ref: decision 0006 § "Members and identity providers"
func TestAccSignInEnable(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "initech")
	s := x.s.WithSealer(keyring(t, db))
	p := x.provider(sso.OIDC, "entra", time.Now().UTC().Truncate(time.Second))
	p, err := s.CreateProvider(ctx, p, []byte("client-secret"), byAdmin(x, sso.ActionCreate))
	if err != nil {
		t.Fatal(err)
	}
	rec := signInRecord(x, p, "root", "root-sub")
	e := byAdmin(x, sso.ActionEnable)
	rec.Enable = &e
	root, err := s.SignInIdentity(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Provider(ctx, x.org, p.ID)
	if err != nil || !got.Enabled() || got.AdminLogin != "root" || trail(t, x, sso.ActionEnable) != 1 {
		t.Errorf("enabled provider %+v, %v", got, err)
	}
	var actor string
	if err := db.QueryRow(ctx, `SELECT actor_id FROM organization_audit_trail_entries
		WHERE organization_id = $1 AND action = $2`, x.org, sso.ActionEnable).Scan(&actor); err != nil ||
		actor != root.MemberID.String() {
		t.Errorf("the enable's actor = %q, %v", actor, err)
	}
	if ok, err := s.IsOrganizationAdmin(ctx, x.org, root.MemberID, root.ID); err != nil || !ok {
		t.Errorf("the admin login's session holds organization-admin = %t, %v", ok, err)
	}
	if ok, err := s.IsOrganizationAdmin(ctx, x.org, root.MemberID, kernel.ULID{}); err != nil || ok {
		t.Errorf("the admin login without its session = %t, %v", ok, err)
	}
	carol, err := s.SignInIdentity(ctx, signInRecord(x, p, "carol", "carol-sub"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.IsOrganizationAdmin(ctx, x.org, carol.MemberID, carol.ID); err != nil || ok {
		t.Errorf("another login's session = %t, %v", ok, err)
	}
	var admins int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM organization_admins WHERE organization_id = $1
		AND member_id = $2`, x.org, root.MemberID).Scan(&admins); err != nil || admins != 0 {
		t.Errorf("organization_admins rows = %d, %v", admins, err)
	}
}

// TestAccSignInSeats checks that an identity that creates a member takes a
// seat, checked in the sign-in's transaction, and that a full limit
// creates nothing.
func TestAccSignInSeats(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "globex")
	f := &seats{refuse: &featuresapi.LimitError{Limit: featuresapi.Seats, Scope: featuresapi.ScopeOrganization}}
	s := x.s.WithSealer(keyring(t, db)).WithFeatures(f)
	p := signInProvider(t, x, s)
	if _, err := s.SignInIdentity(ctx, signInRecord(x, p, "dave", "dave-sub")); !errors.As(err, new(*featuresapi.LimitError)) ||
		f.calls != 1 || !f.inTx {
		t.Errorf("a sign-in past the seats limit = %v, %d calls", err, f.calls)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM members WHERE login = 'dave'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("members after a refused sign-in = %d, %v", n, err)
	}
	f.refuse = nil
	if _, err := s.SignInIdentity(ctx, signInRecord(x, p, "dave", "dave-sub")); err != nil {
		t.Fatal(err)
	}
	// A known identity takes no new seat.
	if _, err := s.SignInIdentity(ctx, signInRecord(x, p, "dave", "dave-sub")); err != nil || f.calls != 2 {
		t.Errorf("a known identity's sign-in = %v, %d calls", err, f.calls)
	}
}

// TestAccBackchannelLogout checks a logout token's jti is accepted once
// per provider, and that it revokes the provider's sessions by sid, or by
// subject without one, on every replica.
func TestAccBackchannelLogout(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "umbrella")
	s := x.s.WithSealer(keyring(t, db))
	p := signInProvider(t, x, s)
	l := listen(t, db)
	exp := time.Now().Add(time.Minute)
	if ok, err := s.AcceptLogoutToken(ctx, p.ID, "jti-1", exp); err != nil || !ok {
		t.Errorf("a new jti = %t, %v", ok, err)
	}
	if ok, err := s.AcceptLogoutToken(ctx, p.ID, "jti-1", exp); err != nil || ok {
		t.Errorf("a replayed jti = %t, %v", ok, err)
	}
	if ps, err := s.ProvidersByClientID(ctx, []string{"web", "nobody"}); err != nil || len(ps) != 1 || ps[0].ID != p.ID {
		t.Errorf("ProvidersByClientID = %+v, %v", ps, err)
	}
	a, err := s.SignInIdentity(ctx, signInRecord(x, p, "erin", "erin-sub"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SignInIdentity(ctx, signInRecord(x, p, "frank", "frank-sub"))
	if err != nil {
		t.Fatal(err)
	}
	messages(t, l)
	if ids, err := s.RevokeIdentitySessions(ctx, p.ID, "sid-erin-sub", "ignored"); err != nil || !slices.Equal(ids, []kernel.ULID{a.ID}) {
		t.Errorf("revoke by sid = %v, %v", ids, err)
	}
	if ids, err := s.RevokeIdentitySessions(ctx, p.ID, "", "frank-sub"); err != nil || !slices.Equal(ids, []kernel.ULID{b.ID}) {
		t.Errorf("revoke by subject = %v, %v", ids, err)
	}
	if got := messages(t, l); !slices.Contains(got, session.Key(a.ID)) || !slices.Contains(got, session.Key(b.ID)) {
		t.Errorf("messages = %v", got)
	}
	if n, err := s.ExpireSignIns(ctx, exp.Add(time.Second)); err != nil || n != 1 {
		t.Errorf("ExpireSignIns = %d, %v; want the expired jti", n, err)
	}
	if slug, ok, err := s.SoleOrganization(ctx); err != nil || !ok || slug != "umbrella" {
		t.Errorf("SoleOrganization = %q, %t, %v", slug, ok, err)
	}
	seed(t, db, "other")
	if _, ok, err := s.SoleOrganization(ctx); err != nil || ok {
		t.Errorf("two organizations: %t, %v", ok, err)
	}
	if o, err := s.SignInOrganization(ctx, "umbrella"); err != nil || o.ID != x.org {
		t.Errorf("SignInOrganization = %+v, %v", o, err)
	}
	if _, err := s.SignInOrganization(ctx, "nobody"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown organization = %v", err)
	}
}
