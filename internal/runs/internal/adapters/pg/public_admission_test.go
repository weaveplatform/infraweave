package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func privateRunPool(w *world) kernel.ULID {
	pool := kernel.NewULID()
	settings := w.stacks.settings[w.stack]
	settings.WorkerPool = &pool
	w.stacks.settings[w.stack] = settings
	return pool
}

func TestAccPublicCreationRefusesPriorityAndDriftBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name     string
		priority kernel.RunPriority
		drift    bool
	}{
		{name: "high priority", priority: kernel.RunPriorityHigh},
		{name: "low priority", priority: kernel.RunPriorityLow},
		{name: "drift detection", drift: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			admitted := false
			err := w.in(t, func(ctx context.Context) error {
				_, _, err := w.lc.Create(ctx, lifecycle.NewRun{
					Account: w.acct, Stack: w.stack, Type: kernel.RunTypeProposed,
					Commit: stacksapi.Commit{Hash: "public-admission"}, Actor: lifecycle.Actor{Login: "alice"}, Priority: tc.priority, DriftDetection: tc.drift,
					BeforeCreate: func(context.Context) error { admitted = true; return nil },
				})
				return err
			})
			if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
				t.Fatalf("public execution admission=%v, want validation refusal", err)
			}
			if admitted || count(t, w.db, "runs", w.acct) != 0 || count(t, w.db, "run_state_transitions", w.acct) != 0 {
				t.Fatal("public refusal committed creation effects")
			}
		})
	}
}

func TestAccPrivateCreationRetainsPriorityAndDrift(t *testing.T) {
	w := newWorld(t)
	pool := privateRunPool(w)
	var run lifecycle.Run
	if err := w.in(t, func(ctx context.Context) error {
		var err error
		run, _, err = w.lc.Create(ctx, lifecycle.NewRun{
			Account: w.acct, Stack: w.stack, Type: kernel.RunTypeProposed, Commit: stacksapi.Commit{Hash: "private-admission"},
			Actor: lifecycle.Actor{Login: "alice"}, Priority: kernel.RunPriorityHigh, DriftDetection: true,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if run.WorkerPool == nil || *run.WorkerPool != pool || run.Priority != kernel.RunPriorityHigh || !run.DriftDetection {
		t.Fatalf("private admission changed: %+v", run)
	}
}

func TestAccPriorityUsesImmutableRunPoolAfterStackMove(t *testing.T) {
	for _, public := range []bool{true, false} {
		t.Run(map[bool]string{true: "public run", false: "private run"}[public], func(t *testing.T) {
			w := newWorld(t)
			if !public {
				privateRunPool(w)
			}
			run := mustCreate(t, w, kernel.RunTypeProposed, "priority-pool")
			settings := w.stacks.settings[w.stack]
			if public {
				pool := kernel.NewULID()
				settings.WorkerPool = &pool
			} else {
				settings.WorkerPool = nil
			}
			w.stacks.settings[w.stack] = settings
			_, err := w.controller(w.access()).SetPriority(t.Context(), w.alice(), "network", run.ID.String(), kernel.RunPriorityHigh)
			if public {
				if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
					t.Fatalf("public run adopted new stack's priority authority: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			stored, err := w.store.Get(t.Context(), w.acct, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := kernel.RunPriorityHigh
			if public {
				expected = kernel.RunPriorityNormal
			}
			if stored.Priority != expected {
				t.Fatalf("stored priority=%s, want %s", stored.Priority, expected)
			}
		})
	}
}
