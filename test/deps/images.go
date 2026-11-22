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
	// ImageK3s is the Terraform-owned local Kubernetes control-plane fixture.
	ImageK3s = "rancher/k3s:v1.37.2-k3s1@sha256:21ea6e0fac4e3930c2ce8aa40ddd41a8b18cb3e9a0222c030dc005e7e88c4332"
	// ImagePostgres serves @postgres: PostgreSQL 18, the version floor.
	//
	// ref: decision 0005
	ImagePostgres = "postgres:18.7@sha256:eb63372c1e05c755f55fa30cbd07aecc443676fc6c0fa90b9002d01537841e1a"
	// ImagePostgresNext is the next major, its beta or release candidate
	// until it is generally available, which the postgres-next job runs.
	//
	// ref: decision 0016
	ImagePostgresNext = "postgres:19.0@sha256:bb6e70add742cd868fb62cb3b2f67d1dd2bdd2b65a521e778420748753687c60"
	// ImagePostgresBelowFloor is PostgreSQL 17, one major below the version
	// floor, which every process mode must refuse.
	//
	// ref: decision 0005
	ImagePostgresBelowFloor = "postgres:17.12@sha256:b60e90042aa6b8dbecd58e224b3e36114dbf8ad754465264c2476ad58a86e45c"
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
	ImageAzurite = "mcr.microsoft.com/azure-storage/azurite:3.38.0@sha256:c26024d62bf4a5b88a40968afd217234c00e1fc9a4790dc6105d1ca7979f6eec"
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
	ImageGitLabCE = "gitlab/gitlab-ce:19.6.1-ce.0@sha256:d3727f8b1a1ee6de44916bb9113f3dc40339dba2d6a6ad6ef331d0f239004056"
	// ImageSpaceliftRunnerTerraform is Spacelift's public Terraform runner
	// image, a weekly rebuild of a release, which a stack names as its
	// runner image to show that an image meeting the runner image contract
	// runs unchanged. Its official GHCR backup avoids ECR Public's anonymous
	// data allowance on shared CI egress. Upstream publishes the same image:
	// https://github.com/spacelift-io/runner-terraform#docker-repository
	//
	// ref: decision 0008 § "Runner images"
	ImageSpaceliftRunnerTerraform = "ghcr.io/spacelift-io/runner-terraform:v2.5.1_20261116@sha256:b48969aba860b11f7fbfc12acdd3be539d8befeea64edeef544655a50f819a35"
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
