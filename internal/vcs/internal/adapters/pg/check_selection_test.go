package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
)

type checkSelectionStore struct {
	feedback.CheckStore
	before func(context.Context) error
}

func (s *checkSelectionStore) ResolveCheckRepository(ctx context.Context, b feedback.CheckBinding) (feedback.CheckBinding, error) {
	if s.before != nil && !tx.In(ctx) {
		before := s.before
		s.before = nil
		if err := before(ctx); err != nil {
			return b, err
		}
	}
	return s.CheckStore.ResolveCheckRepository(ctx, b)
}

type checkSelectionHistory struct{ run runsapi.CheckRun }

func (h checkSelectionHistory) HistoryAt(context.Context, kernel.AccountID, kernel.ULID, int64) (runsapi.RunHistory, error) {
	return runsapi.RunHistory{
		ID: h.run.ID, Stack: h.run.Stack, Type: h.run.Type, CommitHash: h.run.CommitSHA, CreatedAt: h.run.CreatedAt, Through: 1,
		Transitions: []runsapi.RunTransition{{Sequence: 1, State: kernel.RunStateQueued, At: h.run.CreatedAt}},
	}, nil
}

func TestAccCommitCheckFirstBindingRefreshesConcurrentRename(t *testing.T) {
	w := newCheckWorld(t)
	ctx := t.Context()
	native := lifecycle.Repository{Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}, ObservedAt: w.now}
	if err := w.f.store.PutRepositoryIdentity(ctx, w.b.Account, w.b.Integration, native); err != nil {
		t.Fatal(err)
	}
	store := &checkSelectionStore{CheckStore: w.f.store}
	store.before = func(ctx context.Context) error {
		// The first Stack.Settings already returned the old coordinates. The
		// repository map and owned Stacks port now expose the committed rename.
		native.Namespace, native.Name = "new-owner", "renamed"
		if err := w.f.store.PutRepositoryIdentity(ctx, w.b.Account, w.b.Integration, native); err != nil {
			return err
		}
		settings := w.facts.settings[w.b.Stack]
		settings.Namespace, settings.Repository = native.Namespace, native.Name
		w.facts.settings[w.b.Stack] = settings
		return w.fake.RenameProject("acme", "infra", native.Namespace, native.Name)
	}
	w.service.WithChecks(feedback.CheckDeps{
		DB: w.f.db, Store: store, Facts: w.facts, Histories: checkSelectionHistory{w.facts.runs[w.b.Run]}, Stacks: w.facts, Integrations: w.f.svc,
		Providers: map[string]feedback.CheckProviders{"GITLAB": w.checks}, Public: "https://infra.example",
	})
	body, err := json.Marshal(runsapi.Updated{
		State: kernel.RunStateQueued,
		Run:   runsapi.UpdatedRun{ID: w.b.Run, Type: w.b.Type, State: kernel.RunStateQueued, Commit: runsapi.UpdatedCommit{Hash: w.b.Commit}},
		Stack: runsapi.UpdatedStack{ID: w.b.Slug, ULID: w.b.Stack},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := outbox.Event{
		Account: w.b.Account.ULID, ID: kernel.NewULID(), Kind: runsapi.UpdatedKind, SchemaVersion: runsapi.UpdatedVersion,
		Subject: outbox.Subject{Kind: "run", ID: w.b.Run.String()}, Sequence: 1, Document: body,
	}
	if targets, err := w.service.Targets(ctx, event); !errors.Is(err, kernel.ErrUnavailable) || len(targets) != 0 {
		t.Fatal("mixed source snapshot was not retried", targets, err)
	}
	if w.checks.calls != 0 {
		t.Fatal("target lookup performed a native write")
	}
	targets, err := w.service.Targets(ctx, event)
	if err != nil || len(targets) != 1 {
		t.Fatal("fresh selection did not recover", targets, err)
	}
	if err := tx.Do(ctx, w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.Enqueue(ctx, targets[0]) }); err != nil {
		t.Fatal(err)
	}
	bound, err := w.f.store.CheckBinding(ctx, w.b.Account, w.b.Key)
	if err != nil || bound.RepositoryID != native.ID || bound.Namespace != native.Namespace || bound.Repository != native.Name {
		t.Fatal("new binding permanently downgraded a known native repository", bound, err)
	}
	w.report(t)
	if writes := w.fake.CheckWrites(native.Namespace, native.Name); len(writes) != 1 {
		t.Fatal("first check did not reach the current repository", writes)
	}
}

func TestAccCommitCheckNewBindingRetriesSwitchedDefault(t *testing.T) {
	w := newCheckWorld(t)
	ctx := t.Context()
	native := lifecycle.Repository{Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}, ObservedAt: w.now}
	if err := w.f.store.PutRepositoryIdentity(ctx, w.b.Account, w.b.Integration, native); err != nil {
		t.Fatal(err)
	}
	settings := w.facts.settings[w.b.Stack]
	settings.VCSIntegration = nil
	w.facts.settings[w.b.Stack] = settings
	other, err := w.f.store.Get(ctx, w.b.Account, w.b.Integration, false)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := w.f.store.Secrets(ctx, w.b.Account, w.b.Integration)
	if err != nil {
		t.Fatal(err)
	}
	other.ID, other.Slug, other.Name = kernel.NewULID(), "other-checks", "Other checks"
	if err := w.f.store.Create(ctx, other, secrets); err != nil {
		t.Fatal(err)
	}
	makeDefault := func(id kernel.ULID) {
		t.Helper()
		if err := tx.Do(ctx, w.f.db, tx.Options{}, func(ctx context.Context) error {
			if _, err := tx.Q(ctx, w.f.db).Exec(ctx, `UPDATE vcs_integrations SET is_default=false WHERE account_id=$1 AND provider='GITLAB'`, w.b.Account); err != nil {
				return err
			}
			_, err := tx.Q(ctx, w.f.db).Exec(ctx, `UPDATE vcs_integrations SET is_default=true WHERE account_id=$1 AND id=$2`, w.b.Account, id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	makeDefault(w.b.Integration)
	w.b, err = w.f.store.ResolveCheckRepository(ctx, w.b)
	if err != nil || w.b.RepositoryID == "" {
		t.Fatal(w.b, err)
	}
	target := w.target(t, w.b, "infraweave/infra", false, 1)
	makeDefault(other.ID)
	if err := tx.Do(ctx, w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.Enqueue(ctx, target) }); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatal("new binding accepted an obsolete default selected before the transaction", err)
	}
	var count int
	if err := w.f.db.QueryRow(ctx, `SELECT count(*) FROM vcs_check_bindings WHERE account_id=$1`, w.b.Account).Scan(&count); err != nil || count != 0 {
		t.Fatal("retryable source change committed a partial binding", count, err)
	}
	makeDefault(w.b.Integration)
	w.enqueue(t, w.b, 1)
	w.report(t)
	makeDefault(other.ID)
	w.state(w.b.Run, kernel.RunStatePlanning, 3)
	w.enqueue(t, w.b, 3)
	w.report(t)
	if w.checks.calls != 2 {
		t.Fatal("existing immutable binding followed a later replacement default", w.checks.calls)
	}
}
