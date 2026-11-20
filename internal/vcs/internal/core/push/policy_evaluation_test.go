package push

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type policyResult struct {
	evaluation policyapi.Evaluation
	err        error
	calls      int
	request    policyapi.EvaluationRequest
}

func (p *policyResult) Evaluate(_ context.Context, request policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
	p.calls++
	p.request = request
	return p.evaluation, p.err
}

type policySnapshotResult struct {
	stacksapi.PolicySnapshots
	labels []string
	err    error
}

func (p policySnapshotResult) PolicySnapshot(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.PolicySnapshot, error) {
	return stacksapi.PolicySnapshot{Labels: p.labels}, p.err
}

type policySideEffects struct {
	locks, cancels, reports int
	err                     error
	report                  port.PushFeedback
	ids                     []kernel.ULID
}

func (p *policySideEffects) UpdatePushLock(context.Context, kernel.AccountID, kernel.ULID, string, string) error {
	p.locks++
	return p.err
}

func (p *policySideEffects) CancelForPush(_ context.Context, _ kernel.AccountID, _, _ kernel.ULID, ids []kernel.ULID) (int, error) {
	p.cancels++
	p.ids = slices.Clone(ids)
	return len(ids), p.err
}

func (p *policySideEffects) ReportPush(_ context.Context, report port.PushFeedback) error {
	p.reports++
	p.report = report
	return p.err
}

func TestUnitPushPolicyDecisionsAndFailureBoundaries(t *testing.T) {
	cause := errors.New("operational failure")
	for _, tc := range []struct {
		name     string
		policy   policyapi.PushDecision
		fork     bool
		failures bool
		want     string
	}{
		{"track", policyapi.PushDecision{Track: true}, false, false, Track},
		{"propose", policyapi.PushDecision{Propose: true}, false, false, Propose},
		{"explicit ignore", policyapi.PushDecision{Ignore: true}, false, false, CustomPolicy},
		{"no match", policyapi.PushDecision{}, false, false, CustomPolicyNoTrigger},
		{"error", policyapi.PushDecision{Track: true}, false, true, CustomPolicy},
		{"fork default", policyapi.PushDecision{Propose: true}, true, false, CustomPolicyNoTrigger},
		{"fork permitted", policyapi.PushDecision{Propose: true, AllowFork: true}, true, false, Propose},
		{"no trigger", policyapi.PushDecision{Track: true, NoTrigger: true}, false, false, CustomPolicyNoTrigger},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newEvaluation(t)
			p := &policyResult{evaluation: policyapi.Evaluation{Type: policyapi.Push, HasPolicies: true, Push: &tc.policy}}
			if tc.failures {
				p.evaluation.Failures = []policyapi.Failure{{Code: "LIMIT", Message: "limited"}}
			}
			w.s.d.Policies = p
			event := w.push("main", "commit", "root/main.tf")
			if tc.fork {
				event.Event.PullRequest = &vcsapi.PullRequest{Fork: true}
			}
			got, _, err := w.s.policyDecision(t.Context(), w.input, w.stack, event, w.meta.Integration)
			if err != nil || got.Outcome != tc.want && got.Reason != tc.want {
				t.Fatalf("decision=%+v error=%v", got, err)
			}
			if p.calls != 1 || p.request.Account != w.input.Account || p.request.Type != policyapi.Push || p.request.Target.ID != w.input.Stack ||
				p.request.Target.Kind != policyapi.StackTarget ||
				p.request.Run != nil ||
				p.request.Input != nil ||
				p.request.BuildInput == nil {
				t.Fatal("evaluation escaped its account/target/lazy input contract")
			}
		})
	}
	for _, boundary := range []string{"gate missing", "evaluator missing", "gate down", "evaluation down", "stack missing", "stack down", "missing aggregate"} {
		t.Run(boundary, func(t *testing.T) {
			w := newEvaluation(t)
			want := cause
			switch boundary {
			case "gate missing":
				w.s.d.PolicyAllowed = nil
				want = kernel.ErrUnavailable
			case "evaluator missing":
				w.s.d.Policies = nil
				want = kernel.ErrUnavailable
			case "gate down":
				w.s.d.PolicyAllowed = func(context.Context, kernel.AccountID) (bool, error) { return false, cause }
			case "evaluation down":
				w.s.d.Policies = &policyResult{err: cause}
			case "stack missing":
				w.s.d.PolicyStacks = nil
				want = kernel.ErrUnavailable
			case "stack down":
				w.s.d.PolicyStacks = policySnapshotResult{err: cause}
			case "missing aggregate":
				w.s.d.Policies = &policyResult{evaluation: policyapi.Evaluation{HasPolicies: true}}
				want = kernel.ErrUnavailable
			}
			_, _, err := w.s.policyDecision(t.Context(), w.input, w.stack, w.push("main", "head", "main.tf"), w.meta.Integration)
			if !errors.Is(err, want) {
				t.Fatalf("boundary failure=%v want=%v", err, want)
			}
		})
	}
	w := newEvaluation(t)
	p := &policyResult{}
	w.s.d.Policies = p
	w.s.d.PolicyAllowed = func(context.Context, kernel.AccountID) (bool, error) { return false, nil }
	w.s.d.PolicyStacks = policySnapshotResult{labels: []string{disableDefaultPush}}
	got, _, err := w.s.policyDecision(t.Context(), w.input, w.stack, w.push("main", "head", "main.tf"), w.meta.Integration)
	if err != nil || got.Reason != DisabledByLabel || p.calls != 0 {
		t.Fatal("tier-gated policy ran or bypassed the default-disable label", got, err)
	}
}

