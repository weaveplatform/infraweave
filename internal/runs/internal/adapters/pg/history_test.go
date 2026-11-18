package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
)

func TestAccHistoryAt(t *testing.T) {
	db := pool(t)
	store := pg.New(db, nil)
	acct, stack, run := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	at := time.Unix(1800000000, 0).UTC()
	if _, err := db.Exec(t.Context(), `INSERT INTO runs
		(account_id,id,stack_id,type,state,state_version,commit,created_at,triggered_by)
		VALUES ($1,$2,$3,'TRACKED','FINISHED',7,
		'{"hash":"full-commit","branch":"feature/one","tag":"v1","message":"history-private-message"}',$4,'history-private-login')`,
		acct, run, stack, at); err != nil {
		t.Fatal(err)
	}
	states := []kernel.RunState{
		kernel.RunStateQueued, kernel.RunStatePlanning,
		kernel.RunStateUnconfirmed, kernel.RunStateConfirmed, kernel.RunStatePreparingApply,
		kernel.RunStateApplying, kernel.RunStateFinished,
	}
	for i, state := range states {
		if _, err := db.Exec(t.Context(), `INSERT INTO run_state_transitions
			(account_id,run_id,sequence,state,timestamp,terminal,note,username)
			VALUES ($1,$2,$3,$4,$5,$6,'history-private-note','history-private-login')`,
			acct, run, i+1, state, at.Add(time.Duration(i)*time.Minute), state.Terminal()); err != nil {
			t.Fatal(err)
		}
	}
	for _, through := range []int64{1, 3, 7} {
		history, err := store.HistoryAt(t.Context(), acct, run, through)
		if err != nil {
			t.Fatal(err)
		}
		if history.ID != run || history.Stack != stack || history.Type != kernel.RunTypeTracked ||
			history.CommitHash != "full-commit" || history.Branch != "feature/one" || history.Tag != "v1" ||
			history.Local || !history.CreatedAt.Equal(at) || history.Through != through || int64(len(history.Transitions)) != through {
			t.Fatalf("wrong account-bound historical projection: %+v", history)
		}
		for i, step := range history.Transitions {
			if step.Sequence != int64(i+1) || step.State != states[i] || !step.At.Equal(at.Add(time.Duration(i)*time.Minute)) {
				t.Fatalf("transition %d = %+v", i, step)
			}
		}
		b, err := json.Marshal(history)
		if err != nil || strings.Contains(string(b), "history-private") {
			t.Fatal("projection exposes unrelated commit, note or username data", err)
		}
	}
	// Reading state facts must not decode or unseal any runtime/credential
	// envelope. A local workspace is represented only by its presence.
	if _, err := db.Exec(t.Context(), `UPDATE runs SET
		local_workspace='history-private-workspace', env_overrides_sealed='unreadable',
		artifact_key_sealed='unreadable', resolved_config='{"private":"history-private-runtime"}'
		WHERE account_id=$1 AND id=$2`, acct, run); err != nil {
		t.Fatal(err)
	}
	history, err := store.HistoryAt(t.Context(), acct, run, 3)
	if err != nil || !history.Local || history.Type != kernel.RunTypeTracked || len(history.Transitions) != 3 {
		t.Fatalf("state read depends on a runtime envelope: %+v %v", history, err)
	}
	b, err := json.Marshal(history)
	if err != nil || strings.Contains(string(b), "history-private") || strings.Contains(string(b), "unreadable") {
		t.Fatal("projection exposes private workspace/runtime data", err)
	}
	for _, tc := range []struct {
		account kernel.AccountID
		run     kernel.ULID
		through int64
	}{
		{kernel.NewAccountID(), run, 3},
		{acct, kernel.NewULID(), 3},
		{acct, run, 0},
		{acct, run, -1},
		{acct, run, 8},
		{kernel.AccountID{}, run, 3},
		{acct, kernel.ULID{}, 3},
	} {
		if _, err := store.HistoryAt(t.Context(), tc.account, tc.run, tc.through); !errors.Is(err, runsapi.ErrHistoryNotFound) {
			t.Fatalf("absent account/run/exact sequence = %v", err)
		}
	}
	if _, err := db.Exec(t.Context(), `DELETE FROM run_state_transitions WHERE account_id=$1 AND run_id=$2 AND sequence=3`, acct, run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.HistoryAt(t.Context(), acct, run, 3); !errors.Is(err, runsapi.ErrHistoryNotFound) {
		t.Fatal("an absent exact cutoff fell back to another state", err)
	}
	db.Close()
	if _, err := store.HistoryAt(context.Background(), acct, run, 7); err == nil || errors.Is(err, runsapi.ErrHistoryNotFound) {
		t.Fatal("dependency failure was ignored or treated as absent history", err)
	}
}
