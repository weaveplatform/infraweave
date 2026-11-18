package pg_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

func TestAccVCSRepositoryProjection(t *testing.T) {
	f := newFixture(t)
	bound, other := kernel.NewULID(), kernel.NewULID()
	for i, row := range []struct {
		provider, repository string
		integration          kernel.ULID
		detached, foreign    bool
	}{
		{"GITHUB_ENTERPRISE", "infra", bound, false, false},
		{"GITHUB_ENTERPRISE", "infra", bound, false, false},
		{"GITHUB", "private", bound, false, false},
		{"GITHUB", "implicit", kernel.ULID{}, false, false},
		{"GITHUB_ENTERPRISE", "canonical", kernel.ULID{}, false, false},
		{"GITHUB_ENTERPRISE", "another-binding", other, false, false},
		{"GITLAB", "another-provider", kernel.ULID{}, false, false},
		{"GITHUB", "foreign-account", bound, false, true},
		{"GITHUB", "detached", bound, true, false},
	} {
		account := f.acct
		if row.foreign {
			account = kernel.NewAccountID()
		}
		// All rows live in a space the ordinary caller cannot read. The
		// integration-admin projection still needs their repository set.
		_, err := f.db.Exec(t.Context(), `INSERT INTO stacks
			(account_id,id,slug,space_id,name,repository,branch,provider,namespace,vcs_integration_id,vcs_detached,vendor)
			VALUES($1,$2,$3,$4,$3,$5,'main',$6,'acme',$7,$8,'OPEN_TOFU')`,
			account, kernel.NewULID(), fmt.Sprintf("projection-%d", i), f.hidden, row.repository, row.provider, row.integration, row.detached)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		defaultIntegration bool
		want               []string
	}{
		{false, []string{"infra", "private"}},
		{true, []string{"canonical", "implicit", "infra", "private"}},
	} {
		rows, err := f.svc.VCSRepositories(
			t.Context(),
			f.acct,
			stacksapi.Use{Integration: bound, Provider: "GITHUB_ENTERPRISE", Aliases: []string{"GITHUB"}, Default: tc.defaultIntegration},
		)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(rows))
		for i, row := range rows {
			if row.Namespace != "acme" {
				t.Fatal(row)
			}
			got[i] = row.Name
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("default=%t: %v, want %v", tc.defaultIntegration, got, tc.want)
		}
	}
	f.db.Close()
	_, cause := f.db.Exec(t.Context(), "SELECT 1")
	if cause == nil {
		t.Fatal("closed pool unexpectedly accepted a query")
	}
	if _, err := f.svc.VCSRepositories(t.Context(), f.acct, stacksapi.Use{Integration: bound}); !errors.Is(err, cause) {
		t.Fatalf("repository projection failure lost: %v", err)
	}
}

