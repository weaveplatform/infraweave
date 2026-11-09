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
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// StreamName is the stacks stream's name in schema_migrations.
const StreamName = "stacks"

// files are the stacks stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the stacks domain's migration stream, which internal/app
// registers after access'.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}

// DB is the pool the store queries, and begins a transaction on for a
// write of several statements that its caller has not.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Store keeps stacks in PostgreSQL. Every method joins the transaction ctx
// carries.
type Store struct{ db DB }

// New returns the store on db.
func New(db DB) *Store { return &Store{db: db} }

var _ core.Store = (*Store)(nil)

// columns are a stack's columns as scan reads them, over stacks s. Hooks
// read as one JSON object of each phase's commands in order, and labels
// in order, so one row holds the whole stack.
const columns = `s.account_id, s.id, s.slug, s.space_id, s.name, s.description, s.repository,
	s.repository_url, s.branch, s.provider, s.namespace, s.project_root,
	s.additional_project_globs, s.git_sparse_checkout_paths, s.runner_image, s.worker_pool_id,
	s.vcs_integration_id, s.vendor, s.vendor_config, s.terraform_version, s.autodeploy,
	s.autoretry, s.protect_from_deletion, s.disabled, s.local_preview_enabled,
	s.github_action_deploy, s.enable_sensitive_output_upload,
	s.enable_well_known_secret_masking, s.skip_vendor_binary_download, s.manages_state, s.locked_at,
	s.lock_holder, s.locked_by, s.lock_note, s.created_at, s.version,
	(SELECT coalesce(jsonb_object_agg(phase, commands), '{}') FROM (
		SELECT h.phase, jsonb_agg(h.command ORDER BY h.position) AS commands FROM stack_hooks h
		WHERE h.account_id = s.account_id AND h.stack_id = s.id GROUP BY h.phase) AS p),
	ARRAY(SELECT l.label FROM stack_labels l
		WHERE l.account_id = s.account_id AND l.stack_id = s.id ORDER BY l.label)`

// scan reads columns, then into extra.
func scan(row pgx.Row, extra ...any) (core.Stack, error) {
	var (
		st                     core.Stack
		config, hooks          []byte
		lockedAt               *time.Time
		holder, lockedBy, note *string
	)
	err := row.Scan(append([]any{
		&st.Account, &st.ID, &st.Slug, &st.Space, &st.Name, &st.Description, &st.Repository,
		&st.RepositoryURL, &st.Branch, &st.Provider, &st.Namespace, &st.ProjectRoot,
		&st.AdditionalProjectGlobs, &st.GitSparseCheckoutPaths, &st.RunnerImage, &st.WorkerPool,
		&st.VCSIntegration, &st.Vendor, &config, &st.TerraformVersion, &st.Autodeploy,
		&st.Autoretry, &st.ProtectFromDeletion, &st.Disabled, &st.LocalPreviewEnabled,
		&st.GithubActionDeploy, &st.EnableSensitiveOutputUpload,
		&st.EnableWellKnownSecretMasking, &st.SkipVendorBinaryDownload, &st.ManagesState, &lockedAt,
		&holder, &lockedBy, &note, &st.CreatedAt, &st.Version, &hooks, &st.Labels,
	}, extra...)...)
	if err != nil {
		return core.Stack{}, err //nolint:wrapcheck // callers name the read
	}
	st.VendorConfig = config
	if err := json.Unmarshal(hooks, &st.Hooks); err != nil {
		return core.Stack{}, fmt.Errorf("stacks: hooks of %s: %w", st.Slug, err)
	}
	if holder != nil {
		st.Lock = &stacksapi.Lock{Holder: *holder, At: *lockedAt}
		if lockedBy != nil {
			st.Lock.By = *lockedBy
		}
		if note != nil {
			st.Lock.Note = *note
		}
	}
	return st, nil
}

