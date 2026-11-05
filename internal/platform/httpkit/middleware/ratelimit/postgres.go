package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Postgres is the Store every serve replica shares: the platform stream's
// rate_limit_buckets table, refilled by the database clock.
type Postgres struct{ db tx.Querier }

// NewPostgres keeps buckets in db.
func NewPostgres(db tx.Querier) *Postgres { return &Postgres{db: db} }

// takeToken refills the bucket by the time since refilled_at, capped at the
// burst, and takes a token when one is there, in one statement. A new
// bucket starts full and gives its first token. refilled_at never moves
// back, so a database clock that steps back refills nothing.
//
// Whether a token was taken is read from the old and new rows: a taken
// token leaves the bucket at least one below old plus the refill, and a
// refused request leaves it at exactly that, so comparing half a token
// below survives floating-point rounding. A new bucket has no old row.
//
//nolint:gosec // a rate-limit token, not a credential
const takeToken = `
INSERT INTO rate_limit_buckets AS b (caller, class, tokens, refilled_at)
VALUES ($1, $2, $4::float8 - 1, now())
ON CONFLICT (caller, class) DO UPDATE SET (tokens, refilled_at) = (
    SELECT CASE WHEN r.tokens >= 1 THEN r.tokens - 1 ELSE r.tokens END,
           greatest(b.refilled_at, now())
    FROM (SELECT least($4::float8, b.tokens + $3::float8 *
        greatest(0, extract(epoch FROM now() - b.refilled_at)::float8)) AS tokens) r)
RETURNING new.tokens,
    old.tokens IS NULL OR new.tokens < old.tokens + $3::float8 *
        extract(epoch FROM new.refilled_at - old.refilled_at)::float8 - 0.5`

// Take implements Store.
func (p *Postgres) Take(ctx context.Context, key Key, l Limit) (State, error) {
	var (
		tokens  float64
		allowed bool
	)
	err := tx.Q(ctx, p.db).QueryRow(ctx, takeToken, key.Caller, string(key.Class), l.Rate, l.Burst).
		Scan(&tokens, &allowed)
	if err != nil {
		return State{}, fmt.Errorf("take a rate-limit token: %w", err)
	}
	return stateOf(l, tokens, allowed), nil
}

var _ Store = (*Postgres)(nil)

// DisabledCallersKey is the infraweave_invalidate payload that clears every
// replica's copy of api_disabled_callers. Whatever changes the table sends
// it after the change commits.
const DisabledCallersKey = "api_disabled_callers"

// Table is the Switch every serve replica shares: the platform stream's
// api_disabled_callers table. Each replica reads the table once and keeps
// it until Watch hears DisabledCallersKey, the listening connection
// reconnects, or the earliest until passes, so an operator's change takes
// effect on every replica at once without a query per request.
type Table struct {
	db  tx.Querier
	now func() time.Time

	mu sync.Mutex
	// callers is the copy, nil until read. expires is the earliest until
	// among them by this replica's clock, zero when none has one; past it
	// the table is read again and the database decides who is still off.
	callers map[string]struct{}
	expires time.Time
}

// NewTable reads db's api_disabled_callers, timing the expiry of its copy by
// now.
func NewTable(db tx.Querier, now func() time.Time) *Table {
	return &Table{db: db, now: now}
}

// Disabled implements Switch.
func (t *Table) Disabled(ctx context.Context, caller string) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if t.callers == nil || (!t.expires.IsZero() && !now.Before(t.expires)) {
		if err := t.read(ctx, now); err != nil {
			return false, err
		}
	}
	_, off := t.callers[caller]
	return off, nil
}

// read replaces the copy with the callers disabled now, by the database
// clock. The caller holds t.mu.
func (t *Table) read(ctx context.Context, now time.Time) error {
	rows, err := tx.Q(ctx, t.db).Query(ctx, `
SELECT caller, extract(epoch FROM until - now())::float8
FROM api_disabled_callers
WHERE until IS NULL OR until > now()`)
	if err != nil {
		return fmt.Errorf("read disabled callers: %w", err)
	}
	defer rows.Close()
	callers := map[string]struct{}{}
	var expires time.Time
	for rows.Next() {
		var (
			caller string
			left   *float64
		)
		if err := rows.Scan(&caller, &left); err != nil {
			return fmt.Errorf("read disabled callers: %w", err)
		}
		callers[caller] = struct{}{}
		if left != nil {
			if until := now.Add(duration(*left)); expires.IsZero() || until.Before(expires) {
				expires = until
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read disabled callers: %w", err)
	}
	t.callers, t.expires = callers, expires
	return nil
}

// Invalidate drops the copy, so the next request reads the table.
func (t *Table) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callers = nil
}

// Watch invalidates the copy on each DisabledCallersKey and each Resync of
// sub, a subscription to notify.Invalidate, until ctx is done.
func (t *Table) Watch(ctx context.Context, sub *notify.Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-sub.C:
			if m.Resync || m.Payload == DisabledCallersKey {
				t.Invalidate()
			}
		}
	}
}

// Switches disables a caller that any of its switches disables, as the
// server.rate_limit.disabled_callers list and the table together do.
type Switches []Switch

// Disabled implements Switch.
func (s Switches) Disabled(ctx context.Context, caller string) (bool, error) {
	for _, sw := range s {
		off, err := sw.Disabled(ctx, caller)
		if err != nil || off {
			return off, err //nolint:wrapcheck // each switch names its source
		}
	}
	return false, nil
}
