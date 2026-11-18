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
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/pullrequest"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
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
		HeadSHA:       "first",
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
	current.HeadSHA = "current"
	current.UpdatedAt = r.UpdatedAt.Add(time.Hour)
	current.CreatedAt = current.UpdatedAt
	current.Title = "edited"
	current.AuthorLogin = ""
	current.Draft = false
	upsertRequest(t, f, current)
	late := r
	late.ID = kernel.NewULID()
	late.HeadSHA = "late-history"
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
	if got.ID != r.ID || got.HeadSHA != "current" || got.Title != "edited" || got.AuthorLogin != "creator" || got.Draft || !got.CreatedAt.Equal(r.CreatedAt) ||
		!got.UpdatedAt.Equal(current.UpdatedAt) {
		t.Fatalf("stale/partial snapshot replaced current: %+v", got)
	}
	heads, err := f.store.Heads(ctx, f.acct, []kernel.ULID{r.ID})
	if err != nil || !reflect.DeepEqual(heads[r.ID], []string{"first", "late-history", "current"}) {
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
			deps.Dispatcher = pullrequest.New(pullrequest.Deps{Store: w.f.store, Integrations: w.f.svc, Pushes: push.New(push.Deps{})})
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
