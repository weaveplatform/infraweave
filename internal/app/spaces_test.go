package app

import (
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

// TestAccSpaceBackfill checks that each account created before spaces
// existed has its top space, with the account's ID and slug, and legacy
// beneath it once migrate up applies access 00009, as account creation
// seeds them.
func TestAccSpaceBackfill(t *testing.T) {
	ctx := t.Context()
	url := sqltest.Empty(t)
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	old := []migrate.Stream{
		schema.Stream(),
		featuresassembly.Stream(),
		before(t, accessassembly.Stream(), "00009"),
	}
	if _, err := migrate.Up(ctx, cfg, "before", old); err != nil {
		t.Fatal(err)
	}
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	org := kernel.NewOrganizationID()
	accts := map[kernel.AccountID]string{
		kernel.NewAccountID(): "gavin",
		kernel.NewAccountID(): "belson",
	}
	if _, err := db.Exec(ctx, `INSERT INTO organizations (id, slug, name)
		VALUES ($1, 'hooli', 'Hooli')`, org); err != nil {
		t.Fatal(err)
	}
	for id, slug := range accts {
		if _, err := db.Exec(ctx, `INSERT INTO accounts (organization_id, id, slug, name)
			VALUES ($1, $2, $3, $3)`, org, id, slug); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := migrate.Up(ctx, cfg, "after", Streams()); err != nil {
		t.Fatal(err)
	}
	for id, slug := range accts {
		var top, legacy kernel.ULID
		if err := db.QueryRow(ctx, `SELECT id FROM spaces
			WHERE account_id = $1 AND slug = $2 AND parent_id IS NULL`, id, slug).Scan(&top); err != nil ||
			top != id.ULID {
			t.Errorf("account %s's top space = %s, %v", slug, top, err)
		}
		if err := db.QueryRow(ctx, `SELECT id FROM spaces
			WHERE account_id = $1 AND slug = 'legacy' AND parent_id = $1`, id).Scan(&legacy); err != nil {
			t.Errorf("account %s's legacy: %v", slug, err)
		}
		rows, err := db.Query(ctx, `SELECT ancestor_id, descendant_id, depth FROM space_closure
			WHERE account_id = $1 ORDER BY depth, descendant_id = $1 DESC`, id)
		if err != nil {
			t.Fatal(err)
		}
		type row struct {
			ancestor, descendant kernel.ULID
			depth                int
		}
		got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			return x, r.Scan(&x.ancestor, &x.descendant, &x.depth)
		})
		want := []row{{top, top, 0}, {legacy, legacy, 0}, {top, legacy, 1}}
		if err != nil || len(got) != len(want) {
			t.Fatalf("account %s's closure = %v, %v", slug, got, err)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("account %s's closure row %d = %v, want %v", slug, i, got[i], want[i])
			}
		}
	}
}
