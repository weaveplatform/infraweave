package http

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/weaveplatform/infraweave/internal/features"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// tiersList names the tier list's cursors.
const tiersList = "tiers"

// Organizations resolves an organization's slug, which the access domain
// owns; ok is false when no organization has it.
type Organizations func(ctx context.Context, slug string) (id kernel.OrganizationID, ok bool, err error)

// OperatorServer is the features domain's partial of the operator API's
// strict server: tiers, the billing switch, and each organization's tier
// and grants. The operator authenticator admits operator principals
// alone, so every caller is one.
type OperatorServer struct {
	svc  *core.Service
	orgs Organizations
}

// NewOperatorServer returns the partial serving through svc, naming
// organizations through orgs.
func NewOperatorServer(svc *core.Service, orgs Organizations) OperatorServer {
	return OperatorServer{svc: svc, orgs: orgs}
}

// Tiers lists the installation's tiers by name.
func (s OperatorServer) Tiers(
	ctx context.Context,
	req opapi.TiersRequestObject,
) (opapi.TiersResponseObject, error) {
	after := ""
	if req.Params.Cursor != nil {
		c, err := page.Decode[string](tiersList, *req.Params.Cursor)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		after = c.ID
	}
	ts, err := s.svc.Tiers(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	ts = slices.DeleteFunc(ts, func(h core.Held) bool { return h.Name <= after })
	n := int(kernel.PageLimit(req.Params.Limit))
	body := opapi.TierPage{Items: make([]opapi.Tier, 0, min(len(ts), n))}
	for _, h := range ts[:min(len(ts), n)] {
		body.Items = append(body.Items, tierView(h))
	}
	if len(ts) > n {
		last := ts[n-1].Name
		next, err := page.Encode(tiersList, kernel.Cursor[string]{Key: last, ID: last})
		if err != nil {
			return nil, err //nolint:wrapcheck // encoding a string cannot fail
		}
		body.NextCursor = &next
	}
	return opapi.Tiers200JSONResponse{Body: body}, nil
}

// TierCreate creates a tier.
func (s OperatorServer) TierCreate(
	ctx context.Context,
	req opapi.TierCreateRequestObject,
) (opapi.TierCreateResponseObject, error) {
	h, err := s.svc.CreateTier(ctx, features.Tier{
		Name: req.Body.Name, Features: req.Body.Features, Limits: req.Body.Limits,
	})
	if err != nil {
		return nil, operatorError(err)
	}
	return opapi.TierCreate201JSONResponse{Body: tierView(h)}, nil
}

// Tier reads a tier.
func (s OperatorServer) Tier(
	ctx context.Context,
	req opapi.TierRequestObject,
) (opapi.TierResponseObject, error) {
	h, err := s.svc.Tier(ctx, req.Tier)
	if err != nil {
		return nil, operatorError(err)
	}
	return opapi.Tier200JSONResponse{Body: tierView(h)}, nil
}

// TierUpdate replaces a tier's content.
func (s OperatorServer) TierUpdate(
	ctx context.Context,
	req opapi.TierUpdateRequestObject,
) (opapi.TierUpdateResponseObject, error) {
	h, err := s.svc.ReplaceTier(ctx, features.Tier{
		Name: req.Tier, Features: req.Body.Features, Limits: req.Body.Limits,
	})
	if err != nil {
		return nil, operatorError(err)
	}
	return opapi.TierUpdate200JSONResponse{Body: tierView(h)}, nil
}

// TierDelete deletes a tier and answers it as it was.
func (s OperatorServer) TierDelete(
	ctx context.Context,
	req opapi.TierDeleteRequestObject,
) (opapi.TierDeleteResponseObject, error) {
	h, err := s.svc.DeleteTier(ctx, req.Tier)
	if err != nil {
		return nil, operatorError(err)
	}
	return opapi.TierDelete200JSONResponse{Body: tierView(h)}, nil
}

// Billing reads the billing switch and the default tier.
func (s OperatorServer) Billing(
	ctx context.Context,
	_ opapi.BillingRequestObject,
) (opapi.BillingResponseObject, error) {
	b, err := s.svc.BillingState(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	return opapi.Billing200JSONResponse{Body: billingView(b)}, nil
}

// BillingSet sets the billing switch and, when the body names one, the
// default tier.
func (s OperatorServer) BillingSet(
	ctx context.Context,
	req opapi.BillingSetRequestObject,
) (opapi.BillingSetResponseObject, error) {
	b, err := s.svc.SetBilling(ctx, req.Body.Enabled, req.Body.DefaultTier)
	if err != nil {
		return nil, operatorError(err)
	}
	return opapi.BillingSet200JSONResponse{Body: billingView(b)}, nil
}

// OrganizationEntitlement reads an organization's tier, grants and
// entitlement.
func (s OperatorServer) OrganizationEntitlement(
	ctx context.Context,
	req opapi.OrganizationEntitlementRequestObject,
) (opapi.OrganizationEntitlementResponseObject, error) {
	e, err := s.entitlement(
		ctx,
		req.Organization,
		func(org kernel.OrganizationID) (core.Operated, error) {
			return s.svc.OrganizationEntitlement(ctx, org)
		},
	)
	if err != nil {
		return nil, err
	}
	return opapi.OrganizationEntitlement200JSONResponse{Body: e}, nil
}

// OrganizationTierSet moves an organization to another tier.
func (s OperatorServer) OrganizationTierSet(
	ctx context.Context,
	req opapi.OrganizationTierSetRequestObject,
) (opapi.OrganizationTierSetResponseObject, error) {
	e, err := s.entitlement(
		ctx,
		req.Organization,
		func(org kernel.OrganizationID) (core.Operated, error) {
			return s.svc.SetTier(ctx, org, req.Body.Tier)
		},
	)
	if err != nil {
		return nil, err
	}
	return opapi.OrganizationTierSet200JSONResponse{Body: e}, nil
}

// OrganizationFeatureGrant grants an organization a feature.
func (s OperatorServer) OrganizationFeatureGrant(
	ctx context.Context,
	req opapi.OrganizationFeatureGrantRequestObject,
) (opapi.OrganizationFeatureGrantResponseObject, error) {
	e, err := s.entitlement(
		ctx,
		req.Organization,
		func(org kernel.OrganizationID) (core.Operated, error) {
			return s.svc.GrantFeature(ctx, org, req.Feature)
		},
	)
	if err != nil {
		return nil, err
	}
	return opapi.OrganizationFeatureGrant200JSONResponse{Body: e}, nil
}

// OrganizationFeatureRevoke removes an organization's grant of a feature.
func (s OperatorServer) OrganizationFeatureRevoke(
	ctx context.Context,
	req opapi.OrganizationFeatureRevokeRequestObject,
) (opapi.OrganizationFeatureRevokeResponseObject, error) {
	e, err := s.entitlement(
		ctx,
		req.Organization,
		func(org kernel.OrganizationID) (core.Operated, error) {
			return s.svc.RevokeFeature(ctx, org, req.Feature)
		},
	)
	if err != nil {
		return nil, err
	}
	return opapi.OrganizationFeatureRevoke200JSONResponse{Body: e}, nil
}

// OrganizationLimitGrant sets an organization's value of a limit.
func (s OperatorServer) OrganizationLimitGrant(
	ctx context.Context,
	req opapi.OrganizationLimitGrantRequestObject,
) (opapi.OrganizationLimitGrantResponseObject, error) {
	e, err := s.entitlement(
		ctx,
		req.Organization,
		func(org kernel.OrganizationID) (core.Operated, error) {
			return s.svc.GrantLimit(ctx, org, req.Limit, req.Body.Value)
		},
	)
	if err != nil {
		return nil, err
	}
	return opapi.OrganizationLimitGrant200JSONResponse{Body: e}, nil
}

// OrganizationLimitRevoke removes an organization's grant of a limit.
func (s OperatorServer) OrganizationLimitRevoke(
	ctx context.Context,
	req opapi.OrganizationLimitRevokeRequestObject,
) (opapi.OrganizationLimitRevokeResponseObject, error) {
	e, err := s.entitlement(
		ctx,
		req.Organization,
		func(org kernel.OrganizationID) (core.Operated, error) {
			return s.svc.RevokeLimit(ctx, org, req.Limit)
		},
	)
	if err != nil {
		return nil, err
	}
	return opapi.OrganizationLimitRevoke200JSONResponse{Body: e}, nil
}

// entitlement resolves slug and answers fn's entitlement of it, a slug no
// organization has answering 404.
func (s OperatorServer) entitlement(
	ctx context.Context,
	slug string,
	fn func(kernel.OrganizationID) (core.Operated, error),
) (opapi.Entitlement, error) {
	org, ok, err := s.orgs(ctx, slug)
	switch {
	case err != nil:
		return opapi.Entitlement{}, fmt.Errorf("features: organization %q: %w", slug, err)
	case !ok:
		//nolint:wrapcheck // a problem, answered as it is
		return opapi.Entitlement{}, problem.NotFound.Error(fmt.Sprintf("No organization %q.", slug))
	}
	o, err := fn(org)
	if err != nil {
		return opapi.Entitlement{}, operatorError(err)
	}
	return entitlementView(o), nil
}

// operatorError is err as the problem it answers: 422 for input the
// catalogue refuses or a tier the body names that does not exist, 409 for
// a tier name in use or a write the tier's state refuses, 404 for a tier,
// an organization's entitlement or a grant that does not exist, and err
// itself otherwise, which the store marks when the database is
// unreachable.
func operatorError(err error) error {
	switch {
	case errors.Is(err, core.ErrInvalid), errors.Is(err, core.ErrUnknownTier):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.ValidationFailed.Error(err.Error())
	case errors.Is(err, core.ErrExists):
		return problem.AlreadyExists.Error(err.Error()) //nolint:wrapcheck // as above
	case errors.Is(err, core.ErrInvalidState):
		return problem.InvalidState.Error(err.Error()) //nolint:wrapcheck // as above
	case errors.Is(err, featuresapi.ErrNotFound):
		return problem.NotFound.Error(err.Error()) //nolint:wrapcheck // as above
	}
	return err
}

func billingView(b core.Billing) opapi.Billing {
	return opapi.Billing{Enabled: b.Enabled, DefaultTier: b.DefaultTier}
}

func tierView(h core.Held) opapi.Tier {
	return opapi.Tier{
		Name:          h.Name,
		Features:      nonNil(h.Features),
		Limits:        orEmpty(h.Limits),
		Organizations: h.Organizations,
		Editable:      h.Editable(),
	}
}

func entitlementView(o core.Operated) opapi.Entitlement {
	limits := make([]opapi.LimitGrant, 0, len(o.Grants.Limits))
	for _, name := range slices.Sorted(maps.Keys(o.Grants.Limits)) {
		limits = append(limits, opapi.LimitGrant{Limit: name, Value: o.Grants.Limits[name]})
	}
	return opapi.Entitlement{
		Tier:     o.Tier,
		Version:  o.Version,
		Features: nonNil(o.Features),
		Limits:   orEmpty(o.Limits),
		Grants:   opapi.Grants{Features: nonNil(o.Grants.Features), Limits: limits},
	}
}

// nonNil answers an empty list as [] rather than null, as the schema
// requires the member.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmpty(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}
