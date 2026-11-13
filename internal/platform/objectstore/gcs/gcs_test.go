package gcs_test

import (
	"net/http"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/gcs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/objectstoretest"
)

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
	b := objectstoretest.GCS(t, "contract")
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
