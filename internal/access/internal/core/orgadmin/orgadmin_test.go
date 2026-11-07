package orgadmin_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var (
	t0      = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	errDown = errors.New("down")
)

// store is orgadmin.Store over maps of one organization. fail names the
// method that answers errDown; trail records each write's entry.
type store struct {
	org      organization.Organization
	admins   map[kernel.MemberID]bool
	accounts []account.Account
	members  map[kernel.MemberID]orgadmin.Member
	keys     map[kernel.ULID]apikey.OrganizationKey
	seats    featuresapi.Usage
	fail     string
	trail    []kernel.OrganizationAuditEntry
}

func (s *store) down(op string) error {
	if s.fail == op {
		return errDown
	}
	return nil
}

func (s *store) record(op string, e kernel.OrganizationAuditEntry) error {
	if err := s.down(op); err != nil {
		return err
	}
	s.trail = append(s.trail, e)
	return nil
}

func (s *store) Organization(
	context.Context,
	kernel.OrganizationID,
) (organization.Organization, error) {
	return s.org, s.down("Organization")
}

func (s *store) RenameOrganization(
	_ context.Context,
	_ kernel.OrganizationID,
	name string,
	e kernel.OrganizationAuditEntry,
) (organization.Organization, error) {
	if err := s.record("RenameOrganization", e); err != nil {
		return organization.Organization{}, err
	}
	s.org.Name = name
	return s.org, nil
}

func (s *store) IsOrganizationAdmin(
	_ context.Context,
	_ kernel.OrganizationID,
	m kernel.MemberID,
) (bool, error) {
	return s.admins[m], s.down("IsOrganizationAdmin")
}

func (s *store) OrganizationAccounts(
	context.Context,
	kernel.OrganizationID,
	kernel.AccountID,
	int,
) ([]account.Account, error) {
	return s.accounts, s.down("OrganizationAccounts")
}

func (s *store) AccountBySlug(
	_ context.Context,
	_ kernel.OrganizationID,
	slug kernel.Slug,
) (account.Account, error) {
	for _, a := range s.accounts {
		if a.Slug == slug {
			return a, nil
		}
	}
	return account.Account{}, port.ErrNotFound
}

func (s *store) CreateAccount(
	_ context.Context,
	a account.Account,
	admin kernel.MemberID,
	e kernel.OrganizationAuditEntry,
) error {
	if m, ok := s.members[admin]; !ok || !m.Active {
		return orgadmin.ErrNoAdmin
	}
	if err := s.record("CreateAccount", e); err != nil {
		return err
	}
	s.accounts = append(s.accounts, a)
	return nil
}

func (s *store) ClaimAccount(
	_ context.Context,
	_ kernel.OrganizationID,
	acct kernel.AccountID,
	admin kernel.MemberID,
	at time.Time,
	e kernel.OrganizationAuditEntry,
) (account.Account, error) {
	for i, a := range s.accounts {
		if a.ID != acct {
			continue
		}
		if a.Claimed() {
			return account.Account{}, orgadmin.ErrInvalidState
		}
		if m, ok := s.members[admin]; !ok || !m.Active {
			return account.Account{}, orgadmin.ErrNoAdmin
		}
		if err := s.record("ClaimAccount", e); err != nil {
			return account.Account{}, err
		}
		s.accounts[i].ClaimedAt = at
		return s.accounts[i], nil
	}
	return account.Account{}, port.ErrNotFound
}

func (s *store) RenameAccount(
	_ context.Context,
	_ kernel.OrganizationID,
	acct kernel.AccountID,
	name string,
	e kernel.OrganizationAuditEntry,
) (account.Account, error) {
	if err := s.record("RenameAccount", e); err != nil {
		return account.Account{}, err
	}
	for i := range s.accounts {
		if s.accounts[i].ID == acct {
			s.accounts[i].Name = name
			return s.accounts[i], nil
		}
	}
	return account.Account{}, port.ErrNotFound
}

func (s *store) RemoveAccount(
	_ context.Context,
	_ kernel.OrganizationID,
	_ kernel.AccountID,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	if err := s.record("RemoveAccount", e); err != nil {
		return nil, err
	}
	return []kernel.ULID{kernel.NewULID()}, nil
}

