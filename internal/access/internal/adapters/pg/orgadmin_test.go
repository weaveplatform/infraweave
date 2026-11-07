package pg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// byAdmin is a trail entry of the tag's action on x's organization.
func byAdmin(x seeded, action string) kernel.OrganizationAuditEntry {
	return kernel.OrganizationAuditEntry{
		OrganizationID:   x.org,
		ActorKind:        kernel.PrincipalBreakGlass,
		ActorID:          x.org.String(),
		Action:           action,
		EventType:        "UPDATE",
		AffectedResource: kernel.AuditResource{Type: orgadmin.ResourceAccount, ID: "x"},
	}
}

// trail counts x's organization's trail entries of action.
func trail(t *testing.T, x seeded, action string) int {
	t.Helper()
	var n int
	if err := x.db.QueryRow(t.Context(), `SELECT count(*) FROM organization_audit_trail_entries
		WHERE organization_id = $1 AND action = $2`, x.org, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// addMember adds a member of x's organization with status and returns it.
func addMember(t *testing.T, x seeded, login, status string) kernel.MemberID {
	t.Helper()
	m := kernel.NewMemberID()
	if _, err := x.db.Exec(t.Context(), `INSERT INTO members (organization_id, id, login, name,
		email, status) VALUES ($1, $2, $3, $3, $3 || '@example.com', $4)`,
		x.org, m, login, status); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAccOrganizationAdmins(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "initech")
	other := seed(t, db, "initrode")
	s := x.s
	bob := addMember(t, x, "bob", "deactivated")

	if ok, err := s.IsOrganizationAdmin(ctx, x.org, x.member); err != nil || ok {
		t.Errorf("before a grant = %t, %v", ok, err)
	}
	for range 2 {
		if err := s.SetOrganizationAdmin(ctx, x.org, x.member, true, "break_glass:x",
			byAdmin(x, orgadmin.ActionAdminGrant)); err != nil {
			t.Fatal(err)
		}
	}
	var by string
	if err := db.QueryRow(ctx, `SELECT created_by FROM organization_admins
		WHERE organization_id = $1 AND member_id = $2`, x.org, x.member).Scan(&by); err != nil ||
		by != "break_glass:x" {
		t.Errorf("created_by = %q, %v", by, err)
	}
	if ok, err := s.IsOrganizationAdmin(ctx, x.org, x.member); err != nil || !ok {
		t.Errorf("after a grant = %t, %v", ok, err)
	}
	// The role is the organization's: it is nothing in another one.
	if ok, err := s.IsOrganizationAdmin(ctx, other.org, x.member); err != nil || ok {
		t.Errorf("in another organization = %t, %v", ok, err)
	}
	if err := s.SetOrganizationAdmin(ctx, other.org, x.member, true, "x",
		byAdmin(other, orgadmin.ActionAdminGrant)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("grant another organization's member = %v", err)
	}
	if n := trail(t, x, orgadmin.ActionAdminGrant); n != 2 {
		t.Errorf("grants on the trail = %d", n)
	}

	ms, err := s.Members(ctx, x.org, kernel.MemberID{}, 10)
	if err != nil || len(ms) != 2 {
		t.Fatalf("members = %+v, %v", ms, err)
	}
	for _, m := range ms {
		alice := m.ID == x.member
		if m.Admin != alice || (m.Status == member.Active) != alice ||
			(alice && m.Login != "alice") {
			t.Errorf("member = %+v", m)
		}
	}
	first, err := s.Members(ctx, x.org, kernel.MemberID{}, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	rest, err := s.Members(ctx, x.org, first[0].ID, 10)
	if err != nil || len(rest) != 1 || rest[0].ID == first[0].ID {
		t.Errorf("second page = %+v, %v", rest, err)
	}
	if m, err := s.Member(
		ctx,
		x.org,
		bob,
	); err != nil || m.Status != member.Deactivated ||
		m.Login != "bob" {
		t.Errorf("bob = %+v, %v", m, err)
	}
	if _, err := s.Member(ctx, other.org, bob); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("another organization's member = %v", err)
	}

	if err := s.SetOrganizationAdmin(ctx, x.org, x.member, false, "x",
		byAdmin(x, orgadmin.ActionAdminRevoke)); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.IsOrganizationAdmin(ctx, x.org, x.member); err != nil || ok {
		t.Errorf("after a revoke = %t, %v", ok, err)
	}

	o, err := s.RenameOrganization(ctx, x.org, "Initech Global", byAdmin(x, orgadmin.ActionUpdate))
	if err != nil || o.Name != "Initech Global" || o.Slug != "initech" {
		t.Errorf("rename = %+v, %v", o, err)
	}
	if _, err := s.RenameOrganization(ctx, kernel.NewOrganizationID(), "x",
		byAdmin(x, orgadmin.ActionUpdate)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("rename no organization = %v", err)
	}
}

// TestAccClaim checks decision 0007's claim: a single transition from
// unclaimed to claimed that admits the named member, which must be an
// active member of the organization, and that the seats limit checks.
func TestAccClaim(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "hooli-xyz")
	other := seed(t, db, "endframe")
	f := &seats{}
	s := pg.New(db).WithFeatures(f)
	bob := addMember(t, x, "bob", "deactivated")
	at := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	claim := byAdmin(x, orgadmin.ActionAccountClaim)

	for name, m := range map[string]kernel.MemberID{
		"a deactivated member":          bob,
		"no member":                     kernel.NewMemberID(),
		"another organization's member": other.member,
	} {
		if _, err := s.ClaimAccount(
			ctx,
			x.org,
			x.acct,
			m,
			at,
			claim,
		); !errors.Is(
			err,
			orgadmin.ErrNoAdmin,
		) {
			t.Errorf("claim for %s = %v", name, err)
		}
	}
	f.refuse = &featuresapi.LimitError{
		Limit: featuresapi.Seats,
		Scope: featuresapi.ScopeOrganization,
	}
	if _, err := s.ClaimAccount(
		ctx,
		x.org,
		x.acct,
		x.member,
		at,
		claim,
	); !errors.Is(
		err,
		f.refuse,
	) {
		t.Errorf("claim past the seats = %v", err)
	}
	if !f.inTx || f.usage != (featuresapi.Usage{}) {
		t.Errorf("seats checked in a transaction %t, counting %+v", f.inTx, f.usage)
	}
	f.refuse = nil
	a, err := s.ClaimAccount(ctx, x.org, x.acct, x.member, at, claim)
	if err != nil || !a.ClaimedAt.Equal(at) || a.ID != x.acct {
		t.Fatalf("claim = %+v, %v", a, err)
	}
	if _, err := s.ClaimAccount(
		ctx,
		x.org,
		x.acct,
		x.member,
		at,
		claim,
	); !errors.Is(
		err,
		orgadmin.ErrInvalidState,
	) {
		t.Errorf("a second claim = %v", err)
	}
	if _, err := s.ClaimAccount(
		ctx,
		other.org,
		x.acct,
		other.member,
		at,
		claim,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("claim another organization's account = %v", err)
	}
	if _, ok, err := s.Admits(ctx, x.org, x.member, x.acct); err != nil || !ok {
		t.Errorf("the claimed account admits its admin %t, %v", ok, err)
	}
	var version int64
	if err := db.QueryRow(ctx, `SELECT authz_version FROM accounts WHERE id = $1`, x.acct).
		Scan(&version); err != nil || version != 1 {
		t.Errorf("authz_version = %d, %v", version, err)
	}
	if n := trail(t, x, orgadmin.ActionAccountClaim); n != 1 {
		t.Errorf("claims on the trail = %d", n)
	}

	// Alice holds her seat through the claimed account, so a second
	// account admitting her does not grow the organization's count.
	b, err := account.New(x.org, "", "hooli-two", "Hooli Two")
	if err != nil {
		t.Fatal(err)
	}
	b.CreatedAt, b.ClaimedAt = at, at
	if err := s.CreateAccount(
		ctx,
		b,
		x.member,
		byAdmin(x, orgadmin.ActionAccountCreate),
	); err != nil {
		t.Fatal(err)
	}
	if f.usage != (featuresapi.Usage{Organization: 0, Account: 0}) {
		t.Errorf("a seated member's admission counted %+v", f.usage)
	}
	got, err := s.AccountBySlug(ctx, x.org, "hooli-two")
	if err != nil || !got.Claimed() || got.Name != "Hooli Two" {
		t.Errorf("created = %+v, %v", got, err)
	}
	if _, ok, err := s.Admits(ctx, x.org, x.member, b.ID); err != nil || !ok {
		t.Errorf("the created account admits its admin %t, %v", ok, err)
	}
	if _, err := s.AccountBySlug(ctx, other.org, "hooli-two"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("another organization's account by slug = %v", err)
	}
	dup := b
	dup.ID = kernel.NewAccountID()
	if err := s.CreateAccount(ctx, dup, x.member,
		byAdmin(x, orgadmin.ActionAccountCreate)); !errors.Is(err, port.ErrSlugTaken) {
		t.Errorf("a slug in use = %v", err)
	}
	c, _ := account.New(x.org, "", "hooli-three", "Hooli Three")
	c.CreatedAt, c.ClaimedAt = at, at
	if err := s.CreateAccount(ctx, c, bob,
		byAdmin(x, orgadmin.ActionAccountCreate)); !errors.Is(err, orgadmin.ErrNoAdmin) {
		t.Errorf("create for a deactivated member = %v", err)
	}
	if _, err := s.AccountBySlug(ctx, x.org, "hooli-three"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a refused create left %v", err)
	}

	page, err := s.OrganizationAccounts(ctx, x.org, kernel.AccountID{}, 1)
	if err != nil || len(page) != 1 || page[0].ID != x.acct {
		t.Errorf("first page = %+v, %v", page, err)
	}
	page, err = s.OrganizationAccounts(ctx, x.org, x.acct, 10)
	if err != nil || len(page) != 1 || page[0].ID != b.ID {
		t.Errorf("second page = %+v, %v", page, err)
	}

	r, err := s.RenameAccount(
		ctx,
		x.org,
		b.ID,
		"Hooli Deux",
		byAdmin(x, orgadmin.ActionAccountUpdate),
	)
	if err != nil || r.Name != "Hooli Deux" || r.Slug != "hooli-two" {
		t.Errorf("rename = %+v, %v", r, err)
	}
	if _, err := s.RenameAccount(ctx, other.org, b.ID, "x",
		byAdmin(x, orgadmin.ActionAccountUpdate)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("rename another organization's account = %v", err)
	}
}

// TestAccRemoveAccount checks the organization admin's delete: no mark
// needed, never the last account, never another organization's.
func TestAccRemoveAccount(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "pied-piper")
	other := seed(t, db, "nucleus")
	f := &accountFeatures{}
	s := pg.New(db).WithFeatures(f)
	two := second(t, x, "pied-piper-two")
	e := byAdmin(x, orgadmin.ActionAccountDelete)
	if _, err := s.RemoveAccount(ctx, other.org, two, e); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("remove another organization's account = %v", err)
	}
	if _, err := s.RemoveAccount(ctx, x.org, two, e); err != nil {
		t.Fatal(err)
	}
	if len(f.deleted) != 1 || f.deleted[0] != two || !f.inTx {
		t.Errorf("features deleted %v, in a transaction %t", f.deleted, f.inTx)
	}
	if _, err := s.RemoveAccount(ctx, x.org, x.acct, e); !errors.Is(err, orgadmin.ErrInvalidState) {
		t.Errorf("remove the last account = %v", err)
	}
	if n := trail(t, x, orgadmin.ActionAccountDelete); n != 1 {
		t.Errorf("deletes on the trail = %d", n)
	}
}

