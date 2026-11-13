package http

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apidelta"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/changes"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/logs"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Server is the runs domain's partial of the API's strict server.
type Server struct {
	trigger *trigger.Service
	read    *read.Service
	control *control.Service
	logs    *logs.Service
	changes *changes.Service
}

// New returns the partial over the trigger, read, control, logs and
// changes services.
func New(
	t *trigger.Service,
	r *read.Service,
	c *control.Service,
	l *logs.Service,
	ch *changes.Service,
) Server {
	return Server{trigger: t, read: r, control: c, logs: l, changes: ch}
}

// RunTrigger creates a run on the stack, or answers the one of the same
// stack, type and commit that has not ended.
func (s Server) RunTrigger(
	ctx context.Context,
	req api.RunTriggerRequestObject,
) (api.RunTriggerResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := input(req.Body)
	if err != nil {
		return nil, err
	}
	v, err := s.trigger.Trigger(ctx, p, req.StackId, in)
	if err != nil {
		return nil, runError(err, req.StackId, "")
	}
	return api.RunTrigger200JSONResponse{Body: runOf(v)}, nil
}

// UploadLocalWorkspace starts the upload of a local workspace for a run
// proposed on the stack to preview. The route has decided
// STACK_UPLOAD_LOCAL_WORKSPACE on the stack's space.
func (s Server) UploadLocalWorkspace(
	ctx context.Context,
	req api.UploadLocalWorkspaceRequestObject,
) (api.UploadLocalWorkspaceResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var (
		size int64
		sum  []byte
	)
	if req.Body != nil {
		size = req.Body.Size
		// A digest that is not hex is refused as the wrong length.
		sum, _ = hex.DecodeString(req.Body.Sha256)
	}
	up, err := s.trigger.UploadWorkspace(ctx, p, req.StackId, size, sum)
	if err != nil {
		return nil, runError(err, req.StackId, "")
	}
	headers := api.StringMap{Entries: make([]api.KeyValuePairType, 0, len(up.Header))}
	for _, h := range slices.Sorted(maps.Keys(up.Header)) {
		headers.Entries = append(headers.Entries, api.KeyValuePairType{Key: h, Value: up.Header[h]})
	}
	return api.UploadLocalWorkspace201JSONResponse{Body: api.LocalWorkspaceUpload{
		Id: up.ID.String(), UploadUrl: up.URL, UploadHeaders: headers,
	}}, nil
}

// RunProposeLocalWorkspace creates a proposed run on an uploaded local
// workspace of the stack, or answers the one on it that has not ended.
// The route has decided RUN_PROPOSE_LOCAL_WORKSPACE on the stack's space.
func (s Server) RunProposeLocalWorkspace(
	ctx context.Context,
	req api.RunProposeLocalWorkspaceRequestObject,
) (api.RunProposeLocalWorkspaceResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var upload string
	if req.Body != nil {
		upload = req.Body.Workspace
	}
	v, err := s.trigger.ProposeWorkspace(ctx, p, req.StackId, upload)
	if err != nil {
		return nil, runError(err, req.StackId, "")
	}
	return api.RunProposeLocalWorkspace200JSONResponse{Body: runOf(v)}, nil
}

// principal is the caller the route authenticated.
func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return p, problem.Unauthenticated.Error("The request carries no valid credential.")
	}
	return p, nil
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

// runError answers the refusals of the run operations; slug is the stack
// the request names and run the run, if any. A *kernel.Invalid is
// answered as it is.
func runError(err error, slug, run string) error {
	if l, ok := errors.AsType[*lifecycle.Locked](err); ok {
		return forbidden(accessapi.ActionRunTrigger,
			fmt.Sprintf("The stack is locked by %s; only its holder triggers runs.", l.By))
	}
	switch {
	case errors.Is(err, control.ErrBlocking):
		return forbidden(accessapi.ActionRunCancelBlocking,
			"The run holds the stack's lock; canceling it needs RUN_CANCEL_BLOCKING.")
	case errors.Is(err, control.ErrState):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.InvalidState.Error("The run's state does not allow this; " +
			"a cancel needs a run that has not started, a priority one that has not ended, " +
			"a confirm or discard one that waits for confirmation.")
	case errors.Is(err, control.ErrNoHolder):
		//nolint:wrapcheck // as above
		return problem.NotFound.Error(fmt.Sprintf(
			"No run holds stack %q's lock; name the run to act on.", slug))
	case errors.Is(err, read.ErrNotFound):
		//nolint:wrapcheck // as above
		return problem.NotFound.Error(fmt.Sprintf("No run %q in this account.", run))
	case errors.Is(err, changes.ErrExpired):
		//nolint:wrapcheck // as above
		return problem.InvalidState.Error("The run's changes have expired with its artifacts.")
	case errors.Is(err, lifecycle.ErrDisabled):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.InvalidState.Error("The stack is disabled; enable it to trigger runs.")
	case errors.Is(err, trigger.ErrLocalPreview):
		//nolint:wrapcheck // as above
		return problem.Forbidden.Error(fmt.Sprintf(
			"Stack %q does not allow local previews; set its localPreviewEnabled.", slug))
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

// deref is *p, or T's zero value when p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// forbidden is a 403 that names the action the caller lacks.
func forbidden(a accessapi.Action, detail string) error {
	p := problem.Forbidden.New(detail)
	p.Action = string(a)
	return &problem.Error{Problem: p}
}

// runOf is v as the API answers a run.
func runOf(v read.View) api.Run {
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
	allowed := make([]api.Action, len(v.Actions))
	for i, a := range v.Actions {
		allowed[i] = api.Action(a)
	}
	return api.Run{
		AllowedActions: allowed,
		Branch:         r.Commit.Branch,
		Comments:       []api.RunComment{},
		Commit: api.Commit{
			Hash: r.Commit.Hash, Tag: r.Commit.Tag, Url: r.Commit.URL, Message: r.Commit.Message,
			AuthorName: r.Commit.AuthorName, AuthorLogin: r.Commit.AuthorLogin,
			Timestamp: unix(r.Commit.Timestamp.Unix()),
		},
		Commits:              []string{r.Commit.Hash},
		CreatedAt:            unix(r.CreatedAt.Unix()),
		Delta:                apidelta.Of(r.Delta),
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
