package idempotency

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Postgres is the Store every serve replica shares: the platform stream's
// idempotency_keys table. Locks and expiry compare against the database
// clock, so replicas whose clocks differ agree on every outcome. A stored
// body is sealed, since it can hold whatever the request created, such as
// a secret shown only once.
//
// ref: decisions 0005 and 0013
type Postgres struct {
	db        tx.Querier
	keys      Sealer
	ttl, lock float64 // seconds
}

// Sealer seals and opens a stored body; *crypt.Keyring is one.
type Sealer interface {
	SealColumn(ctx context.Context, c crypt.Column, pk []string, plaintext []byte) ([]byte, error)
	OpenColumn(ctx context.Context, c crypt.Column, pk []string, sealed []byte) (crypt.Value, error)
}

// Body is the sealed column a stored body lives in, which crypt.Registry
// lists.
var Body = crypt.Column{
	Table: "idempotency_keys",
	Name:  "body_sealed",
	Key:   []string{"caller", "key"},
}

// NewPostgres keeps keys in db as c says, sealing bodies with keys.
func NewPostgres(db tx.Querier, c Config, keys Sealer) *Postgres {
	return &Postgres{db: db, keys: keys, ttl: c.TTL.Seconds(), lock: c.Lock.Seconds()}
}

// claimKey inserts the key as in flight, or takes over a row that has
// expired or whose lock has lapsed. It returns a row only when this request
// holds the key.
const claimKey = `
INSERT INTO idempotency_keys AS k
    (caller, key, fingerprint, state, locked_until, created_at, expires_at)
VALUES ($1, $2, $3, 'in_flight',
    now() + $4 * interval '1 second', now(), now() + $5 * interval '1 second')
ON CONFLICT (caller, key) DO UPDATE SET
    fingerprint = excluded.fingerprint, state = 'in_flight',
    locked_until = excluded.locked_until, status = NULL, content_type = NULL,
    location = NULL, body_sealed = NULL, created_at = excluded.created_at,
    expires_at = excluded.expires_at
WHERE k.expires_at <= now() OR (k.state = 'in_flight' AND k.locked_until <= now())
RETURNING true`

const readKey = `
SELECT fingerprint, status, coalesce(content_type, ''), coalesce(location, ''), body_sealed
FROM idempotency_keys
WHERE caller = $1 AND key = $2 AND expires_at > now()`

// claimAttempts bounds Claim's loop. A claim that loses reads the row it
// lost to in a second statement, and that row can be released or expire in
// between; the next attempt then claims it.
const claimAttempts = 3

var errContended = errors.New("idempotency key changed hands on every attempt")

// Claim implements Store.
func (p *Postgres) Claim(ctx context.Context, key Key, fingerprint string) (Record, bool, error) {
	db := tx.Q(ctx, p.db)
	for range claimAttempts {
		var claimed bool
		err := db.QueryRow(ctx, claimKey, key.Caller, key.Value, fingerprint, p.lock, p.ttl).
			Scan(&claimed)
		if err == nil {
			return Record{}, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Record{}, false, fmt.Errorf("claim idempotency key: %w", sql.Unavailable(err))
		}
		var (
			rec    Record
			status *int32
			resp   Response
			sealed []byte
		)
		err = db.QueryRow(ctx, readKey, key.Caller, key.Value).
			Scan(&rec.Fingerprint, &status, &resp.ContentType, &resp.Location, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return Record{}, false, fmt.Errorf("read idempotency key: %w", sql.Unavailable(err))
		}
		if sealed != nil {
			body, err := p.keys.OpenColumn(ctx, Body, []string{key.Caller, key.Value}, sealed)
			if err != nil {
				return Record{}, false, fmt.Errorf("open idempotency key's body: %w", err)
			}
			resp.Body = body.Reveal()
		}
		if status != nil {
			resp.Status = int(*status)
			rec.Response = &resp
		}
		return rec, false, nil
	}
	return Record{}, false, errContended
}

// Complete implements Store.
func (p *Postgres) Complete(ctx context.Context, key Key, resp Response) error {
	var sealed []byte
	if len(resp.Body) != 0 {
		var err error
		sealed, err = p.keys.SealColumn(ctx, Body, []string{key.Caller, key.Value}, resp.Body)
		if err != nil {
			return fmt.Errorf("seal idempotency key's body: %w", err)
		}
	}
	tag, err := tx.Q(ctx, p.db).Exec(ctx, `
UPDATE idempotency_keys
SET state = 'done', status = $3, content_type = $4, location = $5, body_sealed = $6,
    expires_at = now() + $7 * interval '1 second'
WHERE caller = $1 AND key = $2 AND state = 'in_flight' AND expires_at > now()`,
		key.Caller, key.Value, resp.Status, resp.ContentType, resp.Location, sealed, p.ttl)
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", sql.Unavailable(err))
	}
	if tag.RowsAffected() == 0 {
		return ErrNotReserved
	}
	return nil
}

// Release implements Store.
func (p *Postgres) Release(ctx context.Context, key Key) error {
	_, err := tx.Q(ctx, p.db).Exec(ctx,
		`DELETE FROM idempotency_keys WHERE caller = $1 AND key = $2 AND state = 'in_flight'`,
		key.Caller, key.Value)
	if err != nil {
		return fmt.Errorf("release idempotency key: %w", sql.Unavailable(err))
	}
	return nil
}

// Purge deletes the keys whose time to live has passed, which Claim
// would treat as absent anyway, and returns how many it deleted. The
// hourly idempotency.purge periodic task runs it.
//
// ref: decision 0005
func (p *Postgres) Purge(ctx context.Context) (int64, error) {
	tag, err := tx.Q(ctx, p.db).Exec(ctx, `DELETE FROM idempotency_keys WHERE expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("purge idempotency keys: %w", sql.Unavailable(err))
	}
	return tag.RowsAffected(), nil
}

var _ Store = (*Postgres)(nil)
