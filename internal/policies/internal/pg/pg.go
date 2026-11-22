package pg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

//go:embed migrations/*.sql
var files embed.FS

// Stream is the policy domain's independently applicable migration stream.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations")
	return migrate.Stream{Name: "policies", Files: sub}
}

// Held declares the policy columns that retain access-domain entities.
func Held() []integrity.Held {
	return []integrity.Held{
		{Entity: "account", Table: "policies", Columns: []string{"account_id"}},
		{Entity: "account", Table: "policy_input_objects", Columns: []string{"account_id"}},
		{Entity: "account", Table: "policy_evaluations", Columns: []string{"account_id"}},
		{Entity: "account", Table: "policy_sample_budgets", Columns: []string{"account_id"}},
		{Entity: "account", Table: "policy_input_capture_heads", Columns: []string{"account_id"}},
		{Entity: "space", Table: "policies", Columns: []string{"account_id", "space_id"}},
		{Entity: "stack", Table: "policy_stack_attachments", Columns: []string{"account_id", "stack_id"}},
	}
}

// DB is the account-qualified statement and transaction executor.
type DB interface {
	tx.Querier
	tx.Beginner
}

// Store joins the transaction carried by its context.
type Store struct {
	db   DB
	seal SampleSealer
}

// New constructs the policy store over db.
func New(db DB) *Store { return &Store{db: db} }

const columns = `account_id,id,space_id,name,body,description,labels,type,engine_type,template_origin,created_at,updated_at,version`

// Create persists a compiled policy under its account.
func (s *Store) Create(ctx context.Context, p core.Policy) error {
	_, err := tx.Q(ctx, s.db).
		Exec(ctx, `INSERT INTO policies (`+columns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, p.Account, p.ID, p.Space, p.Name, p.Body, p.Description, p.Labels, p.Type, p.Engine, p.Template, p.CreatedAt, p.UpdatedAt, p.Version)
	if err != nil {
		return fmt.Errorf("policies: insert: %w", sql.Unavailable(err))
	}
	return nil
}

// Get reads only the requested account's row, optionally holding it for a write.
func (s *Store) Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) (core.Policy, error) {
	query := `SELECT ` + columns + ` FROM policies WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var p core.Policy
	err := tx.Q(ctx, s.db).
		QueryRow(ctx, query, acct, id).
		Scan(&p.Account, &p.ID, &p.Space, &p.Name, &p.Body, &p.Description, &p.Labels, &p.Type, &p.Engine, &p.Template, &p.CreatedAt, &p.UpdatedAt, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Policy{}, core.ErrNotFound
	}
	if err != nil {
		return core.Policy{}, fmt.Errorf("policies: read: %w", sql.Unavailable(err))
	}
	return p, nil
}

// Update replaces mutable settings while its caller holds the policy row lock.
func (s *Store) Update(ctx context.Context, p core.Policy) error {
	tag, err := tx.Q(ctx, s.db).
		Exec(ctx, "UPDATE policies SET space_id=$3,name=$4,body=$5,description=$6,labels=$7,engine_type=$8,updated_at=$9,version=$10 WHERE account_id=$1 AND id=$2", p.Account, p.ID, p.Space, p.Name, p.Body, p.Description, p.Labels, p.Engine, p.UpdatedAt, p.Version)
	if err != nil {
		return fmt.Errorf("policies: update row: %w", sql.Unavailable(err))
	}
	if tag.RowsAffected() != 1 {
		return core.ErrNotFound
	}
	return nil
}

// InSpace checks one account's policies inside the space deletion transaction.
func (s *Store) InSpace(ctx context.Context, acct kernel.AccountID, space kernel.ULID) (bool, error) {
	var used bool
	err := tx.Q(ctx, s.db).QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM policies WHERE account_id=$1 AND space_id=$2)", acct, space).Scan(&used)
	if err != nil {
		return false, fmt.Errorf("policies: space use: %w", sql.Unavailable(err))
	}
	return used, nil
}

// AttachmentCount counts only attachments owned by the account and policy.
func (s *Store) AttachmentCount(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (int, error) {
	var n int
	if err := tx.Q(ctx, s.db).QueryRow(ctx, "SELECT count(*) FROM policy_stack_attachments WHERE account_id=$1 AND policy_id=$2", acct, id).Scan(&n); err != nil {
		return 0, fmt.Errorf("policies: count stack attachments: %w", sql.Unavailable(err))
	}
	return n, nil
}

// Delete removes a policy and its attachments through the owned foreign key.
func (s *Store) Delete(ctx context.Context, acct kernel.AccountID, id kernel.ULID) error {
	if _, err := tx.Q(ctx, s.db).Exec(ctx, "DELETE FROM policies WHERE account_id=$1 AND id=$2", acct, id); err != nil {
		return fmt.Errorf("policies: delete row: %w", sql.Unavailable(err))
	}
	return nil
}

// Purge removes one bounded batch of a deleted account's policies. Their owned
// attachments disappear in the same statement; another account's rows stay.
func (s *Store) Purge(ctx context.Context, acct kernel.AccountID, batch int) (bool, error) {
	tag, err := tx.Q(ctx, s.db).Exec(ctx, `DELETE FROM policies WHERE account_id=$1 AND id IN (SELECT id FROM policies WHERE account_id=$1 LIMIT $2)`, acct, batch)
	if err != nil {
		return false, fmt.Errorf("policies: purge account: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected() == 0, nil
}
