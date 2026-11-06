package app

import (
	"context"
	"fmt"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/platform/crypt"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/azureblob"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/gcs"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/s3"
)

// objects is the object store a process opens: the full Store, which only
// the scheduler role holds, the fs driver's /blobs/{token} routes, nil
// for another driver, and what closes it.
type objects struct {
	store  objectstore.Store
	routes func(chi.Router)
	close  func()
}

// openObjects opens c's driver, each bucket a store names once. The fs
// driver signs its URLs under keys and serves them on the API listener.
func openObjects(ctx context.Context, c objectstore.Config, keys *crypt.Keyring) (objects, error) {
	o := objects{close: func() {}}
	var open func(bucket string) (objectstore.Bucket, error)
	switch c.Driver {
	case objectstore.DriverS3:
		open = func(bucket string) (objectstore.Bucket, error) {
			return s3.New(ctx, c.S3, bucket, c.PartSize) //nolint:wrapcheck // names the driver
		}
	case objectstore.DriverGCS:
		open = func(bucket string) (objectstore.Bucket, error) {
			return gcs.New(
				ctx,
				c.GCS,
				bucket,
				c.PartSize,
				gcs.Options{},
			) //nolint:wrapcheck // as above
		}
	case objectstore.DriverAzureBlob:
		open = func(bucket string) (objectstore.Bucket, error) {
			return azureblob.New(
				c.AzureBlob,
				bucket,
				c.PartSize,
				azureblob.Options{},
			) //nolint:wrapcheck // as above
		}
	default:
		// Validate allows no other driver, and fs no bucket of a store's
		// own, so open runs once.
		open = func(string) (objectstore.Bucket, error) {
			b, err := fs.New(c.FS, keys)
			if err == nil {
				o.routes, o.close = b.Routes, func() { _ = b.Close() }
			}
			return b, err //nolint:wrapcheck // as above
		}
	}
	s, err := objectstore.New(c, open)
	if err != nil {
		return objects{}, fmt.Errorf("objectstore: %w", err)
	}
	o.store = s
	return o, nil
}
