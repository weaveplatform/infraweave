package awskms

import (
	"context"
	"fmt"
	"slices"
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

// signings are the KMS signing algorithms that sign each JWS algorithm.
// A key lists those it signs with; signer.ParsePKIX then checks its curve
// or size, which an RSA_2048 key, listing the same algorithms, fails.
var signings = map[jose.SignatureAlgorithm]types.SigningAlgorithmSpec{
	signer.ES256: types.SigningAlgorithmSpecEcdsaSha256,
	signer.RS256: types.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
	signer.PS256: types.SigningAlgorithmSpecRsassaPssSha256,
}

// Signer signs with a SIGN_VERIFY KMS key under one algorithm.
type Signer struct {
	client  *kms.Client
	keyID   string
	ref     string
	signing types.SigningAlgorithmSpec
	jwk     jose.JSONWebKey
}

var _ signer.KMS = (*Signer)(nil)

// New opens keyID, a KMS key's ID or ARN, to sign alg, reading its public
// key once. An ARN's region is the client's; a bare ID takes the SDK's.
// An alias is signer.ErrRef: it can be pointed at another key, while
// signing_keys holds the public half of the key it named first. A key of
// a spec or usage that cannot sign alg is signer.ErrKey, and an alg KMS
// does not sign signer.ErrAlgorithm. opts change the KMS client, as a
// test's endpoint does.
func New(ctx context.Context, keyID string, alg jose.SignatureAlgorithm, opts ...func(*kms.Options)) (*Signer, error) {
	ref := kek.AWSKMS + ":" + keyID
	if keyID == "" || strings.HasPrefix(keyID, "alias/") || strings.Contains(keyID, ":alias/") {
		return nil, fmt.Errorf("%w: %s: name a key ID or key ARN, not an alias", signer.ErrRef, ref)
	}
	signing, ok := signings[alg]
	if !ok {
		return nil, fmt.Errorf("%w: %s: %q", signer.ErrAlgorithm, ref, alg)
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
	if out.KeyUsage != types.KeyUsageTypeSignVerify || !slices.Contains(out.SigningAlgorithms, signing) {
		return nil, fmt.Errorf("%w: %s is %s for %s with %v, want %s with %s to sign %s",
			signer.ErrKey, ref, out.KeySpec, out.KeyUsage, out.SigningAlgorithms,
			types.KeyUsageTypeSignVerify, signing, alg)
	}
	pub, err := signer.ParsePKIX(ref, alg, out.PublicKey)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the reference
	}
	return &Signer{
		client: client, keyID: keyID, ref: ref, signing: signing, jwk: signer.JWK(pub, "", alg),
	}, nil
}

// Ref is awskms:<key ID or ARN>.
func (s *Signer) Ref() string { return s.ref }

// Public is the key's public JWK, without a kid.
func (s *Signer) Public() jose.JSONWebKey { return s.jwk }

// Sign asks KMS to sign digest as a SHA-256 digest under the key's
// algorithm, and converts KMS's answer to JWS's form: an ECDSA signature
// from DER to r || s.
func (s *Signer) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	if err := signer.CheckDigest(s.jwk.Algorithm, digest); err != nil {
		return nil, err //nolint:wrapcheck // names itself
	}
	out, err := s.client.Sign(ctx, &kms.SignInput{
		KeyId:            aws.String(s.keyID),
		Message:          digest,
		MessageType:      types.MessageTypeDigest,
		SigningAlgorithm: s.signing,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	sig, err := signer.FromDER(s.jwk, out.Signature)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ref, err)
	}
	return sig, nil
}
