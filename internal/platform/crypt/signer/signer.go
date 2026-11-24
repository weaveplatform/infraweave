package signer

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"slices"

	jose "github.com/go-jose/go-jose/v4"
)

// The algorithms a Signer signs with, each over a SHA-256 digest and each
// approved by Go's FIPS 140-3 mode: ES256 is ECDSA on P-256, the
// api-token purpose's; RS256 is RSASSA-PKCS1-v1_5, the federation
// purpose's default; PS256 is RSASSA-PSS with a salt as long as the
// digest, as RFC 7518 §3.5 requires.
//
// ref: decisions 0006 and 0013
const (
	ES256 = jose.ES256
	RS256 = jose.RS256
	PS256 = jose.PS256
)

// Algorithms are every algorithm a Signer signs with.
var Algorithms = []jose.SignatureAlgorithm{ES256, RS256, PS256}

// RSABits is the size of the RSA key GenerateLocal makes for RS256 and
// PS256, and the least a KMS's RSA key may have.
//
// ref: decision 0006 § "Signing keys"
const RSABits = 3072

// DigestSize is the length of the digest a Signer signs: SHA-256's.
const DigestSize = 32

// esSize is the length of each of an ES256 signature's two halves, r and
// s, in JWS's fixed-width form (RFC 7518 §3.4).
const esSize = 32

var (
	// ErrAlgorithm reports an algorithm that is not one of Algorithms.
	ErrAlgorithm = errors.New("unsupported signing algorithm")
	// ErrKey reports a key that cannot sign its algorithm: not ECDSA
	// P-256 for ES256, or not RSA of at least RSABits for RS256 and PS256.
	ErrKey = errors.New("signing key does not fit its algorithm")
	// ErrDigest reports a digest that is not DigestSize bytes.
	ErrDigest = errors.New("digest is not SHA-256")
	// ErrRef reports a KMS key reference a backend cannot open: malformed,
	// or naming a key whose material may change.
	ErrRef = errors.New("invalid KMS signing key reference")
	// ErrSignature reports a KMS's signature that is not one of its
	// algorithm in the form its API documents.
	ErrSignature = errors.New("malformed signature")
)

// Signer holds the private half of one signing key, wherever it lives.
type Signer interface {
	// Public is the key's public JWK, whose KeyID is the kid a token names
	// and whose Algorithm is the JWS alg it signs with.
	Public() jose.JSONWebKey
	// Sign signs digest, the SHA-256 of a JWS signing input, and returns
	// the signature as JWS carries it: r || s for ES256, and as long as
	// the modulus for RS256 and PS256.
	Sign(ctx context.Context, digest []byte) ([]byte, error)
}

// KMS is a Signer whose key lives in a KMS. Ref names the provider and
// the key, as signing_keys.kms_key_ref stores it; its public JWK has no
// KeyID, since the kid is the signing_keys row's, which WithKeyID adds.
type KMS interface {
	Signer
	Ref() string
}

// Local is the local backend: a key held in process, whose private half
// the caller seals for storage (PrivateKey) and opens again (ParseLocal).
type Local struct {
	key crypto.Signer
	jwk jose.JSONWebKey
}

// GenerateLocal generates a key named kid that signs alg: ECDSA on P-256
// for ES256, and RSA of RSABits for RS256 and PS256.
func GenerateLocal(alg jose.SignatureAlgorithm, kid string) (*Local, error) {
	var (
		key crypto.Signer
		err error
	)
	switch alg {
	case ES256:
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case RS256, PS256:
		key, err = rsa.GenerateKey(rand.Reader, RSABits)
	default:
		return nil, fmt.Errorf("%w: %s: %q", ErrAlgorithm, kid, alg)
	}
	if err != nil {
		return nil, fmt.Errorf("generate %s signing key: %w", alg, err)
	}
	return newLocal(kid, alg, key), nil
}

// ParseLocal reads the PKCS #8 form PrivateKey wrote for the key named
// kid, which signs alg, refusing a key that cannot sign it (ErrKey).
func ParseLocal(alg jose.SignatureAlgorithm, kid string, pkcs8 []byte) (*Local, error) {
	k, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrKey, kid, err)
	}
	// An X25519 key is no crypto.Signer, and CheckPublic refuses its nil
	// public half with every other key that signs nothing alg verifies.
	key, ok := k.(crypto.Signer)
	var pub crypto.PublicKey
	if ok {
		pub = key.Public()
	}
	if err := CheckPublic(kid, alg, pub); err != nil {
		return nil, err
	}
	return newLocal(kid, alg, key), nil
}

