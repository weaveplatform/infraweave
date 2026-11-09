package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	root "github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var errStore = errors.New("store down")

// store is an in-memory core.Store of one organization, with billing
// on unless billingOff.
type store struct {
	entitlement *featuresapi.Entitlement
	accounts    map[string]int64
	billingOff  bool
	tiers       []root.Tier
	seeded      []root.Tier
	err         error
	accountErr  error
	billingErr  error
	tiersErr    error
	locked      bool
	calls       []string
	reads       map[string]int
	held        []core.Held
	billing     core.Billing
	grants      core.Grants
	grantsErr   error
	opErr       error
}

func (s *store) read(what string) {
	if s.reads == nil {
		s.reads = map[string]int{}
	}
	s.reads[what]++
}

func (s *store) Billing(context.Context) (bool, error) {
	s.read("billing")
	return !s.billingOff, s.billingErr
}

func (s *store) Tiers(context.Context) ([]root.Tier, error) {
	s.read("tiers")
	return s.tiers, s.tiersErr
}

func (s *store) Seed(_ context.Context, seeds []root.Tier) error {
	s.seeded = seeds
	return s.err
}

func (s *store) CreateOrganization(context.Context, kernel.OrganizationID) error {
	s.calls = append(s.calls, "create")
	return s.err
}

func (s *store) DeleteOrganization(context.Context, kernel.OrganizationID) error {
	s.calls = append(s.calls, "delete")
	return s.err
}

func (s *store) SetAccountLimit(
	context.Context, kernel.OrganizationID, kernel.AccountID, string, int64,
) error {
	s.calls = append(s.calls, "set")
	return s.err
}

func (s *store) DeleteAccount(context.Context, kernel.AccountID) error {
	s.calls = append(s.calls, "delete account")
	return s.err
}

func (s *store) Entitlement(
	_ context.Context,
	_ kernel.OrganizationID,
	lock bool,
) (featuresapi.Entitlement, error) {
	s.locked = lock
	s.read("entitlement")
	if s.err != nil {
		return featuresapi.Entitlement{}, s.err
	}
	if s.entitlement == nil {
		return featuresapi.Entitlement{}, featuresapi.ErrNotFound
	}
	e := *s.entitlement
	e.Features = slices.Clone(e.Features)
	return e, nil
}

func (s *store) AccountLimit(
	_ context.Context,
	_ kernel.AccountID,
	limit string,
) (int64, bool, error) {
	v, ok := s.accounts[limit]
	return v, ok, s.accountErr
}

func (s *store) ClearAccountLimit(context.Context, kernel.AccountID, string) error {
	s.calls = append(s.calls, "clear")
	return s.err
}

func (s *store) AccountLimits(context.Context, kernel.AccountID) (map[string]int64, error) {
	return s.accounts, s.accountErr
}

// inTx runs fn directly, counting the transactions it stands for.
func inTx(n *int) core.Transactor {
	return func(ctx context.Context, fn func(context.Context) error) error {
		*n++
		return fn(ctx)
	}
}

func TestUnitEntitlementLessDisabled(t *testing.T) {
	s := &store{entitlement: &featuresapi.Entitlement{
		Tier: "Business", Version: 3,
		Features: []string{"ai-integrations", "scans", "stacks"},
		Limits:   map[string]int64{"seats": 10},
	}}
	var n int
	svc := core.New(s, inTx(&n), []string{"scans", "ai-integrations"})
	e, err := svc.Entitlement(t.Context(), kernel.NewOrganizationID())
	if err != nil || e.Tier != "Business" || e.Version != 3 ||
		!slices.Equal(e.Features, []string{"stacks"}) || e.Limits["seats"] != 10 || s.locked {
		t.Errorf("Entitlement = %+v, %v (locked %t)", e, err, s.locked)
	}
	s.err = errStore
	if _, err := svc.Entitlement(
		t.Context(),
		kernel.NewOrganizationID(),
	); !errors.Is(
		err,
		errStore,
	) {
		t.Errorf("a failed read = %v", err)
	}
}

