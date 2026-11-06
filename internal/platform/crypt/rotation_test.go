package crypt_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
	"github.com/weaveplatform/infraweave/internal/platform/sql/notify"
)

// objects is the registry's blob_objects column, which the reseal walks.
var objects = crypt.Column{
	Table: "blob_objects",
	Name:  "content_key_sealed",
	Key:   []string{"store", "key"},
}

// put stores a content key sealed by k for blob_objects row key.
func put(t *testing.T, db *pgxpool.Pool, k *crypt.Keyring, key string) {
	t.Helper()
	sealed, err := k.SealColumn(
		t.Context(),
		objects,
		[]string{"states", key},
		[]byte("content-"+key),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `
		INSERT INTO blob_objects (store, key, size, sha256, state, content_key_sealed)
		VALUES ('states', $1, 0, sha256(''), 'live', $2)
		ON CONFLICT (store, key) DO UPDATE SET content_key_sealed = excluded.content_key_sealed`,
		key, sealed); err != nil {
		t.Fatal(err)
	}
}

// underKeys counts blob_objects values by the data key they are sealed
// under, and checks each opens under k with its content.
func underKeys(t *testing.T, db *pgxpool.Pool, k *crypt.Keyring) map[uint32]int {
	t.Helper()
	rows, err := db.Query(t.Context(), `SELECT key, content_key_sealed FROM blob_objects`)
	if err != nil {
		t.Fatal(err)
	}
	found := map[uint32]int{}
	var key string
	var sealed []byte
	if _, err := pgx.ForEachRow(rows, []any{&key, &sealed}, func() error {
		id, err := seal.ColumnKeyID(sealed)
		if err != nil {
			return err
		}
		found[id]++
		v, err := k.OpenColumn(t.Context(), objects, []string{"states", key}, sealed)
		if err != nil || string(v.Reveal()) != "content-"+key {
			return fmt.Errorf("%s: %q, %w", key, v.Reveal(), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

func keyStates(t *testing.T, db *pgxpool.Pool) map[int]string {
	t.Helper()
	rows, err := db.Query(t.Context(), `SELECT id, state FROM encryption_keys`)
	if err != nil {
		t.Fatal(err)
	}
	states := map[int]string{}
	var (
		id    int
		state string
	)
	if _, err := pgx.ForEachRow(rows, []any{&id, &state}, func() error {
		states[id] = state
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return states
}

// rotate rotates in a transaction of its own, as the CLI does.
func rotate(t *testing.T, db *pgxpool.Pool, p kek.Provider, olderThan time.Duration) (bool, error) {
	t.Helper()
	var rotated bool
	err := pgx.BeginFunc(t.Context(), db, func(tx pgx.Tx) error {
		var err error
		rotated, err = crypt.Rotate(t.Context(), tx, p, olderThan)
		return err
	})
	return rotated, err
}

// TestAccRotateAndReseal checks decision 0013's rotation: the old key
// keeps opening while it retires, the reseal walks every value onto the
// new key in batches, and the old key's row is deleted at the end.
func TestAccRotateAndReseal(t *testing.T) {
	crypt.SetResealBatch(t, 3)
	db := pool(t)
	p := provider(t, 1)
	k := load(t, db, p)
	for i := range 7 {
		put(t, db, k, fmt.Sprint("v", i))
	}
	if rotated, err := rotate(t, db, p, 0); err != nil || !rotated {
		t.Fatalf("rotate: %v, %v", rotated, err)
	}
	if _, err := rotate(t, db, p, 0); !errors.Is(err, crypt.ErrRotating) {
		t.Errorf("a second rotation before the reseal: %v", err)
	}
	if err := k.Reload(t.Context()); err != nil || k.Active() != 2 {
		t.Fatalf("reload: %d, %v", k.Active(), err)
	}
	if got := underKeys(t, db, k); got[1] != 7 {
		t.Errorf("before the reseal: %v", got)
	}
	n, err := crypt.Reseal(t.Context(), db, p)
	if err != nil || n != 7 {
		t.Fatalf("Reseal = %d, %v", n, err)
	}
	if got := underKeys(t, db, k); got[2] != 7 || len(got) != 1 {
		t.Errorf("after the reseal: %v", got)
	}
	if states := keyStates(t, db); len(states) != 1 || states[2] != "active" {
		t.Errorf("keys after the reseal: %v", states)
	}
	if n, err := crypt.Reseal(t.Context(), db, p); err != nil || n != 0 {
		t.Errorf("a second reseal: %d, %v", n, err)
	}
	// With the old key gone, a later rotation proceeds.
	if rotated, err := rotate(t, db, p, 0); err != nil || !rotated {
		t.Errorf("the next rotation: %v, %v", rotated, err)
	}
}

var errStop = errors.New("the job stopped")

// TestAccResealInterrupted checks that a reseal stopped part-way leaves
// every value readable and the old key in place, and that running it again
// finishes the job.
func TestAccResealInterrupted(t *testing.T) {
	crypt.SetResealBatch(t, 2)
	db := pool(t)
	p := provider(t, 1)
	k := load(t, db, p)
	for i := range 5 {
		put(t, db, k, fmt.Sprint("v", i))
	}
	if _, err := rotate(t, db, p, 0); err != nil {
		t.Fatal(err)
	}
	if err := k.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	crypt.AfterResealBatch(t, func(context.Context) error { return errStop })
	if _, err := crypt.Reseal(t.Context(), db, p); !errors.Is(err, errStop) {
		t.Fatalf("Reseal: %v", err)
	}
	if got := underKeys(t, db, k); got[1] != 3 || got[2] != 2 {
		t.Errorf("after one batch: %v", got)
	}
	if states := keyStates(t, db); states[1] != "retiring" {
		t.Errorf("the old key after an interrupted reseal: %v", states)
	}
	crypt.AfterResealBatch(t, func(context.Context) error { return nil })
	if _, err := crypt.Reseal(t.Context(), db, p); err != nil {
		t.Fatal(err)
	}
	if got := underKeys(t, db, k); got[2] != 5 || len(got) != 1 {
		t.Errorf("after the second run: %v", got)
	}
	if states := keyStates(t, db); len(states) != 1 {
		t.Errorf("keys: %v", states)
	}
}

// TestAccResealStraggler checks that a value a replica seals with the old
// key after the walk has passed it, because the replica has not reloaded,
// is caught by the next walk, and that the key is not deleted while it
// references anything.
func TestAccResealStraggler(t *testing.T) {
	crypt.SetResealBatch(t, 2)
	db := pool(t)
	p := provider(t, 1)
	lagging := load(t, db, p)
	for i := range 3 {
		put(t, db, lagging, fmt.Sprint("v", i))
	}
	if _, err := rotate(t, db, p, 0); err != nil {
		t.Fatal(err)
	}
	once := true
	// The hook's helpers run on t.Context(), which outlives the walk.
	//nolint:contextcheck // see above
	crypt.AfterResealBatch(t, func(context.Context) error {
		if once {
			once = false
			put(t, db, lagging, "v0")
			put(t, db, lagging, "late")
		}
		if states := keyStates(t, db); states[1] == "" {
			return errors.New("the old key was deleted while a walk was under way")
		}
		return nil
	})
	if _, err := crypt.Reseal(t.Context(), db, p); err != nil {
		t.Fatal(err)
	}
	k := load(t, db, p)
	if got := underKeys(t, db, k); got[2] != 4 || len(got) != 1 {
		t.Errorf("after the reseal: %v", got)
	}
	if states := keyStates(t, db); len(states) != 1 || states[2] != "active" {
		t.Errorf("keys: %v", states)
	}
}

// TestAccResealUnknownKey checks that a value under a key the keyring no
// longer holds stops the reseal, naming the column, rather than being
// skipped as if it were done.
func TestAccResealUnknownKey(t *testing.T) {
	db := pool(t)
	p := provider(t, 1)
	k := load(t, db, p)
	put(t, db, k, "v0")
	if _, err := db.Exec(
		t.Context(),
		`UPDATE blob_objects SET content_key_sealed = overlay(content_key_sealed placing '\x00000063' from 2)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := rotate(t, db, p, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := crypt.Reseal(t.Context(), db, p); !errors.Is(err, crypt.ErrUnknownKey) {
		t.Errorf("Reseal: %v", err)
	}
	if states := keyStates(t, db); states[1] != "retiring" {
		t.Errorf("keys: %v", states)
	}
}

// TestAccRotateInterval checks secrets.rotate's rule: a key younger than
// the interval stays, an older one rotates. The key's age is set, never
// waited for.
func TestAccRotateInterval(t *testing.T) {
	db := pool(t)
	p := provider(t, 1)
	load(t, db, p)
	year := 365 * 24 * time.Hour
	if rotated, err := rotate(t, db, p, year); err != nil || rotated {
		t.Errorf("a new key: %v, %v", rotated, err)
	}
	if _, err := db.Exec(t.Context(),
		`UPDATE encryption_keys SET created_at = now() - interval '366 days'`); err != nil {
		t.Fatal(err)
	}
	if rotated, err := rotate(t, db, p, year); err != nil || !rotated {
		t.Errorf("a key older than a year: %v, %v", rotated, err)
	}
}

// TestAccKeyringReloadOnInvalidate checks that a rotation on one replica
// reaches another's keyring through infraweave_invalidate, without a
// restart, and that a reload the KEK cannot unwrap keeps the keys loaded.
func TestAccKeyringReloadOnInvalidate(t *testing.T) {
	db := pool(t)
	p := provider(t, 1)
	first, second := load(t, db, p), load(t, db, p)
	l := notify.NewListener(notify.Static(db.Config().ConnConfig), slog.New(slog.DiscardHandler))
	sub, err := l.Subscribe(notify.Invalidate)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go l.Run(ctx)
	go second.Watch(ctx, sub, slog.New(slog.DiscardHandler))
	until(t, "the listener", l.Listening)

	if _, err := rotate(t, db, p, 0); err != nil {
		t.Fatal(err)
	}
	until(t, "the second keyring to reload", func() bool { return second.Active() == 2 })
	sealed, err := second.Seal(t.Context(), []byte("a"), []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := seal.ColumnKeyID(sealed); id != 2 {
		t.Errorf("sealed under key %d after the reload", id)
	}
	if first.Active() != 1 {
		t.Error("an unwatched keyring reloaded")
	}

	if _, err := crypt.Rewrap(t.Context(), db, p, provider(t, 2)); err != nil {
		t.Fatal(err)
	}
	if err := first.Reload(t.Context()); !errors.Is(err, kek.ErrUnwrap) {
		t.Errorf("a reload after a rewrap: %v", err)
	}
	if first.Active() != 1 {
		t.Error("a failed reload replaced the keys")
	}
}

// TestAccRewrap checks that a rewrap moves every key to the new KEK and
// leaves the keys themselves alone, and that running it again does
// nothing.
func TestAccRewrap(t *testing.T) {
	db := pool(t)
	from, to := provider(t, 1), provider(t, 2)
	k := load(t, db, from)
	put(t, db, k, "v0")
	if _, err := rotate(t, db, from, 0); err != nil {
		t.Fatal(err)
	}
	if n, err := crypt.Rewrap(t.Context(), db, from, to); err != nil || n != 2 {
		t.Fatalf("Rewrap = %d, %v", n, err)
	}
	if _, err := crypt.Load(t.Context(), db, from); !errors.Is(err, kek.ErrUnwrap) {
		t.Errorf("the old KEK still unwraps: %v", err)
	}
	moved := load(t, db, to)
	if got := underKeys(t, db, moved); got[1] != 1 {
		t.Errorf("a value after the rewrap: %v", got)
	}
	// Both KEKs are local, so kek_ref tells them apart nowhere; the
	// rewrapped keys unwrap under to and are left alone.
	if n, err := crypt.Rewrap(t.Context(), db, from, to); err != nil || n != 0 {
		t.Errorf("a second rewrap: %d, %v", n, err)
	}
}

// until polls cond every 20ms until it holds, failing t after 10 seconds.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// refusing wraps nothing, as a KMS that refuses the call does.
type refusing struct{ kek.Provider }

var errRefused = errors.New("the KMS refused")

func (refusing) Wrap(context.Context, uint32, []byte) ([]byte, error) { return nil, errRefused }

// refuse makes PostgreSQL refuse every op on table.
func refuse(t *testing.T, db *pgxpool.Pool, table, op string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), `
		CREATE OR REPLACE FUNCTION refuse() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'refused'; END $$;
		CREATE TRIGGER refuse BEFORE `+op+` ON `+table+` FOR EACH ROW EXECUTE FUNCTION refuse();`); err != nil {
		t.Fatal(err)
	}
}

// TestAccRotationFailures checks that each operation stops with
// ErrKeyring, or the provider's own error, when a step it needs fails,
// and leaves the keyring as it was.
func TestAccRotationFailures(t *testing.T) {
	p := provider(t, 1)
	for name, tc := range map[string]struct {
		setup func(*testing.T, *pgxpool.Pool)
		run   func(*testing.T, *pgxpool.Pool) error
		want  error
	}{
		"rotate on a closed pool": {
			setup: func(_ *testing.T, db *pgxpool.Pool) { db.Close() },
			run: func(t *testing.T, db *pgxpool.Pool) error {
				_, err := crypt.Rotate(t.Context(), db, p, 0)
				return err
			},
		},
		"rotate with a refused update": {
			setup: func(t *testing.T, db *pgxpool.Pool) { refuse(t, db, "encryption_keys", "UPDATE") },
			run:   func(t *testing.T, db *pgxpool.Pool) error { _, err := rotate(t, db, p, 0); return err },
		},
		"rotate with a KMS that refuses": {
			run: func(t *testing.T, db *pgxpool.Pool) error {
				_, err := rotate(t, db, refusing{p}, 0)
				return err
			},
			want: errRefused,
		},
		"rewrap on a closed pool": {
			setup: func(_ *testing.T, db *pgxpool.Pool) { db.Close() },
			run: func(t *testing.T, db *pgxpool.Pool) error {
				_, err := crypt.Rewrap(t.Context(), db, p, provider(t, 2))
				return err
			},
		},
		"rewrap with a refused update": {
			setup: func(t *testing.T, db *pgxpool.Pool) { refuse(t, db, "encryption_keys", "UPDATE") },
			run: func(t *testing.T, db *pgxpool.Pool) error {
				_, err := crypt.Rewrap(t.Context(), db, p, provider(t, 2))
				return err
			},
		},
		"rewrap to a KMS that refuses": {
			run: func(t *testing.T, db *pgxpool.Pool) error {
				_, err := crypt.Rewrap(t.Context(), db, p, refusing{provider(t, 2)})
				return err
			},
			want: errRefused,
		},
		"rewrap from the wrong KEK": {
			run: func(t *testing.T, db *pgxpool.Pool) error {
				_, err := crypt.Rewrap(t.Context(), db, provider(t, 3), provider(t, 2))
				return err
			},
			want: kek.ErrUnwrap,
		},
		"reseal on a closed pool": {
			setup: func(_ *testing.T, db *pgxpool.Pool) { db.Close() },
			run: func(t *testing.T, db *pgxpool.Pool) error {
				_, err := crypt.Reseal(t.Context(), db, p)
				return err
			},
		},
		"reseal with a refused value": {
			setup: func(t *testing.T, db *pgxpool.Pool) {
				put(t, db, load(t, db, p), "v0")
				if _, err := rotate(t, db, p, 0); err != nil {
					t.Fatal(err)
				}
				refuse(t, db, "blob_objects", "UPDATE")
			},
			run: func(t *testing.T, db *pgxpool.Pool) error {
				_, err := crypt.Reseal(t.Context(), db, p)
				return err
			},
		},
		"reseal with a refused retirement": {
			setup: func(t *testing.T, db *pgxpool.Pool) {
				if _, err := rotate(t, db, p, 0); err != nil {
					t.Fatal(err)
				}
				refuse(t, db, "encryption_keys", "UPDATE")
			},
			run: func(t *testing.T, db *pgxpool.Pool) error {
				_, err := crypt.Reseal(t.Context(), db, p)
				return err
			},
		},
		"reload on a closed pool": {
			setup: func(_ *testing.T, db *pgxpool.Pool) { db.Close() },
			run: func(t *testing.T, _ *pgxpool.Pool) error {
				return keyring.Reload(t.Context())
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := pool(t)
			keyring = load(t, db, p)
			if tc.setup != nil {
				tc.setup(t, db)
			}
			want := tc.want
			if want == nil {
				want = crypt.ErrKeyring
			}
			if err := tc.run(t, db); !errors.Is(err, want) {
				t.Errorf("got %v, want %v", err, want)
			}
		})
	}
}

// keyring is the keyring TestAccRotationFailures's case loaded.
var keyring *crypt.Keyring
