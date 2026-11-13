package azureblob_test

import (
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/azureblob"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
)

// TestAccAzureBlobCloud runs the contract against Azure Blob Storage, in
// the objectstore-cloud job, as the identity its credentials hold;
// azidentity's default chain finds them.
func TestAccAzureBlobCloud(t *testing.T) {
	id, v := objectstoretest.Cloud(t,
		"INFRAWEAVE_TEST_AZUREBLOB_ACCOUNT_URL", "INFRAWEAVE_TEST_AZUREBLOB_CONTAINER")
	b, err := azureblob.New(
		objectstore.AzureBlobConfig{AccountURL: v[0], Container: v[1]},
		"",
		objectstore.MinPartSize,
		azureblob.Options{Client: &service.ClientOptions{ClientOptions: azcore.ClientOptions{
			Transport: objectstoretest.NoMD5(t, http.DefaultTransport),
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	objectstoretest.Run(t, b, objectstoretest.Options{
		PartSize: objectstore.MinPartSize,
		Skip:     objectstoretest.Unbound[objectstore.DriverAzureBlob],
		Identity: id,
	})
	objectstoretest.Canceled(t, b, objectstore.MinPartSize)
}

// TestAccAzureBlobContract runs the contract against Azurite.
func TestAccAzureBlobContract(t *testing.T) {
	b := objectstoretest.AzureBlob(t, "contract")
	objectstoretest.Run(t, b, objectstoretest.Options{
		PartSize: objectstore.MinPartSize,
		Skip:     objectstoretest.Skip(objectstore.DriverAzureBlob),
	})
	objectstoretest.Canceled(t, b, objectstore.MinPartSize)
	if _, err := b.SignURL(
		t.Context(),
		http.MethodDelete,
		"k",
		0,
		objectstore.Constraints{},
	); err == nil {
		t.Error("a DELETE URL")
	}
}
