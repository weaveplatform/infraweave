package pg

import (
	"context"
	"embed"
	"encoding/json"
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
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
)

// StreamName is the vcs stream's name in schema_migrations.
const StreamName = "vcs"

// files are the vcs stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the vcs domain's migration stream, which internal/app
// registers after configuration's.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// Held are the vcs columns that hold other domains' rows: an
// integration's account and space, a manifest form's account, and a run
// token's account and stack.
func Held() []integrity.Held {
	return []integrity.Held{
		{Entity: "account", Table: "vcs_integrations", Columns: []string{"account_id"}},
		{Entity: "account", Table: "vcs_manifests", Columns: []string{"account_id"}},
		{Entity: "account", Table: "vcs_run_tokens", Columns: []string{"account_id"}},
		{
			Entity:  "stack",
			Table:   "vcs_run_tokens",
			Columns: []string{"account_id", "stack_id"},
		},
		{
			Entity:  "space",
			Table:   "vcs_integrations",
			Columns: []string{"account_id", "space_id"},
		},
	}
}

// The sealed columns, each sealed to the integration's row.
//
// ref: decision 0013
var (
	credential = crypt.Column{
		Table: "vcs_integrations", Name: "credentials_sealed", Key: []string{"account_id", "id"},
	}
	webhookSecret = crypt.Column{
		Table: "vcs_integrations", Name: "webhook_secret_sealed", Key: []string{"account_id", "id"},
	}
	previousSecret = crypt.Column{
		Table: "vcs_integrations", Name: "previous_webhook_secret_sealed",
		Key: []string{"account_id", "id"},
	}
)

// SealedColumns are the vcs stream's sealed columns.
func SealedColumns() []crypt.Column {
	return []crypt.Column{credential, webhookSecret, previousSecret}
}

// Sealer seals and opens a column's values; crypt.Keyring is one.
type Sealer interface {
	SealColumn(ctx context.Context, c crypt.Column, pk []string, plaintext []byte) ([]byte, error)
	OpenColumn(ctx context.Context, c crypt.Column, pk []string, sealed []byte) (crypt.Value, error)
}

// Store keeps VCS integrations in PostgreSQL. Every method joins the
// transaction ctx carries.
type Store struct {
	db   tx.Querier
	seal Sealer
}

// New returns the store on db, sealing secrets under seal.
func New(db tx.Querier, seal Sealer) *Store { return &Store{db: db, seal: seal} }

var _ integration.Store = (*Store)(nil)

// columns are an integration's columns as scan reads them, over
// vcs_integrations i.
const columns = `i.account_id, i.id, i.provider, i.slug, i.name, i.space_id, i.description,
	i.labels, i.is_default, i.vcs_checks, i.individual_failures_only,
	i.individual_skip_unchanged, i.use_git_checkout, i.settings, i.created_at, i.updated_at,
	i.version`

// scan reads columns, then into extra.
func scan(row pgx.Row, extra ...any) (integration.Integration, error) {
	var in integration.Integration
	var settings []byte
	err := row.Scan(append([]any{
		&in.Account, &in.ID, &in.Provider, &in.Slug, &in.Name, &in.Space, &in.Description,
		&in.Labels, &in.IsDefault, &in.Checks.Mode, &in.Checks.IndividualFailuresOnly,
		&in.Checks.IndividualSkipUnchanged, &in.UseGitCheckout, &settings, &in.CreatedAt,
		&in.UpdatedAt, &in.Version,
	}, extra...)...)
	if err != nil {
		return integration.Integration{}, err //nolint:wrapcheck // callers name the read
	}
	if err := json.Unmarshal(settings, &in.Settings); err != nil {
		return integration.Integration{}, fmt.Errorf("vcs: settings of %s: %w", in.ID, err)
	}
	return in, nil
}

// key is in's row's primary key, which its secrets are sealed to.
func key(acct kernel.AccountID, id kernel.ULID) []string {
	return []string{acct.UUID(), id.UUID()}
}

