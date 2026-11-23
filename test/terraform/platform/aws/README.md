# Platform scenario on AWS

One infraweave platform in the dedicated lab account that
[`landing-zone/aws`](../landing-zone/aws) creates, behind public HTTPS at
`server.public_url`. The acceptance runner drives scenarios against it, and the
worker clusters of all three clouds (`platform/workers/<cloud>`) attach to it.
The [AWS page of cloud-platform](../../../../docs/diagrams/cloud-platform-aws.drawio.svg)
draws it.

## What it creates

| Component | Resources |
|---|---|
| Network | a VPC with a public and a private /24 in each of two availability zones, an internet gateway, one NAT gateway with its Elastic IP, and an S3 gateway endpoint for the private subnets |
| Ingress | an internet-facing Application Load Balancer, HTTPS only on 443 with `ELBSecurityPolicy-TLS13-1-2-2021-06`, an ACM certificate for `domain_name` validated through DNS, and an alias record in the borrowed Route 53 zone. The target group forwards to the API listener (8080) and checks `/readyz` on `server.admin_listen` (9090) |
| Server | an ECS Fargate cluster and a service of two tasks running `infraweave all`, each with a bootstrap container that writes the configuration file and creates the database role first; one CloudWatch log group |
| Database | RDS for PostgreSQL 18 in the private subnets, with IAM database authentication, `rds.force_ssl`, encrypted storage, no backups, and its master password in a Secrets Manager secret RDS manages |
| Objects | an S3 bucket that blocks public access, enforces bucket-owner ownership, refuses plain HTTP and empties on destroy |
| Keys | a symmetric KMS key, the KEK, and an `ECC_NIST_P256` `SIGN_VERIFY` KMS key for token signing, each with an alias |
| Identity | a task execution role (log writes, the master secret) and a task role, the server's identity: the bucket's objects, `kms:Encrypt` and `kms:Decrypt` on the KEK under the `infraweave=data-key` encryption context, `kms:GetPublicKey` and `kms:Sign` on the signing key, and `rds-db:connect` as the server's database role. Both trust only ECS tasks of this account and region |

Every resource name starts with `iwlab-` and ten hex digits of the owner's
SHA-256, so two owners' platforms fit in one account. Every taggable
resource carries `ownership.labels` through the provider's default tags, the
tasks included through the service's tag propagation.

### Server configuration

The tasks run with `--config /etc/infraweave/config.yaml`. The file is the
only way in for `auth.signing.kms_keys`, a list of mappings that no
`INFRAWEAVE_*` variable carries, and Fargate mounts no file from outside a
task. Each task's bootstrap container (`bootstrap_image`, which needs `sh` and
`psql`) writes the file into task storage that the server container mounts
read-only. It also creates the `infraweave` database role with `rds_iam` and
makes the role the database owner, all inside one transaction under an
advisory lock, before the server container starts. The file sets:

| Key | Value | Read by |
|---|---|---|
| `server.public_url` | `https://<domain_name>` | `internal/app/config/config.go` |
| `server.listen`, `server.admin_listen` | `:8080`, `:9090` | `internal/platform/httpkit/serve/serve.go` |
| `server.timeouts.idle` | 180s, longer than the load balancer's 150-second idle timeout, which itself exceeds the 120-second worker poll | `internal/platform/httpkit/serve/serve.go` |
| `log.level` | `log_level` | `internal/platform/observe` |
| `database.url`, `database.iam_auth` | `postgres://infraweave@<rds>:5432/infraweave?sslmode=require`, `aws` | `internal/platform/sql/sql.go`, `iam.go` |
| `objectstore.driver`, `objectstore.s3.bucket`, `objectstore.s3.region` | `s3`, the bucket, `region` | `internal/platform/objectstore/config.go` |
| `secrets.kek.provider`, `secrets.kek.awskms.key_id`, `.region` | `awskms`, the KEK's ARN, `region` | `internal/platform/crypt/kek/kek.go` |
| `auth.signing.backend`, `auth.signing.kms_keys` | `kms`, `[{provider: awskms, ref: <signing key ARN>}]` | `internal/access/assembly/settings.go` |
| `auth.operator.keys` | `operator_keys` | `internal/access/assembly/settings.go` |

The server container's environment adds `AWS_REGION`, which the IAM token
for RDS is signed for.

## Variables

