package azureblob_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/azureblob"
)

// TestUnitDelegationKey checks that a user delegation key is fetched
// once and again only when it would end before a URL signed with it.
func TestUnitDelegationKey(t *testing.T) {
	b, err := azureblob.New(objectstore.AzureBlobConfig{
		AccountURL: "https://acct.blob.core.windows.net", Container: "c",
	}, "", objectstore.MinPartSize, azureblob.Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	var asked []service.KeyInfo
	b.Fake(
		func() time.Time { return now },
		func(_ context.Context, k service.KeyInfo) (*service.UserDelegationCredential, error) {
			asked = append(asked, k)
			return new(service.UserDelegationCredential), nil
		},
	)
	ctx := t.Context()
	first, err := b.Delegation(ctx, now.Add(15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if *asked[0].Start != now.Add(-5*time.Minute).Format(sas.TimeFormat) ||
		*asked[0].Expiry != now.Add(15*time.Minute+24*time.Hour).Format(sas.TimeFormat) {
		t.Errorf("asked for a key from %s to %s", *asked[0].Start, *asked[0].Expiry)
	}
	if again, _ := b.Delegation(ctx, now.Add(24*time.Hour)); again != first || len(asked) != 1 {
		t.Errorf("a key that lasts long enough was fetched again: %d fetches", len(asked))
	}
	if later, _ := b.Delegation(ctx, now.Add(25*time.Hour)); later == first || len(asked) != 2 {
		t.Errorf("a key that ends too soon was reused: %d fetches", len(asked))
	}

	refused := errors.New("no role assignment")
	b.Fake(
		func() time.Time { return now.Add(72 * time.Hour) },
		func(context.Context, service.KeyInfo) (*service.UserDelegationCredential, error) { return nil, refused },
	)
	if _, err := b.SignURL(
		ctx,
		http.MethodGet,
		"k",
		time.Minute,
		objectstore.Constraints{},
	); !errors.Is(
		err,
		refused,
	) {
		t.Errorf("SignURL without a key: %v", err)
	}
}
