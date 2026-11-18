package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type changingCommentStore struct {
	feedback.CommentStore
	before func() error
	rows   int
}

func (s *changingCommentStore) CommentPullRequests(
	ctx context.Context,
	acct kernel.AccountID,
	in kernel.ULID,
	namespace, repository, sha string,
) ([]feedback.CommentPullRequest, error) {
	if s.before != nil {
		change := s.before
		s.before = nil
		if err := change(); err != nil {
			return nil, err
		}
	}
	rows, err := s.CommentStore.CommentPullRequests(ctx, acct, in, namespace, repository, sha)
	s.rows = len(rows)
	return rows, err
}

func TestAccCommentSelectionRetriesChangedRepositoryAndDefault(t *testing.T) {
	for _, scenario := range []string{"repository rename", "default binding swap"} {
		t.Run(scenario, func(t *testing.T) {
			w := newCommentWorld(t)
			store := &changingCommentStore{CommentStore: w.f.store}
			w.service.WithComments(feedback.CommentDeps{
				DB: w.f.db, Store: store, Histories: w.facts, Stacks: w.facts, Integrations: w.f.svc,
				Providers: map[string]port.Comments{"GITLAB": w.provider.Provider}, Logs: w.facts, Public: "https://infra.example",
			})
			want := w.request
			if scenario == "repository rename" {
				store.before = func() error {
					// The owned Settings double exposes the post-commit stack
					// snapshot; the actual PG lookup must observe the renamed map.
					_, err := w.f.db.Exec(t.Context(), `INSERT INTO vcs_repository_map(account_id,integration_id,repository_id,namespace,name,observed_at)
						VALUES($1,$2,$3,'renamed','platform',now())`, w.f.acct, w.request.Integration, w.request.RepositoryID)
					if err == nil {
						w.facts.stack.Namespace, w.facts.stack.Repository = "renamed", "platform"
						w.facts.stack.Version++
					}
					return err
				}
				want.Namespace, want.Repository = "renamed", "platform"
			} else {
				w.facts.stack.VCSIntegration = nil
				if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_integrations SET is_default=true WHERE account_id=$1 AND id=$2`, w.f.acct, w.request.Integration); err != nil {
					t.Fatal(err)
				}
				second := kernel.NewULID()
				if err := w.f.store.Create(t.Context(), integration.Integration{
					Account: w.f.acct, ID: second, Provider: "GITLAB", Space: w.f.acct.ULID, Slug: "replacement", Name: "Replacement",
					Labels: []string{}, Checks: integration.Checks{Mode: "INDIVIDUAL"}, Settings: port.Settings{APIHost: w.binding.NativeHost},
					CreatedAt: time.Now(), UpdatedAt: time.Now(), Version: 1,
				}, integration.Secrets{}); err != nil {
					t.Fatal(err)
				}
				want.ID, want.Integration = kernel.NewULID(), second
				if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.f.store.Upsert(ctx, want) }); err != nil {
					t.Fatal(err)
				}
				store.before = func() error {
					return tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
						if _, err := tx.Q(ctx, w.f.db).
							Exec(ctx, `UPDATE vcs_integrations SET is_default=false WHERE account_id=$1 AND id=$2`, w.f.acct, w.request.Integration); err != nil {
							return err
						}
						_, err := tx.Q(ctx, w.f.db).Exec(ctx, `UPDATE vcs_integrations SET is_default=true WHERE account_id=$1 AND id=$2`, w.f.acct, second)
						return err
					})
				}
			}
			h := w.facts.history
			body, err := json.Marshal(runsapi.Updated{
				State: kernel.RunStateUnconfirmed,
				Run:   runsapi.UpdatedRun{ID: h.ID, Type: h.Type, State: kernel.RunStateUnconfirmed, Commit: runsapi.UpdatedCommit{Hash: h.CommitHash}},
				Stack: runsapi.UpdatedStack{ID: "infra", ULID: h.Stack},
			})
			if err != nil {
				t.Fatal(err)
			}
			event := outbox.Event{
				Account: w.f.acct.ULID, ID: kernel.NewULID(), Kind: runsapi.UpdatedKind, SchemaVersion: runsapi.UpdatedVersion,
				Subject: outbox.Subject{Kind: "run", ID: h.ID.String()}, Sequence: 3, Document: body,
			}
			if targets, err := w.service.Targets(t.Context(), event); !errors.Is(err, kernel.ErrUnavailable) || len(targets) != 0 {
				t.Fatalf("mixed selection lost retry: targets=%v err=%v", targets, err)
			}
			if scenario == "repository rename" && store.rows != 0 || scenario == "default binding swap" && store.rows != 1 {
				t.Fatalf("race did not exercise the real mixed read: rows=%d", store.rows)
			}
			var count int
			if err := w.f.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM vcs_pr_comments)+(SELECT count(*) FROM jobs WHERE kind=$1)`, feedback.ReportComment.Name).
				Scan(&count); err != nil ||
				count != 0 {
				t.Fatalf("failed selection persisted work: count=%d err=%v", count, err)
			}
			targets, err := w.service.Targets(t.Context(), event)
			if err != nil || len(targets) != 1 {
				t.Fatalf("same event stable retry lost target: targets=%v err=%v", targets, err)
			}
			var selected feedback.Comment
			if err := json.Unmarshal(targets[0].Payload, &selected); err != nil || selected.Target.ID != want.ID || selected.Target.Integration != want.Integration ||
				selected.Target.Namespace != want.Namespace || selected.Target.Repository != want.Repository {
				t.Fatalf("retry selected stale routing: selected=%+v want=%+v err=%v", selected.Target, want, err)
			}
			w.enqueue(t, targets)
			if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE kind=$1`, feedback.ReportComment.Name).Scan(&count); err != nil || count != 1 {
				t.Fatalf("stable retry did not enqueue exactly once: count=%d err=%v", count, err)
			}
			if _, err := w.f.db.Exec(t.Context(), `UPDATE pull_requests SET head_sha=$3 WHERE account_id=$1 AND id=$2`, w.f.acct, want.ID, strings.Repeat("b", 40)); err != nil {
				t.Fatal(err)
			}
			if targets, err := w.service.Targets(t.Context(), event); err != nil || len(targets) != 0 {
				t.Fatalf("stable zero-head result spuriously retried: targets=%v err=%v", targets, err)
			}
		})
	}
}
