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

// TestUnitOpenSigners checks that the local backend opens no KMS key, that
// each provider's reference reaches its backend, which names it when it
// cannot open it, and that the vault keys reach secrets.kek.vault's Vault.
func TestUnitOpenSigners(t *testing.T) {
	keys, closeKMS, err := openSigners(
		t.Context(),
		accessassembly.DefaultSettings().Signing,
		kek.VaultConfig{},
	)
	if err != nil || len(keys) != 0 {
		t.Fatalf("local: %v, %v", keys, err)
	}
	closeKMS()

	f := signertest.NewFake(t)
	r := chi.NewRouter()
	r.Get("/v1/transit/keys/signing", func(w http.ResponseWriter, _ *http.Request) {
		p := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: f.PKIX(t)})
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"type": "ecdsa-p256",
			"keys": map[string]any{"1": map[string]any{"public_key": string(p)}},
		}})
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	t.Setenv(vault.TokenEnv, "token")
	v := kek.Defaults().Vault
	v.Address = srv.URL
	s := accessassembly.SigningSettings{
		Backend: accessassembly.BackendKMS,
		KMSKeys: []accessassembly.KMSKey{{Provider: kek.Vault, Ref: "transit/signing/1"}},
	}
	keys, closeKMS, err = openSigners(t.Context(), s, v)
	if err != nil || len(keys) != 1 || keys[0].Ref() != "vault:transit/signing/1" {
		t.Fatalf("vault: %v, %v", keys, err)
	}
	closeKMS()

	for provider, ref := range map[string]string{
		kek.AWSKMS:  "alias/signing",
		kek.GCPKMS:  "projects/p/locations/global/keyRings/r/cryptoKeys/k",
		kek.AzureKV: "https://kv.vault.azure.net/keys/k",
		kek.Vault:   "transit/signing",
	} {
		s.KMSKeys = []accessassembly.KMSKey{
			{Provider: kek.Vault, Ref: "transit/signing/1"},
			{Provider: provider, Ref: ref},
		}
		if _, _, err := openSigners(t.Context(), s, v); !errors.Is(err, signer.ErrRef) ||
			!strings.Contains(err.Error(), provider+":"+ref) {
			t.Errorf("%s: %v", provider, err)
		}
	}
}
