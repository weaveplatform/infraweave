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
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/logs"
)

// PlanningState reads only an exact planning transition's chunk extent and the
// run's explicit log deletion flag. No execution configuration is decoded.
func (s *Store) PlanningState(ctx context.Context, account kernel.AccountID, run kernel.ULID, version int64) (logs.PlanningState, error) {
	var out logs.PlanningState
	err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT r.logs_deleted_at IS NOT NULL,
  COALESCE(GREATEST(t.log_chunks,(SELECT max(c.seq)+1 FROM run_log_chunks c
   WHERE c.account_id=t.account_id AND c.run_id=t.run_id AND c.state_version=t.sequence)),0)
  FROM runs r JOIN run_state_transitions t ON t.account_id=r.account_id AND t.run_id=r.id
  WHERE r.account_id=$1 AND r.id=$2 AND t.sequence=$3 AND t.state='PLANNING'`, account, run, version).Scan(&out.Deleted, &out.Chunks)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return out, runsapi.ErrPlanningLogNotFound
	case err != nil:
		return out, fmt.Errorf("runs: read exact planning log state: %w", sql.Unavailable(err))
	}
	return out, nil
}

var _ logs.PlanningStore = (*Store)(nil)
