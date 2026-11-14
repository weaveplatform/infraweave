package pg

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/timers"
)

var _ timers.Store = (*Store)(nil)

// Due reads up to batch runs that one of cutoffs times out, as
// timers.Store says. The index runs_waiting serves the states' scan.
func (s *Store) Due(
	ctx context.Context,
	cutoffs []timers.Cutoff,
	batch int,
) ([]lifecycle.Run, error) {
	n := len(cutoffs)
	var states []string
	cutStates, types := make([]string, 0, n), make([]string, 0, n)
	created, at := make([]bool, 0, n), make([]time.Time, 0, n)
	for _, c := range cutoffs {
		if !slices.Contains(states, string(c.State)) {
			states = append(states, string(c.State))
		}
		cutStates, types = append(cutStates, string(c.State)), append(types, string(c.Type))
		created, at = append(created, c.Created), append(at, c.At)
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT `+runColumns+` FROM runs
		WHERE state = ANY($1) AND EXISTS (
			SELECT FROM unnest($2::text[], $3::text[], $4::bool[], $5::timestamptz[])
				AS c(state, type, created, at)
			WHERE c.state = runs.state AND c.type = runs.type
				AND CASE WHEN c.created THEN runs.created_at ELSE runs.state_changed_at END
					<= c.at)
		ORDER BY state_changed_at, id
		LIMIT $6 FOR UPDATE SKIP LOCKED`,
		states, cutStates, types, created, at, batch)
	if err != nil {
		return nil, fmt.Errorf("runs: read the runs a timer ends: %w", sql.Unavailable(err))
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (lifecycle.Run, error) {
		return scanRun(r)
	})
	if err != nil {
		return nil, fmt.Errorf("runs: read the runs a timer ends: %w", sql.Unavailable(err))
	}
	return out, nil
}
