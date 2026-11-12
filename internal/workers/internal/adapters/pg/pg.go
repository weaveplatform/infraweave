package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/pool"
)

// StreamName is the workers stream's name in schema_migrations.
const StreamName = "workers"

// files are the workers stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the workers domain's migration stream, which internal/app
// registers after access'.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// Held are the workers columns that hold other domains' rows: each
// table's account, and a pool's space.
func Held() []integrity.Held {
	return []integrity.Held{
		{Entity: "account", Table: "worker_pools", Columns: []string{"account_id"}},
		{Entity: "account", Table: "workers", Columns: []string{"account_id"}},
		{Entity: "account", Table: "worker_nonces", Columns: []string{"account_id"}},
		{Entity: "space", Table: "worker_pools", Columns: []string{"account_id", "space_id"}},
	}
}

// authorityKey is the pool CA's private key, sealed to its row.
//
// ref: decision 0013
var authorityKey = crypt.Column{
	Table: "worker_pool_ca", Name: "private_key_sealed", Key: []string{"id"},
}

// authorityRow is the pool CA's row's primary key.
var authorityRow = []string{"1"}

// SealedColumns are the workers stream's sealed columns.
func SealedColumns() []crypt.Column { return []crypt.Column{authorityKey} }

// Sealer seals and opens a column's values; crypt.Keyring is one.
type Sealer interface {
	SealColumn(ctx context.Context, c crypt.Column, pk []string, plaintext []byte) ([]byte, error)
	OpenColumn(ctx context.Context, c crypt.Column, pk []string, sealed []byte) (crypt.Value, error)
}

// Store keeps worker pools in PostgreSQL. Every method joins the
// transaction ctx carries.
type Store struct {
	db   tx.Querier
	seal Sealer
}

// New returns the store on db, sealing the pool CA's key under seal.
func New(db tx.Querier, seal Sealer) *Store { return &Store{db: db, seal: seal} }

var _ pool.Store = (*Store)(nil)

// Authority reads the pool CA, creating it with mint when there is none,
// and takes its next serial, which locks the CA's row until ctx's
// transaction ends. Replicas that create it together keep the first.
func (s *Store) Authority(
	ctx context.Context,
	mint func() (pool.Authority, error),
) (pool.Authority, error) {
	var out pool.Authority
	var sealed []byte
	take := func() error {
		return tx.Q(ctx, s.db).QueryRow(ctx, `UPDATE worker_pool_ca
			SET next_serial = next_serial + 1
			WHERE id = 1 RETURNING certificate, private_key_sealed, next_serial - 1`).
			Scan(&out.Certificate, &sealed, &out.Serial)
	}
	err := take()
	if errors.Is(err, pgx.ErrNoRows) {
		if err := s.mint(ctx, mint); err != nil {
			return out, err
		}
		err = take()
	}
	if err != nil {
		return out, fmt.Errorf("workers: take a pool CA serial: %w", sql.Unavailable(err))
	}
	v, err := s.seal.OpenColumn(ctx, authorityKey, authorityRow, sealed)
	if err != nil {
		return out, fmt.Errorf("workers: open the pool CA key: %w", err)
	}
	out.Key = v.Reveal()
	return out, nil
}