func (s *store) Seats(
	context.Context,
	kernel.OrganizationID,
	kernel.AccountID,
) (featuresapi.Usage, error) {
	return s.seats, s.down("Seats")
}

func (s *store) Members(
	context.Context,
	kernel.OrganizationID,
	kernel.MemberID,
	int,
) ([]orgadmin.Member, error) {
	out := make([]orgadmin.Member, 0, len(s.members))
	for _, m := range s.members {
		out = append(out, m)
	}
	return out, s.down("Members")
}

func (s *store) Member(
	_ context.Context,
	_ kernel.OrganizationID,
	id kernel.MemberID,
) (orgadmin.Member, error) {
	m, ok := s.members[id]
	if !ok {
		return orgadmin.Member{}, port.ErrNotFound
	}
	return m, nil
}

func (s *store) DeactivateMember(
	_ context.Context,
	_ kernel.OrganizationID,
	_ kernel.MemberID,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	if err := s.record("DeactivateMember", e); err != nil {
		return nil, err
	}
	return []kernel.ULID{kernel.NewULID()}, nil
}

func (s *store) SetOrganizationAdmin(
	_ context.Context,
	_ kernel.OrganizationID,
	id kernel.MemberID,
	admin bool,
	by string,
	e kernel.OrganizationAuditEntry,
) error {
	if by == "" {
		return errors.New("no grantor")
	}
	if err := s.record("SetOrganizationAdmin", e); err != nil {
		return err
	}
	s.admins[id] = admin
	return nil
}

func (s *store) OrganizationKeys(
	context.Context,
	kernel.OrganizationID,
	kernel.ULID,
	int,
) ([]apikey.OrganizationKey, error) {
	out := make([]apikey.OrganizationKey, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, k)
	}
	return out, s.down("OrganizationKeys")
}

func (s *store) CreateOrganizationKey(
	_ context.Context,
	k apikey.OrganizationKey,
	e kernel.OrganizationAuditEntry,
) error {
	if err := s.record("CreateOrganizationKey", e); err != nil {
		return err
	}
	s.keys[k.ID] = k
	return nil
}

func (s *store) endKey(
	op string,
	id kernel.ULID,
	e kernel.OrganizationAuditEntry,
) (apikey.OrganizationKey, []kernel.ULID, error) {
	k, ok := s.keys[id]
	if !ok {
		return apikey.OrganizationKey{}, nil, port.ErrNotFound
	}
	if err := s.record(op, e); err != nil {
		return apikey.OrganizationKey{}, nil, err
	}
	return k, []kernel.ULID{kernel.NewULID()}, nil
}

func (s *store) DisableOrganizationKey(
	_ context.Context,
	_ kernel.OrganizationID,
	id kernel.ULID,
	e kernel.OrganizationAuditEntry,
) (apikey.OrganizationKey, []kernel.ULID, error) {
	return s.endKey("DisableOrganizationKey", id, e)
}

func (s *store) DeleteOrganizationKey(
	_ context.Context,
	_ kernel.OrganizationID,
	id kernel.ULID,
	e kernel.OrganizationAuditEntry,
) (apikey.OrganizationKey, []kernel.ULID, error) {
	return s.endKey("DeleteOrganizationKey", id, e)
}

func (s *store) Record(
	ctx context.Context,
	e kernel.OrganizationAuditEntry,
	write func(context.Context) error,
) error {
	if err := write(ctx); err != nil {
		return err
	}
	return s.record("Record", e)
}

// entitlements is the features domain's account limits: a pool, each
// account's limits, and the catalogue's limit names.
type entitlements struct {
	featuresapi.Entitlements
	pool    map[string]int64
	poolErr error
	own     map[kernel.AccountID]map[string]int64
	ownErr  error
	setErr  error
	cleared []string
}

func (f *entitlements) LimitNames() []string { return []string{"private-workers", "seats"} }

func (f *entitlements) Entitlement(
	context.Context,
	kernel.OrganizationID,
) (featuresapi.Entitlement, error) {
	return featuresapi.Entitlement{Limits: f.pool}, f.poolErr
}

