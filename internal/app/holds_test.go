package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
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

// holdWorld is the domains as the server wires them over a fresh
// database, with an account whose top space holds two spaces, kept and
// gone, and a member who administers the top space. db and keys build
// another replica over the same database.
type holdWorld struct {
	d          Domains
	db         *sql.DB
	keys       *crypt.Keyring
	q          *pgxpool.Pool
	acct       kernel.AccountID
	kept, gone kernel.ULID
	admin      context.Context
}

func newHoldWorld(t *testing.T) holdWorld {
	t.Helper()
	ctx := t.Context()
	db := sqltest.Open(t)
	q := db.Primary()
	keys := testKeyring(t, q)
	d, err := newDomains(
		config.Defaults(),
		db,
		keys,
		nil,
		testEvents(t, q),
		slog.New(slog.DiscardHandler),
	)
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
	return holdWorld{
		d: d, db: db, keys: keys, q: q, acct: acct, kept: kept, gone: gone, admin: admin,
	}
}

var errRollback = errors.New("rolled back")

// TestAccSpaceHoldRace checks the space hold and the stacks guard as the
// server wires access and stacks: a space delete that starts while a
// transaction holds the space waits for it. When that transaction then
// writes a stack in the space and commits, the stacks guard sees the stack
// and the delete answers 422; when it rolls back, the delete goes ahead.
func TestAccSpaceHoldRace(t *testing.T) {
	ctx := t.Context()
	w := newHoldWorld(t)
	d, q, acct, kept, gone, admin := w.d, w.q, w.acct, w.kept, w.gone, w.admin
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
				_, err = tx.Q(ctx, q).Exec(ctx, `INSERT INTO stacks (account_id, id, slug, space_id,
						name, repository, branch, provider, vendor)
					VALUES ($1, $2, 'infra', $3, 'Infra', 'infra', 'main', 'GITHUB', 'terraform')`,
					acct, kernel.NewULID(), id)
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

// holders is a StackGuard over a scratch table of rows that name stacks,
// standing in for a domain that holds them: a stack with a row would lose
// it on a move, and a deleted stack's rows go with it.
type holders struct{ q tx.Querier }

func (h holders) LostOnMove(ctx context.Context, m stacksapi.StackMove) ([]string, error) {
	var n int
	err := tx.Q(ctx, h.q).QueryRow(ctx, `SELECT count(*) FROM stack_holders
		WHERE account_id = $1 AND stack_id = $2`, m.Account, m.ID).Scan(&n)
	if err != nil || n == 0 {
		return nil, err //nolint:wrapcheck // the test reads it as it is
	}
	return []string{"context held"}, nil
}

func (h holders) Deleting(ctx context.Context, st stacksapi.Stack) error {
	_, err := tx.Q(ctx, h.q).Exec(ctx, `DELETE FROM stack_holders
		WHERE account_id = $1 AND stack_id = $2`, st.Account, st.ID)
	return err //nolint:wrapcheck // as above
}

// TestAccStackHoldRace checks the stack hold and a registered StackGuard
// as the server wires stacks: a stack move or delete that starts while a
// transaction holds the stack waits for it. When that transaction then
// writes a row naming the stack and commits, the move answers 422 and the
// delete removes the row with the stack; when it rolls back, the move
// goes ahead. A stack deleted first is not found by a later hold.
func TestAccStackHoldRace(t *testing.T) {
	ctx := t.Context()
	w := newHoldWorld(t)
	d, q, acct := w.d, w.q, w.acct
	if _, err := q.Exec(
		ctx,
		`CREATE TABLE stack_holders (account_id uuid, stack_id uuid)`,
	); err != nil {
		t.Fatal(err)
	}
	d.Stacks.GuardStacks(holders{q: q})
	in := func(name, space string) api.StackInput {
		return api.StackInput{Name: name, Repository: "infra", Branch: "main", Space: &space}
	}
	ids := map[string]kernel.ULID{}
	for _, name := range []string{"moved", "kept", "deleted"} {
		res, err := d.Stacks.API().StackCreate(w.admin, api.StackCreateRequestObject{
			Body: &api.StackCreateRequest{Input: in(name, "gone")},
		})
		if err != nil {
			t.Fatal(err)
		}
		slug := res.(api.StackCreate201JSONResponse).Body.Id
		var id kernel.ULID
		if err := q.QueryRow(ctx, `SELECT id FROM stacks WHERE account_id = $1 AND slug = $2`,
			acct, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	// race holds the stack slug, starts change on it, and once change
	// waits, writes a row naming the stack and commits, or rolls back; it
	// answers the hold's transaction's error and the change's.
	race := func(slug string, commit bool, change func() error) (held, changed error) {
		holding, release := make(chan struct{}), make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			held = tx.Do(ctx, q, tx.Options{}, func(ctx context.Context) error {
				err := d.Stacks.Stacks().Hold(ctx, acct, ids[slug])
				close(holding)
				if err != nil {
					return err //nolint:wrapcheck // the test reads it as it is
				}
				<-release
				if !commit {
					return errRollback
				}
				_, err = tx.Q(ctx, q).Exec(ctx, `INSERT INTO stack_holders (account_id, stack_id)
					VALUES ($1, $2)`, acct, ids[slug])
				return err //nolint:wrapcheck // as above
			})
		})
		<-holding
		wg.Go(func() { changed = change() })
		awaitLockWait(ctx, t, q)
		close(release)
		wg.Wait()
		return held, changed
	}
	move := func(slug string) func() error {
		return func() error {
			_, err := d.Stacks.API().StackUpdate(w.admin, api.StackUpdateRequestObject{
				StackId: slug, Body: new(in(slug, "kept")),
			})
			return err
		}
	}
	holderRows := func(slug string) int {
		var n int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM stack_holders WHERE stack_id = $1`,
			ids[slug]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	spaceOf := func(slug string) kernel.ULID {
		var space kernel.ULID
		if err := q.QueryRow(ctx, `SELECT space_id FROM stacks WHERE id = $1`,
			ids[slug]).Scan(&space); err != nil {
			t.Fatal(err)
		}
		return space
	}

	held, moved := race("kept", true, move("kept"))
	if held != nil {
		t.Fatalf("the holding transaction = %v", held)
	}
	answer := httptest.NewRecorder()
	problem.ResponseError(answer, nil, moved)
	if answer.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(answer.Body.String(), "Stack kept would no longer reach context held.") {
		t.Errorf("moving a stack a committed row holds = %d %s, want 422 naming it",
			answer.Code, answer.Body)
	}
	if spaceOf("kept") != w.gone {
		t.Error("a refused move moved the stack")
	}

	held, moved = race("moved", false, move("moved"))
	if !errors.Is(held, errRollback) || moved != nil || spaceOf("moved") != w.kept {
		t.Errorf("after a rolled-back hold: hold = %v, move = %v", held, moved)
	}

	held, deleted := race("deleted", true, func() error {
		_, err := d.Stacks.API().StackDelete(w.admin,
			api.StackDeleteRequestObject{StackId: "deleted"})
		return err
	})
	if held != nil || deleted != nil {
		t.Fatalf("hold = %v, delete = %v", held, deleted)
	}
	if n := holderRows("deleted"); n != 0 {
		t.Errorf("%d rows still name the deleted stack", n)
	}
	if n := holderRows("kept"); n != 1 {
		t.Errorf("%d rows name the kept stack, want 1", n)
	}
	if err := tx.Do(ctx, q, tx.Options{}, func(ctx context.Context) error {
		return d.Stacks.Stacks().Hold(ctx, acct, ids["deleted"]) //nolint:wrapcheck // read as is
	}); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("a hold of a deleted stack = %v, want ErrNotFound", err)
	}
}
