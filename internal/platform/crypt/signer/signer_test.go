package signer_test

import (
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"testing"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// TestUnitLocal generates a key for each algorithm, checks that what it
// signs verifies with go-jose under its public JWK, and that its private
// half survives PKCS #8 under the same kid and algorithm.
func TestUnitLocal(t *testing.T) {
	for _, alg := range signer.Algorithms {
		t.Run(string(alg), func(t *testing.T) {
			l, err := signer.GenerateLocal(alg, "kid-1")
			if err != nil {
				t.Fatal(err)
			}
			jwk := l.Public()
			if jwk.KeyID != "kid-1" || jwk.Algorithm != string(alg) || jwk.Use != "sig" ||
				!jwk.IsPublic() {
				t.Errorf("Public = %+v", jwk)
			}
			if err := signer.CheckPublic("kid-1", alg, jwk.Key); err != nil {
				t.Error(err)
			}
			input := sign(t, l, alg)
			der, err := l.PrivateKey()
			if err != nil {
				t.Fatal(err)
			}
			back, err := signer.ParseLocal(alg, "kid-1", der)
			if err != nil {
				t.Fatal(err)
			}
			if back.Public().KeyID != "kid-1" || back.Public().Algorithm != string(alg) {
				t.Errorf("the parsed key is %+v", back.Public())
			}
			if !verifies(t, jwk, sign(t, back, alg)) || !verifies(t, jwk, input) {
				t.Error("the parsed key is not the generated one")
			}
			if _, err := l.Sign(context.Background(), []byte("short")); !errors.Is(err, signer.ErrDigest) {
				t.Errorf("Sign(short) = %v, want ErrDigest", err)
			}
		})
	}
	if _, err := signer.GenerateLocal(jose.HS256, "k"); !errors.Is(err, signer.ErrAlgorithm) {
		t.Errorf("GenerateLocal(HS256) = %v, want ErrAlgorithm", err)
	}
}

// sign signs a JWS through s with go-jose, as the access domain's token
// package does, and answers its compact form.
func sign(t *testing.T, s signer.Signer, alg jose.SignatureAlgorithm) string {
	t.Helper()
	js, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: opaque{t: t, s: s}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := js.Sign([]byte(`{"sub":"signer"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// verifies checks raw with go-jose under jwk, accepting jwk's algorithm
// alone.
func verifies(t *testing.T, jwk jose.JSONWebKey, raw string) bool {
	t.Helper()
	jws, err := jose.ParseSignedCompact(raw, []jose.SignatureAlgorithm{jose.SignatureAlgorithm(jwk.Algorithm)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = jws.Verify(jwk.Key)
	return err == nil
}

type opaque struct {
	t *testing.T
	s signer.Signer
}

func (o opaque) Public() *jose.JSONWebKey {
	k := o.s.Public()
	return &k
}

func (o opaque) Algs() []jose.SignatureAlgorithm {
	return []jose.SignatureAlgorithm{jose.SignatureAlgorithm(o.s.Public().Algorithm)}
}

func (o opaque) SignPayload(payload []byte, _ jose.SignatureAlgorithm) ([]byte, error) {
	d := sha256.Sum256(payload)
	return o.s.Sign(o.t.Context(), d[:]) //nolint:wrapcheck // go-jose wraps it
}

// TestUnitLocalAlgorithms checks that a PS256 key's signature does not
// verify as RS256, and an RS256 key's not as PS256: the padding is the
// algorithm's.
func TestUnitLocalAlgorithms(t *testing.T) {
	rs, err := signer.GenerateLocal(signer.RS256, "rs")
	if err != nil {
		t.Fatal(err)
	}
	der, err := rs.PrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	ps, err := signer.ParseLocal(signer.PS256, "ps", der)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("header.payload"))
	pub, _ := rs.Public().Key.(*rsa.PublicKey)
	pkcs1, err := rs.Sign(t.Context(), digest[:])
	if err != nil {
		t.Fatal(err)
	}
	pss, err := ps.Sign(t.Context(), digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], pkcs1) != nil {
		t.Error("RS256 does not verify as PKCS #1 v1.5")
	}
	if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], pss) == nil {
		t.Error("PS256 verifies as PKCS #1 v1.5")
	}
	if rsa.VerifyPSS(pub, crypto.SHA256, digest[:], pss, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) != nil {
		t.Error("PS256 does not verify as PSS with a salt as long as the digest")
	}
	if len(pkcs1) != signer.RSABits/8 || len(pss) != signer.RSABits/8 {
		t.Errorf("signatures of %d and %d bytes", len(pkcs1), len(pss))
	}
}

func TestUnitParseLocalRefuses(t *testing.T) {
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	es, err := signer.GenerateLocal(signer.ES256, "es")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]any{"P-384": p384, "Ed25519": ed}
	// X25519, a key that signs nothing, is not allowed in FIPS 140-only
	// mode, where it cannot be made.
	if x, err := ecdh.X25519().GenerateKey(rand.Reader); err == nil {
		keys["X25519"] = x
	}
	for name, key := range keys {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		for _, alg := range signer.Algorithms {
			if _, err := signer.ParseLocal(alg, "k", der); !errors.Is(err, signer.ErrKey) {
				t.Errorf("%s as %s: ParseLocal = %v, want ErrKey", name, alg, err)
			}
		}
	}
	der, err := es.PrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, alg := range []jose.SignatureAlgorithm{signer.RS256, signer.PS256} {
		if _, err := signer.ParseLocal(alg, "k", der); !errors.Is(err, signer.ErrKey) {
			t.Errorf("P-256 as %s: ParseLocal = %v, want ErrKey", alg, err)
		}
	}
	if _, err := signer.ParseLocal(jose.HS256, "k", der); !errors.Is(err, signer.ErrAlgorithm) {
		t.Errorf("as HS256: ParseLocal = %v, want ErrAlgorithm", err)
	}
	if _, err := signer.ParseLocal(signer.ES256, "k", []byte("not a key")); !errors.Is(err, signer.ErrKey) {
		t.Errorf("garbage: ParseLocal = %v, want ErrKey", err)
	}
}
