package awskms_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/awskms"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
	"github.com/weaveplatform/infraweave/test/deps"
)

// TestAccAWSKMSMoto opens moto's keys through the backend: an RSA_3072
// key for RS256 and PS256 and an ECC_NIST_P256 key for ES256, each read
// as its spec, and refuses an RSA_2048 key and the P-256 key for RS256,
// naming each. moto signs a DIGEST message as if it were RAW, hashing it
// once more, so each signature is checked over the SHA-256 of the digest
// the backend sent; the fake of TestUnitAWSKMS checks the signatures a
// JWS verifies under.
func TestAccAWSKMSMoto(t *testing.T) {
	environment(t)
	url := objectstoretest.Emulator(t, deps.ImageMoto, "5000/tcp",
		testcontainers.WithWaitStrategy(wait.ForHTTP("/moto-api/").WithPort("5000/tcp")))
	admin := kms.New(kms.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(url),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
	})
	key := func(spec types.KeySpec) string {
		t.Helper()
		out, err := admin.CreateKey(t.Context(), &kms.CreateKeyInput{
			KeySpec: spec, KeyUsage: types.KeyUsageTypeSignVerify,
		})
		if err != nil {
			t.Fatal(err)
		}
		return *out.KeyMetadata.Arn
	}
	rsa3072, p256, rsa2048 := key(types.KeySpecRsa3072), key(types.KeySpecEccNistP256), key(types.KeySpecRsa2048)

	digest := sha256.Sum256([]byte("header.payload"))
	signed := sha256.Sum256(digest[:])
	for id, alg := range map[string]jose.SignatureAlgorithm{
		rsa3072 + " RS256": signer.RS256, rsa3072 + " PS256": signer.PS256, p256 + " ES256": signer.ES256,
	} {
		arn, _, _ := strings.Cut(id, " ")
		s, err := awskms.New(t.Context(), arn, alg, endpoint(url))
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		if s.Public().Algorithm != string(alg) {
			t.Errorf("%s: Public = %+v", alg, s.Public())
		}
		sig, err := s.Sign(t.Context(), digest[:])
		if err != nil {
			t.Fatalf("%s: Sign: %v", alg, err)
		}
		switch pub := s.Public().Key.(type) {
		case *rsa.PublicKey:
			if alg == signer.PS256 {
				err = rsa.VerifyPSS(pub, crypto.SHA256, signed[:], sig, nil)
			} else {
				err = rsa.VerifyPKCS1v15(pub, crypto.SHA256, signed[:], sig)
			}
		case *ecdsa.PublicKey:
			r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
			if !ecdsa.Verify(pub, signed[:], r, s) {
				err = errors.New("ECDSA signature does not verify")
			}
		}
		if err != nil {
			t.Errorf("%s: %v", alg, err)
		}
	}
	for arn, alg := range map[string]jose.SignatureAlgorithm{rsa2048: signer.RS256, p256: signer.PS256} {
		if _, err := awskms.New(t.Context(), arn, alg, endpoint(url)); !errors.Is(err, signer.ErrKey) ||
			!strings.Contains(err.Error(), "awskms:"+arn) {
			t.Errorf("%s for %s: %v", arn, alg, err)
		}
	}
}
