package app

import (
	"context"
	"io"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/awskms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/azurekv"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/gcpkms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/signer/vault"
)

// openSigners opens each key of auth.signing.kms_keys, reading its public
// half, so a key infraweave cannot sign with fails the start, naming its
// reference, rather than the first token. The vault keys reach the Vault
// of secrets.kek.vault. It returns what closes them.
func openSigners(
	ctx context.Context,
	s accessassembly.SigningSettings,
	v kek.VaultConfig,
) ([]signer.KMS, func(), error) {
	var closers []io.Closer
	closeAll := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
	if s.Backend != accessassembly.BackendKMS {
		return nil, closeAll, nil
	}
	keys := make([]signer.KMS, 0, len(s.KMSKeys))
	for _, k := range s.KMSKeys {
		var (
			sg  signer.KMS
			err error
		)
		switch k.Provider {
		case kek.AWSKMS:
			sg, err = awskms.New(ctx, k.Ref)
		case kek.GCPKMS:
			sg, err = gcpkms.New(ctx, k.Ref)
		case kek.AzureKV:
			sg, err = azurekv.New(ctx, k.Ref, azurekv.Options{})
		default:
			sg, err = vault.New(ctx, v, k.Ref, vault.Options{})
		}
		if err != nil {
			closeAll()
			return nil, nil, err //nolint:wrapcheck // each backend names the reference
		}
		if c, ok := sg.(io.Closer); ok {
			closers = append(closers, c)
		}
		keys = append(keys, sg)
	}
	return keys, closeAll, nil
}
