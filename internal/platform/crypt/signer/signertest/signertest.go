package signertest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// Payload is the JWS payload Contract signs, and KeyID the kid it names,
// so the digest a KMS is asked to sign is the same on every run and a
// recorded fixture can answer it.
const (
	Payload = `{"sub":"signertest"}`
	KeyID   = "signertest"
)

// opaque signs through s, as the access domain's token package does.
type opaque struct {
	t   *testing.T
	s   signer.Signer
	alg jose.SignatureAlgorithm
}

func (o opaque) Public() *jose.JSONWebKey {
	k := o.s.Public()
	return &k
}

func (o opaque) Algs() []jose.SignatureAlgorithm { return []jose.SignatureAlgorithm{o.alg} }

func (o opaque) SignPayload(payload []byte, _ jose.SignatureAlgorithm) ([]byte, error) {
	d := sha256.Sum256(payload)
	return o.s.Sign(o.t.Context(), d[:]) //nolint:wrapcheck // go-jose wraps it
}

// Contract checks that s's public key is a signing key for alg that alg
// can sign with, that a JWS it signs verifies with go-jose under that key
// and fails once its payload changes, that a digest of the wrong length
// is signer.ErrDigest, and that its Ref starts with prefix.
func Contract(t *testing.T, s signer.KMS, alg jose.SignatureAlgorithm, prefix string) {
	t.Helper()
	if ref := s.Ref(); !strings.HasPrefix(ref, prefix) {
		t.Errorf("Ref = %q, want %s…", ref, prefix)
	}
	jwk := s.Public()
	if jwk.Algorithm != string(alg) || jwk.Use != "sig" || !jwk.IsPublic() || jwk.KeyID != "" {
		t.Errorf("Public = %+v", jwk)
	}
	if err := signer.CheckPublic(s.Ref(), alg, jwk.Key); err != nil {
		t.Fatal(err)
	}
	js, err := jose.NewSigner(jose.SigningKey{
		Algorithm: alg,
		Key:       opaque{t: t, s: signer.WithKeyID(s, KeyID), alg: alg},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	jws, err := js.Sign([]byte(Payload))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jose.ParseSignedCompact(raw, []jose.SignatureAlgorithm{alg})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := parsed.Verify(jwk.Key); err != nil || string(got) != Payload {
		t.Errorf("Verify = %q, %v", got, err)
	}
	parts := strings.Split(raw, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"tampered"}`))
	tampered, err := jose.ParseSignedCompact(
		strings.Join(parts, "."),
		[]jose.SignatureAlgorithm{alg},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tampered.Verify(jwk.Key); err == nil {
		t.Error("a changed payload verified")
	}
	if _, err := s.Sign(t.Context(), []byte("short")); !errors.Is(err, signer.ErrDigest) {
		t.Errorf("Sign(short) = %v, want ErrDigest", err)
	}
}

// Fake is a KMS key held locally, for a test server that models a KMS.
type Fake struct{ Key crypto.Signer }

// NewFake generates a P-256 key.
func NewFake(t *testing.T) Fake {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Fake{Key: k}
}

// rsaKeys are the RSA keys NewRSAFake answers, generated once per test
// binary: an RSA key of signer.RSABits takes long enough to make that
// count under the race detector.
var rsaKeys = sync.OnceValues(func() ([2]*rsa.PrivateKey, error) {
	var keys [2]*rsa.PrivateKey
	for i := range keys {
		k, err := rsa.GenerateKey(rand.Reader, signer.RSABits)
		if err != nil {
			return keys, err //nolint:wrapcheck // a test's
		}
		keys[i] = k
	}
	return keys, nil
})

// NewRSAFake is an RSA key of signer.RSABits; the second one, other,
// differs from the first.
func NewRSAFake(t *testing.T, other bool) Fake {
	t.Helper()
	keys, err := rsaKeys()
	if err != nil {
		t.Fatal(err)
	}
	if other {
		return Fake{Key: keys[1]}
	}
	return Fake{Key: keys[0]}
}

// NewRSA2048Fake generates an RSA key of 2048 bits, which no algorithm
// accepts.
func NewRSA2048Fake(t *testing.T) Fake {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return Fake{Key: k}
}

// ECDSA is the public key of a P-256 Fake.
func (f Fake) ECDSA() *ecdsa.PublicKey {
	pub, _ := f.Key.Public().(*ecdsa.PublicKey)
	return pub
}

// RSA is the public key of an RSA Fake.
func (f Fake) RSA() *rsa.PublicKey {
	pub, _ := f.Key.Public().(*rsa.PublicKey)
	return pub
}

// PKIX is the public key as a DER SubjectPublicKeyInfo.
func (f Fake) PKIX(t *testing.T) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(f.Key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// DER signs digest under alg as AWS KMS and Cloud KMS answer: ASN.1 DER
// for ES256, and the signature itself for RS256 and PS256.
func (f Fake) DER(t *testing.T, alg jose.SignatureAlgorithm, digest []byte) []byte {
	t.Helper()
	var opts crypto.SignerOpts = crypto.SHA256
	if alg == signer.PS256 {
		opts = &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256}
	}
	sig, err := f.Key.Sign(rand.Reader, digest, opts)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// Raw signs digest under alg as Key Vault and Vault answer: r || s for
// ES256, and the signature itself for RS256 and PS256.
func (f Fake) Raw(t *testing.T, alg jose.SignatureAlgorithm, digest []byte) []byte {
	t.Helper()
	sig, err := signer.FromDER(signer.JWK(f.Key.Public(), "", alg), f.DER(t, alg, digest))
	if err != nil {
		t.Fatal(err)
	}
	return sig
}
