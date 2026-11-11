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
| `kek/google` | the `gcpkms` KEK and signing live tests, run by a maintainer | a key ring, `infraweave-ci`, with a symmetric CryptoKey, `kek`, and an `EC_SIGN_P256_SHA256` CryptoKey, `signing`, whose versions are destroyed a day after the key is; Cloud KMS never deletes a key ring, so a later apply adopts it |
| `kek/azure` | the `azurekv` KEK and signing live tests, run by a maintainer | a resource group with a vault that authorizes through Azure RBAC and is purged when destroyed, an RSA key, `kek`, an EC key on P-256, `signing`, and Key Vault Crypto Officer on the vault for whoever applies the root |
| `kek/aws` | the `awskms` signing live test, run by a maintainer | an `ECC_NIST_P256` KMS key for `SIGN_VERIFY`, deleted seven days after it is destroyed |

Each root takes the lab project, account or subscription from its
provider's own credentials (`gcloud auth application-default login`, an
AWS profile, `az login`) and from a `terraform.tfvars` that stays out of
the repository: Google's `project_id`, Azure's `subscription_id`, and on
AWS, for `objectstore/aws`, the ARN of an existing GitHub OIDC provider, if
the account has one. State is local and ignored; the provider lock files are
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
with any of its secrets unset reports "not configured" and fails.

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
