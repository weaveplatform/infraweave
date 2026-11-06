package loopback

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
)

// rsaKey is one key for every test: generating 2048-bit keys is slow.
var rsaKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

func pkix(t *testing.T, pub any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// open is spacectl's half: unwrap the key, decrypt, strip the padding.
func open(t *testing.T, priv *rsa.PrivateKey, sealed string) string {
	t.Helper()
	k, tok, ok := strings.Cut(sealed, ".")
	if !ok {
		t.Fatalf("%q has no dot", sealed)
	}
	wrapped, err := base64.RawURLEncoding.DecodeString(k)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.DecryptOAEP(sha512.New(), nil, priv, wrapped, nil)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil || len(ct)%aes.BlockSize != 0 || len(ct) < 2*aes.BlockSize {
		t.Fatalf("cipher %v, %d bytes", err, len(ct))
	}
	plain := make([]byte, len(ct)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, ct[:aes.BlockSize]).CryptBlocks(plain, ct[aes.BlockSize:])
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > aes.BlockSize ||
		!bytes.Equal(plain[len(plain)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		t.Fatalf("bad padding %d", pad)
	}
	return string(plain[:len(plain)-pad])
}

func TestUnitSeal(t *testing.T) {
	priv := rsaKey()
	p := pkix(t, &priv.PublicKey)
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&priv.PublicKey),
	}))
	for name, key := range map[string]string{
		"PEM":                 p,
		"PKCS #1":             pkcs1,
		"base64url PEM":       base64.RawURLEncoding.EncodeToString([]byte(p)),
		"padded base64url":    base64.URLEncoding.EncodeToString([]byte(p)),
		"PEM with whitespace": "\n" + p + "  ",
	} {
		for _, tok := range []string{"", "a", strings.Repeat("x", aes.BlockSize), "header.claims.signature"} {
			sealed, err := Seal(key, tok)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if strings.Count(sealed, ".") != 1 || strings.ContainsAny(sealed, "+/=") {
				t.Errorf("%s: %q is not two unpadded base64url halves", name, sealed)
			}
			if got := open(t, priv, sealed); got != tok {
				t.Errorf("%s: opened %q, want %q", name, got, tok)
			}
		}
	}
}

func TestUnitSealRefuses(t *testing.T) {
	// FIPS 140-3 only mode refuses to generate a key this small, so its
	// modulus is made by hand: Seal reads only its length.
	small := &rsa.PublicKey{
		N: new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 1023), big.NewInt(1)),
		E: 65537,
	}
	priv := rsaKey()
	ecdsa := "-----BEGIN PUBLIC KEY-----\n" +
		"MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEj2yA2nVfZtq5r7Wb6m5r0Y2d1E0r\n" +
		"vY3w0l3k2p8n1v5d7H3g6x8y9a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s==\n" +
		"-----END PUBLIC KEY-----\n"
	for name, key := range map[string]string{
		"not base64":    "%%%",
		"no PEM":        base64.RawURLEncoding.EncodeToString([]byte("hello")),
		"not a key":     string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("x")})),
		"1024 bits":     pkix(t, small),
		"private key":   string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})),
		"garbled ECDSA": ecdsa,
	} {
		if _, err := Seal(key, "t"); !errors.Is(err, ErrKey) {
			t.Errorf("%s: %v, want ErrKey", name, err)
		}
	}
}
