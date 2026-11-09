package pg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// onTrail is an entry of action on x's account's trail.
func onTrail(x seeded, action string) kernel.AuditEntry {
	return kernel.AuditEntry{
		AccountID:        x.acct.ULID,
		Actor:            kernel.AuditActor{Username: "alice"},
		Action:           action,
		EventType:        "UPDATE",
		AffectedResource: kernel.AuditResource{Type: user.ResourceManagedUser, ID: "x"},
	}
}

// accountTrail counts x's account's trail entries of action.
func accountTrail(t *testing.T, x seeded, action string) int {
	t.Helper()
	var n int
	if err := x.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_trail_entries
		WHERE account_id = $1 AND action = $2`, x.acct, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// invitation is an invitation sent at at.
func invitation(email string, at time.Time) user.Invitation {
	return user.Invitation{ID: kernel.NewULID(), Email: email, CreatedAt: at, SentAt: at}
}

func TestAccManagedUserInvite(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "pied-piper")
	at := time.Now().UTC().Truncate(time.Second)
	invite := func(login, email string) (user.User, error) {
		return x.s.Invite(ctx, x.org, x.acct, user.Invite{Login: login, Email: email},
			invitation(email, at), onTrail(x, user.ActionInvite))
	}

	// A login no member has creates a pending member the account admits.
	bob, err := invite("bob", "bob@example.com")
	if err != nil || bob.Member.Status != member.Pending || bob.Member.Login != "bob" ||
		bob.Invitation == nil || !bob.Invitation.SentAt.Equal(at) ||
		bob.Invitation.Email != "bob@example.com" || bob.State(at) != user.StatePending {
		t.Fatalf("invite bob = %+v, %v", bob, err)
	}
	if n := accountTrail(t, x, user.ActionInvite); n != 1 {
		t.Errorf("trail entries %d, want 1", n)
	}

	// An existing member is admitted as they are, with no new member.
	alice, err := invite("alice", "")
	if err != nil || alice.Member.ID != x.member || alice.Member.Status != member.Active {
		t.Fatalf("invite alice = %+v, %v", alice, err)
	}

	// A member the account admits, an address with a pending invitation, a
	// deactivated member and a login of two members are refused.
	addMember(t, x, "dave", "deactivated")
	addMember(t, x, "eve", "active")
	addMember(t, x, "eve", "active")
	for _, c := range []struct{ login, email string }{
		{"bob", ""}, {"carol", "bob@example.com"}, {"dave", ""}, {"eve", ""},
	} {
		if _, err := invite(c.login, c.email); !errors.Is(err, user.ErrInvalidState) {
			t.Errorf("invite %q %q: %v, want ErrInvalidState", c.login, c.email, err)
		}
	}

	users, err := x.s.Users(ctx, x.acct, kernel.MemberID{}, 10)
	if err != nil || len(users) != 2 {
		t.Fatalf("users = %d, %v", len(users), err)
	}
	if page, err := x.s.Users(ctx, x.acct, users[0].Member.ID, 10); err != nil ||
		len(page) != 1 || page[0].Member.ID != users[1].Member.ID {
		t.Errorf("users after the first = %+v, %v", page, err)
	}
	if _, err := x.s.User(ctx, x.acct, kernel.NewMemberID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("user of no admission: %v", err)
	}

	// An invite into a second account takes no organization seat for a
	// member another account admits.
	f := &seats{}
	s := x.s.WithFeatures(f)
	other := second(t, x, "pied-piper-dev")
	if _, err := s.Invite(ctx, x.org, other, user.Invite{Login: "bob"}, invitation("", at),
		onTrail(x, user.ActionInvite)); err != nil || f.calls != 1 || !f.inTx {
		t.Fatalf("invite bob again: %v, calls %d, in tx %v", err, f.calls, f.inTx)
	}
	if f.usage.Organization != 1 || f.usage.Account != 0 {
		t.Errorf("usage %+v, want bob's seat held once (alice and bob, less bob)", f.usage)
	}
	f.refuse = errors.New("limit")
	if _, err := s.Invite(ctx, x.org, other, user.Invite{Login: "frank"}, invitation("", at),
		onTrail(x, user.ActionInvite)); !errors.Is(err, f.refuse) {
		t.Errorf("invite beyond the limit: %v", err)
	}
	var frank int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM members WHERE login = 'frank'`).
		Scan(&frank); err != nil || frank != 0 {
		t.Errorf("a refused invite left %d members, %v", frank, err)
	}
}

