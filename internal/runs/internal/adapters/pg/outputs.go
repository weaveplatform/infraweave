package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// outputValue is a stack output's value, sealed to its row whether or
// not it is sensitive, so a value copied to another stack or name does
// not open.
//
// ref: decision 0013 § "Sealed columns"
var outputValue = crypt.Column{
	Table: "stack_outputs", Name: "value_sealed", Key: []string{"account_id", "stack_id", "name"},
}

// ReplaceOutputs replaces the outputs of run's stack with outs, written
// by run, each value sealed to its row and a sensitive output with no
// value kept with none, in the context's transaction.
func (s *Store) ReplaceOutputs(ctx context.Context, run lifecycle.Run, outs map[string]plan.Output) error {
	names := slices.Sorted(maps.Keys(outs))
	sealed := make([][]byte, len(names))
	sensitive := make([]bool, len(names))
	descriptions := make([]string, len(names))
	for i, name := range names {
		o := outs[name]
		sensitive[i], descriptions[i] = o.Sensitive, o.Description
		if o.Value == nil {
			continue
		}
		v, err := s.seal.SealColumn(ctx, outputValue,
			[]string{run.Account.UUID(), run.Stack.UUID(), name}, o.Value)
		if err != nil {
			return fmt.Errorf("runs: seal stack %s's output %s: %w", run.Stack, name, err)
		}
		sealed[i] = v
	}
	q := tx.Q(ctx, s.db)
	if _, err := q.Exec(ctx, `DELETE FROM stack_outputs WHERE account_id = $1 AND stack_id = $2`,
		run.Account, run.Stack); err != nil {
		return fmt.Errorf("runs: delete stack %s's outputs: %w", run.Stack, sql.Unavailable(err))
	}
	if _, err := q.Exec(ctx, `INSERT INTO stack_outputs (account_id, stack_id, name, run_id,
		value_sealed, sensitive, description)
		SELECT $1, $2, o.name, $3, o.value_sealed, o.sensitive, NULLIF(o.description, '')
		FROM unnest($4::text[], $5::bytea[], $6::boolean[], $7::text[])
			AS o(name, value_sealed, sensitive, description)`,
		run.Account, run.Stack, run.ID, names, sealed, sensitive, descriptions); err != nil {
		return fmt.Errorf("runs: write stack %s's outputs: %w", run.Stack, sql.Unavailable(err))
	}
	return nil
}

// Outputs reads the outputs of each of acct's stacks, by name, keyed by
// stack ID, opening the value of each that is not sensitive; a stack
// with none is absent.
func (s *Store) Outputs(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]stacksapi.Output, error) {
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT stack_id, name, coalesce(description, ''),
		sensitive, CASE WHEN NOT sensitive THEN value_sealed END
		FROM stack_outputs WHERE account_id = $1 AND stack_id = ANY($2)
		ORDER BY stack_id, name`, acct, stacks)
	if err != nil {
		return nil, fmt.Errorf("runs: read the stacks' outputs: %w", sql.Unavailable(err))
	}
	type row struct {
		stack  kernel.ULID
		out    stacksapi.Output
		sealed []byte
	}
	var read []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.stack, &r.out.Name, &r.out.Description, &r.out.Sensitive,
			&r.sealed); err != nil {
			rows.Close()
			return nil, fmt.Errorf("runs: read a stack's output: %w", sql.Unavailable(err))
		}
		read = append(read, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: read the stacks' outputs: %w", sql.Unavailable(err))
	}
	out := map[kernel.ULID][]stacksapi.Output{}
	for _, r := range read {
		if r.sealed != nil {
			v, err := s.seal.OpenColumn(ctx, outputValue,
				[]string{acct.UUID(), r.stack.UUID(), r.out.Name}, r.sealed)
			if err != nil {
				return nil, fmt.Errorf("runs: open stack %s's output %s: %w", r.stack,
					r.out.Name, err)
			}
			r.out.Value = json.RawMessage(v.Reveal())
		}
		out[r.stack] = append(out[r.stack], r.out)
	}
	return out, nil
}
