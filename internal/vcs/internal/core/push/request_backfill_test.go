package push

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type buildingPushPolicies struct{}

func (buildingPushPolicies) Evaluate(ctx context.Context, request policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
	_, err := request.BuildInput(ctx)
	return policyapi.Evaluation{}, err
}

type failedRequestSource struct{ cause error }

func (s failedRequestSource) PrepareRequest(context.Context, kernel.AccountID, kernel.ULID, string, vcsapi.Event) (port.RequestFacts, error) {
	return port.RequestFacts{}, s.cause
}

func TestUnitLateRequestNativeFailureKeepsPrivateDetailsOutOfQueueErrors(t *testing.T) {
	w := newEvaluation(t)
	w.s.d.Policies = buildingPushPolicies{}
	w.s.d.Requests = failedRequestSource{cause: fmt.Errorf("private-source-marker: %w", context.Canceled)}
	event := w.push("main", "", "main.tf")
	event.Event.Type = "comment"
	event.Event.Comment = &vcsapi.Comment{PullRequest: 12, Body: "plan now"}
	w.events = []Prepared{event}
	err := w.s.Evaluated(t.Context(), w.job)
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "private-source-marker") || w.complete != 0 || len(w.requests) != 0 {
		t.Fatal("native details escaped queue error classification", err, w.complete, w.requests)
	}
}

type limitedRequestSource struct {
	t     *testing.T
	reset time.Time
	reads int
}

func (s *limitedRequestSource) PrepareRequest(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, _ vcsapi.Event) (port.RequestFacts, error) {
	s.reads++
	if tx.In(ctx) {
		s.t.Fatal("late native request read happened inside the completion transaction")
	}
	return port.RequestFacts{}, &port.RateLimited{Reset: s.reset}
}

func TestUnitLateRequestRateLimitDoesNotConsumeAttempt(t *testing.T) {
	w := newEvaluation(t)
	now := w.input.Deadline.Add(-10 * time.Second)
	w.s.d.Now = func() time.Time { return now }
	w.job.Attempt, w.job.MaxAttempts = 3, 3
	native := &limitedRequestSource{t: t, reset: now.Add(5 * time.Second)}
	w.s.d.Policies = buildingPushPolicies{}
	w.s.d.Requests = native
	event := w.push("main", "", "main.tf")
	event.Event.Type = "comment"
	event.Event.Comment = &vcsapi.Comment{PullRequest: 12, Body: "plan now"}
	w.events = []Prepared{event}
	err := w.s.Evaluated(t.Context(), w.job)
	if err == nil || err.Error() != "snoozed for 5s" || native.reads != 1 || w.complete != 0 || w.failureMarkers != 0 || len(w.requests) != 0 {
		t.Fatal("rate-limited late request lost bounded snooze or published effects", err, native.reads, w.complete, w.failureMarkers, w.requests)
	}
}

func TestUnitNoRequestPoliciesDoNotReadNativeFacts(t *testing.T) {
	w := newEvaluation(t)
	native := &limitedRequestSource{t: t, reset: w.s.d.Now().Add(time.Minute)}
	w.s.d.Requests = native
	event := w.push("main", "", "main.tf")
	event.Event.Type = "comment"
	event.Event.Comment = &vcsapi.Comment{PullRequest: 12, Body: "ordinary note"}
	w.events = []Prepared{event}
	if err := w.s.Evaluated(t.Context(), w.job); err != nil || native.reads != 0 || w.complete != 1 || len(w.requests) != 0 {
		t.Fatal("policy-free request acquired native dependencies or created a run", err, native.reads, w.complete, w.requests)
	}
}

func TestUnitSelectedLateRequestRefusesMissingSourceCapability(t *testing.T) {
	w := newEvaluation(t)
	w.s.d.Policies = buildingPushPolicies{}
	event := w.push("main", "", "main.tf")
	event.Event.Type = "comment"
	event.Event.Comment = &vcsapi.Comment{PullRequest: 12, Body: "plan now"}
	w.events = []Prepared{event}
	err := w.s.Evaluated(t.Context(), w.job)
	if !errors.Is(err, kernel.ErrUnavailable) || w.complete != 0 || len(w.requests) != 0 {
		t.Fatal("missing native preparation became default authorization", err, w.complete, w.requests)
	}
}
