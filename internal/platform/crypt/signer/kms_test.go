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

	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

func TestUnitFromDER(t *testing.T) {
	der, err := asn1.Marshal(struct{ R, S *big.Int }{big.NewInt(1), big.NewInt(2)})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signer.FromDER(der)
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
		if _, err := signer.FromDER(der); !errors.Is(err, signer.ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, der := range map[string][]byte{
		"not DER":  []byte("signature"),
		"trailing": append(der, 0),
	} {
		if _, err := signer.FromDER(der); !errors.Is(err, signer.ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUnitRaw(t *testing.T) {
	if sig, err := signer.Raw(make([]byte, 64)); err != nil || len(sig) != 64 {
		t.Errorf("Raw(64) = %d, %v", len(sig), err)
	}
	if _, err := signer.Raw(make([]byte, 63)); !errors.Is(err, signer.ErrSignature) {
		t.Errorf("Raw(63) = %v", err)
	}
}

func TestUnitParsePKIX(t *testing.T) {
	p256, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ed, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&p256.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := signer.ParsePKIX("ref", der); err != nil || !k.Equal(&p256.PublicKey) {
		t.Errorf("P-256: %v", err)
	}
	for name, pub := range map[string]any{"P-384": &p384.PublicKey, "Ed25519": ed} {
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := signer.ParsePKIX("awskms:k", der); !errors.Is(err, signer.ErrKey) ||
			!strings.Contains(err.Error(), "awskms:k") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := signer.ParsePKIX("ref", []byte("garbage")); !errors.Is(err, signer.ErrKey) {
		t.Errorf("garbage: %v", err)
	}
}

func TestUnitWithKeyID(t *testing.T) {
	l, err := signer.GenerateLocal("old")
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
