package apikey

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

var errMissing = errors.New("missing")

// mem is Store in memory: each key's sessions are the IDs in sessions.
type mem struct {
	keys     map[kernel.ULID]Key
	sessions map[kernel.ULID][]kernel.ULID
	managers map[kernel.AccountID]bool
	// peopleOnly is the accounts that let only people manage keys.
	peopleOnly map[kernel.AccountID]bool
	// personalOff is the accounts whose personal keys are turned off;
	// unadmitted the members no account admits; creatableErr fails the
	// personal key rule's read.
	personalOff  map[kernel.AccountID]bool
	unadmitted   map[kernel.MemberID]bool
	creatableErr error
	// managersErr fails NonAdminKeyManagers.
	managersErr error
	err         error
}

func newMem() *mem {
	return &mem{
		keys:       map[kernel.ULID]Key{},
		sessions:   map[kernel.ULID][]kernel.ULID{},
		managers:   map[kernel.AccountID]bool{},
		peopleOnly: map[kernel.AccountID]bool{},
	}
}

func (m *mem) CreateAPIKey(_ context.Context, k Key) error {
	m.keys[k.ID] = k
	return m.err
}

func (m *mem) AccountAPIKeys(
	_ context.Context,
	acct kernel.AccountID,
	after kernel.ULID,
	limit int,
) ([]Key, error) {
	var out []Key
	for _, k := range m.keys {
		if k.AccountID == acct && k.DeletedAt.IsZero() &&
			(after.IsZero() || k.ID.String() > after.String()) {
			out = append(out, k)
		}
	}
	slices.SortFunc(
		out,
		func(a, b Key) int { return strings.Compare(a.ID.String(), b.ID.String()) },
	)
	return out[:min(len(out), limit)], m.err
}

func (m *mem) AccountAPIKey(_ context.Context, acct kernel.AccountID, id kernel.ULID) (Key, error) {
	k, ok := m.keys[id]
	if !ok || k.AccountID != acct || !k.DeletedAt.IsZero() {
		return Key{}, errMissing
	}
	return k, m.err
}

func (m *mem) UpdateAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	c Change,
) (Key, error) {
	k, err := m.AccountAPIKey(ctx, acct, id)
	if err != nil {
		return Key{}, err
	}
	if c.Name != nil {
		k.Name = *c.Name
	}
	if c.Description != nil {
		k.Description = *c.Description
	}
	if c.Teams != nil {
		k.Teams = *c.Teams
	}
	if c.ClaimMapping != nil {
		k.OIDC.ClaimMapping = *c.ClaimMapping
	}
	m.keys[id] = k
	return k, nil
}

func (m *mem) change(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	f func(*Key),
) (Key, []kernel.ULID, error) {
	k, err := m.AccountAPIKey(ctx, acct, id)
	if err != nil {
		return Key{}, nil, err
	}
	f(&k)
	m.keys[id] = k
	revoked := m.sessions[id]
	delete(m.sessions, id)
	return k, revoked, nil
}

func (m *mem) ResetAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	hash []byte,
) (Key, []kernel.ULID, error) {
	return m.change(ctx, acct, id, func(k *Key) { k.SecretHash = hash })
}

func (m *mem) DisableAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	reason string,
) (Key, []kernel.ULID, error) {
	return m.change(ctx, acct, id, func(k *Key) {
		if k.DisabledAt.IsZero() {
			k.DisabledAt, k.DisabledReason = t0, reason
		}
	})
}

func (m *mem) DeleteAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (Key, []kernel.ULID, error) {
	return m.change(ctx, acct, id, func(k *Key) { k.DeletedAt = t0 })
}

func (m *mem) SetNonAdminManagers(_ context.Context, acct kernel.AccountID, enable bool) error {
	m.managers[acct] = enable
	return m.err
}

func (m *mem) NonHumanKeyManagers(_ context.Context, acct kernel.AccountID) (bool, error) {
	return !m.peopleOnly[acct], m.err
}

func (m *mem) NonAdminKeyManagers(_ context.Context, acct kernel.AccountID) (bool, error) {
	return m.managers[acct], m.managersErr
}

