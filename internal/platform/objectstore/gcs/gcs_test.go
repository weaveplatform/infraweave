package gcs_test

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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/api/option"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/gcs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
	"github.com/weaveplatform/infraweave/test/acceptance"
)

// signer is a throwaway service account key: fake-gcs-server holds no
// IAM, and verifies no signature.
func signer(t *testing.T) storage.SignedURLOptions {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return storage.SignedURLOptions{
		GoogleAccessID: "contract@infraweave-test.iam.gserviceaccount.com",
		PrivateKey:     pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		Insecure:       true,
	}
}

// TestAccGCSCloud runs the contract against Google Cloud Storage, in the
// objectstore-cloud job, as the identity its credentials hold, with
// production's options: Application Default Credentials, and URLs signed
// through IAM signBlob.
func TestAccGCSCloud(t *testing.T) {
	id, v := objectstoretest.Cloud(t, "INFRAWEAVE_TEST_GCS_BUCKET")
	b, err := gcs.New(t.Context(), objectstore.GCSConfig{Bucket: v[0]}, "",
		objectstore.MinPartSize, gcs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	objectstoretest.Run(t, b, objectstoretest.Options{
		PartSize: objectstore.MinPartSize,
		Skip:     objectstoretest.Unbound[objectstore.DriverGCS],
		Identity: id,
	})
	objectstoretest.Canceled(t, b, objectstore.MinPartSize)
}

// TestAccGCSContract runs the contract against fake-gcs-server.
func TestAccGCSContract(t *testing.T) {
	endpoint := objectstoretest.Emulator(t, acceptance.ImageFakeGCS, "4443/tcp",
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
	sign := signer(t)
	sign.Hostname = u.Host
	b, err := gcs.New(
		t.Context(),
		objectstore.GCSConfig{Bucket: "contract"},
		"",
		objectstore.MinPartSize,
		gcs.Options{
			Client: []option.ClientOption{
				option.WithEndpoint(endpoint + "/storage/v1/"),
				option.WithoutAuthentication(),
				option.WithHTTPClient(objectstoretest.NoMD5(t, http.DefaultTransport)),
			},
			Sign: sign,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Handle().Create(t.Context(), "infraweave-test", nil); err != nil {
		t.Fatal(err)
	}
	objectstoretest.Run(t, b, objectstoretest.Options{
		PartSize: objectstore.MinPartSize,
		Skip:     objectstoretest.Skip(objectstore.DriverGCS),
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
