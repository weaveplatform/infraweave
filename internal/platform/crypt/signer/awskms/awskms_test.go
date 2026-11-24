package awskms_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/go-chi/chi/v5"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/awskms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/signertest"
)

// KeyEnv names the ECC_NIST_P256 KMS key the live test signs ES256 with,
// and RSAKeyEnv the RSA_3072 key it signs RS256 and PS256 with, each by
// its ARN, which test/terraform/platform/kek/aws creates.
const (
	KeyEnv    = "INFRAWEAVE_TEST_AWSKMS_SIGNING_KEY"
	RSAKeyEnv = "INFRAWEAVE_TEST_AWSKMS_FEDERATION_KEY"
)

// arn is a key ARN in AWS's documentation account.
const arn = "arn:aws:kms:eu-west-2:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"

// The test credentials, as long as a real secret, since FIPS 140-only
// mode refuses a shorter HMAC key.
const accessKey, secretKey = "infraweave-test", "infraweave-test-secret-0123456789abcdefghi" //nolint:gosec // a test credential

func environment(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", accessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", secretKey)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_REGION", "us-east-1")
}

func endpoint(url string) func(*kms.Options) {
	return func(o *kms.Options) {
		o.BaseEndpoint = aws.String(url)
		o.RetryMaxAttempts = 1
	}
}

// fake models KMS's GetPublicKey and Sign for one key of spec and usage,
// signing with f under the signing algorithm algorithm names; sign, when
// set, replaces the signature it answers. The contract needs it beside
// moto: moto hashes a Sign message of type DIGEST again, so its
// signatures verify under no digest the backend sends.
type fake struct {
	f         signertest.Fake
	spec      types.KeySpec
	usageType types.KeyUsageType
	alg       jose.SignatureAlgorithm
	sign      []byte
	// region is the region the last request was signed for.
	region            string
	failGet, failSign bool
	t                 *testing.T
}