// role is an Authorizer: the caller holds every action on the top space
// when top, every one in a space below when below; err fails Authorize
// and holdsErr Holds.
type role struct {
	top, below    bool
	err, holdsErr error
}

func (r *role) Authorize(
	context.Context,
	kernel.Principal,
	accessapi.Action,
	kernel.ULID,
) (bool, error) {
	return r.top, r.err
}

func (r *role) Holds(context.Context, kernel.Principal, accessapi.Action) (bool, error) {
	return r.top || r.below, r.holdsErr
}

type world struct {
	st      *mem
	role    *role
	svc     *Service
	dropped []kernel.ULID
	me      kernel.Principal
	other   kernel.Principal
}

func newWorld(allowed ...string) *world {
	w := &world{
		st:   newMem(),
		role: &role{top: true},
		me: kernel.Principal{
			Kind: kernel.PrincipalAPIKey, OrganizationID: kernel.NewOrganizationID(),
			AccountID: kernel.NewAccountID(), Login: "creator",
		},
		other: kernel.Principal{Kind: kernel.PrincipalAPIKey, AccountID: kernel.NewAccountID()},
	}
	w.svc = NewService(Deps{
		Store:          w.st,
		Authz:          w.role,
		Drop:           func(id kernel.ULID) { w.dropped = append(w.dropped, id) },
		AllowedIssuers: allowed,
		Now:            func() time.Time { return t0 },
	})
	return w
}

