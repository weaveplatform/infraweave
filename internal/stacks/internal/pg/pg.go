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
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
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

// Held are the stacks columns that hold the access domain's rows: a
// stack's account, and its space within that account.
func Held() []integrity.Held {
	return []integrity.Held{
		{Entity: "account", Table: "stacks", Columns: []string{"account_id"}},
		{Entity: "space", Table: "stacks", Columns: []string{"account_id", "space_id"}},
	}
}

// Keys are the stacks domain's rows other domains hold: a stack, by its
// account and ULID.
func Keys() []integrity.Key {
	return []integrity.Key{
		{Entity: "stack", Table: "stacks", Columns: []string{"account_id", "id"}},
	}
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
	s.lock_holder, s.locked_by, s.lock_note, s.created_at, s.version, s.tracked_branch_head,
	s.tracked_commit, s.tracked_commit_set_by, s.state, s.blocked,
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
		head, commit           []byte
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
		&holder, &lockedBy, &note, &st.CreatedAt, &st.Version, &head, &commit,
		&st.TrackedCommitSetBy, &st.State, &st.Blocked, &hooks, &st.Labels,
	}, extra...)...)
	if err != nil {
		return core.Stack{}, err //nolint:wrapcheck // callers name the read
	}
	for _, c := range []struct {
		doc []byte
		to  **stacksapi.Commit
	}{{head, &st.TrackedBranchHead}, {commit, &st.TrackedCommit}} {
		if c.doc != nil {
			if err := json.Unmarshal(c.doc, c.to); err != nil {
				return core.Stack{}, fmt.Errorf("stacks: tracked commit of %s: %w", st.Slug, err)
			}
		}
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
	st, err := scanRef(acct, tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+refColumns+` FROM stacks
		WHERE account_id = $1 AND id = $2`, acct, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return stacksapi.Stack{}, fmt.Errorf("%w: stack %s", stacksapi.ErrNotFound, id)
	case err != nil:
		return stacksapi.Stack{}, fmt.Errorf("stacks: read stack %s: %w", id, sql.Unavailable(err))
	}
	return st, nil
}

// refColumns are a stacksapi.Stack's columns as scanRef reads them.
const refColumns = `id, slug, name, space_id`

// scanRef reads refColumns of acct's stack.
func scanRef(acct kernel.AccountID, row pgx.Row) (stacksapi.Stack, error) {
	st := stacksapi.Stack{Account: acct}
	err := row.Scan(&st.ID, &st.Slug, &st.Name, &st.Space)
	return st, err //nolint:wrapcheck // callers name the read
}

// refs lists the stacks rows holds, closing rows.
func refs(acct kernel.AccountID, rows pgx.Rows) ([]stacksapi.Stack, error) {
	//nolint:wrapcheck // callers name the read
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (stacksapi.Stack, error) {
		return scanRef(acct, row)
	})
}

// ByIDs reads those of acct's stacks ids that it has, keyed by ID.
func (s *Store) ByIDs(
	ctx context.Context,
	acct kernel.AccountID,
	ids []kernel.ULID,
) (map[kernel.ULID]stacksapi.Stack, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+refColumns+` FROM stacks
		WHERE account_id = $1 AND id = ANY($2)`, acct, ids)
	if err != nil {
		return nil, fmt.Errorf("stacks: read stacks: %w", sql.Unavailable(err))
	}
	all, err := refs(acct, rows)
	if err != nil {
		return nil, fmt.Errorf("stacks: read stacks: %w", sql.Unavailable(err))
	}
	out := make(map[kernel.ULID]stacksapi.Stack, len(all))
	for _, st := range all {
		out[st.ID] = st
	}
	return out, nil
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
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+refColumns+` FROM stacks
		WHERE account_id = $1 AND repository = $2 AND branch = $3
			AND vcs_integration_id IS NOT DISTINCT FROM $4
		ORDER BY slug`, acct, repository, branch, integration)
	if err != nil {
		return nil, fmt.Errorf("stacks: list stacks of %s: %w", repository, sql.Unavailable(err))
	}
	out, err := refs(acct, rows)
	if err != nil {
		return nil, fmt.Errorf("stacks: list stacks of %s: %w", repository, sql.Unavailable(err))
	}
	return out, nil
}

// Hold takes FOR KEY SHARE on acct's stack id in the context's
// transaction, the lock PostgreSQL's own key check takes: a move or
// delete, which locks the row FOR UPDATE before asking its guards, waits
// for the holder to commit and then sees what it wrote.
//
// ref: decision 0004
func (s *Store) Hold(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	var got kernel.ULID
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT id FROM stacks
		WHERE account_id = $1 AND id = $2 FOR KEY SHARE`, acct, id).Scan(&got)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: stack %s", stacksapi.ErrNotFound, id)
	case err != nil:
		return fmt.Errorf("stacks: hold stack %s: %w", id, sql.Unavailable(err))
	}
	return nil
}

// SetRunState writes acct's stack id's state and blocked. The update
// changes no key, so its FOR NO KEY UPDATE lock never waits on a Hold's
// FOR KEY SHARE.
func (s *Store) SetRunState(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	state kernel.StackState,
	blocked bool,
) error {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE stacks SET state = $3, blocked = $4
		WHERE account_id = $1 AND id = $2`, acct, id, state, blocked)
	switch {
	case err != nil:
		return fmt.Errorf("stacks: set run state of %s: %w", id, sql.Unavailable(err))
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: stack %s", stacksapi.ErrNotFound, id)
	}
	return nil
}

