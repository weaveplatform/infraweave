package vault_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/kektest"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/vault"
)

const root = kektest.VaultRoot

// lab adds two AppRoles to kektest's Vault: infraweave, which may encrypt
// and decrypt with kek, and encrypt-only.
type lab struct {
	kektest.VaultLab
	t *testing.T
}

func start(t *testing.T) lab {
	t.Helper()
	l := lab{VaultLab: kektest.StartVault(t), t: t}
	l.Do(http.MethodPost, "sys/auth/approle", map[string]any{"type": "approle"})
	for name, ops := range map[string][]string{
		"infraweave":   {"encrypt", "decrypt"},
		"encrypt-only": {"encrypt"},
	} {
		var policy strings.Builder
		for _, op := range ops {
			policy.WriteString(`path "transit/` + op + `/kek" { capabilities = ["update"] }` + "\n")
		}
		l.Do(http.MethodPut, "sys/policies/acl/"+name, map[string]any{"policy": policy.String()})
		l.Do(
			http.MethodPost,
			"auth/approle/role/"+name,
			map[string]any{"token_policies": name, "token_ttl": "1h"},
		)
	}
	return l
}

// appRole returns the settings that log in as role, its secret ID in a
// new file.
func (l lab) appRole(role string) kek.VaultConfig {
	l.t.Helper()
	c := l.Config()
	file := filepath.Join(l.t.TempDir(), "secret-id")
	secret := l.Do(http.MethodPost, "auth/approle/role/"+role+"/secret-id", map[string]any{})["secret_id"]
	if err := os.WriteFile(file, []byte(secret.(string)+"\n"), 0o600); err != nil {
		l.t.Fatal(err)
	}
	c.Auth = kek.VaultAuth{
		Method:       kek.VaultAppRole,
		RoleID:       l.Do(http.MethodGet, "auth/approle/role/"+role+"/role-id", nil)["role_id"].(string),
		SecretIDFile: file,
	}
	return c
}

// logins counts the logins a provider sends.
type logins struct {
	n    atomic.Int32
	base http.RoundTripper
}

func (c *logins) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/login") {
		c.n.Add(1)
	}
	return c.base.RoundTrip(req)
}

