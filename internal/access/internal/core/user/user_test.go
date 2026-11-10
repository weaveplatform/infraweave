package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var (
	t0      = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	errDown = errors.New("down")
)

// store is user.Store over one account's users. fail makes every method
// answer errDown; trail records each entry.
type store struct {
	acct    kernel.AccountID
	users   map[kernel.MemberID]user.User
	keys    []apikey.Key
	revoked []kernel.ULID
	fail    bool
	trail   []kernel.AuditEntry
}

func (s *store) user(acct kernel.AccountID, id kernel.MemberID) (user.User, error) {
	if s.fail {
		return user.User{}, errDown
	}
	u, ok := s.users[id]
	if !ok || acct != s.acct {
		return user.User{}, fmt.Errorf("%w: %s", port.ErrNotFound, id)
	}
	return u, nil
}

func (s *store) Users(
	_ context.Context,
	acct kernel.AccountID,
	_ kernel.MemberID,
	_ int,
) ([]user.User, error) {
	if s.fail {
		return nil, errDown
	}
	out := make([]user.User, 0, len(s.users))
	for _, u := range s.users {
		if acct == s.acct {
			out = append(out, u)
		}
	}
	return out, nil
}

func (s *store) User(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
) (user.User, error) {
	return s.user(acct, id)
}

func (s *store) Invite(
	_ context.Context,
	_ kernel.OrganizationID,
	acct kernel.AccountID,
	in user.Invite,
	inv user.Invitation,
	e kernel.AuditEntry,
) (user.User, error) {
	if s.fail {
		return user.User{}, errDown
	}
	u := user.User{
		AccountID: acct, SlackMemberID: in.SlackMemberID, Invitation: &inv,
		Member: member.Member{ID: kernel.NewMemberID(), Login: in.Login, Status: member.Pending},
	}
	s.users[u.Member.ID] = u
	s.trail = append(s.trail, e)
	return u, nil
}

func (s *store) SetSlackMemberID(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
	expect kernel.Expect,
	slack string,
	e kernel.AuditEntry,
) (user.User, error) {
	u, err := s.user(acct, id)
	if err != nil {
		return u, err
	}
	if err := expect.Check(u.Version); err != nil {
		return user.User{}, err
	}
	u.SlackMemberID, u.Version = slack, u.Version+1
	s.users[id] = u
	s.trail = append(s.trail, e)
	return u, nil
}

func (s *store) Resend(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
	at time.Time,
	e kernel.AuditEntry,
) (user.User, error) {
	u, err := s.user(acct, id)
	if err != nil {
		return u, err
	}
	if u.Member.Status != member.Pending || u.Invitation == nil {
		return u, user.ErrInvalidState
	}
	inv := *u.Invitation
	inv.SentAt = at
	u.Invitation = &inv
	s.users[id] = u
	s.trail = append(s.trail, e)
	return u, nil
}

func (s *store) Remove(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.MemberID,
	expect kernel.Expect,
	e kernel.AuditEntry,
) ([]kernel.ULID, error) {
	u, err := s.user(acct, id)
	if err != nil {
		return nil, err
	}
	if err := expect.Check(u.Version); err != nil {
		return nil, err
	}
	delete(s.users, id)
	s.trail = append(s.trail, e)
	return s.revoked, nil
}

func (s *store) PersonalAPIKeys(
	_ context.Context,
	acct kernel.AccountID,
	_ kernel.MemberID,
	_ kernel.ULID,
	_ int,
) ([]apikey.Key, error) {
	if s.fail {
		return nil, errDown
	}
	if acct != s.acct {
		return nil, nil
	}
	return s.keys, nil
}

func (s *store) RecordRequest(_ context.Context, e kernel.AuditEntry) error {
	if s.fail {
		return errDown
	}
	s.trail = append(s.trail, e)
	return nil
}

type world struct {
	s       *store
	svc     *user.Service
	p       kernel.Principal
	dropped []kernel.ULID
}

func newWorld() *world {
	acct := kernel.NewAccountID()
	w := &world{
		s: &store{acct: acct, users: map[kernel.MemberID]user.User{}},
		p: kernel.Principal{
			Kind: kernel.PrincipalUser, OrganizationID: kernel.NewOrganizationID(),
			AccountID: acct, MemberID: kernel.NewMemberID(), Login: "alice",
		},
	}
	w.svc = user.NewService(user.Deps{
		Store: w.s,
		Drop:  func(id kernel.ULID) { w.dropped = append(w.dropped, id) },
		Now:   func() time.Time { return t0 },
	})
	return w
}

