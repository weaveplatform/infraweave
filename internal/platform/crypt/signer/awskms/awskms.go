package awskms

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	kekawskms "github.com/weaveplatform/infraweave/internal/platform/crypt/kek/awskms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
)

// Signer signs with an ECC_NIST_P256 KMS key whose usage is SIGN_VERIFY.
type Signer struct {
	client *kms.Client
	keyID  string
	ref    string
	jwk    jose.JSONWebKey
}

var _ signer.KMS = (*Signer)(nil)

// New opens keyID, a KMS key's ID or ARN, reading its public key once. An
// ARN's region is the client's; a bare ID takes the SDK's. An alias is
// signer.ErrRef: it can be pointed at another key, while signing_keys
// holds the public half of the key it named first. A key of another spec
// or usage is signer.ErrKey. opts change the KMS client, as a test's
// endpoint does.
func New(ctx context.Context, keyID string, opts ...func(*kms.Options)) (*Signer, error) {
	ref := kek.AWSKMS + ":" + keyID
	if keyID == "" || strings.HasPrefix(keyID, "alias/") || strings.Contains(keyID, ":alias/") {
		return nil, fmt.Errorf("%w: %s: name a key ID or key ARN, not an alias", signer.ErrRef, ref)
	}
	var region string
	if a, err := arn.Parse(keyID); err == nil {
		region = a.Region
	}
	client, err := kekawskms.NewClient(ctx, region, opts...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	out, err := client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	if out.KeySpec != types.KeySpecEccNistP256 || out.KeyUsage != types.KeyUsageTypeSignVerify {
		return nil, fmt.Errorf("%w: %s is %s for %s, want %s for %s", signer.ErrKey, ref,
			out.KeySpec, out.KeyUsage, types.KeySpecEccNistP256, types.KeyUsageTypeSignVerify)
	}
	pub, err := signer.ParsePKIX(ref, out.PublicKey)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the reference
	}
	return &Signer{client: client, keyID: keyID, ref: ref, jwk: signer.JWK(pub, "")}, nil
}

// Ref is awskms:<key ID or ARN>.
func (s *Signer) Ref() string { return s.ref }

// Public is the key's public JWK, without a kid.
func (s *Signer) Public() jose.JSONWebKey { return s.jwk }

// Sign asks KMS to sign digest as a SHA-256 digest, and converts KMS's DER
// signature to JWS's r || s.
func (s *Signer) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	if err := signer.CheckDigest(digest); err != nil {
		return nil, err //nolint:wrapcheck // names itself
	}
	out, err := s.client.Sign(ctx, &kms.SignInput{
		KeyId:            aws.String(s.keyID),
		Message:          digest,
		MessageType:      types.MessageTypeDigest,
		SigningAlgorithm: types.SigningAlgorithmSpecEcdsaSha256,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	sig, err := signer.FromDER(out.Signature)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	return sig, nil
}
