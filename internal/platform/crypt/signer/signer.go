package signer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"

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
	return &Local{key: key, jwk: jose.JSONWebKey{
		Key:       &key.PublicKey,
		KeyID:     kid,
		Algorithm: string(ES256),
		Use:       "sig",
	}}
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
	if len(digest) != DigestSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrDigest, len(digest))
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