// TestUnitServiceSecretKey creates, reads, lists, updates, resets,
// disables and deletes a secret key, checking that each revoking change
// drops the key's sessions, and that another account sees none of it.
func TestUnitServiceSecretKey(t *testing.T) {
	ctx := t.Context()
	w := newWorld()
	k, secret, err := w.svc.Create(ctx, w.me, Input{
		Name: " ci ", Description: "deploys", Teams: []string{"ops"}, Machine: true,
		ExpiresAt: t0.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "ci" || k.Type != TypeSecret || k.Creator != "creator" || !k.Machine ||
		k.AccountID != w.me.AccountID || !k.CreatedAt.Equal(t0) || !k.Matches(secret) {
		t.Fatalf("Create = %+v", k)
	}
	if got, err := w.svc.Get(ctx, w.me, k.ID); err != nil || got.ID != k.ID {
		t.Errorf("Get = %+v, %v", got, err)
	}
	if _, err := w.svc.Get(ctx, w.other, k.ID); !errors.Is(err, errMissing) {
		t.Errorf("another account's Get: %v", err)
	}
	if list, err := w.svc.List(ctx, w.me, kernel.ULID{}, 10); err != nil || len(list) != 1 {
		t.Errorf("List = %v, %v", list, err)
	}
	if list, _ := w.svc.List(ctx, w.other, kernel.ULID{}, 10); len(list) != 0 {
		t.Errorf("another account lists %v", list)
	}

	name, desc, teams := "deploy", "", []string{}
	if got, err := w.svc.Update(ctx, w.me, k.ID, Change{
		Name: &name, Description: &desc, Teams: &teams,
	}); err != nil || got.Name != "deploy" || got.Description != "" || len(got.Teams) != 0 {
		t.Errorf("Update = %+v, %v", got, err)
	}
	if _, err := w.svc.Update(
		ctx,
		w.other,
		k.ID,
		Change{Name: &name},
	); !errors.Is(
		err,
		errMissing,
	) {
		t.Errorf("another account's Update: %v", err)
	}

	session := kernel.NewULID()
	w.st.sessions[k.ID] = []kernel.ULID{session}
	reset, newSecret, err := w.svc.Reset(ctx, w.me, k.ID)
	if err != nil || newSecret == secret || !reset.Matches(newSecret) || reset.Matches(secret) {
		t.Fatalf("Reset = %+v, %q, %v", reset, newSecret, err)
	}
	if !slices.Equal(w.dropped, []kernel.ULID{session}) {
		t.Errorf("Reset dropped %v, want the key's session", w.dropped)
	}
	if _, _, err := w.svc.Reset(ctx, w.other, k.ID); !errors.Is(err, errMissing) {
		t.Errorf("another account's Reset: %v", err)
	}

	w.st.sessions[k.ID] = []kernel.ULID{kernel.NewULID()}
	off, err := w.svc.Disable(ctx, w.me, k.ID)
	if err != nil || off.DisabledAt.IsZero() || off.DisabledReason != DisabledByAdmin ||
		off.Usable(t0) ||
		len(w.dropped) != 2 {
		t.Errorf("Disable = %+v, %v, dropped %v", off, err, w.dropped)
	}

	w.st.sessions[k.ID] = []kernel.ULID{kernel.NewULID()}
	gone, err := w.svc.Delete(ctx, w.me, k.ID)
	if err != nil || gone.DeletedAt.IsZero() || len(w.dropped) != 3 {
		t.Errorf("Delete = %+v, %v, dropped %v", gone, err, w.dropped)
	}
	if _, err := w.svc.Delete(ctx, w.me, k.ID); !errors.Is(err, errMissing) {
		t.Errorf("deleting twice: %v", err)
	}

	if on, err := w.svc.SetNonAdminManagers(ctx, w.me, true); err != nil || !on ||
		!w.st.managers[w.me.AccountID] {
		t.Errorf("SetNonAdminManagers = %v, %v", on, err)
	}
	w.st.err = errMissing
	if _, err := w.svc.SetNonAdminManagers(ctx, w.me, false); !errors.Is(err, errMissing) {
		t.Errorf("SetNonAdminManagers with the store down: %v", err)
	}
	if _, _, err := w.svc.Create(ctx, w.me, Input{Name: "x"}); !errors.Is(err, errMissing) {
		t.Errorf("Create with the store down: %v", err)
	}
}

// TestUnitServiceOIDCKey creates an OIDC key and checks the rules that
// apply to it alone: no secret to answer or reset, and a claim mapping
// that only it may change.
func TestUnitServiceOIDCKey(t *testing.T) {
	ctx := t.Context()
	w := newWorld("https://token.actions.githubusercontent.com")
	k, secret, err := w.svc.Create(ctx, w.me, Input{Name: "gha", OIDC: &OIDC{
		Issuer:            "https://token.actions.githubusercontent.com",
		ClientID:          "infraweave",
		SubjectExpression: "repo:org/app:.*",
	}})
	if err != nil || secret != "" || k.Type != TypeOIDC || k.SecretHash != nil ||
		k.OIDC.ClaimMapping == nil || k.Teams == nil {
		t.Fatalf("Create = %+v, %q, %v", k, secret, err)
	}
	if _, _, err := w.svc.Reset(ctx, w.me, k.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("Reset of an OIDC key: %v", err)
	}
	m := []Mapping{{Name: "cognito:groups", Value: TeamsClaim}}
	if got, err := w.svc.Update(ctx, w.me, k.ID, Change{ClaimMapping: &m}); err != nil ||
		!slices.Equal(got.OIDC.ClaimMapping, m) {
		t.Errorf("Update of the claim mapping = %+v, %v", got, err)
	}
	if _, err := w.svc.Update(
		ctx,
		w.other,
		k.ID,
		Change{ClaimMapping: &m},
	); !errors.Is(
		err,
		errMissing,
	) {
		t.Errorf("another account's claim mapping update: %v", err)
	}
	plain, _, err := w.svc.Create(ctx, w.me, Input{Name: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Update(
		ctx,
		w.me,
		plain.ID,
		Change{ClaimMapping: &m},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Errorf("a claim mapping on a secret key: %v", err)
	}
}

// TestUnitServiceInvalid checks that each refused input is ErrInvalid
// naming its field, and stores nothing.
func TestUnitServiceInvalid(t *testing.T) {
	ctx := t.Context()
	w := newWorld("https://allowed.example.com")
	good := OIDC{Issuer: "https://allowed.example.com", ClientID: "c", SubjectExpression: "s"}
	with := func(f func(*OIDC)) *OIDC {
		o := good
		f(&o)
		return &o
	}
	for name, tc := range map[string]struct {
		in   Input
		want string
	}{
		"no name":       {Input{Name: "  "}, "name is required"},
		"empty team":    {Input{Name: "k", Teams: []string{"ops", ""}}, "teams"},
		"past expiry":   {Input{Name: "k", ExpiresAt: t0}, "expiresAt"},
		"http issuer":   {Input{Name: "k", OIDC: with(func(o *OIDC) { o.Issuer = "http://allowed.example.com" })}, "oidc.issuer"},
		"other issuer":  {Input{Name: "k", OIDC: with(func(o *OIDC) { o.Issuer = "https://evil.example.com" })}, "oidc.issuer"},
		"no client":     {Input{Name: "k", OIDC: with(func(o *OIDC) { o.ClientID = "" })}, "oidc.clientId"},
		"no subject":    {Input{Name: "k", OIDC: with(func(o *OIDC) { o.SubjectExpression = "" })}, "oidc.subjectExpression is required"},
		"bad subject":   {Input{Name: "k", OIDC: with(func(o *OIDC) { o.SubjectExpression = "(" })}, "oidc.subjectExpression"},
		"empty mapping": {Input{Name: "k", OIDC: with(func(o *OIDC) { o.ClaimMapping = []Mapping{{Name: "a"}} })}, "claim mapping entry 0"},
	} {
		if _, _, err := w.svc.Create(ctx, w.me, tc.in); !errors.Is(err, ErrInvalid) ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want ErrInvalid naming %s", name, err, tc.want)
		}
	}
	if len(w.st.keys) != 0 {
		t.Errorf("refused input stored %d keys", len(w.st.keys))
	}
	k, _, err := w.svc.Create(ctx, w.me, Input{Name: "k"})
	if err != nil {
		t.Fatal(err)
	}
	blank, teams := "", []string{""}
	if _, err := w.svc.Update(
		ctx,
		w.me,
		k.ID,
		Change{Name: &blank, Teams: &teams},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Errorf("a blank name and team: %v", err)
	}
	var none []string
	if got, err := w.svc.Update(
		ctx,
		w.me,
		k.ID,
		Change{Teams: &none},
	); err != nil ||
		got.Teams == nil {
		t.Errorf("nil teams update = %+v, %v", got, err)
	}
}

// TestUnitIssuerAllowed checks the issuer rule: HTTPS only, and only an
// allowed issuer when any are configured.
//
// ref: DV-014
func TestUnitIssuerAllowed(t *testing.T) {
	for issuer, want := range map[string]bool{
		"https://idp.example.com":        true,
		"https://idp.example.com/tenant": true,
		"http://idp.example.com":         false,
		"idp.example.com":                false,
		"https://u:p@idp.example.com":    false,
		"https://idp.example.com?x=1":    false,
		"https://idp.example.com#f":      false,
		"https://":                       false,
		"https://idp.example.com/%zz":    false,
	} {
		if IssuerAllowed(nil, issuer) != want {
			t.Errorf("%q: allowed %v", issuer, !want)
		}
	}
	allowed := []string{"https://idp.example.com"}
	if !IssuerAllowed(allowed, "https://idp.example.com") ||
		IssuerAllowed(allowed, "https://other.example.com") {
		t.Error("the allowlist is not applied")
	}
}

// TestUnitServiceNonHumans checks that, in an account that lets only
// people manage keys, a key's every change is ErrNonHuman and reaches no
// key, while its reads and a member's changes go through.
func TestUnitServiceNonHumans(t *testing.T) {
	ctx := t.Context()
	w := newWorld()
	k, _, err := w.svc.Create(ctx, w.me, Input{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	w.st.peopleOnly[w.me.AccountID] = true
	name := "renamed"
	for op, err := range map[string]error{
		"create": func() error { _, _, err := w.svc.Create(ctx, w.me, Input{Name: "x"}); return err }(),
		"update": func() error {
			_, err := w.svc.Update(ctx, w.me, k.ID, Change{Name: &name})
			return err
		}(),
		"reset":    func() error { _, _, err := w.svc.Reset(ctx, w.me, k.ID); return err }(),
		"disable":  func() error { _, err := w.svc.Disable(ctx, w.me, k.ID); return err }(),
		"delete":   func() error { _, err := w.svc.Delete(ctx, w.me, k.ID); return err }(),
		"managers": func() error { _, err := w.svc.SetNonAdminManagers(ctx, w.me, true); return err }(),
	} {
		if !errors.Is(err, ErrNonHuman) {
			t.Errorf("%s by a key = %v, want ErrNonHuman", op, err)
		}
	}
	if got, err := w.svc.Get(
		ctx,
		w.me,
		k.ID,
	); err != nil || got.Name != "ci" ||
		len(w.st.keys) != 1 {
		t.Errorf("after the refusals: %+v, %v, %d keys", got, err, len(w.st.keys))
	}
	member := w.me
	member.Kind = kernel.PrincipalUser
	if _, err := w.svc.Update(ctx, member, k.ID, Change{Name: &name}); err != nil {
		t.Errorf("a member's update = %v", err)
	}
	w.st.peopleOnly[w.me.AccountID] = false
	w.st.err = errors.New("down")
	if _, err := w.svc.Delete(ctx, w.me, k.ID); !errors.Is(err, w.st.err) {
		t.Errorf("a delete with the store down = %v", err)
	}
}

func (m *mem) CreatePersonalAPIKey(_ context.Context, k Key) (Key, error) {
	switch {
	case m.err != nil:
		return Key{}, m.err
	case m.personalOff[k.AccountID]:
		return Key{}, ErrPersonalOff
	}
	k.OwnerLogin = "alice"
	m.keys[k.ID] = k
	return k, nil
}

func (m *mem) AccountPersonalAPIKeys(
	ctx context.Context,
	acct kernel.AccountID,
	after kernel.ULID,
	limit int,
) ([]Key, error) {
	all, err := m.AccountAPIKeys(ctx, acct, after, len(m.keys))
	var out []Key
	for _, k := range all {
		if k.Personal() {
			out = append(out, k)
		}
	}
	return out[:min(len(out), limit)], err
}

func (m *mem) CountPersonalAPIKeys(ctx context.Context, acct kernel.AccountID) (int, error) {
	keys, err := m.AccountPersonalAPIKeys(ctx, acct, kernel.ULID{}, len(m.keys))
	n := 0
	for _, k := range keys {
		if k.DisabledAt.IsZero() {
			n++
		}
	}
	return n, err
}

func (m *mem) DisablePersonalAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	reason string,
) (Key, []kernel.ULID, error) {
	if k, ok := m.keys[id]; ok && !k.Personal() {
		return Key{}, nil, errMissing
	}
	return m.DisableAPIKey(ctx, acct, id, reason)
}

// TestUnitServicePersonalKey creates, lists, counts and disables a
// personal key, which only a member's own session creates and which
// another account never sees.
func TestUnitServicePersonalKey(t *testing.T) {
	ctx := t.Context()
	w := newWorld()
	alice := w.me
	alice.Kind, alice.MemberID, alice.Login = kernel.PrincipalUser, kernel.NewMemberID(), "alice"

	k, secret, err := w.svc.CreatePersonal(ctx, alice, PersonalInput{
		Name: " laptop ", Description: "mine", ExpiresAt: t0.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !k.Personal() || k.Owner != alice.MemberID || k.OwnerLogin != "alice" ||
		k.Name != "laptop" || k.Creator != "alice" || k.Type != TypeSecret ||
		len(k.Teams) != 0 || !k.Matches(secret) {
		t.Fatalf("CreatePersonal = %+v", k)
	}
	if _, _, err := w.svc.Create(ctx, w.me, Input{Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	if list, err := w.svc.Personal(ctx, w.me, kernel.ULID{}, 10); err != nil ||
		len(list) != 1 || list[0].ID != k.ID {
		t.Errorf("Personal = %v, %v", list, err)
	}
	if list, _ := w.svc.Personal(ctx, w.other, kernel.ULID{}, 10); len(list) != 0 {
		t.Errorf("another account lists %v", list)
	}
	if n, err := w.svc.PersonalCount(ctx, w.me); err != nil || n != 1 {
		t.Errorf("PersonalCount = %d, %v", n, err)
	}

	w.st.sessions[k.ID] = []kernel.ULID{kernel.NewULID()}
	off, err := w.svc.DisablePersonal(ctx, w.me, k.ID)
	if err != nil || off.DisabledReason != DisabledByAdmin || len(w.dropped) != 1 {
		t.Errorf("DisablePersonal = %+v, %v, dropped %v", off, err, w.dropped)
	}
	if n, _ := w.svc.PersonalCount(ctx, w.me); n != 0 {
		t.Errorf("PersonalCount after disabling = %d", n)
	}
	list, _ := w.svc.List(ctx, w.me, kernel.ULID{}, 10)
	for _, o := range list {
		if !o.Personal() {
			if _, err := w.svc.DisablePersonal(ctx, w.me, o.ID); !errors.Is(err, errMissing) {
				t.Errorf("DisablePersonal of the account's key: %v", err)
			}
		}
	}
	w.st.peopleOnly[w.me.AccountID] = true
	if _, err := w.svc.DisablePersonal(ctx, w.me, k.ID); !errors.Is(err, ErrNonHuman) {
		t.Errorf("DisablePersonal by a key where only people manage keys: %v", err)
	}
}

// TestUnitServicePersonalRefusals checks who may not create a personal
// key and the input that is refused.
func TestUnitServicePersonalRefusals(t *testing.T) {
	ctx := t.Context()
	w := newWorld()
	alice := w.me
	alice.Kind, alice.MemberID = kernel.PrincipalUser, kernel.NewMemberID()
	breakGlass := w.me
	breakGlass.Kind = kernel.PrincipalBreakGlass

	for name, p := range map[string]kernel.Principal{
		"an API key":            w.me,
		"the break-glass admin": breakGlass,
	} {
		if _, _, err := w.svc.CreatePersonal(ctx, p, PersonalInput{Name: "k"}); !errors.Is(
			err, ErrNoOwner) {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, _, err := w.svc.CreatePersonal(ctx, alice, PersonalInput{ExpiresAt: t0})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "name") ||
		!strings.Contains(err.Error(), "expiresAt") {
		t.Errorf("no name, expired: %v", err)
	}
	w.st.personalOff = map[kernel.AccountID]bool{alice.AccountID: true}
	if _, _, err := w.svc.CreatePersonal(ctx, alice, PersonalInput{Name: "k"}); !errors.Is(
		err, ErrPersonalOff) {
		t.Errorf("with personal keys off: %v", err)
	}
	w.st.personalOff, w.st.err = nil, errors.New("down")
	if _, _, err := w.svc.CreatePersonal(ctx, alice, PersonalInput{Name: "k"}); !errors.Is(
		err, w.st.err) {
		t.Errorf("a failing store: %v", err)
	}
}

func (m *mem) PersonalCreatable(
	_ context.Context,
	acct kernel.AccountID,
	member kernel.MemberID,
) error {
	switch {
	case m.creatableErr != nil:
		return m.creatableErr
	case m.unadmitted[member]:
		return ErrNoOwner
	case m.personalOff[acct]:
		return ErrPersonalOff
	}
	return nil
}

func (m *mem) KeysOf(
	ctx context.Context,
	acct kernel.AccountID,
	login string,
	member kernel.MemberID,
) ([]kernel.ULID, []kernel.ULID, error) {
	keys, err := m.AccountAPIKeys(ctx, acct, kernel.ULID{}, len(m.keys))
	var created, owned []kernel.ULID
	for _, k := range keys {
		if k.Creator == login {
			created = append(created, k.ID)
		}
		if k.Personal() && k.Owner == member {
			owned = append(owned, k.ID)
		}
	}
	return created, owned, err
}

// TestUnitServiceHeld checks the keys a principal created and owns and
// whether it may create a personal key, each answered from the rule the
// operations enforce.
func TestUnitServiceHeld(t *testing.T) {
	ctx := t.Context()
	w := newWorld()
	alice := w.me
	alice.Kind, alice.MemberID, alice.Login = kernel.PrincipalUser, kernel.NewMemberID(), "alice"

	if h, err := w.svc.Held(ctx, kernel.Principal{Kind: kernel.PrincipalBreakGlass}); err != nil ||
		h.Created != nil || h.Owned != nil || h.CanCreatePersonal {
		t.Errorf("a principal in no account holds %+v, %v", h, err)
	}
	ci, _, err := w.svc.Create(ctx, w.me, Input{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	laptop, _, err := w.svc.CreatePersonal(ctx, alice, PersonalInput{Name: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if h, err := w.svc.Held(
		ctx,
		w.me,
	); err != nil || !slices.Equal(h.Created, []kernel.ULID{ci.ID}) ||
		h.Owned != nil ||
		h.CanCreatePersonal {
		t.Errorf("the key holds %+v, %v", h, err)
	}
	h, err := w.svc.Held(ctx, alice)
	if err != nil || !slices.Equal(h.Created, []kernel.ULID{laptop.ID}) ||
		!slices.Equal(h.Owned, []kernel.ULID{laptop.ID}) || !h.CanCreatePersonal {
		t.Errorf("alice holds %+v, %v", h, err)
	}

	w.st.personalOff = map[kernel.AccountID]bool{alice.AccountID: true}
	if h, err := w.svc.Held(ctx, alice); err != nil || h.CanCreatePersonal {
		t.Errorf("with personal keys off: %+v, %v", h, err)
	}
	w.st.personalOff, w.st.unadmitted = nil, map[kernel.MemberID]bool{alice.MemberID: true}
	if h, err := w.svc.Held(ctx, alice); err != nil || h.CanCreatePersonal {
		t.Errorf("alice the account no longer admits: %+v, %v", h, err)
	}
	w.st.creatableErr = errors.New("down")
	if _, err := w.svc.Held(ctx, alice); !errors.Is(err, w.st.creatableErr) {
		t.Errorf("the rule unreadable: %v", err)
	}
	w.st.creatableErr, w.st.err = nil, errors.New("down")
	if _, err := w.svc.Held(ctx, alice); !errors.Is(err, w.st.err) {
		t.Errorf("the keys unreadable: %v", err)
	}
}

// TestUnitServiceManagers checks the rule each change and allowedActions
// share: the top space's admin manages keys; an admin of a space below
// reads them, and manages them only while the account lets such admins;
// a caller who holds the actions nowhere is Denied; and the authorizer's
// and the store's failures pass through.
func TestUnitServiceManagers(t *testing.T) {
	ctx := t.Context()
	w := newWorld()
	k, _, err := w.svc.Create(ctx, w.me, Input{Name: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	all := []accessapi.Action{
		accessapi.ActionApiKeyRead, accessapi.ActionApiKeyUpdate,
		accessapi.ActionApiKeyReset, accessapi.ActionApiKeyDelete,
	}
	actions := func(want ...accessapi.Action) {
		t.Helper()
		if got, err := w.svc.Actions(ctx, w.me); err != nil || !slices.Equal(got, want) {
			t.Errorf("actions = %v, %v, want %v", got, err, want)
		}
	}
	actions(all...)

	*w.role = role{below: true}
	actions(accessapi.ActionApiKeyRead)
	_, err = w.svc.Update(ctx, w.me, k.ID, Change{})
	if d, ok := errors.AsType[*Denied](err); !ok || d.Action != accessapi.ActionApiKeyUpdate ||
		!strings.Contains(err.Error(), "API_KEY_UPDATE") {
		t.Errorf("an update by a space's admin = %v, want Denied", err)
	}
	w.st.managers[w.me.AccountID] = true
	actions(all...)
	if _, _, err := w.svc.Reset(ctx, w.me, k.ID); err != nil {
		t.Errorf("a reset by a space's admin the account lets manage keys: %v", err)
	}

	*w.role = role{}
	actions()
	if _, err := w.svc.Delete(ctx, w.me, k.ID); !errors.As(err, new(*Denied)) {
		t.Errorf("a delete by a caller with no binding = %v, want Denied", err)
	}

	w.st.peopleOnly[w.me.AccountID] = true
	*w.role = role{top: true}
	actions(accessapi.ActionApiKeyRead)

	for name, fail := range map[string]func(){
		"authorize": func() { *w.role = role{err: errMissing} },
		"holds":     func() { *w.role = role{holdsErr: errMissing} },
		"setting":   func() { *w.role = role{below: true}; w.st.managersErr = errMissing },
	} {
		w.st.peopleOnly[w.me.AccountID], w.st.managersErr = false, nil
		*w.role = role{top: true}
		fail()
		if _, err := w.svc.Actions(ctx, w.me); !errors.Is(err, errMissing) {
			t.Errorf("%s fails: actions = %v", name, err)
		}
	}
}
