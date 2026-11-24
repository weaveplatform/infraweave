package app

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/vault"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/signertest"
)

// transit serves Transit's key reads: each named key is of its type, with
// version 1's public key f's.
func transit(t *testing.T, keys map[string]struct {
	typ string
	f   signertest.Fake
},
) kek.VaultConfig {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/v1/transit/keys/{name}", func(w http.ResponseWriter, req *http.Request) {
		k, ok := keys[chi.URLParam(req, "name")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		p := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: k.f.PKIX(t)})
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"type": k.typ,
			"keys": map[string]any{"1": map[string]any{"public_key": string(p)}},
		}})
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	t.Setenv(vault.TokenEnv, "token")
	v := kek.Defaults().Vault
	v.Address = srv.URL
	return v
}

// TestUnitOpenSigners checks that the local backend opens no KMS key, that
// each provider's reference reaches its backend, which names it when it
// cannot open it, that the api-token keys open for ES256 and the
// federation keys for auth.federation.algorithm, and that the vault keys
// reach secrets.kek.vault's Vault.
func TestUnitOpenSigners(t *testing.T) {
	keys, closeKMS, err := openSigners(t.Context(), accessassembly.DefaultSettings(), kek.VaultConfig{})
	if err != nil || len(keys) != 0 {
		t.Fatalf("local: %v, %v", keys, err)
	}
	closeKMS()

	v := transit(t, map[string]struct {
		typ string
		f   signertest.Fake
	}{
		"signing":    {"ecdsa-p256", signertest.NewFake(t)},
		"federation": {"rsa-3072", signertest.NewRSAFake(t, false)},
		"short":      {"rsa-2048", signertest.NewRSA2048Fake(t)},
	})
	a := accessassembly.DefaultSettings()
	a.Signing = accessassembly.SigningSettings{
		Backend: accessassembly.BackendKMS,
		KMSKeys: []accessassembly.KMSKey{{Provider: kek.Vault, Ref: "transit/signing/1"}},
	}
	a.Federation.KMSKeys = []accessassembly.KMSKey{{Provider: kek.Vault, Ref: "transit/federation/1"}}
	for _, alg := range []string{"RS256", "PS256"} {
		a.Federation.Algorithm = alg
		keys, closeKMS, err = openSigners(t.Context(), a, v)
		if err != nil || len(keys) != 2 || keys[0].Ref() != "vault:transit/signing/1" ||
			keys[0].Public().Algorithm != "ES256" || keys[1].Ref() != "vault:transit/federation/1" ||
			keys[1].Public().Algorithm != alg {
			t.Fatalf("vault for %s: %v, %v", alg, keys, err)
		}
		closeKMS()
	}

	// A federation key that cannot sign the algorithm stops the start,
	// naming it: P-256 under RS256, and RSA 2048.
	for ref, alg := range map[string]string{"transit/signing/1": "RS256", "transit/short/1": "PS256"} {
		f := a
		f.Signing.KMSKeys = nil
		f.Federation.Algorithm = alg
		f.Federation.KMSKeys = []accessassembly.KMSKey{{Provider: kek.Vault, Ref: ref}}
		if _, _, err := openSigners(t.Context(), f, v); !errors.Is(err, signer.ErrKey) ||
			!strings.Contains(err.Error(), "vault:"+ref) {
			t.Errorf("%s under %s: %v", ref, alg, err)
		}
	}
	// An api-token key that cannot sign ES256 does too.
	f := a
	f.Signing.KMSKeys = []accessassembly.KMSKey{{Provider: kek.Vault, Ref: "transit/federation/1"}}
	if _, _, err := openSigners(t.Context(), f, v); !errors.Is(err, signer.ErrKey) ||
		!strings.Contains(err.Error(), "vault:transit/federation/1") {
		t.Errorf("rsa-3072 as an api-token key: %v", err)
	}

	for provider, ref := range map[string]string{
		kek.AWSKMS:  "alias/signing",
		kek.GCPKMS:  "projects/p/locations/global/keyRings/r/cryptoKeys/k",
		kek.AzureKV: "https://kv.vault.azure.net/keys/k",
		kek.Vault:   "transit/signing",
	} {
		a.Federation.KMSKeys = []accessassembly.KMSKey{
			{Provider: kek.Vault, Ref: "transit/federation/1"},
			{Provider: provider, Ref: ref},
		}
		if _, _, err := openSigners(t.Context(), a, v); !errors.Is(err, signer.ErrRef) ||
			!strings.Contains(err.Error(), provider+":"+ref) {
			t.Errorf("%s: %v", provider, err)
		}
	}
}
