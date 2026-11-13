package pg

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/handover"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

var _ handover.Store = (*Store)(nil)

// artifactKey is a run's artifact key, sealed to its row.
//
// ref: decision 0013 § "Sealed columns"
var artifactKey = crypt.Column{
	Table: "runs", Name: "artifact_key_sealed", Key: []string{"account_id", "id"},
}

// overrides are a proposed run's environment overrides, sealed to its
// row, since they may hold secrets.
//
// ref: decision 0013 § "Sealed columns"
var overrides = crypt.Column{
	Table: "runs", Name: "env_overrides_sealed", Key: []string{"account_id", "id"},
}

// SealedColumns are the runs stream's sealed columns, which internal/app
// joins with the other streams' for Reseal.
func SealedColumns() []crypt.Column { return []crypt.Column{artifactKey, overrides} }

// artifactKeySize is the artifact key's length: an AES-256 key.
const artifactKeySize = 32

// Handed reads the run id of whichever account has it and locks its row
// FOR NO KEY UPDATE, so two handovers of the run take turns and make one
// artifact key, while the run's transitions still insert rows that
// reference it; lifecycle.ErrNotFound when no account has it.
func (s *Store) Handed(ctx context.Context, id kernel.ULID) (lifecycle.Run, error) {
	r, err := scanRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE id = $1 FOR NO KEY UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return lifecycle.Run{}, fmt.Errorf("%w: %s", lifecycle.ErrNotFound, id)
	case err != nil:
		return lifecycle.Run{}, fmt.Errorf("runs: hand over run %s: %w", id, sql.Unavailable(err))
	}
	return r, nil
}

// ArtifactKey is run's artifact key, opened, or, when the run has none
// yet, a fresh 256-bit key it seals to the run's row. The caller holds
// the row, as Handed locks it.
func (s *Store) ArtifactKey(ctx context.Context, run lifecycle.Run) (crypt.Value, error) {
	pk := []string{run.Account.UUID(), run.ID.UUID()}
	var sealed []byte
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT artifact_key_sealed FROM runs
		WHERE account_id = $1 AND id = $2`, run.Account, run.ID).Scan(&sealed)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return crypt.Value{}, fmt.Errorf("%w: %s", lifecycle.ErrNotFound, run.ID)
	case err != nil:
		return crypt.Value{}, fmt.Errorf("runs: read run %s's artifact key: %w", run.ID,
			sql.Unavailable(err))
	case sealed != nil:
		v, err := s.seal.OpenColumn(ctx, artifactKey, pk, sealed)
		if err != nil {
			return crypt.Value{}, fmt.Errorf("runs: open run %s's artifact key: %w", run.ID, err)
		}
		return v, nil
	}
	key := make([]byte, artifactKeySize)
	_, _ = rand.Read(key) // crypto/rand never fails
	if sealed, err = s.seal.SealColumn(ctx, artifactKey, pk, key); err != nil {
		return crypt.Value{}, fmt.Errorf("runs: seal run %s's artifact key: %w", run.ID, err)
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE runs SET artifact_key_sealed = $3
		WHERE account_id = $1 AND id = $2`, run.Account, run.ID, sealed); err != nil {
		return crypt.Value{}, fmt.Errorf("runs: write run %s's artifact key: %w", run.ID,
			sql.Unavailable(err))
	}
	return crypt.NewValue(key), nil
}

// SetOverrides seals ovs, run's environment overrides, to its row.
func (s *Store) SetOverrides(
	ctx context.Context,
	run lifecycle.Run,
	ovs []lifecycle.Override,
) error {
	// Strings alone, so never an error.
	doc, _ := json.Marshal(ovs)
	pk := []string{run.Account.UUID(), run.ID.UUID()}
	sealed, err := s.seal.SealColumn(ctx, overrides, pk, doc)
	if err != nil {
		return fmt.Errorf("runs: seal run %s's overrides: %w", run.ID, err)
	}
	if _, err := tx.Q(ctx, s.db).Exec(ctx, `UPDATE runs SET env_overrides_sealed = $3
		WHERE account_id = $1 AND id = $2`, run.Account, run.ID, sealed); err != nil {
		return fmt.Errorf("runs: write run %s's overrides: %w", run.ID, sql.Unavailable(err))
	}
	return nil
}

// Overrides opens run's environment overrides, none for a run without.
func (s *Store) Overrides(ctx context.Context, run lifecycle.Run) ([]lifecycle.Override, error) {
	var sealed []byte
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT env_overrides_sealed FROM runs
		WHERE account_id = $1 AND id = $2`, run.Account, run.ID).Scan(&sealed)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("%w: %s", lifecycle.ErrNotFound, run.ID)
	case err != nil:
		return nil, fmt.Errorf("runs: read run %s's overrides: %w", run.ID, sql.Unavailable(err))
	case sealed == nil:
		return nil, nil
	}
	v, err := s.seal.OpenColumn(ctx, overrides, []string{run.Account.UUID(), run.ID.UUID()},
		sealed)
	if err != nil {
		return nil, fmt.Errorf("runs: open run %s's overrides: %w", run.ID, err)
	}
	var ovs []lifecycle.Override
	if err := json.Unmarshal(v.Reveal(), &ovs); err != nil {
		return nil, fmt.Errorf("runs: decode run %s's overrides: %w", run.ID, err)
	}
	return ovs, nil
}
