package app

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	featuresassembly "github.com/weaveplatform/infraweave/internal/features/assembly"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// before is s without its files from version on, as a release before
// them shipped it.
func before(t *testing.T, s migrate.Stream, version string) migrate.Stream {
	t.Helper()
	files := fstest.MapFS{}
	err := fs.WalkDir(s.Files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path >= version {
			return err
		}
		data, err := fs.ReadFile(s.Files, path)
		files[path] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Files = files
	return s
}

// TestAccEntitlementBackfill checks that an organization created before
// organizations had entitlements holds Enterprise once migrate up applies
// access 00005, and that one that already holds a tier keeps it.
func TestAccEntitlementBackfill(t *testing.T) {
	ctx := t.Context()
	url := sqltest.Empty(t)
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	old := []migrate.Stream{
		schema.Stream(),
		featuresassembly.Stream(),
		before(t, accessassembly.Stream(), "00005"),
	}
	if _, err := migrate.Up(ctx, cfg, "before", old); err != nil {
		t.Fatal(err)
	}
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := featuresassembly.New(featuresassembly.Deps{DB: db}).Seed(ctx); err != nil {
		t.Fatal(err)
	}
	bare, held := kernel.NewOrganizationID(), kernel.NewOrganizationID()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{
			`INSERT INTO organizations (id, slug, name) VALUES ($1, 'bare', 'Bare'), ($2, 'held', 'Held')`,
			[]any{bare, held},
		},
		{
			`INSERT INTO organization_entitlements (organization_id, tier) VALUES ($1, 'Free')`,
			[]any{held},
		},
	} {
		if _, err := db.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := migrate.Up(ctx, cfg, "after", Streams()); err != nil {
		t.Fatal(err)
	}
	for org, want := range map[kernel.OrganizationID]string{bare: "Enterprise", held: "Free"} {
		var tier string
		if err := db.QueryRow(ctx, `SELECT tier FROM organization_entitlements
			WHERE organization_id = $1`, org).Scan(&tier); err != nil || tier != want {
			t.Errorf("organization %s holds %q, %v; want %q", org, tier, err, want)
		}
	}
}
