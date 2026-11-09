package http

import (
	"context"
	"slices"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// The lists' cursor names. The vendor-bound lists are always empty, yet
// a cursor sent to one is checked as any list's is.
const (
	tierFeaturesList = "tier-features"
	addonsList       = "billing-addons"
	plansList        = "self-service-plans"
	flagsList        = "feature-flags"
)

// SeatCounter counts the seats in use in org and in acct, which the
// access domain owns.
type SeatCounter func(
	ctx context.Context,
	org kernel.OrganizationID,
	acct kernel.AccountID,
) (featuresapi.Usage, error)

// Server is the features domain's partial of the API's strict server: the
// Billing and Feature Flags reads, answered from the installation's own
// data in place of the vendor's billing, and the subscription mutations,
// which no installation can perform.
//
// ref: decision 0021
type Server struct {
	svc   *core.Service
	seats SeatCounter
}

// NewServer returns the partial serving through svc, counting seats
// through seats.
func NewServer(svc *core.Service, seats SeatCounter) Server {
	return Server{svc: svc, seats: seats}
}

// unsupported is the subscription mutations' answer: infraweave has no
// billing service, and a pretended success would fail later, unseen.
func unsupported() error {
	//nolint:wrapcheck // a problem, answered as it is
	return problem.InstallationUnsupported.Error(
		"infraweave has no billing service; the operator sets an organization's tier " +
			"through the operator API.")
}

func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return p, problem.Unauthenticated.Error("The request carries no credential.")
	}
	return p, nil
}

// checkCursor refuses a cursor that is not one of list's.
func checkCursor(list string, cursor *string) error {
	if cursor == nil {
		return nil
	}
	_, err := page.Decode[string](list, *cursor)
	return err //nolint:wrapcheck // a problem, answered as it is
}

