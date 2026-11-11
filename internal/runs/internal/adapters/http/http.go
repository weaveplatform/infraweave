package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Server is the runs domain's partial of the API's strict server.
type Server struct {
	trigger *trigger.Service
}

// New returns the partial over the trigger service.
func New(t *trigger.Service) Server { return Server{trigger: t} }

// RunTrigger creates a run on the stack, or answers the one of the same
// stack, type and commit that has not ended.
func (s Server) RunTrigger(
	ctx context.Context,
	req api.RunTriggerRequestObject,
) (api.RunTriggerResponseObject, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.Unauthenticated.Error("The request carries no valid credential.")
	}
	in, err := input(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := s.trigger.Trigger(ctx, p, req.StackId, in)
	if err != nil {
		return nil, runError(err, req.StackId)
	}
	return api.RunTrigger200JSONResponse{Body: runOf(v)}, nil
}

// input is a trigger's body as the trigger service takes it. A runtime
// configuration that sets its yaml beside its other properties is a
// *kernel.Invalid.
func input(body *api.RunTriggerRequest) (trigger.Input, error) {
	var in trigger.Input
	if body == nil {
		return in, nil
	}
	if body.RunType != nil {
		in.Type = kernel.RunType(*body.RunType)
	}
	if body.CommitSha != nil {
		in.CommitSHA = *body.CommitSha
	}
	if body.ForceApply != nil {
		in.ForceApply = string(*body.ForceApply)
	}
	if rc := body.RuntimeConfig; rc != nil {
		if rc.Yaml != nil && *rc != (api.RuntimeConfigInput{Yaml: rc.Yaml}) {
			return in, &kernel.Invalid{
				Detail: "A runtime configuration sets its yaml or its other properties, not both.",
				Errors: []kernel.FieldError{{
					Pointer: "/runtimeConfig/yaml", Reason: kernel.ReasonRule,
					Message: "yaml is set beside other properties.",
				}},
			}
		}
		raw, err := json.Marshal(rc)
		if err != nil {
			return in, fmt.Errorf("runs: encode the runtime configuration: %w", err)
		}
		in.RuntimeConfig = raw
	}
	return in, nil
}

// runError answers the trigger's refusals; slug is the stack the request
// names. A *kernel.Invalid is answered as it is.
func runError(err error, slug string) error {
	if l, ok := errors.AsType[*lifecycle.Locked](err); ok {
		p := problem.Forbidden.New(
			fmt.Sprintf("The stack is locked by %s; only its holder triggers runs.", l.By))
		p.Action = "RUN_TRIGGER"
		return &problem.Error{Problem: p}
	}
	switch {
	case errors.Is(err, lifecycle.ErrDisabled):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.InvalidState.Error("The stack is disabled; enable it to trigger runs.")
	case errors.Is(err, trigger.ErrNoCommit):
		//nolint:wrapcheck // as above
		return problem.InvalidState.Error(
			"The stack tracks no commit yet; name one with commitSha or sync the stack.")
	case errors.Is(err, stacksapi.ErrNotFound):
		//nolint:wrapcheck // as above
		return problem.NotFound.Error(fmt.Sprintf("No stack %q in this account.", slug))
	}
	return err
}

// runOf is v as the API answers a run.
func runOf(v trigger.View) api.Run {
	r := v.Run
	history := make([]api.RunStateTransition, len(v.History))
	for i, st := range v.History {
		version := int32(st.Sequence) //nolint:gosec // a run's transitions are few
		h := api.RunStateTransition{
			HasLogs: st.HasLogs, Note: st.Note, State: api.RunState(st.State),
			StateVersion: &version, Terminal: st.State.Terminal(), Timestamp: unix(st.At.Unix()),
			Username: st.Username,
		}
		if st.Until != nil {
			until := unix(st.Until.Unix())
			h.Until = &until
		}
		history[i] = h
	}
	var force *api.ForceApplyMode
	if r.ForceApply != "" {
		m := api.ForceApplyMode(r.ForceApply)
		force = &m
	}
	var triggeredBy *string
	if r.TriggeredBy != "" {
		triggeredBy = &r.TriggeredBy
	}
	return api.Run{
		Branch:   v.Branch,
		Comments: []api.RunComment{},
		Commit: api.Commit{
			Hash: r.Commit.Hash, Tag: r.Commit.Tag, Url: r.Commit.URL, Message: r.Commit.Message,
			AuthorName: r.Commit.AuthorName, AuthorLogin: r.Commit.AuthorLogin,
			Timestamp: unix(r.Commit.Timestamp.Unix()),
		},
		Commits:              []string{r.Commit.Hash},
		CreatedAt:            unix(r.CreatedAt.Unix()),
		DependsOn:            []api.RunDependency{},
		DriftDetection:       r.DriftDetection,
		ExternalDependencies: []api.RunExternalDependency{},
		Finished:             r.State.Terminal(),
		ForceApplyMode:       force,
		History:              history,
		Id:                   r.ID.String(),
		IsDependedOnBy:       []api.RunDependency{},
		PlanPoliciesOutcomes: []api.PlanPoliciesOutcome{},
		PluginLogs:           []api.PluginLogsResponse{},
		PolicyReceipts:       []api.RunPolicyReceipt{},
		PriorityPreset:       api.RunPriorityPreset(r.Priority),
		Reviews:              []api.RunReview{},
		StackId:              v.Stack.Slug,
		State:                api.RunState(r.State),
		Title:                r.Commit.Message,
		TriggeredBy:          triggeredBy,
		TriggeredRuns:        []api.RunDependency{},
		Type:                 api.RunType(r.Type),
		UpdatedAt:            unix(r.UpdatedAt.Unix()),
		WorkflowId:           r.ID.String(),
	}
}

// unix is a Unix time in seconds as the API's int32 fields carry it,
// which hold seconds until 2038.
func unix(s int64) int32 { return int32(s) } //nolint:gosec // as above
