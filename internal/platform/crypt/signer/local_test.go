package signer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"testing"
)

// A private scalar of zero stands in for a key the platform cannot use:
// neither its PKCS #8 form nor a signature exists.
func TestUnitLocalBrokenKey(t *testing.T) {
	good, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:staticcheck // an invalid key on purpose, which only the raw scalar makes
	l := newLocal("broken", &ecdsa.PrivateKey{PublicKey: good.PublicKey, D: big.NewInt(0)})
	if _, err := l.PrivateKey(); err == nil {
		t.Error("PrivateKey of a zero scalar succeeded")
	}
	if _, err := l.Sign(context.Background(), make([]byte, DigestSize)); err == nil {
		t.Error("Sign with a zero scalar succeeded")
	}
}
