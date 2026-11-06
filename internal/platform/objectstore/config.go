package objectstore

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"time"
)

// The drivers objectstore.driver names.
const (
	DriverS3        = "s3"
	DriverGCS       = "gcs"
	DriverAzureBlob = "azureblob"
	DriverFS        = "fs"
)

// MinPartSize is the smallest part S3 accepts in a multipart upload,
// below which objectstore.part_size cannot go.
const MinPartSize = 5 << 20

// Config holds the object storage settings, keyed below objectstore.
type Config struct {
	// Driver is s3, gcs, azureblob or fs. serve, scheduler and all
	// require it.
	Driver    string          `mapstructure:"driver"`
	S3        S3Config        `mapstructure:"s3"`
	GCS       GCSConfig       `mapstructure:"gcs"`
	AzureBlob AzureBlobConfig `mapstructure:"azureblob"`
	FS        FSConfig        `mapstructure:"fs"`
	// Buckets gives a store a bucket, or on azureblob a container, of its
	// own.
	Buckets Buckets `mapstructure:"buckets"`
	// Retention is how long a store keeps an object it does not say
	// otherwise about; 0 keeps it until its record goes.
	Retention Retention `mapstructure:"retention"`
	// PartSize is the size above which a write goes in parts, and the
	// size of each part.
	PartSize int64 `mapstructure:"part_size"`
	// SignedURLTTL is how long a signed URL lasts unless its caller says.
	SignedURLTTL time.Duration `mapstructure:"signed_url_ttl"`
}

// S3Config holds the s3 driver's settings. Credentials come from the
// SDK's default chain, which reads static keys from AWS_ACCESS_KEY_ID and
// AWS_SECRET_ACCESS_KEY and workload identity from the environment.
type S3Config struct {
	Bucket string `mapstructure:"bucket"`
	// Endpoint is an S3-compatible service's URL, such as RustFS's.
	Endpoint  string `mapstructure:"endpoint"`
	Region    string `mapstructure:"region"`
	PathStyle bool   `mapstructure:"path_style"`
	// Encryption is the server-side encryption every write asks for:
	// none, s3 or kms.
	Encryption string `mapstructure:"encryption"`
	KMSKeyID   string `mapstructure:"kms_key_id"`
}

// GCSConfig holds the gcs driver's settings. Credentials come from
// Application Default Credentials.
type GCSConfig struct {
	Bucket string `mapstructure:"bucket"`
}

// AzureBlobConfig holds the azureblob driver's settings. Credentials come
// from azidentity's default chain.
type AzureBlobConfig struct {
	AccountURL string `mapstructure:"account_url"`
	Container  string `mapstructure:"container"`
}

// FSConfig holds the fs driver's settings.
type FSConfig struct {
	// Root is the directory objects live under, shared by every replica.
	Root string `mapstructure:"root"`
	// URL is the API listener's address as clients reach it, which signed
	// URLs point at: <url>/blobs/<token>.
	URL string `mapstructure:"url"`
}

// Buckets names a store's own bucket; "" keeps it in the driver's.
type Buckets struct {
	RunLogs          string `mapstructure:"run-logs"`
	RunArtifacts     string `mapstructure:"run-artifacts"`
	Workspaces       string `mapstructure:"workspaces"`
	States           string `mapstructure:"states"`
	Modules          string `mapstructure:"modules"`
	PolicyInputs     string `mapstructure:"policy-inputs"`
	Uploads          string `mapstructure:"uploads"`
	Deliveries       string `mapstructure:"deliveries"`
	Inbox            string `mapstructure:"inbox"`
	RunObservability string `mapstructure:"run-observability"`
	FlowsEvents      string `mapstructure:"flows-events"`
}

// Retention holds each store's retention. deliveries and flows-events
// have none here: their bodies live exactly as long as their rows, under
// their owners' settings.
type Retention struct {
	RunLogs          time.Duration `mapstructure:"run-logs"`
	RunArtifacts     time.Duration `mapstructure:"run-artifacts"`
	Workspaces       time.Duration `mapstructure:"workspaces"`
	States           time.Duration `mapstructure:"states"`
	Modules          time.Duration `mapstructure:"modules"`
	PolicyInputs     time.Duration `mapstructure:"policy-inputs"`
	Uploads          time.Duration `mapstructure:"uploads"`
	Inbox            time.Duration `mapstructure:"inbox"`
	RunObservability time.Duration `mapstructure:"run-observability"`
}

