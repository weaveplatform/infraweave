package pg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/linking"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// newLink is an open link of x's organization merging other into m.
func newLink(x seeded, m, other kernel.MemberID) linking.Link {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return linking.Link{
		OrganizationID: x.org, ID: kernel.NewULID(), MemberID: m, OtherMemberID: other,
		CreatedBy: "breakglass:" + x.org.String(), CreatedAt: now, ExpiresAt: now.Add(linking.TTL),
	}
}

// signedIn signs the identity subject in through p, for link when it is
// not zero, from a browser holding prover's session.
func signedIn(t *testing.T, s *pg.Store, x seeded, p sso.Provider, login, subject string, link kernel.ULID, prover kernel.MemberID) (session.Session, error) {
	t.Helper()
	rec := signInRecord(x, p, login, subject)
	rec.Link, rec.Prover = link, prover
	return s.SignInIdentity(t.Context(), rec)
}

func countOf(t *testing.T, x seeded, query string, args ...any) int {
	t.Helper()
	var n int
	if err := x.db.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func exec(t *testing.T, x seeded, query string, args ...any) {
	t.Helper()
	if _, err := x.db.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// TestAccCreateLink checks a link's refusals under the organization's
// lock: a member of another organization or none, one member twice, a
// deactivated member and one without an identity, and a second open link
// of the pair in either order, while an expired one is replaced.
//
// ref: decision 0006 § "Members and identity providers"
func TestAccCreateLink(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "umbrella")
	other := seed(t, db, "aperture")
	s := x.s.WithSealer(keyring(t, db))
	p := signInProvider(t, x, s)
	ada, err := signedIn(t, s, x, p, "ada", "ada-sub", kernel.ULID{}, kernel.MemberID{})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := signedIn(t, s, x, p, "bob", "bob-sub", kernel.ULID{}, kernel.MemberID{})
	if err != nil {
		t.Fatal(err)
	}
	a, b := ada.MemberID, bob.MemberID
	none := addMember(t, x, "carol", "active")
	gone := addMember(t, x, "dan", "deactivated")
	exec(t, x, `INSERT INTO member_identities (organization_id, member_id, identity_provider_id, subject)
		VALUES ($1, $2, $3, 'dan-sub')`, x.org, gone, p.ID)

	for name, c := range map[string]struct {
		m, o  kernel.MemberID
		want  error
		field string
	}{
		"another organization's": {a, other.member, port.ErrNotFound, ""},
		"no member":              {kernel.NewMemberID(), b, port.ErrNotFound, ""},
		"one member twice":       {a, a, organization.ErrInvalid, "otherMemberId"},
		"a deactivated member":   {gone, b, organization.ErrInvalid, "memberId"},
		"no identity":            {a, none, organization.ErrInvalid, "otherMemberId"},
	} {
		err := s.CreateLink(ctx, newLink(x, c.m, c.o), byAdmin(x, linking.ActionCreate))
		var fe *organization.FieldError
		if !errors.Is(err, c.want) || (c.field != "" && (!errors.As(err, &fe) || fe.Field != c.field)) {
			t.Errorf("%s = %v", name, err)
		}
	}
	if n := trail(t, x, linking.ActionCreate); n != 0 {
		t.Errorf("refused links recorded %d entries", n)
	}

	l := newLink(x, a, b)
	if err := s.CreateLink(ctx, l, byAdmin(x, linking.ActionCreate)); err != nil {
		t.Fatal(err)
	}
	if got, slug, err := s.Link(ctx, l.ID); err != nil || slug != "umbrella" || got.ID != l.ID ||
		got.MemberID != a || got.OtherMemberID != b || got.CreatedBy != l.CreatedBy ||
		!got.CreatedAt.Equal(l.CreatedAt) || !got.ExpiresAt.Equal(l.ExpiresAt) || !got.CompletedAt.IsZero() {
		t.Errorf("Link = %+v, %s, %v; want %+v", got, slug, err, l)
	}
	if _, _, err := s.Link(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an unknown link = %v", err)
	}
	for _, pair := range [][2]kernel.MemberID{{a, b}, {b, a}} {
		if err := s.CreateLink(ctx, newLink(x, pair[0], pair[1]), byAdmin(x, linking.ActionCreate)); !errors.Is(err, linking.ErrLinkExists) {
			t.Errorf("a second open link %v = %v", pair, err)
		}
	}
	exec(t, x, `UPDATE member_links SET created_at = created_at - interval '2 hours',
		expires_at = expires_at - interval '2 hours' WHERE id = $1`, l.ID)
	again := newLink(x, b, a)
	if err := s.CreateLink(ctx, again, byAdmin(x, linking.ActionCreate)); err != nil {
		t.Fatalf("a link replacing an expired one = %v", err)
	}
	if _, _, err := s.Link(ctx, l.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("the expired link = %v", err)
	}
	if n := trail(t, x, linking.ActionCreate); n != 2 {
		t.Errorf("%d link entries, want 2", n)
	}
	if fs, err := s.Identities(ctx, x.org, a); err != nil || len(fs) != 1 || fs[0].Subject != "ada-sub" ||
		fs[0].Kind != sso.OIDC || fs[0].Login != "ada" || fs[0].ProviderID != p.ID {
		t.Errorf("ada's identities = %+v, %v", fs, err)
	}

	// The sign-in's expiry deletes a link that expired uncompleted.
	exec(t, x, `UPDATE member_links SET expires_at = now() - interval '1 minute' WHERE id = $1`, again.ID)
	if _, err := s.ExpireSignIns(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link(ctx, again.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("an expired link after the expiry = %v", err)
	}
}

// TestAccMemberMerge checks a link's two sign-ins and the merge's conflict
// handling: the other member's identities, admissions, invitations, user
// bindings, personal keys and organization role move to the member, the
// member's own row winning where both hold one; the other member's
// sessions are revoked on every replica and dropped from this one once the
// merge commits; its row goes, and with it a seat; and the browser holds a
// session of the member. A sign-in of a third identity, from another
// browser or after the link ended is refused and leaves no session.
//
// ref: decision 0006 § "Members and identity providers"
func TestAccMemberMerge(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "initech")
	var dropped []kernel.ULID
	s := x.s.WithSealer(keyring(t, db)).WithSessionInvalidations(func(id kernel.ULID) {
		dropped = append(dropped, id)
	})
	p := signInProvider(t, x, s)
	ada, err := signedIn(t, s, x, p, "ada", "ada-sub", kernel.ULID{}, kernel.MemberID{})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := signedIn(t, s, x, p, "bob", "bob-sub", kernel.ULID{}, kernel.MemberID{})
	if err != nil {
		t.Fatal(err)
	}
	a, b := ada.MemberID, bob.MemberID

	// Both hold the account and organization-admin; each holds one binding
	// the other holds too and bob one of his own; bob holds a personal key
	// and an invitation.
	admit(t, x, a, "dev")
	admit(t, x, b, "ops")
	exec(t, x, `INSERT INTO organization_admins (organization_id, member_id, created_by)
		VALUES ($1, $2, 'test'), ($1, $3, 'test')`, x.org, a, b)
	var top kernel.ULID
	if err := db.QueryRow(ctx, `SELECT id FROM spaces WHERE account_id = $1 AND parent_id IS NULL`, x.acct).
		Scan(&top); err != nil {
		t.Fatal(err)
	}
	var roles []kernel.ULID
	rows, err := db.Query(ctx, `SELECT id FROM roles WHERE account_id = $1 ORDER BY id LIMIT 2`, x.acct)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id kernel.ULID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, id)
	}
	if len(roles) != 2 {
		t.Fatalf("roles %v", roles)
	}
	bind := `INSERT INTO role_bindings (account_id, id, actor_kind, actor_id, role_id, space_id, origin, created_by)
		VALUES ($1, gen_random_uuid(), 'user', $2, $3, $4, 'binding', 'test')`
	exec(t, x, bind, x.acct, a, roles[0], top)
	exec(t, x, bind, x.acct, b, roles[0], top)
	exec(t, x, bind, x.acct, b, roles[1], top)
	key := personal(x, b)
	exec(t, x, `INSERT INTO api_keys (account_id, id, name, type, secret_hash, teams, creator, owner_user_id,
		is_personal) VALUES ($1, $2, 'laptop', 'SECRET', $3, '{}', 'bob', $4, true)`, x.acct, key.ID, key.SecretHash, b)
	exec(t, x, `INSERT INTO account_invitations (account_id, id, member_id, created_at, sent_at)
		VALUES ($1, gen_random_uuid(), $2, now(), now())`, x.acct, b)

	l := newLink(x, a, b)
	if err := s.CreateLink(ctx, l, byAdmin(x, linking.ActionCreate)); err != nil {
		t.Fatal(err)
	}
	// An identity that names no member, and one of a third member, prove
	// nothing.
	if _, err := signedIn(t, s, x, p, "eve", "eve-sub", l.ID, kernel.MemberID{}); !errors.Is(err, linking.ErrNotLinked) {
		t.Errorf("a new identity's link sign-in = %v", err)
	}
	if _, err := signedIn(t, s, x, p, "carl", "carl-sub", kernel.ULID{}, kernel.MemberID{}); err != nil {
		t.Fatal(err)
	}
	if _, err := signedIn(t, s, x, p, "carl", "carl-sub", l.ID, kernel.MemberID{}); !errors.Is(err, linking.ErrNotLinked) {
		t.Errorf("a third member's link sign-in = %v", err)
	}

	members := `SELECT count(*) FROM members WHERE organization_id = $1 AND status <> 'deactivated'`
	before := countOf(t, x, members, x.org)
	l1 := listen(t, db)
	first, err := signedIn(t, s, x, p, "bob", "bob-sub", l.ID, kernel.MemberID{})
	if err != nil || first.MemberID != b {
		t.Fatalf("bob's link sign-in = %+v, %v", first, err)
	}
	if got, _, _ := s.Link(ctx, l.ID); got.OtherMemberSignedAt.IsZero() || !got.MemberSignedAt.IsZero() {
		t.Errorf("after bob's sign-in: %+v", got)
	}
	messages(t, l1)
	if _, err := signedIn(t, s, x, p, "ada", "ada-sub", l.ID, kernel.NewMemberID()); !errors.Is(err, linking.ErrOtherBrowser) {
		t.Errorf("ada's sign-in from another browser = %v", err)
	}
	merged, err := signedIn(t, s, x, p, "ada", "ada-sub", l.ID, b)
	if err != nil || merged.MemberID != a {
		t.Fatalf("ada's link sign-in = %+v, %v", merged, err)
	}

	got, _, err := s.Link(ctx, l.ID)
	if err != nil || got.CompletedAt.IsZero() || got.MemberSignedAt.IsZero() {
		t.Errorf("the completed link = %+v, %v", got, err)
	}
	if n := countOf(t, x, `SELECT count(*) FROM members WHERE id = $1`, b); n != 0 {
		t.Error("bob's member remains")
	}
	if n := countOf(t, x, members, x.org); n != before-1 {
		t.Errorf("%d live members after the merge, want %d", n, before-1)
	}
	if fs, err := s.Identities(ctx, x.org, a); err != nil || len(fs) != 2 {
		t.Errorf("ada's identities = %+v, %v", fs, err)
	}
	var groups []string
	if err := db.QueryRow(ctx, `SELECT latest_groups FROM account_users WHERE account_id = $1 AND member_id = $2`,
		x.acct, a).Scan(&groups); err != nil || !slices.Equal(groups, []string{"dev"}) {
		t.Errorf("ada's admission = %v, %v; want her own", groups, err)
	}
	for what, c := range map[string]struct {
		query string
		want  int
	}{
		"admissions":   {`SELECT count(*) FROM account_users WHERE member_id = $1`, 1},
		"bindings":     {`SELECT count(*) FROM role_bindings WHERE actor_kind = 'user' AND actor_id = $1`, 2},
		"keys":         {`SELECT count(*) FROM api_keys WHERE owner_user_id = $1`, 1},
		"admin":        {`SELECT count(*) FROM organization_admins WHERE member_id = $1`, 1},
		"invitations":  {`SELECT count(*) FROM account_invitations WHERE member_id = $1`, 0},
		"live session": {`SELECT count(*) FROM sessions WHERE member_id = $1 AND revoked_at IS NULL`, 1},
	} {
		if n := countOf(t, x, c.query, a); n != c.want {
			t.Errorf("ada's %s: %d, want %d", what, n, c.want)
		}
	}
	if n := countOf(t, x, `SELECT count(*) FROM role_bindings WHERE actor_id = $1`, b); n != 0 {
		t.Errorf("bob still holds %d bindings", n)
	}
	if got := messages(t, l1); !slices.Contains(got, session.Key(first.ID)) {
		t.Errorf("messages = %v, want bob's session's", got)
	}
	if !slices.Contains(dropped, first.ID) {
		t.Errorf("dropped %v, want bob's session", dropped)
	}
	if read, err := s.Session(ctx, first.ID); err != nil || read.Live(time.Now()) {
		t.Errorf("bob's session = %+v, %v", read, err)
	}
	if n := trail(t, x, linking.ActionMerge); n != 1 {
		t.Errorf("%d merge entries", n)
	}

	// A completed link proves nothing more.
	if _, err := signedIn(t, s, x, p, "ada", "ada-sub", l.ID, a); !errors.Is(err, linking.ErrEnded) {
		t.Errorf("a sign-in for a completed link = %v", err)
	}
	if _, err := signedIn(t, s, x, p, "ada", "ada-sub", kernel.NewULID(), a); !errors.Is(err, linking.ErrEnded) {
		t.Errorf("a sign-in for no link = %v", err)
	}
}

