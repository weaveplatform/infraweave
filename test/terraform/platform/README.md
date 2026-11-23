# Cloud test labs

The infrastructure that decision 0016's scheduled cloud jobs run against, as
Terraform roots, one per job and cloud. Nothing here is part of infraweave or
its reference deployments (`deploy/`); it exists only so a scheduled
workflow on `main` can reach a real cloud through GitHub's OIDC token, with
no stored key.

| Root | Job | Creates |
|---|---|---|
| `objectstore/google` | `objectstore-cloud` | a bucket that deletes objects after a day; a workload identity pool and provider admitting only this repository's `objectstore-cloud.yml` on `main`; writer and deleter service accounts with decision 0020's custom roles |
| `objectstore/aws` | `objectstore-cloud` | a bucket that deletes objects (and incomplete multipart uploads) after a day; the GitHub OIDC provider, unless the account has one; writer and deleter roles with decision 0020's grants, assumable only from the repository's `objectstore-cloud` environment |
| `objectstore/azure` | `objectstore-cloud` | a resource group with a storage account that allows no shared keys and deletes blobs after a day; writer and deleter user-assigned identities with decision 0020's custom roles on the account, whose federated credentials admit only the repository's `objectstore-cloud` environment; nothing is registered in the tenant's directory |
| `workers-identity/aws` | `managed-cloud-workers` | the GitHub OIDC provider, unless the account has one; a role assumable only from the repository's `workers-cloud` environment, for four-hour sessions, that builds EKS clusters, their VPC networking and log groups, and IAM roles under one path and name prefix that must carry the root's permissions boundary, in one region, and reads the quotas and owner tags preflight and cleanup check |
| `workers-identity/azure` | `managed-cloud-workers` | a resource group for the worker stages; a user-assigned identity, in a resource group of its own, whose federated credential admits only the repository's `workers-cloud` environment, with Contributor on the worker group, the right to assign AKS's roles there to managed identities, and quota, inventory and cluster reads on the subscription; nothing is registered in the tenant's directory |
| `workers-identity/google` | `managed-cloud-workers` | a workload identity pool and provider admitting only this repository's `managed-cloud-workers.yml` on `main` in the `workers-cloud` environment; a service account with GKE, network and asset inventory roles on the project that may act only as the node service account the root also creates |
| `kek/google` | the `gcpkms` KEK and signing live tests, run by a maintainer | a key ring, `infraweave-ci`, with a symmetric CryptoKey, `kek`, and an `EC_SIGN_P256_SHA256` CryptoKey, `signing`, whose versions are destroyed a day after the key is; Cloud KMS never deletes a key ring, so a later apply adopts it |
| `kek/azure` | the `azurekv` KEK and signing live tests, run by a maintainer | a resource group with a vault that authorizes through Azure RBAC and is purged when destroyed, an RSA key, `kek`, an EC key on P-256, `signing`, and Key Vault Crypto Officer on the vault for whoever applies the root |
| `kek/aws` | the `awskms` signing live test, run by a maintainer | an `ECC_NIST_P256` KMS key for `SIGN_VERIFY`, deleted seven days after it is destroyed |

Each root takes the lab project, account or subscription from its
provider's own credentials (`gcloud auth application-default login`, an
AWS profile, `az login`) and from a `terraform.tfvars` that stays out of
the repository: Google's `project_id`, Azure's `subscription_id`, and on
AWS, for `objectstore/aws` and `workers-identity/aws`, the ARN of an
existing GitHub OIDC provider, if the account has one, including one the
other root created. State is local and ignored; the provider lock files are
committed, so every machine resolves the same provider builds.

```sh
cd test/terraform/cloud/objectstore/google   # or aws, azure
terraform init
terraform plan -out=lab.tfplan
terraform apply lab.tfplan
terraform output                   # the workflow's environment secrets
```

The `objectstore` roots' outputs are the secrets of `objectstore-cloud.yml`'s
`objectstore-cloud` environment, set with `gh secret set --env
objectstore-cloud`. None is a credential, but they name the lab's accounts,
and the public repository's logs mask secrets and print variables. A cloud
with any of its secrets unset is skipped: its job passes having run
nothing, and the run's summary names the secrets it lacks.

| Root | Output | Secret |
|---|---|---|
| `objectstore/aws` | `bucket`, `region` | `OBJECTSTORE_AWS_BUCKET`, `OBJECTSTORE_AWS_REGION` |
| | `roles.writer`, `roles.deleter` | `OBJECTSTORE_AWS_WRITER_ROLE`, `OBJECTSTORE_AWS_DELETER_ROLE` |
| `objectstore/google` | `bucket`, `workload_identity_provider` | `OBJECTSTORE_GCS_BUCKET`, `OBJECTSTORE_GCS_WORKLOAD_IDENTITY_PROVIDER` |
| | `service_accounts.writer`, `service_accounts.deleter` | `OBJECTSTORE_GCS_WRITER`, `OBJECTSTORE_GCS_DELETER` |
| `objectstore/azure` | `account_url`, `container` | `OBJECTSTORE_AZURE_ACCOUNT_URL`, `OBJECTSTORE_AZURE_CONTAINER` |
| | `tenant_id`, `subscription_id` | `OBJECTSTORE_AZURE_TENANT_ID`, `OBJECTSTORE_AZURE_SUBSCRIPTION_ID` |
| | `client_ids.writer`, `client_ids.deleter` | `OBJECTSTORE_AZURE_WRITER_CLIENT_ID`, `OBJECTSTORE_AZURE_DELETER_CLIENT_ID` |

