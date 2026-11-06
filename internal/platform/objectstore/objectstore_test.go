package objectstore_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// recorder is a bucket that records the calls it gets.
type recorder struct {
	name  string
	calls []string
	ttl   time.Duration
}

func (r *recorder) Put(
	_ context.Context,
	key string,
	_ io.Reader,
	size int64,
) (objectstore.Object, error) {
	r.calls = append(r.calls, "put "+key)
	return objectstore.Object{Size: size}, nil
}

func (r *recorder) Get(_ context.Context, key string) (io.ReadCloser, error) {
	r.calls = append(r.calls, "get "+key)
	return io.NopCloser(strings.NewReader("")), nil
}

func (r *recorder) Head(_ context.Context, key string) (int64, error) {
	r.calls = append(r.calls, "head "+key)
	return 0, nil
}

func (r *recorder) Delete(_ context.Context, key string) error {
	r.calls = append(r.calls, "delete "+key)
	return nil
}

func (r *recorder) SignURL(
	_ context.Context,
	method, key string,
	ttl time.Duration,
	_ objectstore.Constraints,
) (objectstore.Signed, error) {
	r.calls = append(r.calls, "sign "+method+" "+key)
	r.ttl = ttl
	return objectstore.Signed{URL: r.name + "/" + key}, nil
}