// Tier answers the organization's tier as a tier object, not the
// reference's enum, since tiers are data the operator defines.
//
// ref: DV-079
func (s Server) Tier(ctx context.Context, _ api.TierRequestObject) (api.TierResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	t, err := s.svc.OrganizationTier(ctx, p.OrganizationID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	return api.Tier200JSONResponse{
		Body: api.Tier{Name: t.Name, Features: t.Features, Limits: t.Limits},
	}, nil
}

// TierFeatures pages through the organization's entitlement by name.
func (s Server) TierFeatures(
	ctx context.Context,
	req api.TierFeaturesRequestObject,
) (api.TierFeaturesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	after := ""
	if req.Params.Cursor != nil {
		c, err := page.Decode[string](tierFeaturesList, *req.Params.Cursor)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		after = c.ID
	}
	held, err := s.svc.Entitled(ctx, p.OrganizationID)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	i, _ := slices.BinarySearch(held, after)
	if i < len(held) && held[i] == after {
		i++
	}
	rest := held[i:]
	n := int(kernel.PageLimit(req.Params.Limit))
	body := api.TierFeaturePage{Items: slices.Clone(rest[:min(len(rest), n)])}
	if len(rest) > n {
		last := rest[n-1]
		next, err := page.Encode(tierFeaturesList, kernel.Cursor[string]{Key: last, ID: last})
		if err != nil {
			return nil, err //nolint:wrapcheck // encoding a string cannot fail
		}
		body.NextCursor = &next
	}
	return api.TierFeatures200JSONResponse{Body: body}, nil
}

// Seats answers the account's seats: its organization's pool, and its own
// limit when one is set, where members and API keys share one count.
//
// ref: DV-080
func (s Server) Seats(
	ctx context.Context,
	_ api.SeatsRequestObject,
) (api.SeatsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	used, err := s.seats(ctx, p.OrganizationID, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names itself
	}
	st, err := s.svc.Seats(ctx, p.OrganizationID, p.AccountID, used)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	body := api.Seats{Organization: api.SeatLimit(st.Organization)}
	if st.Account != nil {
		a := api.SeatLimit(*st.Account)
		body.Account = &a
	}
	return api.Seats200JSONResponse{Body: body}, nil
}

// BilledExternally answers true: no Stripe subscription exists.
func (Server) BilledExternally(
	context.Context,
	api.BilledExternallyRequestObject,
) (api.BilledExternallyResponseObject, error) {
	return api.BilledExternally200JSONResponse{Body: true}, nil
}

// BillingSubscription answers null: no Stripe subscription exists.
func (Server) BillingSubscription(
	context.Context,
	api.BillingSubscriptionRequestObject,
) (api.BillingSubscriptionResponseObject, error) {
	return api.BillingSubscription200JSONResponse{}, nil
}

// OnTrialUntil answers null: no organization is ever on a trial.
func (Server) OnTrialUntil(
	context.Context,
	api.OnTrialUntilRequestObject,
) (api.OnTrialUntilResponseObject, error) {
	return api.OnTrialUntil200JSONResponse{}, nil
}

// AvailableBillingAddons answers no add-ons.
func (Server) AvailableBillingAddons(
	_ context.Context,
	req api.AvailableBillingAddonsRequestObject,
) (api.AvailableBillingAddonsResponseObject, error) {
	if err := checkCursor(addonsList, req.Params.Cursor); err != nil {
		return nil, err
	}
	return api.AvailableBillingAddons200JSONResponse{
		Body: api.BillingAddonPage{Items: []api.BillingAddon{}},
	}, nil
}

// AvailableSelfServicePlans answers no plans.
func (Server) AvailableSelfServicePlans(
	_ context.Context,
	req api.AvailableSelfServicePlansRequestObject,
) (api.AvailableSelfServicePlansResponseObject, error) {
	if err := checkCursor(plansList, req.Params.Cursor); err != nil {
		return nil, err
	}
	return api.AvailableSelfServicePlans200JSONResponse{
		Body: api.BillingPlanPage{Items: []api.BillingPlan{}},
	}, nil
}

// BillingSubscriptionCreate answers 403 installation-unsupported.
func (Server) BillingSubscriptionCreate(
	context.Context,
	api.BillingSubscriptionCreateRequestObject,
) (api.BillingSubscriptionCreateResponseObject, error) {
	return nil, unsupported()
}

// BillingSubscriptionUpdateV2 answers 403 installation-unsupported.
func (Server) BillingSubscriptionUpdateV2(
	context.Context,
	api.BillingSubscriptionUpdateV2RequestObject,
) (api.BillingSubscriptionUpdateV2ResponseObject, error) {
	return nil, unsupported()
}

// BillingSubscriptionDelete answers 403 installation-unsupported.
func (Server) BillingSubscriptionDelete(
	context.Context,
	api.BillingSubscriptionDeleteRequestObject,
) (api.BillingSubscriptionDeleteResponseObject, error) {
	return nil, unsupported()
}

// BillingSubscriptionUpdateInfo answers 403 installation-unsupported.
func (Server) BillingSubscriptionUpdateInfo(
	context.Context,
	api.BillingSubscriptionUpdateInfoRequestObject,
) (api.BillingSubscriptionUpdateInfoResponseObject, error) {
	return nil, unsupported()
}

// BillingSubscriptionUpdateTier answers 403 installation-unsupported.
func (Server) BillingSubscriptionUpdateTier(
	context.Context,
	api.BillingSubscriptionUpdateTierRequestObject,
) (api.BillingSubscriptionUpdateTierResponseObject, error) {
	return nil, unsupported()
}

// EvaluateFeatureFlags answers an empty set: infraweave holds none of
// Spacelift's flags, as Spacelift SaaS answers.
func (Server) EvaluateFeatureFlags(
	_ context.Context,
	req api.EvaluateFeatureFlagsRequestObject,
) (api.EvaluateFeatureFlagsResponseObject, error) {
	if err := checkCursor(flagsList, req.Params.Cursor); err != nil {
		return nil, err
	}
	return api.EvaluateFeatureFlags200JSONResponse{
		Body: api.FeatureFlagEvaluationResultPage{Items: []api.FeatureFlagEvaluationResult{}},
	}, nil
}
