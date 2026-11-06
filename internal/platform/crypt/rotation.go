package crypt

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
)

// Changed is the infraweave_invalidate payload of a change to
// encryption_keys, on which every replica reloads its keyring.
const Changed = "encryption_keys"

// ErrRotating reports a rotation asked for while the previous one's
// retiring key still awaits its reseal.
var ErrRotating = errors.New("a rotation is under way: a data key is still retiring")

// DB is a pool the rotation operations begin their transactions on.
type DB interface {
	Querier
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Reload reads the keyring again from the database it was loaded from,
// with the provider it was loaded with, and replaces the keys only when
// every key unwraps. A replica whose KEK no longer unwraps the keys, as
// after a rewrap to another provider, keeps serving with the keys it holds
// until it restarts on the new provider.
func (k *Keyring) Reload(ctx context.Context) error {
	fresh, err := load(ctx, k.db, k.p, servable)
	if err != nil {
		return err
	}
	if fresh.active == 0 {
		return fmt.Errorf("%w: no active data key", ErrKeyring)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.active, k.keys = fresh.active, fresh.keys
	return nil
}

// Watch reloads the keyring on each Changed and each Resync of sub, a
// subscription to notify.Invalidate, until ctx is done, logging a reload
// that fails.
func (k *Keyring) Watch(ctx context.Context, sub *notify.Subscription, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-sub.C:
			if !m.Resync && m.Payload != Changed {
				continue
			}
			if err := k.Reload(ctx); err != nil && ctx.Err() == nil {
				log.Error(
					"keyring reload failed; sealing with the keys already loaded",
					"error",
					err,
				)
			} else if err == nil {
				log.Info("keyring reloaded", "active_key", k.Active())
			}
		}
	}
}

// Rotate makes a new data key, wrapped by p, the active one and marks the
// active key retiring, in tx, when the active key is at least olderThan
// old; 0 rotates whatever its age. It reports whether it rotated, and
// notifies Changed in tx, so replicas reload when tx commits. The caller
// enqueues secrets.reseal in the same transaction. It returns ErrRotating
// while a retiring key remains, so rotations never pile up; a concurrent
// rotation waits on the active key's lock and then sees that key retiring.
func Rotate(
	ctx context.Context,
	tx Querier,
	p kek.Provider,
	olderThan time.Duration,
) (bool, error) {
	if _, err := tx.Exec(ctx, `
		SELECT 1 FROM encryption_keys WHERE state IN ('active', 'retiring') FOR UPDATE`); err != nil {
		return false, fmt.Errorf("%w: rotate: %w", ErrKeyring, sql.Unavailable(err))
	}
	var retiring, due bool
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(bool_or(state = 'retiring'), false),
		       coalesce(bool_or(state = 'active' AND created_at <= now() - $1 * interval '1 second'), false)
		FROM encryption_keys WHERE state IN ('active', 'retiring')`, olderThan.Seconds()).
		Scan(&retiring, &due); err != nil {
		return false, fmt.Errorf("%w: rotate: %w", ErrKeyring, sql.Unavailable(err))
	}
	switch {
	case retiring:
		return false, ErrRotating
	case !due:
		return false, nil
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE encryption_keys SET state = 'retiring' WHERE state = 'active'`,
	); err != nil {
		return false, fmt.Errorf("%w: rotate: %w", ErrKeyring, sql.Unavailable(err))
	}
	if err := create(ctx, tx, p); err != nil {
		return false, err
	}
	if err := notify.Notify(ctx, tx, notify.Invalidate, Changed); err != nil {
		return false, fmt.Errorf("%w: rotate: %w", ErrKeyring, err)
	}
	return true, nil
}