const day = 24 * time.Hour

// Defaults are decision 0005's settings, with no driver.
func Defaults() Config {
	return Config{
		S3: S3Config{Region: "us-east-1", Encryption: "none"},
		Retention: Retention{
			RunLogs:          60 * day,
			RunArtifacts:     30 * day,
			Workspaces:       7 * day,
			PolicyInputs:     7 * day,
			Uploads:          day,
			Inbox:            2 * day,
			RunObservability: 60 * day,
		},
		PartSize:     16 << 20,
		SignedURLTTL: 15 * time.Minute,
	}
}

// field returns the value of v's field whose mapstructure tag is n.
func field(v any, n Name) reflect.Value {
	rv := reflect.ValueOf(v)
	for i := range rv.NumField() {
		if rv.Type().Field(i).Tag.Get("mapstructure") == string(n) {
			return rv.Field(i)
		}
	}
	return reflect.Value{}
}

func (b Buckets) of(n Name) string {
	if f := field(b, n); f.IsValid() {
		return f.String()
	}
	return ""
}

// Of returns store n's retention; 0 keeps an object until its record goes.
func (r Retention) Of(n Name) time.Duration {
	if f := field(r, n); f.IsValid() {
		return time.Duration(f.Int())
	}
	return 0
}

// ErrConfig reports a setting the object store could not run with.
var ErrConfig = errors.New("invalid objectstore configuration")

// Validate rejects a setting the store could not run with: an unknown
// driver, a driver without its bucket or directory, a part below S3's
// minimum, a negative retention, a store's own bucket on fs. Errors name
// the key. No driver is valid here; Require refuses it.
func (c Config) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrConfig, fmt.Sprintf(format, args...)))
	}
	switch c.Driver {
	case "":
	case DriverS3:
		if c.S3.Bucket == "" {
			bad("objectstore.s3.bucket is required")
		}
		switch c.S3.Encryption {
		case "none", "s3":
		case "kms":
			if c.S3.KMSKeyID == "" {
				bad("objectstore.s3.kms_key_id is required with encryption kms")
			}
		default:
			bad("objectstore.s3.encryption must be none, s3 or kms, got %q", c.S3.Encryption)
		}
	case DriverGCS:
		if c.GCS.Bucket == "" {
			bad("objectstore.gcs.bucket is required")
		}
	case DriverAzureBlob:
		if _, err := url.ParseRequestURI(c.AzureBlob.AccountURL); err != nil {
			bad("objectstore.azureblob.account_url must be a URL, got %q", c.AzureBlob.AccountURL)
		}
		if c.AzureBlob.Container == "" {
			bad("objectstore.azureblob.container is required")
		}
	case DriverFS:
		if c.FS.Root == "" {
			bad("objectstore.fs.root is required")
		}
		if u, err := url.ParseRequestURI(c.FS.URL); err != nil || u.Host == "" {
			bad("objectstore.fs.url must be the API's absolute URL, got %q", c.FS.URL)
		}
		if c.Buckets != (Buckets{}) {
			bad("objectstore.buckets applies to s3, gcs and azureblob")
		}
	default:
		bad("objectstore.driver must be s3, gcs, azureblob or fs, got %q", c.Driver)
	}
	if c.PartSize < MinPartSize {
		bad("objectstore.part_size must be at least 5 MiB, got %d", c.PartSize)
	}
	if c.SignedURLTTL <= 0 {
		bad("objectstore.signed_url_ttl must be positive, got %s", c.SignedURLTTL)
	}
	for _, n := range Names() {
		if c.Retention.Of(n) < 0 {
			bad("objectstore.retention.%s must not be negative", n)
		}
	}
	return errors.Join(errs...)
}

// ErrNoDriver reports a process mode that stores objects started without
// objectstore.driver.
var ErrNoDriver = errors.New("objectstore.driver is required")

// Require refuses a configuration without a driver, which serve,
// scheduler and all need.
func (c Config) Require() error {
	if c.Driver == "" {
		return ErrNoDriver
	}
	return nil
}
