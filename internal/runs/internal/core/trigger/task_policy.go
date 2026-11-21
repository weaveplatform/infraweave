package trigger

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// TaskDenied is a policy refusal whose evaluation evidence commits without a
// task run. It is distinct from route-level TASK_CREATE authorization denial.
type TaskDenied struct{ Message string }

func (e *TaskDenied) Error() string { return e.Message }

func (s *Service) admitTask(ctx context.Context, p kernel.Principal, stack kernel.ULID, command string, at time.Time) (bool, error) {
	result, err := s.d.Policies.Evaluate(ctx, policyapi.EvaluationRequest{
		Account: p.AccountID, Type: policyapi.Task, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: stack},
		BuildInput: func(ctx context.Context) (json.RawMessage, error) {
			if s.d.Snapshots == nil || s.d.Sessions == nil || s.d.TaskInputs == nil {
				return nil, policyapi.ErrInput
			}
			snapshot, err := s.d.Snapshots.PolicySnapshot(ctx, p.AccountID, stack)
			if err != nil {
				return nil, err //nolint:wrapcheck // the owner names the read
			}
			// Metadata needs only account/stack identity before the run
			// exists; no run ID or persisted run fact is invented here.
			facts, err := s.d.Metadata.Stack(ctx, lifecycle.Run{Account: p.AccountID, Stack: stack}, snapshot)
			if err != nil {
				return nil, err //nolint:wrapcheck // the owner names the read
			}
			metadata, err := s.d.Sessions.ReviewMetadata(ctx, p, snapshot.Settings.Space)
			if err != nil {
				return nil, err //nolint:wrapcheck // the owner names the read
			}
			input := policyapi.TaskInputFacts{Account: p.AccountID, At: at, Command: command, Stack: facts, Session: taskSession(metadata)}
			if ip := accessapi.ClientAddr(ctx); ip != "" {
				input.RemoteIP = policyapi.Fact[string]{Known: true, Value: &ip}
			}
			return s.d.TaskInputs.BuildTaskInput(ctx, input) //nolint:wrapcheck // the shared builder names the projection
		},
	})
	if err != nil {
		return false, err //nolint:wrapcheck // the evaluator names the operational failure
	}
	if !result.HasPolicies || result.Outcome == policyapi.Allow {
		return result.HasPolicies, nil
	}
	var messages []string
	if result.Negative != nil {
		messages = append(messages, result.Negative.Deny...)
	}
	for _, failure := range result.Failures {
		messages = append(messages, failure.Message)
	}
	message := strings.Join(messages, "; ")
	if message == "" {
		message = "Task creation was denied by TASK policies."
	}
	return true, &TaskDenied{Message: message}
}

func taskKnown[T any](value T) policyapi.Fact[T] {
	return policyapi.Fact[T]{Known: true, Value: &value}
}

func taskSession(metadata accessapi.ReviewMetadata) policyapi.SessionFacts {
	p := metadata.Session
	admin := policyapi.SessionNotAdmin
	if p.Admin {
		admin = policyapi.SessionIsAdmin
	}
	if !p.Human() {
		admin = policyapi.SessionMachineAdmin
	}
	out := policyapi.SessionFacts{
		Machine: !p.Human(), Admin: taskKnown(admin), Login: taskKnown(p.Login), Name: taskKnown(p.Name),
		Teams: taskKnown(append([]string{}, p.Teams...)),
	}
	if metadata.CreatorIP != nil {
		out.CreatorIP = taskKnown(*metadata.CreatorIP)
	}
	if metadata.IDPSubject != nil {
		out.IDPSubject = taskKnown(*metadata.IDPSubject)
	}
	if metadata.Identity != nil {
		federations := map[string]policyapi.FederationFacts{}
		for _, federation := range metadata.Identity.Federations {
			federations[federation.Provider.String()] = policyapi.FederationFacts{ID: taskKnown(federation.Subject)}
		}
		out.Identity = taskKnown(policyapi.IdentityFacts{ULID: taskKnown(metadata.Identity.Member.String()), Federations: taskKnown(federations)})
	}
	return out
}
