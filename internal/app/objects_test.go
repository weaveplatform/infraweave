package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// nowhere is a bucket that answers nothing, for composing roles without
// a driver.
type nowhere struct{}

func (nowhere) Put(context.Context, string, io.Reader, int64) (objectstore.Object, error) {
	return objectstore.Object{}, nil
}

func (nowhere) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, objectstore.ErrNotFound
}

func (nowhere) Head(context.Context, string) (int64, error) { return 0, objectstore.ErrNotFound }
func (nowhere) Delete(context.Context, string) error        { return nil }

func (nowhere) SignURL(
	context.Context,
	string,
	string,
	time.Duration,
	objectstore.Constraints,
) (objectstore.Signed, error) {
	return objectstore.Signed{}, nil
}

var storeType = reflect.TypeFor[objectstore.Store]()

// holdsStore walks v and reports the path to the first value that is an
// objectstore.Store. It stops at the objectstore package itself, whose
// WriterOnly wrapper TestUnitWriterOnly proves cannot be asserted back.
func holdsStore(v reflect.Value, path string, seen map[uintptr]bool) string {
	if !v.IsValid() {
		return ""
	}
	if v.Type().Implements(storeType) && (v.Kind() != reflect.Interface || !v.IsNil()) {
		return path
	}
	if pkg := v.Type().PkgPath(); strings.HasSuffix(pkg, "/platform/objectstore") {
		return ""
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return ""
		}
		seen[v.Pointer()] = true
		return holdsStore(v.Elem(), path, seen)
	case reflect.Interface:
		if v.IsNil() {
			return ""
		}
		if v.Elem().Type().Implements(storeType) {
			return path
		}
		return holdsStore(v.Elem(), path, seen)
	case reflect.Struct:
		for i := range v.NumField() {
			if p := holdsStore(v.Field(i), path+"."+v.Type().Field(i).Name, seen); p != "" {
				return p
			}
		}
	}
	return ""
}

// TestUnitServingHoldsNoStore checks that serve's composition holds object
// storage only as an objectstore.Writer: nothing reachable from it is a
// Store, so serve can never delete an object (decision 0005).
func TestUnitServingHoldsNoStore(t *testing.T) {
	c := objectstore.Defaults()
	c.Driver = objectstore.DriverS3
	store, err := objectstore.New(
		c,
		func(string) (objectstore.Bucket, error) { return nowhere{}, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	b := serving(c, nil, nil, objects{store: store})
	if p := holdsStore(reflect.ValueOf(b), "Blobs", map[uintptr]bool{}); p != "" {
		t.Errorf("serve holds an objectstore.Store at %s", p)
	}
	if p := holdsStore(
		reflect.ValueOf(apiServer{blobs: b.Objects}),
		"apiServer",
		map[uintptr]bool{},
	); p != "" {
		t.Errorf("the API server holds an objectstore.Store at %s", p)
	}
	// The walk itself finds a Store where one is.
	if p := holdsStore(
		reflect.ValueOf(objects{store: store}),
		"objects",
		map[uintptr]bool{},
	); p != "objects.store" {
		t.Errorf("the walk missed the scheduler's Store: %q", p)
	}
}

// TestUnitOpenObjects checks that each driver opens from its settings,
// and that only fs serves routes on the API listener.
func TestUnitOpenObjects(t *testing.T) {
	for _, driver := range []string{objectstore.DriverS3, objectstore.DriverAzureBlob, objectstore.DriverFS} {
		t.Run(driver, func(t *testing.T) {
			c := objectstore.Defaults()
			c.Driver = driver
			c.S3.Bucket = "b"
			c.AzureBlob.AccountURL, c.AzureBlob.Container = "https://acct.blob.core.windows.net", "c"
			c.FS.Root, c.FS.URL = t.TempDir(), "https://infraweave.example.com"
			o, err := openObjects(t.Context(), c, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer o.close()
			if (o.routes != nil) != (driver == objectstore.DriverFS) {
				t.Errorf("routes %v", o.routes != nil)
			}
		})
	}
	// Cloud Storage's client needs Application Default Credentials, which
	// a test host may or may not have; either way the driver is the one
	// opened, and a failure names the object store.
	c := objectstore.Defaults()
	c.Driver, c.GCS.Bucket = objectstore.DriverGCS, "b"
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/credentials.json")
	if _, err := openObjects(
		t.Context(),
		c,
		nil,
	); err == nil ||
		!strings.Contains(err.Error(), "objectstore") {
		t.Errorf("gcs without credentials: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c.Driver, c.FS.Root = objectstore.DriverFS, filepath.Join(file, "objects")
	if _, err := openObjects(t.Context(), c, nil); err == nil {
		t.Error("an fs root under a file opened")
	}
}

// TestUnitBlobRoutes checks that the API listener serves the fs driver's
// /blobs/{token} routes when it is given them.
func TestUnitBlobRoutes(t *testing.T) {
	c := objectstore.Defaults()
	c.Driver, c.FS.Root, c.FS.URL = objectstore.DriverFS, t.TempDir(), "https://infraweave.example.com"
	o, err := openObjects(t.Context(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer o.close()
	cfg := local(t)
	h := API(
		discard(),
		cfg.Server,
		memory(cfg.Server),
		Domains{Auth: byAddress{}},
		nil,
		Blobs{Routes: o.routes},
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/blobs/not-a-token", nil),
	)
	if rec.Code != http.StatusForbidden {
		t.Errorf("GET /blobs/not-a-token: %d, want 403", rec.Code)
	}
}

// TestUnitServeNeedsAnObjectStore checks that every process mode refuses
// to start without objectstore.driver, before it reaches the database.
func TestUnitServeNeedsAnObjectStore(t *testing.T) {
	c := local(t)
	c.Database.URL = "postgres://u:p@127.0.0.1:1/x?sslmode=disable"
	c.ObjectStore.Driver = ""
	for _, m := range []Mode{ModeServe, ModeScheduler, ModeAll} {
		if err := Run(t.Context(), c, discard(), m); !errors.Is(err, objectstore.ErrNoDriver) {
			t.Errorf("%s: %v", m, err)
		}
	}
}
