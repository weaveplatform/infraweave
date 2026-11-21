package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

var _ accessapi.LoginEvaluator = (*Service)(nil)

// EvaluateLogin retains the policy owner's selection, ordering, limits and
// evidence transaction. Access owns admission and account-qualified grants.
func (s *Service) EvaluateLogin(ctx context.Context, request accessapi.LoginRequest) (accessapi.LoginEvaluation, error) {
	evaluation, err := s.Evaluate(ctx, policyapi.EvaluationRequest{
		Account: request.Account, Type: policyapi.Login,
		Target: policyapi.Target{Kind: policyapi.SpaceTarget, ID: request.Account.ULID},
		BuildInput: func(ctx context.Context) (json.RawMessage, error) {
			return buildLoginInput(ctx, request)
		},
	})
	if err != nil {
		return accessapi.LoginEvaluation{}, err //nolint:wrapcheck // the evaluator names the operation
	}
	out := accessapi.LoginEvaluation{HasPolicies: evaluation.HasPolicies}
	if decision := evaluation.Login; decision != nil {
		out.Allow, out.Admin, out.Message = decision.Allow, decision.Admin, decision.Message
		out.Teams = slices.Clone(decision.Teams)
		out.Roles = make(map[string][]string, len(decision.Roles))
		for space, roles := range decision.Roles {
			out.Roles[space] = slices.Clone(roles)
		}
		out.SpaceAdmin = slices.Clone(decision.SpaceAdmin)
		out.SpaceWrite = slices.Clone(decision.SpaceWrite)
		out.SpaceRead = slices.Clone(decision.SpaceRead)
	}
	return out, nil
}

func buildLoginInput(ctx context.Context, request accessapi.LoginRequest) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("build login input: %w", err)
	}
	subject := request.Session
	if request.Account.IsZero() || request.At.IsZero() || subject.Member == nil || !subject.TeamsKnown {
		return nil, policyapi.ErrInput
	}
	session := inputs.LoginSession{
		Machine: inputs.Value(subject.Machine), Member: inputs.Value(*subject.Member),
		Login: inputs.Value(subject.Login), Name: inputs.Value(subject.Name),
		CreatorIP: loginOptional(subject.CreatorIP), IDPSubject: loginOptional(subject.IDPSubject),
		Teams: inputs.Null[[]string](),
	}
	if subject.Teams != nil {
		session.Teams = inputs.Value(slices.Clone(subject.Teams))
	}
	if subject.Machine || subject.Admin != nil {
		raw := []byte(`""`)
		if !subject.Machine {
			raw = []byte("false")
			if *subject.Admin {
				raw = []byte("true")
			}
		}
		var admin inputs.Admin
		if err := json.Unmarshal(raw, &admin); err != nil {
			return nil, policyapi.ErrInput
		}
		session.Admin = inputs.Value(admin)
	}
	if identity := subject.Identity; identity != nil {
		if identity.Member.IsZero() {
			return nil, policyapi.ErrInput
		}
		federations := make(map[string]inputs.Federation, len(identity.Federations))
		for _, federation := range identity.Federations {
			if federation.Provider.IsZero() {
				return nil, policyapi.ErrInput
			}
			federations[federation.Provider.String()] = inputs.Federation{ID: inputs.Value(federation.Subject)}
		}
		session.Identity = inputs.Value(inputs.Identity{
			ULID: inputs.Value(identity.Member.String()), Federations: inputs.Value(federations),
		})
	}
	spaces := make([]inputs.Space, len(request.Spaces))
	for i, space := range request.Spaces {
		if space.ID.IsZero() || space.Slug == "" {
			return nil, policyapi.ErrInput
		}
		spaces[i] = inputs.Space{ID: inputs.Value(space.Slug), Name: inputs.Value(space.Name), Labels: inputs.Value(slices.Clone(space.Labels))}
	}
	roles := make([]inputs.Role, len(request.Roles))
	for i, role := range request.Roles {
		if role.ID.IsZero() || role.Slug == "" {
			return nil, policyapi.ErrInput
		}
		roles[i] = inputs.Role{ID: inputs.Value(role.Slug), Slug: inputs.Value(role.Slug), Name: inputs.Value(role.Name), ULID: inputs.Value(role.ID.String())}
	}
	raw, err := json.Marshal(inputs.LoginInput{
		Request: inputs.Value(inputs.Request{TimestampNS: inputs.Value(request.At.UnixNano()), RemoteIP: loginOptional(request.RemoteIP)}),
		Session: inputs.Value(session), Spaces: inputs.Value(spaces), Roles: inputs.Value(roles),
	})
	if err != nil {
		return nil, fmt.Errorf("encode login input: %w", policyapi.ErrInput)
	}
	if err := inputs.Validate(string(policyapi.Login), raw); err != nil {
		return nil, policyapi.ErrInput
	}
	return raw, nil
}

func loginOptional(value *string) inputs.Field[string] {
	if value == nil {
		return inputs.Field[string]{}
	}
	return inputs.Value(*value)
}
