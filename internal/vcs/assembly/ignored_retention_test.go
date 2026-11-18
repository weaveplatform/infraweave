package assembly

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/ignored"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

// The registered maintenance handler must preserve a cleanup refusal so its
// next attempt can finish both retention phases. Diagnostic history has a
// fixed horizon even when an operator retains delivery bodies for longer.
func TestAccIgnoredRetentionCronPropagatesCleanupFailure(t *testing.T) {
	for _, phase := range []string{"delivery", "ignored history"} {
		t.Run(phase, func(t *testing.T) {
			w := newWorld(t)
			now := time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC)
			settings := DefaultSettings()
			settings.Deliveries.Retention = 30 * 24 * time.Hour
			d, err := New(Deps{DB: w.db, Keys: w.keys, Settings: settings, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			oldDelivery, freshDelivery := kernel.NewULID(), kernel.NewULID()
			oldHistory, freshHistory := kernel.NewULID(), kernel.NewULID()
			stack := kernel.NewULID()
			if err := tx.Do(t.Context(), w.db, tx.Options{}, func(ctx context.Context) error {
				for _, row := range []struct {
					id kernel.ULID
					at time.Time
				}{
					{oldDelivery, now.Add(-40 * 24 * time.Hour)}, {freshDelivery, now.Add(-8 * 24 * time.Hour)},
				} {
					if _, err := tx.Q(ctx, w.db).Exec(ctx, `INSERT INTO vcs_deliveries
						(account_id,id,integration_id,provider,delivery_id,event,received_at,headers,signature,body,repository,state,deadline)
						VALUES($1,$2,$3,'GITLAB',$4,'push',$5,'{}','signature','{}','acme/infra','dispatched',$6)`,
						w.acct, row.id, kernel.NewULID(), row.id.String(), row.at, row.at.Add(time.Hour)); err != nil {
						return err
					}
				}
				for _, row := range []struct {
					id   kernel.ULID
					hash string
					at   time.Time
				}{
					{oldHistory, strings.Repeat("a", 40), now.Add(-ignored.Retention - time.Second)},
					{freshHistory, strings.Repeat("b", 40), now.Add(-2 * 24 * time.Hour)},
				} {
					if err := w.store.RecordIgnored(ctx, ignored.Record{
						Account:   w.acct,
						ID:        row.id,
						Stack:     stack,
						Commit:    stacksapi.Commit{Hash: row.hash, Message: "retained subject", Timestamp: row.at},
						Reason:    ignored.TagsIgnored,
						CreatedAt: row.at,
					}); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			table := "vcs_deliveries"
			if phase == "ignored history" {
				table = "vcs_ignored_triggers"
			}
			if _, err := w.db.Exec(t.Context(), `CREATE FUNCTION refuse_retention_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'cleanup refused' USING ERRCODE='P0001'; END $$;
				CREATE TRIGGER refuse_retention_cleanup BEFORE DELETE ON `+table+` FOR EACH ROW EXECUTE FUNCTION refuse_retention_cleanup()`); err != nil {
				t.Fatal(err)
			}
			var handler func(context.Context, queue.Job) error
			for _, task := range d.Cron() {
				if task.Kind.Name == intake.Prune.Name {
					if task.Every != time.Hour {
						t.Fatal("retention is not hourly", task.Every)
					}
					handler = task.Handler
				}
			}
			if handler == nil {
				t.Fatal("retention task is not registered")
			}
			err = handler(t.Context(), queue.Job{})
			cause, ok := errors.AsType[*pgconn.PgError](err)
			if !ok || cause.Code != "P0001" || cause.Message != "cleanup refused" {
				t.Fatal("cleanup error was hidden or lost its database identity", err)
			}
			assertRetained := func(wantOldDelivery, wantOldHistory bool) {
				var oldDeliveryKept, freshDeliveryKept, oldHistoryKept, freshHistoryKept bool
				if err := w.db.QueryRow(t.Context(), `SELECT
					EXISTS(SELECT 1 FROM vcs_deliveries WHERE account_id=$1 AND id=$2),
					EXISTS(SELECT 1 FROM vcs_deliveries WHERE account_id=$1 AND id=$3),
					EXISTS(SELECT 1 FROM vcs_ignored_triggers WHERE account_id=$1 AND id=$4),
					EXISTS(SELECT 1 FROM vcs_ignored_triggers WHERE account_id=$1 AND id=$5 AND commit->>'hash'=$6)`,
					w.acct, oldDelivery, freshDelivery, oldHistory, freshHistory, strings.Repeat("b", 40)).
					Scan(&oldDeliveryKept, &freshDeliveryKept, &oldHistoryKept, &freshHistoryKept); err != nil {
					t.Fatal(err)
				}
				if oldDeliveryKept != wantOldDelivery || oldHistoryKept != wantOldHistory || !freshDeliveryKept || !freshHistoryKept {
					t.Fatalf("retention old/fresh deliveries=%t/%t history=%t/%t", oldDeliveryKept, freshDeliveryKept, oldHistoryKept, freshHistoryKept)
				}
			}
			// A delivery failure prevents the later phase. An ignored-history
			// failure preserves that phase while earlier committed cleanup stays.
			assertRetained(phase == "delivery", true)
			if _, err := w.db.Exec(t.Context(), `DROP TRIGGER refuse_retention_cleanup ON `+table+`; DROP FUNCTION refuse_retention_cleanup()`); err != nil {
				t.Fatal(err)
			}
			if err := handler(t.Context(), queue.Job{}); err != nil {
				t.Fatal("cleanup did not recover after fault removal", err)
			}
			assertRetained(false, false)
			if err := handler(t.Context(), queue.Job{}); err != nil {
				t.Fatal("repeated cleanup is not idempotent", err)
			}
			assertRetained(false, false)
		})
	}
}
