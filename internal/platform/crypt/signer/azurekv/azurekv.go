package azurekv

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"fmt"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	kekazurekv "github.com/weaveplatform/infraweave/internal/platform/crypt/kek/azurekv"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// Signer signs with one version of a Key Vault EC key on P-256.
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
// https://<vault>/keys/<name>/<version>, reading its public key once. A
// key ID without a version is signer.ErrRef, since a key's current
// version changes when it is rotated while signing_keys holds the public
// half of one; a key that is not EC or EC-HSM on P-256 is signer.ErrKey.
func New(ctx context.Context, keyID string, o Options) (*Signer, error) {
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
	s.jwk = signer.JWK(pub, "")
	return s, nil
}

// public is k's P-256 public key. ParseUncompressedPublicKey checks that
// the point is on the curve.
func public(ref string, k *azkeys.JSONWebKey) (*ecdsa.PublicKey, error) {
	if k == nil || k.Kty == nil || (*k.Kty != azkeys.KeyTypeEC && *k.Kty != azkeys.KeyTypeECHSM) ||
		k.Crv == nil || *k.Crv != azkeys.CurveNameP256 {
		return nil, fmt.Errorf("%w: %s is not an EC key on P-256", signer.ErrKey, ref)
	}
	point := append(append([]byte{4}, k.X...), k.Y...)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", signer.ErrKey, ref, err)
	}
	return pub, nil
}

// Ref is azurekv:<key version ID>.
func (s *Signer) Ref() string { return s.ref }

// Public is the key's public JWK, without a kid.
func (s *Signer) Public() jose.JSONWebKey { return s.jwk }

// Sign asks Key Vault to sign digest with ES256, whose answer is already
// JWS's r || s.
func (s *Signer) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	if err := signer.CheckDigest(digest); err != nil {
		return nil, err //nolint:wrapcheck // names itself
	}
	alg := azkeys.SignatureAlgorithmES256
	resp, err := s.client.Sign(ctx, s.name, s.version, azkeys.SignParameters{
		Algorithm: &alg,
		Value:     digest,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	sig, err := signer.Raw(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	return sig, nil
}
