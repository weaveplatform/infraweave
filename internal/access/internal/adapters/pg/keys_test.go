package pg_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

const audience = "https://iw.example.com/api/v1"

func keyring(t *testing.T, db *pgxpool.Pool) *crypt.Keyring {
	t.Helper()
	p, err := kek.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	k, err := crypt.Load(t.Context(), db, p)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func request() token.Request {
	return token.Request{
		Kind:     token.KindAPIKey,
		Subject:  "api::k",
		Session:  kernel.NewULID(),
		Audience: audience,
		TTL:      time.Hour,
	}
}

// Replicas that issue their first tokens together on an empty table agree
// on one active key, and each verifies what the others signed.
func TestAccSigningKeysReplicas(t *testing.T) {
	db := pool(t)
	keys := keyring(t, db)
	const replicas = 4
	var (
		wg   sync.WaitGroup
		svcs = make([]*token.Service, replicas)
		raws = make([]string, replicas)
		errs = make([]error, replicas)
	)
	for i := range replicas {
		svcs[i] = token.New(pg.NewSigningKeys(db, keys), "https://iw.example.com",
			token.Policy{TokenLife: time.Hour}, time.Now)
		wg.Go(func() { raws[i], _, errs[i] = svcs[i].Issue(t.Context(), request()) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM signing_keys`).
		Scan(&n); err != nil ||
		n != 1 {
		t.Fatalf("%d keys, %v; want one", n, err)
	}
	for i, raw := range raws {
		if _, err := svcs[(i+1)%replicas].Verify(t.Context(), raw, audience); err != nil {
			t.Errorf("replica %d's token: %v", i, err)
		}
	}
}

// The store applies a rotation's steps with the times they carry, and a
// private half opens in no other row.
func TestAccSigningKeysRotate(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	store := pg.NewSigningKeys(db, keyring(t, db))
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	plan := func(steps ...token.Step) func([]token.Key) []token.Step {
		return func([]token.Key) []token.Step { return steps }
	}
	if err := store.Rotate(ctx, token.APIToken, plan(
		token.Step{State: token.StateActive, At: now},
		token.Step{State: token.StateNext, At: now.Add(time.Hour)},
	)); err != nil {
		t.Fatal(err)
	}
	keys, err := store.Keys(ctx, token.APIToken)
	if err != nil || len(keys) != 2 {
		t.Fatalf("Keys = %+v, %v", keys, err)
	}
	a, b := keys[0], keys[1]
	if a.State != token.StateActive || !a.ActivatedAt.Equal(now) || !a.CreatedAt.Equal(now) ||
		b.State != token.StateNext || !b.ActivatedAt.IsZero() || b.Public.KeyID != b.ID.String() ||
		!b.Public.IsPublic() || b.Public.Algorithm != "ES256" {
		t.Errorf("keys = %+v", keys)
	}

	later := now.Add(48 * time.Hour)
	if err := store.Rotate(ctx, token.APIToken, plan(
		token.Step{
			ID:          a.ID,
			State:       token.StateRetiring,
			At:          later,
			RetireAfter: later.Add(time.Hour),
		},
		token.Step{ID: b.ID, State: token.StateActive, At: later},
	)); err != nil {
		t.Fatal(err)
	}
	keys, _ = store.Keys(ctx, token.APIToken)
	if keys[0].State != token.StateRetiring || !keys[0].RetireAfter.Equal(later.Add(time.Hour)) ||
		keys[1].State != token.StateActive || !keys[1].ActivatedAt.Equal(later) {
		t.Errorf("after the promotion: %+v", keys)
	}
	if err := store.Rotate(ctx, token.APIToken, plan(
		token.Step{ID: a.ID, State: token.StateRetired, At: later.Add(25 * time.Hour)},
	)); err != nil {
		t.Fatal(err)
	}
	if keys, _ = store.Keys(ctx, token.APIToken); len(keys) != 1 || keys[0].ID != b.ID {
		t.Errorf("a retired key is still listed: %+v", keys)
	}

	// A step no rotation makes rolls the whole rotation back.
	err = store.Rotate(ctx, token.APIToken, plan(
		token.Step{State: token.StateNext, At: later},
		token.Step{ID: b.ID, State: token.StateNext, At: later},
	))
	if !errors.Is(err, pg.ErrStep) {
		t.Errorf("Rotate(back to next) = %v, want ErrStep", err)
	}
	if keys, _ = store.Keys(ctx, token.APIToken); len(keys) != 1 {
		t.Errorf("a refused rotation left %d keys", len(keys))
	}

	// The private half opens under its own row and no other.
	if _, err := store.Signer(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE signing_keys SET private_key_sealed =
		(SELECT private_key_sealed FROM signing_keys WHERE id = $1) WHERE id = $2`, b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Signer(ctx, a.ID); err == nil {
		t.Error("a private half copied into another row opened")
	}
	if _, err := store.Signer(ctx, kernel.NewULID()); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("Signer(unknown) = %v, want ErrNotFound", err)
	}
}

func TestAccSigningKeysUnavailable(t *testing.T) {
	db := pool(t)
	store := pg.NewSigningKeys(db, keyring(t, db))
	db.Close()
	if _, err := store.Keys(t.Context(), token.APIToken); err == nil {
		t.Error("Keys on a closed pool succeeded")
	}
	if err := store.Rotate(t.Context(), token.APIToken,
		func([]token.Key) []token.Step { return nil }); err == nil {
		t.Error("Rotate on a closed pool succeeded")
	}
}

// failingSealer seals nothing.
type failingSealer struct{}

var errSeal = errors.New("keyring down")

func (failingSealer) SealColumn(context.Context, crypt.Column, []string, []byte) ([]byte, error) {
	return nil, errSeal
}

func (failingSealer) OpenColumn(
	context.Context,
	crypt.Column,
	[]string,
	[]byte,
) (crypt.Value, error) {
	return crypt.Value{}, errSeal
}

func TestAccSigningKeysBroken(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	// A key that cannot be sealed is not stored.
	err := pg.NewSigningKeys(db, failingSealer{}).Rotate(ctx, token.APIToken,
		func([]token.Key) []token.Step { return []token.Step{{State: token.StateActive, At: time.Now()}} })
	if !errors.Is(err, errSeal) {
		t.Errorf("Rotate = %v, want the keyring's error", err)
	}
	// A stored public key that does not parse fails the read.
	if _, err := db.Exec(ctx, `
INSERT INTO signing_keys (id, purpose, algorithm, public_jwk, kms_key_ref, state)
VALUES ($1, 'api-token', 'ES256', '{"kty":"none"}', 'ref', 'next')`, kernel.NewULID()); err != nil {
		t.Fatal(err)
	}
	keys := keyring(t, db)
	if _, err := pg.NewSigningKeys(db, keys).Keys(ctx, token.APIToken); err == nil {
		t.Error("Keys read a public key that does not parse")
	}
	// A private half that opens but is no key fails to load.
	id := kernel.NewULID()
	col := pg.SealedColumns()[0]
	junk, err := keys.SealColumn(ctx, col, []string{id.UUID()}, []byte("not a key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `
INSERT INTO signing_keys (id, purpose, algorithm, public_jwk, private_key_sealed, state)
VALUES ($1, 'federation', 'ES256', '{}', $2, 'next')`, id, junk); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.NewSigningKeys(db, keys).Signer(ctx, id); !errors.Is(err, signer.ErrKey) {
		t.Errorf("Signer(junk) = %v, want ErrKey", err)
	}
}
