package pg

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vendorsapi "github.com/weaveplatform/infraweave/internal/vendors/api"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index"
)

// StreamName is the vendors stream's name in schema_migrations.
const StreamName = "vendors"

// files are the vendors stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the vendors domain's migration stream, which internal/app
// registers after access'.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// Store keeps the vendors' releases in PostgreSQL.
type Store struct{ db tx.Querier }

// New is a Store on db.
func New(db tx.Querier) *Store { return &Store{db: db} }

const add = `
INSERT INTO vendor_releases (vendor, version, published_at)
SELECT $1, r.version, r.published_at
FROM unnest($2::text[], $3::timestamptz[]) AS r(version, published_at)
ON CONFLICT (vendor, version) DO NOTHING`

// Add stores the releases of kind it does not hold, and reports how many
// it added. A release it holds keeps its time.
func (s *Store) Add(ctx context.Context, kind vendorsapi.Vendor, rs []index.Release) (int, error) {
	versions := make([]string, len(rs))
	published := make([]*time.Time, len(rs))
	for i, r := range rs {
		versions[i] = r.Version.String()
		if !r.Published.IsZero() {
			published[i] = &r.Published
		}
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, add, string(kind), versions, published)
	if err != nil {
		return 0, fmt.Errorf("add %s releases: %w", kind, sql.Unavailable(err))
	}
	return int(tag.RowsAffected()), nil
}

// Versions are the stored releases of kind, in no order.
func (s *Store) Versions(ctx context.Context, kind vendorsapi.Vendor) ([]string, error) {
	// A failed Query returns rows that carry its error to CollectRows.
	rows, _ := tx.Q(ctx, s.db).Query(ctx,
		`SELECT version FROM vendor_releases WHERE vendor = $1`, string(kind))
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("read %s releases: %w", kind, sql.Unavailable(err))
	}
	return out, nil
}