// TestAccUnlinkIdentities checks an unlink: it removes the member's
// identities of the kind but the one the session signed in through and
// revokes the sessions signed in through them, with its trail entry, and
// refuses to remove the session's own identity alone and a kind the member
// holds none of.
//
// ref: decision 0006 § "Members and identity providers"
func TestAccUnlinkIdentities(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "globex")
	s := x.s.WithSealer(keyring(t, db))
	p := signInProvider(t, x, s)
	old, err := signedIn(t, s, x, p, "ada", "ada-sub", kernel.ULID{}, kernel.MemberID{})
	if err != nil {
		t.Fatal(err)
	}
	m := old.MemberID
	exec(t, x, `INSERT INTO member_identities (organization_id, member_id, identity_provider_id, subject, login)
		VALUES ($1, $2, $3, 'ada-work', 'ada.w')`, x.org, m, p.ID)
	current := signInRecord(x, p, "ada", "ada-work")
	ss, err := s.SignInIdentity(ctx, current)
	if err != nil || ss.MemberID != m {
		t.Fatalf("the second identity's sign-in = %+v, %v", ss, err)
	}
	// A live session through the identity unlinked, beside the current one.
	exec(t, x, `UPDATE sessions SET revoked_at = NULL WHERE id = $1`, old.ID)
	l := listen(t, db)

	left, revoked, err := s.UnlinkIdentities(ctx, x.org, m, ss.ID, sso.OIDC, byAdmin(x, linking.ActionUnlink))
	if err != nil || len(left) != 1 || left[0].Subject != "ada-work" || left[0].Login != "ada.w" ||
		!slices.Equal(revoked, []kernel.ULID{old.ID}) {
		t.Fatalf("UnlinkIdentities = %+v, %v, %v", left, revoked, err)
	}
	if got := messages(t, l); !slices.Equal(got, keys(old.ID)) {
		t.Errorf("messages = %v", got)
	}
	if read, err := s.Session(ctx, ss.ID); err != nil || !read.Live(time.Now()) {
		t.Errorf("the current session = %+v, %v", read, err)
	}
	if n := trail(t, x, linking.ActionUnlink); n != 1 {
		t.Errorf("%d unlink entries", n)
	}
	var fe *organization.FieldError
	if _, _, err := s.UnlinkIdentities(ctx, x.org, m, ss.ID, sso.OIDC, byAdmin(x, linking.ActionUnlink)); !errors.As(err, &fe) {
		t.Errorf("the session's own identity alone = %v", err)
	}
	if _, _, err := s.UnlinkIdentities(ctx, x.org, m, kernel.NewULID(), sso.SAML, byAdmin(x, linking.ActionUnlink)); !errors.Is(err, linking.ErrNoIdentity) {
		t.Errorf("a kind ada holds none of = %v", err)
	}
	if n := trail(t, x, linking.ActionUnlink); n != 1 {
		t.Errorf("refused unlinks recorded: %d entries", n)
	}
}
