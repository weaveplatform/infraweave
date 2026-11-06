package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
)

// TokenEnv is the variable the token method reads when
// secrets.kek.vault.auth.token_file is empty.
const TokenEnv = "VAULT_TOKEN"

// Provider wraps data keys with Vault's Transit engine. It binds
// kek.AAD(id) as Transit's associated_data, which an AEAD key checks on
// decrypt; Transit's context would bind nothing on a key that is not
// derived.
type Provider struct {
	c      kek.VaultConfig
	client *http.Client
	ref    string
	now    func() time.Time

	mu sync.Mutex
	// token is the Vault token; renew is when a login method logs in
	// again, zero for a token that does not expire.
	token string
	renew time.Time
}

// Options are what a test changes.
type Options struct {
	// Client sends the requests; nil is a client with a 30-second timeout.
	Client *http.Client
	// Now is the clock that times a login token's renewal.
	Now func() time.Time
}

// New returns the provider c configures. The token method reads its token
// now; kubernetes and approle log in on the first call.
func New(c kek.VaultConfig, o Options) (*Provider, error) {
	if err := (kek.Config{Provider: kek.Vault, Vault: c}).Validate(); err != nil {
		return nil, err //nolint:wrapcheck // names its key
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	p := &Provider{
		c:      c,
		client: o.Client,
		ref: kek.Vault + ":" + strings.TrimSuffix(
			c.Address,
			"/",
		) + "/" + c.Mount + "/" + c.KeyName,
		now: o.Now,
	}
	if c.Auth.Method == kek.VaultToken {
		token := os.Getenv(TokenEnv)
		if c.Auth.TokenFile != "" {
			b, err := os.ReadFile(c.Auth.TokenFile)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: secrets.kek.vault.auth.token_file: %w",
					kek.ErrConfig,
					err,
				)
			}
			token = string(b)
		}
		if p.token = strings.TrimSpace(token); p.token == "" {
			return nil, fmt.Errorf(
				"%w: secrets.kek.vault.auth.token_file or %s is required",
				kek.ErrConfig,
				TokenEnv,
			)
		}
	}
	return p, nil
}

// Ref is vault:<address>/<mount>/<key name>.
func (p *Provider) Ref() string { return p.ref }

// Wrap encrypts key with the Transit key and stores Transit's ciphertext,
// vault:v<version>:<base64>, which names the key version that unwraps it.
func (p *Provider) Wrap(ctx context.Context, id uint32, key []byte) ([]byte, error) {
	var out struct {
		Ciphertext string `json:"ciphertext"`
	}
	if err := p.transit(ctx, "encrypt", map[string]string{
		"plaintext":       base64.StdEncoding.EncodeToString(key),
		"associated_data": base64.StdEncoding.EncodeToString(kek.AAD(id)),
	}, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", p.ref, err)
	}
	return []byte(out.Ciphertext), nil
}

// Unwrap decrypts wrapped. Transit answers 400 for a ciphertext that does
// not authenticate under the key and associated data, which is ErrUnwrap.
func (p *Provider) Unwrap(ctx context.Context, id uint32, wrapped []byte) ([]byte, error) {
	var out struct {
		Plaintext string `json:"plaintext"`
	}
	err := p.transit(ctx, "decrypt", map[string]string{
		"ciphertext":      string(wrapped),
		"associated_data": base64.StdEncoding.EncodeToString(kek.AAD(id)),
	}, &out)
	if e := (*apiError)(nil); errors.As(err, &e) && e.status == http.StatusBadRequest {
		return nil, fmt.Errorf("%s: %w: %w", p.ref, kek.ErrUnwrap, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.ref, err)
	}
	key, err := base64.StdEncoding.DecodeString(out.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("%s: decrypt: %w", p.ref, err)
	}
	return key, nil
}

// transit calls the Transit key's op. A login method's token that Vault
// refuses is renewed once, since it may have been revoked early.
func (p *Provider) transit(ctx context.Context, op string, in map[string]string, out any) error {
	path := p.c.Mount + "/" + op + "/" + url.PathEscape(p.c.KeyName)
	for attempt := 0; ; attempt++ {
		token, err := p.currentToken(ctx, attempt > 0)
		if err != nil {
			return err
		}
		var data struct {
			Data json.RawMessage `json:"data"`
		}
		err = p.call(ctx, path, token, in, &data)
		if e := (*apiError)(nil); errors.As(err, &e) && e.status == http.StatusForbidden &&
			attempt == 0 && p.c.Auth.Method != kek.VaultToken {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", op, err)
		}
		if err := json.Unmarshal(data.Data, out); err != nil {
			return fmt.Errorf("%s: %w", op, err)
		}
		return nil
	}
}

// currentToken returns the token, logging in when there is none, when it
// is due for renewal, or when force says Vault refused it.
func (p *Provider) currentToken(ctx context.Context, force bool) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.c.Auth.Method == kek.VaultToken ||
		(!force && p.token != "" && (p.renew.IsZero() || p.now().Before(p.renew))) {
		return p.token, nil
	}
	body, err := p.loginBody()
	if err != nil {
		return "", err
	}
	mount := p.c.Auth.Mount
	if mount == "" {
		mount = p.c.Auth.Method
	}
	//nolint:tagliatelle // Vault\'s format
	var out struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int64  `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := p.call(ctx, "auth/"+mount+"/login", "", body, &out); err != nil {
		return "", fmt.Errorf("%s login: %w", p.c.Auth.Method, err)
	}
	p.token, p.renew = out.Auth.ClientToken, time.Time{}
	// Logging in again halfway through the lease leaves the other half
	// for a call that is already under way.
	if lease := time.Duration(out.Auth.LeaseDuration) * time.Second; lease > 0 {
		p.renew = p.now().Add(lease / 2)
	}
	return p.token, nil
}

// loginBody reads the login method's credential, which an agent or the
// kubelet may have rotated since the last login.
func (p *Provider) loginBody() (map[string]string, error) {
	file, key := p.c.Auth.JWTFile, "jwt_file"
	if p.c.Auth.Method == kek.VaultAppRole {
		file, key = p.c.Auth.SecretIDFile, "secret_id_file"
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("secrets.kek.vault.auth.%s: %w", key, err)
	}
	secret := strings.TrimSpace(string(b))
	if p.c.Auth.Method == kek.VaultAppRole {
		return map[string]string{"role_id": p.c.Auth.RoleID, "secret_id": secret}, nil
	}
	return map[string]string{"role": p.c.Auth.Role, "jwt": secret}, nil
}

// apiError is a response other than 200, with Vault's errors.
type apiError struct {
	status int
	errors []string
}

func (e *apiError) Error() string {
	if len(e.errors) == 0 {
		return http.StatusText(e.status)
	}
	return fmt.Sprintf("%s: %s", http.StatusText(e.status), strings.Join(e.errors, "; "))
}

// call POSTs in to /v1/<path> and decodes the response into out.
func (p *Provider) call(ctx context.Context, path, token string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err //nolint:wrapcheck // a map of strings always marshals
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimSuffix(p.c.Address, "/")+"/v1/"+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return err //nolint:wrapcheck // the address is validated; the caller names the call
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err //nolint:wrapcheck // the caller names the call
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err //nolint:wrapcheck // as above
	}
	if resp.StatusCode != http.StatusOK {
		e := &apiError{status: resp.StatusCode}
		var v struct {
			Errors []string `json:"errors"`
		}
		if json.Unmarshal(raw, &v) == nil {
			e.errors = v.Errors
		}
		return e
	}
	return json.Unmarshal(raw, out) //nolint:wrapcheck // as above
}
