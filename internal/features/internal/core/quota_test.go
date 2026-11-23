package core_test

import (
	"errors"
	"testing"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestUnitQuota reads the effective value of a limit CheckLimit applies:
// none with billing off or no entitlement, the lower of the pool and the
// account's ceiling, the pool on a tie.
func TestUnitQuota(t *testing.T) {
	quota := func(v int64, pooled bool) featuresapi.Quota { return featuresapi.Quota{Value: &v, Pooled: pooled} }
	for _, c := range []struct {
		name    string
		s       *store
		want    featuresapi.Quota
		wantErr error
	}{
		{name: "billing off", s: &store{billingOff: true, entitlement: &featuresapi.Entitlement{Limits: map[string]int64{"seats": 1}}}},
		{name: "no entitlement", s: &store{}},
		{name: "neither bounds it", s: &store{entitlement: &featuresapi.Entitlement{}}},
		{name: "the pool alone", s: &store{entitlement: &featuresapi.Entitlement{Limits: map[string]int64{"seats": 3}}}, want: quota(3, true)},
		{name: "a lower ceiling", s: &store{
			entitlement: &featuresapi.Entitlement{Limits: map[string]int64{"seats": 3}},
			accounts:    map[string]int64{"seats": 2},
		}, want: quota(2, false)},
		{name: "an equal ceiling", s: &store{
			entitlement: &featuresapi.Entitlement{Limits: map[string]int64{"seats": 3}},
			accounts:    map[string]int64{"seats": 3},
		}, want: quota(3, true)},
		{name: "a ceiling without a pool", s: &store{
			entitlement: &featuresapi.Entitlement{},
			accounts:    map[string]int64{"seats": 4},
		}, want: quota(4, false)},
		{name: "billing unreadable", s: &store{billingErr: errStore}, wantErr: errStore},
		{name: "entitlement unreadable", s: &store{err: errStore}, wantErr: errStore},
		{name: "ceiling unreadable", s: &store{entitlement: &featuresapi.Entitlement{}, accountErr: errStore}, wantErr: errStore},
	} {
		t.Run(c.name, func(t *testing.T) {
			var n int
			got, err := core.New(c.s, inTx(&n), nil).Quota(t.Context(), kernel.NewOrganizationID(), kernel.NewAccountID(), "seats")
			if !errors.Is(err, c.wantErr) || got.Pooled != c.want.Pooled ||
				(got.Value == nil) != (c.want.Value == nil) || got.Value != nil && *got.Value != *c.want.Value {
				t.Errorf("Quota = %+v, %v; want %+v, %v", got, err, c.want, c.wantErr)
			}
		})
	}
}