// Rewrap unwraps each data key with from and wraps it with to, one key
// per transaction, and returns how many it rewrapped. A key whose kek_ref
// is to's and that to unwraps is left as it is, so a rewrap that stopped
// part-way runs again from the start; two local KEKs share a kek_ref, so
// the unwrap, not the name, decides. Values and objects are
// untouched: the keys inside the wrapping do not change.
func Rewrap(ctx context.Context, db DB, from, to kek.Provider) (int, error) {
	rows, err := db.Query(ctx, `SELECT id FROM encryption_keys ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("%w: rewrap: %w", ErrKeyring, sql.Unavailable(err))
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, fmt.Errorf("%w: rewrap: %w", ErrKeyring, sql.Unavailable(err))
	}
	n := 0
	for _, id := range ids {
		done, err := rewrapOne(ctx, db, uint32(id), from, to) //nolint:gosec // a positive integer
		if err != nil {
			return n, err
		}
		if done {
			n++
		}
	}
	return n, nil
}

func rewrapOne(ctx context.Context, db DB, id uint32, from, to kek.Provider) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("%w: rewrap data key %d: %w", ErrKeyring, id, sql.Unavailable(err))
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var (
		wrapped []byte
		ref     string
	)
	err = tx.QueryRow(ctx, `SELECT wrapped_key, kek_ref FROM encryption_keys WHERE id = $1 FOR UPDATE`, id).
		Scan(&wrapped, &ref)
	if errors.Is(err, pgx.ErrNoRows) {
		// A reseal deleted the key since the list was read.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: rewrap data key %d: %w", ErrKeyring, id, sql.Unavailable(err))
	}
	if ref == to.Ref() {
		if _, err := to.Unwrap(ctx, id, wrapped); err == nil {
			return false, nil
		}
	}
	key, err := from.Unwrap(ctx, id, wrapped)
	if err != nil {
		return false, fmt.Errorf("%w: data key %d: %w", ErrKeyring, id, err)
	}
	if wrapped, err = to.Wrap(ctx, id, key); err != nil {
		return false, fmt.Errorf("%w: wrap data key %d: %w", ErrKeyring, id, err)
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE encryption_keys SET wrapped_key = $2, kek_ref = $3 WHERE id = $1`,
		id,
		wrapped,
		to.Ref(),
	); err != nil {
		return false, fmt.Errorf("%w: rewrap data key %d: %w", ErrKeyring, id, sql.Unavailable(err))
	}
	if err := notify.Notify(ctx, tx, notify.Invalidate, Changed); err != nil {
		return false, fmt.Errorf("%w: rewrap data key %d: %w", ErrKeyring, id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("%w: rewrap data key %d: %w", ErrKeyring, id, sql.Unavailable(err))
	}
	return true, nil
}

// ResealBatch is how many rows one reseal transaction rewrites.
const ResealBatch = 500

// resealBatch and afterBatch are what a test changes: a smaller batch,
// and a hook after each committed batch that can stop the walk.
var (
	resealBatch = ResealBatch
	afterBatch  = func(context.Context) error { return nil }
)

// Reseal brings every sealed value of columns under the active data key,
// then retires and deletes the keys no longer active, as decision 0013's
// secrets.reseal does. It walks each column with keyset pagination,
// ResealBatch rows per transaction, and repeats the walk until one finds
// nothing to reseal, so a value a replica sealed with the old key before
// it reloaded is caught. Only then is the old key marked retired, which
// replicas stop opening with; a final walk, with the retired key loaded
// here, reseals anything still under it, and the key is deleted once a
// walk finds nothing. It is idempotent: one that stops part-way runs again
// from the start.
func Reseal(ctx context.Context, db DB, p kek.Provider, columns []Column) (int, error) {
	all, err := load(ctx, db, p, []string{"active", "retiring", "retired"})
	if err != nil {
		return 0, err
	}
	if all.active == 0 || len(all.keys) == 1 {
		return 0, nil
	}
	total := 0
	for {
		n, err := all.walk(ctx, db, columns)
		total += n
		if err != nil || n == 0 {
			if err != nil {
				return total, err
			}
			break
		}
	}
	if err := changeKeys(ctx, db, `
		UPDATE encryption_keys SET state = 'retired', retired_at = now() WHERE state = 'retiring'`); err != nil {
		return total, err
	}
	for {
		n, err := all.walk(ctx, db, columns)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			break
		}
	}
	return total, changeKeys(ctx, db, `DELETE FROM encryption_keys WHERE state = 'retired'`)
}

