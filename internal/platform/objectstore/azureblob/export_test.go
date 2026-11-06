package azureblob

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
)

// Delegation is delegation, for the key cache's test.
func (b *Bucket) Delegation(
	ctx context.Context,
	until time.Time,
) (*service.UserDelegationCredential, error) {
	return b.delegation(ctx, until)
}

// Fake replaces the clock and the user delegation key service.
func (b *Bucket) Fake(
	now func() time.Time,
	delegate func(context.Context, service.KeyInfo) (*service.UserDelegationCredential, error),
) {
	b.now, b.delegate = now, delegate
}
