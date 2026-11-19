package pg_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/test/deps"
)

func TestAccCommitCheckParentLockPrecedesDependentRows(t *testing.T) {
	for _, operation := range []string{"selection", "reporting"} {
		t.Run(operation, func(t *testing.T) {
			w := newCheckWorld(t)
			ctx := t.Context()
			native := lifecycle.Repository{Repository: vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}, ObservedAt: w.now}
			if err := w.f.store.PutRepositoryIdentity(ctx, w.f.acct, w.b.Integration, native); err != nil {
				t.Fatal(err)
			}
			w.b.RepositoryID = native.ID
			if operation == "reporting" {
				w.enqueue(t, w.b, 1)
			}
			connection, err := w.f.db.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Release()
			owner, err := connection.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = owner.Rollback(ctx) }()
			if _, err := owner.Exec(ctx, `SELECT id FROM vcs_integrations WHERE account_id=$1 AND id=$2 FOR UPDATE`, w.b.Account, w.b.Integration); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			if operation == "selection" {
				go func() {
					done <- tx.Do(ctx, w.f.db, tx.Options{NoRetry: true}, func(ctx context.Context) error {
						b, err := w.f.store.ResolveCheckRepository(ctx, w.b)
						if err != nil {
							return err
						}
						_, err = w.f.store.BindCheck(ctx, b)
						return err
					})
				}()
			} else {
				job := w.claim(t)
				go func() { done <- w.service.Checked(ctx, job) }()
			}
			waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			blocked := deps.Eventually(waitCtx, "check waits for the integration before taking dependent holds", func(ctx context.Context) (bool, error) {
				var waits bool
				err := w.f.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, connection.Conn().PgConn().PID()).Scan(&waits)
				return waits, err
			})
			_, err = owner.Exec(ctx, `SET LOCAL lock_timeout='500ms'`)
			if err == nil && blocked == nil {
				if operation == "selection" {
					_, err = owner.Exec(ctx, `UPDATE vcs_repository_map SET name='renamed' WHERE account_id=$1 AND integration_id=$2`, w.b.Account, w.b.Integration)
				} else {
					_, err = owner.Exec(ctx, `DELETE FROM vcs_integrations WHERE account_id=$1 AND id=$2`, w.b.Account, w.b.Integration)
				}
			}
			if err == nil && blocked == nil {
				err = owner.Commit(ctx)
			} else {
				_ = owner.Rollback(ctx)
			}
			workerErr := <-done
			if blocked != nil || err != nil || workerErr != nil {
				t.Fatal("integration-first update/delete was blocked by a dependent check hold", blocked, err, workerErr)
			}
			if operation == "selection" {
				got, err := w.f.store.CheckBinding(ctx, w.b.Account, w.b.Key)
				if err != nil || got.Repository != "renamed" {
					t.Fatal("selection retained pre-lock coordinates", fmt.Sprint(got), err)
				}
			} else if w.checks.calls != 0 {
				t.Fatal("deleted integration issued a native effect", w.checks.calls)
			}
		})
	}
}

func TestAccCommitCheckIntegrationHoldFencesDefaultChanges(t *testing.T) {
	w := newCheckWorld(t)
	ctx := t.Context()
	connection, err := w.f.db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	held, release, owner := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		owner <- tx.Do(ctx, connection, tx.Options{}, func(ctx context.Context) error {
			if found, err := w.f.store.HoldCheckIntegration(ctx, w.b.Account, w.b.Integration); err != nil || !found {
				return fmt.Errorf("integration hold: found=%v: %w", found, err)
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	changed := make(chan error, 1)
	go func() {
		_, err := w.f.db.Exec(ctx, `UPDATE vcs_integrations SET is_default=NOT is_default WHERE account_id=$1 AND id=$2`, w.b.Account, w.b.Integration)
		changed <- err
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	blocked := deps.Eventually(waitCtx, "effective default cannot change during first binding/reporting", func(ctx context.Context) (bool, error) {
		var waits bool
		err := w.f.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, connection.Conn().PgConn().PID()).Scan(&waits)
		return waits, err
	})
	close(release)
	if err := <-owner; err != nil {
		t.Fatal(err)
	}
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	if blocked != nil {
		t.Fatal(blocked)
	}
}