// Get reads acct's stack id.
func (s *Store) Get(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (stacksapi.Stack, error) {
	st := stacksapi.Stack{Account: acct}
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id, slug, space_id FROM stacks
		WHERE account_id = $1 AND id = $2`, acct, id).Scan(&st.ID, &st.Slug, &st.Space)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return stacksapi.Stack{}, fmt.Errorf("%w: stack %s", stacksapi.ErrNotFound, id)
	case err != nil:
		return stacksapi.Stack{}, fmt.Errorf("stacks: read stack %s: %w", id, sql.Unavailable(err))
	}
	return st, nil
}

// Read reads acct's stack id whole.
func (s *Store) Read(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	lock bool,
) (core.Stack, error) {
	return s.one(ctx, "s.id = $2", acct, id, lock)
}

// BySlug reads acct's stack slug whole.
func (s *Store) BySlug(
	ctx context.Context,
	acct kernel.AccountID,
	slug string,
	lock bool,
) (core.Stack, error) {
	return s.one(ctx, "s.slug = $2", acct, slug, lock)
}

func (s *Store) one(
	ctx context.Context,
	where string,
	acct kernel.AccountID,
	key any,
	lock bool,
) (core.Stack, error) {
	q := `SELECT ` + columns + ` FROM stacks s WHERE s.account_id = $1 AND ` + where
	if lock {
		q += ` FOR UPDATE OF s`
	}
	st, err := scan(tx.Q(ctx, s.db).QueryRow(ctx, q, acct, key))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.Stack{}, fmt.Errorf("%w: stack %v", stacksapi.ErrNotFound, key)
	case err != nil:
		return core.Stack{}, fmt.Errorf("stacks: read stack %v: %w", key, sql.Unavailable(err))
	}
	return st, nil
}

// ByRepository lists acct's stacks that track branch of repository
// through integration.
func (s *Store) ByRepository(
	ctx context.Context,
	acct kernel.AccountID,
	integration *kernel.ULID,
	repository, branch string,
) ([]stacksapi.Stack, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT id, slug, space_id FROM stacks
		WHERE account_id = $1 AND repository = $2 AND branch = $3
			AND vcs_integration_id IS NOT DISTINCT FROM $4
		ORDER BY slug`, acct, repository, branch, integration)
	if err != nil {
		return nil, fmt.Errorf("stacks: list stacks of %s: %w", repository, sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (stacksapi.Stack, error) {
		st := stacksapi.Stack{Account: acct}
		return st, row.Scan(&st.ID, &st.Slug, &st.Space)
	})
	if err != nil {
		return nil, fmt.Errorf("stacks: list stacks of %s: %w", repository, sql.Unavailable(err))
	}
	return out, nil
}

// Create writes a new stack with its hooks and labels.
func (s *Store) Create(ctx context.Context, st core.Stack) error {
	//nolint:wrapcheck // each statement in the transaction names itself
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		_, err := tx.Q(ctx, s.db).Exec(ctx, `INSERT INTO stacks (account_id, id, slug, space_id,
				name, created_at, manages_state, repository, branch, provider, vendor, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, '', '', '', '', $8)`,
			st.Account, st.ID, st.Slug, st.Space, st.Name, st.CreatedAt, st.ManagesState, st.Version)
		if err != nil {
			return writeError(st, err)
		}
		return s.write(ctx, st)
	})
}

// Write replaces the stack's settings, hooks, labels, slug and lock.
func (s *Store) Write(ctx context.Context, st core.Stack) error {
	//nolint:wrapcheck // each statement in the transaction names itself
	return tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		return s.write(ctx, st)
	})
}

