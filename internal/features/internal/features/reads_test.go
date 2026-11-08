package features_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	root "github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/features"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func seeded(names ...string) []root.Tier {
	out := make([]root.Tier, 0, len(names))
	for _, n := range names {
		t, _ := root.Seed(n)
		out = append(out, t)
	}
	return out
}

func TestUnitOrganizationTier(t *testing.T) {
	free := &featuresapi.Entitlement{Tier: root.Free, Features: []string{"stacks"}}
	enterprise, _ := root.Seed(root.Enterprise)
	freeTier, _ := root.Seed(root.Free)
	cases := []struct {
		name string
		s    *store
		want root.Tier
		err  error
	}{
		{
			name: "billing off reads Enterprise, whatever the organization's tier",
			s: &store{
				billingOff:  true,
				entitlement: free,
				tiers:       seeded(root.Free, root.Enterprise),
			},
			want: enterprise,
		},
		{
			name: "billing on reads the organization's tier",
			s:    &store{entitlement: free, tiers: seeded(root.Free, root.Enterprise)},
			want: freeTier,
		},
		{
			name: "an organization without an entitlement reads Enterprise",
			s:    &store{tiers: seeded(root.Enterprise)},
			want: enterprise,
		},
		{
			name: "a tier gone between the reads is not found",
			s:    &store{entitlement: free, tiers: seeded(root.Enterprise)},
			err:  featuresapi.ErrNotFound,
		},
		{name: "billing unreadable", s: &store{billingErr: errStore}, err: errStore},
		{name: "entitlement unreadable", s: &store{err: errStore}, err: errStore},
		{
			name: "tiers unreadable",
			s:    &store{billingOff: true, tiersErr: errStore},
			err:  errStore,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n int
			got, err := features.New(tc.s, inTx(&n), nil).
				OrganizationTier(t.Context(), kernel.NewOrganizationID())
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Errorf("OrganizationTier = %+v, %v, want %v", got, err, tc.err)
				}
				return
			}
			if err != nil || got.Name != tc.want.Name ||
				!slices.Equal(got.Features, tc.want.Features) || got.Limits == nil ||
				!reflect.DeepEqual(got.Limits, nonNil(tc.want.Limits)) {
				t.Errorf("OrganizationTier = %+v, %v, want %+v", got, err, tc.want)
			}
		})
	}
}

func nonNil(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

func TestUnitEntitled(t *testing.T) {
	var n int
	ctx, org := t.Context(), kernel.NewOrganizationID()
	on := &store{entitlement: &featuresapi.Entitlement{
		Tier: root.Free, Features: []string{"api-keys", "scans", "stacks"},
	}}
	got, err := features.New(on, inTx(&n), []string{"scans"}).Entitled(ctx, org)
	if err != nil || !slices.Equal(got, []string{"api-keys", "stacks"}) {
		t.Errorf("billing on = %v, %v", got, err)
	}
	got, err = features.New(&store{}, inTx(&n), []string{"scans"}).Entitled(ctx, org)
	want := slices.DeleteFunc(root.Gated(), func(f string) bool { return f == "scans" })
	if err != nil || !slices.Equal(got, root.Closed(want)) {
		t.Errorf("no entitlement = %v, %v, want %v", got, err, want)
	}
	if _, err := features.New(&store{err: errStore}, inTx(&n), nil).
		Entitled(ctx, org); !errors.Is(err, errStore) {
		t.Errorf("entitlement unreadable = %v", err)
	}
}

func TestUnitSeats(t *testing.T) {
	ten, three := int64(10), int64(3)
	pooled := &featuresapi.Entitlement{
		Tier: root.Starter, Limits: map[string]int64{featuresapi.Seats: ten},
	}
	used := featuresapi.Usage{Organization: 7, Account: 2}
	cases := []struct {
		name string
		s    *store
		want features.Seats
		err  error
	}{
		{
			name: "billing off sets no limit",
			s: &store{
				billingOff: true, entitlement: pooled,
				accounts: map[string]int64{featuresapi.Seats: three},
			},
			want: features.Seats{Organization: features.SeatLimit{InUse: 7}},
		},
		{
			name: "billing on reads the pool",
			s:    &store{entitlement: pooled},
			want: features.Seats{Organization: features.SeatLimit{InUse: 7, Limit: &ten}},
		},
		{
			name: "billing on reads the account's limit when one is set",
			s: &store{
				entitlement: pooled,
				accounts:    map[string]int64{featuresapi.Seats: three},
			},
			want: features.Seats{
				Organization: features.SeatLimit{InUse: 7, Limit: &ten},
				Account:      &features.SeatLimit{InUse: 2, Limit: &three},
			},
		},
		{
			name: "no pool is no limit",
			s:    &store{entitlement: &featuresapi.Entitlement{Tier: root.Enterprise}},
			want: features.Seats{Organization: features.SeatLimit{InUse: 7}},
		},
		{
			name: "an organization without an entitlement has no limit",
			s:    &store{accounts: map[string]int64{featuresapi.Seats: three}},
			want: features.Seats{Organization: features.SeatLimit{InUse: 7}},
		},
		{name: "billing unreadable", s: &store{billingErr: errStore}, err: errStore},
		{name: "entitlement unreadable", s: &store{err: errStore}, err: errStore},
		{
			name: "account limit unreadable",
			s:    &store{entitlement: pooled, accountErr: errStore},
			err:  errStore,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n int
			got, err := features.New(tc.s, inTx(&n), nil).
				Seats(t.Context(), kernel.NewOrganizationID(), kernel.NewAccountID(), used)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Errorf("Seats = %+v, %v, want %v", got, err, tc.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Seats = %+v, %v, want %+v", got, err, tc.want)
			}
		})
	}
}

