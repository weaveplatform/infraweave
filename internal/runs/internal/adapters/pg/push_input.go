package pg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

var _ runsapi.PushRunReader = (*Store)(nil)

// PushInProgress selects only contracted run facts, without opening runtime or
// override secrets that the push input does not consume.
func (s *Store) PushInProgress(ctx context.Context, account kernel.AccountID, stack kernel.ULID) ([]runsapi.PushRunFacts, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT r.id,r.stack_id,r.type,r.state,r.state_version,r.commit,r.triggered_by,
		COALESCE(r.commit->>'branch',''),COALESCE(r.local_workspace,'')<>'',r.drift_detection,
		COALESCE(NULLIF(r.force_apply_mode,''),'DISABLED'),r.command,
		COALESCE((SELECT array_agg(DISTINCT f.flag ORDER BY f.flag) FROM run_flags f
			WHERE f.account_id=r.account_id AND f.run_id=r.id),'{}'::text[]),r.created_at,r.updated_at
		FROM runs r WHERE r.account_id=$1 AND r.stack_id=$2 AND r.state IN ('QUEUED','READY','UNCONFIRMED')
		ORDER BY r.created_at,r.id`, account, stack)
	if err != nil {
		return nil, fmt.Errorf("runs: read push in-progress facts: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (runsapi.PushRunFacts, error) {
		var out runsapi.PushRunFacts
		var commit []byte
		var typ, state string
		if err := row.Scan(&out.ID, &out.Stack, &typ, &state, &out.Version, &commit, &out.TriggeredBy,
			&out.Branch, &out.BasedOnLocalWorkspace, &out.DriftDetection, &out.ForceApplyMode, &out.Command,
			&out.Flags, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return out, err //nolint:wrapcheck // the collecting caller names the failed projection
		}
		if len(commit) != 0 {
			if err := json.Unmarshal(commit, &out.Commit); err != nil {
				return out, fmt.Errorf("runs: decode push commit facts: %w", err)
			}
		}
		out.Type, out.State = kernel.RunType(typ), kernel.RunState(state)
		out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
		return out, nil
	})
	if err != nil {
		return nil, fmt.Errorf("runs: scan push in-progress facts: %w", sql.Unavailable(err))
	}
	return out, nil
}
