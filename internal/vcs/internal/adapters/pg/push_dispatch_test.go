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

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/webhooktest"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

type dispatchProvider struct {
	webhooktest.Provider
	events []vcsapi.Event
}

func (p dispatchProvider) Parse(http.Header, []byte) ([]vcsapi.Event, error) { return p.events, nil }

type dispatchTargets struct {
	stacksapi.Stacks
	rows []stacksapi.Stack
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
