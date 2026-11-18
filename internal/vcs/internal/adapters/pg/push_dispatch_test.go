package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/webhooktest"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

type dispatchProvider struct {
	webhooktest.Provider
	events []vcsapi.Event
}

func TestAccEvaluationHoldsDeliveryRetention(t *testing.T) {
	w := newWebhookWorld(t)
	w.receive(t, "held", webhookBody(128))
	delivery := w.delivery(t, "held")
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return w.f.store.PutPrepared(ctx, w.f.acct, delivery.ID, []push.Prepared{{Event: vcsapi.Event{Type: "tag", Tag: "v1"}}})
	}); err != nil {
		t.Fatal(err)
	}
	held, err := w.f.db.BeginTx(t.Context(), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.WithoutCancel(t.Context())) //nolint:errcheck // rollback after commit is harmless
	reader := pg.New(held, w.f.keys)
	if _, found, err := reader.EventContext(t.Context(), w.f.acct, delivery.ID); err != nil || !found {
		t.Fatal("delivery hold failed", found, err)
	}
	// Another stack child can hold the same delivery and update its non-key
	// counter while this child retains its key-share read lock.
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, w.f.db).Exec(ctx, `SET LOCAL lock_timeout='20ms'`); err != nil {
			return err
		}
		if _, found, err := w.f.store.EventContext(ctx, w.f.acct, delivery.ID); err != nil || !found {
			return fmt.Errorf("second child hold: %w", err)
		}
		allowed, err := w.f.store.ReserveRun(ctx, w.f.acct, delivery.ID, 0, 2)
		if err != nil || !allowed {
			return fmt.Errorf("second child counter: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatal("compatible stack children blocked each other", err)
	}
	before := time.Now().Add(time.Hour)
	blocked := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, w.f.db).Exec(ctx, `SET LOCAL lock_timeout='20ms'`); err != nil {
			return err
		}
		_, _, err := w.store.Drop(ctx, &w.f.acct, &before, 1)
		return err
	})
	if cause, ok := errors.AsType[*pgconn.PgError](blocked); !ok || cause.Code != "55P03" {
		t.Fatal("retention deleted active evaluation data", blocked)
	}
	if events, err := reader.Prepared(t.Context(), w.f.acct, delivery.ID); err != nil || len(events) != 1 {
		t.Fatal("held prepared facts disappeared", events, err)
	}
	if err := held.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, deleted, err := w.store.Drop(t.Context(), &w.f.acct, &before, 1); err != nil || deleted != 1 {
		t.Fatal("released delivery was not retained/deleted normally", deleted, err)
	}
	if _, found, err := w.f.store.EventContext(t.Context(), w.f.acct, delivery.ID); err != nil || found {
		t.Fatal("purged delivery remained visible", found, err)
	}
	if events, err := w.f.store.Prepared(t.Context(), w.f.acct, delivery.ID); err != nil || len(events) != 0 {
		t.Fatal("purged delivery retained prepared facts", events, err)
	}
}

func (p dispatchProvider) Parse(http.Header, []byte) ([]vcsapi.Event, error) { return p.events, nil }

type dispatchTargets struct {
	stacksapi.Stacks
	rows []stacksapi.Stack
}

func (s dispatchTargets) VCSRepositories(context.Context, kernel.AccountID, stacksapi.Use) ([]stacksapi.VCSRepository, error) {
	if len(s.rows) == 0 {
		return nil, nil
	}
	return []stacksapi.VCSRepository{{Namespace: "acme", Name: "infra"}}, nil
}

func (s dispatchTargets) ByVCSRepository(context.Context, kernel.AccountID, stacksapi.Use, string, string) ([]stacksapi.Stack, error) {
	return s.rows, nil
}

type dispatchIntegration struct{ id kernel.ULID }

func (i dispatchIntegration) EventUse(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Use, bool, error) {
	return stacksapi.Use{Integration: i.id, Provider: "GITHUB_ENTERPRISE"}, true, nil
}

type dispatchSource struct {
	calls int
	files []string
}

func (s *dispatchSource) PrepareTag(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, event vcsapi.Event) (stacksapi.Commit, error) {
	if tx.In(ctx) {
		return stacksapi.Commit{}, errors.New("tag provider read under transaction")
	}
	return stacksapi.Commit{Hash: event.Head, Tag: event.Tag}, nil
}

func (s *dispatchSource) PreparePush(ctx context.Context, _ kernel.AccountID, _ kernel.ULID, _ string, event vcsapi.Event) (stacksapi.Commit, []string, error) {
	if tx.In(ctx) {
		return stacksapi.Commit{}, nil, errors.New("provider read under transaction")
	}
	s.calls++
	return stacksapi.Commit{Hash: event.Head}, s.files, nil
}

type refusedDispatchCompletion struct {
	*queue.Queue
	err error
}

func (q refusedDispatchCompletion) CompleteTx(context.Context, queue.Job) error { return q.err }

