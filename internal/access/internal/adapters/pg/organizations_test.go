package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func credential(t *testing.T, login string) breakglass.Credential {
	t.Helper()
	h, err := breakglass.New("pw")
	if err != nil {
		t.Fatal(err)
	}
	return breakglass.Credential{
		Login:     login,
		Hash:      h,
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
}

// TestAccOrganizationStore checks the operator's writes and reads: create
// with a break-glass credential, list by pages, read by slug, replace and
// remove the credential, each with its trail entry.
func TestAccOrganizationStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	o1, a1 := tenant(t, "orgs-one", "orgs-one")
	bg := credential(t, "breakglass")
	if err := s.CreateOrganization(ctx, o1, a1, &bg, created(o1)); err != nil {
		t.Fatal(err)
	}
	o2, a2 := tenant(t, "orgs-two", "orgs-two-app")
	if err := s.CreateOrganization(ctx, o2, a2, nil, created(o2)); err != nil {
		t.Fatal(err)
	}
	got, err := s.OrganizationBySlug(ctx, o1.Slug)
	if err != nil || got.ID != o1.ID || got.Name != o1.Name || got.CreatedAt.IsZero() ||
		len(got.Accounts) != 1 || got.Accounts[0].ID != a1.ID || got.Accounts[0].Claimed() ||
		got.BreakGlass == nil || got.BreakGlass.Login != "breakglass" ||
		!got.BreakGlass.UpdatedAt.Equal(bg.UpdatedAt) {
		t.Errorf("OrganizationBySlug = %+v, %v", got, err)
	}
	var stored string
	if err := db.QueryRow(ctx, `SELECT password_hash FROM organization_break_glass
		WHERE organization_id = $1`, o1.ID).Scan(&stored); err != nil || stored != bg.Hash.String() {
		t.Errorf("stored hash %q, %v", stored, err)
	}
	// Two pages of one: the IDs above order o1 before o2, and other tests'
	// organizations may come between or after.
	var seen []kernel.OrganizationID
	after := kernel.OrganizationID{}
	for {
		page, err := s.Organizations(ctx, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		seen = append(seen, page[0].ID)
		if page[0].ID == o2.ID && (page[0].BreakGlass != nil || len(page[0].Accounts) != 1) {
			t.Errorf("listed %+v", page[0])
		}
		after = page[0].ID
	}
	if i1, i2 := indexOf(seen, o1.ID), indexOf(seen, o2.ID); i1 < 0 || i2 < i1 {
		t.Errorf("listed %v, want %s before %s", seen, o1.ID, o2.ID)
	}
	replaced := credential(t, "breakglass2")
	if err := s.SetBreakGlass(ctx, o2.ID, replaced, created(o2)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBreakGlass(ctx, o2.ID, replaced, created(o2)); err != nil {
		t.Errorf("replace: %v", err)
	}
	if got, _ := s.OrganizationBySlug(ctx, o2.Slug); got.BreakGlass == nil ||
		got.BreakGlass.Login != "breakglass2" {
		t.Errorf("after set: %+v", got.BreakGlass)
	}
	if err := s.DeleteBreakGlass(ctx, o2.ID, created(o2)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.OrganizationBySlug(ctx, o2.Slug); got.BreakGlass != nil {
		t.Errorf("after delete: %+v", got.BreakGlass)
	}
	var entries int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM organization_audit_trail_entries
		WHERE organization_id = $1`, o2.ID).Scan(&entries); err != nil || entries != 4 {
		t.Errorf("o2's trail has %d entries, %v; want 4", entries, err)
	}
	if err := s.CreateOrganization(
		ctx,
		o1,
		a1,
		nil,
		created(o1),
	); !errors.Is(
		err,
		port.ErrSlugTaken,
	) {
		t.Errorf("create again = %v", err)
	}
	missing := kernel.NewOrganizationID()
	if err := s.SetBreakGlass(ctx, missing, bg, created(o1)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("set on a missing organization = %v", err)
	}
	if _, err := s.OrganizationBySlug(ctx, "orgs-missing"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("read a missing organization = %v", err)
	}
	if _, err := s.DeleteOrganization(
		ctx,
		missing,
		created(o1),
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("delete a missing organization = %v", err)
	}
	// An entry the trail refuses rolls the change back.
	o3, a3 := tenant(t, "orgs-three", "orgs-three")
	if err := s.CreateOrganization(ctx, o3, a3, nil, kernel.OrganizationAuditEntry{}); err == nil {
		t.Error("created with an invalid trail entry")
	}
	if _, err := s.OrganizationBySlug(ctx, o3.Slug); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a rolled-back organization: %v", err)
	}
}

func indexOf(ids []kernel.OrganizationID, id kernel.OrganizationID) int {
	for i, x := range ids {
		if x == id {
			return i
		}
	}
	return -1
}

// TestAccDeleteOrganization checks that deleting an organization deletes
// its members, their identities, its accounts, their API keys and its
// sessions, a session with a parent among them, and revokes each live
// session, leaving another organization's untouched.
func TestAccDeleteOrganization(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	o, a := tenant(t, "doomed", "doomed")
	keep, keepAcct := tenant(t, "kept", "kept")
	if err := s.CreateOrganization(ctx, o, a, nil, created(o)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOrganization(ctx, keep, keepAcct, nil, created(keep)); err != nil {
		t.Fatal(err)
	}
	member := kernel.NewMemberID()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ($1, $2, 'alice', 'Alice', 'alice@example.com', 'active')`, []any{o.ID, member}},
		{`INSERT INTO account_users (account_id, organization_id, member_id)
			VALUES ($1, $2, $3)`, []any{a.ID, o.ID, member}},
		{`INSERT INTO member_identities (organization_id, member_id, identity_provider_id, subject)
			VALUES ($1, $2, gen_random_uuid(), 'alice')`, []any{o.ID, member}},
	} {
		if _, err := db.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	key := apikey.Key{
		OrganizationID: o.ID, AccountID: a.ID, ID: kernel.NewULID(), Name: "ci",
		Type: apikey.TypeSecret, Teams: []string{},
	}
	key.SecretHash, _ = apikey.Hash(apikey.NewSecret())
	if err := s.CreateAPIKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	live := func(org kernel.OrganizationID, acct kernel.AccountID) session.Session {
		return session.Session{
			ID: kernel.NewULID(), OrganizationID: org, AccountID: acct,
			Channel: session.ChannelUnknown, ActorKind: kernel.PrincipalAPIKey, ActorID: key.ID,
			CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}
	}
	parent, child, other := live(o.ID, a.ID), live(o.ID, a.ID), live(keep.ID, keepAcct.ID)
	for _, ss := range []session.Session{parent, child, other} {
		if err := s.CreateSession(ctx, ss); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `UPDATE sessions SET parent_session_id = $1 WHERE id = $2`,
		parent.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := s.DeleteOrganization(ctx, o.ID, created(o))
	if err != nil || len(ids) != 2 {
		t.Fatalf("DeleteOrganization = %v, %v", ids, err)
	}
	for table, where := range map[string]string{
		"organizations":     "id = $1",
		"accounts":          "organization_id = $1",
		"members":           "organization_id = $1",
		"member_identities": "organization_id = $1",
		"account_users":     "organization_id = $1",
		"sessions":          "organization_id = $1",
		"api_keys":          "account_id = $1",
	} {
		var n int
		arg := any(o.ID)
		if table == "api_keys" {
			arg = a.ID
		}
		if err := db.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE `+where, arg).
			Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows left, %v", table, n, err)
		}
	}
	if ss, err := s.Session(ctx, other.ID); err != nil || !ss.RevokedAt.IsZero() {
		t.Errorf("another organization's session: %+v, %v", ss, err)
	}
}

func TestAccOrganizationStoreUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	o, _ := tenant(t, "closed-orgs", "closed-orgs")
	bg := credential(t, "x")
	for name, err := range map[string]error{
		"list":   func() error { _, err := s.Organizations(ctx, kernel.OrganizationID{}, 10); return err }(),
		"read":   func() error { _, err := s.OrganizationBySlug(ctx, o.Slug); return err }(),
		"delete": func() error { _, err := s.DeleteOrganization(ctx, o.ID, created(o)); return err }(),
		"set":    s.SetBreakGlass(ctx, o.ID, bg, created(o)),
		"unset":  s.DeleteBreakGlass(ctx, o.ID, created(o)),
	} {
		if err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}