// sealed seals each of the secrets in plain to in's row, nil for an
// empty one.
func (s *Store) sealed(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	sec integration.Secrets,
) (cred, hook []byte, err error) {
	for _, c := range []struct {
		col   crypt.Column
		plain []byte
		to    *[]byte
	}{{credential, sec.Credential, &cred}, {webhookSecret, sec.WebhookSecret, &hook}} {
		if len(c.plain) == 0 {
			continue
		}
		if *c.to, err = s.seal.SealColumn(ctx, c.col, key(acct, id), c.plain); err != nil {
			return nil, nil, fmt.Errorf("vcs: seal %s of %s: %w", c.col.Name, id, err)
		}
	}
	return cred, hook, nil
}

// Create writes a new integration.
func (s *Store) Create(
	ctx context.Context,
	in integration.Integration,
	sec integration.Secrets,
) error {
	settings, err := json.Marshal(in.Settings)
	if err != nil {
		return fmt.Errorf("vcs: settings of %s: %w", in.ID, err)
	}
	cred, hook, err := s.sealed(ctx, in.Account, in.ID, sec)
	if err != nil {
		return err
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_integrations (account_id, id, provider,
			slug, name, space_id, description, labels, is_default, vcs_checks,
			individual_failures_only, individual_skip_unchanged, use_git_checkout, settings,
			credentials_sealed, webhook_secret_sealed, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
			$19)`,
		in.Account, in.ID, in.Provider, in.Slug, in.Name, in.Space, in.Description, in.Labels,
		in.IsDefault, in.Checks.Mode, in.Checks.IndividualFailuresOnly,
		in.Checks.IndividualSkipUnchanged, in.UseGitCheckout, settings, cred, hook, in.CreatedAt,
		in.UpdatedAt, in.Version)
	return writeError(in, err)
}

// writeError names a refused write of in: a second default, or a slug
// another integration of the provider holds.
func writeError(in integration.Integration, err error) error {
	if err == nil {
		return nil
	}
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" {
		if pe.ConstraintName == "vcs_integrations_default" {
			return fmt.Errorf("%w: %s", integration.ErrDefaultTaken, in.Provider)
		}
		return fmt.Errorf("%w: %s %s", integration.ErrSlugTaken, in.Provider, in.Slug)
	}
	return fmt.Errorf("vcs: write integration %s: %w", in.ID, sql.Unavailable(err))
}

// Write replaces the integration's changeable fields and sets its
// version, and its secrets when sec is not nil.
func (s *Store) Write(
	ctx context.Context,
	in integration.Integration,
	sec *integration.Secrets,
) error {
	settings, err := json.Marshal(in.Settings)
	if err != nil {
		return fmt.Errorf("vcs: settings of %s: %w", in.ID, err)
	}
	q := tx.Q(ctx, s.db)
	tag, err := q.Exec(ctx, `UPDATE vcs_integrations SET description = $3, labels = $4,
			vcs_checks = $5, individual_failures_only = $6, individual_skip_unchanged = $7,
			use_git_checkout = $8, settings = $9, updated_at = $10, version = $11
		WHERE account_id = $1 AND id = $2`,
		in.Account, in.ID, in.Description, in.Labels, in.Checks.Mode,
		in.Checks.IndividualFailuresOnly, in.Checks.IndividualSkipUnchanged, in.UseGitCheckout,
		settings, in.UpdatedAt, in.Version)
	switch {
	case err != nil:
		return writeError(in, err)
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: %s", integration.ErrNotFound, in.ID)
	}
	if sec == nil {
		return nil
	}
	cred, hook, err := s.sealed(ctx, in.Account, in.ID, *sec)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE vcs_integrations SET credentials_sealed = $3,
			webhook_secret_sealed = $4
		WHERE account_id = $1 AND id = $2`, in.Account, in.ID, cred, hook)
	return writeError(in, err)
}

