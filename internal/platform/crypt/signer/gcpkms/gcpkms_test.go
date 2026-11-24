package gcpkms_test

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/go-chi/chi/v5"
	jose "github.com/go-jose/go-jose/v4"
	"google.golang.org/api/option"
	htransport "google.golang.org/api/transport/http"

	kekgcpkms "github.com/weaveplatform/infraweave/internal/platform/crypt/kek/gcpkms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/kektest"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/gcpkms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/signertest"
)

const (
	// KeyEnv names the EC_SIGN_P256_SHA256 CryptoKeyVersion the live test
	// signs ES256 with, PKCS1KeyEnv the RSA_SIGN_PKCS1_3072_SHA256 one it
	// signs RS256 with and PSSKeyEnv the RSA_SIGN_PSS_3072_SHA256 one it
	// signs PS256 with, which test/terraform/platform/kek/google creates.
	KeyEnv      = "INFRAWEAVE_TEST_GCPKMS_SIGNING_KEY"
	PKCS1KeyEnv = "INFRAWEAVE_TEST_GCPKMS_FEDERATION_PKCS1_KEY"
	PSSKeyEnv   = "INFRAWEAVE_TEST_GCPKMS_FEDERATION_PSS_KEY"
	// RecordEnv set to 1 makes the live test write the fixture.
	RecordEnv = "INFRAWEAVE_TEST_RECORD"
	fixture   = "testdata/gcpkms.json"
	// recorded is the version the fixture names: the lab's, with its
	// project redacted.
	recorded = "projects/lab-project/locations/global/keyRings/infraweave-ci/cryptoKeys/signing/cryptoKeyVersions/1"
)

func crc(b []byte) string { return strconv.FormatInt(kekgcpkms.CRC32C(b).GetValue(), 10) }

// fake models Cloud KMS's GetPublicKey and AsymmetricSign over REST for
// one version of algorithm, signing with f under alg. change, when set,
// edits each answer.
type fake struct {
	f         signertest.Fake
	algorithm kmspb.CryptoKeyVersion_CryptoKeyVersionAlgorithm
	alg       jose.SignatureAlgorithm
	change    func(op string, reply map[string]any)
	t         *testing.T
}

func (k *fake) open(t *testing.T) (*gcpkms.Signer, error) {
	r := chi.NewRouter()
	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var op string
		reply := map[string]any{"name": recorded}
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/publicKey"):
			op = "publicKey"
			p := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: k.f.PKIX(k.t)}))
			reply["pem"], reply["pemCrc32c"], reply["algorithm"] = p, crc(
				[]byte(p),
			), int32(
				k.algorithm,
			)
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, ":asymmetricSign"):
			op = "sign"
			var in struct {
				Digest struct {
					Sha256 []byte `json:"sha256"`
				} `json:"digest"`
			}
			_ = json.NewDecoder(req.Body).Decode(&in)
			sig := k.f.DER(k.t, k.alg, in.Digest.Sha256)
			reply["signature"] = base64.StdEncoding.EncodeToString(sig)
			reply["signatureCrc32c"], reply["verifiedDigestCrc32c"] = crc(sig), true
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if k.change != nil {
			k.change(op, reply)
		}
		_ = json.NewEncoder(w).Encode(reply)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	s, err := gcpkms.New(t.Context(), recorded, k.alg, option.WithEndpoint(srv.URL),
		option.WithoutAuthentication())
	if s != nil {
		t.Cleanup(func() { _ = s.Close() })
	}
	return s, err
}

func newFake(t *testing.T) *fake {
	return &fake{
		f:         signertest.NewFake(t),
		algorithm: kmspb.CryptoKeyVersion_EC_SIGN_P256_SHA256,
		alg:       signer.ES256,
		t:         t,
	}
}

// newRSAFake is a version of algorithm, an RSA 3072 one, for alg.
func newRSAFake(t *testing.T, algorithm kmspb.CryptoKeyVersion_CryptoKeyVersionAlgorithm, alg jose.SignatureAlgorithm) *fake {
	return &fake{f: signertest.NewRSAFake(t, false), algorithm: algorithm, alg: alg, t: t}
}