func (f *entitlements) AccountLimits(
	_ context.Context,
	acct kernel.AccountID,
) (map[string]int64, error) {
	return f.own[acct], f.ownErr
}

func (f *entitlements) SetAccountLimit(
	_ context.Context,
	_ kernel.OrganizationID,
	acct kernel.AccountID,
	limit string,
	value int64,
) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.own[acct] == nil {
		f.own[acct] = map[string]int64{}
	}
	f.own[acct][limit] = value
	return nil
}

func (f *entitlements) ClearAccountLimit(
	_ context.Context,
	acct kernel.AccountID,
	limit string,
) error {
	f.cleared = append(f.cleared, limit)
	delete(f.own[acct], limit)
	return f.setErr
}

// world is a service over a store holding organization "acme", its first
// account "acme", unclaimed, and the active member alice, an organization
// admin, and bob, who is not.
type world struct {
	st      *store
	f       *entitlements
	s       *orgadmin.Service
	dropped []kernel.ULID
	alice   kernel.MemberID
	bob     kernel.MemberID
	acct    account.Account
}

func newWorld(t *testing.T) *world {
	t.Helper()
	org := organization.Organization{ID: kernel.NewOrganizationID(), Slug: "acme", Name: "Acme"}
	w := &world{alice: kernel.NewMemberID(), bob: kernel.NewMemberID()}
	w.acct = account.Account{
		OrganizationID: org.ID,
		ID:             kernel.NewAccountID(),
		Slug:           "acme",
		Name:           "Acme",
	}
	w.st = &store{
		org:      org,
		admins:   map[kernel.MemberID]bool{w.alice: true},
		accounts: []account.Account{w.acct},
		members: map[kernel.MemberID]orgadmin.Member{
			w.alice: {ID: w.alice, Login: "alice", Active: true, Admin: true},
			w.bob:   {ID: w.bob, Login: "bob", Active: true},
		},
		keys:  map[kernel.ULID]apikey.OrganizationKey{},
		seats: featuresapi.Usage{Organization: 3, Account: 1},
	}
	w.f = &entitlements{
		pool: map[string]int64{"seats": 5},
		own:  map[kernel.AccountID]map[string]int64{},
	}
	w.s = orgadmin.NewService(orgadmin.Deps{
		Store: w.st, Features: w.f,
		Drop: func(id kernel.ULID) { w.dropped = append(w.dropped, id) },
		Now:  func() time.Time { return t0 },
	})
	return w
}

func (w *world) as(kind kernel.PrincipalKind, m kernel.MemberID) orgadmin.Actor {
	return orgadmin.Actor{
		Principal: kernel.Principal{
			Kind: kind, ActorID: m.ULID, OrganizationID: w.st.org.ID, MemberID: m,
		},
		RemoteIP: "192.0.2.1",
	}
}

// TestUnitAdmit checks who the tag serves: an organization token, the
// break-glass admin and a member holding the organization role; a member
// without it is forbidden, and so is any other kind.
func TestUnitAdmit(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	for name, c := range map[string]struct {
		by  orgadmin.Actor
		err error
	}{
		"organization token": {by: w.as(kernel.PrincipalOrganization, kernel.MemberID{})},
		"break-glass admin":  {by: w.as(kernel.PrincipalBreakGlass, kernel.MemberID{})},
		"organization admin": {by: w.as(kernel.PrincipalUser, w.alice)},
		"account member":     {by: w.as(kernel.PrincipalUser, w.bob), err: orgadmin.ErrForbidden},
		"API key":            {by: w.as(kernel.PrincipalAPIKey, kernel.MemberID{}), err: orgadmin.ErrForbidden},
	} {
		o, err := w.s.Organization(ctx, c.by)
		if !errors.Is(err, c.err) || (c.err == nil && o.Slug != "acme") {
			t.Errorf("%s = %+v, %v, want %v", name, o, err, c.err)
		}
	}
	w.st.fail = "IsOrganizationAdmin"
	if _, err := w.s.Organization(
		ctx,
		w.as(kernel.PrincipalUser, w.alice),
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("an unreadable role = %v", err)
	}
}