func TestAccManagedUserChanges(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "hooli-xyz")
	at := time.Now().UTC().Truncate(time.Second)
	bob, err := x.s.Invite(ctx, x.org, x.acct, user.Invite{Login: "bob", SlackMemberID: "U1"},
		invitation("", at), onTrail(x, user.ActionInvite))
	if err != nil || bob.SlackMemberID != "U1" {
		t.Fatalf("invite = %+v, %v", bob, err)
	}
	id := bob.Member.ID

	if u, err := x.s.SetSlackMemberID(
		ctx,
		x.acct,
		id,
		kernel.Expect{},
		"",
		onTrail(x, user.ActionUpdate),
	); err != nil ||
		u.SlackMemberID != "" {
		t.Errorf("clear Slack member = %+v, %v", u, err)
	}
	if _, err := x.s.SetSlackMemberID(ctx, x.acct, x.member, kernel.Expect{}, "U2",
		onTrail(x, user.ActionUpdate)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("update of no admission: %v", err)
	}

	later := at.Add(time.Hour)
	if u, err := x.s.Resend(
		ctx,
		x.acct,
		id,
		later,
		onTrail(x, user.ActionResendInvite),
	); err != nil ||
		!u.Invitation.SentAt.Equal(later) ||
		!u.Invitation.CreatedAt.Equal(at) {
		t.Errorf("resend = %+v, %v", u.Invitation, err)
	}
	if _, err := x.s.Resend(ctx, x.acct, x.member, later,
		onTrail(x, user.ActionResendInvite)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("resend to no admission: %v", err)
	}
	if _, err := db.Exec(
		ctx,
		`UPDATE members SET status = 'active' WHERE id = $1`,
		id,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.Resend(ctx, x.acct, id, later,
		onTrail(x, user.ActionResendInvite)); !errors.Is(err, user.ErrInvalidState) {
		t.Errorf("resend to an active member: %v", err)
	}

	// Bob's personal key and sessions in the account go with his admission;
	// he stays a member of the organization.
	key := kernel.NewULID()
	h, _ := apikey.Hash(apikey.NewSecret())
	if _, err := db.Exec(ctx, `INSERT INTO api_keys (account_id, id, name, type, secret_hash,
		creator, is_personal, owner_user_id) VALUES ($1, $2, 'mine', 'SECRET', $3, 'bob', true, $4)`,
		x.acct, key, h, id); err != nil {
		t.Fatal(err)
	}
	keys, err := x.s.PersonalAPIKeys(ctx, x.acct, id, kernel.ULID{}, 10)
	if err != nil || len(keys) != 1 || keys[0].ID != key {
		t.Fatalf("personal keys = %+v, %v", keys, err)
	}
	if keys, err := x.s.PersonalAPIKeys(ctx, x.acct, id, key, 10); err != nil || len(keys) != 0 {
		t.Errorf("personal keys after the last = %d, %v", len(keys), err)
	}
	web := x.webSession(at.Add(time.Hour))
	web.MemberID, web.ActorID = id, id.ULID
	ks := x.keySession(at.Add(time.Hour))
	ks.ActorID = key
	if err := x.s.CreateSession(ctx, web); err != nil {
		t.Fatal(err)
	}
	if err := x.s.CreateSession(ctx, ks); err != nil {
		t.Fatal(err)
	}
	ids, err := x.s.Remove(ctx, x.acct, id, kernel.Expect{}, onTrail(x, user.ActionDelete))
	if err != nil || !slices.Contains(ids, web.ID) || !slices.Contains(ids, ks.ID) ||
		len(ids) != 2 {
		t.Fatalf("remove = %v, %v", ids, err)
	}
	var reason, status string
	if err := db.QueryRow(ctx, `SELECT k.disabled_reason, m.status FROM api_keys k
		JOIN members m ON m.id = k.owner_user_id WHERE k.id = $1`, key).
		Scan(&reason, &status); err != nil || reason != "DEPROVISIONED" || status != "active" {
		t.Errorf("after removal the key is %q and bob %q, %v", reason, status, err)
	}
	if _, err := x.s.User(ctx, x.acct, id); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("removed user: %v", err)
	}
	if _, err := x.s.Remove(
		ctx,
		x.acct,
		id,
		kernel.Expect{},
		onTrail(x, user.ActionDelete),
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("remove again: %v", err)
	}
	if n := accountTrail(t, x, user.ActionDelete); n != 1 {
		t.Errorf("delete entries %d, want 1", n)
	}

	if err := x.s.RecordRequest(ctx, onTrail(x, user.ActionRequestInvite)); err != nil ||
		accountTrail(t, x, user.ActionRequestInvite) != 1 {
		t.Errorf("record request: %v", err)
	}
	if err := x.s.RecordRequest(ctx, kernel.AuditEntry{}); err == nil {
		t.Error("an incomplete entry was recorded")
	}
}

