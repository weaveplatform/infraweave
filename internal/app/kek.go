package app

import (
	"context"
	"io"

	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/awskms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/azurekv"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/gcpkms"
	"github.com/weaveplatform/infraweave/internal/platform/crypt/kek/vault"
)

// openKEK opens the provider secrets.kek.provider names, and returns what
// closes it.
func openKEK(ctx context.Context, c kek.Config) (kek.Provider, func(), error) {
	if err := c.Validate(); err != nil {
		return nil, nil, err //nolint:wrapcheck // names its key
	}
	var (
		p   kek.Provider
		err error
	)
	switch c.Provider {
	case "":
		return nil, nil, kek.ErrNoProvider
	case kek.AWSKMS:
		p, err = awskms.New(ctx, c.AWSKMS)
	case kek.GCPKMS:
		p, err = gcpkms.New(ctx, c.GCPKMS)
	case kek.AzureKV:
		p, err = azurekv.New(c.AzureKV, azurekv.Options{})
	case kek.Vault:
		p, err = vault.New(c.Vault, vault.Options{})
	default:
		p, err = kek.OpenLocal(c.Local)
	}
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // each provider names itself and its key
	}
	closer := func() {}
	if cl, ok := p.(io.Closer); ok {
		closer = func() { _ = cl.Close() }
	}
	return p, closer, nil
}