func TestUnitOrganization(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	admin := w.as(kernel.PrincipalUser, w.alice)
	o, err := w.s.Rename(ctx, admin, "Acme Global")
	if err != nil || o.Name != "Acme Global" {
		t.Fatalf("rename = %+v, %v", o, err)
	}
	e := w.st.trail[0]
	if e.Action != orgadmin.ActionUpdate || e.ActorKind != kernel.PrincipalUser ||
		e.ActorID != w.alice.String() || e.RemoteIP != "192.0.2.1" ||
		e.AffectedResource.ID != "acme" || string(e.Body) != `{"name":"Acme Global"}` {
		t.Errorf("entry = %+v", e)
	}
	if _, err := w.s.Rename(ctx, admin, " "); !errors.Is(err, organization.ErrInvalid) {
		t.Errorf("a blank name = %v", err)
	}
	if _, err := w.s.Rename(
		ctx,
		w.as(kernel.PrincipalUser, w.bob),
		"x",
	); !errors.Is(
		err,
		orgadmin.ErrForbidden,
	) {
		t.Errorf("rename by a member = %v", err)
	}
	w.st.fail = "Organization"
	if _, err := w.s.Rename(ctx, admin, "x"); !errors.Is(err, errDown) {
		t.Errorf("an unreadable organization = %v", err)
	}
}

func TestUnitAccounts(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	admin := w.as(kernel.PrincipalBreakGlass, kernel.MemberID{})

	a, err := w.s.Claim(ctx, admin, "acme", w.bob.String())
	if err != nil || !a.ClaimedAt.Equal(t0) {
		t.Fatalf("claim = %+v, %v", a, err)
	}
	if _, err := w.s.Claim(
		ctx,
		admin,
		"acme",
		w.bob.String(),
	); !errors.Is(
		err,
		orgadmin.ErrInvalidState,
	) {
		t.Errorf("a second claim = %v", err)
	}
	b, err := w.s.CreateAccount(ctx, admin, "acme-two", "Acme Two", w.alice.String())
	if err != nil || !b.Claimed() || b.OrganizationID != w.st.org.ID {
		t.Fatalf("create = %+v, %v", b, err)
	}
	accts, err := w.s.Accounts(ctx, admin, kernel.AccountID{}, 10)
	if err != nil || len(accts) != 2 {
		t.Errorf("accounts = %+v, %v", accts, err)
	}
	if r, err := w.s.RenameAccount(
		ctx,
		admin,
		"acme-two",
		"Acme Deux",
	); err != nil ||
		r.Name != "Acme Deux" {
		t.Errorf("rename = %+v, %v", r, err)
	}
	if _, err := w.s.RenameAccount(
		ctx,
		admin,
		"acme-two",
		"",
	); !errors.Is(
		err,
		organization.ErrInvalid,
	) {
		t.Errorf("rename to nothing = %v", err)
	}
	if d, err := w.s.DeleteAccount(
		ctx,
		admin,
		"acme-two",
	); err != nil || d.ID != b.ID ||
		len(w.dropped) != 1 {
		t.Errorf("delete = %+v, %v, dropped %v", d, err, w.dropped)
	}

	for name, call := range map[string]func() error{
		"a bad slug": func() error {
			_, err := w.s.CreateAccount(ctx, admin, "Acme!", "Acme", w.alice.String())
			return err
		},
		"a bad admin": func() error {
			_, err := w.s.CreateAccount(ctx, admin, "acme-three", "Acme", "alice")
			return err
		},
		"a deactivated admin": func() error {
			m := w.st.members[w.bob]
			m.Active = false
			w.st.members[w.bob] = m
			_, err := w.s.CreateAccount(ctx, admin, "acme-three", "Acme", w.bob.String())
			return err
		},
		"a claim's bad admin": func() error {
			_, err := w.s.Claim(ctx, admin, "acme", "bob")
			return err
		},
	} {
		var fe *organization.FieldError
		if err := call(); !errors.As(err, &fe) || fe.Field == "" {
			t.Errorf("%s = %v", name, err)
		}
	}
	for name, slug := range map[string]string{"no account": "nope", "not a slug": "No Such"} {
		if _, err := w.s.DeleteAccount(ctx, admin, slug); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s = %v", name, err)
		}
	}
	w.st.fail = "RemoveAccount"
	if _, err := w.s.DeleteAccount(ctx, admin, "acme"); !errors.Is(err, errDown) {
		t.Errorf("a failed delete = %v", err)
	}
	w.st.fail = "ClaimAccount"
	w.st.accounts[0].ClaimedAt = time.Time{}
	if _, err := w.s.Claim(ctx, admin, "acme", w.alice.String()); !errors.Is(err, errDown) {
		t.Errorf("a failed claim = %v", err)
	}
	if _, err := w.s.Accounts(
		ctx,
		w.as(kernel.PrincipalUser, w.bob),
		kernel.AccountID{},
		1,
	); !errors.Is(
		err,
		orgadmin.ErrForbidden,
	) {
		t.Errorf("accounts for a member = %v", err)
	}
	if _, err := w.s.CreateAccount(
		ctx,
		w.as(kernel.PrincipalUser, w.bob),
		"x",
		"x",
		"",
	); !errors.Is(
		err,
		orgadmin.ErrForbidden,
	) {
		t.Errorf("create by a member = %v", err)
	}
}

