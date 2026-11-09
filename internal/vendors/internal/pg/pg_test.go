package pg_test

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/iac/version"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/pg"
)

// TestMain migrates the platform stream and the vendors stream alone.
func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream(), pg.Stream())) }

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func release(s string, published time.Time) index.Release {
	v, err := version.Parse(s)
	if err != nil {
		panic(err)
	}
	return index.Release{Version: v, Published: published}
}

// TestAccStore adds only the releases it does not hold, keeps each
// vendor's apart, and keeps a stored release's time.
func TestAccStore(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	s := pg.New(db)
	at := time.Date(2023, 9, 7, 20, 27, 48, 0, time.UTC)
	n, err := s.Add(ctx, vendorsapi.Terraform, []index.Release{
		release("1.5.7", at), release("1.6.0", time.Time{}),
	})
	if err != nil || n != 2 {
		t.Fatalf("Add = %d, %v", n, err)
	}
	n, err = s.Add(ctx, vendorsapi.Terraform, []index.Release{
		release("1.5.7", at.Add(time.Hour)), release("1.6.0", time.Time{}), release("1.6.1", at),
	})
	if err != nil || n != 1 {
		t.Fatalf("Add again = %d, %v", n, err)
	}
	if n, err := s.Add(ctx, vendorsapi.Terraform, nil); err != nil || n != 0 {
		t.Errorf("Add of nothing = %d, %v", n, err)
	}
	got, err := s.Versions(ctx, vendorsapi.Terraform)
	slices.Sort(got)
	if err != nil || !slices.Equal(got, []string{"1.5.7", "1.6.0", "1.6.1"}) {
		t.Errorf("Versions = %v, %v", got, err)
	}
	if got, err := s.Versions(ctx, vendorsapi.OpenTofu); err != nil || len(got) != 0 {
		t.Errorf("OpenTofu's versions = %v, %v", got, err)
	}
	var stored *time.Time
	if err := db.QueryRow(ctx, `SELECT published_at FROM vendor_releases
		WHERE vendor = 'terraform' AND version = '1.5.7'`).Scan(&stored); err != nil ||
		stored == nil || !stored.Equal(at) {
		t.Errorf("1.5.7 published %v, %v; want %v", stored, err, at)
	}
	if err := db.QueryRow(ctx, `SELECT published_at FROM vendor_releases
		WHERE vendor = 'terraform' AND version = '1.6.0'`).Scan(&stored); err != nil || stored != nil {
		t.Errorf("1.6.0 published %v, %v; want null", stored, err)
	}
}

// TestAccStoreUnavailable fails every read and write on a closed pool.
func TestAccStoreUnavailable(t *testing.T) {
	db := pool(t)
	db.Close()
	s := pg.New(db)
	if _, err := s.Add(t.Context(), vendorsapi.Terraform, nil); err == nil {
		t.Error("Add on a closed pool succeeded")
	}
	if _, err := s.Versions(t.Context(), vendorsapi.Terraform); err == nil {
		t.Error("Versions on a closed pool succeeded")
	}
}
