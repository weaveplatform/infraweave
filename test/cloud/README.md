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
| `kek/google` | the `gcpkms` live test, run by a maintainer | a key ring, `infraweave-ci`, with a symmetric CryptoKey, `kek`, whose versions are destroyed a day after the key is; Cloud KMS never deletes a key ring, so a later apply adopts it |
| `kek/azure` | the `azurekv` live test, run by a maintainer | a resource group with a vault that authorizes through Azure RBAC and is purged when destroyed, an RSA key, `kek`, and Key Vault Crypto Officer on the vault for whoever applies the root |

Each root takes the lab project, account or subscription from its
provider's own credentials (`gcloud auth application-default login`, an
AWS profile, `az login`) and from a `terraform.tfvars` that stays out of
the repository: Google's `project_id`, Azure's `subscription_id`, and on
AWS the ARN of an existing GitHub OIDC provider, if the account has one. State is local and ignored; the provider lock files are
committed, so every machine resolves the same provider builds.

```sh
cd test/cloud/objectstore/google   # or aws, azure
terraform init
terraform plan -out=lab.tfplan
terraform apply lab.tfplan
terraform output                   # the workflow's repository variables
```

The `kek` roots' outputs are the live tests' variables:
`INFRAWEAVE_TEST_GCPKMS_KEY` is `key_name`, and
`INFRAWEAVE_TEST_AZUREKV_URL` is `key_url`. With
`INFRAWEAVE_TEST_RECORD=1` as well, each live test rewrites the recorded
fixture its unit test replays, with the project and vault names redacted
and every UUID zeroed.

The writer identity has no delete permission, so a case that asserts its
delete is refused proves `serve` needs none (decision 0005).
