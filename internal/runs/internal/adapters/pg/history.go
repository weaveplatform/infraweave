package pg

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
)

// HistoryAt reads one account-bound state history in a single statement, so a
// concurrent transition cannot mix its identity and historical cutoff. The
// exact cutoff must already exist; a later current state never replaces it.
func (s *Store) HistoryAt(ctx context.Context, acct kernel.AccountID, run kernel.ULID, sequence int64) (runsapi.RunHistory, error) {
	if acct.IsZero() || run.IsZero() || sequence <= 0 {
		return runsapi.RunHistory{}, runsapi.ErrHistoryNotFound
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT r.stack_id, r.type,
		COALESCE(r.commit->>'hash',''), COALESCE(r.commit->>'branch',''),
		COALESCE(r.commit->>'tag',''), COALESCE(r.local_workspace,'') <> '', r.created_at,
		t.sequence, t.state, t.timestamp
		FROM runs r JOIN run_state_transitions t
		ON t.account_id = r.account_id AND t.run_id = r.id
		WHERE r.account_id = $1 AND r.id = $2 AND t.sequence <= $3
		AND EXISTS (SELECT 1 FROM run_state_transitions last
			WHERE last.account_id = r.account_id AND last.run_id = r.id AND last.sequence = $3)
		ORDER BY t.sequence`, acct, run, sequence)
	if err != nil {
		return runsapi.RunHistory{}, fmt.Errorf("runs: read history of run %s: %w", run, sql.Unavailable(err))
	}
	defer rows.Close()
	out := runsapi.RunHistory{ID: run, Through: sequence, Transitions: []runsapi.RunTransition{}}
	for rows.Next() {
		var transition runsapi.RunTransition
		var typ, state string
		if err := rows.Scan(&out.Stack, &typ, &out.CommitHash, &out.Branch, &out.Tag,
			&out.Local, &out.CreatedAt, &transition.Sequence, &state, &transition.At); err != nil {
			return runsapi.RunHistory{}, fmt.Errorf("runs: read history row of run %s: %w", run, sql.Unavailable(err))
		}
		out.Type, transition.State = kernel.RunType(typ), kernel.RunState(state)
		out.Transitions = append(out.Transitions, transition)
	}
	if err := rows.Err(); err != nil {
		return runsapi.RunHistory{}, fmt.Errorf("runs: read history rows of run %s: %w", run, sql.Unavailable(err))
	}
	if len(out.Transitions) == 0 {
		return runsapi.RunHistory{}, runsapi.ErrHistoryNotFound
	}
	return out, nil
}

var _ runsapi.RunHistories = (*Store)(nil)
