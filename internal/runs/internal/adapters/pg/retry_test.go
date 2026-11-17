package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// recordRetry stores the first-handover record a fixture's ended run had.
func recordRetry(t *testing.T, w *world, run lifecycle.Run, record *runtimeconfig.Record) {
	t.Helper()
	if err := w.in(t, func(ctx context.Context) error {
		return w.store.RecordResolved(ctx, run, record)
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAccRetryResolvedConfiguration copies a record, leaves an absent one
// absent, and keeps a matching active run's own record during deduplication.
func TestAccRetryResolvedConfiguration(t *testing.T) {
	for name, recorded := range map[string]bool{"before first handover": false, "recorded": true} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			a := w.access()
			a.held = holding(accessapi.ActionRunRetry)
			c := w.controller(a)
			c.BindSources(w.sources)
			w.sources.commits = map[string]stacksapi.Commit{"a": {Hash: "a"}}
			input := json.RawMessage(`{"runnerImage":"runner:requested"}`)
			var original lifecycle.Run
			if err := w.in(t, func(ctx context.Context) error {
				var err error
				original, _, err = w.lc.Create(ctx, lifecycle.NewRun{
					Account: w.acct, Stack: w.stack, Type: kernel.RunTypeProposed,
					Commit: stacksapi.Commit{Hash: "a"}, Actor: lifecycle.ActorOf(w.alice()),
					RuntimeConfig: input, ForceApply: "SINGLE",
				})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			ended := w.canceled(t, original)
			var inherited *runtimeconfig.Record
			if recorded {
				inherited = resolvedRecord(t)
				recordRetry(t, w, ended, inherited)
				source, err := w.store.Get(t.Context(), w.acct, ended.ID)
				if err != nil {
					t.Fatal(err)
				}
				inherited = source.ResolvedConfig
			}
			got, err := c.Retry(t.Context(), w.alice(), "network", ended.ID.String())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Run.RuntimeConfig, ended.RuntimeConfig) || got.Run.ForceApply != "SINGLE" {
				t.Errorf("retry dropped runtime input or force-apply mode: %+v", got.Run)
			}
			stored, err := w.store.Get(t.Context(), w.acct, got.Run.ID)
			if err != nil || !reflect.DeepEqual(stored.ResolvedConfig, inherited) || !reflect.DeepEqual(got.Run.ResolvedConfig, inherited) {
				t.Fatalf("retry record: read %+v, stored %+v, want %+v, error %v", got.Run.ResolvedConfig, stored.ResolvedConfig, inherited, err)
			}
			if inherited == nil {
				return
			}
			// A concurrent trigger already created a run on this commit with
			// its own record. Retrying must answer it without replacing that.
			if _, err := c.Cancel(t.Context(), w.alice(), "network", got.Run.ID.String(), ""); err != nil {
				t.Fatal(err)
			}
			active := mustCreate(t, w, kernel.RunTypeProposed, "a")
			own := resolvedRecord(t)
			own.Execution.EnableSensitiveOutputUpload = true
			recordRetry(t, w, active, own)
			storedActive, err := w.store.Get(t.Context(), w.acct, active.ID)
			if err != nil {
				t.Fatal(err)
			}
			own = storedActive.ResolvedConfig
			again, err := c.Retry(t.Context(), w.alice(), "network", ended.ID.String())
			if err != nil || again.Run.ID != active.ID || !reflect.DeepEqual(again.Run.ResolvedConfig, own) || len(again.Run.RuntimeConfig) != 0 {
				t.Fatalf("deduplicated retry %+v, %v", again.Run, err)
			}
		})
	}
}

// TestAccRetryGuards keeps read guards and mutations consistent when a
// run finished or its stack moved to a different tracked commit.
func TestAccRetryGuards(t *testing.T) {
	w := newWorld(t)
	a := w.access()
	a.held = holding(accessapi.ActionRunRetry, accessapi.ActionRunRetryBlocking)
	c := w.controller(a)
	c.BindSources(w.sources)
	w.sources.commits = map[string]stacksapi.Commit{"a": {Hash: "a"}}
	ctx, p := t.Context(), w.alice()
	finished := w.working(t, kernel.RunTypeProposed, kernel.RunStateFinished, nil, kernel.NewULID(), w.now)
	_, err := c.Retry(ctx, p, "network", finished.String())
	refusedFor(t, "a finished run", err, "a retry needs a run that ended other than in FINISHED")
	v, err := w.reader(a).Run(ctx, p, finished.String())
	if err != nil || v.Guards.Retry != "a retry needs a run that ended other than in FINISHED" {
		t.Fatalf("finished run's view %+v, %v", v, err)
	}
	tracked := w.canceled(t, mustCreate(t, w, kernel.RunTypeTracked, "a"))
	settings := w.stacks.settings[w.stack]
	settings.TrackedCommit = &tracked.Commit
	w.stacks.settings[w.stack] = settings
	v, err = w.reader(a).Run(ctx, p, tracked.ID.String())
	if err != nil || v.Guards.Retry != "" {
		t.Fatalf("current tracked run's guard %q, %v", v.Guards.Retry, err)
	}
	settings.TrackedCommit = &stacksapi.Commit{Hash: "newer"}
	w.stacks.settings[w.stack] = settings
	v, err = w.reader(a).Run(ctx, p, tracked.ID.String())
	if err != nil || v.Guards.Retry != "a retry needs a tracked run on the stack's tracked commit" {
		t.Fatalf("old tracked run's guard %q, %v", v.Guards.Retry, err)
	}
	_, err = c.Retry(ctx, p, "network", tracked.ID.String())
	refusedFor(t, "an old tracked run", err, "a retry needs a tracked run on the stack's tracked commit")
	proposed := w.canceled(t, mustCreate(t, w, kernel.RunTypeProposed, "a"))
	if got, err := c.Retry(ctx, p, "network", proposed.ID.String()); err != nil || got.Run.Commit.Hash != "a" {
		t.Fatalf("old proposed run %+v, %v", got, err)
	}
	w.stacks.err["LockForRun"] = errInjected
	if _, err := c.Retry(ctx, p, "network", proposed.ID.String()); !errors.Is(err, errInjected) {
		t.Errorf("failed stack read = %v", err)
	}
}
