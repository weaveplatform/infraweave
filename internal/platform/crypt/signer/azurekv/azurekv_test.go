package azurekv_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/go-chi/chi/v5"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/kektest"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/azurekv"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/signertest"
)

const (
	// KeyEnv names the EC P-256 key version the live test signs ES256
	// with, and RSAKeyEnv the RSA 3072 one it signs RS256 and PS256 with,
	// each by its key ID, https://<vault>/keys/<name>/<version>, which
	// test/terraform/platform/kek/azure creates.
	KeyEnv    = "INFRAWEAVE_TEST_AZUREKV_SIGNING_KEY"
	RSAKeyEnv = "INFRAWEAVE_TEST_AZUREKV_FEDERATION_KEY"
	// RecordEnv set to 1 makes the live test write the fixture.
	RecordEnv = "INFRAWEAVE_TEST_RECORD"
	fixture   = "testdata/azurekv.json"
	// challenge is Key Vault's answer to a request without a token.
	challenge = `Bearer authorization="https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000", resource="https://vault.azure.net"`
)

// token is a credential for a vault that checks none.
type token struct{}

func (token) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// options reach srv, whose challenge names vault.azure.net, not srv.
func options(srv *httptest.Server) azurekv.Options {
	return azurekv.Options{
		Credential: token{},
		Client: &azkeys.ClientOptions{
			ClientOptions:                        azcore.ClientOptions{Transport: srv.Client()},
			DisableChallengeResourceVerification: true,
		},
	}
}

// fake models Key Vault's getKey and sign for one key version, signing
// with f under alg. key and sign, when set, edit the answers.
type fake struct {
	f    signertest.Fake
	alg  jose.SignatureAlgorithm
	key  func(map[string]any)
	sign func(map[string]any)
	t    *testing.T
}

func (k *fake) open(t *testing.T, ref string) (*azurekv.Signer, error) {
	t.Helper()
	r := chi.NewRouter()
	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		kid := "https://" + req.Host + strings.TrimSuffix(req.URL.Path, "/sign")
		var reply map[string]any
		switch {
		case req.Method == http.MethodGet && strings.Count(req.URL.Path, "/") == 3:
			b64 := base64.RawURLEncoding.EncodeToString
			key := map[string]any{"kid": kid, "key_ops": []string{"sign", "verify"}}
			if pub := k.f.RSA(); pub != nil {
				key["kty"], key["n"] = "RSA", b64(pub.N.Bytes())
				key["e"] = b64(big.NewInt(int64(pub.E)).Bytes())
			} else {
				point, err := k.f.ECDSA().Bytes()
				if err != nil {
					k.t.Error(err)
				}
				key["kty"], key["crv"] = "EC", "P-256"
				key["x"], key["y"] = b64(point[1:33]), b64(point[33:])
			}
			if k.key != nil {
				k.key(key)
			}
			reply = map[string]any{"key": key}
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/sign"):
			var in struct {
				Alg   string `json:"alg"`
				Value string `json:"value"`
			}
			_ = json.NewDecoder(req.Body).Decode(&in)
			digest, _ := base64.RawURLEncoding.DecodeString(in.Value)
			if in.Alg != string(k.alg) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			reply = map[string]any{
				"kid":   kid,
				"value": base64.RawURLEncoding.EncodeToString(k.f.Raw(k.t, k.alg, digest)),
			}
			if k.sign != nil {
				k.sign(reply)
			}
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":"refused"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(reply)
	})
	srv := httptest.NewTLSServer(r)
	t.Cleanup(srv.Close)
	return azurekv.New(t.Context(), strings.ReplaceAll(ref, "{vault}", srv.URL), k.alg, options(srv))
}

func newFake(t *testing.T) *fake {
	return &fake{f: signertest.NewFake(t), alg: signer.ES256, t: t}
}

func newRSAFake(t *testing.T, alg jose.SignatureAlgorithm) *fake {
	return &fake{f: signertest.NewRSAFake(t, false), alg: alg, t: t}
}

const ref = "{vault}/keys/signing/0123456789abcdef"

// TestUnitAzureKV runs the contract against the fake for each algorithm.
func TestUnitAzureKV(t *testing.T) {
	for _, k := range []*fake{newFake(t), newRSAFake(t, signer.RS256), newRSAFake(t, signer.PS256)} {
		s, err := k.open(t, ref)
		if err != nil {
			t.Fatal(err)
		}
		signertest.Contract(t, s, k.alg, "azurekv:https://127.0.0.1:")
	}
}

// TestUnitAzureKVRecorded runs the contract against Key Vault's recorded
// answers, once the live test has recorded them, with the key version
// they name.
func TestUnitAzureKVRecorded(t *testing.T) {
	raw, err := os.ReadFile(fixture)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s is not recorded yet: run TestAccAzureKVLive with %s=1", fixture, RecordEnv)
	}
	var exchanges []kektest.Exchange
	if err := json.Unmarshal(raw, &exchanges); err != nil || len(exchanges) == 0 {
		t.Fatalf("%s: %v", fixture, err)
	}
	path, _, _ := strings.Cut(exchanges[0].Path, "?")
	srv := kektest.Replay(t, fixture, true)
	s, err := azurekv.New(t.Context(), srv.URL+path, signer.ES256, options(srv))
	if err != nil {
		t.Fatal(err)
	}
	signertest.Contract(t, s, signer.ES256, "azurekv:"+srv.URL+path)
}

