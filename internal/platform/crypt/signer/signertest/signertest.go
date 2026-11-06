package signertest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
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
	t *testing.T
	s signer.Signer
}

func (o opaque) Public() *jose.JSONWebKey {
	k := o.s.Public()
	return &k
}

func (o opaque) Algs() []jose.SignatureAlgorithm {
	return []jose.SignatureAlgorithm{signer.ES256}
}

func (o opaque) SignPayload(payload []byte, _ jose.SignatureAlgorithm) ([]byte, error) {
	d := sha256.Sum256(payload)
	return o.s.Sign(o.t.Context(), d[:]) //nolint:wrapcheck // go-jose wraps it
}

// Contract checks that s's public key is an ES256 P-256 signing key, that
// a JWS it signs verifies under that key and fails once its payload
// changes, that a digest of the wrong length is signer.ErrDigest, and that
// its Ref starts with prefix.
func Contract(t *testing.T, s signer.KMS, prefix string) {
	t.Helper()
	if ref := s.Ref(); !strings.HasPrefix(ref, prefix) {
		t.Errorf("Ref = %q, want %s…", ref, prefix)
	}
	jwk := s.Public()
	if jwk.Algorithm != string(signer.ES256) || jwk.Use != "sig" || !jwk.IsPublic() ||
		jwk.KeyID != "" {
		t.Errorf("Public = %+v", jwk)
	}
	if _, err := signer.P256(s.Ref(), jwk.Key); err != nil {
		t.Fatal(err)
	}
	js, err := jose.NewSigner(jose.SigningKey{
		Algorithm: signer.ES256,
		Key:       opaque{t: t, s: signer.WithKeyID(s, KeyID)},
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
	parsed, err := jose.ParseSignedCompact(raw, []jose.SignatureAlgorithm{signer.ES256})
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
		[]jose.SignatureAlgorithm{signer.ES256},
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
type Fake struct{ Key *ecdsa.PrivateKey }

// NewFake generates a P-256 key.
func NewFake(t *testing.T) Fake {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Fake{Key: k}
}

// PKIX is the public key as a DER SubjectPublicKeyInfo.
func (f Fake) PKIX(t *testing.T) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&f.Key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// DER signs digest, answering ASN.1 DER as AWS KMS and Cloud KMS do.
func (f Fake) DER(t *testing.T, digest []byte) []byte {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, f.Key, digest)
	if err != nil {
		t.Fatal(err)
	}
	der, err := asn1.Marshal(struct{ R, S *big.Int }{r, s})
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// Raw signs digest, answering r || s as Key Vault and Vault do.
func (f Fake) Raw(t *testing.T, digest []byte) []byte {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, f.Key, digest)
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return sig
}
