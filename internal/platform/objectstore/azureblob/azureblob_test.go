package azureblob_test

import (
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/azureblob"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
	"github.com/weaveplatform/infraweave/test/deps"
)

// Azurite's well-known development account, which every Azurite holds.
const (
	account    = "devstoreaccount1"
	accountKey = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==" //nolint:gosec // public
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
	endpoint := objectstoretest.Emulator(t, deps.ImageAzurite, "10000/tcp",
		// The SDK speaks a newer service version than Azurite announces.
		testcontainers.WithCmd("azurite-blob", "--blobHost", "0.0.0.0", "--blobPort", "10000",
			"--inMemoryPersistence", "--skipApiVersionCheck"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("10000/tcp")),
	)
	key, err := service.NewSharedKeyCredential(account, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	b, err := azureblob.New(
		objectstore.AzureBlobConfig{AccountURL: endpoint + "/" + account, Container: "contract"},
		"",
		objectstore.MinPartSize,
		azureblob.Options{
			SharedKey: key,
			Client: &service.ClientOptions{ClientOptions: azcore.ClientOptions{
				Transport:                       objectstoretest.NoMD5(t, http.DefaultTransport),
				InsecureAllowCredentialWithHTTP: true,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Container().Create(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
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
