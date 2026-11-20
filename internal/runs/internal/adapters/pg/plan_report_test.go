package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

type planReportEvaluation func(context.Context, policyapi.EvaluationRequest) (policyapi.Evaluation, error)

func (f planReportEvaluation) Evaluate(ctx context.Context, req policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
	return f(ctx, req)
}

type afterPlanTransition struct {
	report.Mover
	fail bool
}

func (m *afterPlanTransition) Transition(ctx context.Context, req lifecycle.Request) (lifecycle.Run, error) {
	run, err := m.Mover.Transition(ctx, req)
	if err == nil && m.fail {
		return run, errInjected
	}
	return run, err
}

// Controlled evaluation and post-transition faults exercise the real owner
// transaction, independently of native worker/OPA behavior in plan.feature.
func TestAccPlanReportFailuresRollbackRunEvidenceAndNotifications(t *testing.T) {
	for _, fault := range []string{"evaluation", "after transition"} {
		t.Run(fault, func(t *testing.T) {
			w := newWorld(t)
			settings := w.stacks.settings[w.stack]
			workerPool := kernel.NewULID()
			settings.WorkerPool = &workerPool
			w.stacks.settings[w.stack] = settings
			run := mustCreate(t, w, kernel.RunTypeTracked, "plan-rollback")
			w.takeLock(t, run)
			run = w.mustMove(t, run, kernel.RunStateReady, machine.DriverScheduler, machine.GuardStackLockFree, machine.GuardApproved,
				machine.GuardUpstreamFinished, machine.GuardExternalSettled, machine.GuardStackEnabled)
			run = w.mustMove(t, run, kernel.RunStatePreparing, machine.DriverWorkerClaim)
			run = w.mustMove(t, run, kernel.RunStateInitializing, machine.DriverWorkerReport)
			run = w.mustMove(t, run, kernel.RunStatePlanning, machine.DriverWorkerReport)
			receipt := policyReceipt(w, kernel.NewULID())
			receipt.Type, receipt.Outcome = policyapi.Plan, policyapi.Deny
			receipt.Flags = []string{"plan-uncommitted"}
			receipt.Decision = json.RawMessage(`{"deny":["blocked"],"warn":[],"info":[],"flag":["plan-uncommitted"],"reset_flag":[]}`)
			inject := true
			mover := &afterPlanTransition{Mover: w.lc, fail: fault == "after transition"}
			evaluator := planReportEvaluation(func(context.Context, policyapi.EvaluationRequest) (policyapi.Evaluation, error) {
				if inject && fault == "evaluation" {
					return policyapi.Evaluation{}, errInjected
				}
				return policyapi.Evaluation{
					Type: policyapi.Plan, HasPolicies: true, Outcome: policyapi.Deny,
					Plan: &policyapi.PlanDecision{Deny: []string{"blocked"}}, Receipts: []policyapi.Receipt{receipt},
				}, nil
			})
			service := report.New(report.Deps{DB: w.db, Store: w.store, Lifecycle: mover, Stacks: w.stacks, Policies: evaluator})
			request := workersapi.Report{State: run.State, StateVersion: run.Version, Event: workersapi.ReportSucceeded, Delta: &kernel.PlanDelta{Add: 1}}
			call := func() error {
				return w.in(t, func(ctx context.Context) error { _, err := service.Report(ctx, run.ID, request); return err })
			}
			beforeUpdates, beforeSchedules := len(w.updates(t, run.ID)), w.schedules(t)
			if err := call(); !errors.Is(err, errInjected) {
				t.Fatalf("fault did not reach the caller: %v", err)
			}
			stored, err := w.store.Get(t.Context(), w.acct, run.ID)
			if err != nil || stored.State != kernel.RunStatePlanning || stored.Version != run.Version || stored.Delta != nil {
				t.Fatalf("failed report committed state or delta: %+v %v", stored, err)
			}
			if ev, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{run.ID}); err != nil || len(ev[run.ID].Receipts) != 0 || len(ev[run.ID].Flags) != 0 {
				t.Fatalf("failed report committed receipts or flags: %+v %v", ev, err)
			}
			if holder, held, err := w.store.Holder(t.Context(), w.acct, w.stack); err != nil || !held || holder != run.ID {
				t.Fatal("rolled-back denial released its stack lock")
			}
			if len(w.updates(t, run.ID)) != beforeUpdates || w.schedules(t) != beforeSchedules {
				t.Fatal("failed report committed notifications or scheduler jobs")
			}
			inject, mover.fail = false, false
			if err := call(); err != nil {
				t.Fatalf("healthy retry failed: %v", err)
			}
			stored, err = w.store.Get(t.Context(), w.acct, run.ID)
			if err != nil || stored.State != kernel.RunStateFailed || stored.Version != run.Version+1 || stored.Delta == nil || *stored.Delta != *request.Delta {
				t.Fatal("healthy retry did not commit its exact state and delta")
			}
			if ev, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{run.ID}); err != nil || len(ev[run.ID].Receipts) != 1 || len(ev[run.ID].Flags) != 1 {
				t.Fatal("healthy retry lost or duplicated its evidence")
			}
			if len(w.updates(t, run.ID)) != beforeUpdates+1 {
				t.Fatal("healthy retry lost or duplicated its notification")
			}
		})
	}
}
