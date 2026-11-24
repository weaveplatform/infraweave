package azurekv

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"fmt"
	"math/big"
	"net/url"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	kekazurekv "github.com/weaveplatform/infraweave/internal/platform/crypt/kek/azurekv"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// Signer signs with one version of a Key Vault key under one algorithm.
type Signer struct {
	client        *azkeys.Client
	name, version string
	ref           string
	jwk           jose.JSONWebKey
}

var _ signer.KMS = (*Signer)(nil)

// Options are what a test changes: the KEK provider's.
type Options = kekazurekv.Options

// New opens keyID, a key version's ID,
// https://<vault>/keys/<name>/<version>, to sign alg, reading its public
// key once. A key ID without a version is signer.ErrRef, since a key's
// current version changes when it is rotated while signing_keys holds the
// public half of one; a key that cannot sign alg, which takes an EC or
// EC-HSM key on P-256 for ES256 and an RSA or RSA-HSM key of at least
// signer.RSABits for RS256 and PS256, is signer.ErrKey.
func New(ctx context.Context, keyID string, alg jose.SignatureAlgorithm, o Options) (*Signer, error) {
	ref := kek.AzureKV + ":" + keyID
	u, err := url.Parse(keyID)
	var parts []string
	if err == nil {
		parts = strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	}
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || len(parts) != 3 ||
		parts[0] != "keys" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf(
			"%w: %s: want https://<vault>/keys/<name>/<version>",
			signer.ErrRef,
			ref,
		)
	}
	if !slices.Contains(signer.Algorithms, alg) {
		return nil, fmt.Errorf("%w: %s: %q", signer.ErrAlgorithm, ref, alg)
	}
	client, err := kekazurekv.NewClient("https://"+u.Host, o)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	s := &Signer{client: client, name: parts[1], version: parts[2], ref: ref}
	resp, err := client.GetKey(ctx, s.name, s.version, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	pub, err := public(ref, resp.Key)
	if err != nil {
		return nil, err
	}
	if err := signer.CheckPublic(ref, alg, pub); err != nil {
		return nil, err //nolint:wrapcheck // names the reference
	}
	s.jwk = signer.JWK(pub, "", alg)
	return s, nil
}

// public is k's public key: an EC key's on P-256, whose point
// ParseUncompressedPublicKey checks is on the curve, or an RSA key's.
func public(ref string, k *azkeys.JSONWebKey) (crypto.PublicKey, error) {
	var kty azkeys.KeyType
	if k != nil && k.Kty != nil {
		kty = *k.Kty
	}
	switch kty {
	case azkeys.KeyTypeEC, azkeys.KeyTypeECHSM:
		if k.Crv == nil || *k.Crv != azkeys.CurveNameP256 {
			return nil, fmt.Errorf("%w: %s is an EC key on a curve other than P-256", signer.ErrKey, ref)
		}
		point := append(append([]byte{4}, k.X...), k.Y...)
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", signer.ErrKey, ref, err)
		}
		return pub, nil
	case azkeys.KeyTypeRSA, azkeys.KeyTypeRSAHSM:
		// An exponent wider than four bytes overflows rsa.PublicKey's int
		// on every platform; Key Vault's is 65537.
		if len(k.N) == 0 || len(k.E) == 0 || len(k.E) > 4 {
			return nil, fmt.Errorf("%w: %s: malformed RSA public key", signer.ErrKey, ref)
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(k.N),
			E: int(new(big.Int).SetBytes(k.E).Int64()),
		}, nil
	}
	return nil, fmt.Errorf("%w: %s is a key of type %q", signer.ErrKey, ref, kty)
}

// Ref is azurekv:<key version ID>.
func (s *Signer) Ref() string { return s.ref }

// Public is the key's public JWK, without a kid.
func (s *Signer) Public() jose.JSONWebKey { return s.jwk }

// Sign asks Key Vault to sign digest with the key's algorithm, whose
// answer is already JWS's form.
func (s *Signer) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	if err := signer.CheckDigest(s.jwk.Algorithm, digest); err != nil {
		return nil, err //nolint:wrapcheck // names itself
	}
	alg := azkeys.SignatureAlgorithm(s.jwk.Algorithm)
	resp, err := s.client.Sign(ctx, s.name, s.version, azkeys.SignParameters{
		Algorithm: &alg,
		Value:     digest,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	sig, err := signer.Raw(s.jwk, resp.Result)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	return sig, nil
}
