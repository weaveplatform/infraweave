package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

// The workspace key and private commit/configuration fields never cross this
// projection. JSON null, like SQL NULL, represents a plan not yet recorded;
// an actual empty delta object remains a known plan with zero changes.
const checkFactColumns = `id,stack_id,type,state,state_version,
	COALESCE(commit->>'hash',''),COALESCE(commit->>'branch',''),COALESCE(commit->>'tag',''),
	COALESCE(local_workspace,'')<>'',drift_detection,NULLIF(delta,'null'::jsonb),created_at,updated_at`

// CheckRun reads current state and delta together in one account-bound statement.
func (s *Store) CheckRun(ctx context.Context, account kernel.AccountID, run kernel.ULID) (runsapi.CheckRun, error) {
	if account.IsZero() || run.IsZero() {
		return runsapi.CheckRun{}, runsapi.ErrCheckRunNotFound
	}
	out, err := scanCheckRun(tx.Q(ctx, s.db).QueryRow(ctx, `SELECT `+checkFactColumns+` FROM runs
		WHERE account_id=$1 AND id=$2`, account, run))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return runsapi.CheckRun{}, runsapi.ErrCheckRunNotFound
	case err != nil:
		return runsapi.CheckRun{}, fmt.Errorf("runs: read check facts of run %s: %w", run, sql.Unavailable(err))
	}
	return out, nil
}

// CheckRunsOnCommit deliberately applies no run-type, state, trigger or limit
// policy. UUID array membership avoids duplicating runs for repeated stack IDs.
func (s *Store) CheckRunsOnCommit(ctx context.Context, account kernel.AccountID, stacks []kernel.ULID, sha string) ([]runsapi.CheckRun, error) {
	if len(stacks) == 0 {
		return []runsapi.CheckRun{}, nil
	}
	ids := make([]string, len(stacks))
	for i, id := range stacks {
		ids[i] = id.UUID()
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+checkFactColumns+` FROM runs
		WHERE account_id=$1 AND stack_id=ANY($2::uuid[]) AND commit->>'hash'=$3
		ORDER BY created_at DESC,id DESC`, account, ids, sha)
	if err != nil {
		return nil, fmt.Errorf("runs: read check facts on commit: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (runsapi.CheckRun, error) { return scanCheckRun(row) })
	if err != nil {
		return nil, fmt.Errorf("runs: read check fact rows on commit: %w", sql.Unavailable(err))
	}
	return out, nil
}

func scanCheckRun(row pgx.Row) (runsapi.CheckRun, error) {
	var out runsapi.CheckRun
	var typ, state string
	var delta []byte
	if err := row.Scan(&out.ID, &out.Stack, &typ, &state, &out.Sequence,
		&out.CommitSHA, &out.Branch, &out.Tag, &out.Local, &out.DriftDetection, &delta, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return out, err //nolint:wrapcheck // the public read names the query
	}
	var err error
	if out.Delta, err = deltaOf(delta); err != nil {
		return out, fmt.Errorf("runs: decode current delta of run %s: %w", out.ID, err)
	}
	out.Type, out.State = kernel.RunType(typ), kernel.RunState(state)
	out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
	return out, nil
}