// changeKeys runs stmt and notifies Changed in one transaction.
func changeKeys(ctx context.Context, db DB, stmt string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: reseal: %w", ErrKeyring, sql.Unavailable(err))
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("%w: reseal: %w", ErrKeyring, sql.Unavailable(err))
	}
	if err := notify.Notify(ctx, tx, notify.Invalidate, Changed); err != nil {
		return fmt.Errorf("%w: reseal: %w", ErrKeyring, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: reseal: %w", ErrKeyring, sql.Unavailable(err))
	}
	return nil
}

// walk reseals every value of columns not under the active key and
// returns how many it resealed.
func (k *Keyring) walk(ctx context.Context, db DB, columns []Column) (int, error) {
	total := 0
	for _, c := range columns {
		n, err := k.walkColumn(ctx, db, c)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// walkColumn reseals c's values a batch at a time. Pages follow the
// primary key's text in the C collation, which orders every key type the
// same way on every server; each row is rewritten by the ctid it was
// locked at, which no other transaction can move while the lock holds.
func (k *Keyring) walkColumn(ctx context.Context, db DB, c Column) (int, error) {
	active := binary.BigEndian.AppendUint32(nil, k.Active())
	table := pgx.Identifier{c.Table}.Sanitize()
	column := pgx.Identifier{c.Name}.Sanitize()
	keys := make([]string, len(c.Key))
	params := make([]string, len(c.Key))
	for i, name := range c.Key {
		keys[i] = pgx.Identifier{name}.Sanitize() + `::text COLLATE "C"`
		params[i] = fmt.Sprintf("$%d", i+3)
	}
	order := strings.Join(keys, ", ")
	total := 0
	var after []string
	for {
		var page string
		args := []any{active, resealBatch}
		if after != nil {
			page = fmt.Sprintf(" AND (%s) > (%s)", order, strings.Join(params, ", "))
			for _, v := range after {
				args = append(args, v)
			}
		}
		query := fmt.Sprintf(`
			SELECT ctid, %s, %s FROM %s
			WHERE %s IS NOT NULL AND substring(%s from 2 for 4) <> $1%s
			ORDER BY %s LIMIT $2 FOR UPDATE`,
			strings.Join(keys, ", "), column, table, column, column, page, order)
		n, last, err := k.resealPage(ctx, db, c, query, args, table, column)
		total += n
		if err != nil || n < resealBatch {
			return total, err
		}
		after = last
	}
}

// resealPage reseals the rows query selects in one transaction and
// returns how many there were and the last one's primary key.
func (k *Keyring) resealPage(
	ctx context.Context,
	db DB,
	c Column,
	query string,
	args []any,
	table, column string,
) (int, []string, error) {
	fail := func(err error) (int, []string, error) {
		return 0, nil, fmt.Errorf("%w: reseal %s.%s: %w", ErrKeyring, c.Table, c.Name, err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return fail(sql.Unavailable(err))
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return fail(sql.Unavailable(err))
	}
	type row struct {
		ctid   pgtype.TID
		pk     []string
		sealed []byte
	}
	var found []row
	for rows.Next() {
		r := row{pk: make([]string, len(c.Key))}
		dest := []any{&r.ctid}
		for i := range r.pk {
			dest = append(dest, &r.pk[i])
		}
		if err := rows.Scan(append(dest, &r.sealed)...); err != nil {
			rows.Close()
			return fail(err)
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return fail(sql.Unavailable(err))
	}
	for _, r := range found {
		v, err := k.OpenColumn(ctx, c, r.pk, r.sealed)
		if err != nil {
			return fail(err)
		}
		resealed, err := k.SealColumn(ctx, c, r.pk, v.Reveal())
		if err != nil {
			return fail(err)
		}
		if _, err := tx.Exec(
			ctx,
			fmt.Sprintf(
				`UPDATE %s SET %s = $1 WHERE ctid = $2`,
				table,
				column,
			),
			resealed,
			r.ctid,
		); err != nil {
			return fail(sql.Unavailable(err))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fail(sql.Unavailable(err))
	}
	if len(found) == 0 {
		return 0, nil, nil
	}
	if err := afterBatch(ctx); err != nil {
		return len(found), nil, err
	}
	return len(found), found[len(found)-1].pk, nil
}
