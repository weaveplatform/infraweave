package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestUnitKeyStatusScopes(t *testing.T) {
	q := New(nil)
	if got, err := q.StatusByKeyPrefixes(t.Context(), kernel.ULID{}, nil, nil); err != nil || len(got) != 0 {
		t.Fatal("empty lookup reached storage", got, err)
	}
	if _, err := q.HoldByKeyPrefixTx(t.Context(), kernel.ULID{}, []Kind{{Name: "work"}}, "key"); !errors.Is(err, ErrNoTransaction) {
		t.Fatal("unfenced lookup reached storage", err)
	}
	for _, tc := range []struct {
		name   string
		kinds  []Kind
		prefix string
	}{
		{name: "no kinds", prefix: "key"},
		{name: "unnamed kind", kinds: []Kind{{}}, prefix: "key"},
		{name: "empty prefix", kinds: []Kind{{Name: "work"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := q.StatusByKeyPrefixes(t.Context(), kernel.ULID{}, tc.kinds, []string{tc.prefix}); !errors.Is(err, ErrKeyScope) {
				t.Fatal("unsafe scope reached storage", err)
			}
			err := tx.Do(t.Context(), keyStatusDB{}, tx.Options{}, func(ctx context.Context) error {
				_, err := q.HoldByKeyPrefixTx(ctx, kernel.ULID{}, tc.kinds, tc.prefix)
				return err
			})
			if !errors.Is(err, ErrKeyScope) {
				t.Fatal("unsafe lock scope reached storage", err)
			}
		})
	}
}

// TestUnitKeyStatusStorageFailures checks both a refused query and an interrupted
// row stream: neither can be mistaken for a prefix with no active work.
func TestUnitKeyStatusStorageFailures(t *testing.T) {
	cause := errors.New("query interrupted")
	for _, stream := range []bool{false, true} {
		db := keyStatusDB{cause: cause, stream: stream}
		q := New(db)
		if got, err := q.StatusByKeyPrefixes(t.Context(), kernel.ULID{}, []Kind{{Name: "work"}}, []string{"key"}); !errors.Is(err, cause) || got != nil {
			t.Fatal("failed lookup became a zero count", got, err)
		}
		err := tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
			_, err := q.HoldByKeyPrefixTx(ctx, kernel.ULID{}, []Kind{{Name: "work"}}, "key")
			return err
		})
		if !errors.Is(err, cause) {
			t.Fatal("failed lock became a zero count", err)
		}
	}
}

type keyStatusDB struct {
	pgx.Tx
	cause  error
	stream bool
}

func (d keyStatusDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return d, nil }
func (keyStatusDB) Commit(context.Context) error                             { return nil }
func (keyStatusDB) Rollback(context.Context) error                           { return nil }

func (d keyStatusDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if d.stream {
		return keyStatusRows{cause: d.cause}, nil
	}
	return nil, d.cause
}

type keyStatusRows struct {
	pgx.Rows
	cause error
}

func (keyStatusRows) Close()                        {}
func (keyStatusRows) Next() bool                    { return false }
func (r keyStatusRows) Err() error                  { return r.cause }
func (keyStatusRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
