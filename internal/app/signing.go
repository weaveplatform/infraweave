package app

import (
	"context"
	"io"

	jose "github.com/go-jose/go-jose/v4"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/awskms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/azurekv"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/gcpkms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/vault"
)

// openSigners opens each key of auth.signing.kms_keys for ES256 and each
// of auth.federation.kms_keys for auth.federation.algorithm under the kms
// backend, reading its public half, so a key infraweave cannot sign its
// purpose's algorithm with fails the start, naming its reference, rather
// than the first token. The vault keys reach the Vault of
// secrets.kek.vault. It returns what closes them.
func openSigners(ctx context.Context, a accessassembly.Settings, v kek.VaultConfig) ([]signer.KMS, func(), error) {
	var closers []io.Closer
	closeAll := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
	if a.Signing.Backend != accessassembly.BackendKMS {
		return nil, closeAll, nil
	}
	keys := make([]signer.KMS, 0, len(a.Signing.KMSKeys)+len(a.Federation.KMSKeys))
	for _, purpose := range []struct {
		keys []accessassembly.KMSKey
		alg  jose.SignatureAlgorithm
	}{
		{a.Signing.KMSKeys, signer.ES256},
		{a.Federation.KMSKeys, jose.SignatureAlgorithm(a.Federation.Algorithm)},
	} {
		for _, k := range purpose.keys {
			sg, err := openSigner(ctx, k, purpose.alg, v)
			if err != nil {
				closeAll()
				return nil, nil, err
			}
			if c, ok := sg.(io.Closer); ok {
				closers = append(closers, c)
			}
			keys = append(keys, sg)
		}
	}
	return keys, closeAll, nil
}

// openSigner opens k for alg through its provider's backend.
func openSigner(ctx context.Context, k accessassembly.KMSKey, alg jose.SignatureAlgorithm, v kek.VaultConfig) (signer.KMS, error) {
	//nolint:wrapcheck // each backend names the reference
	switch k.Provider {
	case kek.AWSKMS:
		return awskms.New(ctx, k.Ref, alg)
	case kek.GCPKMS:
		return gcpkms.New(ctx, k.Ref, alg)
	case kek.AzureKV:
		return azurekv.New(ctx, k.Ref, alg, azurekv.Options{})
	default:
		return vault.New(ctx, v, k.Ref, alg, vault.Options{})
	}
}
