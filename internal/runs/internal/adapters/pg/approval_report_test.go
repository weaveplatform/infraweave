package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

func TestAccApprovalRejectedWorkerReportReplaysFinalStep(t *testing.T) {
	w := newWorld(t)
	settings := w.stacks.settings[w.stack]
	pool := kernel.NewULID()
	settings.WorkerPool = &pool
	w.stacks.settings[w.stack] = settings
	r := mustCreate(t, w, kernel.RunTypeTracked, "approval-report")
	w.takeLock(t, r)
	r = w.mustMove(t, r, kernel.RunStateReady, machine.DriverScheduler, machine.GuardStackLockFree, machine.GuardApproved,
		machine.GuardUpstreamFinished, machine.GuardExternalSettled, machine.GuardStackEnabled)
	r = w.mustMove(t, r, kernel.RunStatePreparing, machine.DriverWorkerClaim)
	r = w.mustMove(t, r, kernel.RunStateInitializing, machine.DriverWorkerReport)
	r = w.mustMove(t, r, kernel.RunStatePlanning, machine.DriverWorkerReport)
	w.lc = w.boundApprovals(func(lifecycle.Run) policyapi.Evaluation { return approvalOutcome(w, policyapi.Reject) })
	service := report.New(report.Deps{DB: w.db, Store: w.store, Lifecycle: w.lc, Stacks: w.stacks})
	request := workersapi.Report{State: r.State, StateVersion: r.Version, Event: workersapi.ReportSucceeded, Delta: &kernel.PlanDelta{Add: 1}}
	call := func(req workersapi.Report) (workersapi.Step, error) {
		var step workersapi.Step
		err := w.in(t, func(ctx context.Context) error { var err error; step, err = service.Report(ctx, r.ID, req); return err })
		return step, err
	}
	first, err := call(request)
	if err != nil || first.Action != workersapi.ActionExit || first.State != kernel.RunStateFailed || first.StateVersion != r.Version+2 {
		t.Fatalf("report did not return final approval rejection: %+v %v", first, err)
	}
	again, err := call(request)
	if err != nil || again != first {
		t.Fatalf("lost-response replay did not return committed final step: %+v %v", again, err)
	}
	evidence, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{r.ID})
	if err != nil || len(evidence[r.ID].Receipts) != 1 {
		t.Fatalf("replay duplicated evaluation: %+v %v", evidence, err)
	}
	history, err := w.store.Histories(t.Context(), w.acct, []kernel.ULID{r.ID})
	if err != nil || len(history[r.ID]) != int(first.StateVersion) {
		t.Fatalf("replay duplicated transition: %+v %v", history, err)
	}
	wrong := request
	wrong.Delta = &kernel.PlanDelta{Add: 2}
	if _, err := call(wrong); !errors.Is(err, workersapi.ErrMovedOn) {
		t.Fatalf("altered plan accepted as committed approval replay: %v", err)
	}
	wrong = request
	wrong.Event = workersapi.ReportFailed
	wrong.Delta = nil
	if _, err := call(wrong); !errors.Is(err, workersapi.ErrMovedOn) {
		t.Fatalf("different stale report accepted as approval replay: %v", err)
	}
}
