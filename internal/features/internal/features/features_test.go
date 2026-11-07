package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/features"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var errStore = errors.New("store down")

// store is an in-memory features.Store of one organization.
type store struct {
	entitlement *featuresapi.Entitlement
	accounts    map[string]int64
	err         error
	accountErr  error
	locked      bool
	calls       []string
}

func (s *store) CreateOrganization(context.Context, kernel.OrganizationID, string) error {
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

// inTx runs fn directly, counting the transactions it stands for.
func inTx(n *int) features.Transactor {
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
	svc := features.New(s, inTx(&n), []string{"scans", "ai-integrations"})
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

func TestUnitEntitlementWritesPassThrough(t *testing.T) {
	s := &store{err: errStore}
	var n int
	svc := features.New(s, inTx(&n), nil)
	ctx, org, acct := t.Context(), kernel.NewOrganizationID(), kernel.NewAccountID()
	for name, err := range map[string]error{
		"create":         svc.CreateOrganization(ctx, org, "Enterprise"),
		"delete":         svc.DeleteOrganization(ctx, org),
		"set":            svc.SetAccountLimit(ctx, org, acct, "seats", 2),
		"delete account": svc.DeleteAccount(ctx, acct),
	} {
		if !errors.Is(err, errStore) {
			t.Errorf("%s = %v", name, err)
		}
	}
	slices.Sort(s.calls)
	if want := []string{"create", "delete", "delete account", "set"}; !slices.Equal(s.calls, want) {
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
			err := features.New(s, inTx(&n), nil).CheckLimit(t.Context(),
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
	if err := features.New(&store{err: errStore}, inTx(&n), nil).
		CheckLimit(ctx, org, acct, "seats", never); !errors.Is(err, errStore) {
		t.Errorf("a failed entitlement read = %v", err)
	}
	s := &store{entitlement: &featuresapi.Entitlement{Limits: map[string]int64{"seats": 1}}}
	if err := features.New(s, inTx(&n), nil).CheckLimit(ctx, org, acct, "seats",
		func(context.Context) (featuresapi.Usage, error) { return featuresapi.Usage{}, errStore },
	); !errors.Is(err, errStore) {
		t.Errorf("a failed count = %v", err)
	}
	s.accountErr = errStore
	if err := features.New(s, inTx(&n), nil).
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