func TestAccRepointVCSRepository(t *testing.T) {
	for _, defaultIntegration := range []bool{false, true} {
		t.Run(fmt.Sprintf("default=%t", defaultIntegration), func(t *testing.T) {
			f := newFixture(t)
			bound, other := kernel.NewULID(), kernel.NewULID()
			use := stacksapi.Use{Integration: bound, Provider: "GITHUB_ENTERPRISE", Aliases: []string{"GITHUB"}, Default: defaultIntegration}
			from, to := stacksapi.VCSRepository{Namespace: "old", Name: "infra"}, stacksapi.VCSRepository{Namespace: "new", Name: "renamed"}
			changed := []kernel.ULID{}
			for _, row := range []struct {
				slug, provider, namespace, repository, branch string
				integration                                   kernel.ULID
				detached, foreign, matches                    bool
			}{
				{"main", "GITHUB_ENTERPRISE", "old", "infra", "main", bound, false, false, true},
				{"feature", "GITHUB_ENTERPRISE", "old", "infra", "feature", bound, false, false, true},
				{"alias", "GITHUB", "old", "infra", "other", bound, false, false, true},
				{"default", "GITHUB_ENTERPRISE", "old", "infra", "main", kernel.ULID{}, false, false, defaultIntegration},
				{"default-alias", "GITHUB", "old", "infra", "feature", kernel.ULID{}, false, false, defaultIntegration},
				{"different-integration", "GITHUB_ENTERPRISE", "old", "infra", "main", other, false, false, false},
				{"different-provider", "GITLAB", "old", "infra", "main", bound, false, false, false},
				{"different-default-provider", "GITLAB", "old", "infra", "main", kernel.ULID{}, false, false, false},
				{"different-namespace", "GITHUB_ENTERPRISE", "elsewhere", "infra", "main", bound, false, false, false},
				{"different-repository", "GITHUB_ENTERPRISE", "old", "other", "main", bound, false, false, false},
				{"detached", "GITHUB_ENTERPRISE", "old", "infra", "main", bound, true, false, false},
				{"different-account", "GITHUB_ENTERPRISE", "old", "infra", "main", bound, false, true, false},
			} {
				account, id := f.acct, kernel.NewULID()
				if row.foreign {
					account = kernel.NewAccountID()
				}
				if _, err := f.db.Exec(t.Context(), `INSERT INTO stacks
					(account_id,id,slug,space_id,name,provider,namespace,repository,branch,vcs_integration_id,vcs_detached,vendor)
					VALUES($1,$2,$3,$4,$3,$5,$6,$7,$8,$9,$10,'OPEN_TOFU')`,
					account, id, row.slug, f.hidden, row.provider, row.namespace, row.repository, row.branch, row.integration, row.detached); err != nil {
					t.Fatal(err)
				}
				if row.matches {
					changed = append(changed, id)
				}
			}
			if err := tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
				return f.svc.RepointVCSRepository(ctx, f.acct, use, from, to)
			}); err != nil {
				t.Fatal(err)
			}
			rows, err := f.db.Query(t.Context(), `SELECT id,namespace,repository,version FROM stacks ORDER BY id`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id kernel.ULID
				var namespace, repository string
				var version int64
				if err := rows.Scan(&id, &namespace, &repository, &version); err != nil {
					t.Fatal(err)
				}
				if slices.Contains(changed, id) {
					if namespace != to.Namespace || repository != to.Name || version != 2 {
						t.Fatal("matching stack was not versioned/repointed", id, namespace, repository, version)
					}
				} else if version != 1 || namespace == to.Namespace || repository == to.Name {
					t.Fatal("repoint crossed its source binding", id, namespace, repository, version)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if err := tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
				if err := f.svc.RepointVCSRepository(ctx, f.acct, use, from, to); err != nil {
					return err
				}
				return f.svc.RepointVCSRepository(ctx, f.acct, use, to, to)
			}); err != nil {
				t.Fatal(err)
			}
			var events, audits, advances int
			var valid bool
			err = f.db.QueryRow(t.Context(), `SELECT count(*),coalesce(bool_and(kind=$2 AND schema_version=1 AND sequence=2
				AND subject_kind='stack' AND subject_id=document->>'stack' AND space=$3
				AND document->>'change'='updated' AND (document->>'source')::boolean),false),
				(SELECT count(*) FROM audit_trail_entries WHERE account_id=$1),
				(SELECT coalesce(sum(version-1),0) FROM stacks) FROM events WHERE account_id=$1`,
				f.acct, stacksapi.ChangedKind, f.hidden.String()).Scan(&events, &valid, &audits, &advances)
			if err != nil || events != len(changed) || !valid || audits != 0 || advances != len(changed) {
				t.Fatal("rename lost canonical events, duplicated effects or wrote an audit", events, valid, audits, advances, err)
			}
		})
	}
}

