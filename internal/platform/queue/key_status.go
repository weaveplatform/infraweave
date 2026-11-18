package queue

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// ErrKeyScope reports a key lookup without job kinds or with an empty prefix.
var ErrKeyScope = errors.New("job key scope needs named kinds and nonempty prefixes")

// KeyStatus counts an account's jobs of selected kinds under a literal unique-key
// prefix. It contains no payloads, errors or domain-specific outcome.
type KeyStatus struct {
	Pending   int64
	Running   int64
	Succeeded int64
	Dead      int64
	Cancelled int64
}

// StatusByKeyPrefixes reads each prefix's current counts in the context's
// transaction, or on the pool. Prefixes are literal, may overlap, and duplicate
// inputs do not multiply counts. An empty batch answers an empty map. A zero
// account selects installation jobs alone; only the kinds' names are used.
func (q *Queue) StatusByKeyPrefixes(ctx context.Context, account kernel.ULID, kinds []Kind, prefixes []string) (map[string]KeyStatus, error) {
	if len(prefixes) == 0 {
		return map[string]KeyStatus{}, nil
	}
	names, err := scopeNames(kinds, prefixes)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Q(ctx, q.q).Query(ctx, `WITH prefixes AS (SELECT DISTINCT unnest($3::text[]) AS prefix)
		SELECT p.prefix,
			count(j.id) FILTER (WHERE j.state='pending'),
			count(j.id) FILTER (WHERE j.state='running'),
			count(j.id) FILTER (WHERE j.state='succeeded'),
			count(j.id) FILTER (WHERE j.state='dead'),
			count(j.id) FILTER (WHERE j.state='cancelled')
		FROM prefixes p LEFT JOIN jobs j ON (j.account_id=$1 OR (j.account_id IS NULL AND $1::uuid IS NULL))
			AND j.kind=ANY($2::text[]) AND starts_with(j.unique_key,p.prefix)
		GROUP BY p.prefix`, account, names, prefixes)
	if err != nil {
		return nil, fmt.Errorf("read job key statuses: %w", sql.Unavailable(err))
	}
	out := make(map[string]KeyStatus, len(prefixes))
	var prefix string
	var status KeyStatus
	_, err = pgx.ForEachRow(rows, []any{&prefix, &status.Pending, &status.Running, &status.Succeeded, &status.Dead, &status.Cancelled}, func() error {
		out[prefix] = status
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read job key statuses: %w", sql.Unavailable(err))
	}
	return out, nil
}

// HoldByKeyPrefixTx locks all matching jobs, including terminal ones, in job-ID
// order until the caller's transaction ends. Operator retry and worker completion
// wait on those locks. Its second read also locks children committed while the
// first read waited for their parent. Callers must fence producers against later
// inserts and recheck their own version after locking; these are row locks, not a
// predicate lock. A zero account selects installation jobs alone.
func (q *Queue) HoldByKeyPrefixTx(ctx context.Context, account kernel.ULID, kinds []Kind, prefix string) (KeyStatus, error) {
	if !tx.In(ctx) {
		return KeyStatus{}, ErrNoTransaction
	}
	names, err := scopeNames(kinds, []string{prefix})
	if err != nil {
		return KeyStatus{}, err
	}
	if _, err := q.holdKeyPrefix(ctx, account, names, prefix); err != nil {
		return KeyStatus{}, err
	}
	return q.holdKeyPrefix(ctx, account, names, prefix)
}

func scopeNames(kinds []Kind, prefixes []string) ([]string, error) {
	if len(kinds) == 0 {
		return nil, ErrKeyScope
	}
	names := make([]string, len(kinds))
	for i, kind := range kinds {
		if kind.Name == "" {
			return nil, ErrKeyScope
		}
		names[i] = kind.Name
	}
	for _, prefix := range prefixes {
		if prefix == "" {
			return nil, ErrKeyScope
		}
	}
	return names, nil
}

func (q *Queue) holdKeyPrefix(ctx context.Context, account kernel.ULID, kinds []string, prefix string) (KeyStatus, error) {
	rows, err := tx.Q(ctx, q.q).Query(ctx, `SELECT state FROM jobs
		WHERE (account_id=$1 OR (account_id IS NULL AND $1::uuid IS NULL))
			AND kind=ANY($2::text[]) AND starts_with(unique_key,$3)
		ORDER BY id FOR UPDATE`, account, kinds, prefix)
	if err != nil {
		return KeyStatus{}, fmt.Errorf("hold job key prefix: %w", sql.Unavailable(err))
	}
	var out KeyStatus
	var state string
	_, err = pgx.ForEachRow(rows, []any{&state}, func() error {
		switch state {
		case "pending":
			out.Pending++
		case "running":
			out.Running++
		case "succeeded":
			out.Succeeded++
		case "dead":
			out.Dead++
		case "cancelled":
			out.Cancelled++
		}
		return nil
	})
	if err != nil {
		return KeyStatus{}, fmt.Errorf("hold job key prefix: %w", sql.Unavailable(err))
	}
	return out, nil
}
