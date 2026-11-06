package idempotency

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) {
	os.Exit(sqltest.Main(m, schema.Stream()))
}

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// keyring is the keyring of p's database, under a local test KEK.
func keyring(t *testing.T, p *pgxpool.Pool) *crypt.Keyring {
	t.Helper()
	k, err := kek.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypt.Load(t.Context(), p, k)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// store is a Postgres store on p, sealing under p's keyring.
func store(t *testing.T, p *pgxpool.Pool) *Postgres {
	t.Helper()
	return NewPostgres(p, Defaults(), keyring(t, p))
}

func TestAccPostgresContract(t *testing.T) {
	p := pool(t)
	contract(t, store(t, p), func(k Key, d deadline) {
		column := "locked_until"
		if d == ttlLapses {
			column = "expires_at"
		}
		if _, err := p.Exec(t.Context(),
			`UPDATE idempotency_keys SET `+column+` = now() - interval '1 second'
WHERE caller = $1 AND key = $2`, k.Caller, k.Value); err != nil {
			t.Fatal(err)
		}
	})
}

// TestAccPostgresSharedByReplicas checks that two stores on one database,
// as two replicas hold, see each other's keys.
func TestAccPostgresSharedByReplicas(t *testing.T) {
	p := pool(t)
	a, b := store(t, p), store(t, p)
	k := Key{Caller: "a", Value: "k"}
	if _, ok, err := a.Claim(t.Context(), k, "fp"); !ok || err != nil {
		t.Fatalf("claim on a: %v %v", ok, err)
	}
	if rec, ok, err := b.Claim(t.Context(), k, "fp"); ok || err != nil || rec.Response != nil {
		t.Errorf("claim on b while a holds it: %+v %v %v", rec, ok, err)
	}
	if err := a.Complete(t.Context(), k, Response{Status: 201, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if rec, ok, err := b.Claim(t.Context(), k, "fp"); ok || err != nil ||
		rec.Response == nil || string(rec.Response.Body) != "x" {
		t.Errorf("claim on b after a completed: %+v %v %v", rec, ok, err)
	}
}

func TestAccPostgresUnreachable(t *testing.T) {
	p := pool(t)
	s := store(t, p)
	p.Close()
	ctx := context.Background()
	k := Key{Value: "k"}
	if _, _, err := s.Claim(ctx, k, "fp"); err == nil {
		t.Error("Claim on a closed pool")
	}
	if err := s.Complete(ctx, k, Response{Status: 201}); err == nil {
		t.Error("Complete on a closed pool")
	}
	if err := s.Release(ctx, k); err == nil {
		t.Error("Release on a closed pool")
	}
	if _, err := s.Purge(ctx); err == nil {
		t.Error("Purge on a closed pool")
	}
}

// TestAccIdempotencyPurge checks that Purge removes expired keys, done or
// in flight, and nothing else.
func TestAccIdempotencyPurge(t *testing.T) {
	p := pool(t)
	s := store(t, p)
	keys := map[string]bool{
		"live-done":   false,
		"live-flight": false,
		"old-done":    true,
		"old-flight":  true,
	}
	for v, expired := range keys {
		k := Key{Caller: "c", Value: v}
		if _, ok, err := s.Claim(t.Context(), k, "fp"); !ok || err != nil {
			t.Fatalf("claim %s: %v %v", v, ok, err)
		}
		if v == "live-done" || v == "old-done" {
			if err := s.Complete(t.Context(), k, Response{Status: 201}); err != nil {
				t.Fatal(err)
			}
		}
		if expired {
			if _, err := p.Exec(
				t.Context(),
				`UPDATE idempotency_keys SET expires_at = now() - interval '1 second' WHERE key = $1`,
				v,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	n, err := s.Purge(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("Purge = %d, %v; want the 2 expired keys", n, err)
	}
	rows, err := p.Query(t.Context(), `SELECT key FROM idempotency_keys ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	left, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || !slices.Equal(left, []string{"live-done", "live-flight"}) {
		t.Errorf("left %q, %v; want the live keys", left, err)
	}
}

// TestAccPostgresBodySealed checks that a stored body is sealed under the
// key's caller and value: the row holds no plaintext, a body moved to
// another key does not open, and an empty body stores nothing.
func TestAccPostgresBodySealed(t *testing.T) {
	p := pool(t)
	s := store(t, p)
	a, b := Key{Caller: "c", Value: "a"}, Key{Caller: "c", Value: "b"}
	for k, body := range map[Key]string{a: `{"secret":"s3cr3t"}`, b: ""} {
		if _, ok, err := s.Claim(t.Context(), k, "fp"); !ok || err != nil {
			t.Fatalf("claim %v: %v %v", k, ok, err)
		}
		if err := s.Complete(
			t.Context(),
			k,
			Response{Status: 201, Body: []byte(body)},
		); err != nil {
			t.Fatal(err)
		}
	}
	var sealed []byte
	if err := p.QueryRow(t.Context(),
		`SELECT body_sealed FROM idempotency_keys WHERE key = 'a'`).Scan(&sealed); err != nil ||
		bytes.Contains(sealed, []byte("s3cr3t")) {
		t.Errorf("body_sealed %q, %v", sealed, err)
	}
	if rec, ok, err := s.Claim(t.Context(), b, "fp"); ok || err != nil ||
		rec.Response == nil || len(rec.Response.Body) != 0 {
		t.Errorf("empty body: %+v %v %v", rec, ok, err)
	}
	if _, err := p.Exec(t.Context(), `UPDATE idempotency_keys SET body_sealed = $1 WHERE key = 'b'`,
		sealed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Claim(t.Context(), b, "fp"); err == nil ||
		!strings.Contains(err.Error(), "open idempotency key's body") {
		t.Errorf("a body moved to another key: %v", err)
	}
}

// failingSealer seals nothing; Complete never opens.
type failingSealer struct{ Sealer }

var errSeal = errors.New("no keys")

func (failingSealer) SealColumn(context.Context, crypt.Column, []string, []byte) ([]byte, error) {
	return nil, errSeal
}

// TestAccPostgresSealFails checks that a body that cannot be sealed is not
// stored, and the key stays in flight for the caller to release.
func TestAccPostgresSealFails(t *testing.T) {
	p := pool(t)
	s := NewPostgres(p, Defaults(), failingSealer{})
	k := Key{Caller: "c", Value: "k"}
	if _, ok, err := s.Claim(t.Context(), k, "fp"); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := s.Complete(
		t.Context(),
		k,
		Response{Status: 201, Body: []byte("x")},
	); !errors.Is(
		err,
		errSeal,
	) {
		t.Errorf("Complete: %v", err)
	}
	var state string
	if err := p.QueryRow(t.Context(), `SELECT state FROM idempotency_keys WHERE key = 'k'`).
		Scan(&state); err != nil || state != "in_flight" {
		t.Errorf("state %q, %v", state, err)
	}
}
