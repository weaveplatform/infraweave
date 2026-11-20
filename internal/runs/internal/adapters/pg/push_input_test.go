package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/machine"
)

func TestAccPushInProgressUsesOwnedFactsAndFlagUnion(t *testing.T) {
	w := newWorld(t)
	queued := mustCreate(t, w, kernel.RunTypeTracked, "queued")
	task := mustCreate(t, w, kernel.RunTypeTask, "task")
	ended := mustCreate(t, w, kernel.RunTypeProposed, "ended")
	w.mustMove(t, ended, kernel.RunStateCanceled, machine.DriverAPI)
	marker := "private-push-runtime-workspace"
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET local_workspace=$3,drift_detection=true,force_apply_mode='SINGLE',
		runtime_config=jsonb_build_object('private',$3::text),resolved_config=jsonb_build_object('private',$3::text),
		env_overrides_sealed=decode('01','hex') WHERE account_id=$1 AND id=$2`, w.acct, queued.ID, marker); err != nil {
		t.Fatal(err)
	}
	abort := errors.New("rollback owned flags")
	err := w.in(t, func(ctx context.Context) error {
		for _, flags := range [][]string{{"one", "shared"}, {"shared", "two"}} {
			receipt := policyReceipt(w, kernel.NewULID())
			receipt.Flags = flags
			if err := w.store.RecordPolicyEvidence(ctx, w.acct, queued.ID, queued.Version, []policyapi.Receipt{receipt}); err != nil {
				return err
			}
		}
		got, err := w.store.PushInProgress(ctx, w.acct, w.stack)
		if err != nil {
			return err
		}
		if len(got) != 2 {
			return errors.New("push input did not exclude terminal runs")
		}
		seenQueued, seenTask := false, false
		for _, run := range got {
			seenQueued = seenQueued || run.ID == queued.ID
			seenTask = seenTask || run.ID == task.ID
			if run.ID == queued.ID && (!slices.Equal(run.Flags, []string{"one", "shared", "two"}) || !run.BasedOnLocalWorkspace || !run.DriftDetection ||
				run.ForceApplyMode != "SINGLE" || run.Version != 1 || run.Commit == nil || run.Commit.Hash != "queued" || run.TriggeredBy == nil || *run.TriggeredBy != "alice") {
				return errors.New("push input omitted persisted current run facts")
			}
			if run.ID == task.ID && (run.Command == nil || *run.Command != "terraform output" || run.Type != kernel.RunTypeTask) {
				return errors.New("push input omitted its task command")
			}
		}
		if !seenQueued || !seenTask {
			return errors.New("push input omitted an in-progress run")
		}
		raw, err := json.Marshal(got)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(marker)) || bytes.Contains(raw, []byte("runtimeConfig")) {
			return errors.New("push metadata exposed an unconsumed private field")
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	got, err := w.store.PushInProgress(t.Context(), w.acct, w.stack)
	if err != nil || len(got) != 2 {
		t.Fatalf("push metadata=%+v error=%v", got, err)
	}
	for _, run := range got {
		if len(run.Flags) != 0 || !run.CreatedAt.Equal(queued.CreatedAt) || !run.UpdatedAt.Equal(queued.UpdatedAt) {
			t.Errorf("push metadata retained rolled-back flags or changed timestamps: %+v", run)
		}
	}
	if bytes.Compare(got[0].ID[:], got[1].ID[:]) >= 0 {
		t.Fatal("equal creation times did not retain deterministic ID order")
	}
	for _, scope := range []struct {
		name    string
		account kernel.AccountID
		stack   kernel.ULID
	}{{"other account", kernel.NewAccountID(), w.stack}, {"other stack", w.acct, kernel.NewULID()}} {
		t.Run(scope.name, func(t *testing.T) {
			if got, err := w.store.PushInProgress(t.Context(), scope.account, scope.stack); err != nil || len(got) != 0 {
				t.Fatalf("foreign progress facts=%+v error=%v", got, err)
			}
		})
	}
}

func TestAccPushInProgressFailsClosedOnOwnedStorageFailure(t *testing.T) {
	w := newWorld(t)
	run := mustCreate(t, w, kernel.RunTypeTracked, "corrupt")
	if _, err := w.db.Exec(t.Context(), `UPDATE runs SET commit='"invalid-commit-shape"'::jsonb WHERE account_id=$1 AND id=$2`, w.acct, run.ID); err != nil {
		t.Fatal(err)
	}
	if rows, err := w.store.PushInProgress(t.Context(), w.acct, w.stack); err == nil || errors.Is(err, policyapi.ErrInput) || rows != nil {
		t.Fatalf("corrupt progress facts=%+v error=%v", rows, err)
	}
	w.db.Close()
	if rows, err := w.store.PushInProgress(t.Context(), w.acct, w.stack); err == nil || errors.Is(err, policyapi.ErrInput) || rows != nil {
		t.Fatalf("unavailable progress facts=%+v error=%v", rows, err)
	}
}