func (k *fake) serve() string {
	r := chi.NewRouter()
	r.Post("/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		k.region = strings.Split(strings.Split(req.Header.Get("Authorization"), "/")[2], "/")[0]
		var in map[string]any
		_ = json.NewDecoder(req.Body).Decode(&in)
		var out map[string]any
		switch target := req.Header.Get("X-Amz-Target"); {
		case target == "TrentService.GetPublicKey" && !k.failGet:
			out = map[string]any{
				"KeyId":             in["KeyId"],
				"KeySpec":           k.spec,
				"KeyUsage":          k.usageType,
				"PublicKey":         base64.StdEncoding.EncodeToString(k.f.PKIX(k.t)),
				"SigningAlgorithms": algorithms[k.spec],
			}
		case target == "TrentService.Sign" && !k.failSign &&
			in["MessageType"] == "DIGEST" && in["SigningAlgorithm"] == signing[k.alg]:
			msg, _ := base64.StdEncoding.DecodeString(in["Message"].(string))
			sig := k.sign
			if sig == nil {
				sig = k.f.DER(k.t, k.alg, msg)
			}
			out = map[string]any{
				"KeyId":     in["KeyId"],
				"Signature": base64.StdEncoding.EncodeToString(sig),
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"AccessDeniedException","message":"refused"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	srv := httptest.NewServer(r)
	k.t.Cleanup(srv.Close)
	return srv.URL
}

// signing are the KMS signing algorithms the backend asks for.
var signing = map[jose.SignatureAlgorithm]string{
	signer.ES256: "ECDSA_SHA_256",
	signer.RS256: "RSASSA_PKCS1_V1_5_SHA_256",
	signer.PS256: "RSASSA_PSS_SHA_256",
}

// algorithms are the signing algorithms KMS lists for a key of each spec.
var algorithms = map[types.KeySpec][]string{
	types.KeySpecEccNistP256: {"ECDSA_SHA_256"},
	types.KeySpecRsa2048: {
		"RSASSA_PKCS1_V1_5_SHA_256", "RSASSA_PSS_SHA_256",
	},
	types.KeySpecRsa3072: {
		"RSASSA_PKCS1_V1_5_SHA_256", "RSASSA_PKCS1_V1_5_SHA_384", "RSASSA_PSS_SHA_256", "RSASSA_PSS_SHA_384",
	},
}

func newFake(t *testing.T) *fake {
	return &fake{
		f:         signertest.NewFake(t),
		spec:      types.KeySpecEccNistP256,
		usageType: types.KeyUsageTypeSignVerify,
		alg:       signer.ES256,
		t:         t,
	}
}

// newRSAFake is an RSA_3072 key for alg.
func newRSAFake(t *testing.T, alg jose.SignatureAlgorithm) *fake {
	k := newFake(t)
	k.f, k.spec, k.alg = signertest.NewRSAFake(t, false), types.KeySpecRsa3072, alg
	return k
}

// TestUnitAWSKMS runs the contract against the fake for each algorithm,
// with the key named by its ARN, whose region the client signs its
// requests for.
func TestUnitAWSKMS(t *testing.T) {
	environment(t)
	for _, k := range []*fake{newFake(t), newRSAFake(t, signer.RS256), newRSAFake(t, signer.PS256)} {
		s, err := awskms.New(t.Context(), arn, k.alg, endpoint(k.serve()))
		if err != nil {
			t.Fatal(err)
		}
		signertest.Contract(t, s, k.alg, "awskms:"+arn)
		if k.region != "eu-west-2" {
			t.Errorf("%s: signed for %q, want the ARN's region", k.alg, k.region)
		}
	}
}

// TestUnitAWSKMSRefused checks that an alias, an algorithm KMS does not
// sign, a key of a spec or usage that cannot sign the algorithm, a key
// whose public half its spec belies, and KMS's refusals fail naming the
// reference, and that a signature that is not DER is
// signer.ErrSignature.
func TestUnitAWSKMSRefused(t *testing.T) {
	environment(t)
	for _, id := range []string{"", "alias/signing", "arn:aws:kms:eu-west-2:111122223333:alias/signing"} {
		if _, err := awskms.New(t.Context(), id, signer.ES256); !errors.Is(err, signer.ErrRef) {
			t.Errorf("%q: %v", id, err)
		}
	}
	if _, err := awskms.New(t.Context(), "key-id", jose.HS256); !errors.Is(err, signer.ErrAlgorithm) ||
		!strings.Contains(err.Error(), "awskms:key-id") {
		t.Errorf("HS256: %v", err)
	}
	for name, change := range map[string]func(*fake){
		"RSA for ES256":   func(k *fake) { k.spec = types.KeySpecRsa3072 },
		"encrypt":         func(k *fake) { k.usageType = types.KeyUsageTypeEncryptDecrypt },
		"P-256 for RS256": func(k *fake) { k.alg = signer.RS256 },
		"RSA 2048":        func(k *fake) { k.spec, k.alg = types.KeySpecRsa2048, signer.PS256 },
		"spec belied": func(k *fake) {
			k.spec, k.alg, k.f = types.KeySpecRsa3072, signer.RS256, signertest.NewRSA2048Fake(t)
		},
	} {
		k := newFake(t)
		change(k)
		if _, err := awskms.New(t.Context(), "key-id", k.alg, endpoint(k.serve())); !errors.Is(err, signer.ErrKey) ||
			!strings.Contains(err.Error(), "awskms:key-id") {
			t.Errorf("%s: %v", name, err)
		}
	}
	k := newFake(t)
	k.failGet = true
	if _, err := awskms.New(t.Context(), "key-id", signer.ES256, endpoint(k.serve())); err == nil ||
		!strings.Contains(err.Error(), "awskms:key-id") {
		t.Errorf("GetPublicKey refused: %v", err)
	}

	k = newFake(t)
	url := k.serve()
	s, err := awskms.New(t.Context(), "key-id", signer.ES256, endpoint(url))
	if err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, signer.DigestSize)
	k.sign = []byte("not DER")
	if _, err := s.Sign(t.Context(), digest); !errors.Is(err, signer.ErrSignature) {
		t.Errorf("not DER: %v", err)
	}
	k.failSign = true
	if _, err := s.Sign(t.Context(), digest); err == nil || errors.Is(err, signer.ErrSignature) ||
		!strings.Contains(err.Error(), "awskms:key-id") {
		t.Errorf("Sign refused: %v", err)
	}
}

// TestAccAWSKMSLive runs the contract against AWS KMS with the SDK's
// default chain: ES256 on the P-256 key, and RS256 and PS256 on the RSA
// key.
func TestAccAWSKMSLive(t *testing.T) {
	for env, algs := range map[string][]jose.SignatureAlgorithm{
		KeyEnv:    {signer.ES256},
		RSAKeyEnv: {signer.RS256, signer.PS256},
	} {
		key := os.Getenv(env)
		for _, alg := range algs {
			t.Run(string(alg), func(t *testing.T) {
				if key == "" {
					t.Skipf("%s is not set: no KMS key to sign %s with", env, alg)
				}
				s, err := awskms.New(t.Context(), key, alg)
				if err != nil {
					t.Fatal(err)
				}
				signertest.Contract(t, s, alg, "awskms:"+key)
			})
		}
	}
}
