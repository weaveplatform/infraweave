package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
)

func TestAccPushCreationSeedsEvidenceBeforeQueuedConsequences(t *testing.T) {
	w := newWorld(t)
	privateRunPool(w)
	creator, _ := w.internalCreator()
	request := creationRequest(w, "policy-head")
	request.Priority = kernel.RunPriorityHigh
	request.Receipts = []runsapi.PolicyReceipt{{
		ID: kernel.NewULID(), Policy: kernel.NewULID(), PolicyName: "Push flags", PolicyVersion: 7,
		Type: "GIT_PUSH", Engine: "REGO_V1", Outcome: "PUSH", CreatedAt: w.now,
		Decision: json.RawMessage(`{"propose":true,"flag":["from-push"],"reset_flag":[],"exact":9007199254740993}`),
		Flags:    []string{"from-push"}, ResetFlags: []string{},
	}}
	var created runsapi.CreatedRun
	if err := w.in(t, func(ctx context.Context) error {
		var err error
		created, err = creator.Create(ctx, request)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{created.ID})
	if err != nil || len(snapshot[created.ID].Receipts) != 1 || !slices.Equal(snapshot[created.ID].Flags, []string{"from-push"}) {
		t.Fatalf("initial QUEUED receipt and flags were not committed: %+v error=%v", snapshot, err)
	}
	if snapshot[created.ID].Receipts[0].StateVersion != 1 {
		t.Fatal("initial push evidence did not join state version 1")
	}
	updates := w.updates(t, created.ID)
	if len(updates) != 1 || len(updates[0].Doc.PolicyReceipts) != 1 || !slices.Equal(updates[0].Doc.Run.Flags, []string{"from-push"}) {
		t.Fatalf("initial event was published before push evidence: %+v", updates)
	}
	stored, err := w.store.Get(t.Context(), w.acct, created.ID)
	if err != nil || stored.Priority != kernel.RunPriorityHigh {
		t.Fatalf("initial priority=%s error=%v", stored.Priority, err)
	}
	if err := w.in(t, func(ctx context.Context) error {
		again, err := creator.Create(ctx, request)
		if err == nil && (again.Created || again.ID != created.ID) {
			t.Errorf("duplicate created another run: %+v", again)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = w.store.PolicyEvidence(t.Context(), w.acct, []kernel.ULID{created.ID})
	if err != nil || len(snapshot[created.ID].Receipts) != 1 || len(w.updates(t, created.ID)) != 1 {
		t.Fatalf("duplicate replayed initial policy evidence: %+v error=%v", snapshot, err)
	}
}

func TestAccPushCreationRollsBackInitialEvidence(t *testing.T) {
	for _, reason := range []string{"caller rollback", "missing evidence recorder"} {
		t.Run(reason, func(t *testing.T) {
			w := newWorld(t)
			creator, spaces := w.internalCreator()
			if reason == "missing evidence recorder" {
				creator = trigger.New(trigger.Deps{
					DB: w.db, Stacks: w.stacks, Spaces: spaces,
					Lifecycle: lifecycle.New(lifecycle.Deps{Store: w.store, Stacks: w.stacks, Events: w.events, Jobs: w.jobs}),
				})
			}
			request := creationRequest(w, "rollback-policy")
			request.Receipts = []runsapi.PolicyReceipt{{
				ID: kernel.NewULID(), Policy: kernel.NewULID(), PolicyName: "Push flags", PolicyVersion: 1,
				Type: "GIT_PUSH", Engine: "REGO_V0", Outcome: "PUSH", CreatedAt: w.now,
				Decision: json.RawMessage(`{"propose":true,"flag":["must-rollback"]}`), Flags: []string{"must-rollback"},
			}}
			abort := errors.New("caller rollback")
			err := w.in(t, func(ctx context.Context) error {
				if _, err := creator.Create(ctx, request); err != nil {
					return err
				}
				return abort
			})
			want := abort
			if reason == "missing evidence recorder" {
				want = kernel.ErrUnavailable
			}
			if !errors.Is(err, want) {
				t.Fatalf("creation rollback error=%v, want %v", err, want)
			}
			for _, table := range []string{"runs", "run_state_transitions", "run_policy_receipts", "run_flags", "events", "jobs"} {
				if n := count(t, w.db, table, w.acct); n != 0 {
					t.Errorf("%s escaped %s: %d rows", table, reason, n)
				}
			}
		})
	}
}
