package signer_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"math/big"
	"strings"
	"testing"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/signertest"
)

func es256(t *testing.T) jose.JSONWebKey {
	t.Helper()
	return signer.JWK(signertest.NewFake(t).Key.Public(), "", signer.ES256)
}

func TestUnitFromDER(t *testing.T) {
	jwk := es256(t)
	der, err := asn1.Marshal(struct{ R, S *big.Int }{big.NewInt(1), big.NewInt(2)})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signer.FromDER(jwk, der)
	if err != nil || len(sig) != 64 || sig[31] != 1 || sig[63] != 2 {
		t.Fatalf("FromDER = %x, %v", sig, err)
	}
	wide := new(big.Int).Lsh(big.NewInt(1), 256)
	for name, v := range map[string]struct{ R, S *big.Int }{
		"zero r":     {big.NewInt(0), big.NewInt(2)},
		"negative s": {big.NewInt(1), big.NewInt(-2)},
		"wide r":     {wide, big.NewInt(2)},
	} {
		der, err := asn1.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := signer.FromDER(jwk, der); !errors.Is(err, signer.ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, der := range map[string][]byte{
		"not DER":  []byte("signature"),
		"trailing": append(der, 0),
	} {
		if _, err := signer.FromDER(jwk, der); !errors.Is(err, signer.ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// An RSA signature is answered as JWS carries it, as long as the
	// modulus.
	for _, alg := range []jose.SignatureAlgorithm{signer.RS256, signer.PS256} {
		rsaJWK := signer.JWK(signertest.NewRSAFake(t, false).Key.Public(), "", alg)
		if sig, err := signer.FromDER(rsaJWK, make([]byte, signer.RSABits/8)); err != nil ||
			len(sig) != signer.RSABits/8 {
			t.Errorf("%s: FromDER = %d bytes, %v", alg, len(sig), err)
		}
		if _, err := signer.FromDER(rsaJWK, der); !errors.Is(err, signer.ErrSignature) {
			t.Errorf("%s: an ECDSA signature: %v", alg, err)
		}
	}
}

func TestUnitRaw(t *testing.T) {
	jwk := es256(t)
	if sig, err := signer.Raw(jwk, make([]byte, 64)); err != nil || len(sig) != 64 {
		t.Errorf("Raw(64) = %d, %v", len(sig), err)
	}
	if _, err := signer.Raw(jwk, make([]byte, 63)); !errors.Is(err, signer.ErrSignature) {
		t.Errorf("Raw(63) = %v", err)
	}
	rsaJWK := signer.JWK(signertest.NewRSAFake(t, false).Key.Public(), "", signer.RS256)
	if sig, err := signer.Raw(rsaJWK, make([]byte, 384)); err != nil || len(sig) != 384 {
		t.Errorf("RS256 Raw(384) = %d, %v", len(sig), err)
	}
	if _, err := signer.Raw(rsaJWK, make([]byte, 64)); !errors.Is(err, signer.ErrSignature) {
		t.Errorf("RS256 Raw(64) = %v", err)
	}
}

// TestUnitParsePKIX checks that a key is read under each algorithm it
// signs, and refused under any other, naming its reference: an RSA key
// below 3072 bits, a curve other than P-256 and a key of another type.
func TestUnitParsePKIX(t *testing.T) {
	p256 := signertest.NewFake(t)
	rsa3072 := signertest.NewRSAFake(t, false)
	rsa2048 := signertest.NewRSA2048Fake(t)
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ed, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := signer.ParsePKIX("ref", signer.ES256, p256.PKIX(t)); err != nil ||
		!p256.ECDSA().Equal(k) {
		t.Errorf("P-256: %v", err)
	}
	for _, alg := range []jose.SignatureAlgorithm{signer.RS256, signer.PS256} {
		if k, err := signer.ParsePKIX("ref", alg, rsa3072.PKIX(t)); err != nil ||
			!rsa3072.RSA().Equal(k) {
			t.Errorf("RSA 3072 for %s: %v", alg, err)
		}
	}
	for name, tc := range map[string]struct {
		pub  any
		alg  jose.SignatureAlgorithm
		want string
	}{
		"P-384":           {&p384.PublicKey, signer.ES256, "ECDSA P-384"},
		"Ed25519":         {ed, signer.ES256, "ed25519.PublicKey"},
		"RSA for ES256":   {rsa3072.RSA(), signer.ES256, "RSA 3072"},
		"P-256 for RS256": {p256.ECDSA(), signer.RS256, "ECDSA P-256"},
		"RSA 2048":        {rsa2048.RSA(), signer.RS256, "RSA 2048"},
		"RSA 2048 PSS":    {rsa2048.RSA(), signer.PS256, "at least 3072 bits"},
	} {
		der, err := x509.MarshalPKIXPublicKey(tc.pub)
		if err != nil {
			t.Fatal(err)
		}
		_, err = signer.ParsePKIX("awskms:k", tc.alg, der)
		if !errors.Is(err, signer.ErrKey) || !strings.Contains(err.Error(), "awskms:k") ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := signer.ParsePKIX("ref", signer.ES256, []byte("garbage")); !errors.Is(err, signer.ErrKey) {
		t.Errorf("garbage: %v", err)
	}
	if _, err := signer.ParsePKIX("ref", jose.EdDSA, p256.PKIX(t)); !errors.Is(err, signer.ErrAlgorithm) {
		t.Errorf("EdDSA: %v", err)
	}
}

func TestUnitCheckDigest(t *testing.T) {
	for _, alg := range signer.Algorithms {
		if err := signer.CheckDigest(string(alg), make([]byte, signer.DigestSize)); err != nil {
			t.Errorf("%s: %v", alg, err)
		}
		if err := signer.CheckDigest(string(alg), make([]byte, 48)); !errors.Is(err, signer.ErrDigest) {
			t.Errorf("%s with SHA-384's length: %v", alg, err)
		}
	}
	if err := signer.CheckDigest("RS1", make([]byte, signer.DigestSize)); !errors.Is(err, signer.ErrAlgorithm) {
		t.Errorf("RS1: %v", err)
	}
}

func TestUnitWithKeyID(t *testing.T) {
	l, err := signer.GenerateLocal(signer.ES256, "old")
	if err != nil {
		t.Fatal(err)
	}
	s := signer.WithKeyID(l, "new")
	if got := s.Public(); got.KeyID != "new" || got.Key != l.Public().Key {
		t.Errorf("Public = %+v", got)
	}
	if l.Public().KeyID != "old" {
		t.Error("WithKeyID renamed the signer it wraps")
	}
	if _, err := s.Sign(t.Context(), make([]byte, signer.DigestSize)); err != nil {
		t.Error(err)
	}
}