func TestUnitMembers(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	admin := w.as(kernel.PrincipalOrganization, kernel.MemberID{})
	if ms, err := w.s.Members(ctx, admin, kernel.MemberID{}, 10); err != nil || len(ms) != 2 {
		t.Errorf("members = %+v, %v", ms, err)
	}
	m, err := w.s.SetAdmin(ctx, admin, w.bob.String(), true)
	if err != nil || !m.Admin || !w.st.admins[w.bob] {
		t.Errorf("grant = %+v, %v", m, err)
	}
	if e := w.st.trail[len(w.st.trail)-1]; e.Action != orgadmin.ActionAdminGrant {
		t.Errorf("entry = %+v", e)
	}
	if m, err := w.s.SetAdmin(ctx, admin, w.bob.String(), false); err != nil || m.Admin {
		t.Errorf("revoke = %+v, %v", m, err)
	}
	if e := w.st.trail[len(w.st.trail)-1]; e.Action != orgadmin.ActionAdminRevoke {
		t.Errorf("entry = %+v", e)
	}
	m, err = w.s.Deactivate(ctx, admin, w.bob.String())
	if err != nil || m.Active || len(w.dropped) != 1 {
		t.Errorf("deactivate = %+v, %v, dropped %v", m, err, w.dropped)
	}
	for name, id := range map[string]string{"no member": kernel.NewULID().String(), "not an ID": "bob"} {
		if _, err := w.s.Deactivate(ctx, admin, id); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s = %v", name, err)
		}
	}
	for _, op := range []string{"DeactivateMember", "SetOrganizationAdmin"} {
		w.st.fail = op
		if _, err := w.s.Deactivate(
			ctx,
			admin,
			w.bob.String(),
		); op == "DeactivateMember" &&
			!errors.Is(err, errDown) {
			t.Errorf("a failed deactivation = %v", err)
		}
		if _, err := w.s.SetAdmin(
			ctx,
			admin,
			w.bob.String(),
			true,
		); op == "SetOrganizationAdmin" &&
			!errors.Is(err, errDown) {
			t.Errorf("a failed grant = %v", err)
		}
	}
	w.st.fail = "IsOrganizationAdmin"
	if _, err := w.s.Members(
		ctx,
		w.as(kernel.PrincipalUser, w.alice),
		kernel.MemberID{},
		1,
	); !errors.Is(
		err,
		errDown,
	) {
		t.Errorf("members with an unreadable role = %v", err)
	}
}

