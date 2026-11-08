package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// awaitLockWait returns once a session of q's database waits on a lock,
// which is how the test knows a delete has reached the held row.
func awaitLockWait(ctx context.Context, t *testing.T, q tx.Querier) {
	t.Helper()
	for ctx.Err() == nil {
		var n int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
	}
	t.Fatal("no session waited on a lock")
}

// TestAccSpaceHoldRace checks the space hold and the stacks guard as the
// server wires access and stacks: a space delete that starts while a
// transaction holds the space waits for it. When that transaction then
// writes a stack in the space and commits, the stacks guard sees the stack
// and the delete answers 422; when it rolls back, the delete goes ahead.
func TestAccSpaceHoldRace(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	q := db.Primary()
	d, err := newDomains(config.Defaults(), db, testKeyring(t, q), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Features.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	op := kernel.WithPrincipal(ctx, kernel.Principal{Kind: kernel.PrincipalOperator})
	if _, err := d.Access.OperatorAPI().
		OrganizationCreate(op, opapi.OrganizationCreateRequestObject{
			Body: &opapi.OrganizationCreateInput{
				Slug: "hold", Name: "Hold", Account: opapi.AccountInput{Slug: "hold", Name: "Hold"},
			},
		}); err != nil {
		t.Fatal(err)
	}
	var (
		org       kernel.OrganizationID
		acct      kernel.AccountID
		top, role kernel.ULID
	)
	if err := q.QueryRow(ctx, `SELECT a.organization_id, a.id, s.id, r.id FROM accounts a
		JOIN spaces s ON s.account_id = a.id AND s.parent_id IS NULL
		JOIN roles r ON r.account_id = a.id AND r.slug = 'space-admin'
		WHERE a.slug = 'hold'`).Scan(&org, &acct, &top, &role); err != nil {
		t.Fatal(err)
	}
	member := kernel.NewMemberID()
	kept, gone := kernel.NewULID(), kernel.NewULID()
	for _, s := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ($1, $2, 'alice', 'Alice', 'alice@example.com', 'active')`, []any{org, member}},
		{`INSERT INTO account_users (account_id, organization_id, member_id)
			VALUES ($1, $2, $3)`, []any{acct, org, member}},
		{
			`INSERT INTO role_bindings
			(account_id, id, actor_kind, actor_id, role_id, space_id, origin, created_by)
			VALUES ($1, $2, 'user', $3, $4, $5, 'binding', 'test')`,
			[]any{acct, kernel.NewULID(), member, role, top},
		},
		{
			`INSERT INTO spaces (account_id, id, slug, name, parent_id)
			VALUES ($1, $2, 'kept', 'Kept', $4), ($1, $3, 'gone', 'Gone', $4)`,
			[]any{acct, kept, gone, top},
		},
		{
			`INSERT INTO space_closure (account_id, ancestor_id, descendant_id, depth)
			VALUES ($1, $2, $2, 0), ($1, $4, $2, 1), ($1, $3, $3, 0), ($1, $4, $3, 1)`,
			[]any{acct, kept, gone, top},
		},
	} {
		if _, err := q.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	admin := kernel.WithPrincipal(ctx, kernel.Principal{
		Kind: kernel.PrincipalUser, ActorID: member.ULID,
		OrganizationID: org, AccountID: acct, MemberID: member,
	})
	errRollback := errors.New("rolled back")
	// race holds space id, starts deleting slug, and once the delete
	// waits, writes a stack there and commits, or rolls back; it answers
	// the hold's transaction's error and the delete's.
	race := func(slug string, id kernel.ULID, commit bool) (held, deleted error) {
		holding, release := make(chan struct{}), make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			held = tx.Do(ctx, q, tx.Options{}, func(ctx context.Context) error {
				err := d.Access.Spaces().Hold(ctx, acct, id)
				close(holding)
				if err != nil {
					return err //nolint:wrapcheck // the test reads it as it is
				}
				<-release
				if !commit {
					return errRollback
				}
				_, err = tx.Q(ctx, q).Exec(ctx, `INSERT INTO stacks (account_id, id, slug, space_id)
					VALUES ($1, $2, 'infra', $3)`, acct, kernel.NewULID(), id)
				return err //nolint:wrapcheck // as above
			})
		})
		<-holding
		wg.Go(func() {
			_, deleted = d.Access.API().
				SpaceDelete(admin, api.SpaceDeleteRequestObject{SpaceId: slug})
		})
		awaitLockWait(ctx, t, q)
		close(release)
		wg.Wait()
		return held, deleted
	}

	held, deleted := race("kept", kept, true)
	if held != nil {
		t.Fatalf("the holding transaction = %v", held)
	}
	if p, ok := errors.AsType[*problem.Error](deleted); !ok ||
		p.Problem.Status != http.StatusUnprocessableEntity {
		t.Errorf("deleting a space a committed stack lives in = %v, want 422", deleted)
	}

	held, deleted = race("gone", gone, false)
	if !errors.Is(held, errRollback) || deleted != nil {
		t.Errorf("after a rolled-back hold: hold = %v, delete = %v", held, deleted)
	}

	var left []string
	rows, err := q.Query(ctx, `SELECT slug FROM spaces
		WHERE account_id = $1 AND slug IN ('kept', 'gone') ORDER BY slug`, acct)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		left = append(left, s)
	}
	if len(left) != 1 || left[0] != "kept" {
		t.Errorf("spaces left = %v, want [kept]", left)
	}
	if err := d.Access.Spaces().Hold(ctx, acct, gone); err == nil {
		t.Error("a deleted space was held")
	}
}