// Get reads acct's integration id.
func (s *Store) Get(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	lock bool,
) (integration.Integration, error) {
	query := `SELECT ` + columns + ` FROM vcs_integrations i
		WHERE i.account_id = $1 AND i.id = $2`
	if lock {
		query += ` FOR UPDATE`
	}
	return one(scan(tx.Q(ctx, s.db).QueryRow(ctx, query, acct, id)))
}

// Default reads acct's default integration of provider.
func (s *Store) Default(
	ctx context.Context,
	acct kernel.AccountID,
	provider string,
) (integration.Integration, error) {
	return one(scan(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+columns+` FROM vcs_integrations i
		WHERE i.account_id = $1 AND i.provider = $2 AND i.is_default`, acct, provider)))
}

// one names the read of a single integration.
func one(in integration.Integration, err error) (integration.Integration, error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return in, integration.ErrNotFound
	case err != nil:
		return in, fmt.Errorf("vcs: read integration: %w", sql.Unavailable(err))
	}
	return in, nil
}

// Secrets opens acct's integration id's credential and webhook secret.
func (s *Store) Secrets(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (integration.Secrets, error) {
	var cred, hook []byte
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT credentials_sealed, webhook_secret_sealed
		FROM vcs_integrations WHERE account_id = $1 AND id = $2`, acct, id).Scan(&cred, &hook)
	if _, err := one(integration.Integration{}, err); err != nil {
		return integration.Secrets{}, err
	}
	var out integration.Secrets
	for _, c := range []struct {
		col    crypt.Column
		sealed []byte
		to     *[]byte
	}{{credential, cred, &out.Credential}, {webhookSecret, hook, &out.WebhookSecret}} {
		if c.sealed == nil {
			continue
		}
		v, err := s.seal.OpenColumn(ctx, c.col, key(acct, id), c.sealed)
		if err != nil {
			return integration.Secrets{}, fmt.Errorf("vcs: open %s of %s: %w", c.col.Name, id, err)
		}
		*c.to = v.Reveal()
	}
	return out, nil
}

// Delete deletes acct's integration id.
func (s *Store) Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM vcs_integrations WHERE account_id = $1 AND id = $2`, acct, id)
	if err != nil {
		return fmt.Errorf("vcs: delete integration %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// InSpace reports whether an integration of acct lives in space.
func (s *Store) InSpace(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	var in bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM vcs_integrations
		WHERE account_id = $1 AND space_id = $2)`, acct, space).Scan(&in)
	if err != nil {
		return false, fmt.Errorf("vcs: integrations in space %s: %w", space, sql.Unavailable(err))
	}
	return in, nil
}

// Purge deletes up to batch of the deleted account acct's integrations.
func (s *Store) Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	gone := 0
	for _, table := range []string{"vcs_integrations", "vcs_manifests"} {
		tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM `+table+` WHERE account_id = $1
			AND id IN (SELECT id FROM `+table+` WHERE account_id = $1 LIMIT $2)`, acct, batch)
		if err != nil {
			return false, fmt.Errorf("vcs: purge account %s's %s: %w", acct, table,
				sql.Unavailable(err))
		}
		gone += int(tag.RowsAffected())
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM vcs_run_tokens WHERE account_id = $1
		AND (run_id, token_id) IN (SELECT run_id, token_id FROM vcs_run_tokens
			WHERE account_id = $1 LIMIT $2)`, acct, batch)
	if err != nil {
		return false, fmt.Errorf("vcs: purge account %s's vcs_run_tokens: %w", acct,
			sql.Unavailable(err))
	}
	gone += int(tag.RowsAffected())
	return gone == 0, nil
}

// OpenManifest keeps m, a manifest form of acct, until it expires.
func (s *Store) OpenManifest(
	ctx context.Context,
	acct kernel.AccountID,
	m integration.Manifest,
) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO vcs_manifests
		(account_id, id, api_host, user_facing_host, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		acct, m.ID, m.APIHost, m.UserFacingHost, m.ExpiresAt)
	if err != nil {
		return fmt.Errorf("vcs: open manifest %s: %w", m.ID, sql.Unavailable(err))
	}
	return nil
}

// TakeManifest deletes acct's manifest form id and answers it, when it
// has not expired at now; integration.ErrNotFound otherwise.
func (s *Store) TakeManifest(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	now time.Time,
) (integration.Manifest, error) {
	m := integration.Manifest{ID: id}
	err := tx.Q(ctx, s.db).QueryRow(ctx, `DELETE FROM vcs_manifests
		WHERE account_id = $1 AND id = $2
		RETURNING api_host, user_facing_host, expires_at`, acct, id).
		Scan(&m.APIHost, &m.UserFacingHost, &m.ExpiresAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return m, fmt.Errorf("%w: manifest %s", integration.ErrNotFound, id)
	case err != nil:
		return m, fmt.Errorf("vcs: take manifest %s: %w", id, sql.Unavailable(err))
	case !m.ExpiresAt.After(now):
		return m, fmt.Errorf("%w: manifest %s expired", integration.ErrNotFound, id)
	}
	return m, nil
}

// integrationColumn is the SQL of each of integration.Fields. The
// account's space tree belongs to the access domain, so space reads it
// from the scope.
var integrationColumn = map[string]sqlsearch.Column{
	"name":        {Expr: "i.name", Cast: "text", Text: true},
	"description": {Expr: "i.description", Text: true},
	"label":       {Expr: "i.labels", Many: true, Text: true},
	"space":       {Expr: "i.space_id", SpaceTree: true},
	"provider":    {Expr: "i.provider", Cast: "text"},
	"isDefault":   {Expr: "i.is_default"},
	"createdAt":   {Expr: "i.created_at", Cast: "timestamptz"},
	"updatedAt":   {Expr: "i.updated_at", Cast: "timestamptz"},
}

// integrationSearch is the vcs_integrations table as integration.Fields
// search it.
var integrationSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "vcs-integrations", From: "vcs_integrations i", Select: columns,
		Space: "i.space_id", ID: "i.id::text", Order: []search.Order{{Field: "name"}},
	}
	for _, f := range integration.Fields {
		c := integrationColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// searchScope is scope as the search of acct's integrations reads it.
func searchScope(acct kernel.AccountID, scope integration.Scope) sqlsearch.Scope {
	spaces := make([]sqlsearch.SpaceNode, len(scope.Spaces))
	for i, n := range scope.Spaces {
		spaces[i] = sqlsearch.SpaceNode{Space: n.ID, Parent: n.Parent, ID: n.Slug, Display: n.Name}
	}
	out := sqlsearch.Scope{
		Where: "i.account_id = $1", Args: []any{acct}, Reachable: scope.Reachable, Spaces: spaces,
	}
	if scope.Provider != "" {
		out.Where += " AND i.provider = $2"
		out.Args = append(out.Args, scope.Provider)
	}
	return out
}

// Search reads a page of acct's integrations in the scope that in keeps.
func (s *Store) Search(
	ctx context.Context,
	acct kernel.AccountID,
	in search.Input,
	scope integration.Scope,
) (search.Result[integration.Integration], error) {
	out, err := sqlsearch.Find(ctx, tx.Q(ctx, s.db), integrationSearch, searchScope(acct, scope),
		in, scan)
	if err != nil {
		return out, fmt.Errorf("vcs: %w", err)
	}
	return out, nil
}

// Suggest lists the values acct's integrations in the scope that in
// keeps hold.
func (s *Store) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	in search.SuggestionsInput,
	scope integration.Scope,
) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), integrationSearch,
		searchScope(acct, scope), in)
	if err != nil {
		return out, fmt.Errorf("vcs: %w", err)
	}
	return out, nil
}
