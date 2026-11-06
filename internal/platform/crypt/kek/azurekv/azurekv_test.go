package azurekv_test

import (
	"context"
	"errors"
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

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/azurekv"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/kektest"
)

const (
	// URLEnv names the key the live test wraps with, as its URL without a
	// version, which test/cloud/kek/azure creates.
	URLEnv = "INFRAWEAVE_TEST_AZUREKV_URL"
	// RecordEnv set to 1 makes the live test write the fixture.
	RecordEnv = "INFRAWEAVE_TEST_RECORD"
	fixture   = "testdata/azurekv.json"
)

// token is a credential for the recorded vault, which checks none.
type token struct{}

func (token) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "recorded", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// replayed is the provider against Key Vault's recorded answers, served
// over TLS since the SDK sends a bearer token only over https.
func replayed(t *testing.T, c kek.AzureKVConfig) *azurekv.Provider {
	t.Helper()
	srv := kektest.Replay(t, fixture, true)
	c.VaultURL = srv.URL
	p, err := azurekv.New(c, azurekv.Options{
		Credential: token{},
		Client: &azkeys.ClientOptions{
			ClientOptions: azcore.ClientOptions{Transport: srv.Client()},
			// The recorded challenge names vault.azure.net, not the test
			// server.
			DisableChallengeResourceVerification: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestUnitAzureKV runs the contract against Key Vault's recorded answers.
func TestUnitAzureKV(t *testing.T) {
	kektest.Contract(
		t,
		replayed(t, kek.AzureKVConfig{KeyName: "kek"}),
		"azurekv:https://127.0.0.1:",
	)
}

// TestUnitAzureKVMalformed checks that a wrapped key the provider did not
// write is refused before Key Vault is asked.
func TestUnitAzureKVMalformed(t *testing.T) {
	p := unreachable(t)
	for name, wrapped := range map[string][]byte{
		"empty":           nil,
		"unknown version": {2, 0},
		"short version":   {1, 9, 'a'},
	} {
		if _, err := p.Unwrap(t.Context(), 3, wrapped); !errors.Is(err, kek.ErrUnwrap) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestAccAzureKVLive runs the contract against Key Vault with azidentity's
// default chain, and with RecordEnv=1 records the fixture TestUnitAzureKV
// replays.
func TestAccAzureKVLive(t *testing.T) {
	raw := os.Getenv(URLEnv)
	if raw == "" {
		t.Skipf("%s is not set: no Key Vault key to test against", URLEnv)
	}
	u, err := url.Parse(raw)
	name, ok := strings.CutPrefix(u.Path, "/keys/")
	if err != nil || !ok || name == "" || strings.Contains(name, "/") {
		t.Fatalf("%s=%q is not https://<vault>/keys/<name>", URLEnv, raw)
	}
	c := kek.AzureKVConfig{VaultURL: "https://" + u.Host, KeyName: name}
	var o azurekv.Options
	if os.Getenv(RecordEnv) == "1" {
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
	p, err := azurekv.New(c, o)
	if err != nil {
		t.Fatal(err)
	}
	kektest.Contract(t, p, "azurekv:"+c.VaultURL+"/keys/"+name)
}

// unreachable is the provider for a vault on a port that refuses at once.
func unreachable(t *testing.T) *azurekv.Provider {
	t.Helper()
	c := kek.AzureKVConfig{VaultURL: "https://127.0.0.1:1", KeyName: "kek"}
	p, err := azurekv.New(c, azurekv.Options{
		Credential: token{},
		Client: &azkeys.ClientOptions{ClientOptions: azcore.ClientOptions{
			Retry: policy.RetryOptions{MaxRetries: -1},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestUnitAzureKVUnreachable checks that a vault the provider cannot reach
// fails with the provider and key named, not as a wrong KEK.
func TestUnitAzureKVUnreachable(t *testing.T) {
	p := unreachable(t)
	for name, call := range map[string]func() error{
		"wrap":   func() error { _, err := p.Wrap(t.Context(), 1, kektest.Key()); return err },
		"unwrap": func() error { _, err := p.Unwrap(t.Context(), 1, []byte{1, 1, 'v', 0}); return err },
	} {
		if err := call(); err == nil || errors.Is(err, kek.ErrUnwrap) ||
			!strings.Contains(err.Error(), "azurekv:https://127.0.0.1:1/keys/kek") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := azurekv.New(
		kek.AzureKVConfig{VaultURL: "https://kv.vault.azure.net"},
		azurekv.Options{},
	); !errors.Is(
		err,
		kek.ErrConfig,
	) {
		t.Errorf("no key: %v", err)
	}
}

// TestUnitAzureKVUnversioned checks that a wrapKey answer whose key ID
// names no version is refused, since nothing could unwrap what it wrapped.
func TestUnitAzureKVUnversioned(t *testing.T) {
	r := chi.NewRouter()
	r.Post("/keys/kek/wrapkey", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(
			[]byte(`{"kid":"https://lab-vault.vault.azure.net/keys/kek","value":"AAEC"}`),
		)
	})
	srv := httptest.NewTLSServer(r)
	t.Cleanup(srv.Close)
	p, err := azurekv.New(kek.AzureKVConfig{VaultURL: srv.URL, KeyName: "kek"}, azurekv.Options{
		Credential: token{},
		Client: &azkeys.ClientOptions{
			ClientOptions: azcore.ClientOptions{Transport: srv.Client()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wrap(t.Context(), 1, kektest.Key()); !errors.Is(err, azurekv.ErrKeyID) {
		t.Errorf("Wrap: %v", err)
	}
	// azidentity's default chain needs no credential until a call.
	if _, err := azurekv.New(
		kek.AzureKVConfig{VaultURL: srv.URL, KeyName: "kek"},
		azurekv.Options{},
	); err != nil {
		t.Errorf("the default chain: %v", err)
	}
}
