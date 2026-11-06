package azurekv

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
)

// version opens a key the azurekv provider wrapped: 0x01 | length of the
// key version (uint8) | key version | RSA-OAEP-256 ciphertext.
const version byte = 0x01

// ErrKeyID reports a wrapKey answer whose key ID names no version, which
// Unwrap would need.
var ErrKeyID = errors.New("wrapKey answered without a versioned key ID")

// Provider wraps data keys with Key Vault's wrapKey and unwrapKey, with
// RSA-OAEP-256, under one RSA key. RSA-OAEP takes no associated data, so
// the wrapped plaintext is the data key's ID (uint32, big-endian) and then
// the key: OAEP is non-malleable, so no ciphertext changes the ID without
// failing to unwrap, and Unwrap refuses an ID other than the one it is
// asked for.
type Provider struct {
	client  *azkeys.Client
	name    string
	version string
	ref     string
}

// Options are what a test changes.
type Options struct {
	// Credential authenticates to Key Vault; nil is azidentity's default
	// chain.
	Credential azcore.TokenCredential
	Client     *azkeys.ClientOptions
}

// New returns the provider c configures.
func New(c kek.AzureKVConfig, o Options) (*Provider, error) {
	if err := (kek.Config{Provider: kek.AzureKV, AzureKV: c}).Validate(); err != nil {
		return nil, err //nolint:wrapcheck // names its key
	}
	ref := kek.AzureKV + ":" + strings.TrimSuffix(c.VaultURL, "/") + "/keys/" + c.KeyName
	cred := o.Credential
	if cred == nil {
		var err error
		if cred, err = azidentity.NewDefaultAzureCredential(nil); err != nil {
			return nil, fmt.Errorf("%s: %w", ref, err)
		}
	}
	client, err := azkeys.NewClient(c.VaultURL, cred, o.Client)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	return &Provider{client: client, name: c.KeyName, version: c.KeyVersion, ref: ref}, nil
}

// Ref is azurekv:<vault URL>/keys/<key name>. The version that wrapped
// each key is inside the wrapped key.
func (p *Provider) Ref() string { return p.ref }

func algorithm() *azkeys.EncryptionAlgorithm {
	a := azkeys.EncryptionAlgorithmRSAOAEP256
	return &a
}

// Wrap wraps id and key under the configured version, or the key's
// current one, and records the version Key Vault used.
func (p *Provider) Wrap(ctx context.Context, id uint32, key []byte) ([]byte, error) {
	resp, err := p.client.WrapKey(ctx, p.name, p.version, azkeys.KeyOperationParameters{
		Algorithm: algorithm(),
		Value:     append(binary.BigEndian.AppendUint32(nil, id), key...),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: wrapKey: %w", p.ref, err)
	}
	var v string
	if resp.KID != nil {
		v = resp.KID.Version()
	}
	if v == "" || len(v) > 255 {
		return nil, fmt.Errorf("%s: %w", p.ref, ErrKeyID)
	}
	out := append([]byte{version, byte(len(v))}, v...) //nolint:gosec // at most 255, checked above
	return append(out, resp.Result...), nil
}

// Unwrap unwraps wrapped with the key version that wrapped it. Key Vault
// answers 400 for a ciphertext that does not decrypt, which is ErrUnwrap,
// as is a key wrapped for another ID.
func (p *Provider) Unwrap(ctx context.Context, id uint32, wrapped []byte) ([]byte, error) {
	if len(wrapped) < 2 || wrapped[0] != version || len(wrapped) < 2+int(wrapped[1]) {
		return nil, fmt.Errorf("%s: %w", p.ref, kek.ErrUnwrap)
	}
	n := 2 + int(wrapped[1])
	resp, err := p.client.UnwrapKey(
		ctx,
		p.name,
		string(wrapped[2:n]),
		azkeys.KeyOperationParameters{
			Algorithm: algorithm(),
			Value:     wrapped[n:],
		},
		nil,
	)
	if e := (*azcore.ResponseError)(
		nil,
	); errors.As(err, &e) &&
		e.StatusCode == http.StatusBadRequest {
		return nil, fmt.Errorf("%s: %w: %w", p.ref, kek.ErrUnwrap, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: unwrapKey: %w", p.ref, err)
	}
	prefix := binary.BigEndian.AppendUint32(nil, id)
	if !bytes.HasPrefix(resp.Result, prefix) {
		return nil, fmt.Errorf("%s: %w: wrapped for another data key", p.ref, kek.ErrUnwrap)
	}
	return resp.Result[len(prefix):], nil
}
