package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/audit"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// StreamName is the cloud stream's name in schema_migrations.
const StreamName = "cloud"

// files are the cloud stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the cloud domain's migration stream, which internal/app
// registers after state's.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// Held are the cloud columns that hold other domains' rows: an
// integration's account and space, and the stack an attachment names.
func Held() []integrity.Held {
	return []integrity.Held{
		{Entity: "account", Table: "aws_integrations", Columns: []string{"account_id"}},
		{Entity: "space", Table: "aws_integrations", Columns: []string{"account_id", "space_id"}},
		{
			Entity:  "stack",
			Table:   "aws_integration_attachments",
			Columns: []string{"account_id", "stack_id"},
		},
	}
}

// DB is the pool the store queries, and begins a transaction on for a
// write of several statements that its caller has not.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Store keeps AWS integrations and their attachments in PostgreSQL.
// Every method joins the transaction ctx carries.
type Store struct{ db DB }

// New returns the store on db.
func New(db DB) *Store { return &Store{db: db} }

var _ core.Store = (*Store)(nil)

// columns are an integration's columns as scan reads them.
const columns = `account_id, id, space_id, name, role_arn, region, duration_seconds,
	generate_credentials_in_worker, external_id, labels, autoattach_enabled, tag_assume_role,
	legacy, created_at, updated_at, version`

func scan(row pgx.Row) (core.Integration, error) {
	var i core.Integration
	err := row.Scan(&i.Account, &i.ID, &i.Space, &i.Name, &i.RoleARN, &i.Region, &i.Duration,
		&i.Worker, &i.ExternalID, &i.Labels, &i.Autoattach, &i.TagSession, &i.Legacy,
		&i.CreatedAt, &i.UpdatedAt, &i.Version)
	return i, err //nolint:wrapcheck // callers name the read
}

// The constraints whose violation is a refusal rather than a failure.
const (
	uniqueName        = "aws_integrations_name"
	uniqueAttachment  = "aws_integration_attachments_pair"
	uniqueReadOfStack = "aws_integration_attachments_read"
	uniqueWrite       = "aws_integration_attachments_write"
)

// violated is the constraint err violates as a unique key, or empty.
func violated(err error) string {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" {
		return pe.ConstraintName
	}
	return ""
}

// Create writes a new integration.
func (s *Store) Create(ctx context.Context, i core.Integration) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO aws_integrations (`+columns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		i.Account, i.ID, i.Space, i.Name, i.RoleARN, i.Region, i.Duration, i.Worker, i.ExternalID,
		i.Labels, i.Autoattach, i.TagSession, i.Legacy, i.CreatedAt, i.UpdatedAt, i.Version)
	return written(err, i)
}

// written answers a write of i's error: a *core.NameTaken for its name.
func written(err error, i core.Integration) error {
	switch {
	case violated(err) == uniqueName:
		return &core.NameTaken{Name: i.Name}
	case err != nil:
		return fmt.Errorf("cloud: write AWS integration %s: %w", i.ID, sql.Unavailable(err))
	}
	return nil
}

// Write replaces the integration's fields and sets its version.
func (s *Store) Write(ctx context.Context, i core.Integration) error {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE aws_integrations SET space_id = $3, name = $4,
			role_arn = $5, region = $6, duration_seconds = $7, generate_credentials_in_worker = $8,
			external_id = $9, labels = $10, autoattach_enabled = $11, tag_assume_role = $12,
			updated_at = $13, version = $14
		WHERE account_id = $1 AND id = $2`,
		i.Account, i.ID, i.Space, i.Name, i.RoleARN, i.Region, i.Duration, i.Worker, i.ExternalID,
		i.Labels, i.Autoattach, i.TagSession, i.UpdatedAt, i.Version)
	if err := written(err, i); err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: AWS integration %s", core.ErrNotFound, i.ID)
	}
	return nil
}

// locks are the clauses each core.Lock reads a row with.
var locks = map[core.Lock]string{core.NoLock: "", core.Share: " FOR SHARE", core.Update: " FOR UPDATE"}

// Get reads acct's integration id.
func (s *Store) Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock core.Lock) (core.Integration, error) {
	return s.one(ctx, `SELECT `+columns+` FROM aws_integrations
		WHERE account_id = $1 AND id = $2`+locks[lock], id.String(), acct, id)
}

// ByName reads acct's integration name.
func (s *Store) ByName(ctx context.Context, acct kernel.AccountID, name string) (core.Integration, error) {
	return s.one(ctx, `SELECT `+columns+` FROM aws_integrations
		WHERE account_id = $1 AND name = $2`, name, acct, name)
}

