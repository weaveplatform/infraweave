package deps

import (
	"errors"
	"fmt"
	"os"
)

// Every container image the suite pulls, pinned by the digest of its
// multi-platform index so a run is repeatable on any host. deps-refresh.yml
// moves each pin to its image's newest release, a PostgreSQL pin within its
// major; the tag names the release for a reader, and the digest is what
// Docker pulls. internal/platform/sql/sqltest runs on the same pins. The
// runner images the suite builds pin their base in their Dockerfiles under
// deploy/images, which Dependabot moves.
const (
	// ImagePostgres serves @postgres: PostgreSQL 18, the version floor.
	//
	// ref: decision 0005
	ImagePostgres = "postgres:18.6@sha256:e3d24f6906d0760bf4da0dae872415391ededefad23fd471c47dc53b0ddf3c42"
	// ImagePostgresNext is the next major, its beta or release candidate
	// until it is generally available, which the postgres-next job runs.
	//
	// ref: decision 0016
	ImagePostgresNext = "postgres:19rc1@sha256:4b3e4b2edcf554492dd1c5e008237208c1d6e6faf2f15c24bf5fc5c4bcf0a21d"
	// ImagePostgresBelowFloor is PostgreSQL 17, one major below the version
	// floor, which every process mode must refuse.
	//
	// ref: decision 0005
	ImagePostgresBelowFloor = "postgres:17.11@sha256:ff87138949ad9a46f32ff23bd6852b52ce024a13dffe9b4087be98292cdd3b4f"
	// ImageObjectStore serves @objectstore: RustFS, the S3-compatible store
	// the suite runs the s3 driver against.
	//
	// ref: decisions 0010 and 0016
	ImageObjectStore = "rustfs/rustfs:1.0.5@sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74"
	// ObjectStoreReady is ImageObjectStore's readiness path, which answers
	// 200 once its storage, IAM and locks are connected. /health answers
	// 200 as soon as the listener is up, while S3 requests still answer
	// 503 for a moment longer on a loaded host.
	ObjectStoreReady = "/health/ready"
	// ImageFakeGCS is the Cloud Storage emulator objectstoretest's gcs
	// suite runs against.
	ImageFakeGCS = "fsouza/fake-gcs-server:1.56.1@sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef"
	// ImageAzurite is the Blob Storage emulator objectstoretest's
	// azureblob suite runs against.
	ImageAzurite = "mcr.microsoft.com/azure-storage/azurite:3.37.0@sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5"
	// ImageVault is the Vault whose Transit engine the vault KEK
	// provider's suite, the vault start and @vault run against.
	ImageVault = "hashicorp/vault:2.2.1@sha256:962a92876e168d68eaaa24b0b8b49bbce1cb63024060a67bba95cd158a366fc2"
	// ImageGitea serves @vcs: a Git host with real repositories, webhooks
	// and commit statuses, in place of hosted VCS providers.
	//
	// ref: decision 0016
	ImageGitea = "gitea/gitea:28.3.0@sha256:8967bf9deaf3c5c4adae13519436dc1b20c646ac83fe37ae810c933a1045227d"
	// ImageMoto is the AWS emulator whose KMS the awskms KEK provider's
	// suite runs against. LocalStack's image needs a licence token to
	// start, which no public pull request has.
	ImageMoto = "motoserver/moto:5.2.5@sha256:59a1965d845249b04def37a0e2565f9cc6c44543cb89b68e90f28f344b3b0d1a"
	// ImageBitbucketDC is the Bitbucket Data Center the scheduled
	// vcs-lab job starts with its licence, since Atlassian hosts none;
	// the job reads the pin from this line.
	//
	// ref: decision 0016
	ImageBitbucketDC = "atlassian/bitbucket:10.5.2@sha256:3a11bf2e699423aed764de5a587f231860c20261e28f9e42e7df290a3cc904a9"
	// ImageGitLabCE is the GitLab the scheduled vcs-lab job starts, since
	// GitLab.com offers the group and project access tokens the lab mints
	// only on paid tiers; the job reads the pin from this line.
	//
	// ref: decision 0016
	ImageGitLabCE = "gitlab/gitlab-ce:19.5.4-ce.0@sha256:fde2dacdec6307a142c3f28bc07c62e534bccf47bc7ffb3a4e81a90e1cf0c5b7"
	// ImageSpaceliftRunnerTerraform is Spacelift's public Terraform runner
	// image, a weekly rebuild of a release, which a stack names as its
	// runner image to show that an image meeting the runner image contract
	// runs unchanged.
	//
	// ref: decision 0008 § "Runner images"
	ImageSpaceliftRunnerTerraform = "public.ecr.aws/spacelift/runner-terraform:v2.5.1_20261109@sha256:a66ede89510abef9006667af10dce1a9825315490091bba16696c14eafba2c04"
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
