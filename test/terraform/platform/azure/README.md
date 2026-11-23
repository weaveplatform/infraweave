# Platform scenario on Azure

One infraweave platform in the dedicated lab subscription that
[`landing-zone/azure`](../landing-zone/azure) creates, behind public HTTPS
at `https://<domain_name>`. The acceptance runner drives scenarios against
it, and the worker clusters of all three clouds attach to it. The page
"Platform on Azure" of [`cloud-platform.drawio`](../../../../docs/diagrams/cloud-platform.drawio)
draws it.

## What it creates

Everything except the DNS records lives in one resource group,
`<owner>-platform`:

| Resource | Shape |
|---|---|
| Virtual network | `vnet_cidr`; a /23 delegated to `Microsoft.App/environments` and the /24 after it delegated to PostgreSQL |
| Container Apps environment | Workload profiles (Consumption), integrated in the /23, public, logging to a Log Analytics workspace kept 30 days; Container Apps creates and deletes its infrastructure group, `<owner>-platform-infrastructure` |
| Container app `infraweave` | `image` running `infraweave all`, exactly 2 replicas, managed ingress over HTTPS only to port 8080, startup, readiness and liveness probes on the admin listener's port 9090, and a user-assigned identity |
| Custom domain | `domain_name`, with an `asuid` TXT record and a CNAME in the borrowed `dns_zone`, and a managed certificate that Container Apps issues and renews |
| PostgreSQL Flexible Server 18 | `postgres_sku`, private access only, through the delegated subnet and a private DNS zone linked to the VNet; Entra authentication only, with the app's identity as its Entra administrator; database `infraweave` |
| Storage account and container `infraweave` | Shared keys off, so every request and every signed URL goes through Entra ID; a public endpoint, since worker Pods on other clouds fetch the signed URLs |
| Key Vault | Azure RBAC, purged on destroy; an RSA 3072 key `kek` and an EC P-256 key `signing` |
| Role assignments | For the app's identity: blob read, write, add and delete on the container; Storage Blob Delegator on the account; wrap and unwrap on `kek`; read and sign on `signing`. For whoever applies the root: Key Vault Crypto Officer on the vault, to create the keys |

The server's configuration is a YAML file that Container Apps mounts from
an app secret at `/etc/infraweave/config`, and the app passes it to the
image as `--config`. The file's keys are the ones `internal/app/config.Load`
decodes:

| Key | Value |
|---|---|
| `server.listen`, `server.admin_listen` | `:8080` for ingress, `:9090` for the probes |
| `server.public_url` | `https://<domain_name>` |
| `database.url`, `database.iam_auth` | `postgres://<identity>@<server>:5432/infraweave?sslmode=verify-full&sslrootcert=system`, `azure` |
| `objectstore.driver`, `objectstore.azureblob` | `azureblob`, with the account URL and container |
| `secrets.kek.provider`, `secrets.kek.azurekv` | `azurekv`, with the vault URL and `kek` |
| `auth.signing.backend`, `auth.signing.kms_keys` | `kms`, with `azurekv` and the signing key's versioned ID |
| `auth.operator.keys` | `operator_keys` |
| `log.level` | `log_level` |

The configuration can't come from `INFRAWEAVE_*` variables, because
`auth.signing.kms_keys` is a list of objects and no variable can carry one.
`AZURE_CLIENT_ID` selects the identity for azidentity's default chain,
which the PostgreSQL token, the Blob Storage driver and both Key Vault
backends all use. No password, account key or connection secret exists.

The app's identity is the PostgreSQL server's Entra administrator. A
narrower Entra role would need SQL run inside the VNet by an
administrator, and no step of this root can run it. The administrator
belongs to `azure_pg_admin` and is not a superuser.

## Before applying

- `image` is the `image` field of `server-image.json`, which
  `.github/workflows/server-image.yml` publishes: a release's asset
  (`gh release download v<version> --pattern server-image.json`), or the
  `server-image` artifact of a dispatch on `main`, which publishes main's
  head. The image, `ghcr.io/weaveplatform/infraweave`, is public, its
  entrypoint is `infraweave all`, and its distroless base holds the system
  CA roots that verify the PostgreSQL server's certificate.
- `dns_zone` is a public Azure DNS zone in the lab subscription, already
  delegated from its parent. The root adds records to it and never
  changes the zone itself.
- Sign in with `az login` as a principal that can create role definitions
  and role assignments in the subscription, such as Owner. The same
  shell runs `az containerapp hostname bind`, because the azurerm
  provider can't bind a managed certificate to a hostname.

## Variables

| Variable | Required | Meaning |
|---|---|---|
| `subscription_id` | yes | `landing-zone/azure`'s `subscription_id` |
| `ownership` | yes | `{owner, batch, expires_at, labels}`, as `cloudlab.py` supplies it to every stage. `labels` must carry `io.infraweave.lab.owner` with `owner`. Every taggable resource carries the labels and `io.infraweave.lab.expires-at` |
| `image` | yes | `<repository>@sha256:<digest>` |
| `domain_name` | yes | `server.public_url`'s host, below `dns_zone.name` |
| `dns_zone` | yes | `{name, resource_group_name}` of the borrowed zone |
| `operator_keys` | no | `auth.operator.keys`: `{id, secret_sha256}` with a ULID and the lower-case hex SHA-256 of a secret that only the runner holds. Never use the acceptance harness's derived operator key on this public server |
| `location` | no | `uksouth` |
| `vnet_cidr` | no | `10.72.0.0/16`, a /22 or larger |
| `cpu`, `memory` | no | Each replica's size: `1` and `2Gi` |
| `postgres_sku` | no | `B_Standard_B2s`. B1ms has too few connections for two replicas |
| `log_level` | no | `info` |

## Outputs

`platform` is what the runner and the worker roots attach with: `public_url`,
the `api`, `operator`, `worker` and `state` endpoints, `ca_certificate = null`
(the managed certificate chains to a public root), the tenant, subscription
and location, the app's default FQDN and egress addresses, the storage
account URL and container, the vault URL with the `kek` key's versionless
ID and the `signing` key's versioned ID, the database server, name and
user, the app identity's client and principal IDs, and the Log Analytics
workspace ID.

`inventory` lists every resource the root creates as `{kind, id, scope,
owned, owner, lifecycle}`, with ARM resource IDs, plus the borrowed DNS zone
as `external`. Cleanup's owner-tag search reads tagged entries back. Role
definitions, role assignments, DNS records and the environment's
infrastructure group carry no tag that search finds, so the empty state is
their evidence.

## Apply, time and cost

```sh
cd test/terraform/platform/azure
terraform init
terraform plan -out=lab.tfplan
terraform apply lab.tfplan
```

An apply takes about 20 to 40 minutes. The VNet-integrated environment
and the private PostgreSQL server each take several minutes, and the
managed certificate takes up to 20. The app's default FQDN answers before
the custom domain does.

While the platform is applied, it costs roughly US$0.30 an hour at list
prices. Most of that is the two always-on 1 vCPU / 2 GiB replicas and the
B2s server, with small amounts for Log Analytics ingestion, Key Vault
operations and storage. The managed certificate is free. Destroy the
platform when the batch ends.

## Destroy

```sh
terraform destroy
```

Destroy needs the same `az` sign-in as apply. It first removes the hostname
from the app, because Container Apps refuses to delete a certificate that a
hostname uses. It then deletes the rest and purges the vault, so a later
apply can reuse the name. Deleting the environment and the server takes
10 to 20 minutes. Container Apps deletes the infrastructure group with the
environment.