// TestUnitAzureKVRefused checks that a reference without a version, an
// algorithm Key Vault does not sign, a key that cannot sign the algorithm,
// and a signature of the wrong length are refused, naming the reference.
func TestUnitAzureKVRefused(t *testing.T) {
	for _, r := range []string{
		"", "http://v.vault.azure.net/keys/k/1", "https://v.vault.azure.net/keys/k",
		"https://v.vault.azure.net/secrets/k/1", "https://v.vault.azure.net/keys/k/1?x=1",
		"https://v.vault.azure.net/keys//1", ":",
	} {
		if _, err := azurekv.New(
			t.Context(),
			r,
			signer.ES256,
			azurekv.Options{Credential: token{}},
		); !errors.Is(
			err,
			signer.ErrRef,
		) {
			t.Errorf("%q: %v", r, err)
		}
	}
	if _, err := azurekv.New(t.Context(), "https://v.vault.azure.net/keys/k/1", jose.HS256,
		azurekv.Options{Credential: token{}}); !errors.Is(err, signer.ErrAlgorithm) {
		t.Errorf("HS256: %v", err)
	}
	for name, tc := range map[string]struct {
		k      *fake
		change func(map[string]any)
	}{
		"RSA for ES256": {newFake(t), func(k map[string]any) { k["kty"] = "RSA" }},
		"P-384":         {newFake(t), func(k map[string]any) { k["crv"] = "P-384" }},
		"oct":           {newFake(t), func(k map[string]any) { k["kty"] = "oct" }},
		"off curve":     {newFake(t), func(k map[string]any) { k["x"] = base64.RawURLEncoding.EncodeToString(make([]byte, 32)) }},
		"EC for RS256":  {&fake{f: signertest.NewFake(t), alg: signer.RS256, t: t}, nil},
		"RSA 2048":      {&fake{f: signertest.NewRSA2048Fake(t), alg: signer.PS256, t: t}, nil},
		"no exponent":   {newRSAFake(t, signer.RS256), func(k map[string]any) { delete(k, "e") }},
		"wide exponent": {newRSAFake(t, signer.RS256), func(k map[string]any) { k["e"] = "AQAAAAAB" }},
		"RSA-HSM 2048":  {&fake{f: signertest.NewRSA2048Fake(t), alg: signer.RS256, t: t}, func(k map[string]any) { k["kty"] = "RSA-HSM" }},
	} {
		k := tc.k
		k.key = tc.change
		if _, err := k.open(
			t,
			ref,
		); !errors.Is(err, signer.ErrKey) ||
			!strings.Contains(err.Error(), "/keys/signing/") {
			t.Errorf("%s: %v", name, err)
		}
	}
	k := newFake(t)
	k.sign = func(r map[string]any) { r["value"] = base64.RawURLEncoding.EncodeToString([]byte("short")) }
	s, err := k.open(t, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sign(
		t.Context(),
		make([]byte, signer.DigestSize),
	); !errors.Is(
		err,
		signer.ErrSignature,
	) {
		t.Errorf("short signature: %v", err)
	}
}

// TestUnitAzureKVUnreachable checks that a vault the backend cannot reach
// fails naming the reference.
func TestUnitAzureKVUnreachable(t *testing.T) {
	_, err := azurekv.New(t.Context(), "https://127.0.0.1:1/keys/signing/1", signer.ES256, azurekv.Options{
		Credential: token{},
		Client: &azkeys.ClientOptions{ClientOptions: azcore.ClientOptions{
			Retry: policy.RetryOptions{MaxRetries: -1},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "azurekv:https://127.0.0.1:1/keys/signing/1") {
		t.Errorf("New: %v", err)
	}
}

// TestAccAzureKVLive runs the contract against Key Vault with azidentity's
// default chain, and with RecordEnv=1 records the fixture
// TestUnitAzureKVRecorded replays.
func TestAccAzureKVLive(t *testing.T) {
	key := os.Getenv(KeyEnv)
	if key == "" {
		t.Skipf("%s is not set: no Key Vault signing key to test against", KeyEnv)
	}
	var o azurekv.Options
	if os.Getenv(RecordEnv) == "1" {
		u, err := url.Parse(key)
		if err != nil {
			t.Fatal(err)
		}
		rec := kektest.Record(
			t,
			http.DefaultTransport,
			fixture,
			u.Host,
			"lab-vault.vault.azure.net",
		)
		o.Client = &azkeys.ClientOptions{
			ClientOptions: azcore.ClientOptions{Transport: &http.Client{Transport: rec}},
		}
	}
	s, err := azurekv.New(t.Context(), key, signer.ES256, o)
	if err != nil {
		t.Fatal(err)
	}
	signertest.Contract(t, s, signer.ES256, "azurekv:"+key)
}

// TestAccAzureKVLiveRSA runs the contract against Key Vault's RSA key with
// azidentity's default chain, under RS256 and PS256.
func TestAccAzureKVLiveRSA(t *testing.T) {
	key := os.Getenv(RSAKeyEnv)
	for _, alg := range []jose.SignatureAlgorithm{signer.RS256, signer.PS256} {
		t.Run(string(alg), func(t *testing.T) {
			if key == "" {
				t.Skipf("%s is not set: no Key Vault RSA key to sign %s with", RSAKeyEnv, alg)
			}
			s, err := azurekv.New(t.Context(), key, alg, azurekv.Options{})
			if err != nil {
				t.Fatal(err)
			}
			signertest.Contract(t, s, alg, "azurekv:"+key)
		})
	}
}
