package kektest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
	"github.com/weaveplatform/infraweave/test/deps"
)

// VaultRoot is the root token of the Vault StartVault starts.
const VaultRoot = "root"

// VaultLab is a Vault in dev mode with the Transit engine at transit and
// a key, kek, of the default aes256-gcm96 type.
type VaultLab struct {
	Addr string
	t    *testing.T
}

// StartVault starts deps.ImageVault for t, skipping without Docker
// as objectstoretest.Emulator does.
func StartVault(t *testing.T) VaultLab {
	t.Helper()
	addr := objectstoretest.Emulator(t, deps.ImageVault, "8200/tcp",
		testcontainers.WithEnv(map[string]string{
			"VAULT_DEV_ROOT_TOKEN_ID": VaultRoot,
			// Dev mode keeps everything in memory, so it needs no mlock,
			// and the container no IPC_LOCK.
			"SKIP_SETCAP": "true",
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/v1/sys/health").WithPort("8200/tcp")),
	)
	l := VaultLab{Addr: addr, t: t}
	l.Do(http.MethodPost, "sys/mounts/transit", map[string]any{"type": "transit"})
	l.Do(http.MethodPost, "transit/keys/kek", map[string]any{})
	return l
}

// Config is the vault provider's settings for kek, with the token method.
func (l VaultLab) Config() kek.VaultConfig {
	c := kek.Defaults().Vault
	c.Address, c.KeyName = l.Addr, "kek"
	return c
}

// Do calls Vault with the root token and returns the response's data.
func (l VaultLab) Do(method, path string, body any) map[string]any {
	l.t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(
		l.t.Context(),
		method,
		l.Addr+"/v1/"+path,
		bytes.NewReader(b),
	)
	if err != nil {
		l.t.Fatal(err)
	}
	req.Header.Set("X-Vault-Token", VaultRoot)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Data map[string]any `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode >= 300 {
		l.t.Fatalf("%s %s: %s", method, path, resp.Status)
	}
	return out.Data
}
