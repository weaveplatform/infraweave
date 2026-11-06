package signer

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"

	jose "github.com/go-jose/go-jose/v4"
)

// ES256 is ECDSA on P-256 with SHA-256, the api-token purpose's algorithm
// and the only curve decision 0013's FIPS 140-3 rule allows.
const ES256 = jose.ES256

// DigestSize is the length of the digest a Signer signs: SHA-256's.
const DigestSize = 32

// esSize is the length of each of an ES256 signature's two halves, r and
// s, in JWS's fixed-width form (RFC 7518 §3.4).
const esSize = 32

var (
	// ErrKey reports a private key that is not a P-256 ECDSA key.
	ErrKey = errors.New("signing key is not ECDSA P-256")
	// ErrDigest reports a digest that is not DigestSize bytes.
	ErrDigest = errors.New("digest is not SHA-256")
	// ErrRef reports an auth.signing.kms_keys reference a backend cannot
	// open: malformed, or naming a key whose material may change.
	ErrRef = errors.New("invalid KMS signing key reference")
	// ErrSignature reports a KMS's signature that is not an ES256
	// signature in the form its API documents.
	ErrSignature = errors.New("malformed ES256 signature")
)

// Signer holds the private half of one signing key, wherever it lives.
type Signer interface {
	// Public is the key's public JWK, whose KeyID is the kid a token names
	// and whose Algorithm is the JWS alg it signs with.
	Public() jose.JSONWebKey
	// Sign signs digest, the SHA-256 of a JWS signing input, and returns
	// the signature as JWS carries it: r || s for ES256.
	Sign(ctx context.Context, digest []byte) ([]byte, error)
}

// KMS is a Signer whose key lives in a KMS. Ref names the provider and
// the key, as signing_keys.kms_key_ref stores it; its public JWK has no
// KeyID, since the kid is the signing_keys row's, which WithKeyID adds.
type KMS interface {
	Signer
	Ref() string
}

// Local is the local backend: a P-256 key held in process, whose private
// half the caller seals for storage (PrivateKey) and opens again
// (ParseLocal).
type Local struct {
	key *ecdsa.PrivateKey
	jwk jose.JSONWebKey
}

// GenerateLocal generates a key named kid.
func GenerateLocal(kid string) (*Local, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}
	return newLocal(kid, key), nil
}

// ParseLocal reads the PKCS #8 form PrivateKey wrote for the key named kid.
func ParseLocal(kid string, pkcs8 []byte) (*Local, error) {
	k, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrKey, kid, err)
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: %s", ErrKey, kid)
	}
	return newLocal(kid, key), nil
}

func newLocal(kid string, key *ecdsa.PrivateKey) *Local {
	return &Local{key: key, jwk: JWK(&key.PublicKey, kid)}
}

// JWK is pub as an ES256 signing key named kid.
func JWK(pub *ecdsa.PublicKey, kid string) jose.JSONWebKey {
	return jose.JSONWebKey{Key: pub, KeyID: kid, Algorithm: string(ES256), Use: "sig"}
}

// P256 returns pub as a P-256 ECDSA key, or ErrKey naming ref: a KMS key
// of another type or curve signs nothing ES256 verifies.
func P256(ref string, pub crypto.PublicKey) (*ecdsa.PublicKey, error) {
	k, ok := pub.(*ecdsa.PublicKey)
	if !ok || k.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: %s is %T", ErrKey, ref, pub)
	}
	return k, nil
}

// ParsePKIX reads a DER SubjectPublicKeyInfo, the form AWS KMS answers and
// a PEM public key carries, as a P-256 key, naming ref when it is not one.
func ParsePKIX(ref string, der []byte) (*ecdsa.PublicKey, error) {
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrKey, ref, err)
	}
	return P256(ref, pub)
}

// FromDER converts the ASN.1 DER ECDSA signature AWS KMS and Cloud KMS
// answer into JWS's r || s (RFC 7518 §3.4). Trailing bytes, a negative or
// zero half, or a half wider than P-256's order are ErrSignature.
func FromDER(der []byte) ([]byte, error) {
	var v struct{ R, S *big.Int }
	rest, err := asn1.Unmarshal(der, &v)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrSignature, err)
	case len(rest) != 0:
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrSignature, len(rest))
	case v.R.Sign() <= 0 || v.S.Sign() <= 0 || v.R.BitLen() > 8*esSize || v.S.BitLen() > 8*esSize:
		return nil, fmt.Errorf("%w: r or s out of range", ErrSignature)
	}
	sig := make([]byte, 2*esSize)
	v.R.FillBytes(sig[:esSize])
	v.S.FillBytes(sig[esSize:])
	return sig, nil
}

// Raw checks that sig is r || s, the form Key Vault and Vault's jws
// marshaling answer.
func Raw(sig []byte) ([]byte, error) {
	if len(sig) != 2*esSize {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrSignature, len(sig), 2*esSize)
	}
	return sig, nil
}

// CheckDigest refuses a digest that is not DigestSize bytes, before a KMS
// is asked.
func CheckDigest(digest []byte) error {
	if len(digest) != DigestSize {
		return fmt.Errorf("%w: %d bytes", ErrDigest, len(digest))
	}
	return nil
}

// WithKeyID is s with its public JWK named kid.
func WithKeyID(s Signer, kid string) Signer { return named{Signer: s, kid: kid} }

type named struct {
	Signer

	kid string
}

func (n named) Public() jose.JSONWebKey {
	k := n.Signer.Public()
	k.KeyID = n.kid
	return k
}

// PrivateKey is the key's private half in PKCS #8, for the caller to seal.
func (l *Local) PrivateKey() ([]byte, error) {
	b, err := x509.MarshalPKCS8PrivateKey(l.key)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrKey, l.jwk.KeyID, err)
	}
	return b, nil
}

// Public is the key's public JWK.
func (l *Local) Public() jose.JSONWebKey { return l.jwk }

// Sign signs digest with the key.
func (l *Local) Sign(_ context.Context, digest []byte) ([]byte, error) {
	if err := CheckDigest(digest); err != nil {
		return nil, err
	}
	r, s, err := ecdsa.Sign(rand.Reader, l.key, digest)
	if err != nil {
		return nil, fmt.Errorf("sign with %s: %w", l.jwk.KeyID, err)
	}
	sig := make([]byte, 2*esSize)
	r.FillBytes(sig[:esSize])
	s.FillBytes(sig[esSize:])
	return sig, nil
}
