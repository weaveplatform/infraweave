package vault_test

import (
	"crypto/ecdsa"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/kektest"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/signertest"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/vault"
)

// TestAccVault runs the contract against Transit with an ecdsa-p256 key,
// and checks that the version a reference names keeps signing after the
// key rotates, and that a key of another type or a version it lacks is
// refused.
func TestAccVault(t *testing.T) {
	l := kektest.StartVault(t)
	l.Do(http.MethodPost, "transit/keys/signing", map[string]any{"type": "ecdsa-p256"})
	t.Setenv("VAULT_TOKEN", kektest.VaultRoot)
	c := l.Config()
	s, err := vault.New(t.Context(), c, "transit/signing/1", vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	signertest.Contract(t, s, "vault:transit/signing/1")

	l.Do(http.MethodPost, "transit/keys/signing/rotate", map[string]any{})
	again, err := vault.New(t.Context(), c, "transit/signing/1", vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if pub, ok := again.Public().Key.(*ecdsa.PublicKey); !ok || !pub.Equal(s.Public().Key) {
		t.Error("version 1's public key changed when the key rotated")
	}
	signertest.Contract(t, again, "vault:transit/signing/1")

	for ref, want := range map[string]error{
		"transit/kek/1":     signer.ErrKey,
		"transit/signing/3": signer.ErrKey,
	} {
		if _, err := vault.New(t.Context(), c, ref, vault.Options{}); !errors.Is(err, want) ||
			!strings.Contains(err.Error(), "vault:"+ref) {
			t.Errorf("%s: %v", ref, err)
		}
	}
	if _, err := vault.New(t.Context(), c, "transit/missing/1", vault.Options{}); err == nil ||
		!strings.Contains(err.Error(), "vault:transit/missing/1") {
		t.Errorf("a missing key: %v", err)
	}
}

// TestUnitVaultRefs checks that a reference that does not name a mount, a
// key and a positive version is signer.ErrRef, before Vault is asked, and
// that settings that do not reach Vault are kek.ErrConfig.
func TestUnitVaultRefs(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "token")
	c := kek.Defaults().Vault
	c.Address = "http://127.0.0.1:1"
	for _, ref := range []string{
		"", "signing", "transit/signing", "transit/signing/0", "transit/signing/x",
		"/signing/1", "transit//1",
	} {
		if _, err := vault.New(
			t.Context(),
			c,
			ref,
			vault.Options{},
		); !errors.Is(
			err,
			signer.ErrRef,
		) {
			t.Errorf("%q: %v", ref, err)
		}
	}
	if _, err := vault.New(
		t.Context(),
		kek.VaultConfig{},
		"transit/signing/1",
		vault.Options{},
	); !errors.Is(
		err,
		kek.ErrConfig,
	) {
		t.Errorf("no address: %v", err)
	}
}

// TestUnitVaultSignatures checks that an answer that is not a signature
// of the version, in base64url, of r || s, is signer.ErrSignature.
func TestUnitVaultSignatures(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "token")
	f := signertest.NewFake(t)
	var signature string
	r := chi.NewRouter()
	r.Get("/v1/transit/keys/signing", func(w http.ResponseWriter, _ *http.Request) {
		p := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: f.PKIX(t)})
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"type": "ecdsa-p256",
			"keys": map[string]any{"1": map[string]any{"public_key": string(p)}},
		}})
	})
	r.Post("/v1/transit/sign/signing", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).
			Encode(map[string]any{"data": map[string]any{"signature": signature}})
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	c := kek.Defaults().Vault
	c.Address = srv.URL
	s, err := vault.New(t.Context(), c, "transit/signing/1", vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for name, sig := range map[string]string{
		"another version": "vault:v2:" + strings.Repeat("A", 86),
		"not base64url":   "vault:v1:!!",
		"short":           "vault:v1:AAAA",
	} {
		signature = sig
		if _, err := s.Sign(
			t.Context(),
			make([]byte, signer.DigestSize),
		); !errors.Is(
			err,
			signer.ErrSignature,
		) {
			t.Errorf("%s: %v", name, err)
		}
	}
	srv.Close()
	if _, err := s.Sign(t.Context(), make([]byte, signer.DigestSize)); err == nil ||
		errors.Is(
			err,
			signer.ErrSignature,
		) || !strings.Contains(err.Error(), "vault:transit/signing/1") {
		t.Errorf("Vault gone: %v", err)
	}
}