func (s *Store) write(ctx context.Context, st core.Stack) error {
	q := tx.Q(ctx, s.db)
	var lockedAt *time.Time
	var holder, by, note *string
	if st.Lock != nil {
		lockedAt = &st.Lock.At
		holder, by, note = &st.Lock.Holder, &st.Lock.By, &st.Lock.Note
	}
	config := []byte(st.VendorConfig)
	if len(config) == 0 {
		config = []byte(`{}`)
	}
	tag, err := q.Exec(ctx, `UPDATE stacks SET slug = $3, space_id = $4, name = $5,
			description = $6, repository = $7, repository_url = $8, branch = $9, provider = $10,
			namespace = $11, project_root = $12, additional_project_globs = $13,
			git_sparse_checkout_paths = $14, runner_image = $15, worker_pool_id = $16,
			vcs_integration_id = $17, vendor = $18, vendor_config = $19, terraform_version = $20,
			autodeploy = $21, autoretry = $22, protect_from_deletion = $23, disabled = $24,
			local_preview_enabled = $25, github_action_deploy = $26,
			enable_sensitive_output_upload = $27, enable_well_known_secret_masking = $28,
			skip_vendor_binary_download = $29, locked_at = $30, lock_holder = $31, locked_by = $32,
			lock_note = $33, version = $34
		WHERE account_id = $1 AND id = $2`,
		st.Account, st.ID, st.Slug, st.Space, st.Name, st.Description, st.Repository,
		st.RepositoryURL, st.Branch, st.Provider, st.Namespace, st.ProjectRoot,
		st.AdditionalProjectGlobs, st.GitSparseCheckoutPaths, st.RunnerImage, st.WorkerPool,
		st.VCSIntegration, st.Vendor, config, st.TerraformVersion, st.Autodeploy, st.Autoretry,
		st.ProtectFromDeletion, st.Disabled, st.LocalPreviewEnabled, st.GithubActionDeploy,
		st.EnableSensitiveOutputUpload, st.EnableWellKnownSecretMasking,
		st.SkipVendorBinaryDownload, lockedAt, holder, by, note, st.Version)
	switch {
	case err != nil:
		return writeError(st, err)
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: stack %s", stacksapi.ErrNotFound, st.ID)
	}
	var phases, commands []string
	var positions []int32
	for phase, cmds := range st.Hooks {
		for i, c := range cmds {
			phases, positions, commands = append(
				phases,
				phase,
			), append(
				positions,
				int32(i),
			), //nolint:gosec // a request's hooks are far fewer
				append(
					commands,
					c,
				)
		}
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM stack_hooks WHERE account_id = $1 AND stack_id = $2`, nil},
		{
			`INSERT INTO stack_hooks (account_id, stack_id, phase, position, command)
			SELECT $1, $2, * FROM unnest($3::text[], $4::integer[], $5::text[])`,
			[]any{phases, positions, commands},
		},
		{`DELETE FROM stack_labels WHERE account_id = $1 AND stack_id = $2`, nil},
		{`INSERT INTO stack_labels (account_id, stack_id, label)
			SELECT $1, $2, unnest($3::text[])`, []any{st.Labels}},
	} {
		if _, err := q.Exec(
			ctx,
			stmt.sql,
			append([]any{st.Account, st.ID}, stmt.args...)...); err != nil {
			return fmt.Errorf("stacks: write stack %s: %w", st.Slug, sql.Unavailable(err))
		}
	}
	return nil
}

// writeError names a failed write of st, a *core.SlugTaken when another
// stack of its account holds its slug.
func writeError(st core.Stack, err error) error {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" {
		return &core.SlugTaken{Slug: st.Slug}
	}
	return fmt.Errorf("stacks: write stack %s: %w", st.Slug, sql.Unavailable(err))
}

// Delete deletes acct's stack id, its hooks, labels and stars with it.
func (s *Store) Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM stacks WHERE account_id = $1 AND id = $2`,
		acct, id); err != nil {
		return fmt.Errorf("stacks: delete stack %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// SetStar stars or unstars acct's stack id for actor.
func (s *Store) SetStar(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	actor core.Actor,
	on bool,
) error {
	q := `DELETE FROM stack_stars
		WHERE account_id = $1 AND stack_id = $2 AND actor_kind = $3 AND actor_id = $4`
	if on {
		q = `INSERT INTO stack_stars (account_id, stack_id, actor_kind, actor_id)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, q, acct, id, string(actor.Kind), actor.ID); err != nil {
		return fmt.Errorf("stacks: star stack %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// Starred is which of acct's stacks ids actor has starred.
func (s *Store) Starred(
	ctx context.Context,
	acct kernel.AccountID,
	ids []kernel.ULID,
	actor core.Actor,
) (map[kernel.ULID]bool, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT stack_id FROM stack_stars
		WHERE account_id = $1 AND stack_id = ANY($2) AND actor_kind = $3 AND actor_id = $4`,
		acct, ids, string(actor.Kind), actor.ID)
	if err != nil {
		return nil, fmt.Errorf("stacks: read stars: %w", sql.Unavailable(err))
	}
	starred, err := pgx.CollectRows(rows, pgx.RowTo[kernel.ULID])
	if err != nil {
		return nil, fmt.Errorf("stacks: read stars: %w", sql.Unavailable(err))
	}
	out := make(map[kernel.ULID]bool, len(starred))
	for _, id := range starred {
		out[id] = true
	}
	return out, nil
}

// SpaceInUse reports whether a stack of acct lives in space.
func (s *Store) SpaceInUse(
	ctx context.Context,
	acct kernel.AccountID,
	space kernel.ULID,
) (bool, error) {
	var used bool
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stacks
		WHERE account_id = $1 AND space_id = $2)`, acct, space).Scan(&used); err != nil {
		return false, fmt.Errorf("stacks: read stacks in space %s: %w", space, sql.Unavailable(err))
	}
	return used, nil
}

// stackColumn is the SQL of each of core.Fields. The account's space
// tree belongs to the access domain, so space reads it from the scope
// rather than from a table this domain does not own.
var stackColumn = map[string]sqlsearch.Column{
	"id":          {Expr: "s.slug", Cast: "text", Text: true},
	"name":        {Expr: "s.name", Cast: "text", Text: true},
	"description": {Expr: "s.description", Text: true},
	"label": {
		Expr: `ARRAY(SELECT l.label FROM stack_labels l
			WHERE l.account_id = s.account_id AND l.stack_id = s.id)`,
		Many: true, Text: true,
	},
	"space":       {Expr: "s.space_id", SpaceTree: true},
	"repository":  {Expr: "s.repository", Cast: "text", Text: true},
	"branch":      {Expr: "s.branch", Cast: "text"},
	"projectRoot": {Expr: "s.project_root"},
	"provider":    {Expr: "s.provider"},
	"vendor":      {Expr: "s.vendor"},
	"autodeploy":  {Expr: "s.autodeploy"},
	"disabled":    {Expr: "s.disabled"},
	"locked":      {Expr: "(s.locked_at IS NOT NULL)"},
	"starred":     {Expr: "(star.stack_id IS NOT NULL)"},
	"createdAt":   {Expr: "s.created_at", Cast: "timestamptz"},
}

// stackSearch is the stacks table as core.Fields search it. The caller's
// star joins every query, $2 and $3 naming the caller, and its key holds
// one star per caller and stack, so the join never repeats a stack.
var stackSearch = func() sqlsearch.Entity {
	e := sqlsearch.Entity{
		List: "stacks",
		From: `stacks s LEFT JOIN stack_stars star ON star.account_id = s.account_id
			AND star.stack_id = s.id AND star.actor_kind = $2 AND star.actor_id = $3`,
		Select: columns, Space: "s.space_id", ID: "s.slug",
		Order: []search.Order{{Field: "id"}},
	}
	for _, f := range core.Fields {
		c := stackColumn[f.Name]
		c.Field = f
		e.Columns = append(e.Columns, c)
	}
	return e
}()

// searchScope is scope as the search of acct's stacks reads it.
func searchScope(acct kernel.AccountID, scope core.Scope) sqlsearch.Scope {
	spaces := make([]sqlsearch.SpaceNode, len(scope.Spaces))
	for i, n := range scope.Spaces {
		spaces[i] = sqlsearch.SpaceNode{Space: n.ID, Parent: n.Parent, ID: n.Slug, Display: n.Name}
	}
	return sqlsearch.Scope{
		Where:     "s.account_id = $1",
		Args:      []any{acct, string(scope.Actor.Kind), scope.Actor.ID},
		Reachable: scope.Reachable,
		Spaces:    spaces,
	}
}

// Search reads a page of acct's stacks in the scope that in keeps.
func (s *Store) Search(
	ctx context.Context,
	acct kernel.AccountID,
	in search.Input,
	scope core.Scope,
) (search.Result[core.Stack], error) {
	out, err := sqlsearch.Find(
		ctx,
		tx.Q(ctx, s.db),
		stackSearch,
		searchScope(acct, scope),
		in,
		scan,
	)
	if err != nil {
		return out, fmt.Errorf("stacks: %w", err)
	}
	return out, nil
}

// Suggest lists the values acct's stacks in the scope that in keeps hold.
func (s *Store) Suggest(
	ctx context.Context,
	acct kernel.AccountID,
	in search.SuggestionsInput,
	scope core.Scope,
) (search.Suggestions, error) {
	out, err := sqlsearch.Suggest(ctx, tx.Q(ctx, s.db), stackSearch, searchScope(acct, scope), in)
	if err != nil {
		return out, fmt.Errorf("stacks: %w", err)
	}
	return out, nil
}
