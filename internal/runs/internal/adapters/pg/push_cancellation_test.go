package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/control"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func pushWaiting(t *testing.T, w *world, state kernel.RunState, hash string) lifecycle.Run {
	t.Helper()
	run := mustCreate(t, w, kernel.RunTypeTracked, hash)
	if state == kernel.RunStateQueued {
		return run
	}
	run = w.mustMove(t, run, kernel.RunStateReady, machine.DriverScheduler,
		machine.GuardStackLockFree, machine.GuardApproved, machine.GuardUpstreamFinished, machine.GuardExternalSettled, machine.GuardStackEnabled)
	if state == kernel.RunStateReady {
		return run
	}
	for _, step := range []struct {
		state  kernel.RunState
		driver machine.Driver
	}{
		{kernel.RunStatePreparing, machine.DriverWorkerClaim},
		{kernel.RunStateInitializing, machine.DriverWorkerReport},
		{kernel.RunStatePlanning, machine.DriverWorkerReport},
		{kernel.RunStateUnconfirmed, machine.DriverDecision},
	} {
		run = w.mustMove(t, run, step.state, step.driver)
	}
	return run
}

func TestAccPushCancellationSkipsForeignAndAdvancedTargets(t *testing.T) {
	w := newWorld(t)
	eligible := mustCreate(t, w, kernel.RunTypeTracked, "eligible")
	proposal := mustCreate(t, w, kernel.RunTypeProposed, "proposal")
	task := mustCreate(t, w, kernel.RunTypeTask, "task")
	pickedUp := pushWaiting(t, w, kernel.RunStateReady, "picked-up")
	pickedUp = w.mustMove(t, pickedUp, kernel.RunStatePreparing, machine.DriverWorkerClaim)
	approved := pushWaiting(t, w, kernel.RunStateUnconfirmed, "approved")
	approved = w.mustMove(t, approved, kernel.RunStateConfirmed, machine.DriverAPI, machine.GuardApproved)
	createElsewhere := func(account kernel.AccountID) lifecycle.Run {
		t.Helper()
		stack := kernel.NewULID()
		w.stacks.settings[stack] = stacksapi.Settings{Stack: stacksapi.Stack{ID: stack, Account: account, Space: kernel.NewULID(), Slug: "other"}}
		var run lifecycle.Run
		if err := w.in(t, func(ctx context.Context) error {
			var err error
			run, _, err = w.lc.Create(ctx, lifecycle.NewRun{
				Account: account, Stack: stack, Type: kernel.RunTypeTracked, Commit: stacksapi.Commit{Hash: "other"},
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return run
	}
	otherStack, otherAccount := createElsewhere(w.acct), createElsewhere(kernel.NewAccountID())
	replacement := mustCreate(t, w, kernel.RunTypeTracked, "new")
	controller := w.internalSupersede()
	if _, err := controller.CancelForPush(t.Context(), w.acct, w.stack, replacement.ID, nil); !errors.Is(err, runsapi.ErrNoTransaction) {
		t.Fatalf("unfenced cancellation=%v", err)
	}
	if err := w.in(t, func(ctx context.Context) error {
		changed, err := controller.CancelForPush(ctx, w.acct, w.stack, replacement.ID, []kernel.ULID{
			eligible.ID, proposal.ID, task.ID, pickedUp.ID, approved.ID, otherStack.ID, otherAccount.ID, replacement.ID, {}, kernel.NewULID(),
		})
		if err == nil && changed != 1 {
			return fmt.Errorf("canceled %d targets, want only one eligible run", changed)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, unchanged := range []lifecycle.Run{proposal, task, pickedUp, approved, otherStack, otherAccount, replacement} {
		got, err := w.store.Get(t.Context(), unchanged.Account, unchanged.ID)
		if err != nil || got.State != unchanged.State || got.Version != unchanged.Version {
			t.Errorf("ineligible target %s changed from %s/%d to %s/%d: %v", unchanged.ID, unchanged.State, unchanged.Version, got.State, got.Version, err)
		}
	}
}

func TestAccPushCancellationRollsBackWithNewReplacement(t *testing.T) {
	w := newWorld(t)
	old := pushWaiting(t, w, kernel.RunStateReady, "old")
	creator, _ := w.internalCreator()
	request := creationRequest(w, "new")
	request.Type = kernel.RunTypeTracked
	request.Receipts = []runsapi.PolicyReceipt{{
		ID: kernel.NewULID(), Policy: kernel.NewULID(), PolicyName: "Replacement", PolicyVersion: 1,
		Type: "GIT_PUSH", Engine: "REGO_V1", Outcome: "PUSH", CreatedAt: w.now,
		Decision: json.RawMessage(`{"track":true,"flag":["replacement"]}`), Flags: []string{"replacement"},
	}}
	before := map[string]int{}
	for _, table := range []string{"runs", "run_state_transitions", "run_policy_receipts", "run_flags", "events", "jobs"} {
		before[table] = count(t, w.db, table, w.acct)
	}
	abort := errors.New("rollback replacement and cancellation")
	controller := w.internalSupersede()
	err := w.in(t, func(ctx context.Context) error {
		created, err := creator.Create(ctx, request)
		if err != nil {
			return err
		}
		if _, err := controller.CancelForPush(ctx, w.acct, w.stack, created.ID, []kernel.ULID{old.ID}); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	for table, n := range before {
		if got := count(t, w.db, table, w.acct); got != n {
			t.Errorf("%s after rollback=%d, want %d", table, got, n)
		}
	}
	got, err := w.store.Get(t.Context(), w.acct, old.ID)
	if err != nil || got.State != old.State || got.Version != old.Version {
		t.Fatalf("cancellation escaped rollback: %+v error=%v", got, err)
	}
}

type pushDisconnectStore struct {
	control.Store
	db     *pgxpool.Pool
	target kernel.ULID
	killed bool
}

func (s *pushDisconnectStore) Lock(ctx context.Context, account kernel.AccountID, id kernel.ULID) (lifecycle.Run, error) {
	if id == s.target && !s.killed {
		var pid int32
		if err := tx.Q(ctx, s.db).QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return lifecycle.Run{}, err
		}
		if _, err := s.db.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			return lifecycle.Run{}, err
		}
		s.killed = true
	}
	return s.Store.Lock(ctx, account, id)
}

func TestAccPushCancellationPropagatesActualBackendFailure(t *testing.T) {
	for _, boundary := range []string{"replacement", "target"} {
		t.Run(boundary, func(t *testing.T) {
			w := newWorld(t)
			old := mustCreate(t, w, kernel.RunTypeTracked, "old")
			store := &pushDisconnectStore{Store: w.store, db: w.db}
			controller := control.New(control.Deps{
				DB: w.db, Store: store, Stacks: w.stacks, Spaces: &creationSpaces{account: w.acct}, Lifecycle: w.lc,
			})
			creator, _ := w.internalCreator()
			request := creationRequest(w, "new")
			request.Type = kernel.RunTypeTracked
			before := count(t, w.db, "events", w.acct)
			err := w.in(t, func(ctx context.Context) error {
				created, err := creator.Create(ctx, request)
				if err != nil {
					return err
				}
				store.target = old.ID
				if boundary == "replacement" {
					store.target = created.ID
				}
				_, err = controller.CancelForPush(ctx, w.acct, w.stack, created.ID, []kernel.ULID{old.ID})
				return err
			})
			if !store.killed || !errors.Is(err, kernel.ErrUnavailable) {
				t.Fatalf("actual backend failure killed=%v error=%v", store.killed, err)
			}
			got, readErr := w.store.Get(t.Context(), w.acct, old.ID)
			if readErr != nil || got.State != old.State || got.Version != old.Version || count(t, w.db, "runs", w.acct) != 1 || count(t, w.db, "events", w.acct) != before {
				t.Fatalf("failed cancellation leaked writes: state=%s/%d error=%v", got.State, got.Version, readErr)
			}
		})
	}
}

func TestAccPushCancellationRefusesInvalidReplacementAndMissingOwners(t *testing.T) {
	for _, reason := range []string{"account", "stack", "replacement", "missing replacement", "task replacement", "missing owner", "stack read", "space hold", "stack lock"} {
		t.Run(reason, func(t *testing.T) {
			w := newWorld(t)
			replacement := mustCreate(t, w, kernel.RunTypeTracked, "replacement")
			account, stack, id := w.acct, w.stack, replacement.ID
			spaces := &creationSpaces{account: w.acct}
			deps := control.Deps{DB: w.db, Store: w.store, Stacks: w.stacks, Spaces: spaces, Lifecycle: w.lc}
			cause := errors.New("owner port unavailable")
			want := runsapi.ErrPushCancellationInput
			switch reason {
			case "account":
				account = kernel.AccountID{}
			case "stack":
				stack = kernel.ULID{}
			case "replacement":
				id = kernel.ULID{}
			case "missing replacement":
				id = kernel.NewULID()
			case "task replacement":
				id = mustCreate(t, w, kernel.RunTypeTask, "task").ID
			case "missing owner":
				deps.Spaces, want = nil, kernel.ErrUnavailable
			case "stack read":
				w.stacks.err["Settings"], want = cause, cause
			case "space hold":
				spaces.err, want = cause, cause
			case "stack lock":
				w.stacks.err["LockForRun"], want = cause, cause
			}
			controller := control.New(deps)
			err := w.in(t, func(ctx context.Context) error {
				_, err := controller.CancelForPush(ctx, account, stack, id, nil)
				return err
			})
			if !errors.Is(err, want) {
				t.Fatalf("cancellation error=%v, want %v", err, want)
			}
		})
	}
}

func TestAccPushCancellationRechecksWaitingStates(t *testing.T) {
	for _, state := range []kernel.RunState{kernel.RunStateQueued, kernel.RunStateReady, kernel.RunStateUnconfirmed} {
		t.Run(string(state), func(t *testing.T) {
			w := newWorld(t)
			old := pushWaiting(t, w, state, "old")
			replacement := mustCreate(t, w, kernel.RunTypeTracked, "new")
			control := w.internalSupersede()
			if err := w.in(t, func(ctx context.Context) error {
				changed, err := control.CancelForPush(ctx, w.acct, w.stack, replacement.ID, []kernel.ULID{old.ID, old.ID, replacement.ID, kernel.NewULID()})
				if err != nil {
					return err
				}
				if changed != 1 {
					return fmt.Errorf("push cancellation changed %d runs, want one", changed)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			got, err := w.store.Get(t.Context(), w.acct, old.ID)
			want := kernel.RunStateCanceled
			if state == kernel.RunStateUnconfirmed {
				want = kernel.RunStateDiscarded
			}
			if err != nil || got.State != want || got.Version != old.Version+1 {
				t.Fatalf("push cancellation state=%s/%d error=%v, want %s/%d", got.State, got.Version, err, want, old.Version+1)
			}
			history := w.steps(t, old.ID)
			if note := history[len(history)-1].Note; note == nil || *note != "canceled by push policy in favor of run "+replacement.ID.String() {
				t.Fatalf("push cancellation note=%v", note)
			}
			if err := w.in(t, func(ctx context.Context) error {
				changed, err := control.CancelForPush(ctx, w.acct, w.stack, replacement.ID, []kernel.ULID{old.ID})
				if err == nil && changed != 0 {
					t.Errorf("duplicate cancellation changed %d runs", changed)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