func TestAccManagedUserGroups(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "aviato")
	at := time.Now().UTC().Truncate(time.Second)
	if _, err := x.s.Invite(ctx, x.org, x.acct, user.Invite{Login: "alice"}, invitation("", at),
		onTrail(x, user.ActionInvite)); err != nil {
		t.Fatal(err)
	}
	// Alice's latest sign-in reported two groups, as P23's sign-in records.
	if _, err := db.Exec(ctx, `UPDATE account_users SET latest_groups = '{ops,dev}'
		WHERE account_id = $1`, x.acct); err != nil {
		t.Fatal(err)
	}
	create := func(name string) (group.Group, error) {
		return x.s.CreateGroup(ctx, group.Group{
			AccountID: x.acct, ID: kernel.NewULID(), Name: name, Description: "d", CreatedAt: at,
		}, onTrail(x, group.ActionCreate))
	}
	ops, err := create("ops")
	if err != nil || !slices.Equal(ops.Members, []string{"alice"}) || ops.Description != "d" {
		t.Fatalf("create ops = %+v, %v", ops, err)
	}
	if _, err := create("ops"); !errors.Is(err, port.ErrSlugTaken) {
		t.Errorf("a second ops: %v", err)
	}
	qa, err := create("qa")
	if err != nil || len(qa.Members) != 0 {
		t.Fatalf("create qa = %+v, %v", qa, err)
	}
	// Another account may hold a group of the same name.
	other := seed(t, db, "aviato-two")
	if _, err := other.s.CreateGroup(ctx, group.Group{
		AccountID: other.acct, ID: kernel.NewULID(), Name: "ops", CreatedAt: at,
	}, onTrail(other, group.ActionCreate)); err != nil {
		t.Errorf("ops in another account: %v", err)
	}

	gs, err := x.s.Groups(ctx, x.acct, kernel.ULID{}, 10)
	if err != nil || len(gs) != 2 || gs[0].ID != ops.ID {
		t.Fatalf("groups = %+v, %v", gs, err)
	}
	if gs, err := x.s.Groups(
		ctx,
		x.acct,
		ops.ID,
		10,
	); err != nil || len(gs) != 1 ||
		gs[0].ID != qa.ID {
		t.Errorf("groups after ops = %+v, %v", gs, err)
	}
	u, err := x.s.User(ctx, x.acct, x.member)
	if err != nil || u.GroupCount != 1 || !slices.Equal(u.LatestGroups, []string{"ops", "dev"}) {
		t.Errorf("alice = %+v, %v", u, err)
	}
	if us, err := x.s.GroupUsers(ctx, x.acct, ops.ID, kernel.MemberID{}, 10); err != nil ||
		len(us) != 1 || us[0].Member.ID != x.member {
		t.Errorf("ops members = %+v, %v", us, err)
	}
	if us, err := x.s.GroupUsers(
		ctx,
		x.acct,
		qa.ID,
		kernel.MemberID{},
		10,
	); err != nil ||
		len(us) != 0 {
		t.Errorf("qa members = %+v, %v", us, err)
	}
	if names, err := x.s.AvailableGroups(ctx, x.acct, "", 10); err != nil ||
		!slices.Equal(names, []string{"dev", "ops"}) {
		t.Errorf("available = %v, %v", names, err)
	}
	if names, err := x.s.AvailableGroups(ctx, x.acct, "dev", 10); err != nil ||
		!slices.Equal(names, []string{"ops"}) {
		t.Errorf("available after dev = %v, %v", names, err)
	}

	if g, err := x.s.SetDescription(
		ctx,
		x.acct,
		qa.ID,
		kernel.Expect{},
		"quality",
		onTrail(x, group.ActionUpdate),
	); err != nil ||
		g.Description != "quality" {
		t.Errorf("describe = %+v, %v", g, err)
	}
	if _, err := x.s.SetDescription(ctx, other.acct, qa.ID, kernel.Expect{}, "x",
		onTrail(other, group.ActionUpdate)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("describe from another account: %v", err)
	}
	if _, err := x.s.Group(ctx, other.acct, qa.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("read from another account: %v", err)
	}
	if err := x.s.DeleteGroup(
		ctx,
		x.acct,
		qa.ID,
		kernel.Expect{},
		onTrail(x, group.ActionDelete),
	); err != nil {
		t.Fatal(err)
	}
	if err := x.s.DeleteGroup(ctx, x.acct, qa.ID,
		kernel.Expect{},
		onTrail(x, group.ActionDelete)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}
	if n := accountTrail(t, x, group.ActionCreate); n != 2 {
		t.Errorf("create entries %d, want 2", n)
	}

	// The organization's deletion takes its invitations and groups with it.
	o, _ := tenant(t, "aviato", "aviato")
	o.ID = x.org
	if _, err := x.s.DeleteOrganization(ctx, x.org, created(o)); err != nil {
		t.Fatalf("delete the organization: %v", err)
	}
}