func TestAccVaultToken(t *testing.T) {
	l := start(t)
	t.Setenv(vault.TokenEnv, root)
	p, err := vault.New(l.Config(), vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	kektest.Contract(t, p, "vault:"+l.Addr+"/transit/kek")

	c := l.Config()
	c.Auth.TokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(c.Auth.TokenFile, []byte("not-a-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused, err := vault.New(c, vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// A static token is never renewed, so a refusal is final.
	_, err = refused.Wrap(t.Context(), 1, kektest.Key())
	if err == nil || errors.Is(err, kek.ErrUnwrap) || !strings.Contains(err.Error(), "Forbidden") ||
		!strings.Contains(err.Error(), refused.Ref()) {
		t.Errorf("a refused token: %v", err)
	}
}

func TestAccVaultAppRole(t *testing.T) {
	l := start(t)
	now := time.Now()
	count := &logins{base: http.DefaultTransport}
	p, err := vault.New(l.appRole("infraweave"), vault.Options{
		Client: &http.Client{Transport: count},
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	kektest.Contract(t, p, "vault:")
	if n := count.n.Load(); n != 1 {
		t.Errorf("%d logins for one token's lease", n)
	}

	// Halfway through the hour's lease the provider logs in again.
	now = now.Add(31 * time.Minute)
	if _, err := p.Wrap(t.Context(), 1, kektest.Key()); err != nil {
		t.Fatal(err)
	}
	if n := count.n.Load(); n != 2 {
		t.Errorf("%d logins after half the lease", n)
	}

	// A token revoked early is refused once, then replaced.
	l.Do(http.MethodPut, "sys/leases/revoke-prefix/auth/approle/login", map[string]any{})
	if _, err := p.Wrap(t.Context(), 1, kektest.Key()); err != nil {
		t.Fatalf("after revocation: %v", err)
	}
	if n := count.n.Load(); n != 3 {
		t.Errorf("%d logins after revocation", n)
	}
}

// TestAccVaultDenied checks that a token without decrypt permission fails
// Unwrap with Vault's refusal and the provider's name, not ErrUnwrap: the
// KEK may be right, and the operator must fix the policy instead.
func TestAccVaultDenied(t *testing.T) {
	l := start(t)
	p, err := vault.New(l.appRole("encrypt-only"), vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := p.Wrap(t.Context(), 1, kektest.Key())
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Unwrap(t.Context(), 1, wrapped)
	if err == nil || errors.Is(err, kek.ErrUnwrap) ||
		!strings.Contains(
			err.Error(),
			"permission denied",
		) || !strings.Contains(err.Error(), p.Ref()) {
		t.Errorf("denied decrypt: %v", err)
	}

	c := l.appRole("infraweave")
	c.Auth.RoleID = "not-a-role"
	wrong, err := vault.New(c, vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Wrap(
		t.Context(),
		1,
		kektest.Key(),
	); err == nil ||
		!strings.Contains(err.Error(), "approle login") {
		t.Errorf("a wrong role ID: %v", err)
	}
}

func TestUnitVaultUnreachable(t *testing.T) {
	t.Setenv(vault.TokenEnv, root)
	c := kek.Defaults().Vault
	c.Address, c.KeyName = "http://127.0.0.1:1", "kek"
	p, err := vault.New(c, vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Unwrap(t.Context(), 7, []byte("vault:v1:x")); err == nil ||
		errors.Is(
			err,
			kek.ErrUnwrap,
		) || !strings.Contains(err.Error(), "vault:http://127.0.0.1:1/transit/kek") {
		t.Errorf("unreachable: %v", err)
	}
}

// kubernetes models Vault's kubernetes login and a Transit key whose
// answers the test sets.
func kubernetes(t *testing.T, plaintext string) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/v1/auth/kubernetes/login", func(w http.ResponseWriter, req *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(req.Body).Decode(&in)
		if in["role"] != "infraweave" || in["jwt"] != "service-account-token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":["invalid role or JWT"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"auth":{"client_token":"k8s-token","lease_duration":0}}`))
	})
	r.Post("/v1/transit/{op}/kek", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Vault-Token") != "k8s-token" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("not JSON"))
			return
		}
		_, _ = w.Write(
			[]byte(`{"data":{"ciphertext":"vault:v1:abc","plaintext":"` + plaintext + `"}}`),
		)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestUnitVaultKubernetes(t *testing.T) {
	jwt := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(jwt, []byte("service-account-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := kek.Defaults().Vault
	c.Address, c.KeyName = kubernetes(t, "AAEC").URL, "kek"
	c.Auth = kek.VaultAuth{Method: kek.VaultKubernetes, Role: "infraweave", JWTFile: jwt}
	p, err := vault.New(c, vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if wrapped, err := p.Wrap(
		t.Context(),
		1,
		kektest.Key(),
	); err != nil ||
		string(wrapped) != "vault:v1:abc" {
		t.Fatalf("Wrap = %q, %v", wrapped, err)
	}
	// A login with no lease is kept.
	if key, err := p.Unwrap(
		t.Context(),
		1,
		[]byte("vault:v1:abc"),
	); err != nil ||
		!bytes.Equal(key, []byte{0, 1, 2}) {
		t.Errorf("Unwrap = %x, %v", key, err)
	}

	c.Address = kubernetes(t, "not base64!").URL
	if p, err = vault.New(c, vault.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Unwrap(t.Context(), 1, []byte("vault:v1:abc")); err == nil {
		t.Error("a plaintext that is not base64 unwrapped")
	}

	c.Auth.Role = "another"
	if p, err = vault.New(c, vault.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wrap(t.Context(), 1, kektest.Key()); err == nil ||
		!strings.Contains(err.Error(), "kubernetes login: Bad Request: invalid role or JWT") {
		t.Errorf("a refused login: %v", err)
	}

	c.Auth.JWTFile = filepath.Join(t.TempDir(), "missing")
	if p, err = vault.New(c, vault.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wrap(t.Context(), 1, kektest.Key()); err == nil ||
		!strings.Contains(err.Error(), "secrets.kek.vault.auth.jwt_file") {
		t.Errorf("no service account token: %v", err)
	}
}

func TestUnitVaultConfig(t *testing.T) {
	t.Setenv(vault.TokenEnv, "")
	c := kek.Defaults().Vault
	c.Address, c.KeyName = "http://vault:8200", "kek"
	if _, err := vault.New(c, vault.Options{}); !errors.Is(err, kek.ErrConfig) ||
		!strings.Contains(err.Error(), "token_file or VAULT_TOKEN is required") {
		t.Errorf("no token: %v", err)
	}
	c.Auth.TokenFile = filepath.Join(t.TempDir(), "missing")
	if _, err := vault.New(c, vault.Options{}); !errors.Is(err, kek.ErrConfig) {
		t.Errorf("a missing token file: %v", err)
	}
	c.KeyName = ""
	if _, err := vault.New(c, vault.Options{}); !errors.Is(err, kek.ErrConfig) {
		t.Errorf("no key: %v", err)
	}
}