// Legacy reads the legacy integration attached to acct's stack.
func (s *Store) Legacy(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) (core.Integration, error) {
	return s.one(ctx, `SELECT `+columns+` FROM aws_integrations i
		WHERE account_id = $1 AND legacy AND EXISTS (SELECT 1 FROM aws_integration_attachments a
			WHERE a.account_id = i.account_id AND a.integration_id = i.id AND a.stack_id = $2)`,
		"legacy of stack "+stack.String(), acct, stack)
}

func (s *Store) one(ctx context.Context, q, what string, args ...any) (core.Integration, error) {
	i, err := scan(tx.Q(ctx, s.db).QueryRow(ctx, q, args...))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.Integration{}, fmt.Errorf("%w: AWS integration %s", core.ErrNotFound, what)
	case err != nil:
		return core.Integration{}, fmt.Errorf("cloud: read AWS integration %s: %w", what,
			sql.Unavailable(err))
	}
	return i, nil
}

func (s *Store) many(ctx context.Context, q string, args ...any) ([]core.Integration, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("cloud: read AWS integrations: %w", sql.Unavailable(err))
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Integration, error) {
		return scan(row)
	})
	if err != nil {
		return nil, fmt.Errorf("cloud: read AWS integrations: %w", sql.Unavailable(err))
	}
	return all, nil
}

// ByIDs reads those of acct's integrations ids that it has, keyed by ID.
func (s *Store) ByIDs(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]core.Integration, error) {
	all, err := s.many(ctx, `SELECT `+columns+` FROM aws_integrations
		WHERE account_id = $1 AND id = ANY($2)`, acct, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[kernel.ULID]core.Integration, len(all))
	for _, i := range all {
		out[i.ID] = i
	}
	return out, nil
}

