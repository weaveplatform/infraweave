package integrity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// ErrUndeclared reports a held column whose entity no Key declares.
var ErrUndeclared = errors.New("held entity has no declared key")

// Key is how the domain that owns entity finds one of its rows: Table's
// Columns, in order.
type Key struct {
	Entity  string
	Table   string
	Columns []string
}

// Held is a column set of another domain's table that holds an entity by
// its key: Table's Columns match the entity's Key.Columns in order.
type Held struct {
	Entity  string
	Table   string
	Columns []string
}

// String names the held columns as table(column, ...).
func (h Held) String() string { return h.Table + "(" + strings.Join(h.Columns, ", ") + ")" }

// Orphans is how many distinct IDs Held holds whose row is gone.
type Orphans struct {
	Held  Held
	Count int64
}

// Count counts, for each of held in order, the distinct IDs it holds
// whose row its entity's Key no longer finds. A held column whose entity
// has no Key, or whose columns do not pair with the key's, is
// ErrUndeclared, before anything is read.
func Count(ctx context.Context, q tx.Querier, keys []Key, held []Held) ([]Orphans, error) {
	byEntity := make(map[string]Key, len(keys))
	for _, k := range keys {
		byEntity[k.Entity] = k
	}
	queries := make([]string, len(held))
	for i, h := range held {
		k, ok := byEntity[h.Entity]
		if !ok || len(k.Columns) != len(h.Columns) || len(h.Columns) == 0 {
			return nil, fmt.Errorf("%w: %s holds %q", ErrUndeclared, h, h.Entity)
		}
		queries[i] = orphans(k, h)
	}
	out := make([]Orphans, len(held))
	for i, h := range held {
		out[i].Held = h
		if err := q.QueryRow(ctx, queries[i]).Scan(&out[i].Count); err != nil {
			return nil, fmt.Errorf("integrity: count %s: %w", h, sql.Unavailable(err))
		}
	}
	return out, nil
}

// orphans is the count of h's distinct non-null IDs that no row of k's
// table matches.
func orphans(k Key, h Held) string {
	cols := make([]string, len(h.Columns))
	match := make([]string, len(h.Columns))
	for i := range h.Columns {
		hc := "h." + pgx.Identifier{h.Columns[i]}.Sanitize()
		cols[i] = hc
		match[i] = "o." + pgx.Identifier{k.Columns[i]}.Sanitize() + " = " + hc
	}
	held := strings.Join(cols, ", ")
	return `SELECT count(*) FROM (SELECT DISTINCT ` + held +
		` FROM ` + pgx.Identifier{h.Table}.Sanitize() + ` h` +
		` WHERE (` + held + `) IS NOT NULL AND NOT EXISTS (SELECT 1 FROM ` +
		pgx.Identifier{k.Table}.Sanitize() + ` o WHERE ` + strings.Join(match, " AND ") +
		`)) x`
}
