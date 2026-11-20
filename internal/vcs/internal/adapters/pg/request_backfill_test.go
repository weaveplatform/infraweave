package pg_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

func missingRequestEvent() push.Prepared {
	return push.Prepared{
		Event: vcsapi.Event{
			Type:       "comment",
			Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"},
			Comment:    &vcsapi.Comment{PullRequest: 12, Author: "reviewer", Body: "plan now"},
		},
		AffectedStacksCount: new(int64(0)),
	}
}

func filledRequestEvent(original push.Prepared, label string) push.Prepared {
	out := original
	peer := original.Event.Repository
	out.Request = &port.RequestFacts{
		Snapshot: port.RequestSnapshot{
			Request:        vcsapi.PullRequest{Number: 12, BaseBranch: "main", HeadBranch: "feature", AuthorLogin: "creator", Title: "request", Labels: []string{label}},
			Base:           strings.Repeat("b", 40),
			Head:           strings.Repeat("a", 40),
			BaseRepository: peer,
			HeadRepository: peer,
		},
		Base:  stacksapi.Commit{Hash: strings.Repeat("b", 40), Branch: "main"},
		Head:  stacksapi.Commit{Hash: strings.Repeat("a", 40), Branch: "feature"},
		Actor: "reviewer",
	}
	out.Event.Head, out.Event.Branch = out.Request.Head.Hash, "feature"
	out.Event.PullRequest = &out.Request.Snapshot.Request
	out.Commit = out.Request.Head
	out.Files = []string{label + ".tf"}
	return out
}

func TestAccRequestBackfillOwnershipRollbackAndImmutableWinner(t *testing.T) {
	w := newWebhookWorld(t)
	w.receive(t, "backfill", webhookBody(128))
	delivery := w.delivery(t, "backfill")
	original := missingRequestEvent()
	first := filledRequestEvent(original, "first")
	if _, err := w.f.store.BackfillRequest(t.Context(), w.f.acct, delivery.ID, 0, original.Event, first); !errors.Is(err, push.ErrNoTransaction) {
		t.Fatal("unfenced backfill accepted", err)
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return w.f.store.PutPrepared(ctx, w.f.acct, delivery.ID, []push.Prepared{original})
	}); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("later receipt storage failed")
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		got, err := w.f.store.BackfillRequest(ctx, w.f.acct, delivery.ID, 0, original.Event, first)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatal("backfill changed native facts", got)
		}
		return cause
	})
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
	rows, err := w.f.store.Prepared(t.Context(), w.f.acct, delivery.ID)
	if err != nil || len(rows) != 1 || rows[0].Request != nil {
		t.Fatal("failed completion leaked native backfill", rows, err)
	}
	for _, name := range []string{"foreign account", "foreign delivery", "different event", "missing slot", "missing request"} {
		t.Run(name, func(t *testing.T) {
			account, id, number, event, prepared := w.f.acct, delivery.ID, 0, original.Event, first
			switch name {
			case "foreign account":
				account = kernel.NewAccountID()
			case "foreign delivery":
				id = kernel.NewULID()
			case "different event":
				event.Action = "edited"
			case "missing slot":
				number = 1
			case "missing request":
				prepared = original
			}
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
				got, err := w.f.store.BackfillRequest(ctx, account, id, number, event, prepared)
				if !errors.Is(err, push.ErrRequestChanged) || !reflect.DeepEqual(got, push.Prepared{}) {
					t.Fatal("changed authority reached native facts", got, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		_, err := w.f.store.BackfillRequest(ctx, w.f.acct, delivery.ID, 0, original.Event, first)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	second := filledRequestEvent(original, "second")
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		got, err := w.f.store.BackfillRequest(ctx, w.f.acct, delivery.ID, 0, original.Event, second)
		if err == nil && !reflect.DeepEqual(got, first) {
			t.Fatal("later child replaced the frozen request", got)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAccConcurrentRequestBackfillsShareFirstCommittedSnapshot(t *testing.T) {
	w := newWebhookWorld(t)
	w.receive(t, "concurrent", webhookBody(128))
	delivery := w.delivery(t, "concurrent")
	original := missingRequestEvent()
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return w.f.store.PutPrepared(ctx, w.f.acct, delivery.ID, []push.Prepared{original})
	}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		event push.Prepared
		err   error
	}
	done := make(chan result, 2)
	for _, label := range []string{"first", "second"} {
		go func() {
			var got push.Prepared
			err := tx.Do(t.Context(), w.f.db, tx.Options{NoRetry: true}, func(ctx context.Context) error {
				var err error
				got, err = w.f.store.BackfillRequest(ctx, w.f.acct, delivery.ID, 0, original.Event, filledRequestEvent(original, label))
				return err
			})
			done <- result{got, err}
		}()
	}
	a, b := <-done, <-done
	if a.err != nil || b.err != nil || a.event.Request == nil || !reflect.DeepEqual(a.event, b.event) {
		t.Fatal("concurrent children observed different native input snapshots", a, b)
	}
}

func TestAccRequestBackfillPropagatesActualBackendFailure(t *testing.T) {
	w := newWebhookWorld(t)
	w.receive(t, "backend", webhookBody(128))
	delivery := w.delivery(t, "backend")
	original := missingRequestEvent()
	err := tx.Do(t.Context(), w.f.db, tx.Options{NoRetry: true}, func(ctx context.Context) error {
		_, _ = tx.Q(ctx, w.f.db).Exec(ctx, `SELECT pg_terminate_backend(pg_backend_pid())`)
		_, err := w.f.store.BackfillRequest(ctx, w.f.acct, delivery.ID, 0, original.Event, filledRequestEvent(original, "first"))
		if err == nil || errors.Is(err, push.ErrRequestChanged) {
			t.Fatal("backend failure became a stale-event/no-op outcome", err)
		}
		return err
	})
	if err == nil {
		t.Fatal("actual connection termination was hidden")
	}
}