// isField reports whether err, or one error it joins, is a FieldError of
// field.
func isField(err error, field string) bool {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range j.Unwrap() {
			if isField(e, field) {
				return true
			}
		}
		return false
	}
	var fe *organization.FieldError
	return errors.As(err, &fe) && fe.Field == field
}

func TestUnitUserState(t *testing.T) {
	// EXPIRED is derived from the invitation's sentAt as the clock moves,
	// and a resend restarts it.
	synctest.Test(t, func(t *testing.T) {
		sent := time.Now()
		u := user.User{
			Member:     member.Member{Status: member.Pending},
			Invitation: &user.Invitation{SentAt: sent, CreatedAt: sent},
		}
		if got := u.State(time.Now()); got != user.StatePending {
			t.Errorf("at once = %s", got)
		}
		<-time.After(user.InvitationTTL - time.Second)
		if got := u.State(time.Now()); got != user.StatePending {
			t.Errorf("a second before 24 hours = %s", got)
		}
		<-time.After(time.Second)
		if got := u.State(time.Now()); got != user.StateExpired {
			t.Errorf("at 24 hours = %s", got)
		}
		u.Invitation.SentAt = time.Now()
		if got := u.State(time.Now()); got != user.StatePending {
			t.Errorf("after a resend = %s", got)
		}
	})
	for status, want := range map[member.Status]user.State{
		member.Active:      user.StateActive,
		member.Deactivated: user.StateInactive,
		member.Pending:     user.StatePending,
	} {
		if got := (user.User{Member: member.Member{Status: status}}).State(t0); got != want {
			t.Errorf("%s = %s, want %s", status, got, want)
		}
	}
}

func TestUnitUserInvite(t *testing.T) {
	ctx := t.Context()
	w := newWorld()
	u, err := w.svc.Invite(ctx, w.p, "192.0.2.1",
		user.Invite{Login: "bob", Email: "bob@example.com", SlackMemberID: "U1"})
	if err != nil || u.Member.Login != "bob" || u.Invitation == nil ||
		!u.Invitation.SentAt.Equal(t0) || u.Invitation.Email != "bob@example.com" {
		t.Fatalf("invite = %+v, %v", u, err)
	}
	e := w.s.trail[0]
	var body map[string]string
	if e.Action != user.ActionInvite || e.RemoteIP != "192.0.2.1" || e.Actor.Username != "alice" ||
		e.AccountID != w.p.AccountID.ULID || json.Unmarshal(e.Body, &body) != nil ||
		body["username"] != "bob" {
		t.Errorf("entry = %+v", e)
	}

	_, err = w.svc.Invite(ctx, w.p, "", user.Invite{Login: "", Email: "nope"})
	if !isField(err, "username") || !isField(err, "invitationEmail") {
		t.Errorf("bad input: %v", err)
	}
	w.s.fail = true
	if _, err := w.svc.Invite(
		ctx,
		w.p,
		"",
		user.Invite{Login: "carol"},
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("store down: %v", err)
	}
}

