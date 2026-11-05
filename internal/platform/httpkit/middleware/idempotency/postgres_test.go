package idempotency

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

func TestAccPostgresContract(t *testing.T) {
	p := pool(t)
	contract(t, NewPostgres(p, Defaults()), func(k Key, d deadline) {
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
	a, b := NewPostgres(p, Defaults()), NewPostgres(p, Defaults())
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
	s := NewPostgres(p, Defaults())
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
	s := NewPostgres(p, Defaults())
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
