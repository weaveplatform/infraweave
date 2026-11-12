package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	opapi "github.com/weaveplatform/infraweave/internal/platform/gen/operator"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// TestAccOrphans checks integrity.orphans over every domain's declared
// columns on the migrated schema: nothing is logged while every held row
// is there, a space deleted outside any purge is logged at warn as one ID
// stacks holds, and a state workspace and lock of no stack and no run are
// logged as the stack and run they hold.
func TestAccOrphans(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	q := db.Primary()
	d, err := newDomains(
		config.Defaults(),
		db,
		testKeyring(t, q),
		nil,
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
				Slug: "orphans", Name: "Orphans",
				Account: opapi.AccountInput{Slug: "orphans", Name: "Orphans"},
			},
		}); err != nil {
		t.Fatal(err)
	}
	var (
		acct kernel.AccountID
		top  kernel.ULID
	)
	if err := q.QueryRow(ctx, `SELECT a.id, s.id FROM accounts a
		JOIN spaces s ON s.account_id = a.id AND s.parent_id IS NULL
		WHERE a.slug = 'orphans'`).Scan(&acct, &top); err != nil {
		t.Fatal(err)
	}
	child := kernel.NewULID()
	for _, s := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO spaces (account_id, id, slug, name, parent_id)
			VALUES ($1, $2, 'child', 'Child', $3)`, []any{acct, child, top}},
		{`INSERT INTO space_closure (account_id, ancestor_id, descendant_id, depth)
			VALUES ($1, $2, $2, 0), ($1, $3, $2, 1)`, []any{acct, child, top}},
		{
			`INSERT INTO stacks (account_id, id, slug, space_id, name, repository, branch,
			provider, vendor)
			VALUES ($1, $2, 'infra', $3, 'Infra', 'infra', 'main', 'GITHUB', 'terraform')`,
			[]any{acct, kernel.NewULID(), child},
		},
	} {
		if _, err := q.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	run := func() []map[string]any {
		var buf bytes.Buffer
		task := orphansTask(q, slog.New(slog.NewJSONHandler(&buf, nil)))
		if task.Kind != CountOrphans || task.Every != orphansEvery {
			t.Errorf("task %+v", task)
		}
		if err := task.Handler(ctx, queue.Job{}); err != nil {
			t.Fatal(err)
		}
		var lines []map[string]any
		for line := range bytes.Lines(buf.Bytes()) {
			var m map[string]any
			if err := json.Unmarshal(line, &m); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, m)
		}
		return lines
	}
	if got := run(); len(got) != 0 {
		t.Errorf("with every held row there: %v", got)
	}
	if _, err := q.Exec(
		ctx,
		`DELETE FROM space_closure WHERE descendant_id = $1`,
		child,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Exec(ctx, `DELETE FROM spaces WHERE id = $1`, child); err != nil {
		t.Fatal(err)
	}
	got := run()
	if len(got) != 1 || got[0]["level"] != "WARN" ||
		got[0]["held"] != "stacks(account_id, space_id)" || got[0]["entity"] != "space" ||
		got[0]["ids"] != float64(1) {
		t.Errorf("after a space is deleted outside a purge: %v", got)
	}

	gone := kernel.NewULID()
	if _, err := q.Exec(ctx, `INSERT INTO state_workspaces (account_id, stack_id, name)
		VALUES ($1, $2, '')`, acct, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Exec(ctx, `INSERT INTO state_locks
		(account_id, stack_id, workspace, lock_id, mode, run_id, info)
		VALUES ($1, $2, '', 'a', 'shared', $3, '{}')`, acct, gone, kernel.NewULID()); err != nil {
		t.Fatal(err)
	}
	lines := run()
	state := make([]string, 0, len(lines))
	for _, line := range lines {
		state = append(state, fmt.Sprint(line["entity"], " ", line["held"], " ", line["ids"]))
	}
	want := []string{
		"space stacks(account_id, space_id) 1",
		"stack state_workspaces(account_id, stack_id) 1",
		"stack state_locks(account_id, stack_id) 1",
		"run state_locks(account_id, run_id) 1",
	}
	if strings.Join(state, "\n") != strings.Join(want, "\n") {
		t.Errorf("with a workspace and lock of no stack or run:\n%s\nwant:\n%s",
			strings.Join(state, "\n"), strings.Join(want, "\n"))
	}
	db.Close()
	if err := orphansTask(q, discard()).Handler(ctx, queue.Job{}); err == nil {
		t.Error("a closed database counted")
	}
}