// mint creates the pool CA with mint unless another transaction has.
// The CA's own certificate has serial 1, so the pools' start at 2.
func (s *Store) mint(ctx context.Context, mint func() (pool.Authority, error)) error {
	a, err := mint()
	if err != nil {
		return fmt.Errorf("workers: create the pool CA: %w", err)
	}
	sealed, err := s.seal.SealColumn(ctx, authorityKey, authorityRow, a.Key)
	if err != nil {
		return fmt.Errorf("workers: seal the pool CA key: %w", err)
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO worker_pool_ca
		(id, certificate, private_key_sealed, next_serial) VALUES (1, $1, $2, 2)
		ON CONFLICT (id) DO NOTHING`, a.Certificate, sealed)
	if err != nil {
		return fmt.Errorf("workers: create the pool CA: %w", sql.Unavailable(err))
	}
	return nil
}

// columns are a pool's columns as scan reads them, over worker_pools p.
const columns = `p.account_id, p.id, p.name, p.space_id, p.description, p.labels,
	p.preparing_timeout_seconds, p.drift_detection_run_limit, p.certificate, p.serial,
	p.created_at, p.updated_at, p.version`

// scan reads columns, then into extra.
func scan(row pgx.Row, extra ...any) (pool.Pool, error) {
	var p pool.Pool
	var preparing *int32
	err := row.Scan(append([]any{
		&p.Account, &p.ID, &p.Name, &p.Space, &p.Description, &p.Labels, &preparing,
		&p.DriftDetectionRunLimit, &p.Certificate, &p.Serial, &p.CreatedAt, &p.UpdatedAt,
		&p.Version,
	}, extra...)...)
	if preparing != nil {
		p.PreparingTimeout = time.Duration(*preparing) * time.Second
	}
	return p, err //nolint:wrapcheck // callers name the read
}

// seconds is d as preparing_timeout_seconds holds it, NULL for zero.
func seconds(d time.Duration) *int32 {
	if d == 0 {
		return nil
	}
	s := int32(d / time.Second) //nolint:gosec // the API's seconds are int32
	return &s
}

// Create writes a new pool.
func (s *Store) Create(ctx context.Context, p pool.Pool) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO worker_pools (account_id, id, name,
			space_id, description, labels, preparing_timeout_seconds, drift_detection_run_limit,
			certificate, serial, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		p.Account, p.ID, p.Name, p.Space, p.Description, p.Labels, seconds(p.PreparingTimeout),
		p.DriftDetectionRunLimit, p.Certificate, p.Serial, p.CreatedAt, p.UpdatedAt, p.Version)
	return writeError(p, err)
}

// writeError names a refused write of p: a name another pool of its
// account has.
func writeError(p pool.Pool, err error) error {
	if err == nil {
		return nil
	}
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" {
		return fmt.Errorf("%w: %s", pool.ErrNameTaken, p.Name)
	}
	return fmt.Errorf("workers: write worker pool %s: %w", p.ID, sql.Unavailable(err))
}

// Write replaces the pool's fields and sets its version.
func (s *Store) Write(ctx context.Context, p pool.Pool) error {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE worker_pools SET name = $3, space_id = $4,
			description = $5, labels = $6, preparing_timeout_seconds = $7,
			drift_detection_run_limit = $8, certificate = $9, serial = $10, updated_at = $11,
			version = $12
		WHERE account_id = $1 AND id = $2`,
		p.Account, p.ID, p.Name, p.Space, p.Description, p.Labels, seconds(p.PreparingTimeout),
		p.DriftDetectionRunLimit, p.Certificate, p.Serial, p.UpdatedAt, p.Version)
	switch {
	case err != nil:
		return writeError(p, err)
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: %s", pool.ErrNotFound, p.ID)
	}
	return nil
}

// Get reads acct's pool id.
func (s *Store) Get(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	lock bool,
) (pool.Pool, error) {
	query := `SELECT ` + columns + ` FROM worker_pools p WHERE p.account_id = $1 AND p.id = $2`
	if lock {
		query += ` FOR UPDATE`
	}
	p, err := scan(tx.Q(ctx, s.db).QueryRow(ctx, query, acct, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return p, fmt.Errorf("%w: %s", pool.ErrNotFound, id)
	case err != nil:
		return p, fmt.Errorf("workers: read worker pool %s: %w", id, sql.Unavailable(err))
	}
	return p, nil
}

// Delete deletes acct's pool id with its workers and their nonces.
func (s *Store) Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	for _, stmt := range []string{
		`DELETE FROM worker_nonces WHERE account_id = $1 AND pool_id = $2`,
		`DELETE FROM workers WHERE account_id = $1 AND pool_id = $2`,
		`DELETE FROM worker_pools WHERE account_id = $1 AND id = $2`,
	} {
		if _, err := tx.Q(ctx, s.db).Exec(ctx, stmt, acct, id); err != nil {
			return fmt.Errorf("workers: delete worker pool %s: %w", id, sql.Unavailable(err))
		}
	}
	return nil
}