// List reads a page of acct's integrations in spaces, by name in byte
// order, which the names' unique key keeps total.
func (s *Store) List(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, legacy bool, page core.Page) ([]core.Integration, bool, error) {
	all, err := s.many(ctx, `SELECT `+columns+` FROM aws_integrations
		WHERE account_id = $1 AND space_id = ANY($2) AND ($3 OR NOT legacy)
			AND name COLLATE "C" > $4
		ORDER BY name COLLATE "C" LIMIT $5`, acct, spaces, legacy, page.After, page.Limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(all) > page.Limit {
		return all[:page.Limit], true, nil
	}
	return all, false, nil
}

// Labelled reads acct's integrations in spaces with auto-attachment
// enabled that carry any of labels, through the labels' GIN index.
func (s *Store) Labelled(ctx context.Context, acct kernel.AccountID, spaces []kernel.ULID, labels []string) ([]core.Integration, error) {
	return s.many(ctx, `SELECT `+columns+` FROM aws_integrations
		WHERE account_id = $1 AND space_id = ANY($2) AND autoattach_enabled AND NOT legacy
			AND labels && $3::text[]
		ORDER BY name COLLATE "C"`, acct, spaces, labels)
}

// InSpace reports whether an integration of acct lives in space.
func (s *Store) InSpace(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error) {
	var in bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM aws_integrations
		WHERE account_id = $1 AND space_id = $2)`, acct, space).Scan(&in)
	if err != nil {
		return false, fmt.Errorf("cloud: read the AWS integrations of space %s: %w", space,
			sql.Unavailable(err))
	}
	return in, nil
}

// Delete deletes acct's integration id; its attachments cascade.
func (s *Store) Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM aws_integrations WHERE account_id = $1 AND id = $2`, acct, id)
	if err != nil {
		return fmt.Errorf("cloud: delete AWS integration %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// attachmentColumns are an attachment's columns as scanAttachment reads
// them.
const attachmentColumns = `account_id, id, integration_id, stack_id, read, write, created_at`

func scanAttachment(row pgx.Row) (core.Attachment, error) {
	var a core.Attachment
	err := row.Scan(&a.Account, &a.ID, &a.Integration, &a.Stack, &a.Read, &a.Write, &a.CreatedAt)
	return a, err //nolint:wrapcheck // callers name the read
}

// conflict answers a write of an attachment of stack's error: a
// *core.Conflict for each unique key it breaks.
func conflict(err error, stack kernel.ULID) error {
	switch violated(err) {
	case "":
	case uniqueReadOfStack:
		return &core.Conflict{Detail: "the stack has a read attachment already; " +
			"a stack has one read and one write attachment"}
	case uniqueWrite:
		return &core.Conflict{Detail: "the stack has a write attachment already; " +
			"a stack has one read and one write attachment"}
	case uniqueAttachment:
		return &core.Conflict{Detail: "the AWS integration is attached to the stack already"}
	}
	if err != nil {
		return fmt.Errorf("cloud: attach to stack %s: %w", stack, sql.Unavailable(err))
	}
	return nil
}

// Attach writes a.
func (s *Store) Attach(ctx context.Context, a core.Attachment) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO aws_integration_attachments (`+
		attachmentColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.Account, a.ID, a.Integration, a.Stack, a.Read, a.Write, a.CreatedAt)
	return conflict(err, a.Stack)
}

// SetSides sets the sides of acct's attachment id.
func (s *Store) SetSides(ctx context.Context, acct kernel.AccountID, id kernel.ULID, read, write bool) error {
	var stack kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `UPDATE aws_integration_attachments
		SET read = $3, write = $4 WHERE account_id = $1 AND id = $2 RETURNING stack_id`,
		acct, id, read, write).Scan(&stack)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: attachment %s", core.ErrNotFound, id)
	}
	return conflict(err, stack)
}

// Attachment reads acct's attachment id.
func (s *Store) Attachment(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (core.Attachment, error) {
	a, err := scanAttachment(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+attachmentColumns+`
		FROM aws_integration_attachments WHERE account_id = $1 AND id = $2`, acct, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.Attachment{}, fmt.Errorf("%w: attachment %s", core.ErrNotFound, id)
	case err != nil:
		return core.Attachment{}, fmt.Errorf("cloud: read attachment %s: %w", id,
			sql.Unavailable(err))
	}
	return a, nil
}

// Detach deletes acct's attachment id.
func (s *Store) Detach(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	_, err := tx.Q(ctx, s.db).Exec(ctx,
		`DELETE FROM aws_integration_attachments WHERE account_id = $1 AND id = $2`, acct, id)
	if err != nil {
		return fmt.Errorf("cloud: detach %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// Attachments reads the attachments of integrations, or of stacks when
// byStack is set, keyed by that ID, oldest first.
func (s *Store) Attachments(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID, byStack bool) (map[kernel.ULID][]core.Attachment, error) {
	key := "integration_id"
	if byStack {
		key = "stack_id"
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+attachmentColumns+`
		FROM aws_integration_attachments WHERE account_id = $1 AND `+key+` = ANY($2)
		ORDER BY created_at, id`, acct, ids)
	if err != nil {
		return nil, fmt.Errorf("cloud: read attachments: %w", sql.Unavailable(err))
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Attachment, error) {
		return scanAttachment(row)
	})
	if err != nil {
		return nil, fmt.Errorf("cloud: read attachments: %w", sql.Unavailable(err))
	}
	out := map[kernel.ULID][]core.Attachment{}
	for _, a := range all {
		k := a.Integration
		if byStack {
			k = a.Stack
		}
		out[k] = append(out[k], a)
	}
	return out, nil
}

// AttachedStacks lists the stacks an attachment of acct names.
func (s *Store) AttachedStacks(ctx context.Context, acct kernel.AccountID) ([]kernel.ULID, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT DISTINCT stack_id
		FROM aws_integration_attachments WHERE account_id = $1`, acct)
	if err != nil {
		return nil, fmt.Errorf("cloud: read attached stacks: %w", sql.Unavailable(err))
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
	if err != nil {
		return nil, fmt.Errorf("cloud: read attached stacks: %w", sql.Unavailable(err))
	}
	return ids, nil
}

// DeleteStack deletes stack's legacy integration, then its attachments.
func (s *Store) DeleteStack(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error {
	q := tx.Q(ctx, s.db)
	for _, stmt := range []string{
		`DELETE FROM aws_integrations i WHERE account_id = $1 AND legacy AND EXISTS (
			SELECT 1 FROM aws_integration_attachments a
			WHERE a.account_id = i.account_id AND a.integration_id = i.id AND a.stack_id = $2)`,
		`DELETE FROM aws_integration_attachments WHERE account_id = $1 AND stack_id = $2`,
	} {
		if _, err := q.Exec(ctx, stmt, acct, stack); err != nil {
			return fmt.Errorf("cloud: delete what stack %s holds: %w", stack,
				sql.Unavailable(err))
		}
	}
	return nil
}

// Audit records e on its account's trail, in the context's transaction.
func (s *Store) Audit(ctx context.Context, e kernel.AuditEntry) error {
	_, err := audit.Record(ctx, e)
	return err //nolint:wrapcheck // the trail names the write
}

// purged are the tables Purge empties of an account, in order: the rows
// that name an integration before it.
var purged = []string{"aws_integration_attachments", "aws_integrations"}

// Purge deletes up to batch of the deleted account acct's rows from the
// first table of purged that holds any, and reports true once none does.
func (s *Store) Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	for _, table := range purged {
		tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM `+table+` WHERE account_id = $1 AND id IN (
			SELECT id FROM `+table+` WHERE account_id = $1 LIMIT $2)`, acct, batch)
		if err != nil {
			return false, fmt.Errorf("cloud: purge account %s's %s: %w", acct, table,
				sql.Unavailable(err))
		}
		if tag.RowsAffected() > 0 {
			return false, nil
		}
	}
	return true, nil
}
