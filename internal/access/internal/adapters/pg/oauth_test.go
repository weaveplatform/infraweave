package pg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/oauth"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// oauthRequest records a request of a new client, approved for x's member
// in x's account, and answers it with its code.
func oauthRequest(t *testing.T, x seeded) (oauth.Request, string) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	c := oauth.Client{
		ID: kernel.NewULID(), Name: "Editor", RedirectURIs: []string{"http://127.0.0.1/cb"},
		CreatedAt: now,
	}
	if err := x.s.CreateClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := oauth.Request{
		ID: kernel.NewULID(), Client: c, RedirectURI: "http://127.0.0.1:9/cb",
		Resource: "https://iw.example.com/mcp", Scopes: []string{"mcp:read"}, State: "s",
		Challenge: oauth.Challenge("v"), CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	if err := x.s.CreateRequest(ctx, r); err != nil {
		t.Fatal(err)
	}
	code, hash := session.NewSecret()
	r.Decided, r.OrganizationID, r.AccountID, r.MemberID = true, x.org, x.acct, x.member
	r.CodeHash, r.CodeExpiresAt = hash, now.Add(time.Minute)
	if err := x.s.Decide(ctx, r, time.Now()); err != nil {
		t.Fatal(err)
	}
	return r, code
}

// grantOf is the grant a code of r builds, whose refresh token hashes to
// hash.
func grantOf(r oauth.Request, o oauth.Owner, hash []byte) session.Session {
	return session.Session{
		ID: kernel.NewULID(), OrganizationID: r.OrganizationID, AccountID: r.AccountID,
		Channel: session.ChannelUnknown, ActorKind: kernel.PrincipalUser,
		ActorID: r.MemberID.ULID, MemberID: r.MemberID, Login: o.Login, Name: o.Name,
		Teams: o.Teams, Scopes: r.Scopes, ExpiresAt: time.Now().Add(time.Hour),
		SecretHash: hash, ClientID: r.Client.ID.String(), Resource: r.Resource,
	}
}

