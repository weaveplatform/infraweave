# Platform scenario on Google Cloud

One infraweave platform in the dedicated lab project that
[`landing-zone/google`](../landing-zone/google) creates, behind public HTTPS
on a lab domain. The acceptance runner calls it at `server.public_url`, and
the worker clusters of every cloud attach to it there. The
[GCP page of `cloud-platform.drawio`](../../../../docs/diagrams/cloud-platform-google.drawio.svg)
draws it.

## What it creates

Every name carries `iw-platform-<suffix>` or `iw-<suffix>`, where the
suffix is the first eight hex digits of the owner's SHA-256, so batches in
the same project never collide.

| Component | Resources |
|---|---|
| Network | A VPC and subnet with Private Google Access; Cloud Run reaches it through Direct VPC egress, for private ranges only. Private services access (a reserved `/20` and the Service Networking peering) holds Cloud SQL's private IP. |
| Load balancer | A global external Application Load Balancer (`EXTERNAL_MANAGED`): a global address, a forwarding rule on 443, a target HTTPS proxy with a Google-managed certificate for `domain_name`, a URL map and a backend service on a serverless NEG to Cloud Run. With `dns_managed_zone`, the domain's A record in that Cloud DNS zone. |
| Cloud Run | A service running `infraweave all`, reachable only through the load balancer, its run.app URL disabled. CPU is always allocated and the instance count is fixed at two, because the scheduler runs in the background of each instance. |
| Artifact Registry | A remote Docker repository proxying `https://ghcr.io`, since Cloud Run cannot pull from GHCR. Cloud Run pulls the pinned digest through it. |
| Cloud SQL | PostgreSQL 18, Enterprise edition, zonal, private IP only, TLS required, IAM database authentication on. The database `infraweave` and the service account's IAM user, a member of `cloudsqlsuperuser`. |
| GCS | A bucket with uniform access and public access prevention, emptied on destroy. |
| Cloud KMS | A key ring with a symmetric key, `kek`, and an `EC_SIGN_P256_SHA256` key, `signing`. |
| Secret Manager | The server's configuration file, mounted into the service. |
| Identity | One runtime service account: `roles/cloudsql.instanceUser` conditioned to the instance; a custom role with `storage.objects.get`, `create` and `delete` on the bucket; Token Creator on itself, for keyless V4 signed URLs; Encrypter/Decrypter on `kek`; Signer and Public Key Viewer on `signing`; Secret Accessor on the configuration. |

The root also enables the APIs it uses, and leaves them enabled on destroy.

### Server configuration

`auth.signing.kms_keys` is a list of mappings, which no `INFRAWEAVE_*`
variable can carry, so the whole configuration is one strict YAML file in
Secret Manager, mounted at `/etc/infraweave/infraweave.yaml` and passed as
`--config`. It holds no credential:

| Key | Value |
|---|---|
| `server.listen` | `:8080`, the port Cloud Run routes to |
| `server.admin_listen` | `:9090`; the startup, liveness and readiness probes call `/startupz`, `/livez` and `/readyz` there |
| `server.public_url` | `https://<domain_name>` |
| `database.url` | `postgres://<user>@<private IP>:5432/infraweave?sslmode=require` |
| `database.iam_auth` | `gcp`: each connection's password is the service account's OAuth token |
| `objectstore.driver`, `objectstore.gcs.bucket` | `gcs`, the bucket |
| `secrets.kek.provider`, `secrets.kek.gcpkms.key_name` | `gcpkms`, the `kek` key |
| `auth.signing.backend`, `auth.signing.kms_keys` | `kms`, `gcpkms` with the `signing` key's first version |
| `auth.operator.keys` | `operator_keys` |

## Image

`image` is the server image on GHCR, pinned by digest:
`ghcr.io/<path>@sha256:<digest>`. The root maps it to
`<region>-docker.pkg.dev/<project>/iw-ghcr-<suffix>/<path>@sha256:<digest>`.
Its entrypoint must be `infraweave all`. The image is built from the
tested revision by [#791](https://github.com/weaveplatform/infraweave/issues/791),
which does not exist yet; until it does, this root has no image to run.

## Variables

| Variable | Default | |
|---|---|---|
| `project_id` | | `landing-zone/google`'s `project_id` |
| `region` | `us-central1` | every regional resource |
| `ownership` | | `{owner, batch, expires_at, labels}`, which `cloudlab.py` supplies to every stage |
| `image` | | the digest-pinned GHCR image |
| `domain_name` | | the lab domain, `server.public_url`'s host |
| `dns_managed_zone` | `null` | a Cloud DNS zone in the project holding `domain_name`; null leaves the A record to you |
| `operator_keys` | `[]` | `auth.operator.keys`: `{id, secret_sha256}` |
| `subnet_cidr` | `10.20.0.0/24` | the Direct VPC egress subnet, `/26` or larger |
| `database_tier` | `db-custom-1-3840` | the Cloud SQL machine type |

Every resource that takes labels carries `ownership.labels` with each key
and value lowercased and other characters than letters, digits, `_` and
`-` turned into `_`, so `io.infraweave.lab.owner` is
`io_infraweave_lab_owner`, which the cleanup's asset search queries, plus
`io_infraweave_lab_expires_at`. The KMS keys carry every label but the
owner's: Cloud KMS keeps a destroyed key, labels included, for good, and
the owner search would never read the batch absent.

## Outputs

- `platform`: `schema`, `kind`, `provider`, the ownership, `project_id`,
  `region`, `public_url`, the `api`, `worker` and `state` endpoints under
  it, `domain_name`, `load_balancer_ip`, the Cloud Run `service`, the
  `service_account`, the mapped `image`, the `bucket`, the `kms` key ring,
  `kek` and `signing_key` version, the `database` instance, connection
  name, private IP, name and user, and the `network` and `subnetwork`.
  Worker clusters reach `public_url` and trust it through the system roots.
- `inventory`: `{kind, id, scope, owned, owner, lifecycle}` for every
  resource, as `cloudlab.py` reads inventories; each `id` is a suffix of
  the resource's Cloud Asset Inventory name. The key ring is listed
  `owned = false`, `lifecycle = "external"`: Cloud KMS never deletes one.

## Apply

```sh
cd test/terraform/platform/google
terraform init
terraform plan -out=lab.tfplan
terraform apply lab.tfplan
terraform output platform
```

The certificate provisions only once `domain_name`'s A record holds
`load_balancer_ip`; without `dns_managed_zone`, create that record after the
first apply. Until the certificate is `ACTIVE`, HTTPS to the domain fails.

### Time and cost

An apply takes about 15 to 25 minutes, most of it Cloud SQL and private
services access; the certificate then takes 15 to 60 minutes after DNS
resolves. A destroy takes about 10 minutes.

At list prices in `us-central1` the platform costs about $0.25 an hour,
about $6 a day: two always-allocated 1 vCPU, 1 GiB Cloud Run instances
about $0.14, the Cloud SQL instance about $0.05, the forwarding rule about
$0.03, and the rest (KMS key versions, Secret Manager, storage, Artifact
Registry and egress) cents. Destroy it when the batch ends.

## Destroy

```sh
terraform destroy
```

Cloud SQL drops its database and IAM user with the instance, which Terraform
leaves to it. The Service Networking connection is removed with its
peering, since Cloud SQL releases it only some time after the instance
goes. The bucket is emptied first. The key ring and both keys stay in the
project, as Cloud KMS keeps them; their versions are destroyed a day later.
The custom role is deleted and its ID stays reserved for some weeks, which
the per-owner suffix makes harmless. The APIs stay enabled.
