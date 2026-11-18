package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/feedback"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type checkFacts struct {
	stacksapi.Stacks
	account  kernel.AccountID
	settings map[kernel.ULID]stacksapi.Settings
	runs     map[kernel.ULID]runsapi.CheckRun
}

func (f *checkFacts) Run(_ context.Context, account kernel.AccountID, id kernel.ULID) (runsapi.CheckRun, error) {
	r, ok := f.runs[id]
	if account != f.account || !ok {
		return r, runsapi.ErrCheckRunNotFound
	}
	return r, nil
}

func (f *checkFacts) OnCommit(_ context.Context, account kernel.AccountID, ids []kernel.ULID, sha string) ([]runsapi.CheckRun, error) {
	out := []runsapi.CheckRun{}
	if account != f.account {
		return out, nil
	}
	for _, r := range f.runs {
		if r.CommitSHA == sha && slices.Contains(ids, r.Stack) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *checkFacts) Hold(_ context.Context, account kernel.AccountID, id kernel.ULID) error {
	if _, ok := f.settings[id]; !ok || account != f.account {
		return stacksapi.ErrNotFound
	}
	return nil
}

func (f *checkFacts) Settings(_ context.Context, account kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error) {
	s, ok := f.settings[id]
	if !ok || account != f.account {
		return s, stacksapi.ErrNotFound
	}
	return s, nil
}

func (f *checkFacts) ByVCSRepository(_ context.Context, account kernel.AccountID, u stacksapi.Use, ns, repo string) ([]stacksapi.Stack, error) {
	out := []stacksapi.Stack{}
	for _, s := range f.settings {
		if account == f.account && s.Namespace == ns && s.Repository == repo && s.VCSIntegration != nil && *s.VCSIntegration == u.Integration {
			out = append(out, stacksapi.Stack{ID: s.ID, Slug: s.Slug})
		}
	}
	return out, nil
}

type countedCheck struct {
	feedback.CheckProviders
	calls int
	cause error
}

func (p *countedCheck) Set(ctx context.Context, r port.Repository, sha string, report port.CheckReport) (string, error) {
	p.calls++
	if p.cause != nil {
		return "", p.cause
	}
	return p.CheckProviders.Set(ctx, r, sha, report) //nolint:wrapcheck // counted native boundary
}

type checkWorld struct {
	*deploymentWorld
	service *feedback.Service
	b       feedback.CheckBinding
	facts   *checkFacts
	checks  *countedCheck
	now     time.Time
	logs    bytes.Buffer
}

func newCheckWorld(t *testing.T) *checkWorld {
	t.Helper()
	base := newDeploymentWorld(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	host, err := base.provider.CheckHost(port.Settings{APIHost: base.binding.NativeHost})
	if err != nil {
		t.Fatal(err)
	}
	b := feedback.CheckBinding{
		Account:     base.f.acct,
		Key:         "run:" + base.binding.Run.ID.String(),
		Run:         base.binding.Run.ID,
		Stack:       base.binding.Stack.ID,
		Slug:        "infra",
		Integration: base.binding.Integration,
		Provider:    "GITLAB",
		NativeHost:  host,
		Namespace:   "acme",
		Repository:  "infra",
		Commit:      base.binding.Run.Commit,
		Ref:         "main",
		Type:        kernel.RunTypeProposed,
		Policy:      port.CheckPolicy{Mode: "INDIVIDUAL"},
		CreatedAt:   now,
	}
	facts := &checkFacts{
		account: b.Account,
		settings: map[kernel.ULID]stacksapi.Settings{
			b.Stack: {ID: b.Stack, Slug: b.Slug, Provider: b.Provider, Namespace: b.Namespace, Repository: b.Repository, VCSIntegration: &b.Integration},
		},
		runs: map[kernel.ULID]runsapi.CheckRun{
			b.Run: {ID: b.Run, Stack: b.Stack, Type: b.Type, State: kernel.RunStateQueued, CommitSHA: b.Commit, Sequence: 1, CreatedAt: b.CreatedAt},
		},
	}
	w := &checkWorld{deploymentWorld: base, b: b, facts: facts, now: now, checks: &countedCheck{CheckProviders: base.provider.Provider}}
	w.service = feedback.New(base.jobs, func(ctx context.Context, account kernel.AccountID) error {
		return base.f.deps.Spaces.Hold(ctx, account, account.ULID)
	}).WithChecks(feedback.CheckDeps{DB: base.f.db, Store: base.f.store, Facts: facts, Stacks: facts, Integrations: base.f.svc, Providers: map[string]feedback.CheckProviders{"GITLAB": w.checks}, Public: "https://infra.example", Now: func() time.Time { return w.now }, Log: slog.New(slog.NewJSONHandler(&w.logs, nil))})
	return w
}

func (w *checkWorld) target(t *testing.T, b feedback.CheckBinding, name string, aggregate bool, sequence int64) vcsapi.FeedbackTarget {
	t.Helper()
	body, err := json.Marshal(feedback.CheckRequest{Binding: b, Name: name, Aggregate: aggregate, Sequence: sequence})
	if err != nil {
		t.Fatal(err)
	}
	return vcsapi.FeedbackTarget{Account: b.Account.ULID, EventID: kernel.NewULID(), Key: "check:" + b.Key + ":" + name, Job: feedback.ReportCheck.Name, Payload: body}
}

func (w *checkWorld) enqueue(t *testing.T, b feedback.CheckBinding, sequence int64) {
	t.Helper()
	target := w.target(t, b, "infraweave/"+b.Slug, false, sequence)
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.Enqueue(ctx, target) }); err != nil {
		t.Fatal(err)
	}
}

