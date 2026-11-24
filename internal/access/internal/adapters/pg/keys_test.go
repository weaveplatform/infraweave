package pg_test

import (
	"bytes"
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
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
		svcs[i] = token.New(pg.NewSigningKeys(db, keys, nil), "https://iw.example.com",
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
	store := pg.NewSigningKeys(db, keyring(t, db), nil)
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	plan := func(steps ...token.Step) func([]token.Key) []token.Step {
		return func([]token.Key) []token.Step { return steps }
	}
	if err := store.Rotate(ctx, token.APIToken, plan(
		token.Step{State: token.StateActive, At: now, Algorithm: signer.ES256},
		token.Step{State: token.StateNext, At: now.Add(time.Hour), Algorithm: signer.ES256},
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
		token.Step{State: token.StateNext, At: later, Algorithm: signer.ES256},
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
	store := pg.NewSigningKeys(db, keyring(t, db), nil)
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

func (failingSealer) OpenColumn(context.Context, crypt.Column, []string, []byte) (crypt.Value, error) {
	return crypt.Value{}, errSeal
}

func TestAccSigningKeysBroken(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	// A key that cannot be sealed is not stored.
	err := pg.NewSigningKeys(db, failingSealer{}, nil).Rotate(ctx, token.APIToken,
		func([]token.Key) []token.Step {
			return []token.Step{{State: token.StateActive, At: time.Now(), Algorithm: signer.ES256}}
		})
	if !errors.Is(err, errSeal) {
		t.Errorf("Rotate = %v, want the keyring's error", err)
	}
	// A key of no algorithm is not generated.
	err = pg.NewSigningKeys(db, keyring(t, db), nil).Rotate(ctx, token.Federation,
		func([]token.Key) []token.Step { return []token.Step{{State: token.StateActive, At: time.Now()}} })
	if !errors.Is(err, signer.ErrAlgorithm) {
		t.Errorf("Rotate(no algorithm) = %v, want ErrAlgorithm", err)
	}
	// A stored public key that does not parse fails the read.
	if _, err := db.Exec(ctx, `
INSERT INTO signing_keys (id, purpose, algorithm, public_jwk, kms_key_ref, state)
VALUES ($1, 'api-token', 'ES256', '{"kty":"none"}', 'ref', 'next')`, kernel.NewULID()); err != nil {
		t.Fatal(err)
	}
	keys := keyring(t, db)
	if _, err := pg.NewSigningKeys(db, keys, nil).Keys(ctx, token.APIToken); err == nil {
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
	if _, err := pg.NewSigningKeys(db, keys, nil).Signer(ctx, id); !errors.Is(err, signer.ErrKey) {
		t.Errorf("Signer(junk) = %v, want ErrKey", err)
	}
}

// kmsKey is a KMS key held locally: a local key under a reference, whose
// public JWK, as a KMS backend's, has no kid.
type kmsKey struct {
	*signer.Local

	ref string
}

func newKMSKey(t *testing.T, ref string) kmsKey {
	t.Helper()
	return newKMSKeyFor(t, signer.ES256, ref)
}

func newKMSKeyFor(t *testing.T, alg jose.SignatureAlgorithm, ref string) kmsKey {
	t.Helper()
	l, err := signer.GenerateLocal(alg, "")
	if err != nil {
		t.Fatal(err)
	}
	return kmsKey{Local: l, ref: ref}
}

func (k kmsKey) Ref() string { return k.ref }

// Under the kms backend a key's row holds its reference and no private
// half, tokens signed through the KMS key verify on a replica that opened
// it too, a second reference rotates through next and active, and a row
// whose reference is no longer configured does not sign.
func TestAccSigningKeysKMS(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	keys := keyring(t, db)
	a, b := newKMSKey(t, "vault:transit/a/1"), newKMSKey(t, "vault:transit/b/1")
	clock := time.Now()
	now := func() time.Time { return clock }
	replica := func(kms ...signer.KMS) *token.Service {
		refs := make([]string, 0, len(kms))
		for _, k := range kms {
			refs = append(refs, k.Ref())
		}
		return token.New(pg.NewSigningKeys(db, keys, kms), "https://iw.example.com",
			token.Policy{TokenLife: time.Hour, Refs: refs}, now)
	}
	first := replica(a)
	raw, _, err := first.Issue(ctx, request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replica(a).Verify(ctx, raw, audience); err != nil {
		t.Errorf("another replica: %v", err)
	}
	var (
		ref    string
		sealed bool
	)
	if err := db.QueryRow(
		ctx,
		`SELECT kms_key_ref, private_key_sealed IS NOT NULL FROM signing_keys`,
	).Scan(&ref, &sealed); err != nil ||
		ref != a.Ref() || sealed {
		t.Fatalf("the row: %q, sealed %v, %v", ref, sealed, err)
	}

	both := replica(a, b)
	if err := both.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(token.MinPublished)
	before, _, err := both.Issue(ctx, request())
	if err != nil {
		t.Fatal(err)
	}
	if err := both.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(token.SignerRefresh)
	if _, err := both.Verify(ctx, before, audience); err != nil {
		t.Errorf("the retiring key's token: %v", err)
	}
	listed, err := pg.NewSigningKeys(db, keys, nil).Keys(ctx, token.APIToken)
	retiring, active := token.Key{Ref: a.Ref(), State: token.StateRetiring},
		token.Key{Ref: b.Ref(), State: token.StateActive}
	if err != nil || len(listed) != 2 ||
		listed[0].Ref != retiring.Ref || listed[0].State != retiring.State ||
		listed[1].Ref != active.Ref || listed[1].State != active.State {
		t.Fatalf("after the rotation: %+v, %v", listed, err)
	}
	if _, _, err := both.Issue(ctx, request()); err != nil {
		t.Errorf("Issue through b: %v", err)
	}

	// b's reference removed from the configuration while b signs.
	if _, _, err := replica(a).Issue(ctx, request()); !errors.Is(err, pg.ErrKMSRef) {
		t.Errorf("Issue without b's KMS key = %v, want ErrKMSRef", err)
	}
	err = pg.NewSigningKeys(db, keys, nil).
		Rotate(ctx, token.APIToken, func([]token.Key) []token.Step {
			return []token.Step{{State: token.StateNext, At: clock, Ref: "vault:transit/c/1"}}
		})
	if !errors.Is(err, pg.ErrKMSRef) {
		t.Errorf("a step naming an unopened KMS key = %v, want ErrKMSRef", err)
	}
}

// federationClaims are a federation token's claims, which the store's
// caller sets.
func federationClaims() jwt.Claims {
	now := time.Now()
	return jwt.Claims{
		Issuer: "https://iw.example.com", Subject: "space:root", Audience: jwt.Audience{"iw.example.com/acme"},
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(time.Hour)),
	}
}

// Under the local backend each purpose's first key is of its algorithm,
// RS256 on RSA 3072 for federation and ES256 for api-token, with its
// private half sealed; a federation token verifies against the published
// key on another replica.
func TestAccSigningKeysFederation(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	keys := keyring(t, db)
	for _, alg := range []jose.SignatureAlgorithm{signer.RS256, signer.PS256, signer.ES256} {
		if _, err := db.Exec(ctx, `DELETE FROM signing_keys`); err != nil {
			t.Fatal(err)
		}
		fed := token.NewFederation(pg.NewSigningKeys(db, keys, nil),
			token.Policy{TokenLife: time.Hour, Algorithm: alg}, time.Now)
		raw, err := fed.Sign(ctx, federationClaims())
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := token.New(pg.NewSigningKeys(db, keys, nil), "https://iw.example.com",
			token.Policy{TokenLife: time.Hour}, time.Now).Issue(ctx, request()); err != nil {
			t.Fatal(err)
		}
		got, err := db.Query(ctx, `
SELECT purpose, algorithm, private_key_sealed IS NOT NULL AND kms_key_ref IS NULL, public_jwk->>'alg'
FROM signing_keys WHERE state = 'active' ORDER BY purpose`)
		if err != nil {
			t.Fatal(err)
		}
		var rows []string
		for got.Next() {
			var purpose, algorithm, jwkAlg string
			var sealed bool
			if err := got.Scan(&purpose, &algorithm, &sealed, &jwkAlg); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, fmt.Sprintf("%s %s %v %s", purpose, algorithm, sealed, jwkAlg))
		}
		if want := []string{"api-token ES256 true ES256", fmt.Sprintf("federation %s true %s", alg, alg)}; !slices.Equal(rows, want) {
			t.Errorf("%s: active keys %q, want %q", alg, rows, want)
		}
		other := token.NewFederation(pg.NewSigningKeys(db, keys, nil),
			token.Policy{TokenLife: time.Hour, Algorithm: alg}, time.Now)
		published, err := other.FederationKeys(ctx)
		if err != nil || len(published) != 1 {
			t.Fatalf("%s: FederationKeys = %+v, %v", alg, published, err)
		}
		if pub, ok := published[0].Key.(*rsa.PublicKey); alg != signer.ES256 && (!ok || pub.N.BitLen() != signer.RSABits) {
			t.Errorf("%s: published key is %T", alg, published[0].Key)
		}
		parsed, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{alg})
		if err != nil {
			t.Fatal(err)
		}
		var claims jwt.Claims
		if err := parsed.Claims(published[0].Key, &claims); err != nil || claims.Subject != "space:root" {
			t.Errorf("%s: the token against the published key: %+v, %v", alg, claims, err)
		}
	}
}

// Replicas that use an empty table together, each creating its first
// api-token and federation keys, agree on one active key of each purpose:
// concurrent rotations of both purposes queue on one lock.
func TestAccSigningKeysPurposesConcurrent(t *testing.T) {
	db := pool(t)
	keys := keyring(t, db)
	const replicas = 4
	var wg sync.WaitGroup
	errs := make([]error, 2*replicas)
	for i := range replicas {
		fed := token.NewFederation(pg.NewSigningKeys(db, keys, nil),
			token.Policy{TokenLife: time.Hour, Algorithm: signer.ES256}, time.Now)
		api := token.New(pg.NewSigningKeys(db, keys, nil), "https://iw.example.com",
			token.Policy{TokenLife: time.Hour}, time.Now)
		wg.Go(func() { _, errs[2*i] = fed.FederationKeys(t.Context()) })
		wg.Go(func() { _, _, errs[2*i+1] = api.Issue(t.Context(), request()) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	for _, purpose := range []token.Purpose{token.APIToken, token.Federation} {
		var n int
		if err := db.QueryRow(t.Context(),
			`SELECT count(*) FROM signing_keys WHERE purpose = $1`, purpose).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s: %d keys, %v; want one", purpose, n, err)
		}
	}
}

// Under the kms backend a federation key's row holds its RSA KMS key's
// reference and algorithm, apart from the api-token key's.
func TestAccSigningKeysFederationKMS(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	keys := keyring(t, db)
	api, fed := newKMSKey(t, "vault:transit/a/1"), newKMSKeyFor(t, signer.RS256, "vault:transit/rsa/1")
	store := pg.NewSigningKeys(db, keys, []signer.KMS{api, fed})
	f := token.NewFederation(store, token.Policy{
		TokenLife: time.Hour, Algorithm: signer.RS256, Refs: []string{fed.Ref()},
	}, time.Now)
	raw, err := f.Sign(ctx, federationClaims())
	if err != nil {
		t.Fatal(err)
	}
	var ref, alg string
	if err := db.QueryRow(ctx, `SELECT kms_key_ref, algorithm FROM signing_keys WHERE purpose = 'federation'`).
		Scan(&ref, &alg); err != nil || ref != fed.Ref() || alg != "RS256" {
		t.Fatalf("the federation row: %q, %q, %v", ref, alg, err)
	}
	parsed, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{signer.RS256})
	if err != nil {
		t.Fatal(err)
	}
	var claims jwt.Claims
	if err := parsed.Claims(fed.Public().Key, &claims); err != nil {
		t.Errorf("the token against the KMS key: %v", err)
	}

	// The same reference opened for PS256, after the algorithm changed,
	// does not sign for the RS256 key.
	pss := newKMSKeyFor(t, signer.PS256, fed.Ref())
	_, err = token.NewFederation(pg.NewSigningKeys(db, keys, []signer.KMS{pss}), token.Policy{
		TokenLife: time.Hour, Algorithm: signer.PS256, Refs: []string{fed.Ref()},
	}, time.Now).Sign(ctx, federationClaims())
	if !errors.Is(err, pg.ErrKMSAlgorithm) || !strings.Contains(err.Error(), fed.Ref()) {
		t.Errorf("Sign through a KMS key opened for another algorithm = %v, want ErrKMSAlgorithm", err)
	}
}
