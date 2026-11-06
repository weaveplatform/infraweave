package awskms

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
)

// Provider wraps data keys with AWS KMS Encrypt and Decrypt under one KMS
// key, with the encryption context {"infraweave":"data-key","id":"<id>"}.
type Provider struct {
	client *kms.Client
	keyID  string
	ref    string
}

// New returns the provider c configures, with credentials from the SDK's
// default chain. opts change the KMS client, as a test's endpoint does.
func New(ctx context.Context, c kek.AWSKMSConfig, opts ...func(*kms.Options)) (*Provider, error) {
	if err := (kek.Config{Provider: kek.AWSKMS, AWSKMS: c}).Validate(); err != nil {
		return nil, err //nolint:wrapcheck // names its key
	}
	var load []func(*config.LoadOptions) error
	if c.Region != "" {
		load = append(load, config.WithRegion(c.Region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, load...)
	if err != nil {
		return nil, fmt.Errorf("%s:%s: %w", kek.AWSKMS, c.KeyID, err)
	}
	return &Provider{
		client: kms.NewFromConfig(cfg, opts...),
		keyID:  c.KeyID,
		ref:    kek.AWSKMS + ":" + c.KeyID,
	}, nil
}

// Ref is awskms:<key ID>, as secrets.kek.awskms.key_id names it.
func (p *Provider) Ref() string { return p.ref }

func encryptionContext(id uint32) map[string]string {
	return map[string]string{"infraweave": "data-key", "id": strconv.FormatUint(uint64(id), 10)}
}

// Wrap stores KMS's ciphertext blob, which names the key and its backing
// key version.
func (p *Provider) Wrap(ctx context.Context, id uint32, key []byte) ([]byte, error) {
	out, err := p.client.Encrypt(ctx, &kms.EncryptInput{
		KeyId:             aws.String(p.keyID),
		Plaintext:         key,
		EncryptionContext: encryptionContext(id),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.ref, err)
	}
	return out.CiphertextBlob, nil
}

// Unwrap decrypts wrapped, naming the key so KMS refuses a blob another
// key wrapped. A blob that does not authenticate under the encryption
// context, or that another key wrapped, is ErrUnwrap.
func (p *Provider) Unwrap(ctx context.Context, id uint32, wrapped []byte) ([]byte, error) {
	out, err := p.client.Decrypt(ctx, &kms.DecryptInput{
		KeyId:             aws.String(p.keyID),
		CiphertextBlob:    wrapped,
		EncryptionContext: encryptionContext(id),
	})
	var (
		invalid   *types.InvalidCiphertextException
		incorrect *types.IncorrectKeyException
	)
	if errors.As(err, &invalid) || errors.As(err, &incorrect) {
		return nil, fmt.Errorf("%s: %w: %w", p.ref, kek.ErrUnwrap, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.ref, err)
	}
	return out.Plaintext, nil
}