// TestUnitEntitlement checks the billing switch: off, every organization
// reads as Enterprise less the switched-off features; on, it reads as its
// tier and grants, which the store has already merged.
//
// ref: decision 0021
func TestUnitEntitlement(t *testing.T) {
	stored := &featuresapi.Entitlement{
		Tier: "Free", Version: 2,
		Features: []string{"api-keys", "stacks"},
		Limits:   map[string]int64{"seats": 2},
	}
	var n int
	ctx, org := t.Context(), kernel.NewOrganizationID()
	off := &store{billingOff: true, entitlement: stored}
	e, err := core.New(off, inTx(&n), []string{"scans"}).Entitlement(ctx, org)
	want := slices.DeleteFunc(root.Gated(), func(f string) bool { return f == "scans" })
	if err != nil || e.Tier != "Free" || e.Version != 2 || !slices.Equal(e.Features, want) ||
		len(e.Limits) != 0 {
		t.Errorf("billing off = %+v, %v", e, err)
	}
	on := &store{entitlement: stored}
	e, err = core.New(on, inTx(&n), []string{"scans"}).Entitlement(ctx, org)
	if err != nil || !slices.Equal(e.Features, stored.Features) || e.Limits["seats"] != 2 {
		t.Errorf("billing on = %+v, %v", e, err)
	}
	broken := &store{billingErr: errStore, entitlement: stored}
	if _, err := core.New(broken, inTx(&n), nil).
		Entitlement(ctx, org); !errors.Is(
		err,
		errStore,
	) {
		t.Errorf("billing unreadable = %v", err)
	}
}

func TestUnitEntitlementWritesPassThrough(t *testing.T) {
	s := &store{err: errStore}
	var n int
	svc := core.New(s, inTx(&n), nil)
	ctx, org, acct := t.Context(), kernel.NewOrganizationID(), kernel.NewAccountID()
	for name, err := range map[string]error{
		"create":         svc.CreateOrganization(ctx, org),
		"delete":         svc.DeleteOrganization(ctx, org),
		"clear":          svc.ClearAccountLimit(ctx, acct, "seats"),
		"delete account": svc.DeleteAccount(ctx, acct),
	} {
		if !errors.Is(err, errStore) {
			t.Errorf("%s = %v", name, err)
		}
	}
	slices.Sort(s.calls)
	if want := []string{
		"clear",
		"create",
		"delete",
		"delete account",
	}; !slices.Equal(
		s.calls,
		want,
	) {
		t.Errorf("calls = %v, want %v", s.calls, want)
	}
}

