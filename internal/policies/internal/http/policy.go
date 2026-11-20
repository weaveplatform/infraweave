package http

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		return kernel.Principal{}, problem.Unauthenticated.Error("The request carries no valid credential.") //nolint:wrapcheck // already a public HTTP problem
	}
	return p, nil
}

// PolicyCreatev2 compiles and persists a policy in the route-authorized space.
func (s Server) PolicyCreatev2(ctx context.Context, req api.PolicyCreatev2RequestObject) (api.PolicyCreatev2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := req.Body
	input := core.Input{Name: in.Name, Body: in.Body, Description: in.Description, Type: rego.Type(in.Type), Template: in.PolicyTemplateULID}
	if in.Space != nil {
		input.Space = *in.Space
	}
	if in.EngineType != nil {
		input.Engine = rego.EngineType(*in.EngineType)
	}
	if in.Labels != nil {
		input.Labels = *in.Labels
	}
	row, err := s.svc.Create(ctx, p, input)
	if err != nil {
		return nil, policyError(err)
	}
	v, err := s.view(ctx, p, row)
	if err != nil {
		return nil, err
	}
	return api.PolicyCreatev2201JSONResponse{Body: v}, nil
}

// Policy reads one immutable policy ID through its account-qualified subject.
func (s Server) Policy(ctx context.Context, req api.PolicyRequestObject) (api.PolicyResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.svc.Get(ctx, p.AccountID, req.PolicyId)
	if err != nil {
		return nil, policyError(err)
	}
	v, err := s.view(ctx, p, row)
	if err != nil {
		return nil, err
	}
	tag := strict.ETag(row.Version)
	return api.Policy200JSONResponse{Body: v, Headers: api.Policy200ResponseHeaders{ETag: &tag}}, nil
}

// PolicyUpdatev2 compiles a replacement under its explicit or preserved engine.
func (s Server) PolicyUpdatev2(ctx context.Context, req api.PolicyUpdatev2RequestObject) (api.PolicyUpdatev2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := req.Body
	input := core.Input{Name: in.Name, Body: in.Body, Description: in.Description}
	if in.Space != nil {
		input.Space = *in.Space
	}
	if in.EngineType != nil {
		input.Engine = rego.EngineType(*in.EngineType)
	}
	if in.Labels != nil {
		input.Labels = *in.Labels
	}
	row, err := s.svc.Update(ctx, p, req.PolicyId, strict.IfMatch(req.Params.IfMatch), input)
	if err != nil {
		return nil, policyError(err)
	}
	view, err := s.view(ctx, p, row)
	if err != nil {
		return nil, err
	}
	tag := strict.ETag(row.Version)
	return api.PolicyUpdatev2200JSONResponse{Body: view, Headers: api.PolicyUpdatev2200ResponseHeaders{ETag: &tag}}, nil
}

func (s Server) view(ctx context.Context, p kernel.Principal, row core.Policy) (api.Policy, error) {
	names, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.Policy{}, fmt.Errorf("policies: name spaces: %w", err)
	}
	return api.Policy{
		Id:                     row.ID.String(),
		Name:                   row.Name,
		Body:                   row.Body,
		Description:            row.Description,
		Type:                   api.PolicyType(row.Type),
		EngineType:             api.PolicyEngineType(row.Engine),
		Labels:                 row.Labels,
		Kind:                   api.PolicyKindPolicy,
		Space:                  names[row.Space],
		SpaceDetails:           api.SpaceRef{Id: names[row.Space]},
		CreatedAt:              unix(row.CreatedAt.Unix()),
		UpdatedAt:              unix(row.UpdatedAt.Unix()),
		AttachedStacks:         []api.PolicyStackAttachment{},
		AttachedIntentProjects: []api.PolicyIntentProjectAttachment{},
		EvaluationRecords:      []api.PolicyEvaluationRecord{},
	}, nil
}

// unix follows the reference timestamp schema, which represents pre-2038 seconds.
func unix(value int64) int32 { return int32(value) } //nolint:gosec // the served schema is int32 Unix seconds

func policyError(err error) error {
	var denied *core.Denied
	if errors.As(err, &denied) {
		p := problem.Forbidden.New(fmt.Sprintf("You may not perform %s here.", denied.Action))
		p.Action = string(denied.Action)
		return &problem.Error{Problem: p}
	}
	var missing *core.Missing
	if errors.As(err, &missing) {
		return problem.NotFound.Error(fmt.Sprintf("No %s %q in this account.", missing.Kind, missing.ID)) //nolint:wrapcheck // already a public HTTP problem
	}
	return err
}