func TestAccRepointVCSRepositoryRollback(t *testing.T) {
	for _, fault := range []string{"outer", "update", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			f := newFixture(t)
			use := stacksapi.Use{Integration: kernel.NewULID(), Provider: "GITLAB"}
			from, to := stacksapi.VCSRepository{Namespace: "old", Name: "infra"}, stacksapi.VCSRepository{Namespace: "new", Name: "infra"}
			for _, slug := range []string{"first", "second"} {
				if _, err := f.db.Exec(t.Context(), `INSERT INTO stacks
					(account_id,id,slug,space_id,name,provider,namespace,repository,branch,vcs_integration_id,vendor)
					VALUES($1,$2,$3,$4,$3,'GITLAB','old','infra','main',$5,'OPEN_TOFU')`,
					f.acct, kernel.NewULID(), slug, f.hidden, use.Integration); err != nil {
					t.Fatal(err)
				}
			}
			q := queue.New(f.db)
			kind := queue.Kind{Name: "test.rename", Queue: queue.Default, Policy: queue.Ignore}
			if _, err := q.Enqueue(t.Context(), kind, struct{}{}, queue.Account(f.acct.ULID)); err != nil {
				t.Fatal(err)
			}
			jobs, err := q.Claim(t.Context(), queue.Default, []string{kind.Name}, 1, time.Minute, "rename")
			if err != nil || len(jobs) != 1 {
				t.Fatal("rename claim", jobs, err)
			}
			if fault != "outer" {
				table, operation := "events", "INSERT"
				if fault == "update" {
					table, operation = "stacks", "UPDATE"
				}
				if _, err := f.db.Exec(t.Context(), `CREATE FUNCTION refuse_repoint() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN RAISE EXCEPTION 'repoint refused' USING ERRCODE='P0001'; END $$;
					CREATE TRIGGER refuse_repoint BEFORE `+operation+` ON `+table+` FOR EACH ROW EXECUTE FUNCTION refuse_repoint()`); err != nil {
					t.Fatal(err)
				}
			}
			cause := errors.New("caller rolls back")
			err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
				if err := q.CompleteTx(ctx, jobs[0]); err != nil {
					return err
				}
				if err := f.svc.RepointVCSRepository(ctx, f.acct, use, from, to); err != nil {
					return err
				}
				return cause
			})
			if fault == "outer" && !errors.Is(err, cause) {
				t.Fatal("outer rollback hidden", err)
			}
			if fault != "outer" {
				if pgerr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgerr.Code != "P0001" {
					t.Fatal("SQL/outbox refusal hidden", err)
				}
			}
			var changed, events int
			var state string
			if err := f.db.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM stacks WHERE account_id=$1 AND (namespace<>'old' OR version<>1)),
				(SELECT count(*) FROM events WHERE account_id=$1),
				(SELECT state FROM jobs WHERE id=$2)`, f.acct, jobs[0].ID).Scan(&changed, &events, &state); err != nil || changed != 0 || events != 0 || state != "running" {
				t.Fatal("repoint failure committed coordinates/version/event/ack", changed, events, state, err)
			}
		})
	}
}

func TestUnitRepointVCSRepositoryRequiresTransaction(t *testing.T) {
	store := pg.New(nil)
	service := core.New(core.Deps{})
	repo := stacksapi.VCSRepository{Namespace: "acme", Name: "infra"}
	for _, target := range []stacksapi.VCSRepository{repo, {Namespace: "acme", Name: "other"}} {
		if _, err := store.RepointVCSRepository(t.Context(), kernel.AccountID{}, stacksapi.Use{}, repo, target); !errors.Is(err, stacksapi.ErrNoTransaction) {
			t.Fatal("unfenced store write/no-op reached storage", err)
		}
		if err := service.RepointVCSRepository(t.Context(), kernel.AccountID{}, stacksapi.Use{}, repo, target); !errors.Is(err, stacksapi.ErrNoTransaction) {
			t.Fatal("unfenced public write/no-op reached storage", err)
		}
	}
}