func TestAccDeactivateMember(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "bachmanity")
	if _, err := db.Exec(ctx, `INSERT INTO account_users (account_id, organization_id, member_id)
		VALUES ($1, $2, $3)`, x.acct, x.org, x.member); err != nil {
		t.Fatal(err)
	}
	personal := kernel.NewULID()
	h, _ := apikey.Hash(apikey.NewSecret())
	if _, err := db.Exec(ctx, `INSERT INTO api_keys (account_id, id, name, type, secret_hash,
		creator, is_personal, owner_user_id) VALUES ($1, $2, 'mine', 'SECRET', $3, 'alice', true, $4)`,
		x.acct, personal, h, x.member); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Hour)
	web := x.webSession(at)
	keyed := x.keySession(at)
	keyed.ActorID = personal
	shared := x.keySession(at)
	for _, ss := range []session.Session{web, keyed, shared} {
		if err := x.s.CreateSession(ctx, ss); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := x.s.DeactivateMember(ctx, x.org, x.member, byAdmin(x, orgadmin.ActionDeactivate))
	if err != nil || len(ids) != 2 {
		t.Fatalf("deactivate = %v, %v", ids, err)
	}
	for _, id := range ids {
		if id != web.ID && id != keyed.ID {
			t.Errorf("revoked %s, which is neither the member's nor its personal key's", id)
		}
	}
	k, err := x.s.APIKey(ctx, personal)
	if err != nil || k.DisabledReason != "DEPROVISIONED" {
		t.Errorf("personal key = %+v, %v", k, err)
	}
	if m, err := x.s.Member(ctx, x.org, x.member); err != nil || m.Status != member.Deactivated {
		t.Errorf("member = %+v, %v", m, err)
	}
	if u, err := x.s.Seats(ctx, x.org, x.acct); err != nil || u.Organization != 0 {
		t.Errorf("seats after deactivation = %+v, %v", u, err)
	}
	if _, err := x.s.DeactivateMember(ctx, x.org, kernel.NewMemberID(),
		byAdmin(x, orgadmin.ActionDeactivate)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("deactivate no member = %v", err)
	}
}

