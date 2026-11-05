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
	ImagePostgres = "postgres:18.6@sha256:3a4264520e121176619f54ff867d12774bc81e95e7b84db58d63bccdb6144b81"
	// ImagePostgresNext is the next major, its beta or release candidate
	// until it is generally available, which the postgres-next job runs
	// (decision 0016).
	ImagePostgresNext = "postgres:19rc1@sha256:f6bab6213c486e940e310a88490935cbc71b88f6c19fee3cd2ab14b3b02ff65b"
	// ImagePostgresBelowFloor is PostgreSQL 17, one major below decision
	// 0005's floor, which every process mode must refuse.
	ImagePostgresBelowFloor = "postgres:17.11@sha256:3b0cda93be7c8dba2ed484d19393d93845dc3dafb88973e248ba8e6d99d784e1"
	// ImageObjectStore serves @objectstore: RustFS, the S3-compatible store
	// decision 0010 names.
	ImageObjectStore = "rustfs/rustfs:1.0.4@sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2"
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
