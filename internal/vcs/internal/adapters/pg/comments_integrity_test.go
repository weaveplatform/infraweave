package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestAccCommentMutationsRequireCompletionTransaction(t *testing.T) {
	w := newCommentWorld(t)
	targets := w.targets(t)
	if len(targets) != 1 {
		t.Fatal("missing eligible comment", targets)
	}
	var c feedback.Comment
	if err := json.Unmarshal(targets[0].Payload, &c); err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.store.BindComment(t.Context(), c); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatal("binding without a completion transaction", err)
	}
	var count int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_pr_comments`).Scan(&count); err != nil || count != 0 {
		t.Fatal("untransactional binding persisted a mapping", count, err)
	}
	w.enqueue(t, targets)
	j := w.commentJob(t)
	if err := tx.Do(
		t.Context(),
		w.f.db,
		tx.Options{},
		func(ctx context.Context) error { return w.service.Commented(ctx, j) },
	); !errors.Is(err, feedback.ErrCommentReport) ||
		!strings.Contains(err.Error(), "inherited transaction") {
		t.Fatal("native effect inherited the caller's retrying transaction", err)
	}
	var before, after string
	if err := w.f.db.QueryRow(t.Context(), `SELECT to_jsonb(c)::text FROM vcs_pr_comments c WHERE account_id=$1 AND id=$2`, c.Account, c.ID).
		Scan(&before); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"hold":       func() error { _, err := w.f.store.HoldComment(t.Context(), c.Account, c.ID); return err }(),
		"record":     w.f.store.RecordComment(t.Context(), c, port.CommentReference{ID: "uncommitted-native-comment"}),
		"superseded": func() error { _, err := w.f.store.SupersededComments(t.Context(), c); return err }(),
		"minimized":  w.f.store.MinimizedComment(t.Context(), c.Account, c.ID),
	} {
		if !errors.Is(err, queue.ErrNoTransaction) {
			t.Errorf("%s without a completion transaction: %v", name, err)
		}
	}
	if err := w.f.db.QueryRow(t.Context(), `SELECT to_jsonb(c)::text FROM vcs_pr_comments c WHERE account_id=$1 AND id=$2`, c.Account, c.ID).
		Scan(&after); err != nil || after != before {
		t.Fatal("untransactional mutation changed the durable mapping", before, after, err)
	}
	if err := w.service.Commented(t.Context(), j); err != nil {
		t.Fatal("rejected mutations prevented the genuine completion", err)
	}
	if writes := w.fake.CommentWrites("acme", "infra", "7"); len(writes) != 1 {
		t.Fatal("genuine completion did not report exactly once", writes)
	}
}

func TestAccCommentInvalidEnvelopeDiesWithoutProviderEffects(t *testing.T) {
	for _, scenario := range []string{"malformed envelope", "foreign account"} {
		t.Run(scenario, func(t *testing.T) {
			w := newCommentWorld(t)
			targets := w.targets(t)
			if len(targets) != 1 {
				t.Fatal("missing eligible comment", targets)
			}
			var c feedback.Comment
			if err := json.Unmarshal(targets[0].Payload, &c); err != nil {
				t.Fatal(err)
			}
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
				_, err := w.f.store.BindComment(ctx, c)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var payload any = "invalid envelope"
			if scenario == "foreign account" {
				payload = map[string]any{"account": kernel.NewAccountID(), "id": c.ID}
			}
			id, err := w.jobs.Enqueue(t.Context(), feedback.ReportComment, payload, queue.Account(w.f.acct.ULID))
			if err != nil {
				t.Fatal(err)
			}
			registry := queue.NewRegistry()
			if err := registry.Register(feedback.ReportComment, w.service.Commented, queue.HandlerOptions{}); err != nil {
				t.Fatal(err)
			}
			cfg := queue.Defaults()
			cfg.PollInterval = 10 * time.Millisecond
			cfg.Queues.Deliveries.Workers = 1
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			done := make(chan struct{})
			workers := queue.NewWorkers(w.jobs, registry, cfg, queue.AllowAll{}, nil,
				slog.New(slog.NewTextHandler(io.Discard, nil)), "invalid-comment-contract")
			go func() { defer close(done); workers.Run(ctx) }()
			t.Cleanup(func() { cancel(); <-done })
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				dead, err := w.jobs.ReadDead(ctx, id)
				if err == nil {
					if dead.Attempt != 1 || dead.LastError != feedback.ErrCommentReport.Error() || dead.Account != w.f.acct.ULID {
						t.Fatal("invalid envelope did not fail permanently and safely", dead)
					}
					break
				}
				if !errors.Is(err, queue.ErrNotDead) {
					t.Fatal(err)
				}
				select {
				case <-ctx.Done():
					t.Fatal("invalid envelope was retried instead of becoming dead", ctx.Err())
				case <-tick.C:
				}
			}
			if writes := w.fake.CommentWrites("acme", "infra", "7"); len(writes) != 0 {
				t.Fatal("invalid envelope reached the provider", writes)
			}
			var reported int64
			if err := w.f.db.QueryRow(t.Context(), `SELECT reported_revision FROM vcs_pr_comments WHERE account_id=$1 AND id=$2`, c.Account, c.ID).
				Scan(&reported); err != nil || reported != 0 {
				t.Fatal("invalid envelope acknowledged the genuine mapping", reported, err)
			}
		})
	}
}

func TestAccCommentMalformedDurableMetadataPreservesClaim(t *testing.T) {
	for _, field := range []string{"binding", "reference"} {
		t.Run(field, func(t *testing.T) {
			w := newCommentWorld(t)
			w.enqueue(t, w.targets(t))
			j := w.commentJob(t)
			var id kernel.ULID
			var binding, reference []byte
			if err := w.f.db.QueryRow(t.Context(), `SELECT id,binding,reference FROM vcs_pr_comments WHERE account_id=$1`, w.f.acct).
				Scan(&id, &binding, &reference); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE vcs_pr_comments SET binding=jsonb_set(binding,'{planVersion}','"invalid"'::jsonb) WHERE account_id=$1 AND id=$2`
			wantField := "planVersion"
			if field == "reference" {
				query = `UPDATE vcs_pr_comments SET reference='{"id":[]}'::jsonb WHERE account_id=$1 AND id=$2`
				wantField = "id"
			}
			if _, err := w.f.db.Exec(t.Context(), query, w.f.acct, id); err != nil {
				t.Fatal(err)
			}
			err := w.service.Commented(t.Context(), j)
			cause, ok := errors.AsType[*json.UnmarshalTypeError](err)
			if !ok || cause.Field != wantField || errors.Is(err, feedback.ErrNoComment) {
				t.Fatal("malformed durable metadata was mistaken for a missing target", err)
			}
			if writes := w.fake.CommentWrites("acme", "infra", "7"); len(writes) != 0 {
				t.Fatal("malformed metadata reached the provider", writes)
			}
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.jobs.HoldTx(ctx, j) }); err != nil {
				t.Fatal("failed metadata read acknowledged or invalidated the claim", err)
			}
			if _, err := w.f.db.Exec(t.Context(), `UPDATE vcs_pr_comments SET binding=$3,reference=$4 WHERE account_id=$1 AND id=$2`,
				w.f.acct, id, binding, reference); err != nil {
				t.Fatal(err)
			}
			if err := w.service.Commented(t.Context(), j); err != nil {
				t.Fatal("restored metadata could not recover the same claim", err)
			}
			if writes := w.fake.CommentWrites("acme", "infra", "7"); len(writes) != 1 {
				t.Fatal("recovery did not post exactly one native comment", writes)
			}
			if err := w.jobs.Complete(t.Context(), j); !errors.Is(err, queue.ErrLeaseLost) {
				t.Fatal("native reference and claim did not complete together", err)
			}
		})
	}
}