func TestAccOrganizationKeys(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "raviga-capital")
	other := seed(t, db, "bream-hall")
	s := x.s
	at := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	keys := make([]apikey.OrganizationKey, 0, 2)
	for i, creator := range []kernel.MemberID{x.member, {}} {
		h, _ := apikey.HashOrganization(apikey.NewOrganizationSecret())
		k := apikey.OrganizationKey{
			OrganizationID: x.org, ID: kernel.NewULID(), Name: "bot", SecretHash: h,
			CreatorMemberID: creator, CreatedAt: at,
		}
		if i == 1 {
			k.ExpiresAt = at.Add(time.Hour)
		}
		if err := s.CreateOrganizationKey(
			ctx,
			k,
			byAdmin(x, orgadmin.ActionKeyCreate),
		); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	got, err := s.OrganizationKeys(ctx, x.org, kernel.ULID{}, 10)
	if err != nil || len(got) != 2 || got[0].CreatorMemberID != x.member ||
		!got[1].ExpiresAt.Equal(at.Add(time.Hour)) || !got[1].CreatorMemberID.IsZero() {
		t.Fatalf("keys = %+v, %v", got, err)
	}
	if page, err := s.OrganizationKeys(ctx, x.org, got[0].ID, 10); err != nil || len(page) != 1 {
		t.Errorf("second page = %+v, %v", page, err)
	}
	if none, err := s.OrganizationKeys(
		ctx,
		other.org,
		kernel.ULID{},
		10,
	); err != nil ||
		len(none) != 0 {
		t.Errorf("another organization's keys = %+v, %v", none, err)
	}
	k := keys[0]
	if err := s.TouchOrganizationKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	read, err := s.OrganizationKey(ctx, k.ID)
	if err != nil || read.LastUsedAt.IsZero() || string(read.SecretHash) != string(k.SecretHash) {
		t.Errorf("read = %+v, %v", read, err)
	}
	if _, err := s.OrganizationKey(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("no key = %v", err)
	}

	ss := session.Session{
		ID:             kernel.NewULID(),
		OrganizationID: x.org,
		Channel:        session.ChannelUnknown,
		ActorKind:      kernel.PrincipalOrganization,
		ActorID:        k.ID,
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	if err := s.CreateSession(ctx, ss); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DisableOrganizationKey(ctx, other.org, k.ID,
		byAdmin(other, orgadmin.ActionKeyDisable)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("disable another organization's key = %v", err)
	}
	d, ids, err := s.DisableOrganizationKey(ctx, x.org, k.ID, byAdmin(x, orgadmin.ActionKeyDisable))
	if err != nil || d.DisabledAt.IsZero() || len(ids) != 1 || ids[0] != ss.ID {
		t.Errorf("disable = %+v, %v, %v", d, ids, err)
	}
	again, ids, err := s.DisableOrganizationKey(
		ctx,
		x.org,
		k.ID,
		byAdmin(x, orgadmin.ActionKeyDisable),
	)
	if err != nil || !again.DisabledAt.Equal(d.DisabledAt) || len(ids) != 0 {
		t.Errorf("disable again = %+v, %v, %v", again, ids, err)
	}
	if _, _, err := s.DeleteOrganizationKey(ctx, x.org, k.ID,
		byAdmin(x, orgadmin.ActionKeyDelete)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DeleteOrganizationKey(ctx, x.org, k.ID,
		byAdmin(x, orgadmin.ActionKeyDelete)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("delete a deleted key = %v", err)
	}

	// Deleting the organization takes its keys and admins with it.
	if err := s.SetOrganizationAdmin(ctx, x.org, x.member, true, "x",
		byAdmin(x, orgadmin.ActionAdminGrant)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteOrganization(ctx, x.org, byAdmin(x, "organization.delete")); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM organization_keys
		WHERE organization_id = $1) + (SELECT count(*) FROM organization_admins
		WHERE organization_id = $1)`, x.org).Scan(&left); err != nil || left != 0 {
		t.Errorf("rows left = %d, %v", left, err)
	}
}

func TestAccRecord(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "sliceline")
	refused := errors.New("refused")
	if err := x.s.Record(ctx, byAdmin(x, orgadmin.ActionLimitSet),
		func(context.Context) error { return refused }); !errors.Is(err, refused) {
		t.Errorf("a refused write = %v", err)
	}
	if err := x.s.Record(ctx, byAdmin(x, orgadmin.ActionLimitSet),
		func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if n := trail(t, x, orgadmin.ActionLimitSet); n != 1 {
		t.Errorf("limit writes on the trail = %d, want only the one that succeeded", n)
	}
}

func TestAccOrganizationAdminUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p)
	p.Close()
	ctx := context.Background()
	org, acct, m, id := kernel.NewOrganizationID(), kernel.NewAccountID(), kernel.NewMemberID(), kernel.NewULID()
	e := kernel.OrganizationAuditEntry{OrganizationID: org}
	a := account.Account{OrganizationID: org, ID: acct, Slug: "closed", Name: "x"}
	for name, call := range map[string]func() error{
		"is admin":    func() error { _, err := s.IsOrganizationAdmin(ctx, org, m); return err },
		"accounts":    func() error { _, err := s.OrganizationAccounts(ctx, org, acct, 1); return err },
		"by slug":     func() error { _, err := s.AccountBySlug(ctx, org, "closed"); return err },
		"create":      func() error { return s.CreateAccount(ctx, a, m, e) },
		"members":     func() error { _, err := s.Members(ctx, org, m, 1); return err },
		"member":      func() error { _, err := s.Member(ctx, org, m); return err },
		"deactivate":  func() error { _, err := s.DeactivateMember(ctx, org, m, e); return err },
		"set admin":   func() error { return s.SetOrganizationAdmin(ctx, org, m, true, "x", e) },
		"keys":        func() error { _, err := s.OrganizationKeys(ctx, org, id, 1); return err },
		"key":         func() error { _, err := s.OrganizationKey(ctx, id); return err },
		"touch key":   func() error { return s.TouchOrganizationKey(ctx, id) },
		"create key":  func() error { return s.CreateOrganizationKey(ctx, apikey.OrganizationKey{}, e) },
		"rename":      func() error { _, err := s.RenameOrganization(ctx, org, "x", e); return err },
		"remove":      func() error { _, err := s.RemoveAccount(ctx, org, acct, e); return err },
		"claim":       func() error { _, err := s.ClaimAccount(ctx, org, acct, m, time.Now(), e); return err },
		"rename acct": func() error { _, err := s.RenameAccount(ctx, org, acct, "x", e); return err },
		"disable key": func() error { _, _, err := s.DisableOrganizationKey(ctx, org, id, e); return err },
		"record":      func() error { return s.Record(ctx, e, func(context.Context) error { return nil }) },
	} {
		if err := call(); err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}
