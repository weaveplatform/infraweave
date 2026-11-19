package vcslab

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func appKeyFixture(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func TestUnitAppHookKeyCanonicalIdentityAndRotation(t *testing.T) {
	one, eight := appKeyFixture(t)
	a, err := DeriveAppKey("42", one)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range [][]byte{eight, append([]byte("\n"), eight...)} {
		b, err := DeriveAppKey("00042", encoded)
		if err != nil || a != b {
			t.Fatal("equivalent private-key encoding changed signing identity")
		}
	}
	b, err := DeriveAppKey("43", one)
	if err != nil || b.secret == a.secret || b.Readiness == a.Readiness || b.Fingerprint != a.Fingerprint {
		t.Fatal("App identity did not separate secret and readiness")
	}
	rotated, _ := appKeyFixture(t)
	c, err := DeriveAppKey("42", rotated)
	if err != nil || c.secret == a.secret || c.Readiness == a.Readiness || c.Fingerprint == a.Fingerprint {
		t.Fatal("key rotation reused signing identity")
	}
	if len(a.Secret()) != 64 || !strings.HasPrefix(a.Readiness, "v1:") {
		t.Fatal("invalid signing-key format")
	}
	for _, rendered := range []string{fmt.Sprint(a), fmt.Sprintf("%+v", a), fmt.Sprintf("%#v", a), fmt.Sprintf("%#v", struct{ Key AppKey }{a})} {
		if strings.Contains(rendered, a.secret) || strings.Contains(rendered, string(one)) {
			t.Fatal("formatted App key reveals secret")
		}
	}
}

func TestUnitAppHookKeyRefusesInvalidInputPrivately(t *testing.T) {
	one, _ := appKeyFixture(t)
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id  string
		key []byte
	}{
		{"0", one},
		{"-1", one},
		{"bad", one},
		{"42", []byte("private-input-must-not-escape")},
		{"42", append(append([]byte{}, one...), one...)},
		{"42", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})},
		{"42", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})},
	} {
		_, err := DeriveAppKey(tc.id, tc.key)
		if !errors.Is(err, ErrAppKey) || strings.Contains(err.Error(), "private-input") {
			t.Fatal("invalid input did not fail privately")
		}
	}
}

// This permanently public synthetic key is never a provider credential. Its
// independently computed HKDF vector pins the operator setup across releases.
func TestUnitAppHookSetupVersionOneVector(t *testing.T) {
	encoded, err := os.ReadFile("testdata/synthetic-app-key.pem")
	if err != nil {
		t.Fatal(err)
	}
	key, err := DeriveAppKey("42", encoded)
	if err != nil {
		t.Fatal(err)
	}
	if key.Secret() != "a1601615eaa2b0c4255b919d6f822da30373f32293061e9ea57aaa746af05982" ||
		key.Readiness != "v1:24a448a31001964a1644702682428bac6979c4c3ade47cd02477f6fa90785948" ||
		key.Fingerprint != "d470326882cd40beff7398182698ebdecdceec4c684e9ffb3946067e984e4512" {
		t.Fatal("version-one operator setup vector changed")
	}
}
