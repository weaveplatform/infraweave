package pg

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// StreamName is the configuration stream's name in schema_migrations.
const StreamName = "configuration"

// files are the configuration stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the configuration domain's migration stream, which
// internal/app registers after stacks'.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// Held are the configuration columns that hold other domains' rows: a
// context's account and space, and the stacks elements and attachments
// name.
func Held() []integrity.Held {
	return []integrity.Held{
		{Entity: "account", Table: "contexts", Columns: []string{"account_id"}},
		{Entity: "space", Table: "contexts", Columns: []string{"account_id", "space_id"}},
		{Entity: "stack", Table: "config_elements", Columns: []string{"account_id", "stack_id"}},
		{
			Entity:  "stack",
			Table:   "context_attachments",
			Columns: []string{"account_id", "stack_id"},
		},
	}
}

// value is every config element's value, sealed to the element's row.
//
// ref: decision 0013
var value = crypt.Column{
	Table: "config_elements",
	Name:  "value_sealed",
	Key:   []string{"account_id", "id"},
}

// SealedColumns are the configuration stream's sealed columns.
func SealedColumns() []crypt.Column { return []crypt.Column{value} }

// Sealer seals and opens a column's values; crypt.Keyring is one.
type Sealer interface {
	SealColumn(ctx context.Context, c crypt.Column, pk []string, plaintext []byte) ([]byte, error)
	OpenColumn(ctx context.Context, c crypt.Column, pk []string, sealed []byte) (crypt.Value, error)
}

// DB is the pool the store queries, and begins a transaction on for a
// write of several statements that its caller has not.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Store keeps contexts, config elements and attachments in PostgreSQL.
// Every method joins the transaction ctx carries.
type Store struct {
	db   DB
	seal Sealer
}

// New returns the store on db, sealing values under seal.
func New(db DB, seal Sealer) *Store { return &Store{db: db, seal: seal} }

var _ core.Store = (*Store)(nil)

// columns are a context's columns as scan reads them, over contexts c.
const columns = `c.account_id, c.id, c.slug, c.space_id, c.name, c.description, c.labels, c.hooks,
	c.created_at, c.updated_at, c.version`

// scan reads columns, then into extra.
func scan(row pgx.Row, extra ...any) (core.Context, error) {
	var c core.Context
	var hooks []byte
	err := row.Scan(append([]any{
		&c.Account, &c.ID, &c.Slug, &c.Space, &c.Name, &c.Description, &c.Labels, &hooks,
		&c.CreatedAt, &c.UpdatedAt, &c.Version,
	}, extra...)...)
	if err != nil {
		return core.Context{}, err //nolint:wrapcheck // callers name the read
	}
	if err := json.Unmarshal(hooks, &c.Hooks); err != nil {
		return core.Context{}, fmt.Errorf("configuration: hooks of %s: %w", c.Slug, err)
	}
	return c, nil
}

// Create writes a new context.
func (s *Store) Create(ctx context.Context, c core.Context) error {
	hooks, err := json.Marshal(c.Hooks)
	if err != nil {
		return fmt.Errorf("configuration: hooks of %s: %w", c.Slug, err)
	}
	_, err = tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO contexts (account_id, id, slug, space_id, name,
			description, labels, hooks, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		c.Account, c.ID, c.Slug, c.Space, c.Name, c.Description, c.Labels, hooks, c.CreatedAt,
		c.UpdatedAt, c.Version)
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" {
		return &core.SlugTaken{Slug: c.Slug}
	} else if err != nil {
		return fmt.Errorf("configuration: write context %s: %w", c.Slug, sql.Unavailable(err))
	}
	return nil
}

