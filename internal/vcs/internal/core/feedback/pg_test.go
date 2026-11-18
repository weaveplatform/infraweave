package feedback

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream())) }

func TestAccFeedbackEnqueueTransaction(t *testing.T) {
	pool := sqltest.Open(t).Primary()
	q := queue.New(pool)
	target := vcsapi.FeedbackTarget{Account: kernel.NewULID(), EventID: kernel.NewULID(), Key: "run:check", Job: ReportCheck.Name, Payload: []byte(`{"run":"id"}`)}
	holds := 0
	s := New(q, func(ctx context.Context, acct kernel.AccountID) error {
		if !tx.In(ctx) || acct.IsZero() {
			t.Error("owner hold escaped its transaction")
		}
		holds++
		return nil
	})
	if err := s.Enqueue(t.Context(), target); !errors.Is(err, queue.ErrNoTransaction) || holds != 0 {
		t.Fatalf("outside transaction = %v, %d holds", err, holds)
	}
	if err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		if err := s.Enqueue(ctx, target); err != nil {
			return err
		}
		return s.Enqueue(ctx, target)
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE kind=$1 AND account_id=$2", ReportCheck.Name, target.Account).Scan(&n); err != nil || n != 1 {
		t.Fatalf("job count %d: %v", n, err)
	}
	other := target
	other.Account = kernel.NewULID()
	if err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error { return s.Enqueue(ctx, other) }); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE kind=$1", ReportCheck.Name).Scan(&n); err != nil || n != 2 {
		t.Fatalf("account isolation: jobs %d, %v", n, err)
	}
	stop := errors.New("roll back caller")
	rolled := target
	rolled.Key = "rolled-back"
	if err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error {
		if err := s.Enqueue(ctx, rolled); err != nil {
			return err
		}
		return stop
	}); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE kind=$1", ReportCheck.Name).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rollback: jobs %d, %v", n, err)
	}
}

func TestAccFeedbackEnqueueRefusals(t *testing.T) {
	pool := sqltest.Open(t).Primary()
	q := queue.New(pool)
	target := vcsapi.FeedbackTarget{Account: kernel.NewULID(), EventID: kernel.NewULID(), Key: "run:check", Job: ReportCheck.Name, Payload: []byte(`{}`)}
	missing := errors.New("owner no longer exists")
	for name, tt := range map[string]struct {
		service *Service
		target  vcsapi.FeedbackTarget
		want    error
	}{
		"no owner fence":    {New(q, nil), target, ErrAccountHold},
		"missing owner":     {New(q, func(context.Context, kernel.AccountID) error { return missing }), target, missing},
		"invalid target":    {New(q, func(context.Context, kernel.AccountID) error { t.Error("held an invalid target"); return nil }), vcsapi.FeedbackTarget{}, vcsapi.ErrFeedbackTarget},
		"oversized payload": {New(q, func(context.Context, kernel.AccountID) error { return nil }), vcsapi.FeedbackTarget{Account: target.Account, EventID: target.EventID, Key: target.Key, Job: target.Job, Payload: []byte(`"` + strings.Repeat("x", queue.MaxPayload) + `"`)}, queue.ErrPayloadTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			err := tx.Do(t.Context(), pool, tx.Options{}, func(ctx context.Context) error { return tt.service.Enqueue(ctx, tt.target) })
			if !errors.Is(err, tt.want) {
				t.Fatalf("enqueue = %v, want %v", err, tt.want)
			}
		})
	}
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM jobs").Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused jobs %d: %v", n, err)
	}
}