func TestUnitRouter(t *testing.T) {
	c := objectstore.Defaults()
	c.Buckets.States = "states-bucket"
	c.Buckets.Modules = "states-bucket"
	opened := map[string]*recorder{}
	s, err := objectstore.New(c, func(bucket string) (objectstore.Bucket, error) {
		if _, ok := opened[bucket]; ok {
			t.Errorf("bucket %q opened twice", bucket)
		}
		opened[bucket] = &recorder{name: bucket}
		return opened[bucket], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 {
		t.Fatalf("opened %d buckets, want the default and states-bucket", len(opened))
	}
	ctx := t.Context()
	if _, err := s.Put(ctx, objectstore.States, "acct/1", strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, objectstore.RunLogs, "acct/run/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Head(ctx, objectstore.Modules, "acct/m"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, objectstore.Uploads, "acct/u"); err != nil {
		t.Fatal(err)
	}
	signed, err := s.SignURL(
		ctx,
		http.MethodGet,
		objectstore.Inbox,
		"acct/i",
		0,
		objectstore.Constraints{},
	)
	if err != nil || signed.URL != "/inbox/acct/i" {
		t.Fatalf("SignURL: %+v, %v", signed, err)
	}
	if opened[""].ttl != 15*time.Minute {
		t.Errorf("a ttl of 0 signed for %s, want signed_url_ttl", opened[""].ttl)
	}
	if _, err := s.SignURL(
		ctx,
		http.MethodPut,
		objectstore.Inbox,
		"acct/i",
		time.Second,
		objectstore.Constraints{Size: 1},
	); err != nil {
		t.Fatal(err)
	}
	if opened[""].ttl != time.Second {
		t.Errorf("an explicit ttl signed for %s", opened[""].ttl)
	}
	want := map[string][]string{
		"states-bucket": {"put states/acct/1", "head modules/acct/m"},
		"": {
			"get run-logs/acct/run/1",
			"delete uploads/acct/u",
			"sign GET inbox/acct/i",
			"sign PUT inbox/acct/i",
		},
	}
	for bucket, calls := range want {
		if got := strings.Join(opened[bucket].calls, ", "); got != strings.Join(calls, ", ") {
			t.Errorf("bucket %q got %s", bucket, got)
		}
	}

	for name, call := range map[string]func() error{
		"unknown store": func() error { _, err := s.Head(ctx, "elsewhere", "k"); return err },
		"bad key put":   func() error { _, err := s.Put(ctx, objectstore.States, "../x", nil, 0); return err },
		"bad key get":   func() error { _, err := s.Get(ctx, objectstore.States, ""); return err },
		"bad key head":  func() error { _, err := s.Head(ctx, objectstore.States, "/x"); return err },
		"bad key del":   func() error { return s.Delete(ctx, objectstore.States, "a//b") },
		"bad key sign": func() error {
			_, err := s.SignURL(ctx, http.MethodGet, objectstore.States, "a/./b", 0, objectstore.Constraints{})
			return err
		},
		"DELETE URL": func() error {
			_, err := s.SignURL(ctx, http.MethodDelete, objectstore.States, "a", 0, objectstore.Constraints{})
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := objectstore.New(
		c,
		func(string) (objectstore.Bucket, error) { return nil, errors.New("no bucket") },
	); err == nil {
		t.Error("a bucket that fails to open")
	}
}

func TestUnitCheckKey(t *testing.T) {
	for _, k := range []string{"a", "acct/01JC/state", "a.b/c-d_e"} {
		if err := objectstore.CheckKey(k); err != nil {
			t.Errorf("%q: %v", k, err)
		}
	}
	for _, k := range []string{"", "/a", "a/", "a//b", "./a", "a/..", "a\\b", "a\x00"} {
		if err := objectstore.CheckKey(k); !errors.Is(err, objectstore.ErrKey) {
			t.Errorf("%q: %v", k, err)
		}
	}
}

// TestUnitWriterOnly checks that serve's view of the store has no path
// back to Delete.
func TestUnitWriterOnly(t *testing.T) {
	rec := &recorder{}
	s, err := objectstore.New(
		objectstore.Defaults(),
		func(string) (objectstore.Bucket, error) { return rec, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	w := objectstore.WriterOnly(s)
	if _, ok := w.(objectstore.Store); ok {
		t.Fatal("a Writer asserts to a Store")
	}
	if _, ok := w.(interface {
		Delete(context.Context, objectstore.Name, string) error
	}); ok {
		t.Fatal("a Writer has Delete")
	}
	ctx := t.Context()
	_, _ = w.Put(ctx, objectstore.States, "a", strings.NewReader(""), 0)
	_, _ = w.Get(ctx, objectstore.States, "a")
	_, _ = w.Head(ctx, objectstore.States, "a")
	_, _ = w.SignURL(ctx, http.MethodGet, objectstore.States, "a", 0, objectstore.Constraints{})
	if len(rec.calls) != 4 {
		t.Errorf("calls %v", rec.calls)
	}
}

func TestUnitStores(t *testing.T) {
	if len(objectstore.Names()) != 11 {
		t.Errorf("%d stores, want decision 0005's 11", len(objectstore.Names()))
	}
	for _, n := range objectstore.Names() {
		want := n == objectstore.States || n == objectstore.PolicyInputs ||
			n == objectstore.RunArtifacts
		if n.Sealed() != want {
			t.Errorf("%s sealed %v", n, n.Sealed())
		}
	}
	r := objectstore.Defaults().Retention
	for n, want := range map[objectstore.Name]time.Duration{
		objectstore.RunLogs: 60 * 24 * time.Hour, objectstore.Uploads: 24 * time.Hour,
		objectstore.States: 0, objectstore.Deliveries: 0, objectstore.FlowsEvents: 0,
	} {
		if got := r.Of(n); got != want {
			t.Errorf("%s retention %s, want %s", n, got, want)
		}
	}
}

func TestUnitConfig(t *testing.T) {
	valid := func(driver string) objectstore.Config {
		c := objectstore.Defaults()
		c.Driver = driver
		c.S3.Bucket, c.GCS.Bucket = "b", "b"
		c.AzureBlob.AccountURL, c.AzureBlob.Container = "https://acct.blob.core.windows.net", "c"
		c.FS.Root, c.FS.URL = "/var/lib/infraweave/objects", "https://infraweave.example.com"
		return c
	}
	for _, d := range []string{"", "s3", "gcs", "azureblob", "fs"} {
		if err := valid(d).Validate(); err != nil {
			t.Errorf("%q: %v", d, err)
		}
	}
	if err := objectstore.Defaults().Require(); !errors.Is(err, objectstore.ErrNoDriver) {
		t.Errorf("no driver: %v", err)
	}
	if err := valid("fs").Require(); err != nil {
		t.Errorf("fs: %v", err)
	}
	for name, tc := range map[string]struct {
		edit func(*objectstore.Config)
		want string
	}{
		"unknown driver": {func(c *objectstore.Config) { c.Driver = "ftp" }, "objectstore.driver"},
		"s3 bucket":      {func(c *objectstore.Config) { c.Driver = "s3"; c.S3.Bucket = "" }, "objectstore.s3.bucket"},
		"s3 encryption":  {func(c *objectstore.Config) { c.Driver = "s3"; c.S3.Encryption = "aes" }, "objectstore.s3.encryption"},
		"kms key":        {func(c *objectstore.Config) { c.Driver = "s3"; c.S3.Encryption = "kms" }, "kms_key_id"},
		"gcs bucket":     {func(c *objectstore.Config) { c.Driver = "gcs"; c.GCS.Bucket = "" }, "objectstore.gcs.bucket"},
		"azure url":      {func(c *objectstore.Config) { c.Driver = "azureblob"; c.AzureBlob.AccountURL = "acct" }, "account_url"},
		"azure container": {
			func(c *objectstore.Config) { c.Driver = "azureblob"; c.AzureBlob.Container = "" }, "container",
		},
		"fs root":    {func(c *objectstore.Config) { c.Driver = "fs"; c.FS.Root = "" }, "objectstore.fs.root"},
		"fs url":     {func(c *objectstore.Config) { c.Driver = "fs"; c.FS.URL = "/blobs" }, "objectstore.fs.url"},
		"fs buckets": {func(c *objectstore.Config) { c.Driver = "fs"; c.Buckets.States = "x" }, "objectstore.buckets"},
		"part size":  {func(c *objectstore.Config) { c.PartSize = 1 << 20 }, "part_size"},
		"ttl":        {func(c *objectstore.Config) { c.SignedURLTTL = 0 }, "signed_url_ttl"},
		"retention":  {func(c *objectstore.Config) { c.Retention.Inbox = -time.Hour }, "retention.inbox"},
	} {
		t.Run(name, func(t *testing.T) {
			c := valid("")
			tc.edit(&c)
			err := c.Validate()
			if !errors.Is(err, objectstore.ErrConfig) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}