func TestUnitKeys(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	member := w.as(kernel.PrincipalUser, w.alice)
	k, secret, err := w.s.CreateKey(ctx, member, "bot", t0.Add(time.Hour))
	if err != nil || k.CreatorMemberID != w.alice || !k.Matches(secret) ||
		!k.ExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("create = %+v, %v", k, err)
	}
	if e := w.st.trail[0]; string(
		e.Body,
	) != fmt.Sprintf(
		`{"expiresAt":%d,"name":"bot"}`,
		t0.Add(time.Hour).Unix(),
	) ||
		e.AffectedResource.ID != k.ID.String() {
		t.Errorf("entry = %+v %s", e, e.Body)
	}
	bg, _, err := w.s.CreateKey(
		ctx,
		w.as(kernel.PrincipalBreakGlass, kernel.MemberID{}),
		"ops",
		time.Time{},
	)
	if err != nil || !bg.CreatorMemberID.IsZero() || !bg.ExpiresAt.IsZero() {
		t.Errorf("create by break-glass = %+v, %v", bg, err)
	}
	if ks, err := w.s.Keys(ctx, member, kernel.ULID{}, 10); err != nil || len(ks) != 2 {
		t.Errorf("keys = %+v, %v", ks, err)
	}
	if d, err := w.s.DisableKey(ctx, member, k.ID.String()); err != nil || d.ID != k.ID {
		t.Errorf("disable = %+v, %v", d, err)
	}
	if d, err := w.s.DeleteKey(ctx, member, k.ID.String()); err != nil || d.ID != k.ID {
		t.Errorf("delete = %+v, %v", d, err)
	}
	if got := []string{
		w.st.trail[2].EventType,
		w.st.trail[3].EventType,
	}; !reflect.DeepEqual(
		got,
		[]string{"UPDATE", "DELETE"},
	) {
		t.Errorf("event types = %v", got)
	}
	if len(w.dropped) != 2 {
		t.Errorf("dropped %v", w.dropped)
	}
	for name, c := range map[string]struct {
		name    string
		expires time.Time
	}{
		"a blank name":  {name: ""},
		"a past expiry": {name: "x", expires: t0},
	} {
		if _, _, err := w.s.CreateKey(
			ctx,
			member,
			c.name,
			c.expires,
		); !errors.Is(
			err,
			organization.ErrInvalid,
		) {
			t.Errorf("%s = %v", name, err)
		}
	}
	for name, id := range map[string]string{"no key": kernel.NewULID().String(), "not an ID": "k"} {
		if _, err := w.s.DisableKey(ctx, member, id); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s = %v", name, err)
		}
	}
	w.st.fail = "CreateOrganizationKey"
	if _, _, err := w.s.CreateKey(ctx, member, "x", time.Time{}); !errors.Is(err, errDown) {
		t.Errorf("a failed create = %v", err)
	}
	bob := w.as(kernel.PrincipalUser, w.bob)
	if _, _, err := w.s.CreateKey(
		ctx,
		bob,
		"x",
		time.Time{},
	); !errors.Is(
		err,
		orgadmin.ErrForbidden,
	) {
		t.Errorf("create by a member = %v", err)
	}
	if _, err := w.s.Keys(ctx, bob, kernel.ULID{}, 1); !errors.Is(err, orgadmin.ErrForbidden) {
		t.Errorf("keys for a member = %v", err)
	}
	if _, err := w.s.DeleteKey(ctx, bob, k.ID.String()); !errors.Is(err, orgadmin.ErrForbidden) {
		t.Errorf("delete by a member = %v", err)
	}
}

func ptr(v int64) *int64 { return &v }

