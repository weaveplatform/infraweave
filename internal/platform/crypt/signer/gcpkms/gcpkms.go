package gcpkms

import (
	"context"
	"encoding/pem"
	"fmt"
	"regexp"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	jose "github.com/go-jose/go-jose/v4"
	"google.golang.org/api/option"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	kekgcpkms "github.com/weaveplatform/infraweave/internal/platform/crypt/kek/gcpkms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// versionName is a CryptoKeyVersion's resource name. AsymmetricSign takes
// a version, and each version's key pair is fixed, so the public half
// signing_keys holds stays the version's.
var versionName = regexp.MustCompile(
	`^projects/[^/]+/locations/[^/]+/keyRings/[^/]+/cryptoKeys/[^/]+/cryptoKeyVersions/[^/]+$`,
)

// Signer signs with an EC_SIGN_P256_SHA256 CryptoKeyVersion.
type Signer struct {
	client *kms.KeyManagementClient
	name   string
	ref    string
	jwk    jose.JSONWebKey
}

var _ signer.KMS = (*Signer)(nil)

// New opens name, a CryptoKeyVersion's resource name, reading its public
// key once, with Application Default Credentials unless opts say
// otherwise. A name of anything else is signer.ErrRef, a version of
// another algorithm signer.ErrKey, and an answer whose checksum or name
// does not confirm it kekgcpkms.ErrIntegrity.
func New(ctx context.Context, name string, opts ...option.ClientOption) (*Signer, error) {
	ref := kek.GCPKMS + ":" + name
	if !versionName.MatchString(name) {
		return nil, fmt.Errorf(
			"%w: %s: want projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>/cryptoKeyVersions/<v>",
			signer.ErrRef,
			ref,
		)
	}
	client, err := kekgcpkms.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	s, err := open(ctx, client, name, ref)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return s, nil
}

func open(ctx context.Context, client *kms.KeyManagementClient, name, ref string) (*Signer, error) {
	pk, err := client.GetPublicKey(ctx, &kmspb.GetPublicKeyRequest{Name: name})
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", ref, err)
	case pk.GetName() != name ||
		pk.GetPemCrc32C().GetValue() != kekgcpkms.CRC32C([]byte(pk.GetPem())).GetValue():
		return nil, fmt.Errorf("%s: public key: %w", ref, kekgcpkms.ErrIntegrity)
	case pk.GetAlgorithm() != kmspb.CryptoKeyVersion_EC_SIGN_P256_SHA256:
		return nil, fmt.Errorf("%w: %s is %s, want %s", signer.ErrKey, ref,
			pk.GetAlgorithm(), kmspb.CryptoKeyVersion_EC_SIGN_P256_SHA256)
	}
	block, _ := pem.Decode([]byte(pk.GetPem()))
	if block == nil {
		return nil, fmt.Errorf("%w: %s: the public key is not PEM", signer.ErrKey, ref)
	}
	pub, err := signer.ParsePKIX(ref, block.Bytes)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the reference
	}
	return &Signer{client: client, name: name, ref: ref, jwk: signer.JWK(pub, "")}, nil
}

// Ref is gcpkms:<CryptoKeyVersion resource name>.
func (s *Signer) Ref() string { return s.ref }

// Public is the key's public JWK, without a kid.
func (s *Signer) Public() jose.JSONWebKey { return s.jwk }

// Close closes the client.
func (s *Signer) Close() error { return s.client.Close() } //nolint:wrapcheck // nothing to add

// Sign asks Cloud KMS to sign digest as a SHA-256 digest, checks the
// integrity fields of the request and the answer, and converts Cloud
// KMS's DER signature to JWS's r || s.
func (s *Signer) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	if err := signer.CheckDigest(digest); err != nil {
		return nil, err //nolint:wrapcheck // names itself
	}
	out, err := s.client.AsymmetricSign(ctx, &kmspb.AsymmetricSignRequest{
		Name:         s.name,
		Digest:       &kmspb.Digest{Digest: &kmspb.Digest_Sha256{Sha256: digest}},
		DigestCrc32C: kekgcpkms.CRC32C(digest),
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	case !out.GetVerifiedDigestCrc32C() || out.GetName() != s.name ||
		out.GetSignatureCrc32C().GetValue() != kekgcpkms.CRC32C(out.GetSignature()).GetValue():
		return nil, fmt.Errorf("%s: sign: %w", s.ref, kekgcpkms.ErrIntegrity)
	}
	sig, err := signer.FromDER(out.GetSignature())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	return sig, nil
}