func TestAccPushDispatchAtomicFence(t *testing.T) {
	for _, fault := range []string{"none", "completion rollback", "stale lease"} {
		t.Run(fault, func(t *testing.T) {
			w := newWebhookWorld(t)
			source := &dispatchSource{}
			for number := range 1800 {
				source.files = append(source.files, fmt.Sprintf("project/very-long-path-for-a-real-prepared-file-%04d.tf", number))
			}
			repo := vcsapi.Repository{Namespace: "acme", Name: "infra"}
			events := []vcsapi.Event{
				{Type: "push", Repository: repo, Branch: "main", Before: strings.Repeat("a", 40), Head: strings.Repeat("b", 40)},
				{Type: "push", Repository: repo, Branch: "feature", Before: strings.Repeat("b", 40), Head: strings.Repeat("c", 40)},
				{Type: "tag", Repository: repo, Tag: "v1", Head: strings.Repeat("c", 40)},
			}
			deps := w.deps
			deps.Providers["GITHUB_ENTERPRISE"] = dispatchProvider{events: events}
			pushes := push.New(
				push.Deps{
					DB:           w.f.db,
					Store:        w.f.store,
					Stacks:       dispatchTargets{rows: []stacksapi.Stack{{ID: kernel.NewULID()}, {ID: kernel.NewULID()}}},
					Integrations: dispatchIntegration{w.id},
					Sources:      source,
					Jobs:         w.queue,
				},
			)
			deps.Dispatcher = pushes
			cause := errors.New("completion storage failed")
			if fault == "completion rollback" {
				deps.Jobs = refusedDispatchCompletion{Queue: w.queue, err: cause}
			}
			w.svc = intake.New(deps)
			w.receive(t, "refs", webhookBody(128))
			job := w.claim(t, w.queue, "parent")
			if fault == "stale lease" {
				if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 minute' WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := w.queue.Recover(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			err := w.svc.Processed(t.Context(), job)
			if fault == "none" && err != nil || fault == "completion rollback" && !errors.Is(err, cause) || fault == "stale lease" && !errors.Is(err, queue.ErrLeaseLost) {
				t.Fatal("wrong dispatch settlement", err)
			}
			delivery := w.delivery(t, "refs")
			prepared, err := w.f.store.Prepared(t.Context(), w.f.acct, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			var children, maxPayload int
			if err := w.f.db.QueryRow(t.Context(), `SELECT count(*),coalesce(max(octet_length(payload::text)),0) FROM jobs WHERE account_id=$1 AND kind=$2`, w.f.acct, push.Evaluate.Name).
				Scan(&children, &maxPayload); err != nil {
				t.Fatal(err)
			}
			if fault != "none" {
				if len(prepared) != 0 || children != 0 || delivery.State != intake.Received {
					t.Fatal("failed parent leaked prepared data, children or dispatched", len(prepared), children, delivery.State)
				}
				return
			}
			if len(prepared) != 3 || source.calls != 2 || children != 2 || maxPayload >= 64<<10 || delivery.State != intake.Dispatched {
				t.Fatal("dispatch lost refs, repeated per-stack work or exceeded queue bound", len(prepared), source.calls, children, maxPayload)
			}
			body, err := json.Marshal(prepared)
			if err != nil || len(body) <= 64<<10 || prepared[1].Event.Branch != "feature" || len(prepared[0].Files) != 1800 {
				t.Fatal("prepared facts were truncated to fit a job", len(body), err)
			}
			jobs, err := w.queue.Claim(t.Context(), queue.VCSInbox, []string{push.Evaluate.Name}, 2, time.Minute, "children")
			if err != nil || len(jobs) != 2 {
				t.Fatal("children are not independently claimable", jobs, err)
			}
			for _, child := range jobs {
				var ref push.Evaluation
				if json.Unmarshal(child.Payload, &ref) != nil || ref.Delivery != delivery.ID || ref.Account != w.f.acct || !ref.Deadline.Equal(delivery.Deadline) {
					t.Fatal("child lost immutable identity or inherited deadline")
				}
				if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.queue.CompleteTx(ctx, child) }); err != nil {
					t.Fatal(err)
				}
			}
			before := time.Now().Add(time.Hour)
			if _, _, err := w.store.Drop(t.Context(), &w.f.acct, &before, 10); err != nil {
				t.Fatal(err)
			}
			if remaining, err := w.f.store.Prepared(t.Context(), w.f.acct, delivery.ID); err != nil || len(remaining) != 0 {
				t.Fatal("retention left prepared event data", remaining, err)
			}
		})
	}
}

func (i dispatchIntegration) HeldEventUse(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (stacksapi.Use, bool, error) {
	if !tx.In(ctx) {
		return stacksapi.Use{}, false, queue.ErrNoTransaction
	}
	return i.EventUse(ctx, acct, id)
}
