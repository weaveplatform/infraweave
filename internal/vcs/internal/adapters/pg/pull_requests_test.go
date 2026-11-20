package pg_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/pullrequest"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
	"github.com/weaveplatform/infraweave/test/deps"
)

func storedRequest(f *fixture, id kernel.ULID) pullrequest.Request {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return pullrequest.Request{
		Account:       f.acct,
		Integration:   id,
		ID:            kernel.NewULID(),
		RepositoryID:  "stable-repo",
		Namespace:     "acme",
		Repository:    "infra",
		Number:        "17",
		BaseBranch:    "main",
		HeadBranch:    "feature",
		HeadSHA:       requestHash("first"),
		AuthorLogin:   "creator",
		Title:         "changes",
		URL:           "https://example.com/pr/17",
		HeadBranchURL: "https://example.com/feature",
		State:         pullrequest.Open,
		CreatedAt:     at,
		UpdatedAt:     at,
		Draft:         true,
	}
}

func upsertRequest(t *testing.T, f *fixture, r pullrequest.Request) {
	t.Helper()
	if err := tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error { return f.store.Upsert(ctx, r) }); err != nil {
		t.Fatal(err)
	}
}

func TestAccPullRequestSnapshotsAndHistory(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	in, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	r := storedRequest(f, in.ID)
	if err := f.store.Upsert(ctx, r); !errors.Is(err, pullrequest.ErrNoTransaction) {
		t.Fatal("unfenced metadata accepted", err)
	}
	upsertRequest(t, f, r)
	current := r
	current.ID = kernel.NewULID()
	current.HeadSHA = requestHash("current")
	current.UpdatedAt = r.UpdatedAt.Add(time.Hour)
	current.CreatedAt = current.UpdatedAt
	current.Title = "edited"
	current.AuthorLogin = ""
	current.Draft = false
	upsertRequest(t, f, current)
	late := r
	late.ID = kernel.NewULID()
	late.HeadSHA = requestHash("late-history")
	late.UpdatedAt = r.UpdatedAt.Add(time.Minute)
	late.Title = "stale"
	late.State = pullrequest.Closed
	upsertRequest(t, f, late)
	upsertRequest(t, f, r)
	rows, err := f.store.Candidates(ctx, f.acct, in.ID, "acme", "infra", "main", nil, 100)
	if err != nil || len(rows) != 1 {
		t.Fatal("stable identity created duplicate or stale closure hid current", rows, err)
	}
	got := rows[0]
	if got.ID != r.ID || got.HeadSHA != requestHash("current") || got.Title != "edited" || got.AuthorLogin != "creator" || got.Draft || !got.CreatedAt.Equal(r.CreatedAt) ||
		!got.UpdatedAt.Equal(current.UpdatedAt) {
		t.Fatalf("stale/partial snapshot replaced current: %+v", got)
	}
	heads, err := f.store.Heads(ctx, f.acct, []kernel.ULID{r.ID})
	if err != nil || !reflect.DeepEqual(heads[r.ID], []string{requestHash("first"), requestHash("late-history"), requestHash("current")}) {
		t.Fatal("history was overwritten/duplicated", heads, err)
	}
	for _, acct := range []kernel.AccountID{f.acct, kernel.NewAccountID()} {
		login, err := f.store.Author(ctx, acct, in.ID, r.RepositoryID, r.Number)
		want := ""
		if acct == f.acct {
			want = "creator"
		}
		if err != nil || login != want {
			t.Fatal("creator scope", login, err)
		}
	}
	if heads, err := f.store.Heads(ctx, kernel.NewAccountID(), []kernel.ULID{r.ID}); err != nil || len(heads) != 0 {
		t.Fatal("foreign history visible", heads, err)
	}
	for _, scope := range []struct {
		name                          string
		acct                          kernel.AccountID
		integration                   kernel.ULID
		namespace, repository, branch string
	}{
		{"account", kernel.NewAccountID(), in.ID, "acme", "infra", "main"}, {"integration", f.acct, kernel.NewULID(), "acme", "infra", "main"}, {"namespace", f.acct, in.ID, "other", "infra", "main"}, {"repository", f.acct, in.ID, "acme", "other", "main"}, {"branch", f.acct, in.ID, "acme", "infra", "other"},
	} {
		t.Run(scope.name, func(t *testing.T) {
			rows, err := f.store.Candidates(ctx, scope.acct, scope.integration, scope.namespace, scope.repository, scope.branch, nil, 100)
			if err != nil || len(rows) != 0 {
				t.Fatal("unscoped candidate", rows, err)
			}
		})
	}
	// A repository path change updates the existing stable identity, including
	// closing/reopening lifecycle state, without losing earlier heads.
	current.Repository = "renamed"
	current.State = pullrequest.Merged
	current.UpdatedAt = current.UpdatedAt.Add(time.Minute)
	upsertRequest(t, f, current)
	if rows, err := f.store.Candidates(ctx, f.acct, in.ID, "acme", "infra", "main", nil, 100); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	current.State = pullrequest.Open
	current.UpdatedAt = current.UpdatedAt.Add(time.Minute)
	upsertRequest(t, f, current)
	if rows, err := f.store.Candidates(ctx, f.acct, in.ID, "acme", "renamed", "main", nil, 100); err != nil || len(rows) != 1 || rows[0].ID != r.ID {
		t.Fatal("reopen/path projection lost identity", rows, err)
	}
}

