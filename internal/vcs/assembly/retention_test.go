package assembly

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/cron"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/pullrequest"
)

func TestAccRetentionCronPropagatesFailureAndRetries(t *testing.T) {
	for _, fault := range []string{"delivery", "request"} {
		t.Run(fault, func(t *testing.T) {
			w := newWorld(t)
			stack := w.integration("GITLAB", port.Settings{}, []byte("credential"), "acme", "infra")
			integration := *w.stacks.settings[stack].VCSIntegration
			now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
			old, fresh := now.Add(-48*time.Hour), now.Add(-time.Hour)
			settings := DefaultSettings()
			settings.Deliveries.Retention = 24 * time.Hour
			domain, err := New(Deps{DB: w.db, Keys: w.keys, Settings: settings, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			var retention cron.Task
			for _, task := range domain.Cron() {
				if task.Kind.Name == intake.Prune.Name {
					retention = task
				}
			}
			if retention.Handler == nil {
				t.Fatal("the domain exposes no delivery/request retention task")
			}
			expiredDelivery, freshDelivery := kernel.NewULID(), kernel.NewULID()
			for _, delivery := range []struct {
				id kernel.ULID
				at time.Time
			}{{expiredDelivery, old}, {freshDelivery, fresh}} {
				if _, err := w.db.Exec(t.Context(), `INSERT INTO vcs_deliveries
					(account_id,id,integration_id,provider,delivery_id,event,received_at,headers,signature,body,repository,state,deadline)
					VALUES($1,$2,$3,'GITLAB',$4,'push',$5,'{}','signature','{}','native-repo','dispatched',$5)`,
					w.acct, delivery.id, integration, delivery.id.String(), delivery.at); err != nil {
					t.Fatal(err)
				}
			}
			requests := []pullrequest.Request{
				{ID: kernel.NewULID(), State: pullrequest.Closed, UpdatedAt: old},
				{ID: kernel.NewULID(), State: pullrequest.Open, UpdatedAt: old},
				{ID: kernel.NewULID(), State: pullrequest.Closed, UpdatedAt: fresh},
			}
			if err := tx.Do(t.Context(), w.db, tx.Options{}, func(ctx context.Context) error {
				for i, request := range requests {
					request.Account, request.Integration = w.acct, integration
					request.RepositoryID, request.Namespace, request.Repository = "native-repo", "acme", "infra"
					request.Number = strconv.Itoa(i + 1)
					request.BaseBranch, request.HeadBranch = "main", "feature"
					request.CreatedAt = old.Add(-time.Hour)
					for _, digit := range []string{"a", "b"} {
						request.HeadSHA = strings.Repeat(digit, 40)
						if err := w.store.Upsert(ctx, request); err != nil {
							return err
						}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			assertRows := func(wantDelivery, wantRequest bool) {
				t.Helper()
				var delivery, request, keptDelivery, keptOpen, keptClosed bool
				var expiredHeads, keptHeads int
				err := w.db.QueryRow(t.Context(), `SELECT
					EXISTS(SELECT 1 FROM vcs_deliveries WHERE account_id=$1 AND id=$2),
					EXISTS(SELECT 1 FROM vcs_deliveries WHERE account_id=$1 AND id=$3),
					EXISTS(SELECT 1 FROM pull_requests WHERE account_id=$1 AND id=$4),
					EXISTS(SELECT 1 FROM pull_requests WHERE account_id=$1 AND id=$5 AND state='open' AND updated_at=$7),
					EXISTS(SELECT 1 FROM pull_requests WHERE account_id=$1 AND id=$6 AND state='closed' AND updated_at=$8),
					(SELECT count(*) FROM pull_request_heads WHERE account_id=$1 AND request_id=$4),
					(SELECT count(*) FROM pull_request_heads WHERE account_id=$1 AND request_id IN ($5,$6))`,
					w.acct, expiredDelivery, freshDelivery, requests[0].ID, requests[1].ID, requests[2].ID, old, fresh).
					Scan(&delivery, &keptDelivery, &request, &keptOpen, &keptClosed, &expiredHeads, &keptHeads)
				wantHeads := 0
				if wantRequest {
					wantHeads = 2
				}
				if err != nil || delivery != wantDelivery || request != wantRequest || !keptDelivery || !keptOpen || !keptClosed || expiredHeads != wantHeads || keptHeads != 4 {
					t.Fatal("retention lost its failure boundary, retained expired history or pruned live history",
						delivery, request, keptDelivery, keptOpen, keptClosed, expiredHeads, keptHeads, err)
				}
			}
			assertRows(true, true)
			table := "vcs_deliveries"
			if fault == "request" {
				table = "pull_requests"
			}
			if _, err := w.db.Exec(t.Context(), `CREATE FUNCTION refuse_retention() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'retention refused' USING ERRCODE='P0001'; END $$;
				CREATE TRIGGER refuse_retention BEFORE DELETE ON `+table+` FOR EACH ROW EXECUTE FUNCTION refuse_retention()`); err != nil {
				t.Fatal(err)
			}
			err = retention.Handler(t.Context(), queue.Job{})
			if cause, ok := errors.AsType[*pgconn.PgError](err); !ok || cause.Code != "P0001" || cause.Message != "retention refused" {
				t.Fatal("composed cron lost the actual cleanup refusal", err)
			}
			// Delivery cleanup commits before request pruning. Its failure leaves
			// both phases untouched; a request failure preserves only that phase.
			assertRows(fault == "delivery", true)
			if _, err := w.db.Exec(t.Context(), `DROP TRIGGER refuse_retention ON `+table+`; DROP FUNCTION refuse_retention()`); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := retention.Handler(t.Context(), queue.Job{}); err != nil {
					t.Fatal("retention cannot recover after clearing the storage fault", err)
				}
				assertRows(false, false)
			}
		})
	}
}