func (w *checkWorld) claim(t *testing.T) queue.Job {
	t.Helper()
	jobs, err := w.jobs.Claim(t.Context(), queue.Deliveries, []string{feedback.ReportCheck.Name}, 1, time.Minute, "commit-checks")
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	return jobs[0]
}

func (w *checkWorld) report(t *testing.T) {
	t.Helper()
	j := w.claim(t)
	if err := w.service.Checked(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if err := w.jobs.Complete(t.Context(), j); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatal("check acknowledgement did not commit with cursor", err)
	}
}

func (w *checkWorld) state(id kernel.ULID, state kernel.RunState, seq int64) {
	r := w.facts.runs[id]
	r.State = state
	r.Sequence = seq
	if state == kernel.RunStateFinished {
		r.Delta = &kernel.PlanDelta{Add: 1}
	}
	w.facts.runs[id] = r
}

func TestAccCommitCheckCoalescingAndOwnership(t *testing.T) {
	w := newCheckWorld(t)
	w.enqueue(t, w.b, 1)
	w.state(w.b.Run, kernel.RunStatePlanning, 3)
	w.enqueue(t, w.b, 3)
	w.enqueue(t, w.b, 2)
	j := w.claim(t)
	var r feedback.CheckRequest
	if json.Unmarshal(j.Payload, &r) != nil || r.Sequence != 3 {
		t.Fatal("late event replaced newer pending state", r)
	}
	if err := w.service.Checked(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	w.state(w.b.Run, kernel.RunStateFinished, 5)
	w.enqueue(t, w.b, 5)
	w.report(t)
	writes := w.fake.CheckWrites("acme", "infra")
	if len(writes) != 2 || writes[0].Status != "running" || writes[1].Status != "success" || writes[0].ID != writes[1].ID {
		t.Fatal("native context was not advanced in place", writes)
	}
	older, newer := w.b, w.b
	older.Run = kernel.NewULID()
	older.Key = "run:" + older.Run.String()
	older.CreatedAt = older.CreatedAt.Add(time.Hour)
	newer.Run = kernel.NewULID()
	newer.Key = "run:" + newer.Run.String()
	newer.CreatedAt = older.CreatedAt.Add(time.Minute)
	for _, b := range []feedback.CheckBinding{older, newer} {
		w.facts.runs[b.Run] = runsapi.CheckRun{ID: b.Run, Stack: b.Stack, Type: b.Type, CommitSHA: b.Commit, State: kernel.RunStateFinished, Sequence: 7, CreatedAt: b.CreatedAt}
	}
	w.enqueue(t, older, 7)
	w.enqueue(t, newer, 7)
	before := w.checks.calls
	w.report(t)
	if w.checks.calls != before {
		t.Fatal("older run posted while newer owner was pending")
	}
	w.report(t)
	w.state(older.Run, kernel.RunStateFailed, 9)
	w.enqueue(t, older, 9)
	w.report(t)
	writes = w.fake.CheckWrites("acme", "infra")
	if writes[len(writes)-1].Status != "success" {
		t.Fatal("older run overwrote newer owner", writes)
	}
	var resources int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_check_resources WHERE account_id=$1`, w.f.acct).Scan(&resources); err != nil || resources != 1 {
		t.Fatal("shared resource split by run", resources, err)
	}
}

func TestAccCommitCheckClaimRecoveryAndNoRetry(t *testing.T) {
	for _, mode := range []string{"lost response", "acknowledgement"} {
		t.Run(mode, func(t *testing.T) {
			w := newCheckWorld(t)
			w.state(w.b.Run, kernel.RunStatePlanning, 3)
			w.enqueue(t, w.b, 3)
			old := w.claim(t)
			if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := w.jobs.Recover(t.Context()); err != nil {
				t.Fatal(err)
			}
			current := w.claim(t)
			if err := w.service.Checked(t.Context(), old); !errors.Is(err, queue.ErrLeaseLost) || w.checks.calls != 0 {
				t.Fatal("stale generation issued native effect", err, w.checks.calls)
			}
			if mode == "lost response" {
				w.fake.FailNextCheckCreate()
			} else {
				if _, err := w.f.db.Exec(
					t.Context(),
					`CREATE FUNCTION refuse_check_ack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'ack unavailable' USING ERRCODE='40001'; END $$; CREATE TRIGGER refuse_check_ack BEFORE UPDATE OF sequence ON vcs_check_resources FOR EACH ROW EXECUTE FUNCTION refuse_check_ack()`,
				); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.service.Checked(t.Context(), current); err == nil || w.checks.calls != 1 {
				t.Fatal("provider was retried inside its transaction", err, w.checks.calls)
			}
			var sequence int64
			var state string
			if err := w.f.db.QueryRow(t.Context(), `SELECT sequence FROM vcs_check_resources WHERE account_id=$1`, w.f.acct).Scan(&sequence); err != nil || sequence != 0 {
				t.Fatal(sequence, err)
			}
			if err := w.f.db.QueryRow(t.Context(), `SELECT state FROM jobs WHERE id=$1`, current.ID).Scan(&state); err != nil || state != "running" {
				t.Fatal("failed effect committed acknowledgement", state, err)
			}
			if mode == "acknowledgement" {
				if _, err := w.f.db.Exec(t.Context(), `DROP TRIGGER refuse_check_ack ON vcs_check_resources; DROP FUNCTION refuse_check_ack()`); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.service.Checked(t.Context(), current); err != nil {
				t.Fatal(err)
			}
			if w.checks.calls != 2 || len(w.fake.CheckWrites("acme", "infra")) != 1 {
				t.Fatal("later attempt duplicated native effect", w.checks.calls, w.fake.CheckWrites("acme", "infra"))
			}
		})
	}
}

func TestAccCommitCheckEnqueueAtomicityAndDeletion(t *testing.T) {
	w := newCheckWorld(t)
	target := w.target(t, w.b, "infraweave/infra", false, 1)
	cause := errors.New("fanout rolled back")
	err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		if err := w.service.Enqueue(ctx, target); err != nil {
			return err
		}
		return cause
	})
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
	for _, table := range []string{"vcs_check_bindings", "vcs_check_requests", "vcs_check_resources", "jobs"} {
		var n int
		if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM `+table+` WHERE account_id=$1`, w.f.acct).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial enqueue survived", table, n, err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			errs <- tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.Enqueue(ctx, target) })
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE kind=$1`, feedback.ReportCheck.Name).Scan(&n); err != nil || n != 1 {
		t.Fatal("concurrent duplicate jobs", n, err)
	}
	j := w.claim(t)
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return w.service.Deleting(ctx, stacksapi.Stack{Account: w.f.acct, ID: w.b.Stack})
	}); err != nil {
		t.Fatal(err)
	}
	delete(w.facts.settings, w.b.Stack)
	if err := w.service.Checked(t.Context(), j); err != nil || w.checks.calls != 0 {
		t.Fatal("deleted stack posted", err)
	}
	w.enqueue(t, w.b, 2)
	if _, err := w.f.store.CheckBinding(t.Context(), w.f.acct, w.b.Key); !errors.Is(err, feedback.ErrNoCheck) {
		t.Fatal("deleted stack binding resurrected", err)
	}
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_check_resources WHERE account_id=$1`, w.f.acct).Scan(&n); err != nil || n != 1 {
		t.Fatal("historical owner cursor disappeared", n, err)
	}
	for {
		done, err := w.service.PurgeChecks(t.Context(), w.f.acct, 1)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_check_resources WHERE account_id=$1`, w.f.acct).Scan(&n); err != nil || n != 0 {
		t.Fatal("account purge retained native cursor", n, err)
	}
}

func TestAccCommitCheckCapAndCurrentFacts(t *testing.T) {
	w := newCheckWorld(t)
	run := w.facts.runs[w.b.Run]
	delete(w.facts.runs, w.b.Run)
	if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error {
		return w.service.Capped(ctx, w.f.acct, w.b.Integration, w.b.Stack, w.b.Commit, kernel.RunTypeProposed, 2)
	}); err != nil {
		t.Fatal(err)
	}
	w.report(t)
	writes := w.fake.CheckWrites("acme", "infra")
	if len(writes) != 1 || writes[0].Status != "failed" || !strings.Contains(writes[0].Description, "limit of 2 runs") {
		t.Fatal("cap had no failed check/budget", writes)
	}
	// A queued event is refreshed with the current terminal state and its own
	// current delta; stale event states never borrow a later plan's counts.
	run.CreatedAt = w.now.Add(time.Minute)
	w.b.CreatedAt = run.CreatedAt
	w.facts.runs[w.b.Run] = run
	w.state(w.b.Run, kernel.RunStateFinished, 5)
	w.enqueue(t, w.b, 1)
	w.report(t)
	writes = w.fake.CheckWrites("acme", "infra")
	last := writes[len(writes)-1]
	if last.Status != "success" || !strings.Contains(last.Description, "1 added") {
		t.Fatal("current facts were mixed with stale state", last)
	}
	var caps int
	if err := w.f.db.QueryRow(t.Context(), `SELECT count(*) FROM vcs_check_bindings WHERE is_cap`).Scan(&caps); err != nil || caps != 1 {
		t.Fatal(caps, err)
	}
}

func TestAccCommitCheckSafeFailureAndSnooze(t *testing.T) {
	w := newCheckWorld(t)
	w.enqueue(t, w.b, 1)
	j := w.claim(t)
	w.checks.cause = &port.RateLimited{Reset: w.now.Add(time.Minute)}
	if err := w.service.Checked(t.Context(), j); err == nil || err.Error() != queue.Snooze(time.Minute).Error() {
		t.Fatal("rate limit did not request attempt-free snooze", err)
	}
	if err := w.jobs.Snooze(t.Context(), j, 0); err != nil {
		t.Fatal(err)
	}
	next := w.claim(t)
	if next.Attempt != j.Attempt {
		t.Fatal("snooze consumed an attempt", j.Attempt, next.Attempt)
	}
	w.checks.cause = errors.New("private-provider-body-and-credential")
	next.MaxAttempts = next.Attempt
	if err := w.service.Checked(t.Context(), next); !errors.Is(err, feedback.ErrCheckReport) || strings.Contains(err.Error(), "private-provider") {
		t.Fatal("unsafe queue failure", err)
	}
	if strings.Contains(w.logs.String(), "private-provider") || !strings.Contains(w.logs.String(), w.b.Run.String()) || !strings.Contains(w.logs.String(), "infraweave/infra") {
		t.Fatal("exhaustion diagnostics unsafe or unhelpful", w.logs.String())
	}
}

func TestAccCommitCheckAggregateNativeOwnership(t *testing.T) {
	t.Run("newer integration and equal-owner progress", func(t *testing.T) {
		w := newCheckWorld(t)
		older := w.b
		older.Policy.Mode = "AGGREGATED"
		w.state(older.Run, kernel.RunStateFinished, 1)
		in, err := w.f.store.Get(t.Context(), w.f.acct, older.Integration, false)
		if err != nil {
			t.Fatal(err)
		}
		secrets, err := w.f.store.Secrets(t.Context(), w.f.acct, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		in.ID = kernel.NewULID()
		in.Slug = "other-integration"
		in.Name = "Other integration"
		if err := w.f.store.Create(t.Context(), in, secrets); err != nil {
			t.Fatal(err)
		}
		newer := older
		newer.Run = kernel.NewULID()
		newer.Key = "run:" + newer.Run.String()
		newer.Stack = kernel.NewULID()
		newer.Slug = "newer"
		newer.Integration = in.ID
		newer.CreatedAt = older.CreatedAt.Add(time.Minute)
		w.facts.settings[newer.Stack] = stacksapi.Settings{
			ID:             newer.Stack,
			Slug:           newer.Slug,
			Provider:       newer.Provider,
			Namespace:      newer.Namespace,
			Repository:     newer.Repository,
			VCSIntegration: &newer.Integration,
		}
		w.facts.runs[newer.Run] = runsapi.CheckRun{
			ID:        newer.Run,
			Stack:     newer.Stack,
			Type:      newer.Type,
			State:     kernel.RunStateFailed,
			Sequence:  1,
			CommitSHA: newer.Commit,
			CreatedAt: newer.CreatedAt,
		}
		enqueue := func(b feedback.CheckBinding, sequence int64) {
			t.Helper()
			target := w.target(t, b, "infraweave/proposed", true, sequence)
			if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.Enqueue(ctx, target) }); err != nil {
				t.Fatal(err)
			}
		}
		enqueue(newer, 1)
		w.report(t)
		enqueue(older, 1)
		w.report(t)
		writes := w.fake.CheckWrites("acme", "infra")
		if len(writes) != 1 || writes[0].Status != "failed" {
			t.Fatal("older integration aggregate stole newer native owner", writes)
		}
		previous := newer
		previous.Run = kernel.NewULID()
		previous.Key = "run:" + previous.Run.String()
		previous.CreatedAt = newer.CreatedAt.Add(-time.Second)
		w.facts.runs[previous.Run] = runsapi.CheckRun{
			ID:        previous.Run,
			Stack:     previous.Stack,
			Type:      previous.Type,
			State:     kernel.RunStatePlanning,
			Sequence:  1,
			CommitSHA: previous.Commit,
			CreatedAt: previous.CreatedAt,
		}
		enqueue(previous, 1)
		w.report(t)
		writes = w.fake.CheckWrites("acme", "infra")
		if writes[len(writes)-1].Status != "running" {
			t.Fatal("older run's current progress was hidden by latest owner", writes)
		}
		w.state(previous.Run, kernel.RunStateFinished, 2)
		enqueue(previous, 2)
		w.report(t)
		writes = w.fake.CheckWrites("acme", "infra")
		if len(writes) != 3 || writes[2].Status != "failed" {
			t.Fatal("aggregate failed to refresh equal-owner facts", writes)
		}
	})
	t.Run("aggregate wins equal-owner reserved name", func(t *testing.T) {
		w := newCheckWorld(t)
		w.b.Slug = "proposed"
		setting := w.facts.settings[w.b.Stack]
		setting.Slug = "proposed"
		w.facts.settings[w.b.Stack] = setting
		w.enqueue(t, w.b, 1)
		w.report(t)
		all := w.b
		all.Run = kernel.NewULID()
		all.Key = "run:" + all.Run.String()
		all.CreatedAt = all.CreatedAt.Add(-time.Second)
		all.Policy.Mode = "ALL"
		w.facts.runs[all.Run] = runsapi.CheckRun{
			ID:        all.Run,
			Stack:     all.Stack,
			Type:      all.Type,
			State:     kernel.RunStateFinished,
			Sequence:  1,
			CommitSHA: all.Commit,
			CreatedAt: all.CreatedAt,
		}
		w.state(w.b.Run, kernel.RunStateFailed, 2)
		target := w.target(t, all, "infraweave/proposed", true, 1)
		if err := tx.Do(t.Context(), w.f.db, tx.Options{}, func(ctx context.Context) error { return w.service.Enqueue(ctx, target) }); err != nil {
			t.Fatal(err)
		}
		w.report(t)
		w.enqueue(t, w.b, 2)
		w.report(t)
		writes := w.fake.CheckWrites("acme", "infra")
		if len(writes) != 2 || writes[1].Status != "failed" || !strings.Contains(writes[1].Description, "proposed: FAILED") {
			t.Fatal("old individual replaced the aggregate outcome/counts", writes)
		}
	})
}