// TestUnitLimits checks decision 0021's limits as the tag answers them:
// every limit of the catalogue, the pool's value where it sets one, the
// account's own where one is set, and the seats in use.
func TestUnitLimits(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	admin := w.as(kernel.PrincipalOrganization, kernel.MemberID{})
	got, err := w.s.Limits(ctx, admin)
	want := []orgadmin.Limit{
		{Name: "private-workers"},
		{Name: "seats", Value: ptr(5), InUse: ptr(3)},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("limits = %+v, %v", got, err)
	}
	u, err := w.s.SetAccountLimit(ctx, admin, "acme", "seats", 2)
	want = []orgadmin.Limit{
		{Name: "private-workers"},
		{Name: "seats", Value: ptr(2), InUse: ptr(1)},
	}
	if err != nil || u.Account.ID != w.acct.ID || !reflect.DeepEqual(u.Limits, want) {
		t.Errorf("set = %+v, %v", u, err)
	}
	if e := w.st.trail[0]; e.Action != orgadmin.ActionLimitSet ||
		string(e.Body) != `{"limit":"seats","value":2}` {
		t.Errorf("entry = %+v", e)
	}
	us, err := w.s.Usage(ctx, admin, kernel.AccountID{}, 10)
	if err != nil || len(us) != 1 || !reflect.DeepEqual(us[0].Limits, want) {
		t.Errorf("usage = %+v, %v", us, err)
	}
	u, err = w.s.ClearAccountLimit(ctx, admin, "acme", "seats")
	if err != nil || u.Limits[1].Value != nil || w.f.cleared[0] != "seats" {
		t.Errorf("clear = %+v, %v", u, err)
	}

	w.f.setErr = &featuresapi.AccountLimitError{Limit: "seats", Value: 9, Pool: 5}
	if _, err := w.s.SetAccountLimit(ctx, admin, "acme", "seats", 9); !errors.Is(err, w.f.setErr) {
		t.Errorf("above the pool = %v", err)
	}
	if len(w.st.trail) != 2 {
		t.Errorf("a refused limit was recorded: %+v", w.st.trail)
	}
	w.f.setErr = nil
	if _, err := w.s.SetAccountLimit(
		ctx,
		admin,
		"nope",
		"seats",
		1,
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("no account = %v", err)
	}
	w.f.ownErr = errDown
	if _, err := w.s.Usage(ctx, admin, kernel.AccountID{}, 10); !errors.Is(err, errDown) {
		t.Errorf("unreadable account limits = %v", err)
	}
	w.f.ownErr = nil
	w.st.fail = "Seats"
	for name, call := range map[string]func() error{
		"limits": func() error { _, err := w.s.Limits(ctx, admin); return err },
		"usage":  func() error { _, err := w.s.Usage(ctx, admin, kernel.AccountID{}, 1); return err },
	} {
		if err := call(); !errors.Is(err, errDown) {
			t.Errorf("%s with uncounted seats = %v", name, err)
		}
	}
	w.st.fail = "Record"
	if _, err := w.s.SetAccountLimit(ctx, admin, "acme", "seats", 1); !errors.Is(err, errDown) {
		t.Errorf("an unrecorded limit = %v", err)
	}
	w.st.fail = "OrganizationAccounts"
	if _, err := w.s.Usage(ctx, admin, kernel.AccountID{}, 1); !errors.Is(err, errDown) {
		t.Errorf("unlisted accounts = %v", err)
	}
	w.st.fail = ""
	w.f.poolErr = errDown
	if _, err := w.s.Limits(ctx, admin); !errors.Is(err, errDown) {
		t.Errorf("an unreadable entitlement = %v", err)
	}
	w.f.poolErr = featuresapi.ErrNotFound
	if got, err := w.s.Limits(ctx, admin); err != nil || got[1].Value != nil {
		t.Errorf("no entitlement = %+v, %v", got, err)
	}
	if _, err := w.s.Limits(
		ctx,
		w.as(kernel.PrincipalUser, w.bob),
	); !errors.Is(
		err,
		orgadmin.ErrForbidden,
	) {
		t.Errorf("limits for a member = %v", err)
	}
}

// TestUnitNoFeatures checks the service without the features domain: no
// limit applies and nothing is listed.
func TestUnitNoFeatures(t *testing.T) {
	w := newWorld(t)
	s := orgadmin.NewService(
		orgadmin.Deps{Store: w.st, Drop: func(kernel.ULID) {}, Now: func() time.Time { return t0 }},
	)
	ctx := t.Context()
	admin := w.as(kernel.PrincipalOrganization, kernel.MemberID{})
	if got, err := s.Limits(ctx, admin); err != nil || len(got) != 0 {
		t.Errorf("limits = %+v, %v", got, err)
	}
	if u, err := s.SetAccountLimit(
		ctx,
		admin,
		"acme",
		"seats",
		1,
	); err != nil ||
		len(u.Limits) != 0 {
		t.Errorf("set = %+v, %v", u, err)
	}
	if _, err := s.ClearAccountLimit(ctx, admin, "acme", "seats"); err != nil {
		t.Errorf("clear = %v", err)
	}
}
