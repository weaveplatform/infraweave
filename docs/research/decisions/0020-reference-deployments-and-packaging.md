# 0020: Reference deployments and packaging

Status: Proposed

## Context

infraweave is self-hosted software, so how it is built, shipped, installed
and upgraded is part of the product. Self-hosted Spacelift sets the bar
([24](../reference/24-self-hosted-reference.md)):
- **One binary.** Every backend process is the same binary, started with a
  different subcommand: a stateless server, a stateless drain that consumes
  the queues and fires cron, and an optional VCS gateway
  ([24 section 2.1](../reference/24-self-hosted-reference.md#21-process-inventory)).
- **Two hard dependencies.** PostgreSQL and object storage are required. The
  queue, encryption and the worker broker each have a built-in form
  ([24 section 3](../reference/24-self-hosted-reference.md#3-external-dependencies)).
- **Reference artefacts.** A Helm chart and Terraform modules for ECS, EKS,
  AKS and GKE, plus an on-prem path on any Kubernetes cluster with
  S3-compatible storage
  ([24 section 6](../reference/24-self-hosted-reference.md#6-the-four-reference-deployments-compared)).
- **Upgrades.** Zero-downtime rollouts with migrations at start, and module
  majors tied to application versions, which the upgrade notes have to call
  out one by one
  ([24 section 9](../reference/24-self-hosted-reference.md#9-upgrades-the-v2-to-v3-kit-and-what-changed)).

This record decides packaging and deployment: the artefacts, the process
roles they run, the reference deployments, and the upgrade rules. Decision
0004 owns the code structure. Decisions 0005 (persistence and object
storage), 0008 (workers and runner images), 0012 (the VCS agent) and 0013
(keys) own the behaviour that the artefacts carry.

## Decision

### Two binaries, one server binary with several roles

The repository builds two Go binaries with `CGO_ENABLED=0`. Their code
layout is decision 0004's.

- **`infraweave`** (`cmd/infraweave`) is every operator and worker
  process. Each process is a subcommand of it:

| Subcommand | Runs | State | Listeners |
|---|---|---|---|
| `infraweave serve` | the API role (table below) | stateless | API and admin |
| `infraweave scheduler` | the scheduler role (table below) | stateless | admin only |
| `infraweave all` | `serve` and `scheduler` in one process, for single-node installs | stateless | API and admin |
| `infraweave worker` | the launcher: registration, work delivery and run containers (decision 0008) | none | admin only |
| `infraweave worker exec` | hidden; the phase executor inside the run container (decision 0008) | none | none |
| `infraweave vcs-agent` | the VCS agent (decision 0012) | none | admin only |
| `infraweave flows-agent` | Flows app execution ([0018](0018-flows-runtime.md)) | none | admin only |
| `infraweave migrate up`, `infraweave migrate status` | the schema migrations of decision 0005, then exit | none | none |
| `infraweave version` | build information from `internal/buildinfo` | none | none |

- **`infraweavectl`** (`cmd/infraweavectl`) is the client CLI over
  `pkg/client/deploy`: the commands that replace spacectl, including
  `infraweavectl mcp` ([0019](0019-ai-provider-boundary.md)). Its design is
  outside this record.

Operators and workers download `infraweave`; users download `infraweavectl`.

Server roles:

| Role | Does | Scales on |
|---|---|---|
| `serve` | The REST API, web UI assets, inbound VCS webhooks, the registry protocol ([0014](0014-module-and-provider-registry.md)), the state backend (decision 0010), `/mcp` ([0019](0019-ai-provider-boundary.md)), the worker long-poll endpoints (decision 0008), the VCS agent endpoints ([0012](0012-vcs-integration-model.md)), the Flows endpoints and agent gateway ([0018](0018-flows-runtime.md)), and the launcher downloads below | request rate and CPU |
| `scheduler` | The consumers of the persistent work queue (decision 0011), including the outbox, the scheduler ticker and cron, event delivery ([0015](0015-events-notifications-webhooks-audit.md)) and notification policies | queue depth |

- **Single node.** `infraweave all` is a complete installation in one
  process.
- **VCS agents.** The VCS agent's long-poll endpoints are part of `serve`
  (decision 0012). They become a role of their own only if their
  connections need their own scaling, as decision 0004 states.
- **Coordination.** Roles never coordinate through process memory. Any
  number of `serve` and `scheduler` replicas may run against one database, so
  every role relies on decision 0011's claims and decision 0005's
  multi-replica rules.

Listeners:

| Setting | Default | Serves | Exposure |
|---|---|---|---|
| `server.listen` | `:8080` | `serve` traffic | behind the ingress or load balancer; TLS ends there or at `server.tls` |
| `server.admin_listen` | `:9090` | `/livez`, `/readyz`, `/startupz`, `/metrics`, `/metrics/spacelift` ([0017](0017-observability-and-health.md)) | cluster-internal only; the chart's `NetworkPolicy` admits only the namespaces in `metrics.allowedNamespaces` |

### Artefacts

| Artefact | Content | Published as |
|---|---|---|
| Release archives | `infraweave` for `linux`, `darwin` and `windows` on `amd64` and `arm64` (the Makefile's `PLATFORMS`), `SHA256SUMS`, a Sigstore bundle per file | GitHub release assets |
| Server image | `infraweave` on `gcr.io/distroless/static-debian12:nonroot`, user `65532`, read-only root file system, no shell; `ENTRYPOINT ["/infraweave"]` | `ghcr.io/weaveplatform/infraweave:<version>`, multi-arch |
| FIPS image | The same, built with `GOFIPS140` set to the validated Go cryptographic module | `ghcr.io/weaveplatform/infraweave:<version>-fips` |
| Runner images | Built from the repository's runner image definitions, whose contents (Terraform, OpenTofu and the other vendor tools of report 20) decision 0008 defines. The acceptance harness builds the same definitions locally as `infraweave-runner:acceptance` ([0016](0016-testing-and-acceptance.md)) | `ghcr.io/weaveplatform/infraweave-runner-<vendor>:<version>` |
| Flows runtime image | The Node runtime that executes Flows app bundles ([0018](0018-flows-runtime.md)) | `ghcr.io/weaveplatform/infraweave-flows-runtime:<version>` |
| Helm chart | `infraweave` (`serve` and `scheduler`) and `infraweave-workers` (`infraweave worker`) | `oci://ghcr.io/weaveplatform/charts/<chart>:<version>` |
| Terraform modules | `deploy/terraform/modules/<cloud>` (see reference deployments) | Git source in this repository at the release tag |
| Compose file | `deploy/compose/compose.yaml` | Release asset |

- **Signing.** Every image and archive is signed with keyless Sigstore
  signatures from the release workflow's GitHub OIDC identity. Every image
  carries SLSA provenance and an SPDX SBOM as attestations. Verification
  pins the certificate identity to `release-please.yml` on `main` and the
  issuer to `https://token.actions.githubusercontent.com`.
- **Versioning.** release-please produces one version for the binary, the
  images, both charts, the modules and the compose file. A chart's
  `version` equals its `appVersion`. Modules take the version through
  their Git `ref`, so no separate compatibility table between module and
  application versions exists.
- **Image references.** The chart references images by tag and accepts
  `image.digest`. When a digest is set, the tag is ignored.
- **Additional tools.** A role that needs a binary beside `infraweave`, such
  as `git` for raw-git sources, gets it only if its owning decision names
  it. The image then switches to a distroless base that carries that tool,
  and the change is recorded here. Go-native libraries are preferred.

### Launcher distribution

The `serve` role serves the `infraweave` Linux binaries of its own version, which workers run as `infraweave worker`:
- `GET /downloads/launcher/<version>/linux-<arch>`, for `amd64` and `arm64`;
- `GET /downloads/launcher/<version>/SHA256SUMS`;
- the matching Sigstore bundles.

The server image carries both Linux binaries, the sums and the bundles under
`/usr/share/infraweave/launcher/`, so an air-gapped install has nothing to
upload. Launchers run on Linux hosts only, so no other platform is served.
VM-based workers download from the server they register with and verify the
checksum and the signature before running. They never need egress to a
vendor download host. Kubernetes workers use the server image, which
carries the same binary.

### Configuration in deployments

- **One file.** Every artefact passes configuration as infraweave's strict
  YAML file (`--config`), and secrets as environment variables or files the
  file references. The chart renders `values.config` into a `ConfigMap`, so
  a key that infraweave does not know fails the pod at start rather than
  being ignored.
- **Public URL.** `server.public_url`, required when the `serve` role runs,
  is the HTTPS origin that clients and workers use. Every absolute URL
  infraweave emits derives from it: the registry host
  ([0014](0014-module-and-provider-registry.md)), webhook and Slack
  callbacks ([0015](0015-events-notifications-webhooks-audit.md)), the token
  issuer of decision 0006, and the launcher downloads. The chart sets it
  from `ingress.host`, and the cloud modules from their DNS name input.
- **Strict chart values.** `values.schema.json` sets
  `additionalProperties: false` at every level, so `helm install` rejects a
  misspelt value.
- **Secrets.** The database URL, object-store credentials (when workload
  identity is not used), the local key of decision 0013, and the bootstrap
  credential of decision 0006 come from `secrets.existingSecret` or from
  the cloud's secret store through the CSI driver. The chart never writes a
  secret value into a `ConfigMap`.

### Reference deployments

Every reference deployment lives under `deploy/` and runs the same
subcommands:

| Reference | Compute | PostgreSQL | Object storage | Key root (decision 0013) | Identity for storage and keys |
|---|---|---|---|---|---|
| `deploy/compose` | one `infraweave all` plus one `infraweave worker` on Docker | `postgres` container, volume | local disk volume (decision 0005) | local key file | none |
| `deploy/helm` on any Kubernetes | chart Deployments | operator-supplied | S3-compatible or local persistent volume | local key in a Secret | none |
| `deploy/terraform/modules/aws` | EKS, Auto Mode | RDS for PostgreSQL, Multi-AZ, IAM authentication | S3, one bucket with store prefixes | AWS KMS | EKS Pod Identity |
| `deploy/terraform/modules/azure` | AKS | Azure Database for PostgreSQL Flexible Server, private access, Entra authentication | Blob Storage, one account | Azure Key Vault key | Microsoft Entra Workload ID |
| `deploy/terraform/modules/google` | GKE Autopilot, private nodes | Cloud SQL for PostgreSQL, private IP, IAM authentication | Cloud Storage | Cloud KMS | GKE Workload Identity Federation |

Rules every cloud module follows:

- **Inputs.** It takes a network (or creates one when `create_network` is
  true), a DNS name, and a certificate source. It creates object storage
  for decision 0005's logical stores: one bucket with store prefixes by default, or one bucket per store when `separate_buckets`
  is true. It also creates a database at decision 0005's version floor,
  the key, the cluster, and a `helm_release` of the chart.
- **Database sizing.** It sets PostgreSQL's `max_connections` to at least
  the sum over server replicas of `database.max_conns` plus one listening
  connection (decision 0005), plus 20% headroom. It puts no
  transaction-mode pooler in front of the database.
- **Images.** It pins the chart and images to the module's own release.
- **No static credentials.** Workloads use the cloud's workload identity,
  one identity per role, with least privilege: `scheduler` deletes expired
  objects and `serve` does not. Object storage is private, and browsers and
  workers reach it through pre-signed URLs only.
- **Outputs.** It outputs the server URL and the worker registration
  endpoint.
- **Examples.** Each module has one example root per scenario under
  `deploy/terraform/examples/<cloud>/<scenario>`: `minimal`,
  `vcs-agent`, `shared-pool`. The example is the documentation the
  consumer copies.

The chart deploys `serve` and `scheduler` as separate Deployments by default,
because they scale on different signals:

| Value | Default |
|---|---|
| `serve.replicas`, `scheduler.replicas` | `2`, `2` |
| `serve.autoscaling` | CPU-based `HorizontalPodAutoscaler`, off |
| `scheduler.autoscaling` | on `infraweave_queue_jobs` with `state="pending"` ([0017](0017-observability-and-health.md)), through KEDA when installed, off |
| `sharedPool.enabled` | `false`: an operator-run pool that every space may use, standing in for Spacelift's public pool ([0021](0021-capability-scope-and-supersession.md)) |
| `podDisruptionBudget.maxUnavailable` | `1` per role |
| `terminationGracePeriodSeconds` | `serve` 45, `scheduler` 120; both exceed 0017's ready delay, request drain and flush caps combined |
| `migrations.mode` | `hook`: a `pre-install,pre-upgrade` Job runs `infraweave migrate up`, and the roles run with `database.migrate: check`; `startup` leaves decision 0005's default, `database.migrate: auto` |

### Upgrades

- **Versions.** infraweave follows semantic versioning through
  release-please. Upgrading within a major may skip minors. Crossing a
  major requires the last minor of the previous major first. This record
  asks decision 0005's migration runner to refuse otherwise and name that
  version.
- **Mixed versions during a rollout.** Version N and N-1 of every server
  role run together against the schema of version N, under decision 0005's
  forward-only migration rules.
- **Order.** Infrastructure (a module upgrade), then `migrate up`, then the
  server roles by rolling update, then the launchers. The chart and the
  modules encode this order, so an operator applies one release.
- **Launchers.** A launcher older than the server by one minor keeps
  working: decision 0008 versions the worker protocol, and the server
  accepts the previous minor's launchers. A launcher refreshes its binary
  from the server's downloads when it restarts.
- **Rollback.** Rolling back to the previous minor is supported, because
  its schema is a subset. Rolling back across a release that removed
  schema needs a database restore, and that release's notes say so.
- **Database majors.** PostgreSQL major upgrades are the operator's.
  infraweave tolerates the downtime: the queue is in the database, and
  inbound VCS webhooks retry from the VCS side or are re-synced by decision
  0012's reconciliation.
- **Moving from Spacelift.** This is a data import (decision 0001), not an
  upgrade path, and it is outside this record.

### Backup and recovery

| Element | What the operator protects |
|---|---|
| PostgreSQL | Point-in-time backups; the queue is covered by them |
| Object storage | Replication or versioning for the classes decision 0005 marks durable (state, registry artefacts, run logs, policy samples); the rest is transient |
| Key root | The KMS key's replication, or the local key file kept outside the cluster; losing it loses every sealed value (decision 0013) |
| Server roles | Nothing: stateless, redeployed |

## Rationale

- **One server binary with roles, not one binary per service.** The roles
  share the domain, the store and the queue. Separate binaries would add
  images, release steps and version skew between processes that must agree
  on a schema. `infraweave all` makes the compose path and local
  development a single command. The client CLI is separate because users
  need none of the server's dependencies. Splitting `serve` from `scheduler` in Kubernetes
  keeps a webhook or delivery backlog from taking API capacity, as
  Spacelift's own split does.
- **Rejected: a cron process apart from the queue consumers.** Spacelift
  folded its scheduler into the drain, and the `scheduler` role does the
  same; decision 0011's claims make cron safe on any number of
  `scheduler` replicas.
- **Kubernetes on every cloud, not ECS.** Three Kubernetes modules share
  one chart, so behaviour is defined once. An ECS module would be a second
  compute model with its own task definitions, health checks and identity
  wiring, tested separately.
- **Modules in this repository, not separate module repositories.**
  Spacelift's separate repositories carry their own majors, which then have
  to be matched to application versions. One tag for everything removes
  that matrix.
- **The server hosts launcher downloads.** Workers already reach the
  server, and air-gapped installs have nothing else to reach. A vendor
  download host would add egress and a second trust root.
- **Strict configuration end to end.** The repository's strict YAML rule
  would be undermined if the chart silently dropped unknown values.
- **Departure from Spacelift:** no ECS or CloudFormation reference
  deployment.
- **Departure from Spacelift:** modules and chart share the application's
  version.
- **Departure from Spacelift:** launchers download from the installation
  itself and are verified with Sigstore, rather than with GPG keys from a
  public key server.
- **Departure from Spacelift:** no licence token. Spacelift's self-hosted
  limits (no public pool, no Azure or GCP integration;
  [26 section 2.3](../reference/26-scope-and-tiers.md#23-self-hosted)) are not inherited; the
  shared pool is a chart option ([0021](0021-capability-scope-and-supersession.md)).
- **Departure from Spacelift:** no MQTT broker listener, following 0021's
  supersession test; decision 0008 owns the transport.
- **Departure from Spacelift:** no VCS gateway process or gRPC listener.
  VCS agents long-poll `serve` over HTTP (decision 0012).

## Constraints

- **PostgreSQL is the only database**, at the version floor decision 0005
  sets. The modules create no read replica, because no decision uses one.
- **The S3-compatible store must support** what decision 0005 needs:
  pre-signed URLs and multipart uploads. It needs neither bucket versioning
  nor lifecycle expiry: decision 0005 writes every state version as its own
  immutable object and expires objects itself. Spacelift asks for both for
  its own install
  ([24 section 3.2](../reference/24-self-hosted-reference.md#32-object-storage-buckets)),
  which is a departure. An operator may still add a lifecycle rule as a
  backstop, set longer than the store's retention.
  MinIO's upstream server is no longer developed, so the compose reference
  uses local disk instead.
- **Real-cloud applies** of the modules run only in a protected GitHub
  environment with the maintainer's approval, through OIDC to lab
  accounts. They are not part of the pull-request gate.
- **Kubernetes versions.** The chart supports the Kubernetes minors that
  the three managed services all support at release time.

## Verification

- `make build` cross-compiles every target in `PLATFORMS`.
- `TestUnitServerListeners` checks that each subcommand binds only its
  own listeners.
- `deploy/check.sh`, run in the quality gate:
  - `helm lint` and `helm template` with `values.schema.json` against
    every example values file, plus `kubeconform` on the output;
  - `tofu fmt -check`, `tofu validate` and `tflint` on every module and
    example;
  - `tofu test` with mocked providers for the module contract: inputs,
    outputs, workload identity, no static keys, and the bucket rules
    decision 0005 names.
- `test/acceptance/features/deploy_compose.feature` (`@postgres`): the
  compose file starts, a launcher downloads and verifies its binary from
  the server, and a tracked run finishes on it.
- `test/acceptance/features/deploy_upgrade.feature` (`@postgres`
  `@objectstore`):
  - the previous release's binary and the candidate run together against
    one database after `migrate up`, and both serve reads and writes;
  - `migrate up` refuses to skip a major.
- `test/acceptance/features/deploy_helm.feature` (`@kubernetes`
  `@postgres` `@objectstore`): the chart installs into the k3s container
  of [0016](0016-testing-and-acceptance.md), every role reports `/readyz`,
  and one tracked run finishes on an `infraweave-workers` launcher.
- **Release.** The release workflow verifies every signature and
  attestation it produced with `cosign verify` before it publishes the
  release.

## References

- [24: process inventory](../reference/24-self-hosted-reference.md#21-process-inventory),
  [external dependencies](../reference/24-self-hosted-reference.md#3-external-dependencies),
  [networking](../reference/24-self-hosted-reference.md#4-networking-and-ports),
  [reference deployments](../reference/24-self-hosted-reference.md#6-the-four-reference-deployments-compared),
  [Helm chart values](../reference/24-self-hosted-reference.md#7-helm-chart-values),
  [upgrades](../reference/24-self-hosted-reference.md#9-upgrades-the-v2-to-v3-kit-and-what-changed),
  [disaster recovery](../reference/24-self-hosted-reference.md#11-disaster-recovery).
- [10: launcher distribution](../reference/10-workers-and-execution.md#45-launcher-distribution),
  [runner images](../reference/10-workers-and-execution.md#6-runner-images).
- [26: self-hosted](../reference/26-scope-and-tiers.md#23-self-hosted),
  [FedRAMP](../reference/26-scope-and-tiers.md#24-fedramp).
- Decisions 0001, 0004, 0005, 0006, 0008, 0010, 0011, 0012 and 0013.
- [Sigstore cosign](https://docs.sigstore.dev/cosign/),
  [SLSA provenance](https://slsa.dev/spec/v1.0/provenance),
  [Go FIPS 140-3 mode](https://go.dev/doc/security/fips140).
