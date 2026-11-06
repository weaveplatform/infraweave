package apikey

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

var errMissing = errors.New("missing")

// mem is Store in memory: each key's sessions are the IDs in sessions.
type mem struct {
	keys     map[kernel.ULID]Key
	sessions map[kernel.ULID][]kernel.ULID
	managers map[kernel.AccountID]bool
	err      error
}

func newMem() *mem {
	return &mem{
		keys:     map[kernel.ULID]Key{},
		sessions: map[kernel.ULID][]kernel.ULID{},
		managers: map[kernel.AccountID]bool{},
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

type world struct {
	st      *mem
	svc     *Service
	dropped []kernel.ULID
	me      kernel.Principal
	other   kernel.Principal
}

func newWorld(allowed ...string) *world {
	w := &world{
		st: newMem(),
		me: kernel.Principal{
			Kind: kernel.PrincipalAPIKey, OrganizationID: kernel.NewOrganizationID(),
			AccountID: kernel.NewAccountID(), Login: "creator",
		},
		other: kernel.Principal{Kind: kernel.PrincipalAPIKey, AccountID: kernel.NewAccountID()},
	}
	w.svc = NewService(Deps{
		Store:          w.st,
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

// TestUnitIssuerAllowed checks DV-014: HTTPS only, and only an allowed
// issuer when any are configured.
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