func TestAccOAuthRequests(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "oauth-requests")

	if _, err := x.s.Client(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown client: %v", err)
	}
	if _, err := x.s.Request(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown request: %v", err)
	}
	r, _ := oauthRequest(t, x)
	c, err := x.s.Client(ctx, r.Client.ID)
	if err != nil || c.Name != "Editor" || !slices.Equal(c.RedirectURIs, r.Client.RedirectURIs) ||
		!c.CreatedAt.Equal(r.Client.CreatedAt) {
		t.Errorf("client = %+v, %v", c, err)
	}
	got, err := x.s.Request(ctx, r.ID)
	if err != nil || !got.Decided || got.AccountID != x.acct || got.MemberID != x.member ||
		got.OrganizationID != x.org || got.Client.ID != c.ID || got.State != "s" ||
		got.Challenge != r.Challenge || !slices.Equal(got.CodeHash, r.CodeHash) ||
		!got.CodeExpiresAt.Equal(r.CodeExpiresAt) || got.RedirectURI != r.RedirectURI ||
		!slices.Equal(got.Scopes, r.Scopes) {
		t.Errorf("request = %+v, %v", got, err)
	}
	if err := x.s.Decide(ctx, oauth.Request{ID: r.ID}, time.Now()); !errors.Is(
		err, port.ErrNotFound) {
		t.Errorf("a second decision: %v", err)
	}

	late := r
	late.ID, late.CodeHash = kernel.NewULID(), nil
	if err := x.s.CreateRequest(ctx, late); err != nil {
		t.Fatal(err)
	}
	if err := x.s.Decide(ctx, oauth.Request{ID: late.ID},
		r.ExpiresAt); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a decision at the request's end: %v", err)
	}
	if err := x.s.Decide(ctx, oauth.Request{ID: late.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if denied, err := x.s.Request(ctx, late.ID); err != nil || !denied.Decided ||
		!denied.AccountID.IsZero() || denied.CodeHash != nil {
		t.Errorf("a denied request = %+v, %v", denied, err)
	}

	if hosts, err := x.s.RedirectHosts(ctx, x.acct); err != nil || len(hosts) != 0 {
		t.Errorf("an account's default hosts = %v, %v", hosts, err)
	}
	if err := x.s.SetRedirectHosts(ctx, x.acct, []string{"*.example.com"}); err != nil {
		t.Fatal(err)
	}
	if hosts, err := x.s.RedirectHosts(ctx, x.acct); err != nil ||
		!slices.Equal(hosts, []string{"*.example.com"}) {
		t.Errorf("hosts = %v, %v", hosts, err)
	}

	if _, err := db.Exec(ctx, `UPDATE oauth_clients SET last_used_at = now() - interval '31 days'
		WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := x.s.ExpireOAuth(ctx, time.Now().Add(-time.Hour),
		time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.Client(ctx, c.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unused client survives expiry: %v", err)
	}
	if _, err := x.s.Request(ctx, r.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unused client's request survives expiry: %v", err)
	}
	kept, _ := oauthRequest(t, x)
	if err := x.s.ExpireOAuth(ctx, kept.ExpiresAt.Add(time.Second),
		time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.Request(ctx, kept.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an ended request survives expiry: %v", err)
	}
	if _, err := x.s.Client(ctx, kept.Client.ID); err != nil {
		t.Errorf("a client used within the hour is expired: %v", err)
	}
}

func TestAccOAuthGrants(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "oauth-grants")
	admit(t, x, x.member, "dev")

	r, code := oauthRequest(t, x)
	refused := errors.New("refused")
	if _, err := x.s.Redeem(ctx, session.HashSecret(code),
		func(oauth.Request, oauth.Owner) (session.Session, error) {
			return session.Session{}, refused
		}); !errors.Is(err, refused) {
		t.Errorf("a refused redemption: %v", err)
	}
	if _, err := x.s.Redeem(ctx, session.HashSecret("unknown"),
		nil); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown code: %v", err)
	}
	_, hash := session.NewSecret()
	var owner oauth.Owner
	g, err := x.s.Redeem(ctx, session.HashSecret(code),
		func(rr oauth.Request, o oauth.Owner) (session.Session, error) {
			if rr.ID != r.ID {
				t.Errorf("redeemed request %s, want %s", rr.ID, r.ID)
			}
			owner = o
			return grantOf(rr, o, hash), nil
		})
	if err != nil || owner.Login != "alice" || owner.Name != "Alice" ||
		!slices.Equal(owner.Teams, []string{"dev"}) {
		t.Fatalf("redeem = %+v for %+v, %v", g, owner, err)
	}
	read, err := x.s.Session(ctx, g.ID)
	if err != nil || read.ClientID != r.Client.ID.String() || read.Resource != r.Resource ||
		!slices.Equal(read.SecretHash, hash) || !slices.Equal(read.Scopes, r.Scopes) {
		t.Errorf("the grant's row = %+v, %v", read, err)
	}
	if browser, err := x.s.SessionBySecret(ctx, hash); err != nil ||
		browser.Channel == session.ChannelWeb {
		t.Errorf("a refresh token reads as a browser session: %+v, %v", browser, err)
	}

	next, nextHash := session.NewSecret()
	rotated, err := x.s.Refresh(ctx, hash,
		func(ss session.Session, o oauth.Owner) (session.Session, error) {
			if ss.ID != g.ID || o.Login != "alice" {
				t.Errorf("rotating %+v for %+v", ss, o)
			}
			ss.SecretHash, ss.ExpiresAt = nextHash, ss.ExpiresAt.Add(time.Hour)
			ss.Name = "Alice B"
			return ss, nil
		})
	if err != nil || rotated.ID != g.ID {
		t.Fatalf("refresh = %+v, %v", rotated, err)
	}
	read, err = x.s.Session(ctx, g.ID)
	if err != nil || !slices.Equal(read.SecretHash, nextHash) || read.Name != "Alice B" ||
		!read.RevokedAt.IsZero() {
		t.Errorf("the rotated grant = %+v, %v", read, err)
	}
	if _, err := x.s.Refresh(ctx, nextHash,
		func(session.Session, oauth.Owner) (session.Session, error) {
			return session.Session{}, refused
		}); !errors.Is(err, refused) {
		t.Errorf("a refused rotation: %v", err)
	}
	if _, err := x.s.Refresh(ctx, session.HashSecret("unknown"),
		nil); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown refresh token: %v", err)
	}
	revoked, err := x.s.Refresh(ctx, hash, nil)
	if !errors.Is(err, oauth.ErrReused) || revoked.ID != g.ID || revoked.RevokedAt.IsZero() {
		t.Errorf("a spent refresh token = %+v, %v", revoked, err)
	}
	if _, err := x.s.Refresh(ctx, session.HashSecret(next), nil); !errors.Is(
		err, port.ErrNotFound) {
		t.Errorf("the revoked grant's current token: %v", err)
	}
	// The grant is revoked already, so the replay revokes nothing more.
	if again, err := x.s.Redeem(ctx, session.HashSecret(code), nil); !errors.Is(
		err, oauth.ErrReused) || !again.ID.IsZero() {
		t.Errorf("a code redeemed twice = %+v, %v", again, err)
	}
	r4, code4 := oauthRequest(t, x)
	_, hash4 := session.NewSecret()
	g4, err := x.s.Redeem(ctx, session.HashSecret(code4),
		func(rr oauth.Request, o oauth.Owner) (session.Session, error) {
			return grantOf(rr, o, hash4), nil
		})
	if err != nil || r4.ID.IsZero() {
		t.Fatal(err)
	}
	if replay, err := x.s.Redeem(ctx, session.HashSecret(code4), nil); !errors.Is(
		err, oauth.ErrReused) || replay.ID != g4.ID || replay.RevokedAt.IsZero() {
		t.Errorf("a live grant's code redeemed twice = %+v, %v", replay, err)
	}

	// A member the account no longer admits holds no grant.
	r2, code2 := oauthRequest(t, x)
	_, hash2 := session.NewSecret()
	g2, err := x.s.Redeem(ctx, session.HashSecret(code2),
		func(rr oauth.Request, o oauth.Owner) (session.Session, error) {
			return grantOf(rr, o, hash2), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	_, code3 := oauthRequest(t, x)
	if _, err := db.Exec(ctx, `DELETE FROM account_users WHERE account_id = $1 AND member_id = $2`,
		x.acct, x.member); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.Redeem(ctx, session.HashSecret(code3),
		nil); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a code of a removed member: %v", err)
	}
	gone, err := x.s.Refresh(ctx, hash2, nil)
	if !errors.Is(err, port.ErrNotFound) || gone.ID != g2.ID || gone.RevokedAt.IsZero() {
		t.Errorf("a removed member's grant = %+v, %v", gone, err)
	}
	if r2.ID.IsZero() {
		t.Fatal("no request")
	}

	// A spent code whose grant is gone revokes nothing and is still spent.
	if _, err := db.Exec(ctx, `UPDATE oauth_requests SET session_id = NULL WHERE id = $1`,
		r2.ID); err != nil {
		t.Fatal(err)
	}
	if lost, err := x.s.Redeem(ctx, session.HashSecret(code2), nil); !errors.Is(
		err, oauth.ErrReused) || !lost.ID.IsZero() {
		t.Errorf("a spent code with no grant = %+v, %v", lost, err)
	}
}
