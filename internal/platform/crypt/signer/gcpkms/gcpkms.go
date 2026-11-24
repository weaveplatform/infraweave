package gcpkms

import (
	"context"
	"encoding/pem"
	"fmt"
	"regexp"
	"slices"

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

// algorithms are the CryptoKeyVersion algorithms that sign each JWS
// algorithm: each fixes its key's size and its padding.
var algorithms = map[jose.SignatureAlgorithm][]kmspb.CryptoKeyVersion_CryptoKeyVersionAlgorithm{
	signer.ES256: {kmspb.CryptoKeyVersion_EC_SIGN_P256_SHA256},
	signer.RS256: {
		kmspb.CryptoKeyVersion_RSA_SIGN_PKCS1_3072_SHA256,
		kmspb.CryptoKeyVersion_RSA_SIGN_PKCS1_4096_SHA256,
	},
	signer.PS256: {
		kmspb.CryptoKeyVersion_RSA_SIGN_PSS_3072_SHA256,
		kmspb.CryptoKeyVersion_RSA_SIGN_PSS_4096_SHA256,
	},
}

// Signer signs with a CryptoKeyVersion whose algorithm signs one JWS
// algorithm.
type Signer struct {
	client *kms.KeyManagementClient
	name   string
	ref    string
	jwk    jose.JSONWebKey
}

var _ signer.KMS = (*Signer)(nil)

// New opens name, a CryptoKeyVersion's resource name, to sign alg,
// reading its public key once, with Application Default Credentials
// unless opts say otherwise. A name of anything else is signer.ErrRef, an
// alg Cloud KMS does not sign signer.ErrAlgorithm, a version whose
// algorithm signs another signer.ErrKey, and an answer whose checksum or
// name does not confirm it kekgcpkms.ErrIntegrity.
func New(ctx context.Context, name string, alg jose.SignatureAlgorithm, opts ...option.ClientOption) (*Signer, error) {
	ref := kek.GCPKMS + ":" + name
	if _, ok := algorithms[alg]; !ok {
		return nil, fmt.Errorf("%w: %s: %q", signer.ErrAlgorithm, ref, alg)
	}
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
	s, err := open(ctx, client, name, ref, alg)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return s, nil
}

func open(ctx context.Context, client *kms.KeyManagementClient, name, ref string, alg jose.SignatureAlgorithm) (*Signer, error) {
	pk, err := client.GetPublicKey(ctx, &kmspb.GetPublicKeyRequest{Name: name})
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", ref, err)
	case pk.GetName() != name ||
		pk.GetPemCrc32C().GetValue() != kekgcpkms.CRC32C([]byte(pk.GetPem())).GetValue():
		return nil, fmt.Errorf("%s: public key: %w", ref, kekgcpkms.ErrIntegrity)
	case !slices.Contains(algorithms[alg], pk.GetAlgorithm()):
		return nil, fmt.Errorf("%w: %s is %s, want one of %s to sign %s", signer.ErrKey, ref,
			pk.GetAlgorithm(), algorithms[alg], alg)
	}
	block, _ := pem.Decode([]byte(pk.GetPem()))
	if block == nil {
		return nil, fmt.Errorf("%w: %s: the public key is not PEM", signer.ErrKey, ref)
	}
	pub, err := signer.ParsePKIX(ref, alg, block.Bytes)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the reference
	}
	return &Signer{client: client, name: name, ref: ref, jwk: signer.JWK(pub, "", alg)}, nil
}

// Ref is gcpkms:<CryptoKeyVersion resource name>.
func (s *Signer) Ref() string { return s.ref }

// Public is the key's public JWK, without a kid.
func (s *Signer) Public() jose.JSONWebKey { return s.jwk }

// Close closes the client.
func (s *Signer) Close() error { return s.client.Close() } //nolint:wrapcheck // nothing to add

// Sign asks Cloud KMS to sign digest as a SHA-256 digest, checks the
// integrity fields of the request and the answer, and converts Cloud
// KMS's answer to JWS's form: an ECDSA signature from DER to r || s.
func (s *Signer) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	if err := signer.CheckDigest(s.jwk.Algorithm, digest); err != nil {
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
	sig, err := signer.FromDER(s.jwk, out.GetSignature())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	return sig, nil
}