// TestUnitSetAccountLimit checks the rule for an account limit: a limit
// of the catalogue, zero or more, and no more than the pool as it applies
// now, which billing off lifts.
//
// ref: decision 0021
func TestUnitSetAccountLimit(t *testing.T) {
	pooled := &featuresapi.Entitlement{
		Tier: root.Starter, Limits: map[string]int64{featuresapi.Seats: 10},
	}
	cases := []struct {
		name  string
		s     *store
		limit string
		value int64
		want  *featuresapi.AccountLimitError
		err   error
		set   bool
	}{
		{name: "at the pool", s: &store{entitlement: pooled}, limit: "seats", value: 10, set: true},
		{
			name: "above the pool", s: &store{entitlement: pooled}, limit: "seats", value: 11,
			want: &featuresapi.AccountLimitError{Limit: "seats", Value: 11, Pool: 10},
		},
		{
			name: "above the pool with billing off",
			s: &store{
				billingOff:  true,
				entitlement: pooled,
			},
			limit: "seats",
			value: 11,
			set:   true,
		},
		{
			name: "a limit the pool does not set",
			s:    &store{entitlement: pooled}, limit: "private-workers", value: 4, set: true,
		},
		{
			name: "negative", s: &store{entitlement: pooled}, limit: "seats", value: -1,
			want: &featuresapi.AccountLimitError{Limit: "seats", Value: -1},
		},
		{
			name: "a capability", s: &store{entitlement: pooled}, limit: "stacks", value: 1,
			want: &featuresapi.AccountLimitError{Limit: "stacks", Value: 1, Unknown: true},
		},
		{
			name: "no such name", s: &store{entitlement: pooled}, limit: "SEATS", value: 1,
			want: &featuresapi.AccountLimitError{Limit: "SEATS", Value: 1, Unknown: true},
		},
		{name: "entitlement unreadable", s: &store{err: errStore}, limit: "seats", err: errStore},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n int
			err := features.New(tc.s, inTx(&n), nil).SetAccountLimit(
				t.Context(), kernel.NewOrganizationID(), kernel.NewAccountID(), tc.limit, tc.value)
			le, _ := errors.AsType[*featuresapi.AccountLimitError](err)
			switch {
			case tc.want != nil && (le == nil || *le != *tc.want || le.Error() == ""):
				t.Errorf("SetAccountLimit = %v, want %+v", err, tc.want)
			case tc.err != nil && !errors.Is(err, tc.err):
				t.Errorf("SetAccountLimit = %v, want %v", err, tc.err)
			case tc.want == nil && tc.err == nil && err != nil:
				t.Errorf("SetAccountLimit = %v", err)
			}
			if set := slices.Contains(tc.s.calls, "set"); set != tc.set {
				t.Errorf("stored %t, want %t", set, tc.set)
			}
		})
	}
	var n int
	svc := features.New(&store{accounts: map[string]int64{"seats": 3}}, inTx(&n), nil)
	limits, err := svc.AccountLimits(t.Context(), kernel.NewAccountID())
	if err != nil || limits["seats"] != 3 {
		t.Errorf("AccountLimits = %v, %v", limits, err)
	}
	le, _ := errors.AsType[*featuresapi.AccountLimitError](
		svc.ClearAccountLimit(t.Context(), kernel.NewAccountID(), "stacks"))
	if le == nil || !le.Unknown || le.Limit != "stacks" {
		t.Errorf("clearing a capability = %+v", le)
	}
	want := []string{}
	for _, f := range root.Catalogue() {
		if f.Kind == root.Limit {
			want = append(want, f.Name)
		}
	}
	if got := svc.LimitNames(); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Errorf("LimitNames = %v, want %v", got, want)
	}
}