// Find lists acct's stacks in spaces that carry any of labels, by slug.
func (s *Store) Find(
	ctx context.Context,
	acct kernel.AccountID,
	spaces []kernel.ULID,
	labels []string,
) ([]stacksapi.Stack, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT s.id, s.slug, s.name, s.space_id FROM stacks s
		WHERE s.account_id = $1 AND s.space_id = ANY($2) AND EXISTS (
			SELECT 1 FROM stack_labels l
			WHERE l.account_id = s.account_id AND l.stack_id = s.id AND l.label = ANY($3))
		ORDER BY s.slug`, acct, spaces, labels)
	if err != nil {
		return nil, fmt.Errorf("stacks: find stacks by label: %w", sql.Unavailable(err))
	}
	out, err := refs(acct, rows)
	if err != nil {
		return nil, fmt.Errorf("stacks: find stacks by label: %w", sql.Unavailable(err))
	}
	return out, nil
}

// InSpaces lists acct's stacks in spaces, by slug.
func (s *Store) InSpaces(
	ctx context.Context,
	acct kernel.AccountID,
	spaces []kernel.ULID,
) ([]stacksapi.Stack, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+refColumns+` FROM stacks
		WHERE account_id = $1 AND space_id = ANY($2)
		ORDER BY slug`, acct, spaces)
	if err != nil {
		return nil, fmt.Errorf("stacks: read stacks in spaces: %w", sql.Unavailable(err))
	}
	out, err := refs(acct, rows)
	if err != nil {
		return nil, fmt.Errorf("stacks: read stacks in spaces: %w", sql.Unavailable(err))
	}
	return out, nil
}

// Labels reads the labels of acct's stacks ids, sorted, keyed by ID; a
// stack with none is left out.
func (s *Store) Labels(
	ctx context.Context,
	acct kernel.AccountID,
	ids []kernel.ULID,
) (map[kernel.ULID][]string, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT stack_id, label FROM stack_labels
		WHERE account_id = $1 AND stack_id = ANY($2)
		ORDER BY stack_id, label COLLATE "C"`, acct, ids)
	if err != nil {
		return nil, fmt.Errorf("stacks: read stack labels: %w", sql.Unavailable(err))
	}
	defer rows.Close()
	out := map[kernel.ULID][]string{}
	for rows.Next() {
		var id kernel.ULID
		var label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, fmt.Errorf("stacks: read stack labels: %w", sql.Unavailable(err))
		}
		out[id] = append(out[id], label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stacks: read stack labels: %w", sql.Unavailable(err))
	}
	return out, nil
}

// SetTracked sets acct's stack id's branch head and tracked commit,
// leaving either unchanged when nil, and its version as it is.
func (s *Store) SetTracked(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	head, commit *stacksapi.Commit,
	setBy string,
) error {
	docs := make([][]byte, 2)
	for i, c := range []*stacksapi.Commit{head, commit} {
		if c != nil {
			var err error
			if docs[i], err = json.Marshal(c); err != nil {
				return fmt.Errorf("stacks: encode tracked commit of %s: %w", id, err)
			}
		}
	}
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE stacks SET
			tracked_branch_head = coalesce($3, tracked_branch_head),
			tracked_commit = coalesce($4, tracked_commit),
			tracked_commit_set_by = CASE WHEN $4::jsonb IS NULL THEN tracked_commit_set_by ELSE $5 END
		WHERE account_id = $1 AND id = $2`, acct, id, docs[0], docs[1], setBy)
	switch {
	case err != nil:
		return fmt.Errorf("stacks: set tracked commit of %s: %w", id, sql.Unavailable(err))
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: stack %s", stacksapi.ErrNotFound, id)
	}
	return nil
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

// Purge deletes up to batch of the deleted account acct's stacks, with
// their hooks, labels and stars, in one statement, and reports whether
// none is left. Each call commits on its own unless ctx carries a
// transaction, so a purge stopped between calls keeps what it deleted and
// resumes from there.
func (s *Store) Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM stacks WHERE account_id = $1 AND id IN (
		SELECT id FROM stacks WHERE account_id = $1 LIMIT $2)`, acct, batch)
	if err != nil {
		return false, fmt.Errorf("stacks: purge account %s: %w", acct, sql.Unavailable(err))
	}
	return tag.RowsAffected() < int64(batch), nil
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
	"state":       {Expr: "s.state", Cast: "text"},
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
	out := sqlsearch.Scope{
		Where:     "s.account_id = $1",
		Args:      []any{acct, string(scope.Actor.Kind), scope.Actor.ID},
		Reachable: scope.Reachable,
		Spaces:    spaces,
	}
	if scope.Stacks != nil {
		out.Where += " AND s.id = ANY($4::uuid[])"
		out.Args = append(out.Args, scope.Stacks)
	}
	return out
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