// InSpace reports whether a pool of acct lives in space.
func (s *Store) InSpace(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	var in bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM worker_pools
		WHERE account_id = $1 AND space_id = $2)`, acct, space).Scan(&in)
	if err != nil {
		return false, fmt.Errorf("workers: pools in space %s: %w", space, sql.Unavailable(err))
	}
	return in, nil
}

// purged are the tables Purge empties of an account, each with the key
// that names a row within the account, a pool's rows last.
var purged = []struct{ table, key string }{
	{"worker_nonces", "pool_id, jti"},
	{"workers", "id"},
	{"worker_pools", "id"},
}

// Purge deletes up to batch of the deleted account acct's rows from the
// first of its nonces, workers and pools that has any, and reports
// whether none is left.
func (s *Store) Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	for _, p := range purged {
		tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM `+p.table+
			` WHERE account_id = $1 AND (`+p.key+`) IN (
			SELECT `+p.key+` FROM `+p.table+` WHERE account_id = $1 LIMIT $2)`, acct, batch)
		if err != nil {
			return false, fmt.Errorf("workers: purge account %s's %s: %w", acct, p.table,
				sql.Unavailable(err))
		}
		if tag.RowsAffected() > 0 {
			return false, nil
		}
	}
	return true, nil
}

// poolColumn is the SQL of each of pool.Fields. The account's space tree
// belongs to the access domain, so space reads it from the scope.
var poolColumn = map[string]sqlsearch.Column{
	"name":        {Expr: "p.name", Cast: "text", Text: true},
	"description": {Expr: "p.description", Text: true},
	"label":       {Expr: "p.labels", Many: true, Text: true},
	"space":       {Expr: "p.space_id", SpaceTree: true},
	"createdAt":   {Expr: "p.created_at", Cast: "timestamptz"},
	"updatedAt":   {Expr: "p.updated_at", Cast: "timestamptz"},
}

// poolSearch is the worker_pools table as pool.Fields search it.
var poolSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "worker-pools", From: "worker_pools p", Select: columns,
		Space: "p.space_id", ID: "p.id::text", Order: []search.Order{{Field: "name"}},
	}
	for _, f := range pool.Fields {
		c := poolColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// searchScope is scope as the search of acct's pools reads it.
func searchScope(acct kernel.AccountID, scope pool.Scope) sqlsearch.Scope {
	spaces := make([]sqlsearch.SpaceNode, len(scope.Spaces))
	for i, n := range scope.Spaces {
		spaces[i] = sqlsearch.SpaceNode{Space: n.ID, Parent: n.Parent, ID: n.Slug, Display: n.Name}
	}
	return sqlsearch.Scope{
		Where: "p.account_id = $1", Args: []any{acct}, Reachable: scope.Reachable, Spaces: spaces,
	}
}

// Search reads a page of acct's pools in the scope that in keeps.
func (s *Store) Search(
	ctx context.Context,
	acct kernel.AccountID,
	in search.Input,
	scope pool.Scope,
) (search.Result[pool.Pool], error) {
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), poolSearch, searchScope(acct, scope), in, scan)
	if err != nil {
		return out, fmt.Errorf("workers: %w", err)
	}
	return out, nil
}

// Suggest lists the values acct's pools in the scope that in keeps hold.
func (s *Store) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	in search.SuggestionsInput,
	scope pool.Scope,
) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), poolSearch, searchScope(acct, scope), in)
	if err != nil {
		return out, fmt.Errorf("workers: %w", err)
	}
	return out, nil
}
