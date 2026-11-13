package objectstoretest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/api/option"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/azureblob"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/gcs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/s3"
	"github.com/weaveplatform/infraweave/test/deps"
)

// The credentials of RustFS and Azurite, which the emulators start with:
// RustFS takes them from its environment, and every Azurite holds its
// well-known development account.
const (
	s3AccessKey, s3SecretKey = "infraweave-test", "infraweave-test-secret"
	azureAccount             = "devstoreaccount1"
	azureAccountKey          = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==" //nolint:gosec // public
)

// S3 starts RustFS, the harness's @objectstore image, and answers the
// bucket name on it, created, writing in parts of objectstore.MinPartSize.
func S3(t *testing.T, name string) *s3.Bucket {
	t.Helper()
	endpoint := Emulator(
		t,
		deps.ImageObjectStore,
		"9000/tcp",
		testcontainers.WithEnv(map[string]string{
			"RUSTFS_ACCESS_KEY": s3AccessKey,
			"RUSTFS_SECRET_KEY": s3SecretKey,
		}),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP(deps.ObjectStoreReady).WithPort("9000/tcp"),
		),
	)
	c := objectstore.Defaults().S3
	c.Bucket, c.Endpoint, c.PathStyle = name, endpoint, true
	b, err := s3.New(t.Context(), c, "", objectstore.MinPartSize,
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(s3AccessKey, s3SecretKey, ""),
		),
		config.WithHTTPClient(NoMD5(t, http.DefaultTransport)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Client().
		CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: aws.String(name)}); err != nil {
		t.Fatal(err)
	}
	return b
}

// GCS starts fake-gcs-server and answers the bucket name on it, created,
// writing in resumable chunks of objectstore.MinPartSize. It signs URLs
// under a throwaway service account key: the emulator holds no IAM, and
// verifies no signature.
func GCS(t *testing.T, name string) *gcs.Bucket {
	t.Helper()
	endpoint := Emulator(t, deps.ImageFakeGCS, "4443/tcp",
		testcontainers.WithCmd("-scheme", "http", "-port", "4443", "-backend", "memory"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/storage/v1/b").WithPort("4443/tcp")),
	)
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	// Resumable uploads continue at the URL the server announces, and it
	// routes reads and signed URLs as /<bucket>/<object> only for its
	// public host, so both must be the address the test reaches it on.
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		endpoint+"/_internal/config",
		strings.NewReader(`{"externalUrl":"`+endpoint+`","publicHost":"`+u.Host+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set externalUrl: %d", resp.StatusCode)
	}
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	b, err := gcs.New(
		t.Context(),
		objectstore.GCSConfig{Bucket: name},
		"",
		objectstore.MinPartSize,
		gcs.Options{
			Client: []option.ClientOption{
				option.WithEndpoint(endpoint + "/storage/v1/"),
				option.WithoutAuthentication(),
				option.WithHTTPClient(NoMD5(t, http.DefaultTransport)),
			},
			Sign: storage.SignedURLOptions{
				GoogleAccessID: "contract@infraweave-test.iam.gserviceaccount.com",
				PrivateKey:     pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
				Insecure:       true,
				Hostname:       u.Host,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Handle().Create(t.Context(), "infraweave-test", nil); err != nil {
		t.Fatal(err)
	}
	return b
}

// AzureBlob starts Azurite and answers the container name on it, created,
// writing in blocks of objectstore.MinPartSize.
func AzureBlob(t *testing.T, name string) *azureblob.Bucket {
	t.Helper()
	endpoint := Emulator(t, deps.ImageAzurite, "10000/tcp",
		// The SDK speaks a newer service version than Azurite announces.
		testcontainers.WithCmd("azurite-blob", "--blobHost", "0.0.0.0", "--blobPort", "10000",
			"--inMemoryPersistence", "--skipApiVersionCheck"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("10000/tcp")),
	)
	key, err := service.NewSharedKeyCredential(azureAccount, azureAccountKey)
	if err != nil {
		t.Fatal(err)
	}
	b, err := azureblob.New(
		objectstore.AzureBlobConfig{AccountURL: endpoint + "/" + azureAccount, Container: name},
		"",
		objectstore.MinPartSize,
		azureblob.Options{
			SharedKey: key,
			Client: &service.ClientOptions{ClientOptions: azcore.ClientOptions{
				Transport:                       NoMD5(t, http.DefaultTransport),
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
	return b
}
