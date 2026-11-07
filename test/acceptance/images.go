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
	ImagePostgres = "postgres:18.6@sha256:e3d24f6906d0760bf4da0dae872415391ededefad23fd471c47dc53b0ddf3c42"
	// ImagePostgresNext is the next major, its beta or release candidate
	// until it is generally available, which the postgres-next job runs
	// (decision 0016).
	ImagePostgresNext = "postgres:19rc1@sha256:4b3e4b2edcf554492dd1c5e008237208c1d6e6faf2f15c24bf5fc5c4bcf0a21d"
	// ImagePostgresBelowFloor is PostgreSQL 17, one major below decision
	// 0005's floor, which every process mode must refuse.
	ImagePostgresBelowFloor = "postgres:17.11@sha256:ff87138949ad9a46f32ff23bd6852b52ce024a13dffe9b4087be98292cdd3b4f"
	// ImageObjectStore serves @objectstore: RustFS, the S3-compatible store
	// decision 0010 names.
	ImageObjectStore = "rustfs/rustfs:1.0.4@sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2"
	// ImageFakeGCS is the Cloud Storage emulator objectstoretest's gcs
	// suite runs against.
	ImageFakeGCS = "fsouza/fake-gcs-server:1.56.1@sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef"
	// ImageAzurite is the Blob Storage emulator objectstoretest's
	// azureblob suite runs against.
	ImageAzurite = "mcr.microsoft.com/azure-storage/azurite:3.37.0@sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5"
	// ImageVault is the Vault whose Transit engine the vault KEK
	// provider's suite, the vault start and @vault run against.
	ImageVault = "hashicorp/vault:2.2.1@sha256:962a92876e168d68eaaa24b0b8b49bbce1cb63024060a67bba95cd158a366fc2"
	// ImageMoto is the AWS emulator whose KMS the awskms KEK provider's
	// suite runs against. LocalStack's image needs a licence token to
	// start, which no public pull request has.
	ImageMoto = "motoserver/moto:5.2.5@sha256:59a1965d845249b04def37a0e2565f9cc6c44543cb89b68e90f28f344b3b0d1a"
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