| Variable | Required | Meaning |
|---|---|---|
| `ownership` | yes | `{owner, batch, expires_at, labels}`, which `cloudlab.py` supplies to every stage; `labels` must carry `io.infraweave.lab.owner` with `owner`'s value |
| `image` | yes | the infraweave server image pinned by digest, `<repository>@sha256:<digest>`; its entrypoint must be `infraweave all` |
| `domain_name` | yes | the lab domain the platform answers at |
| `route53_zone_id` | yes | the public hosted zone in the same account that holds `domain_name` |
| `landing_zone_role_arn` | no | `landing-zone/aws`'s `role_arn`; null applies into the caller's own account |
| `operator_keys` | no | `[{id, secret_sha256}]` for `auth.operator.keys`, where `id` is a ULID and `secret_sha256` the hex SHA-256 of a secret only the runner holds |
| `region` | no | default `eu-north-1` |
| `ingress_cidrs` | no | who may reach 443; default `0.0.0.0/0`, since the runner and three clouds' clusters call it |
| `bootstrap_image` | no | default the acceptance tests' `postgres:18.7` digest |
| `vpc_cidr` | no | default `10.64.0.0/16`; /20 or larger |
| `cpu_architecture`, `task_cpu`, `task_memory` | no | default `ARM64`, 1024, 2048 |
| `postgres_version`, `db_instance_class` | no | default `18`, `db.t4g.micro` |
| `log_level`, `log_retention_days` | no | default `info`, 7 |

The server image comes from #791, which publishes it to GHCR for linux/amd64
and linux/arm64 with `infraweave all` as its entrypoint. Until that image
exists, this root validates but cannot be applied: the service never reaches
a steady state. Both images must be public, because the execution role holds
no registry credential.

The zone must be in this account; a lab subdomain delegated from the
organization's own domain is enough.

## Outputs

`platform` is what the runner and the worker roots attach with:

| Field | Value |
|---|---|
| `schema`, `kind`, `provider` | `1`, `platform`, `aws` |
| `owner`, `batch`, `expires_at` | the `ownership` input |
| `cloud` | `{account_id, region}` |
| `public_url` | `server.public_url` |
| `ca_certificate` | `null`: ACM issues from a public CA, so clients trust the certificate, and the S3 endpoint of signed URLs, with their system roots |
| `endpoints` | `{api, operator, worker, state}`: `/api/v1`, `/operator/v1`, `/worker/v1` and `/state/v1` below `public_url` |
| `egress_ips` | the NAT gateway's address, which the tasks reach GHCR and the VCS providers from |
| `bucket` | `{name, region}` |
| `keys` | `{kek, signing}` key ARNs |
| `log_group` | the tasks' log group |

`inventory` lists every resource the root creates as `{kind, id, scope, owned,
owner, lifecycle}`, with the ARN as `id` wherever the resource has one, so
the owner tag search's ARNs end in it, and `lifecycle` `platform`. The
borrowed hosted zone is listed with `owned: false` and `lifecycle:
external`. Route 53 records carry no tags, so the empty state is their only
evidence.

## Apply

```sh
cd test/terraform/platform/aws
terraform init
terraform plan -out=lab.tfplan      # terraform.tfvars: the variables above
terraform apply lab.tfplan
terraform output -json platform
```

An apply takes about 15 to 20 minutes, most of it creating RDS. It returns only
once both tasks pass the load balancer's health check, so the first start's
migrations come before it. The service's deployment circuit breaker stops a
start that keeps failing, and the apply then fails; the tasks' logs are in
`log_group`.

## Cost

While applied, it bills by the hour for the load balancer, the NAT gateway,
three public IPv4 addresses (the NAT gateway's and the load balancer's two),
RDS, two Fargate tasks and two KMS keys: in `eu-north-1`, roughly $0.20 an
hour, or about $5 a day, before data transfer and NAT processing. The S3
gateway endpoint keeps the tasks' object traffic off the NAT gateway. Destroy
it when the batch ends.

## Destroy

```sh
terraform destroy
```

A destroy takes about 10 to 15 minutes. It removes the service before the
roles, the database and the network, empties and deletes the bucket, and
deletes RDS without a final snapshot. The KMS keys wait seven days in
`PendingDeletion`, KMS's shortest window, and keep their tags meanwhile, so
an owner tag search can still return them until then. The hosted zone is left
as it was, without the two records the root added.
