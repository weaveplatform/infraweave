package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/kektest"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/vault"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// TestAccAllWithVault checks that all starts with its data keys wrapped
// by Vault's Transit engine: the first start wraps key 1, the second
// unwraps it, and a value sealed under the keyring a test loads through
// Vault opens. A Vault the replica cannot use stops the start, naming the
// provider and the key.
func TestAccAllWithVault(t *testing.T) {
	l := kektest.StartVault(t)
	t.Setenv(vault.TokenEnv, kektest.VaultRoot)
	observer := sqltest.Open(t)
	withVault := func(c *config.Config) { c.Secrets.KEK = kek.Config{Provider: kek.Vault, Vault: l.Config()} }

	for range 2 {
		_, _, stop := runModeWith(t, observer, ModeAll, withVault)
		stop()
	}
	var ref string
	if err := observer.Primary().QueryRow(t.Context(),
		`SELECT kek_ref FROM encryption_keys WHERE id = 1 AND state = 'active'`).
		Scan(&ref); err != nil ||
		ref != "vault:"+l.Addr+"/transit/kek" {
		t.Fatalf("kek_ref %q, %v", ref, err)
	}
	p, err := vault.New(l.Config(), vault.Options{})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypt.Load(t.Context(), observer.Primary(), p)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := keys.Seal(t.Context(), []byte("t.c:1"), []byte("a secret"))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := keys.Open(
		t.Context(),
		[]byte("t.c:1"),
		sealed,
	); err != nil ||
		string(v.Reveal()) != "a secret" {
		t.Errorf("Open = %v, %v", v, err)
	}

	for name, edit := range map[string]func(*config.Config){
		"a token Vault refuses": func(c *config.Config) {
			withVault(c)
			t.Setenv(vault.TokenEnv, "not-a-token")
		},
		"an unreachable Vault": func(c *config.Config) {
			withVault(c)
			c.Secrets.KEK.Vault.Address = "http://127.0.0.1:1"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := local(t)
			c.Database.URL = observer.Primary().Config().ConnString()
			edit(&c)
			err := Run(t.Context(), c, discard(), ModeAll)
			if !errors.Is(err, crypt.ErrKeyring) || !strings.Contains(err.Error(), "data key 1") ||
				!strings.Contains(err.Error(), "vault:") {
				t.Errorf("Run: %v", err)
			}
		})
	}
}

func TestUnitOpenKEK(t *testing.T) {
	c := local(t).Secrets.KEK
	c.Provider = ""
	if _, _, err := openKEK(t.Context(), c); !errors.Is(err, kek.ErrNoProvider) {
		t.Errorf("no provider: %v", err)
	}
	t.Setenv(vault.TokenEnv, "")
	for _, provider := range []string{kek.AWSKMS, kek.GCPKMS, kek.AzureKV, kek.Vault} {
		c.Provider = provider
		c.AWSKMS = kek.AWSKMSConfig{KeyID: "alias/infraweave", Region: "us-east-1"}
		c.GCPKMS = kek.GCPKMSConfig{KeyName: "projects/p/locations/global/keyRings/r/cryptoKeys/k"}
		c.AzureKV = kek.AzureKVConfig{VaultURL: "https://kv.vault.azure.net", KeyName: "k"}
		c.Vault.Address, c.Vault.KeyName = "http://127.0.0.1:1", "kek"
		p, closeKEK, err := openKEK(t.Context(), c)
		// Without a token the vault provider cannot start; the others
		// reach their cloud only when they wrap.
		if provider == kek.Vault {
			if !errors.Is(err, kek.ErrConfig) {
				t.Errorf("vault without a token: %v", err)
			}
			continue
		}
		if err != nil {
			// A host without cloud credentials fails here, naming the
			// provider; that is the start failing as it should.
			if !strings.Contains(err.Error(), provider+":") {
				t.Errorf("%s: %v", provider, err)
			}
			continue
		}
		if !strings.HasPrefix(p.Ref(), provider+":") {
			t.Errorf("%s: Ref %q", provider, p.Ref())
		}
		closeKEK()
	}
}
