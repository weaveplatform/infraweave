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

	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/awskms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/signertest"
)

// KeyEnv names the KMS key the live test signs with, by its ARN, which
// test/terraform/cloud/kek/aws creates.
const KeyEnv = "INFRAWEAVE_TEST_AWSKMS_SIGNING_KEY"

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
// signing with f; sign, when set, replaces the signature it answers. moto
// cannot stand in: it hashes a Sign message of type DIGEST again, so its
// signatures verify under no digest the backend sends.
type fake struct {
	f         signertest.Fake
	spec      types.KeySpec
	usageType types.KeyUsageType
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
				"KeyId":     in["KeyId"],
				"KeySpec":   k.spec,
				"KeyUsage":  k.usageType,
				"PublicKey": base64.StdEncoding.EncodeToString(k.f.PKIX(k.t)),
			}
		case target == "TrentService.Sign" && !k.failSign &&
			in["MessageType"] == "DIGEST" && in["SigningAlgorithm"] == "ECDSA_SHA_256":
			msg, _ := base64.StdEncoding.DecodeString(in["Message"].(string))
			sig := k.sign
			if sig == nil {
				sig = k.f.DER(k.t, msg)
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

func newFake(t *testing.T) *fake {
	return &fake{
		f:         signertest.NewFake(t),
		spec:      types.KeySpecEccNistP256,
		usageType: types.KeyUsageTypeSignVerify,
		t:         t,
	}
}

// TestUnitAWSKMS runs the contract against the fake, with the key named by
// its ARN, whose region the client signs its requests for.
func TestUnitAWSKMS(t *testing.T) {
	environment(t)
	k := newFake(t)
	s, err := awskms.New(t.Context(), arn, endpoint(k.serve()))
	if err != nil {
		t.Fatal(err)
	}
	signertest.Contract(t, s, "awskms:"+arn)
	if k.region != "eu-west-2" {
		t.Errorf("signed for %q, want the ARN's region", k.region)
	}
}

// TestUnitAWSKMSRefused checks that an alias, a key of another spec or
// usage, and KMS's refusals fail naming the reference, and that a
// signature that is not DER is signer.ErrSignature.
func TestUnitAWSKMSRefused(t *testing.T) {
	environment(t)
	for _, id := range []string{"", "alias/signing", "arn:aws:kms:eu-west-2:111122223333:alias/signing"} {
		if _, err := awskms.New(t.Context(), id); !errors.Is(err, signer.ErrRef) {
			t.Errorf("%q: %v", id, err)
		}
	}
	for name, change := range map[string]func(*fake){
		"RSA":     func(k *fake) { k.spec = types.KeySpecRsa2048 },
		"encrypt": func(k *fake) { k.usageType = types.KeyUsageTypeEncryptDecrypt },
	} {
		k := newFake(t)
		change(k)
		if _, err := awskms.New(
			t.Context(),
			"key-id",
			endpoint(k.serve()),
		); !errors.Is(
			err,
			signer.ErrKey,
		) ||
			!strings.Contains(err.Error(), "awskms:key-id") {
			t.Errorf("%s: %v", name, err)
		}
	}
	k := newFake(t)
	k.failGet = true
	if _, err := awskms.New(t.Context(), "key-id", endpoint(k.serve())); err == nil ||
		!strings.Contains(err.Error(), "awskms:key-id") {
		t.Errorf("GetPublicKey refused: %v", err)
	}

	k = newFake(t)
	url := k.serve()
	s, err := awskms.New(t.Context(), "key-id", endpoint(url))
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
// default chain.
func TestAccAWSKMSLive(t *testing.T) {
	key := os.Getenv(KeyEnv)
	if key == "" {
		t.Skipf("%s is not set: no KMS signing key to test against", KeyEnv)
	}
	s, err := awskms.New(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	signertest.Contract(t, s, "awskms:"+key)
}