// TestUnitGCPKMS runs the contract against the fake for each algorithm.
func TestUnitGCPKMS(t *testing.T) {
	for _, k := range []*fake{
		newFake(t),
		newRSAFake(t, kmspb.CryptoKeyVersion_RSA_SIGN_PKCS1_3072_SHA256, signer.RS256),
		newRSAFake(t, kmspb.CryptoKeyVersion_RSA_SIGN_PSS_3072_SHA256, signer.PS256),
	} {
		s, err := k.open(t)
		if err != nil {
			t.Fatal(err)
		}
		signertest.Contract(t, s, k.alg, "gcpkms:"+recorded)
	}
}

// TestUnitGCPKMSRecorded runs the contract against Cloud KMS's recorded
// answers, once the live test has recorded them.
func TestUnitGCPKMSRecorded(t *testing.T) {
	if _, err := os.Stat(fixture); errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s is not recorded yet: run TestAccGCPKMSLive with %s=1", fixture, RecordEnv)
	}
	srv := kektest.Replay(t, fixture, false)
	s, err := gcpkms.New(t.Context(), recorded, signer.ES256, option.WithEndpoint(srv.URL),
		option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	signertest.Contract(t, s, signer.ES256, "gcpkms:"+recorded)
}

// TestUnitGCPKMSRefused checks that a reference that is not a version,
// an algorithm Cloud KMS does not sign, a version whose algorithm signs
// another, a key its algorithm belies, and answers whose integrity fields
// do not confirm them are refused, naming the reference.
func TestUnitGCPKMSRefused(t *testing.T) {
	for _, name := range []string{"", "projects/p/locations/l/keyRings/r/cryptoKeys/k"} {
		if _, err := gcpkms.New(t.Context(), name, signer.ES256); !errors.Is(err, signer.ErrRef) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := gcpkms.New(t.Context(), recorded, jose.HS256); !errors.Is(err, signer.ErrAlgorithm) ||
		!strings.Contains(err.Error(), recorded) {
		t.Errorf("HS256: %v", err)
	}
	for name, k := range map[string]*fake{
		"PSS for ES256":   newRSAFake(t, kmspb.CryptoKeyVersion_RSA_SIGN_PSS_3072_SHA256, signer.ES256),
		"PSS for RS256":   newRSAFake(t, kmspb.CryptoKeyVersion_RSA_SIGN_PSS_3072_SHA256, signer.RS256),
		"PKCS1 for PS256": newRSAFake(t, kmspb.CryptoKeyVersion_RSA_SIGN_PKCS1_3072_SHA256, signer.PS256),
		"PKCS1 2048":      newRSAFake(t, kmspb.CryptoKeyVersion_RSA_SIGN_PKCS1_2048_SHA256, signer.RS256),
		"P-256 for RS256": {
			f: signertest.NewFake(t), algorithm: kmspb.CryptoKeyVersion_EC_SIGN_P256_SHA256,
			alg: signer.RS256, t: t,
		},
		"algorithm belied": {
			f: signertest.NewRSA2048Fake(t), algorithm: kmspb.CryptoKeyVersion_RSA_SIGN_PKCS1_3072_SHA256,
			alg: signer.RS256, t: t,
		},
	} {
		if _, err := k.open(t); !errors.Is(err, signer.ErrKey) || !strings.Contains(err.Error(), recorded) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, change := range map[string]func(map[string]any){
		"PEM changed":  func(r map[string]any) { r["pemCrc32c"] = "1" },
		"another key":  func(r map[string]any) { r["name"] = recorded + "0" },
		"not PEM":      func(r map[string]any) { r["pem"], r["pemCrc32c"] = "key", crc([]byte("key")) },
		"not on P-256": func(r map[string]any) { r["pem"], r["pemCrc32c"] = rsaPEM, crc([]byte(rsaPEM)) },
	} {
		k := newFake(t)
		k.change = func(op string, r map[string]any) { change(r) }
		_, err := k.open(t)
		if err == nil || !strings.Contains(err.Error(), recorded) ||
			(!errors.Is(err, kekgcpkms.ErrIntegrity) && !errors.Is(err, signer.ErrKey)) {
			t.Errorf("%s: %v", name, err)
		}
	}
	digest := make([]byte, signer.DigestSize)
	for name, change := range map[string]func(map[string]any){
		"digest unverified":  func(r map[string]any) { r["verifiedDigestCrc32c"] = false },
		"signature changed":  func(r map[string]any) { r["signatureCrc32c"] = "1" },
		"another key signed": func(r map[string]any) { r["name"] = recorded + "0" },
	} {
		k := newFake(t)
		k.change = func(op string, r map[string]any) {
			if op == "sign" {
				change(r)
			}
		}
		s, err := k.open(t)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Sign(t.Context(), digest); !errors.Is(err, kekgcpkms.ErrIntegrity) {
			t.Errorf("%s: %v", name, err)
		}
	}
	k := newFake(t)
	k.change = func(op string, r map[string]any) {
		if op == "sign" {
			r["signature"] = base64.StdEncoding.EncodeToString([]byte("sig"))
			r["signatureCrc32c"] = crc([]byte("sig"))
		}
	}
	s, err := k.open(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sign(t.Context(), digest); !errors.Is(err, signer.ErrSignature) {
		t.Errorf("not DER: %v", err)
	}
}

// TestUnitGCPKMSUnreachable checks that a Cloud KMS the backend cannot
// reach fails naming the reference.
func TestUnitGCPKMSUnreachable(t *testing.T) {
	if _, err := gcpkms.New(t.Context(), recorded, signer.ES256, option.WithEndpoint("http://127.0.0.1:1"),
		option.WithoutAuthentication()); err == nil || !strings.Contains(err.Error(), recorded) {
		t.Errorf("New: %v", err)
	}
}

var project = regexp.MustCompile(`^projects/([^/]+)/`)

// TestAccGCPKMSLive runs the contract against Cloud KMS with Application
// Default Credentials, ES256 on the P-256 version and RS256 and PS256 on
// the RSA ones, and with RecordEnv=1 records the ES256 fixture
// TestUnitGCPKMSRecorded replays.
func TestAccGCPKMSLive(t *testing.T) {
	for env, alg := range map[string]jose.SignatureAlgorithm{
		PKCS1KeyEnv: signer.RS256,
		PSSKeyEnv:   signer.PS256,
	} {
		t.Run(string(alg), func(t *testing.T) {
			key := os.Getenv(env)
			if key == "" {
				t.Skipf("%s is not set: no Cloud KMS key to sign %s with", env, alg)
			}
			s, err := gcpkms.New(t.Context(), key, alg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			signertest.Contract(t, s, alg, "gcpkms:"+key)
		})
	}
	key := os.Getenv(KeyEnv)
	if key == "" {
		t.Skipf("%s is not set: no Cloud KMS signing key to test against", KeyEnv)
	}
	var opts []option.ClientOption
	if os.Getenv(RecordEnv) == "1" {
		m := project.FindStringSubmatch(key)
		if m == nil {
			t.Fatalf("%s=%q is not a CryptoKeyVersion name", KeyEnv, key)
		}
		rec := kektest.Record(t, http.DefaultTransport, fixture, m[1], "lab-project")
		authed, err := htransport.NewTransport(t.Context(), rec,
			option.WithScopes("https://www.googleapis.com/auth/cloudkms"))
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, option.WithHTTPClient(&http.Client{Transport: authed}))
	}
	s, err := gcpkms.New(t.Context(), key, signer.ES256, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	signertest.Contract(t, s, signer.ES256, "gcpkms:"+key)
}

// rsaPEM is an RSA public key, which an EC_SIGN_P256_SHA256 version never
// answers.
const rsaPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA8nzoFBQ7CZa9+LvSc7Ef
7LxyYQad0slQZXBdyKcwjQyjz0+nei7neKJrh6vNEUqpPizBf/QKAHpRTMtudHyq
GKFQXQftOtk+TCxetPTUq8UonBzyJaZVN1kxsffTagI1m9dsjNWlMs5TqFoZFBhj
ADWFL4YutIag0jrLhB6e9EvEl10IafS5e0PlxnJio12c3jjvi2wavSjeuFGI8tGO
v2fF6XqOymzdjWrE72ALBwpGjcRN+kpKBm7P52HpFO3m9lynH9IMG90fhfuQgfrk
P9B6Ges9Bkqy1xtSmE3ol37XGKGzcxF8SQqGplT2F0pTZYvygmLkoKrgMVcgl7ED
XwIDAQAB
-----END PUBLIC KEY-----
`