func TestAccPullRequestCandidatePagingAndRetention(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	in, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	r := storedRequest(f, in.ID)
	ids := make([]kernel.ULID, 0, 107)
	for n := range 107 {
		row := r
		row.ID = kernel.NewULID()
		row.Number = fmt.Sprint(n)
		ids = append(ids, row.ID)
		upsertRequest(t, f, row)
	}
	slices.SortFunc(ids, func(a, b kernel.ULID) int { return bytes.Compare(b[:], a[:]) })
	var got []kernel.ULID
	var after *pullrequest.Cursor
	for {
		rows, err := f.store.Candidates(ctx, f.acct, in.ID, r.Namespace, r.Repository, r.BaseBranch, after, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			got = append(got, row.ID)
		}
		last := rows[len(rows)-1]
		after = &pullrequest.Cursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	if !slices.Equal(ids, got) {
		t.Fatal("update-time ties lost/duplicated a page", len(ids), len(got))
	}
	// Same physical database, different account: purge must not affect it.
	foreign := integration.Integration{
		Account:   kernel.NewAccountID(),
		ID:        kernel.NewULID(),
		Provider:  "GITLAB",
		Slug:      "foreign",
		Name:      "Foreign",
		Labels:    []string{},
		Space:     kernel.NewULID(),
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
		Version:   1,
	}
	if err := f.store.Create(ctx, foreign, integration.Secrets{}); err != nil {
		t.Fatal(err)
	}
	other := r
	other.Account = foreign.Account
	other.Integration = foreign.ID
	other.ID = kernel.NewULID()
	upsertRequest(t, f, other)
	for _, state := range []string{pullrequest.Closed, pullrequest.Merged} {
		row := r
		row.ID = kernel.NewULID()
		row.Number = state
		row.State = state
		upsertRequest(t, f, row)
	}
	before := r.UpdatedAt.Add(time.Second)
	for range 2 {
		n, err := f.store.Prune(ctx, nil, &before, 1)
		if err != nil || n != 1 {
			t.Fatal("closed/merged prune", n, err)
		}
	}
	if n, err := f.store.Prune(ctx, nil, &before, 1); err != nil || n != 0 {
		t.Fatal("open request expired", n, err)
	}
	if n, err := f.store.Prune(ctx, &f.acct, nil, 200); err != nil || n != 107 {
		t.Fatal("account purge", n, err)
	}
	var heads int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM pull_request_heads WHERE account_id=$1`, f.acct).Scan(&heads); err != nil || heads != 0 {
		t.Fatal("purge orphaned heads", heads, err)
	}
	rows, err := f.store.Candidates(ctx, foreign.Account, foreign.ID, r.Namespace, r.Repository, r.BaseBranch, nil, 100)
	if err != nil || len(rows) != 1 {
		t.Fatal("account purge touched foreign history", rows, err)
	}
}

func TestAccPullRequestParentCompletionFence(t *testing.T) {
	for _, fault := range []string{"none", "completion rollback", "stale lease"} {
		t.Run(fault, func(t *testing.T) {
			w := newWebhookWorld(t)
			r := storedRequest(w.f, w.id)
			event := vcsapi.Event{
				Type:       "pull_request",
				Action:     "opened",
				Repository: vcsapi.Repository{ID: r.RepositoryID, Namespace: r.Namespace, Name: r.Repository},
				Head:       r.HeadSHA,
				PullRequest: &vcsapi.PullRequest{
					Number:        17,
					HeadBranch:    r.HeadBranch,
					BaseBranch:    r.BaseBranch,
					AuthorLogin:   r.AuthorLogin,
					Title:         r.Title,
					URL:           r.URL,
					HeadBranchURL: r.HeadBranchURL,
					State:         pullrequest.Open,
					CreatedAt:     r.CreatedAt,
					UpdatedAt:     r.UpdatedAt,
				},
			}
			deps := w.deps
			deps.Providers["GITHUB_ENTERPRISE"] = dispatchProvider{events: []vcsapi.Event{event}}
			deps.Dispatcher = pullrequest.New(pullrequest.Deps{Store: w.f.store, Integrations: w.f.svc})
			cause := errors.New("completion storage refused")
			if fault == "completion rollback" {
				deps.Jobs = refusedDispatchCompletion{Queue: w.queue, err: cause}
			}
			w.svc = intake.New(deps)
			w.receive(t, "request", webhookBody(128))
			job := w.claim(t, w.queue, "parent")
			if fault == "stale lease" {
				if _, err := w.f.db.Exec(t.Context(), `UPDATE jobs SET lease_until=now()-interval '1 minute' WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := w.queue.Recover(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			err := w.svc.Processed(t.Context(), job)
			if fault == "none" && err != nil || fault == "completion rollback" && !errors.Is(err, cause) || fault == "stale lease" && !errors.Is(err, queue.ErrLeaseLost) {
				t.Fatal("wrong settlement", err)
			}
			var rows, heads, children int
			if err := w.f.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM pull_requests),(SELECT count(*) FROM pull_request_heads),(SELECT count(*) FROM jobs WHERE kind<>$1)`, intake.Process.Name).
				Scan(&rows, &heads, &children); err != nil {
				t.Fatal(err)
			}
			d := w.delivery(t, "request")
			want := 0
			if fault == "none" {
				want = 1
				if d.State != intake.Dispatched {
					t.Fatal("successful upkeep not dispatched", d.State)
				}
			} else if d.State != intake.Received {
				t.Fatal("failed fence marked delivery", d.State)
			}
			if rows != want || heads != want || children != 0 {
				t.Fatal("completion leaked/lost metadata or created PR work", rows, heads, children)
			}
		})
	}
}

func TestAccPullRequestUpkeepAtomicityAndPurgeFence(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	in, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	r := storedRequest(f, in.ID)
	// A failure in the second write must roll back the current metadata too.
	if _, err := f.db.Exec(
		ctx,
		`CREATE FUNCTION refuse_request_head() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'head refused' USING ERRCODE='P0001'; END$$; CREATE TRIGGER refuse_request_head BEFORE INSERT ON pull_request_heads FOR EACH ROW EXECUTE FUNCTION refuse_request_head()`,
	); err != nil {
		t.Fatal(err)
	}
	err = tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error { return f.store.Upsert(ctx, r) })
	if cause, ok := errors.AsType[*pgconn.PgError](err); !ok || cause.Code != "P0001" {
		t.Fatal("head failure hidden", err)
	}
	var count int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM pull_requests`).Scan(&count); err != nil || count != 0 {
		t.Fatal("head failure left current metadata", count, err)
	}
	if _, err := f.db.Exec(ctx, `DROP TRIGGER refuse_request_head ON pull_request_heads; DROP FUNCTION refuse_request_head()`); err != nil {
		t.Fatal(err)
	}
	// The writer's real key-share lock fences deletion until its transaction
	// resolves; deletion first makes subsequent queued upkeep a harmless no-op.
	held, err := f.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // rollback after commit is harmless
	if _, err := held.Exec(ctx, `SELECT id FROM vcs_integrations WHERE account_id=$1 AND id=$2 FOR UPDATE`, f.acct, in.ID); err != nil {
		t.Fatal(err)
	}
	blocked := tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, f.db).Exec(ctx, `SET LOCAL lock_timeout='20ms'`); err != nil {
			return err
		}
		return f.store.Upsert(ctx, r)
	})
	if cause, ok := errors.AsType[*pgconn.PgError](blocked); !ok || cause.Code != "55P03" {
		t.Fatal("upkeep bypassed integration deletion lock", blocked)
	}
	if _, err := held.Exec(ctx, `DELETE FROM vcs_integrations WHERE account_id=$1 AND id=$2`, f.acct, in.ID); err != nil {
		t.Fatal(err)
	}
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	upsertRequest(t, f, r)
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM pull_requests`).Scan(&count); err != nil || count != 0 {
		t.Fatal("queued request resurrected purged account history", count, err)
	}
}

func TestAccPullRequestReadUnavailable(t *testing.T) {
	f := newFixture(t)
	f.db.Close()
	ctx := t.Context()
	id := kernel.NewULID()
	for name, err := range map[string]error{
		"author": func() error { _, err := f.store.Author(ctx, f.acct, id, "repo", "17"); return err }(),
		"candidates": func() error {
			_, err := f.store.Candidates(ctx, f.acct, id, "acme", "infra", "main", nil, 50)
			return err
		}(),
		"heads": func() error { _, err := f.store.Heads(ctx, f.acct, []kernel.ULID{id}); return err }(),
		"prune": func() error { _, err := f.store.Prune(ctx, nil, nil, 10); return err }(),
	} {
		if err == nil {
			t.Fatal("closed database became empty success", name)
		}
	}
	if heads, err := f.store.Heads(ctx, f.acct, nil); err != nil || len(heads) != 0 {
		t.Fatal("empty history needlessly read database", heads, err)
	}
}

func requestHash(text string) string { return fmt.Sprintf("%040x", []byte(text)) }

func TestAccPullRequestIntegrationDeletionCascades(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	in, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	r := storedRequest(f, in.ID)
	upsertRequest(t, f, r)
	r.HeadSHA = requestHash("next")
	r.UpdatedAt = r.UpdatedAt.Add(time.Minute)
	upsertRequest(t, f, r)
	if _, err := f.svc.Delete(ctx, f.admin, in.ID.String(), integration.GitLab); err != nil {
		t.Fatal(err)
	}
	var rows, heads int
	if err := f.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM pull_requests),(SELECT count(*) FROM pull_request_heads)`).
		Scan(&rows, &heads); err != nil || rows != 0 ||
		heads != 0 {
		t.Fatal("integration deletion orphaned an open request/history", rows, heads, err)
	}
	upsertRequest(t, f, r)
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM pull_requests`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("queued upkeep recreated deleted integration history", rows, err)
	}
}

type localWorkspaceRuns struct{ called bool }

func (r *localWorkspaceRuns) OnCommits(context.Context, kernel.AccountID, kernel.ULID, []string) ([]runsapi.CommitRun, error) {
	r.called = true
	return []runsapi.CommitRun{{ID: kernel.NewULID(), CommitSHA: ""}}, nil
}

func TestAccPullRequestRejectsNonCommitMembership(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	in, err := f.svc.Create(ctx, f.admin, gitlab("Main", "", true))
	if err != nil {
		t.Fatal(err)
	}
	r := storedRequest(f, in.ID)
	upsertRequest(t, f, r)
	bad := r
	bad.HeadSHA = ""
	if err := tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error { return f.store.Upsert(ctx, bad) }); !errors.Is(err, pullrequest.ErrHead) {
		t.Fatal("new invalid open head accepted", err)
	}
	// A branch-deletion notification still closes the existing record, retains
	// its last real head, and contributes no empty historical head.
	bad.State = pullrequest.Closed
	bad.UpdatedAt = r.UpdatedAt.Add(time.Hour)
	upsertRequest(t, f, bad)
	var head, state string
	if err := f.db.QueryRow(ctx, `SELECT head_sha,state FROM pull_requests WHERE account_id=$1 AND id=$2`, f.acct, r.ID).
		Scan(&head, &state); err != nil || head != r.HeadSHA ||
		state != pullrequest.Closed {
		t.Fatal("terminal no-head snapshot stranded/regressed request", head, state, err)
	}
	heads, err := f.store.Heads(ctx, f.acct, []kernel.ULID{r.ID})
	if err != nil || !slices.Equal(heads[r.ID], []string{r.HeadSHA}) {
		t.Fatal("terminal snapshot inserted invalid history", heads, err)
	}
	// Reproduce legacy/minimal payload data directly, bypassing the new write
	// guard: even this stored empty head cannot qualify via a local-workspace
	// run's empty commit projection. VCS must not query Runs with that value.
	if _, err := f.db.Exec(ctx, `UPDATE pull_requests SET head_sha='',state='open' WHERE account_id=$1 AND id=$2`, f.acct, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `DELETE FROM pull_request_heads WHERE account_id=$1 AND request_id=$2`, f.acct, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, `INSERT INTO pull_request_heads(account_id,request_id,sha,became_head_at) VALUES($1,$2,'',$3)`, f.acct, r.ID, r.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	runs := &localWorkspaceRuns{}
	svc := pullrequest.New(pullrequest.Deps{Store: f.store, Integrations: f.svc, Runs: runs})
	id := kernel.NewULID()
	got, err := svc.PullRequests(
		ctx,
		f.acct,
		[]stacksapi.Settings{{Stack: stacksapi.Stack{ID: id}, Provider: integration.GitLab, Namespace: r.Namespace, Repository: r.Repository, Branch: r.BaseBranch}},
	)
	if err != nil || len(got[id]) != 0 || runs.called {
		t.Fatal("stored noncommit joined local-workspace membership", got, runs.called, err)
	}
}

type renamedRequestRuns struct {
	account    kernel.AccountID
	stack, run kernel.ULID
	head       string
}

func (r renamedRequestRuns) OnCommits(_ context.Context, acct kernel.AccountID, stack kernel.ULID, _ []string) ([]runsapi.CommitRun, error) {
	if acct != r.account || stack != r.stack {
		return nil, errDown
	}
	return []runsapi.CommitRun{{ID: r.run, CommitSHA: r.head, CreatedAt: time.Now()}}, nil
}

func TestAccPullRequestReadsFollowNativeRepositoryRenames(t *testing.T) {
	cases := []struct {
		name, provider, native string
		legacy                 bool
	}{
		{name: "github", provider: integration.GitHubEnterprise, native: "7"},
		{name: "gitlab", provider: integration.GitLab, native: "42"},
		{name: "bitbucket canonical UUID", provider: integration.BitbucketCloud, native: "7bf80420-965b-4f23-90c0-ec114b064264"},
		{name: "bitbucket historical braced UUID", provider: integration.BitbucketCloud, native: "{7BF80420-965B-4F23-90C0-EC114B064264}", legacy: true},
		{name: "bitbucket historical uppercase UUID", provider: integration.BitbucketCloud, native: "7BF80420-965B-4F23-90C0-EC114B064264", legacy: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			id := kernel.NewULID()
			now := time.Now().UTC().Truncate(time.Microsecond)
			if err := f.store.Create(
				t.Context(),
				integration.Integration{
					Account:   f.acct,
					ID:        id,
					Provider:  tc.provider,
					Slug:      "rename",
					Name:      "Rename",
					Space:     f.acct.ULID,
					Labels:    []string{},
					Checks:    integration.Checks{Mode: "INDIVIDUAL"},
					CreatedAt: now,
					UpdatedAt: now,
					Version:   1,
				},
				integration.Secrets{},
			); err != nil {
				t.Fatal(err)
			}
			request := storedRequest(f, id)
			request.RepositoryID = tc.native
			upsertRequest(t, f, request)
			if tc.legacy {
				if _, err := f.db.Exec(t.Context(), `UPDATE pull_requests SET repository_id=$3 WHERE account_id=$1 AND id=$2`, f.acct, request.ID, tc.native); err != nil {
					t.Fatal(err)
				}
			}
			native := &lifecycle.Service{DB: f.db, Store: f.store, Stacks: &routedTargets{}, Now: func() time.Time { return now }}
			use := stacksapi.Use{Integration: id, Provider: tc.provider}
			original := vcsapi.Repository{ID: tc.native, Namespace: "acme", Name: "infra"}
			if err := native.Seed(t.Context(), f.acct, id, use, original); err != nil {
				t.Fatal(err)
			}
			renamed := original
			renamed.Namespace, renamed.Name = "platform", "renamed"
			if err := tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
				return native.Apply(ctx, f.acct, id, use, now.Add(time.Second), []vcsapi.Event{{Repository: renamed}})
			}); err != nil {
				t.Fatal(err)
			}
			candidates, err := f.store.Candidates(t.Context(), f.acct, id, renamed.Namespace, renamed.Name, "main", nil, 100)
			if err != nil || len(candidates) != 1 || candidates[0].ID != request.ID {
				t.Fatal("retained PR vanished after native rename", candidates, err)
			}
			if old, err := f.store.Candidates(t.Context(), f.acct, id, original.Namespace, original.Name, "main", nil, 100); err != nil || len(old) != 0 {
				t.Fatal("stale path still selected mapped PR", old, err)
			}
			stack, run := kernel.NewULID(), kernel.NewULID()
			reader := pullrequest.New(
				pullrequest.Deps{Store: f.store, Integrations: f.svc, Runs: renamedRequestRuns{account: f.acct, stack: stack, run: run, head: request.HeadSHA}},
			)
			views, err := reader.PullRequests(
				t.Context(),
				f.acct,
				[]stacksapi.Settings{
					{Stack: stacksapi.Stack{ID: stack}, Provider: tc.provider, VCSIntegration: &id, Namespace: renamed.Namespace, Repository: renamed.Name, Branch: "main"},
				},
			)
			if err != nil || len(views[stack]) != 1 || !slices.Equal(views[stack][0].Runs, []kernel.ULID{run}) {
				t.Fatal("renamed stack lost PR/run projection", views, err)
			}
			if foreign, err := f.store.Candidates(t.Context(), kernel.NewAccountID(), id, renamed.Namespace, renamed.Name, "main", nil, 100); err != nil || len(foreign) != 0 {
				t.Fatal("mapped path crossed accounts", foreign, err)
			}
			if tc.legacy {
				update := request
				update.ID = kernel.NewULID()
				update.RepositoryID = lifecycle.CanonicalID(tc.native)
				update.HeadSHA = requestHash("new-head")
				update.UpdatedAt = now.Add(2 * time.Second)
				upsertRequest(t, f, update)
				var count int
				if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM pull_requests WHERE account_id=$1 AND integration_id=$2`, f.acct, id).
					Scan(&count); err != nil ||
					count != 1 {
					t.Fatal("canonical update duplicated historical UUID PR", count, err)
				}
				heads, err := f.store.Heads(t.Context(), f.acct, []kernel.ULID{request.ID})
				if err != nil || !slices.Equal(heads[request.ID], []string{request.HeadSHA, update.HeadSHA}) {
					t.Fatal("UUID update replaced stable request/head history", heads, err)
				}
				author, err := f.store.Author(t.Context(), f.acct, id, update.RepositoryID, request.Number)
				if err != nil || author != request.AuthorLogin {
					t.Fatal("canonical creator lookup missed historical UUID", author, err)
				}
			}
		})
	}
}

