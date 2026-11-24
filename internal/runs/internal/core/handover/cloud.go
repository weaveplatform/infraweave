package handover

import (
	"context"
	"maps"
	"slices"
	"strings"

	cloudapi "github.com/weaveplatform/infraweave/internal/cloud/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

// BindCredentials binds the cloud domain's credentials, which internal/app
// constructs after the runs domain. Until they are bound, no phase
// receives cloud credentials.
func (s *Service) BindCredentials(c cloudapi.Credentials) { s.credentials = c }

// Scope is the side of its stack's cloud attachments a run of type t reads
// its credentials from in state s: a proposed run plans with the read
// side; a task, a destroy run and a module test run with the write side
// throughout; and a tracked run plans with the read side and applies with
// the write side, unless its stack deploys automatically, whose plan
// applies in the same phase and so reads the write side from the start.
//
// ref: decision 0006 "AWS external ID and session name"
// ref: report 12 § "6.4 Scope"
func Scope(t kernel.RunType, s kernel.RunState, autodeploy bool) cloudapi.Scope {
	switch t {
	case kernel.RunTypeTask, kernel.RunTypeDestroy, kernel.RunTypeTesting:
		return cloudapi.ScopeWrite
	case kernel.RunTypeTracked:
		if autodeploy || s == kernel.RunStatePreparingApply || s == kernel.RunStateApplying {
			return cloudapi.ScopeWrite
		}
	}
	return cloudapi.ScopeRead
}

// cloud adds the cloud credentials of r's phase on a stack that deploys
// automatically when autodeploy is set: each computed variable that no
// variable of spec's environment names, the secret ones by name in spec
// and by value in sec, and the roles r's worker assumes. A stack, context
// or run variable of the same name replaces a computed one, as it does
// infraweave's own variables. The credentials are fetched once for the
// handover and never refreshed.
//
// ref: decision 0008 § "The run specification and its secrets"
// ref: decision 0013 § "Delivery to runs"
func (s *Service) cloud(ctx context.Context, r lifecycle.Run, autodeploy bool, spec *workersapi.Spec, sec *secrets) error {
	if s.credentials == nil {
		return nil
	}
	phase, err := s.credentials.ForPhase(ctx, cloudapi.PhaseRequest{
		Account: r.Account, Stack: r.Stack, Run: r.ID, Scope: Scope(r.Type, r.State, autodeploy),
	})
	if err != nil {
		return err //nolint:wrapcheck // the cloud domain names the integration
	}
	set := map[string]bool{}
	for _, v := range spec.Environment {
		set[v.Name] = true
	}
	for _, v := range phase.Environment {
		switch {
		case set[v.Name]:
			continue
		case v.Secret:
			sec.Environment[v.Name] = v.Value
			spec.Environment = append(spec.Environment, workersapi.Variable{Name: v.Name, WriteOnly: true})
		default:
			spec.Environment = append(spec.Environment, workersapi.Variable{Name: v.Name, Value: &v.Value})
		}
	}
	slices.SortFunc(spec.Environment, func(a, b workersapi.Variable) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, a := range phase.WorkerAssumptions {
		spec.CloudAssumptions = append(spec.CloudAssumptions, workersapi.CloudAssumption{
			Integration: a.Integration, IntegrationName: a.IntegrationName, RoleARN: a.RoleARN,
			ExternalID: a.ExternalID, SessionName: a.SessionName, DurationSeconds: a.DurationSeconds,
			Region: a.Region, Tags: maps.Clone(a.Tags),
		})
	}
	return nil
}