func newLocal(kid string, alg jose.SignatureAlgorithm, key crypto.Signer) *Local {
	return &Local{key: key, jwk: JWK(key.Public(), kid, alg)}
}

// JWK is pub as a key named kid that signs alg.
func JWK(pub crypto.PublicKey, kid string, alg jose.SignatureAlgorithm) jose.JSONWebKey {
	return jose.JSONWebKey{Key: pub, KeyID: kid, Algorithm: string(alg), Use: "sig"}
}

// CheckPublic refuses pub, the public half of the key ref names, unless
// it can sign alg: ECDSA on P-256 for ES256, and RSA of at least RSABits
// for RS256 and PS256. A refusal is ErrKey naming ref and what the key
// is, or ErrAlgorithm for an alg not in Algorithms.
func CheckPublic(ref string, alg jose.SignatureAlgorithm, pub crypto.PublicKey) error {
	switch alg {
	case ES256:
		if k, ok := pub.(*ecdsa.PublicKey); ok && k.Curve == elliptic.P256() {
			return nil
		}
		return fmt.Errorf("%w: %s is %s, want ECDSA P-256 for %s", ErrKey, ref, describe(pub), alg)
	case RS256, PS256:
		if k, ok := pub.(*rsa.PublicKey); ok && k.N.BitLen() >= RSABits {
			return nil
		}
		return fmt.Errorf("%w: %s is %s, want RSA of at least %d bits for %s",
			ErrKey, ref, describe(pub), RSABits, alg)
	}
	return fmt.Errorf("%w: %s: %q", ErrAlgorithm, ref, alg)
}

// describe names pub's type and size, for an error.
func describe(pub crypto.PublicKey) string {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		return "ECDSA " + k.Curve.Params().Name
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA %d", k.N.BitLen())
	}
	return fmt.Sprintf("%T", pub)
}

// ParsePKIX reads a DER SubjectPublicKeyInfo, the form AWS KMS answers and
// a PEM public key carries, as a key that can sign alg, naming ref when
// it is not one (CheckPublic).
func ParsePKIX(ref string, alg jose.SignatureAlgorithm, der []byte) (crypto.PublicKey, error) {
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrKey, ref, err)
	}
	if err := CheckPublic(ref, alg, pub); err != nil {
		return nil, err
	}
	return pub, nil
}

// FromDER converts a signature in the form AWS KMS and Cloud KMS answer
// for jwk's key into JWS's: an ES256 signature, ASN.1 DER, becomes r || s
// (RFC 7518 §3.4), where trailing bytes, a negative or zero half, or a
// half wider than P-256's order are ErrSignature; an RSA signature, which
// both answer as JWS carries it, is checked as Raw checks it.
func FromDER(jwk jose.JSONWebKey, der []byte) ([]byte, error) {
	if jwk.Algorithm != string(ES256) {
		return Raw(jwk, der)
	}
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

// Raw checks that sig is as long as JWS makes a signature of jwk's key:
// r || s for ES256, the form Key Vault and Vault's jws marshaling answer,
// and the modulus' length for an RSA key.
func Raw(jwk jose.JSONWebKey, sig []byte) ([]byte, error) {
	want := 2 * esSize
	if k, ok := jwk.Key.(*rsa.PublicKey); ok {
		want = k.Size()
	}
	if len(sig) != want {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrSignature, len(sig), want)
	}
	return sig, nil
}

// CheckDigest refuses an algorithm not in Algorithms and a digest that is
// not DigestSize bytes, before a KMS is asked.
func CheckDigest(alg string, digest []byte) error {
	if !slices.Contains(Algorithms, jose.SignatureAlgorithm(alg)) {
		return fmt.Errorf("%w: %q", ErrAlgorithm, alg)
	}
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

// Sign signs digest with the key under its algorithm. The standard
// library answers ECDSA in ASN.1 DER, which FromDER turns into r || s.
func (l *Local) Sign(_ context.Context, digest []byte) ([]byte, error) {
	if err := CheckDigest(l.jwk.Algorithm, digest); err != nil {
		return nil, err
	}
	var opts crypto.SignerOpts = crypto.SHA256
	if l.jwk.Algorithm == string(PS256) {
		opts = &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256}
	}
	sig, err := l.key.Sign(rand.Reader, digest, opts)
	if err != nil {
		return nil, fmt.Errorf("sign with %s: %w", l.jwk.KeyID, err)
	}
	return FromDER(l.jwk, sig)
}
