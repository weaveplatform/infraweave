package gcpkms

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
)

// ErrIntegrity reports a request or response whose CRC32C Cloud KMS's
// integrity fields do not confirm, which Google's guidance is to discard.
var ErrIntegrity = errors.New("integrity check of Cloud KMS's answer failed")

// Provider wraps data keys with Cloud KMS Encrypt and Decrypt under one
// CryptoKey, with kek.AAD(id) as additional authenticated data. It calls
// Cloud KMS over REST, so one transport serves production and the
// recorded fixtures alike.
type Provider struct {
	client *kms.KeyManagementClient
	name   string
	ref    string
}

// New returns the provider c configures, with Application Default
// Credentials unless opts say otherwise.
func New(ctx context.Context, c kek.GCPKMSConfig, opts ...option.ClientOption) (*Provider, error) {
	if err := (kek.Config{Provider: kek.GCPKMS, GCPKMS: c}).Validate(); err != nil {
		return nil, err //nolint:wrapcheck // names its key
	}
	ref := kek.GCPKMS + ":" + c.KeyName
	client, err := NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	return &Provider{client: client, name: c.KeyName, ref: ref}, nil
}

// NewClient returns a Cloud KMS client over REST, with Application Default
// Credentials unless opts say otherwise. REST rather than gRPC lets one
// transport serve production and the recorded fixtures; the gcpkms signing
// backend calls Cloud KMS through it too.
func NewClient(ctx context.Context, opts ...option.ClientOption) (*kms.KeyManagementClient, error) {
	//nolint:wrapcheck // the caller names the key
	return kms.NewKeyManagementRESTClient(ctx, opts...)
}

// CRC32C is Cloud KMS's integrity checksum of b, as its requests and
// answers carry it.
func CRC32C(b []byte) *wrapperspb.Int64Value { return crc(b) }

// Ref is gcpkms:<CryptoKey resource name>.
func (p *Provider) Ref() string { return p.ref }

// Close closes the client.
func (p *Provider) Close() error { return p.client.Close() } //nolint:wrapcheck // nothing to add

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

func crc(b []byte) *wrapperspb.Int64Value {
	return wrapperspb.Int64(int64(crc32.Checksum(b, castagnoli)))
}

// Wrap stores Cloud KMS's ciphertext, which names the CryptoKeyVersion
// that decrypts it.
func (p *Provider) Wrap(ctx context.Context, id uint32, key []byte) ([]byte, error) {
	aad := kek.AAD(id)
	out, err := p.client.Encrypt(ctx, &kmspb.EncryptRequest{
		Name:                              p.name,
		Plaintext:                         key,
		PlaintextCrc32C:                   crc(key),
		AdditionalAuthenticatedData:       aad,
		AdditionalAuthenticatedDataCrc32C: crc(aad),
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", p.ref, err)
	case !out.GetVerifiedPlaintextCrc32C() ||
		!out.GetVerifiedAdditionalAuthenticatedDataCrc32C() ||
		out.GetCiphertextCrc32C().GetValue() != crc(out.GetCiphertext()).GetValue():
		return nil, fmt.Errorf("%s: encrypt: %w", p.ref, ErrIntegrity)
	case !strings.HasPrefix(out.GetName(), p.name+"/cryptoKeyVersions/"):
		return nil, fmt.Errorf(
			"%s: encrypt: %w: answered by %q",
			p.ref,
			ErrIntegrity,
			out.GetName(),
		)
	}
	return out.GetCiphertext(), nil
}

// Unwrap decrypts wrapped. Cloud KMS answers INVALID_ARGUMENT for a
// ciphertext that does not authenticate under the key and the additional
// authenticated data, which is ErrUnwrap.
func (p *Provider) Unwrap(ctx context.Context, id uint32, wrapped []byte) ([]byte, error) {
	aad := kek.AAD(id)
	out, err := p.client.Decrypt(ctx, &kmspb.DecryptRequest{
		Name:                              p.name,
		Ciphertext:                        wrapped,
		CiphertextCrc32C:                  crc(wrapped),
		AdditionalAuthenticatedData:       aad,
		AdditionalAuthenticatedDataCrc32C: crc(aad),
	})
	if e := (*apierror.APIError)(nil); errors.As(err, &e) &&
		e.GRPCStatus().Code() == codes.InvalidArgument {
		return nil, fmt.Errorf("%s: %w: %w", p.ref, kek.ErrUnwrap, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.ref, err)
	}
	if out.GetPlaintextCrc32C().GetValue() != crc(out.GetPlaintext()).GetValue() {
		return nil, fmt.Errorf("%s: decrypt: %w", p.ref, ErrIntegrity)
	}
	return out.GetPlaintext(), nil
}
