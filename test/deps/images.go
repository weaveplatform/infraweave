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
	ImagePostgres = "postgres:18.7@sha256:2ea8630df176ad090ec92136dee850397c5af8781e4bcf2e4162593691c61bbc"
	// ImagePostgresNext is the next major, its beta or release candidate
	// until it is generally available, which the postgres-next job runs.
	//
	// ref: decision 0016
	ImagePostgresNext = "postgres:19rc2@sha256:15924313d5606f326836976643a5f681a64fb30f591e79fcb15e160979323802"
	// ImagePostgresBelowFloor is PostgreSQL 17, one major below the version
	// floor, which every process mode must refuse.
	//
	// ref: decision 0005
	ImagePostgresBelowFloor = "postgres:17.12@sha256:0d938e975177bef9f1f61f3f9dbcf2e0aa0a139568f66d3415ed8892eb97f6ac"
	// ImageObjectStore serves @objectstore: RustFS, the S3-compatible store
	// the suite runs the s3 driver against.
	//
	// ref: decisions 0010 and 0016
	ImageObjectStore = "rustfs/rustfs:1.0.6@sha256:ee717072290bec9b11abfc0365f8310b00df1cd9b3639943edae47bdef05044a"
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
	ImageVault = "hashicorp/vault:2.2.2@sha256:dfa48c2fa2b0099ccb47681ec5c4804bf5aec9d6879ecfee07ad4b92a5427ad6"
	// ImageGitea serves @vcs: a Git host with real repositories, webhooks
	// and commit statuses, in place of hosted VCS providers.
	//
	// ref: decision 0016
	ImageGitea = "gitea/gitea:28.4.0@sha256:aa90e8f00b755b0d665214ab9c14447ea44d3190dbe6b6d0cb46cd9c4f55d309"
	// ImageMoto is the AWS emulator whose KMS the awskms KEK provider's
	// suite runs against. LocalStack's image needs a licence token to
	// start, which no public pull request has.
	ImageMoto = "motoserver/moto:5.2.5@sha256:59a1965d845249b04def37a0e2565f9cc6c44543cb89b68e90f28f344b3b0d1a"
	// ImageBitbucketDC is the Bitbucket Data Center the scheduled
	// vcs-lab job starts with its licence, since Atlassian hosts none;
	// the job reads the pin from this line.
	//
	// ref: decision 0016
	ImageBitbucketDC = "atlassian/bitbucket:9.4.7@sha256:ecd0fc5337ac1bf479d395ea5129625f829735cdcb81401bac82d2ed7b6c64d1"
	// ImageGitLabCE is the GitLab the scheduled vcs-lab job starts, since
	// GitLab.com offers the group and project access tokens the lab mints
	// only on paid tiers; the job reads the pin from this line.
	//
	// ref: decision 0016
	ImageGitLabCE = "gitlab/gitlab-ce:19.5.5-ce.0@sha256:392e6bd5bf118ae520e4038261a5ae39aea05de0d2299a162b4c822a9684cf9b"
	// ImageSpaceliftRunnerTerraform is Spacelift's public Terraform runner
	// image, a weekly rebuild of a release, which a stack names as its
	// runner image to show that an image meeting the runner image contract
	// runs unchanged.
	//
	// ref: decision 0008 § "Runner images"
	ImageSpaceliftRunnerTerraform = "public.ecr.aws/spacelift/runner-terraform:v2.5.1_20261116@sha256:b48969aba860b11f7fbfc12acdd3be539d8befeea64edeef544655a50f819a35"
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