func TestAccCommentOrphanedTargetsSettleWithoutProviderEffects(t *testing.T) {
	for _, parent := range []string{"mapping", "integration", "pull request"} {
		t.Run(parent, func(t *testing.T) {
			w := newCommentWorld(t)
			w.enqueue(t, w.targets(t))
			j := w.commentJob(t)
			var id kernel.ULID
			if err := w.f.db.QueryRow(t.Context(), `SELECT id FROM vcs_pr_comments WHERE account_id=$1`, w.f.acct).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
				_, err := w.f.store.HoldComment(ctx, kernel.NewAccountID(), id)
				return err
			}); !errors.Is(err, feedback.ErrNoComment) {
				t.Fatal("foreign account held an existing comment identity", err)
			}
			query := `DELETE FROM vcs_pr_comments WHERE account_id=$1 AND id=$2`
			removed := id
			switch parent {
			case "integration":
				query, removed = `DELETE FROM vcs_integrations WHERE account_id=$1 AND id=$2`, w.request.Integration
			case "pull request":
				query, removed = `DELETE FROM pull_requests WHERE account_id=$1 AND id=$2`, w.request.ID
			}
			if _, err := w.f.db.Exec(t.Context(), query, w.f.acct, removed); err != nil {
				t.Fatal(err)
			}
			if err := w.service.Commented(t.Context(), j); err != nil {
				t.Fatal("deleted metadata could not settle its stale job", err)
			}
			if writes := w.fake.CommentWrites("acme", "infra", "7"); len(writes) != 0 {
				t.Fatal("orphaned metadata reached the provider", writes)
			}
			if err := w.jobs.Complete(t.Context(), j); !errors.Is(err, queue.ErrLeaseLost) {
				t.Fatal("stale job remained live after settling", err)
			}
		})
	}
}
