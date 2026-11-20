package report_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/evidence"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

type controlledPlanEvaluation func(context.Context, policyapi.EvaluationRequest) (policyapi.Evaluation, error)

func (f controlledPlanEvaluation) Evaluate(ctx context.Context, req policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
	return f(ctx, req)
}

// Controlled aggregates isolate report routing. The acceptance features use
// actual OPA and native worker documents for the production evaluation path.
func TestUnitPlanRoutingRetainsTestingAndInformationalBehavior(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  kernel.RunType
		plan policyapi.PlanDecision
		note string
	}{
		{name: "testing warning still applies a no-change test", typ: kernel.RunTypeTesting, plan: policyapi.PlanDecision{Warn: []string{"test warning"}}, note: "test warning"},
		{name: "information does not hold autodeploy", typ: kernel.RunTypeTracked, plan: policyapi.PlanDecision{Info: []string{"informational"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(tc.typ, kernel.RunStatePlanning)
			w.st.settings.Autodeploy = true
			calls := 0
			evaluator := controlledPlanEvaluation(func(_ context.Context, req policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
				calls++
				if req.Account != w.s.run.Account || req.Type != policyapi.Plan || req.Run == nil || *req.Run != w.s.run.ID || req.BuildInput == nil {
					t.Fatal("planning report lost its owner or lazy input boundary")
				}
				return policyapi.Evaluation{Type: policyapi.Plan, HasPolicies: true, Outcome: policyapi.Allow, Plan: &tc.plan}, nil
			})
			w.svc = report.New(report.Deps{DB: db{}, Store: w.s, Lifecycle: w.m, Stacks: w.st, Policies: evaluator})
			delta := kernel.PlanDelta{Add: 1}
			if tc.typ == kernel.RunTypeTesting {
				delta = kernel.PlanDelta{}
			}
			got, err := w.send(t, workersapi.Report{StateVersion: 2, State: kernel.RunStatePlanning, Event: workersapi.ReportSucceeded, Delta: &delta})
			want(t, got, err, workersapi.ActionExecute, kernel.RunStateApplying, 3)
			if calls != 1 || len(w.m.reqs) != 1 || w.m.reqs[0].Note != tc.note {
				t.Fatal("planning evaluation or informational/warning history was lost")
			}
		})
	}
}

func TestUnitPlanReportRefusesContradictoryEvaluationAndMissingSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  policyapi.Evaluation
		err  error
	}{
		{name: "wrong policy type", out: policyapi.Evaluation{Type: policyapi.Approval, Outcome: policyapi.NoPolicies}, err: evidence.ErrReceipt},
		{name: "missing typed decision", out: policyapi.Evaluation{Type: policyapi.Plan, HasPolicies: true, Outcome: policyapi.Allow}, err: evidence.ErrReceipt},
		{name: "contradictory policy absence", out: policyapi.Evaluation{Type: policyapi.Plan, HasPolicies: true, Outcome: policyapi.NoPolicies}, err: evidence.ErrReceipt},
		{name: "proposed warning cannot guess approval presence", out: policyapi.Evaluation{Type: policyapi.Plan, HasPolicies: true, Outcome: policyapi.Allow, Plan: &policyapi.PlanDecision{Warn: []string{"review"}}}, err: kernel.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(kernel.RunTypeProposed, kernel.RunStatePlanning)
			w.svc = report.New(report.Deps{
				DB: db{}, Store: w.s, Lifecycle: w.m, Stacks: w.st,
				Policies: controlledPlanEvaluation(func(context.Context, policyapi.EvaluationRequest) (policyapi.Evaluation, error) { return tc.out, nil }),
			})
			_, err := w.send(t, workersapi.Report{StateVersion: 2, State: kernel.RunStatePlanning, Event: workersapi.ReportSucceeded, Delta: &kernel.PlanDelta{Add: 1}})
			if !errors.Is(err, tc.err) || len(w.m.reqs) != 0 {
				t.Fatalf("unavailable or invalid evaluation advanced a run: %v", err)
			}
		})
	}
}