func TestUnitUserReadsAndWrites(t *testing.T) {
	ctx := t.Context()
	w := newWorld()
	u, err := w.svc.Invite(ctx, w.p, "", user.Invite{Login: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	id := u.Member.ID.String()

	if got, err := w.svc.User(ctx, w.p, id); err != nil || got.Member.ID != u.Member.ID {
		t.Errorf("user = %+v, %v", got, err)
	}
	if us, err := w.svc.Users(ctx, w.p, kernel.MemberID{}, 10); err != nil || len(us) != 1 {
		t.Errorf("users = %d, %v", len(us), err)
	}
	for _, bad := range []string{"nope", kernel.NewMemberID().String()} {
		if _, err := w.svc.User(ctx, w.p, bad); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("user %q: %v", bad, err)
		}
	}
	// A managed user of another account is unknown here.
	q := w.p
	q.AccountID = kernel.NewAccountID()
	if _, err := w.svc.User(ctx, q, id); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("user from another account: %v", err)
	}

	if got, err := w.svc.Update(
		ctx,
		w.p,
		"",
		id,
		kernel.Expect{},
		"U2",
	); err != nil ||
		got.SlackMemberID != "U2" {
		t.Errorf("update = %+v, %v", got, err)
	}
	if _, err := w.svc.Update(
		ctx,
		w.p,
		"",
		"nope",
		kernel.Expect{},
		"",
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("update of no user: %v", err)
	}

	if got, err := w.svc.Resend(ctx, w.p, "", id); err != nil || !got.Invitation.SentAt.Equal(t0) {
		t.Errorf("resend = %+v, %v", got, err)
	}
	if _, err := w.svc.Resend(ctx, w.p, "", "nope"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("resend to no user: %v", err)
	}

	w.s.keys = []apikey.Key{{ID: kernel.NewULID()}}
	if keys, err := w.svc.PersonalAPIKeys(
		ctx,
		w.p,
		id,
		kernel.ULID{},
		10,
	); err != nil ||
		len(keys) != 1 {
		t.Errorf("personal keys = %d, %v", len(keys), err)
	}
	if _, err := w.svc.PersonalAPIKeys(ctx, w.p, "nope", kernel.ULID{}, 10); !errors.Is(err,
		port.ErrNotFound) {
		t.Errorf("personal keys of no user: %v", err)
	}

	// The store sees the expect each change carries.
	stale := kernel.ExpectVersions(9)
	if _, err := w.svc.Update(ctx, w.p, "", id, stale, "U3"); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale update: %v", err)
	}
	if _, err := w.svc.Delete(ctx, w.p, "", id, stale); !errors.Is(err, kernel.ErrStale) {
		t.Errorf("a stale delete: %v", err)
	}
	w.s.revoked = []kernel.ULID{kernel.NewULID()}
	if got, err := w.svc.Delete(
		ctx,
		w.p,
		"",
		id,
		kernel.Expect{},
	); err != nil ||
		got.Member.ID != u.Member.ID ||
		len(w.dropped) != 1 {
		t.Errorf("delete = %+v, %v, dropped %v", got, err, w.dropped)
	}
	if _, err := w.svc.Delete(
		ctx,
		w.p,
		"",
		id,
		kernel.Expect{},
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("delete again: %v", err)
	}

	w.s.fail = true
	if _, err := w.svc.Delete(ctx, w.p, "", id, kernel.Expect{}); !errors.Is(err, errDown) {
		t.Errorf("delete with the store down: %v", err)
	}
}

// removeFails answers Remove with errDown after a read that succeeds.
type removeFails struct{ *store }

func (removeFails) Remove(
	context.Context,
	kernel.AccountID,
	kernel.MemberID,
	kernel.Expect,
	kernel.AuditEntry,
) ([]kernel.ULID, error) {
	return nil, errDown
}

func TestUnitUserDeleteFails(t *testing.T) {
	w := newWorld()
	u, err := w.svc.Invite(t.Context(), w.p, "", user.Invite{Login: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	svc := user.NewService(user.Deps{Store: removeFails{w.s}, Now: func() time.Time { return t0 }})
	if _, err := svc.Delete(
		t.Context(),
		w.p,
		"",
		u.Member.ID.String(),
		kernel.Expect{},
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("delete = %v", err)
	}
}

func TestUnitUserRequests(t *testing.T) {
	ctx := t.Context()
	w := newWorld()
	if err := w.svc.RequestInvite(ctx, w.p, "", "carol@example.com", "carol"); err != nil ||
		w.s.trail[0].Action != user.ActionRequestInvite {
		t.Errorf("request invite: %v, %+v", err, w.s.trail)
	}
	err := w.svc.RequestInvite(ctx, w.p, "", "", "")
	if !isField(err, "email") || !isField(err, "userName") {
		t.Errorf("an empty request: %v", err)
	}
	if err := w.svc.RequestInvite(ctx, w.p, "", "nope", "carol"); !isField(err, "email") {
		t.Errorf("a bad address: %v", err)
	}

	if err := w.svc.RequestRoleChange(ctx, w.p, "", "legacy", "WRITE"); err != nil ||
		w.s.trail[1].Action != user.ActionRequestRoleChange ||
		w.s.trail[1].AffectedResource.ID != "legacy" {
		t.Errorf("request role change: %v, %+v", err, w.s.trail)
	}
	if err := w.svc.RequestRoleChange(ctx, w.p, "", "legacy", "OWNER"); !isField(err,
		"requestedAccessLevel") {
		t.Errorf("a level that does not exist: %v", err)
	}
	key := w.p
	key.Kind = kernel.PrincipalAPIKey
	if err := w.svc.RequestRoleChange(ctx, key, "", "legacy", "READ"); !errors.Is(err,
		user.ErrNotMember) {
		t.Errorf("an API key asks: %v", err)
	}
}
