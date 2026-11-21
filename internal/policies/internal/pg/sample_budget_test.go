package pg_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/pg"
)

func TestAccSampleBudgetsFenceReplicasAccountsHoursAndRollback(t *testing.T) {
	f := newFixture(t)
	cfg := f.db.Config()
	cfg.ConnConfig.RuntimeParams["timezone"] = "Pacific/Auckland"
	other, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(other.Close)
	stores := []*pg.Store{f.store, pg.New(other)}
	pools := []*pgxpool.Pool{f.db, other}
	account, second := f.principal.AccountID, kernel.NewAccountID()
	for bucket, limit := range map[string]int{"NOTIFICATION": 200, "GIT_PUSH": 200, "OTHER": 500} {
		if _, err := f.db.Exec(t.Context(), `INSERT INTO policy_sample_budgets(account_id,hour,bucket,used)
            VALUES($1,date_trunc('hour',statement_timestamp(),'UTC'),$2,$3),
            ($1,date_trunc('hour',statement_timestamp(),'UTC')-interval '1 hour',$2,$4)`, account, bucket, limit-1, limit); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []policyapi.Type{policyapi.Notification, policyapi.Push, policyapi.Plan} {
		var accepted, otherAccepted atomic.Int32
		var wg sync.WaitGroup
		failures := make(chan error, 64)
		for i := range 32 {
			for _, who := range []kernel.AccountID{account, second} {
				wg.Go(func() {
					typ := kind
					if kind == policyapi.Plan && i%2 == 0 {
						typ = policyapi.Task
					}
					err := tx.Do(t.Context(), pools[i%len(pools)], tx.Options{}, func(ctx context.Context) error {
						ok, err := stores[i%len(stores)].Reserve(ctx, who, typ)
						if err == nil && ok {
							tx.AfterCommit(ctx, func(context.Context) {
								if who == account {
									accepted.Add(1)
								} else {
									otherAccepted.Add(1)
								}
							})
						}
						return err
					})
					if err != nil {
						failures <- err
					}
				})
			}
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		if accepted.Load() != 1 || otherAccepted.Load() != 32 {
			t.Fatalf("%s last-slot race/account isolation: %d/%d", kind, accepted.Load(), otherAccepted.Load())
		}
	}
	var previousRows, currentRows int
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE hour=date_trunc('hour',statement_timestamp(),'UTC')-interval '1 hour'),
        count(*) FILTER(WHERE hour=date_trunc('hour',statement_timestamp(),'UTC')) FROM policy_sample_budgets WHERE account_id=ANY($1)`, []kernel.AccountID{account, second}).Scan(&previousRows, &currentRows); err != nil || previousRows != 3 || currentRows != 6 {
		t.Fatalf("UTC hour isolation: %d/%d %v", previousRows, currentRows, err)
	}
	rollbackAccount := kernel.NewAccountID()
	sentinel := errors.New("rollback reserved quota")
	if err := tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		if ok, err := f.store.Reserve(ctx, rollbackAccount, policyapi.Login); err != nil || !ok {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("rollback: %v", err)
	}
	var rows int
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM policy_sample_budgets WHERE account_id=$1`, rollbackAccount).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rolled-back budget persisted: %d %v", rows, err)
	}
	if _, err := f.store.Reserve(t.Context(), account, policyapi.Plan); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatalf("unfenced quota reservation: %v", err)
	}
}