// Write replaces the context's fields and sets its version.
func (s *Store) Write(ctx context.Context, c core.Context) error {
	hooks, err := json.Marshal(c.Hooks)
	if err != nil {
		return fmt.Errorf("configuration: hooks of %s: %w", c.Slug, err)
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE contexts SET space_id = $3, name = $4,
			description = $5, labels = $6, hooks = $7, updated_at = $8, version = $9
		WHERE account_id = $1 AND id = $2`,
		c.Account, c.ID, c.Space, c.Name, c.Description, c.Labels, hooks, c.UpdatedAt, c.Version)
	switch {
	case err != nil:
		return fmt.Errorf("configuration: write context %s: %w", c.Slug, sql.Unavailable(err))
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: context %s", core.ErrNotFound, c.Slug)
	}
	return nil
}

// BySlug reads acct's context slug.
func (s *Store) BySlug(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	lock core.Lock,
) (core.Context, error) {
	q := `SELECT ` + columns + ` FROM contexts c WHERE c.account_id = $1 AND c.slug = $2`
	if lock == core.Update {
		q += ` FOR UPDATE`
	}
	c, err := scan(tx.Q(ctx, s.db).QueryRow(ctx, q, acct, slug))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.Context{}, fmt.Errorf("%w: context %s", core.ErrNotFound, slug)
	case err != nil:
		return core.Context{}, fmt.Errorf(
			"configuration: read context %s: %w",
			slug,
			sql.Unavailable(err),
		)
	}
	return c, nil
}

// ByIDs reads those of acct's contexts ids that it has, keyed by ID.
func (s *Store) ByIDs(
	ctx context.Context,
	acct kernel.AccountID,
	ids []kernel.ULID,
) (map[kernel.ULID]core.Context, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+columns+` FROM contexts c
		WHERE c.account_id = $1 AND c.id = ANY($2)`, acct, ids)
	if err != nil {
		return nil, fmt.Errorf("configuration: read contexts: %w", sql.Unavailable(err))
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Context, error) {
		return scan(row)
	})
	if err != nil {
		return nil, fmt.Errorf("configuration: read contexts: %w", sql.Unavailable(err))
	}
	out := make(map[kernel.ULID]core.Context, len(all))
	for _, c := range all {
		out[c.ID] = c
	}
	return out, nil
}

// Delete deletes acct's context id; its elements and attachments go with
// it by their foreign keys.
func (s *Store) Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM contexts WHERE account_id = $1 AND id = $2`,
		acct, id); err != nil {
		return fmt.Errorf("configuration: delete context %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// InSpace reports whether a context of acct lives in space.
func (s *Store) InSpace(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	var used bool
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM contexts
		WHERE account_id = $1 AND space_id = $2)`, acct, space).Scan(&used); err != nil {
		return false, fmt.Errorf(
			"configuration: read contexts in space %s: %w",
			space,
			sql.Unavailable(err),
		)
	}
	return used, nil
}

// elementColumns are an element's columns as scanElement reads them, over
// config_elements e. A write-only element's sealed value reads as NULL, so
// it is never opened.
const elementColumns = `e.account_id, e.id, e.context_id, e.stack_id, e.element, e.file_mount,
	CASE WHEN e.write_only THEN NULL ELSE e.value_sealed END, e.checksum, e.write_only,
	e.description, e.file_mode, e.created_at`

// scanElement reads elementColumns, opening a readable value.
func (s *Store) scanElement(ctx context.Context, row pgx.Row) (core.Element, error) {
	var e core.Element
	var ctxID, stack *kernel.ULID
	var sealed []byte
	err := row.Scan(&e.Account, &e.ID, &ctxID, &stack, &e.Name, &e.FileMount, &sealed, &e.Checksum,
		&e.WriteOnly, &e.Description, &e.FileMode, &e.CreatedAt)
	if err != nil {
		return core.Element{}, err //nolint:wrapcheck // callers name the read
	}
	if ctxID != nil {
		e.Owner.Context = *ctxID
	}
	if stack != nil {
		e.Owner.Stack = *stack
	}
	if sealed != nil {
		v, err := s.seal.OpenColumn(ctx, value, []string{e.Account.UUID(), e.ID.UUID()}, sealed)
		if err != nil {
			return core.Element{}, fmt.Errorf("configuration: open element %s: %w", e.Name, err)
		}
		plain := string(v.Reveal())
		e.Value = &plain
	}
	return e, nil
}

// owners are the context and stack IDs among owners.
func owners(all []core.Owner) (contexts, stacks []kernel.ULID) {
	for _, o := range all {
		if o.Stack.IsZero() {
			contexts = append(contexts, o.Context)
		} else {
			stacks = append(stacks, o.Stack)
		}
	}
	return contexts, stacks
}

// Elements reads the elements of each of owners, by name, keyed by the
// owner's ID.
func (s *Store) Elements(
	ctx context.Context,
	acct kernel.AccountID,
	all []core.Owner,
) (map[kernel.ULID][]core.Element, error) {
	contexts, stacks := owners(all)
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+elementColumns+` FROM config_elements e
		WHERE e.account_id = $1 AND (e.context_id = ANY($2) OR e.stack_id = ANY($3))
		ORDER BY e.element COLLATE "C"`, acct, contexts, stacks)
	if err != nil {
		return nil, fmt.Errorf("configuration: read config elements: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := map[kernel.ULID][]core.Element{}
	for rows.Next() {
		e, err := s.scanElement(ctx, rows)
		if err != nil {
			return nil, fmt.Errorf("configuration: read config elements: %w", sql.Unavailable(err))
		}
		key := e.Owner.Context
		if key.IsZero() {
			key = e.Owner.Stack
		}
		out[key] = append(out[key], e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("configuration: read config elements: %w", sql.Unavailable(err))
	}
	return out, nil
}

// ownerWhere is the condition on e's owner, with the owner's ID at $2.
func ownerWhere(o core.Owner) (string, kernel.ULID) {
	if o.Stack.IsZero() {
		return "e.context_id = $2", o.Context
	}
	return "e.stack_id = $2", o.Stack
}

// Element reads owner's element name.
func (s *Store) Element(
	ctx context.Context,
	acct kernel.AccountID,
	owner core.Owner,
	name string,
) (core.Element, error) {
	where, id := ownerWhere(owner)
	e, err := s.scanElement(ctx, tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+elementColumns+`
		FROM config_elements e WHERE e.account_id = $1 AND `+where+` AND e.element = $3`,
		acct, id, name))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.Element{}, fmt.Errorf("%w: config element %s", core.ErrNotFound, name)
	case err != nil:
		return core.Element{}, fmt.Errorf(
			"configuration: read config element %s: %w",
			name,
			sql.Unavailable(err),
		)
	}
	return e, nil
}

// PutElement writes e, sealing its value to e's row, or keeping the value
// of e's row when e.Value is nil.
func (s *Store) PutElement(ctx context.Context, e core.Element) error {
	var sealed []byte
	if e.Value != nil {
		var err error
		sealed, err = s.seal.SealColumn(ctx, value, []string{e.Account.UUID(), e.ID.UUID()},
			[]byte(*e.Value))
		if err != nil {
			return fmt.Errorf("configuration: seal config element %s: %w", e.Name, err)
		}
	}
	var ctxID, stack *kernel.ULID
	if !e.Owner.Context.IsZero() {
		ctxID = &e.Owner.Context
	}
	if !e.Owner.Stack.IsZero() {
		stack = &e.Owner.Stack
	}
	// An INSERT checks NOT NULL before it finds the conflict, so keeping a
	// value is an UPDATE of the row the service has already read.
	q := `INSERT INTO config_elements (account_id, id, context_id, stack_id, element, file_mount,
			value_sealed, checksum, write_only, description, file_mode, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (account_id, id) DO UPDATE SET file_mount = EXCLUDED.file_mount,
			value_sealed = EXCLUDED.value_sealed, checksum = EXCLUDED.checksum,
			write_only = EXCLUDED.write_only, description = EXCLUDED.description,
			file_mode = EXCLUDED.file_mode, created_at = EXCLUDED.created_at`
	if sealed == nil {
		q = `UPDATE config_elements SET file_mount = $6, checksum = $8, write_only = $9,
			description = $10, file_mode = $11, created_at = $12
		WHERE account_id = $1 AND id = $2
			AND context_id IS NOT DISTINCT FROM $3 AND stack_id IS NOT DISTINCT FROM $4
			AND element = $5 AND $7::bytea IS NULL`
	}
	_, err := tx.Q(ctx, s.db).Exec(ctx, q, e.Account, e.ID, ctxID, stack, e.Name, e.FileMount,
		sealed, e.Checksum, e.WriteOnly, e.Description, e.FileMode, e.CreatedAt)
	if err != nil {
		return fmt.Errorf(
			"configuration: write config element %s: %w",
			e.Name,
			sql.Unavailable(err),
		)
	}
	return nil
}

// LockElements takes the transaction's advisory lock on owner's
// elements. The two-key form keeps it apart from the single-key locks
// the migrator takes, and a hash collision only makes two owners' writers
// take turns.
func (s *Store) LockElements(ctx context.Context, acct kernel.AccountID, owner core.Owner) error {
	_, id := ownerWhere(owner)
	_, err := tx.Q(ctx, s.db).Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('config_elements'), hashtext($1::text || '/' || $2::text))`,
		acct, id)
	if err != nil {
		return fmt.Errorf("configuration: lock config elements: %w", sql.Unavailable(err))
	}
	return nil
}

// DeleteElements deletes owner's elements names, every one when names is
// nil.
func (s *Store) DeleteElements(
	ctx context.Context,
	acct kernel.AccountID,
	owner core.Owner,
	names []string,
) error {
	where, id := ownerWhere(owner)
	q := `DELETE FROM config_elements e WHERE e.account_id = $1 AND ` + where +
		` AND ($3::text[] IS NULL OR e.element = ANY($3))`
	if _, err := tx.Q(ctx, s.db).Exec(ctx, q, acct, id, names); err != nil {
		return fmt.Errorf("configuration: delete config elements: %w", sql.Unavailable(err))
	}
	return nil
}

// attachmentColumns are an attachment's columns as scanAttachment reads
// them.
const attachmentColumns = `account_id, id, context_id, stack_id, priority, is_autoattached,
	created_at`

func scanAttachment(row pgx.Row) (core.Attachment, error) {
	var a core.Attachment
	err := row.Scan(&a.Account, &a.ID, &a.Context, &a.Stack, &a.Priority, &a.Autoattached,
		&a.CreatedAt)
	return a, err //nolint:wrapcheck // callers name the read
}

// Attach writes a, or the priority of the attachment its context already
// has to its stack, which keeps its ID.
func (s *Store) Attach(ctx context.Context, a core.Attachment) (core.Attachment, error) {
	out, err := scanAttachment(tx.Q(ctx, s.db).QueryRow(ctx, `INSERT INTO context_attachments
			(account_id, id, context_id, stack_id, priority, is_autoattached, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (account_id, context_id, stack_id) DO UPDATE SET priority = EXCLUDED.priority
		RETURNING `+attachmentColumns,
		a.Account, a.ID, a.Context, a.Stack, a.Priority, a.Autoattached, a.CreatedAt))
	if err != nil {
		return core.Attachment{}, fmt.Errorf("configuration: attach context %s: %w", a.Context,
			sql.Unavailable(err))
	}
	return out, nil
}

// Attachment reads acct's attachment id.
func (s *Store) Attachment(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (core.Attachment, error) {
	a, err := scanAttachment(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+attachmentColumns+`
		FROM context_attachments WHERE account_id = $1 AND id = $2`, acct, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.Attachment{}, fmt.Errorf("%w: attachment %s", core.ErrNotFound, id)
	case err != nil:
		return core.Attachment{}, fmt.Errorf("configuration: read attachment %s: %w", id,
			sql.Unavailable(err))
	}
	return a, nil
}

// Detach deletes acct's attachment id.
func (s *Store) Detach(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM context_attachments
		WHERE account_id = $1 AND id = $2`, acct, id); err != nil {
		return fmt.Errorf("configuration: detach %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// Attachments reads the attachments of contexts ids, or of stacks ids
// when byStack is set, keyed by that ID.
func (s *Store) Attachments(
	ctx context.Context,
	acct kernel.AccountID,
	ids []kernel.ULID,
	byStack bool,
) (map[kernel.ULID][]core.Attachment, error) {
	by := "context_id"
	if byStack {
		by = "stack_id"
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+attachmentColumns+` FROM context_attachments
		WHERE account_id = $1 AND `+by+` = ANY($2)
		ORDER BY priority, context_id, stack_id`, acct, ids)
	if err != nil {
		return nil, fmt.Errorf("configuration: read attachments: %w", sql.Unavailable(err))
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Attachment, error) {
		return scanAttachment(row)
	})
	if err != nil {
		return nil, fmt.Errorf("configuration: read attachments: %w", sql.Unavailable(err))
	}
	out := map[kernel.ULID][]core.Attachment{}
	for _, a := range all {
		key := a.Context
		if byStack {
			key = a.Stack
		}
		out[key] = append(out[key], a)
	}
	return out, nil
}

// AttachedStacks lists the stacks any of acct's contexts is attached to.
func (s *Store) AttachedStacks(ctx context.Context, acct kernel.AccountID) ([]kernel.ULID, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT DISTINCT stack_id FROM context_attachments
		WHERE account_id = $1`, acct)
	if err != nil {
		return nil, fmt.Errorf("configuration: read attached stacks: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
	if err != nil {
		return nil, fmt.Errorf("configuration: read attached stacks: %w", sql.Unavailable(err))
	}
	return out, nil
}

// DeleteStack deletes stack's attachments and elements.
func (s *Store) DeleteStack(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error {
	q := tx.Q(ctx, s.db)
	for _, stmt := range []string{
		`DELETE FROM context_attachments WHERE account_id = $1 AND stack_id = $2`,
		`DELETE FROM config_elements WHERE account_id = $1 AND stack_id = $2`,
	} {
		if _, err := q.Exec(ctx, stmt, acct, stack); err != nil {
			return fmt.Errorf(
				"configuration: delete what stack %s holds: %w",
				stack,
				sql.Unavailable(err),
			)
		}
	}
	return nil
}

// contextColumn is the SQL of each of core.Fields. The account's space
// tree belongs to the access domain, so space reads it from the scope.
var contextColumn = map[string]sqlsearch.Column{
	"id":          {Expr: "c.slug", Cast: "text", Text: true},
	"name":        {Expr: "c.name", Cast: "text", Text: true},
	"description": {Expr: "c.description", Text: true},
	"label":       {Expr: "c.labels", Many: true, Text: true},
	"space":       {Expr: "c.space_id", SpaceTree: true},
	"createdAt":   {Expr: "c.created_at", Cast: "timestamptz"},
	"updatedAt":   {Expr: "c.updated_at", Cast: "timestamptz"},
}

// contextSearch is the contexts table as core.Fields search it.
var contextSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "contexts", From: "contexts c", Select: columns, Space: "c.space_id", ID: "c.slug",
		Order: []search.Order{{Field: "id"}},
	}
	for _, f := range core.Fields {
		c := contextColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// searchScope is scope as the search of acct's contexts reads it.
func searchScope(acct kernel.AccountID, scope core.Scope) sqlsearch.Scope {
	spaces := make([]sqlsearch.SpaceNode, len(scope.Spaces))
	for i, n := range scope.Spaces {
		spaces[i] = sqlsearch.SpaceNode{Space: n.ID, Parent: n.Parent, ID: n.Slug, Display: n.Name}
	}
	return sqlsearch.Scope{
		Where: "c.account_id = $1", Args: []any{acct}, Reachable: scope.Reachable, Spaces: spaces,
	}
}

// Search reads a page of acct's contexts in the scope that in keeps.
func (s *Store) Search(
	ctx context.Context,
	acct kernel.AccountID,
	in search.Input,
	scope core.Scope,
) (search.Result[core.Context], error) {
	out, err := sqlsearch.Find(
		ctx,
		tx.Q(ctx, s.db),
		contextSearch,
		searchScope(acct, scope),
		in,
		scan,
	)
	if err != nil {
		return out, fmt.Errorf("configuration: %w", err)
	}
	return out, nil
}

// Suggest lists the values acct's contexts in the scope that in keeps
// hold.
func (s *Store) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	in search.SuggestionsInput,
	scope core.Scope,
) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), contextSearch, searchScope(acct, scope), in)
	if err != nil {
		return out, fmt.Errorf("configuration: %w", err)
	}
	return out, nil
}