The workflow's jobs run in the `objectstore-cloud` environment, whose
token subject the AWS and Azure trust admits; give the environment a
deployment branch rule that admits only `main`. Each cloud runs the
contract as the writer, with the delete case replaced by one that asserts
the cloud refuses it, then as the deleter, which removes what it wrote;
the lifecycle rule clears the writer's objects.

The `workers-identity` roots' outputs are the secrets of
`managed-cloud-workers.yml`'s `workers-cloud` environment, set with `gh
secret set --env workers-cloud`. A batch that selects a cloud with any of
these secrets unset fails preflight; no cloud is skipped.

| Root | Output | Secret |
|---|---|---|
| `workers-identity/aws` | `role_arn`, `region` | `WORKERS_AWS_ROLE`, `WORKERS_AWS_REGION` |
| `workers-identity/azure` | `client_id`, `tenant_id` | `WORKERS_AZURE_CLIENT_ID`, `WORKERS_AZURE_TENANT_ID` |
| | `subscription_id` | `WORKERS_AZURE_SUBSCRIPTION_ID` |
| `workers-identity/google` | `workload_identity_provider`, `service_account` | `WORKERS_GOOGLE_WORKLOAD_IDENTITY_PROVIDER`, `WORKERS_GOOGLE_SERVICE_ACCOUNT` |

Their other outputs are the bounds the worker stages build within, and go
into each cloud's `WORKERS_<CLOUD>_INPUTS` file rather than a secret of
their own. On AWS, `stage_scope` names the prefix every EKS cluster, IAM
role and IAM policy, and every log group under `/aws/eks/`, must start
with, the path every role and policy must sit under, and the permissions
boundary every role must carry; the role creates no cluster without the
`io.infraweave.lab.owner` tag and deletes no VPC resource without it. On
Azure, `resource_group_name` and `location` are the stage identifiers; the
identity changes nothing outside that group, and the stages' provider
registers no resource providers, which applying the root does instead. On
Google, `node_service_account` is the account stage clusters run their
nodes as; the service account may act as no other, so a cluster that
names none is refused. Setting `grantable_roles` lets a stage grant those
project roles, and no others, to Kubernetes service accounts through
Workload Identity Federation for GKE.

The `kek` roots' outputs are the live tests' variables:

| Root | Output | Variable | Test |
|---|---|---|---|
| `kek/google` | `key_name` | `INFRAWEAVE_TEST_GCPKMS_KEY` | `TestAccGCPKMSLive` in `crypt/kek/gcpkms` |
| | `signing_key` | `INFRAWEAVE_TEST_GCPKMS_SIGNING_KEY` | `TestAccGCPKMSLive` in `crypt/signer/gcpkms` |
| `kek/azure` | `key_url` | `INFRAWEAVE_TEST_AZUREKV_URL` | `TestAccAzureKVLive` in `crypt/kek/azurekv` |
| | `signing_key_id` | `INFRAWEAVE_TEST_AZUREKV_SIGNING_KEY` | `TestAccAzureKVLive` in `crypt/signer/azurekv` |
| `kek/aws` | `signing_key_arn` | `INFRAWEAVE_TEST_AWSKMS_SIGNING_KEY` | `TestAccAWSKMSLive` in `crypt/signer/awskms` |

With `INFRAWEAVE_TEST_RECORD=1` as well, each Google and Azure live test
rewrites the recorded fixture its unit test replays, with the project and
vault names redacted and every UUID zeroed. The signing backends' replay
tests skip until their fixture is recorded.

The writer identity has no delete permission, so a case that asserts its
delete is refused proves `serve` needs none (decision 0005).

## Worker hosting acceptance

The [managed Kubernetes worker hosting contract](../../../docs/implementation/worker-hosting-contracts.md)
specifies the future `workers/{aws,azure,google}` roots and their shared modules
under `deploy/terraform/modules/workers/{aws,azure,google}`. Each cloud delivery
must include both its module and its real acceptance root, with separate
cluster/deployment state, reference-only Secrets, verified immutable artifacts,
explicit cloud IDs, owner/TTL inventory and ordered destroy plus cloud absence
readback. Existing shared infrastructure retains its external ownership. These
roots are not implemented or proven by this documentation change; their owners
supply verified provider locks and actual apply/run/destroy instructions.

The general credential-discovery examples above do not apply to worker roots:
worker inputs explicitly name the cloud account/subscription/project, tenant
where applicable, region/location, network and cluster. The caller's short-lived
identity must match those IDs before mutation. Live applies keep the protected
environment and maintainer approval requirement from ADR 0020.

The `workers-*` batches of [infraweave-lab](../lab/README.md#managed-cloud-worker-batches)
apply these roots: each cloud's `cluster/` then `deployment/` stage under one
owner, with private per-stage state, preflight of credentials and quota before
any apply, and per-cloud cleanup with cloud readback. Until a cloud's root is
committed here, its batch fails preflight rather than skipping.