func TestUnitPushCreationEffectsOnlyForNewRuns(t *testing.T) {
	for _, mode := range []string{"new", "duplicate", "refused", "capped", "lock down", "cancel down", "missing lock owner", "missing cancel owner"} {
		t.Run(mode, func(t *testing.T) {
			w := newEvaluation(t)
			effects := &policySideEffects{}
			target := kernel.NewULID()
			p := policyapi.PushDecision{Track: true, Priority: "high", Lock: "deploy", Cancel: []string{target.String(), "bad-id"}}
			w.s.d.Policies = &policyResult{evaluation: policyapi.Evaluation{Type: policyapi.Push, HasPolicies: true, Push: &p}}
			w.s.d.PolicyLocks = effects
			w.s.d.PolicyCancellations = effects
			pool := kernel.NewULID()
			w.stack.WorkerPool = &pool
			w.events = []Prepared{w.push("main", "head", "main.tf")}
			cause := errors.New("effect storage failure")
			wantErr := error(nil)
			switch mode {
			case "duplicate":
				w.created["head"] = true
			case "refused":
				w.createErr = runsapi.ErrCreationRefused
			case "capped":
				w.cap = 0
			case "lock down":
				effects.err = cause
				wantErr = cause
			case "cancel down":
				p.Lock = ""
				effects.err = cause
				wantErr = cause
			case "missing lock owner":
				w.s.d.PolicyLocks = nil
				wantErr = kernel.ErrUnavailable
			case "missing cancel owner":
				p.Lock = ""
				w.s.d.PolicyCancellations = nil
				wantErr = kernel.ErrUnavailable
			}
			err := w.s.Evaluated(t.Context(), w.job)
			if !errors.Is(err, wantErr) || wantErr != nil && w.complete != 0 {
				t.Fatalf("effect failure/settlement=%v complete=%d", err, w.complete)
			}
			if mode == "duplicate" || mode == "refused" || mode == "capped" {
				if effects.locks != 0 || effects.cancels != 0 {
					t.Fatal("non-created replacement changed policy lock/cancellations")
				}
			}
			if mode == "new" &&
				(effects.locks != 1 || effects.cancels != 1 || len(effects.ids) != 1 || effects.ids[0] != target || w.requests[0].PolicyLockCaller != "policy:deploy" || w.requests[0].Priority != kernel.RunPriorityHigh) {
				t.Fatal("new run lost exact owner, priority or cancellation")
			}
		})
	}
}
