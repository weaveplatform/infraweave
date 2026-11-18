package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
)

func TestAccCommentReadersFollowNativeRepositoryIdentity(t *testing.T) {
	canonical := "bcaad389-6eea-4c0f-9f17-b8c50d38699e"
	for _, stored := range []string{"7", canonical, "{" + strings.ToUpper(canonical) + "}"} {
		t.Run(stored, func(t *testing.T) {
			w := newCommentWorld(t)
			id := stored
			if strings.Contains(stored, "-") {
				id = canonical
			}
			// Keep the historical stored spelling: today's upkeep canonicalizes
			// new UUID writes, but an existing PR must retain its local identity.
			if _, err := w.f.db.Exec(t.Context(), `UPDATE pull_requests SET repository_id=$2 WHERE account_id=$1 AND id=$3`, w.f.acct, stored, w.request.ID); err != nil {
				t.Fatal(err)
			}
			targets := w.targets(t)
			if len(targets) != 1 {
				t.Fatal("unmapped request lost its stored-path fallback", targets)
			}
			var first feedback.Comment
			if err := json.Unmarshal(targets[0].Payload, &first); err != nil || first.Target.Mapped || first.Target.RepositoryID != id {
				t.Fatal("unmapped canonical identity", first.Target, err)
			}
			w.enqueue(t, targets)
			if _, err := w.f.db.Exec(t.Context(), `INSERT INTO vcs_repository_map(account_id,integration_id,repository_id,namespace,name,observed_at)
				VALUES($1,$2,$3,'renamed','platform',now())`, w.f.acct, w.request.Integration, id); err != nil {
				t.Fatal(err)
			}
			read, err := w.f.store.CommentPullRequests(t.Context(), w.f.acct, w.request.Integration, "renamed", "platform", w.request.HeadSHA)
			if err != nil || len(read) != 1 || read[0].ID != w.request.ID || read[0].RepositoryID != id || !read[0].Mapped {
				t.Fatal("renamed current-head lookup lost native identity", read, err)
			}
			if old, err := w.f.store.CommentPullRequests(t.Context(), w.f.acct, w.request.Integration, "acme", "infra", w.request.HeadSHA); err != nil || len(old) != 0 {
				t.Fatal("known map fell back to stale request coordinates", old, err)
			}
			pr, err := w.f.store.CommentPullRequest(t.Context(), w.f.acct, w.request.ID)
			if err != nil || pr != read[0] {
				t.Fatal("direct target projection differs from current heads", pr, err)
			}
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
				live, err := w.f.store.HoldComment(ctx, w.f.acct, first.ID)
				if err != nil || live.Target != pr {
					t.Fatal("queued binding missed live native path", live.Target, err)
				}
				// A later enqueue that observes the map fixes its known presence;
				// a stale unknown-path duplicate must never undo that knowledge.
				first.Target = pr
				if _, err := w.f.store.BindComment(ctx, first); err != nil {
					return err
				}
				first.Target.Mapped = false
				_, err = w.f.store.BindComment(ctx, first)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := w.f.db.Exec(t.Context(), `DELETE FROM vcs_repository_map WHERE account_id=$1 AND integration_id=$2`, w.f.acct, w.request.Integration); err != nil {
				t.Fatal(err)
			}
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
				_, err := w.f.store.HoldComment(ctx, w.f.acct, first.ID)
				return err
			}); !errors.Is(err, feedback.ErrNoComment) {
				t.Fatal("removed known identity fell back to stale routing", err)
			}
		})
	}
}
