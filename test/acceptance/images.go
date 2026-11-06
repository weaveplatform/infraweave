package acceptance

import (
	"errors"
	"fmt"
	"os"
)

// Every container image the suite starts, pinned by the digest of its
// multi-platform index so a run is repeatable on any host. deps-refresh.yml
// moves each pin to its image's newest release, a PostgreSQL pin within its
// major; the tag names the release for a reader, and the digest is what
// Docker pulls. internal/platform/sql/sqltest runs on the same pins.
const (
	// ImagePostgres serves @postgres: PostgreSQL 18 (decision 0005).
	ImagePostgres = "postgres:18.6@sha256:bd5dfa494a5a259704365813dbfa5fa6b3e69be7348d43b547f540c730478583"
	// ImagePostgresNext is the next major, its beta or release candidate
	// until it is generally available, which the postgres-next job runs
	// (decision 0016).
	ImagePostgresNext = "postgres:19rc1@sha256:f6bab6213c486e940e310a88490935cbc71b88f6c19fee3cd2ab14b3b02ff65b"
	// ImagePostgresBelowFloor is PostgreSQL 17, one major below decision
	// 0005's floor, which every process mode must refuse.
	ImagePostgresBelowFloor = "postgres:17.11@sha256:748a18a54d3842d43bc156f0840d78029b8d349e0feca98c12b9c4ce3404a6b3"
	// ImageObjectStore serves @objectstore: RustFS, the S3-compatible store
	// decision 0010 names.
	ImageObjectStore = "rustfs/rustfs:1.0.4@sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2"
	// ImageFakeGCS is the Cloud Storage emulator objectstoretest's gcs
	// suite runs against.
	ImageFakeGCS = "fsouza/fake-gcs-server:1.56.1@sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef"
	// ImageAzurite is the Blob Storage emulator objectstoretest's
	// azureblob suite runs against.
	ImageAzurite = "mcr.microsoft.com/azure-storage/azurite:3.37.0@sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5"
)

// ErrPostgresEnv marks a PostgresEnv value that names no image.
var ErrPostgresEnv = errors.New("unknown " + PostgresEnv)

// PostgresEnv selects the PostgreSQL major @postgres and sqltest start:
// unset or empty for ImagePostgres, next for ImagePostgresNext.
const PostgresEnv = "INFRAWEAVE_POSTGRES"

// Postgres is the PostgreSQL image this run starts. Any other value of
// PostgresEnv is an error rather than the floor, so a misspelt next never
// passes on the floor unnoticed.
func Postgres() (string, error) {
	switch v := os.Getenv(PostgresEnv); v {
	case "":
		return ImagePostgres, nil
	case "next":
		return ImagePostgresNext, nil
	default:
		return "", fmt.Errorf("%w: %q: want next or unset", ErrPostgresEnv, v)
	}
}
