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
	input := updateInput(*req.Body)
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

// PolicyDelete removes an unattached policy, or its attachments when forced.
func (s Server) PolicyDelete(ctx context.Context, req api.PolicyDeleteRequestObject) (api.PolicyDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	force := req.Params.Force != nil && *req.Params.Force
	row, err := s.svc.Delete(ctx, p, req.PolicyId, strict.IfMatch(req.Params.IfMatch), force)
	if err != nil {
		return nil, policyError(err)
	}
	v, err := s.view(ctx, p, row)
	if err != nil {
		return nil, err
	}
	return api.PolicyDelete200JSONResponse{Body: v}, nil
}

// PolicyUpdatev2Patch applies a merge patch to the current input view and fences
// the write with that view's version, retrying a concurrent unconditional edit.
func (s Server) PolicyUpdatev2Patch(ctx context.Context, req api.PolicyUpdatev2PatchRequestObject) (api.PolicyUpdatev2PatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.PolicyUpdateInput, int64, error) {
		row, err := s.svc.Get(ctx, p.AccountID, req.PolicyId)
		if err != nil {
			return api.PolicyUpdateInput{}, 0, err //nolint:wrapcheck // policyError maps the lookup
		}
		names, err := s.spaces.Slugs(ctx, p.AccountID)
		if err != nil {
			return api.PolicyUpdateInput{}, 0, fmt.Errorf("policies: name patch space: %w", err)
		}
		space, engine := names[row.Space], api.PolicyEngineType(row.Engine)
		return api.PolicyUpdateInput{Name: row.Name, Body: row.Body, Description: row.Description, Labels: &row.Labels, EngineType: &engine, Space: &space}, row.Version, nil
	}
	write := func(in api.PolicyUpdateInput, expect kernel.Expect) (core.Policy, error) {
		return s.svc.Update(ctx, p, req.PolicyId, expect, updateInput(in)) //nolint:wrapcheck // policyError maps the write
	}
	row, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, policyError(err)
	}
	v, err := s.view(ctx, p, row)
	if err != nil {
		return nil, err
	}
	tag := strict.ETag(row.Version)
	return api.PolicyUpdatev2Patch200JSONResponse{Body: v, Headers: api.PolicyUpdatev2Patch200ResponseHeaders{ETag: &tag}}, nil
}

func updateInput(in api.PolicyUpdateInput) core.Input {
	out := core.Input{Name: in.Name, Body: in.Body, Description: in.Description}
	if in.Space != nil {
		out.Space = *in.Space
	}
	if in.EngineType != nil {
		out.Engine = rego.EngineType(*in.EngineType)
	}
	if in.Labels != nil {
		out.Labels = *in.Labels
	}
	return out
}

func (s Server) view(ctx context.Context, p kernel.Principal, row core.Policy) (api.Policy, error) {
	names, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.Policy{}, fmt.Errorf("policies: name spaces: %w", err)
	}
	return policyView(row, names), nil
}

func policyView(row core.Policy, names map[kernel.ULID]string) api.Policy {
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
	}
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