func TestUnitPooledLimits(t *testing.T) {
	cases := []struct {
		name     string
		pool     map[string]int64
		account  map[string]int64
		used     featuresapi.Usage
		want     *featuresapi.LimitError
		counted  bool
		noEntitl bool
	}{
		{name: "no entitlement", noEntitl: true},
		{name: "neither limited", pool: map[string]int64{}},
		{
			name:    "below both",
			pool:    map[string]int64{"seats": 3},
			account: map[string]int64{"seats": 2},
			used:    featuresapi.Usage{Organization: 2, Account: 1},
			counted: true,
		},
		{
			name:    "pool reached",
			pool:    map[string]int64{"seats": 3},
			used:    featuresapi.Usage{Organization: 3},
			counted: true,
			want: &featuresapi.LimitError{
				Limit: "seats",
				Scope: featuresapi.ScopeOrganization,
				Value: 3,
			},
		},
		{
			name: "account reached with room in the pool",
			pool: map[string]int64{"seats": 3},
			account: map[string]int64{
				"seats": 1,
			},
			used:    featuresapi.Usage{Organization: 1, Account: 1},
			counted: true,
			want: &featuresapi.LimitError{
				Limit: "seats",
				Scope: featuresapi.ScopeAccount,
				Value: 1,
			},
		},
		{
			name: "both reached names the pool",
			pool: map[string]int64{"seats": 2},
			account: map[string]int64{
				"seats": 1,
			},
			used:    featuresapi.Usage{Organization: 2, Account: 1},
			counted: true,
			want: &featuresapi.LimitError{
				Limit: "seats",
				Scope: featuresapi.ScopeOrganization,
				Value: 2,
			},
		},
		{
			name:    "an account limit without a pool",
			pool:    map[string]int64{},
			account: map[string]int64{"seats": 0},
			counted: true,
			want: &featuresapi.LimitError{
				Limit: "seats",
				Scope: featuresapi.ScopeAccount,
				Value: 0,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &store{accounts: c.account}
			if !c.noEntitl {
				s.entitlement = &featuresapi.Entitlement{Tier: "Free", Limits: c.pool}
			}
			var n int
			counted := false
			err := core.New(s, inTx(&n), nil).CheckLimit(t.Context(),
				kernel.NewOrganizationID(), kernel.NewAccountID(), "seats",
				func(context.Context) (featuresapi.Usage, error) {
					counted = true
					return c.used, nil
				})
			le, _ := errors.AsType[*featuresapi.LimitError](err)
			switch {
			case c.want == nil && err != nil, c.want != nil && (le == nil || *le != *c.want):
				t.Errorf("CheckLimit = %v, want %v", err, c.want)
			case counted != c.counted:
				t.Errorf("counted = %t, want %t", counted, c.counted)
			case n != 1 || !s.locked && !c.noEntitl:
				t.Errorf("%d transactions, locked %t", n, s.locked)
			}
		})
	}
}

func TestUnitPooledLimitsFailures(t *testing.T) {
	var n int
	ctx, org, acct := t.Context(), kernel.NewOrganizationID(), kernel.NewAccountID()
	never := func(context.Context) (featuresapi.Usage, error) {
		t.Error("counted after a failed read")
		return featuresapi.Usage{}, nil
	}
	if err := core.New(&store{err: errStore}, inTx(&n), nil).
		CheckLimit(ctx, org, acct, "seats", never); !errors.Is(err, errStore) {
		t.Errorf("a failed entitlement read = %v", err)
	}
	s := &store{entitlement: &featuresapi.Entitlement{Limits: map[string]int64{"seats": 1}}}
	if err := core.New(s, inTx(&n), nil).CheckLimit(ctx, org, acct, "seats",
		func(context.Context) (featuresapi.Usage, error) { return featuresapi.Usage{}, errStore },
	); !errors.Is(err, errStore) {
		t.Errorf("a failed count = %v", err)
	}
	s.accountErr = errStore
	if err := core.New(s, inTx(&n), nil).
		CheckLimit(ctx, org, acct, "seats", never); !errors.Is(err, errStore) {
		t.Errorf("a failed account limit read = %v", err)
	}
}

func TestUnitLimitError(t *testing.T) {
	e := &featuresapi.LimitError{Limit: "seats", Scope: featuresapi.ScopeAccount, Value: 4}
	if got := e.Error(); got != "features: seats reached its account limit of 4" {
		t.Errorf("Error = %q", got)
	}
}

func TestUnitSeed(t *testing.T) {
	s := &store{}
	var n int
	if err := core.New(s, inTx(&n), nil).Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(s.seeded))
	for _, tier := range s.seeded {
		names = append(names, tier.Name)
	}
	if !slices.Equal(names, root.SeededTiers) {
		t.Errorf("seeded %v, want %v", names, root.SeededTiers)
	}
	s.err = errStore
	if err := core.New(s, inTx(&n), nil).Seed(t.Context()); !errors.Is(err, errStore) {
		t.Errorf("a failed seed = %v", err)
	}
}