type requestHoldBarrier struct {
	*pg.Store
	ready, proceed chan struct{}
}

func (s requestHoldBarrier) Upsert(ctx context.Context, r pullrequest.Request) error {
	if err := s.Store.Upsert(ctx, r); err != nil {
		return err
	}
	close(s.ready)
	select {
	case <-s.proceed:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func TestAccCompoundParentsAcquireExclusiveOwnerBeforeLifecycle(t *testing.T) {
	w := newWebhookWorld(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	first := requestHoldBarrier{Store: w.f.store, ready: make(chan struct{}), proceed: make(chan struct{})}
	second := requestHoldBarrier{Store: w.f.store, ready: make(chan struct{}), proceed: make(chan struct{})}
	release := func() {
		select {
		case <-first.proceed:
		default:
			close(first.proceed)
		}
		select {
		case <-second.proceed:
		default:
			close(second.proceed)
		}
	}
	defer release()
	native := &lifecycle.Service{DB: w.f.db, Store: w.f.store, Stacks: &routedTargets{}, Now: time.Now}
	prepare := func(label, repository string, store pullrequest.Store) intake.DispatchPlan {
		t.Helper()
		if status := w.receive(t, label, webhookBody(128)); status != 202 { //nolint:contextcheck // fixture helpers use the test lifetime before bounded parent work
			t.Fatal(status)
		}
		d := w.delivery(t, label) //nolint:contextcheck // fixture helpers use the test lifetime before bounded parent work
		events := []vcsapi.Event{
			{
				Type:        "pull_request",
				Repository:  vcsapi.Repository{ID: repository, Namespace: "acme", Name: label},
				Head:        requestHash(label),
				PullRequest: &vcsapi.PullRequest{Number: 17, BaseBranch: "main", HeadBranch: "feature", AuthorLogin: "creator", Title: label, State: pullrequest.Open},
			},
		}
		service := pullrequest.New(
			pullrequest.Deps{
				Store:  store,
				Pushes: push.New(push.Deps{DB: w.f.db, Store: w.f.store, Stacks: &routedTargets{}, Integrations: w.f.svc, Jobs: w.queue, Lifecycle: native}),
			},
		)
		plan, err := service.Prepare(ctx, d, events)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	plan1, plan2 := prepare("one", "7", first), prepare("two", "8", second)
	done := make(chan error, 2)
	pids := make(chan uint32, 2)
	run := func(plan intake.DispatchPlan) {
		done <- tx.Do(ctx, w.f.db, tx.Options{NoRetry: true}, func(ctx context.Context) error {
			if _, err := tx.Q(ctx, w.f.db).Exec(ctx, `SET LOCAL deadlock_timeout='25ms'`); err != nil {
				return err
			}
			var pid uint32
			if err := tx.Q(ctx, w.f.db).QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			pids <- pid
			return plan.Commit(ctx)
		})
	}
	go run(plan1)
	select {
	case <-first.ready:
	case <-ctx.Done():
		t.Fatal(context.Cause(ctx))
	}
	<-pids
	go run(plan2)
	var secondPID uint32
	select {
	case secondPID = <-pids:
	case <-ctx.Done():
		t.Fatal(context.Cause(ctx))
	}
	// Old KEY SHARE allowed both upserts to reach the barrier and then deadlock
	// upgrading. The exclusive hold makes the second parent wait BEFORE upkeep.
	if err := deps.Eventually(ctx, "second parent completes its shared hold or waits for the exclusive owner", func(ctx context.Context) (bool, error) {
		select {
		case <-second.ready:
			return true, nil
		default:
		}
		var waiting bool
		err := w.f.db.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, secondPID).Scan(&waiting)
		return waiting, err
	}); err != nil {
		t.Fatal(err)
	}
	release()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal("compound parents failed their owner/lifecycle fence", err)
		}
	}
	var requests, maps int
	if err := w.f.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM pull_requests WHERE account_id=$1),(SELECT count(*) FROM vcs_repository_map WHERE account_id=$1)`, w.f.acct).
		Scan(&requests, &maps); err != nil || requests != 2 ||
		maps != 2 {
		t.Fatal("compound completion lost request or lifecycle work", requests, maps, err)
	}
}
