package app

import (
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	featuresassembly "github.com/weaveplatform/infraweave/internal/features/assembly"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// TestAccRoleBackfill checks that an account created before roles existed
// holds the five system roles once migrate up applies access 00010, and
// that no binding is inferred for it.
func TestAccRoleBackfill(t *testing.T) {
	ctx := t.Context()
	url := sqltest.Empty(t)
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	old := []migrate.Stream{
		schema.Stream(),
		featuresassembly.Stream(),
		before(t, accessassembly.Stream(), "00010"),
	}
	if _, err := migrate.Up(ctx, cfg, "before", old); err != nil {
		t.Fatal(err)
	}
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	org, acct := kernel.NewOrganizationID(), kernel.NewAccountID()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (id, slug, name) VALUES ($1, 'hooli', 'Hooli')`, []any{org}},
		{
			`INSERT INTO accounts (organization_id, id, slug, name, claimed_at)
			VALUES ($1, $2, 'hooli', 'Hooli', now())`,
			[]any{org, acct},
		},
	} {
		if _, err := db.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := migrate.Up(ctx, cfg, "after", Streams()); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT slug FROM roles
		WHERE account_id = $1 AND is_system ORDER BY slug`, acct)
	if err != nil {
		t.Fatal(err)
	}
	slugs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	want := []string{
		"drift-detection-manager",
		"space-admin",
		"space-reader",
		"space-writer",
		"worker-pool-controller",
	}
	if err != nil || !slices.Equal(slugs, want) {
		t.Errorf("system roles = %v, %v; want %v", slugs, err, want)
	}
	var bindings int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM role_bindings WHERE account_id = $1`, acct).
		Scan(&bindings); err != nil || bindings != 0 {
		t.Errorf("bindings = %d, %v; want none inferred", bindings, err)
	}
}
