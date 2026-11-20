package pg_test

import (
	"context"
	"fmt"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

type reportSpaceHold struct {
	accessapi.Spaces
	held bool
}

func (s *reportSpaceHold) Hold(context.Context, kernel.AccountID, kernel.ULID) error {
	s.held = true
	return nil
}

type reportOwnerOrder struct {
	stacksapi.Stacks
	w       *world
	run     kernel.ULID
	checked bool
	probe   context.Context
}

func (s *reportOwnerOrder) check() error {
	if s.checked {
		return nil
	}
	s.checked = true
	// A different connection must be able to lock the run at first owner
	// contact. Otherwise a space/stack writer can hold the inverse order.
	err := tx.Do(s.probe, s.w.db, tx.Options{}, func(other context.Context) error {
		var id kernel.ULID
		return tx.Q(other, s.w.db).QueryRow(other, `SELECT id FROM runs WHERE account_id=$1 AND id=$2 FOR UPDATE NOWAIT`, s.w.acct, s.run).Scan(&id)
	})
	if err != nil {
		return fmt.Errorf("run row locked before owner fence: %w", err)
	}
	return nil
}

func (s *reportOwnerOrder) Get(ctx context.Context, account kernel.AccountID, id kernel.ULID) (stacksapi.Stack, error) {
	if err := s.check(); err != nil {
		return stacksapi.Stack{}, err
	}
	return s.Stacks.Get(ctx, account, id)
}

func (s *reportOwnerOrder) Settings(ctx context.Context, account kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error) {
	if err := s.check(); err != nil {
		return stacksapi.Settings{}, err
	}
	return s.Stacks.Settings(ctx, account, id)
}

func TestAccApprovalReportFencesOwnersBeforeRunRow(t *testing.T) {
	w := newWorld(t)
	settings := w.stacks.settings[w.stack]
	pool := kernel.NewULID()
	settings.WorkerPool = &pool
	w.stacks.settings[w.stack] = settings
	r := mustCreate(t, w, kernel.RunTypeTracked, "owner-order")
	w.takeLock(t, r)
	r = w.mustMove(
		t,
		r,
		kernel.RunStateReady,
		machine.DriverScheduler,
		machine.GuardStackLockFree,
		machine.GuardApproved,
		machine.GuardUpstreamFinished,
		machine.GuardExternalSettled,
		machine.GuardStackEnabled,
	)
	r = w.mustMove(t, r, kernel.RunStatePreparing, machine.DriverWorkerClaim)
	r = w.mustMove(t, r, kernel.RunStateInitializing, machine.DriverWorkerReport)
	r = w.mustMove(t, r, kernel.RunStatePlanning, machine.DriverWorkerReport)
	owners := &reportOwnerOrder{Stacks: w.stacks, w: w, run: r.ID, probe: t.Context()}
	spaces := &reportSpaceHold{}
	s := report.New(report.Deps{DB: w.db, Store: w.store, Stacks: owners, Spaces: spaces, Lifecycle: w.lc})
	err := w.in(t, func(ctx context.Context) error {
		_, err := s.Report(ctx, r.ID, workersapi.Report{State: r.State, StateVersion: r.Version, Event: workersapi.ReportSucceeded, Delta: &kernel.PlanDelta{Add: 1}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !owners.checked || !spaces.held {
		t.Fatal("report skipped its owner fences")
	}
}
