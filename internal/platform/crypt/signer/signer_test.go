package signer_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"math/big"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

func TestUnitLocal(t *testing.T) {
	l, err := signer.GenerateLocal("kid-1")
	if err != nil {
		t.Fatal(err)
	}
	jwk := l.Public()
	if jwk.KeyID != "kid-1" || jwk.Algorithm != "ES256" || jwk.Use != "sig" || !jwk.IsPublic() {
		t.Errorf("Public = %+v", jwk)
	}

	digest := sha256.Sum256([]byte("header.payload"))
	sig, err := l.Sign(context.Background(), digest[:])
	if err != nil || len(sig) != 64 {
		t.Fatalf("Sign = %d bytes, %v", len(sig), err)
	}
	pub, ok := jwk.Key.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("public key is %T", jwk.Key)
	}
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Error("the signature does not verify under the public key")
	}
	if _, err := l.Sign(context.Background(), []byte("short")); !errors.Is(err, signer.ErrDigest) {
		t.Errorf("Sign(short) = %v, want ErrDigest", err)
	}

	// The private half survives PKCS #8 and signs under the same kid.
	der, err := l.PrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	back, err := signer.ParseLocal("kid-1", der)
	if err != nil {
		t.Fatal(err)
	}
	sig, err = back.Sign(context.Background(), digest[:])
	if err != nil {
		t.Fatal(err)
	}
	r, s = new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) || back.Public().KeyID != "kid-1" {
		t.Error("the parsed key is not the generated one")
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
	for name, key := range map[string]any{"P-384": p384, "Ed25519": ed} {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := signer.ParseLocal("k", der); !errors.Is(err, signer.ErrKey) {
			t.Errorf("%s: ParseLocal = %v, want ErrKey", name, err)
		}
	}
	if _, err := signer.ParseLocal("k", []byte("not a key")); !errors.Is(err, signer.ErrKey) {
		t.Errorf("garbage: ParseLocal = %v, want ErrKey", err)
	}
}
