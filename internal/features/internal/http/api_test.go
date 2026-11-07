package http

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// served returns the partial over a seeded database with "acme" on
// Starter and a seats limit of 1 on its account, billing switched as on
// says, and a context carrying a principal of the account.
func served(t *testing.T, on bool) (Server, context.Context) {
	t.Helper()
	svc, _, acme := seeded(t)
	ctx := t.Context()
	acct := kernel.NewAccountID()
	if _, err := svc.SetTier(ctx, acme, features.Starter); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAccountLimit(ctx, acme, acct, featuresapi.Seats, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetBilling(ctx, on, nil); err != nil {
		t.Fatal(err)
	}
	// No watcher runs here to drop the snapshots the writes above read.
	svc.Flush()
	seats := func(_ context.Context, org kernel.OrganizationID, a kernel.AccountID) (featuresapi.Usage, error) {
		if org != acme || a != acct {
			return featuresapi.Usage{}, errDown
		}
		return featuresapi.Usage{Organization: 4, Account: 1}, nil
	}
	return NewServer(svc, seats), kernel.WithPrincipal(ctx, kernel.Principal{
		Kind: kernel.PrincipalAPIKey, OrganizationID: acme, AccountID: acct,
	})
}

func TestAccBillingReads(t *testing.T) {
	starter, _ := features.Seed(features.Starter)
	enterprise, _ := features.Seed(features.Enterprise)
	pool := starter.Limits[featuresapi.Seats]
	one := int64(1)
	cases := []struct {
		name     string
		on       bool
		tier     features.Tier
		entitled []string
		seats    api.Seats
	}{
		{
			name:     "billing off",
			tier:     enterprise,
			entitled: features.Gated(),
			seats:    api.Seats{Organization: api.SeatLimit{InUse: 4}},
		},
		{
			name:     "billing on",
			on:       true,
			tier:     starter,
			entitled: starter.Features,
			seats: api.Seats{
				Organization: api.SeatLimit{InUse: 4, Limit: &pool},
				Account:      &api.SeatLimit{InUse: 1, Limit: &one},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := served(t, tc.on)
			r, err := s.Tier(ctx, api.TierRequestObject{})
			tier, ok := r.(api.Tier200JSONResponse)
			limits := tc.tier.Limits
			if limits == nil {
				limits = map[string]int64{}
			}
			if err != nil || !ok || tier.Body.Name != tc.tier.Name ||
				!slices.Equal(tier.Body.Features, slices.Sorted(slices.Values(tc.tier.Features))) ||
				!reflect.DeepEqual(tier.Body.Limits, limits) {
				t.Errorf("tier = %+v, %v, want %+v", r, err, tc.tier)
			}

			var got []string
			two := int32(2)
			req := api.TierFeaturesRequestObject{Params: api.TierFeaturesParams{Limit: &two}}
			for {
				r, err := s.TierFeatures(ctx, req)
				p, ok := r.(api.TierFeatures200JSONResponse)
				if err != nil || !ok {
					t.Fatalf("tierFeatures = %+v, %v", r, err)
				}
				got = append(got, p.Body.Items...)
				if p.Body.NextCursor == nil {
					break
				}
				req.Params.Cursor = p.Body.NextCursor
			}
			if want := features.Closed(
				slices.Sorted(slices.Values(tc.entitled)),
			); !slices.Equal(
				got,
				want,
			) {
				t.Errorf("tierFeatures = %v, want %v", got, want)
			}

			r2, err := s.Seats(ctx, api.SeatsRequestObject{})
			if seats, ok := r2.(api.Seats200JSONResponse); err != nil || !ok ||
				!reflect.DeepEqual(seats.Body, tc.seats) {
				t.Errorf("seats = %+v, %v, want %+v", r2, err, tc.seats)
			}
		})
	}
}

func TestAccBillingReadsRefused(t *testing.T) {
	s, ctx := served(t, true)
	bare := t.Context()
	_, err := s.Tier(bare, api.TierRequestObject{})
	wantType(t, "tier without a principal", err, problem.Unauthenticated)
	_, err = s.TierFeatures(bare, api.TierFeaturesRequestObject{})
	wantType(t, "tierFeatures without a principal", err, problem.Unauthenticated)
	_, err = s.Seats(bare, api.SeatsRequestObject{})
	wantType(t, "seats without a principal", err, problem.Unauthenticated)

	bad := "nope"
	_, err = s.TierFeatures(ctx, api.TierFeaturesRequestObject{
		Params: api.TierFeaturesParams{Cursor: &bad},
	})
	wantType(t, "a bad tierFeatures cursor", err, problem.InvalidCursor)

	other := kernel.WithPrincipal(t.Context(), kernel.Principal{
		OrganizationID: kernel.NewOrganizationID(), AccountID: kernel.NewAccountID(),
	})
	if _, err := s.Seats(other, api.SeatsRequestObject{}); !errors.Is(err, errDown) {
		t.Errorf("seats uncounted = %v", err)
	}
}

func TestUnitVendorBound(t *testing.T) {
	var s Server
	ctx := t.Context()
	if r, err := s.BilledExternally(ctx, api.BilledExternallyRequestObject{}); err != nil ||
		r != (api.BilledExternally200JSONResponse{Body: true}) {
		t.Errorf("billedExternally = %+v, %v", r, err)
	}
	if r, err := s.BillingSubscription(ctx, api.BillingSubscriptionRequestObject{}); err != nil ||
		r.(api.BillingSubscription200JSONResponse).Body != nil {
		t.Errorf("billingSubscription = %+v, %v", r, err)
	}
	if r, err := s.OnTrialUntil(ctx, api.OnTrialUntilRequestObject{}); err != nil ||
		r.(api.OnTrialUntil200JSONResponse).Body != nil {
		t.Errorf("onTrialUntil = %+v, %v", r, err)
	}

	bad := "nope"
	lists := map[string]func(cursor *string) (int, error){
		"availableBillingAddons": func(c *string) (int, error) {
			r, err := s.AvailableBillingAddons(ctx, api.AvailableBillingAddonsRequestObject{
				Params: api.AvailableBillingAddonsParams{Cursor: c},
			})
			if err != nil {
				return 0, err
			}
			return len(r.(api.AvailableBillingAddons200JSONResponse).Body.Items), nil
		},
		"availableSelfServicePlans": func(c *string) (int, error) {
			r, err := s.AvailableSelfServicePlans(ctx, api.AvailableSelfServicePlansRequestObject{
				Params: api.AvailableSelfServicePlansParams{Cursor: c},
			})
			if err != nil {
				return 0, err
			}
			return len(r.(api.AvailableSelfServicePlans200JSONResponse).Body.Items), nil
		},
		"evaluateFeatureFlags": func(c *string) (int, error) {
			r, err := s.EvaluateFeatureFlags(ctx, api.EvaluateFeatureFlagsRequestObject{
				Params: api.EvaluateFeatureFlagsParams{Cursor: c},
			})
			if err != nil {
				return 0, err
			}
			body := r.(api.EvaluateFeatureFlags200JSONResponse).Body
			if body.Items == nil {
				return -1, nil
			}
			return len(body.Items), nil
		},
	}
	for name, list := range lists {
		if n, err := list(nil); err != nil || n != 0 {
			t.Errorf("%s = %d items, %v", name, n, err)
		}
		_, err := list(&bad)
		wantType(t, name+" with a bad cursor", err, problem.InvalidCursor)
	}

	for name, call := range map[string]func() error{
		"billingSubscriptionCreate": func() error {
			_, err := s.BillingSubscriptionCreate(ctx, api.BillingSubscriptionCreateRequestObject{})
			return err
		},
		"billingSubscriptionUpdateV2": func() error {
			_, err := s.BillingSubscriptionUpdateV2(ctx, api.BillingSubscriptionUpdateV2RequestObject{})
			return err
		},
		"billingSubscriptionDelete": func() error {
			_, err := s.BillingSubscriptionDelete(ctx, api.BillingSubscriptionDeleteRequestObject{})
			return err
		},
		"billingSubscriptionUpdateInfo": func() error {
			_, err := s.BillingSubscriptionUpdateInfo(ctx, api.BillingSubscriptionUpdateInfoRequestObject{})
			return err
		},
		"billingSubscriptionUpdateTier": func() error {
			_, err := s.BillingSubscriptionUpdateTier(ctx, api.BillingSubscriptionUpdateTierRequestObject{})
			return err
		},
	} {
		wantType(t, name, call(), problem.InstallationUnsupported)
	}
}
