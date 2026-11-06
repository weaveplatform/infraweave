package kek_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
)

func encoded(fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

func TestUnitLocalWrap(t *testing.T) {
	p, err := kek.OpenLocal(kek.LocalConfig{Key: encoded(1)})
	if err != nil {
		t.Fatal(err)
	}
	if p.Ref() != "local" {
		t.Errorf("Ref = %q", p.Ref())
	}
	data := bytes.Repeat([]byte{9}, 32)
	wrapped, err := p.Wrap(t.Context(), 3, data)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.Unwrap(t.Context(), 3, wrapped); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("round trip: %x, %v", got, err)
	}
	other, err := kek.OpenLocal(kek.LocalConfig{Key: encoded(2)})
	if err != nil {
		t.Fatal(err)
	}
	version := bytes.Clone(wrapped)
	version[0] = 2
	for name, tc := range map[string]struct {
		p       kek.Provider
		id      uint32
		wrapped []byte
	}{
		"wrong KEK":       {other, 3, wrapped},
		"another key ID":  {p, 4, wrapped},
		"unknown version": {p, 3, version},
		"empty":           {p, 3, nil},
		"truncated":       {p, 3, wrapped[:20]},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.p.Unwrap(
				t.Context(),
				tc.id,
				tc.wrapped,
			); !errors.Is(
				err,
				kek.ErrUnwrap,
			) {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestUnitLocalKeyFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "kek")
	if err := os.WriteFile(good, []byte(encoded(1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := kek.OpenLocal(kek.LocalConfig{KeyFile: good})
	if err != nil {
		t.Fatal(err)
	}
	fromEnv, err := kek.OpenLocal(kek.LocalConfig{Key: encoded(1)})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := fromFile.Wrap(t.Context(), 1, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fromEnv.Unwrap(t.Context(), 1, wrapped); err != nil {
		t.Errorf("the file's KEK and the variable's differ: %v", err)
	}
}

func TestUnitConfig(t *testing.T) {
	vault := func(f func(*kek.VaultConfig)) kek.Config {
		c := kek.Defaults()
		c.Provider = kek.Vault
		c.Vault.Address, c.Vault.KeyName = "https://vault:8200", "kek"
		f(&c.Vault)
		return c
	}
	for name, tc := range map[string]struct {
		c    kek.Config
		want string
	}{
		"unknown provider": {kek.Config{Provider: "rot13"}, `secrets.kek.provider must be local, awskms, gcpkms, azurekv or vault, got "rot13"`},
		"no awskms key":    {kek.Config{Provider: kek.AWSKMS}, "secrets.kek.awskms.key_id is required"},
		"no gcpkms key":    {kek.Config{Provider: kek.GCPKMS}, "secrets.kek.gcpkms.key_name is required"},
		"a key ring": {
			kek.Config{Provider: kek.GCPKMS, GCPKMS: kek.GCPKMSConfig{KeyName: "projects/p/locations/l/keyRings/r"}},
			"key_name must be projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>",
		},
		"no vault URL": {kek.Config{Provider: kek.AzureKV}, "secrets.kek.azurekv.vault_url is required"},
		"an http vault URL": {
			kek.Config{Provider: kek.AzureKV, AzureKV: kek.AzureKVConfig{VaultURL: "http://kv.vault.azure.net", KeyName: "k"}},
			"secrets.kek.azurekv.vault_url must be a https URL",
		},
		"no azurekv key": {
			kek.Config{Provider: kek.AzureKV, AzureKV: kek.AzureKVConfig{VaultURL: "https://kv.vault.azure.net"}},
			"secrets.kek.azurekv.key_name is required",
		},
		"no Vault address": {vault(func(v *kek.VaultConfig) { v.Address = "" }), "secrets.kek.vault.address is required"},
		"a Vault path":     {vault(func(v *kek.VaultConfig) { v.Address = "vault:8200" }), "must be a https URL"},
		"no Transit key":   {vault(func(v *kek.VaultConfig) { v.KeyName = "" }), "secrets.kek.vault.key_name is required"},
		"no mount":         {vault(func(v *kek.VaultConfig) { v.Mount = "" }), "secrets.kek.vault.mount is required"},
		"unknown method": {
			vault(func(v *kek.VaultConfig) { v.Auth.Method = "userpass" }),
			`secrets.kek.vault.auth.method must be token, kubernetes or approle, got "userpass"`,
		},
		"kubernetes without a role": {
			vault(func(v *kek.VaultConfig) { v.Auth.Method = kek.VaultKubernetes }),
			"secrets.kek.vault.auth.role is required",
		},
		"approle without a secret ID": {
			vault(func(v *kek.VaultConfig) { v.Auth.Method, v.Auth.RoleID = kek.VaultAppRole, "r" }),
			"secrets.kek.vault.auth.secret_id_file is required",
		},
		"no key": {kek.Config{Provider: kek.Local}, "secrets.kek.local.key_file or INFRAWEAVE_SECRETS_KEK is required"},
		"both keys": {
			kek.Config{Provider: kek.Local, Local: kek.LocalConfig{KeyFile: "f", Key: encoded(1)}},
			"not both",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.c.Validate()
			if !errors.Is(err, kek.ErrConfig) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
	for name, c := range map[string]kek.Config{
		"no provider": {},
		"awskms":      {Provider: kek.AWSKMS, AWSKMS: kek.AWSKMSConfig{KeyID: "alias/infraweave"}},
		"gcpkms": {Provider: kek.GCPKMS, GCPKMS: kek.GCPKMSConfig{
			KeyName: "projects/p/locations/global/keyRings/r/cryptoKeys/k",
		}},
		"azurekv": {Provider: kek.AzureKV, AzureKV: kek.AzureKVConfig{VaultURL: "https://kv.vault.azure.net", KeyName: "k"}},
		"vault":   vault(func(*kek.VaultConfig) {}),
		"vault over http, with kubernetes": vault(func(v *kek.VaultConfig) {
			v.Address, v.Auth.Method, v.Auth.Role = "http://vault:8200", kek.VaultKubernetes, "infraweave"
		}),
		"approle": vault(func(v *kek.VaultConfig) {
			v.Auth.Method, v.Auth.RoleID, v.Auth.SecretIDFile = kek.VaultAppRole, "r", "/run/secrets/secret-id"
		}),
	} {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUnitLocalKey(t *testing.T) {
	for name, tc := range map[string]struct {
		c    kek.LocalConfig
		want string
	}{
		"not base64":   {kek.LocalConfig{Key: "!!"}, "not base64"},
		"16 bytes":     {kek.LocalConfig{Key: base64.StdEncoding.EncodeToString(make([]byte, 16))}, "is 16 bytes, want 32"},
		"missing file": {kek.LocalConfig{KeyFile: "/no/such/kek"}, "key_file"},
		"neither":      {kek.LocalConfig{}, "is required"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := kek.OpenLocal(tc.c)
			if !errors.Is(err, kek.ErrConfig) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := kek.NewLocal(make([]byte, 31)); !errors.Is(err, kek.ErrConfig) {
		t.Errorf("31-byte KEK: %v", err)
	}
}

func TestUnitAAD(t *testing.T) {
	if got := string(kek.AAD(4294967295)); got != "infraweave/data-key/4294967295" {
		t.Errorf("AAD = %q", got)
	}
}