// TestUnitEntitlementSnapshot checks that a replica reads an organization's
// entitlement, the billing switch and the tiers once, until an
// invalidation for them drops the snapshot.
func TestUnitEntitlementSnapshot(t *testing.T) {
	s := &store{entitlement: &featuresapi.Entitlement{Tier: "Free", Features: []string{"stacks"}}}
	var n int
	svc := core.New(s, inTx(&n), nil)
	ctx, org, other := t.Context(), kernel.NewOrganizationID(), kernel.NewOrganizationID()
	read := func() {
		t.Helper()
		if _, err := svc.Entitlement(ctx, org); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Billing(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []struct {
		name               string
		invalidate         func()
		entitlement, bills int
	}{
		{"first read", func() {}, 1, 1},
		{"cached", func() {}, 1, 1},
		{"another domain's payload", func() { svc.Invalidate("access:" + org.String()) }, 1, 1},
		{"another organization", func() { svc.Invalidate(core.InvalidateKey(other)) }, 1, 1},
		{"this organization", func() { svc.Invalidate(core.InvalidateKey(org)) }, 2, 1},
		{"the billing switch", func() { svc.Invalidate("features:billing") }, 3, 2},
		{"a missed notification", svc.Flush, 4, 3},
	} {
		step.invalidate()
		read()
		if s.reads["entitlement"] != step.entitlement || s.reads["billing"] != step.bills {
			t.Errorf("%s: reads %v, want entitlement %d, billing %d",
				step.name, s.reads, step.entitlement, step.bills)
		}
	}
	s.billingErr = errStore
	svc.Flush()
	if _, err := svc.Billing(ctx); !errors.Is(err, errStore) {
		t.Errorf("a failed billing read = %v", err)
	}
}

// TestUnitEntitlementClosed checks that a feature whose requirement the
// installation switched off leaves the entitlement with it.
func TestUnitEntitlementClosed(t *testing.T) {
	var dependent root.Feature
	for _, f := range root.Catalogue() {
		if f.Gated() && len(f.Requires) > 0 {
			dependent = f
			break
		}
	}
	if dependent.Name == "" {
		t.Skip("no feature in the catalogue requires another")
	}
	s := &store{entitlement: &featuresapi.Entitlement{
		Features: append([]string{dependent.Name}, dependent.Requires...),
	}}
	var n int
	e, err := core.New(s, inTx(&n), dependent.Requires[:1]).
		Entitlement(t.Context(), kernel.NewOrganizationID())
	if err != nil || slices.Contains(e.Features, dependent.Name) {
		t.Errorf("Entitlement = %v, %v; want %s dropped with %s",
			e.Features, err, dependent.Name, dependent.Requires[0])
	}
}

func TestUnitAllow(t *testing.T) {
	org := kernel.NewOrganizationID()
	member := kernel.WithPrincipal(t.Context(), kernel.Principal{OrganizationID: org})
	operator := kernel.WithPrincipal(t.Context(), kernel.Principal{})
	tiers := []root.Tier{
		{Name: "Business", Features: []string{"policies", "scans", "stacks"}},
		{Name: "Alpha", Features: []string{"policies", "stacks"}},
		{Name: "Starter", Features: []string{"policies", "stacks"}},
		{Name: "Free", Features: []string{"stacks"}},
	}
	free := &featuresapi.Entitlement{Tier: "Free", Features: []string{"stacks"}}
	cases := []struct {
		name    string
		ctx     context.Context
		store   *store
		feature string
		want    *core.Refusal
		err     error
	}{
		{
			name: "switched off refuses every principal", ctx: operator,
			store: &store{}, feature: "scans",
			want: &core.Refusal{Disabled: true, Feature: "scans"},
		},
		{name: "no principal", ctx: t.Context(), store: &store{}, feature: "policies"},
		{name: "an operator", ctx: operator, store: &store{}, feature: "policies"},
		{
			name: "billing off", ctx: member,
			store: &store{billingOff: true, entitlement: free}, feature: "policies",
		},
		{name: "no entitlement", ctx: member, store: &store{}, feature: "policies"},
		{name: "held", ctx: member, store: &store{entitlement: free}, feature: "stacks"},
		{
			name:    "lacked names the tiers smallest first",
			ctx:     member,
			store:   &store{entitlement: free, tiers: tiers},
			feature: "policies",
			want: &core.Refusal{
				Feature: "policies",
				Tiers:   []string{"Alpha", "Starter", "Business"},
			},
		},
		{
			name: "lacked by every tier", ctx: member,
			store: &store{entitlement: free, tiers: tiers}, feature: "audit-trail",
			want: &core.Refusal{Feature: "audit-trail", Tiers: []string{}},
		},
		{
			name: "billing unreadable", ctx: member,
			store: &store{billingErr: errStore}, feature: "policies", err: errStore,
		},
		{
			name: "entitlement unreadable", ctx: member,
			store: &store{err: errStore}, feature: "policies", err: errStore,
		},
		{
			name: "tiers unreadable",
			ctx:  member,
			store: &store{
				entitlement: free,
				tiersErr:    errStore,
			},
			feature: "policies",
			err:     errStore,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var n int
			err := core.New(c.store, inTx(&n), []string{"scans"}).Allow(c.ctx, c.feature)
			got, _ := errors.AsType[*core.Refusal](err)
			if c.err != nil && !errors.Is(err, c.err) ||
				c.err == nil && c.want == nil && err != nil ||
				!reflect.DeepEqual(got, c.want) {
				t.Errorf("Allow = %v; want %+v, %v", err, c.want, c.err)
			}
		})
	}
}

// TestUnitAllowTiersSnapshot checks that the tiers a refusal names are
// read once, until a tier changes.
func TestUnitAllowTiersSnapshot(t *testing.T) {
	s := &store{
		entitlement: &featuresapi.Entitlement{},
		tiers:       []root.Tier{{Name: "Free", Features: []string{"stacks"}}},
	}
	var n int
	svc := core.New(s, inTx(&n), nil)
	ctx := kernel.WithPrincipal(
		t.Context(),
		kernel.Principal{OrganizationID: kernel.NewOrganizationID()},
	)
	refused := func() {
		t.Helper()
		err := svc.Allow(ctx, "stacks")
		if r, ok := errors.AsType[*core.Refusal](
			err,
		); !ok ||
			!slices.Equal(r.Tiers, []string{"Free"}) {
			t.Fatalf("Allow = %v", err)
		}
	}
	refused()
	refused()
	svc.Invalidate("features:tier:Free")
	refused()
	if s.reads["tiers"] != 2 {
		t.Errorf("tiers read %d times, want 2", s.reads["tiers"])
	}
}

func TestUnitPooledLimitsBillingOff(t *testing.T) {
	var n int
	never := func(context.Context) (featuresapi.Usage, error) {
		t.Error("counted with billing off")
		return featuresapi.Usage{}, nil
	}
	s := &store{billingOff: true, entitlement: &featuresapi.Entitlement{
		Limits: map[string]int64{"seats": 0},
	}}
	if err := core.New(s, inTx(&n), nil).CheckLimit(t.Context(),
		kernel.NewOrganizationID(), kernel.NewAccountID(), "seats", never); err != nil {
		t.Errorf("CheckLimit with billing off = %v", err)
	}
	s = &store{billingErr: errStore}
	if err := core.New(s, inTx(&n), nil).CheckLimit(t.Context(),
		kernel.NewOrganizationID(),
		kernel.NewAccountID(), "seats", never); !errors.Is(err, errStore) {
		t.Errorf("CheckLimit with billing unreadable = %v", err)
	}
}

func TestUnitRefusalError(t *testing.T) {
	for r, want := range map[*core.Refusal]string{
		{Disabled: true, Feature: "scans"}: "features: scans is switched off",
		{Feature: "scans"}:                 "features: scans is not in the organization's tier",
	} {
		if r.Error() != want {
			t.Errorf("Error = %q, want %q", r.Error(), want)
		}
	}
}
