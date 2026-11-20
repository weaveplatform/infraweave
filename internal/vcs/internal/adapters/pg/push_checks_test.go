package pg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func pushCheckInput(w *checkWorld, failed bool) port.PushFeedback {
	return port.PushFeedback{
		Account: w.b.Account, Delivery: kernel.NewULID(), Integration: w.b.Integration, Stack: w.b.Stack,
		Commit: w.b.Commit, Type: w.b.Type, Failed: failed, Messages: []string{"public policy decision"}, Policies: []string{"Required event"},
	}
}

func TestAccIgnoredPushChecksModesOptionsAndDedup(t *testing.T) {
	for _, mode := range []string{"INDIVIDUAL", "AGGREGATED", "ALL"} {
		for _, failed := range []bool{false, true} {
			for _, suppress := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/fail=%t/suppress=%t", mode, failed, suppress), func(t *testing.T) {
					w := newCheckWorld(t)
					delete(w.facts.runs, w.b.Run)
					if _, err := w.f.db.Exec(
						t.Context(),
						`UPDATE vcs_integrations SET vcs_checks=$2,individual_failures_only=$3,individual_skip_unchanged=$3 WHERE account_id=$1`,
						w.f.acct,
						mode,
						suppress,
					); err != nil {
						t.Fatal(err)
					}
					input := pushCheckInput(w, failed)
					write := func() error {
						return tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.ReportPush(ctx, input) })
					}
					if err := write(); err != nil {
						t.Fatal(err)
					}
					if w.checks.calls != 0 {
						t.Fatal("policy decision invoked a provider before commit")
					}
					want := 0
					if mode != "AGGREGATED" && (!suppress || failed) {
						want++
					}
					if mode != "INDIVIDUAL" {
						want += 3
					}
					var jobs int
					if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2`, w.f.acct, feedback.ReportCheck.Name).
						Scan(&jobs); err != nil ||
						jobs != want {
						t.Fatalf("durable jobs=%d want=%d error=%v", jobs, want, err)
					}
					for range want {
						w.report(t)
					}
					writes := w.fake.CheckWrites("acme", "infra")
					if len(writes) != want {
						t.Fatalf("native effects=%d want=%d", len(writes), want)
					}
					for _, check := range writes {
						if check.Name == "infraweave/infra" || check.Name == "infraweave/proposed" {
							state := "skipped"
							if failed {
								state = "failed"
							}
							if check.Status != state || !strings.Contains(check.Description, "public policy decision") || !strings.Contains(check.Description, "Required event") ||
								!strings.HasSuffix(check.URL, "/stacks/infra") {
								t.Fatalf("no-run check lost decision metadata: %+v", check)
							}
						}
					}
					if err := write(); err != nil {
						t.Fatal(err)
					}
					if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE account_id=$1 AND kind=$2`, w.f.acct, feedback.ReportCheck.Name).
						Scan(&jobs); err != nil ||
						jobs != want {
						t.Fatalf("same decision created duplicate durable jobs=%d error=%v", jobs, err)
					}
				})
			}
		}
	}
}

func TestAccIgnoredPushCheckRollbackRecoveryAndLatestFacts(t *testing.T) {
	w := newCheckWorld(t)
	delete(w.facts.runs, w.b.Run)
	input := pushCheckInput(w, true)
	if err := w.service.ReportPush(t.Context(), input); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatalf("unfenced check=%v", err)
	}
	abort := errors.New("roll back ignored decision")
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.service.ReportPush(ctx, input); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	for _, table := range []string{"vcs_check_bindings", "vcs_check_requests", "vcs_check_resources", "jobs"} {
		var n int
		if err := w.f.db.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE account_id=$1", w.f.acct).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s survived caller rollback: %d error=%v", table, n, err)
		}
	}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.ReportPush(ctx, input) }); err != nil {
		t.Fatal(err)
	}
	job := w.claim(t)
	w.fake.FailNextCheckCreate()
	if err := w.service.Checked(t.Context(), job); err == nil || w.checks.calls != 1 {
		t.Fatalf("lost successful response retried inside its transaction: %v", err)
	}
	if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.jobs.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.service.Checked(t.Context(), job); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("stale check claim=%v", err)
	}
	w.report(t)
	if w.checks.calls != 2 || len(w.fake.CheckWrites("acme", "infra")) != 1 {
		t.Fatal("recovered claim duplicated native no-run effect")
	}
	w.now = w.now.Add(time.Second)
	input.Delivery = kernel.NewULID()
	input.Failed = false
	input.Messages = []string{"latest no-run result"}
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.ReportPush(ctx, input) }); err != nil {
		t.Fatal(err)
	}
	w.report(t)
	facts, err := w.f.store.NoRunChecks(t.Context(), w.b, w.b.Type)
	if err != nil || len(facts) != 1 || facts[0].Push == nil || facts[0].Push.Failed || facts[0].Push.Message != "latest no-run result" {
		t.Fatalf("latest scoped no-run facts=%+v error=%v", facts, err)
	}
	for _, scope := range []string{"account", "integration", "repository", "commit", "type"} {
		b, kind := w.b, w.b.Type
		switch scope {
		case "account":
			b.Account = kernel.NewAccountID()
		case "integration":
			b.Integration = kernel.NewULID()
		case "repository":
			b.Repository = "other"
		case "commit":
			b.Commit = strings.Repeat("c", 40)
		case "type":
			kind = kernel.RunTypeTracked
		}
		got, err := w.f.store.NoRunChecks(t.Context(), b, kind)
		if err != nil || len(got) != 0 {
			t.Fatalf("foreign %s no-run facts=%+v error=%v", scope, got, err)
		}
	}
}
