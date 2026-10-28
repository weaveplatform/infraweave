# 24: Self-hosted reference architecture

Checked against: docs.spacelift.io self-hosted `latest` (v7.1.0) read 2026-10-28; introspection `b3255fe38f5e`; the Helm chart and Terraform modules at the commits listed in [Versions at the research baseline](#versions-at-the-research-baseline).

Research baseline: **2026-10-28**.

This report records how Spacelift ships itself for customers who run it in
their own infrastructure: the processes the backend image runs, the external
services they depend on, the object storage buckets and queues, the ports
between them, every configuration value the Helm chart and the reference
Terraform modules expose, how the four cloud flavours (AWS ECS, AWS EKS,
Azure AKS and Google GKE) differ, the move from the CloudFormation installer
(v2) to the reference architecture (v3 onwards), and usage reporting. It is
the strongest available evidence for infraweave's own process topology and
dependency set, so the architecture options and the deployment design start
here. Worker internals (launcher, worker binary, MQTT and long-poll
transports) are report 10; VCS agent pools are report 11; the metrics
exporters and audit trail are report 19; Intelligence settings are report 22;
self-hosted Flows is report 23.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, cited by URL, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` |
| **[S]** | Source code at the cited commit (MIT unless stated) |
| **[RN]** | The self-hosted changelog, <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog>, read 2026-10-28 |
| **[I]** | Inferred; the sentence says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

## 1. Distribution and install methods

| Fact | Value | Source |
|---|---|---|
| Current self-hosted release | v7.1.0, the version the `latest` docs describe and the guides pin (`SPACELIFT_VERSION=v7.1.0`) | [D] [on-prem guide][sh-onprem]; [RN] [changelog][sh-cl] |
| Release archive | `self-hosted-<version>.tar.gz`, holding the backend and launcher container images as tarballs (`container-images/spacelift-backend.tar`, `spacelift-launcher.tar`); since v6.4.0 it holds amd64 and arm64 builds, the arm64 files carrying an `-arm64` suffix | [D] [on-prem guide][sh-onprem]; [RN] v6.4.0 |
| Container images | Two: `spacelift-backend` (every backend service) and `spacelift-launcher` (only needed for Kubernetes workers) | [D] [environment requirements][sh-env] |
| Registry distribution | The Helm chart defaults to `oci.spacelift.io/deploy/backend:<appVersion>`, needs pull credentials, and offers a FIPS 140 build as `<repository>-fips`. From v8.0.0 releases are distributed through a Spacelift container registry | [S] [values.yaml L7-L25][hc-values]; [S] [chart README][hc-readme]; [RN] v7.0.0 |
| Launcher binaries | From v7.1.0 published per release at `https://downloads.spacelift.io/deploy/<version>/`; EC2 workers verify signature and checksum, needing egress to `downloads.spacelift.io` and `keys.openpgp.org` | [D] [Docker-based workers][sh-dbw] |
| Licence | A JWT licence token (`LICENSE_TYPE=jwt`, `LICENSE_TOKEN`) since v3; `LICENSE_TYPE=aws` (AWS License Manager) is the documented default and the CloudFormation mechanism. The token has a validity window and must be renewed | [D] [general configuration][sh-gen]; [RN] v3.0.0 |
| Licence in the API | `Query.debugInfo` returns `DebugInfo { commitSha, selfHostedVersion, selfHostedLicence }`; `SelfHostedLicence.validity` is a `LicenseDateRange { begin: Int!, end: Int }`; both strings are empty on SaaS | [G] `DebugInfo`, `SelfHostedLicence`, `LicenseDateRange` |
| Install methods | The reference architecture (recommended, any container platform, Terraform modules and a Helm chart) and the CloudFormation installer (deprecated) | [D] [install methods][sh-methods] |
| CloudFormation end of life | v7 is the last major with CloudFormation; 7.x releases are the last to ship its templates; v8.0.0 drops it | [RN] v7.0.0 |
| Upgrades | Zero downtime unless a release says otherwise: apply infrastructure changes (bumping module `?ref=` to any minimum the changelog names, then re-applying generated secrets), push the new images, redeploy the backend, then redeploy workers (not needed with the Kubernetes operator) | [D] [reference architecture][sh-intro] |

The self-hosted docs describe two generations of architecture. The
CloudFormation installer (v2, still shipped for 7.x) runs a server, a drain
and a scheduler on ECS Fargate and depends on AWS IoT Core, SQS and AWS
License Manager [D] [CloudFormation install][sh-cf]. The reference
architecture introduced in v3.0.0 removes the AWS dependency: IoT Core, SQS
and KMS become optional because the application carries built-in
equivalents, the licence becomes a JWT, and Terraform modules for ECS, EKS,
GKE and AKS replace the templates [RN] v3.0.0.

## 2. Components

### 2.1 Process inventory

Every backend process is the same `spacelift` binary in the backend image,
started with a different subcommand [D] [general configuration][sh-gen].

| Component | Command | Role | Ingress | State | Source |
|---|---|---|---|---|---|
| Server | `spacelift backend server` | Serves the embedded frontend, the GraphQL API, the REST API and inbound webhooks; hosts the built-in MQTT broker when that broker is selected; since v6.4.0 also assembles run payloads for long-poll workers | HTTP `1983`; MQTT `1984` (optional); instrumentation `8080` | Stateless | [D] [general configuration][sh-gen]; [D] [networking][sh-net]; [RN] v6.4.1; [S] [server-deployment.yaml L127-L156][hc-server]; [S] [ECS server.tf L18][ecs-cd-server-18] |
| Drain | `spacelift backend drain-local` (Helm) or `spacelift backend drain` (ECS) | Consumes every message queue: VCS webhooks, run state changes, notifications, drift detection, billing; since v6.4.0 also runs the cron scheduler that fires recurring work | None (instrumentation `8080` only) | Stateless | [D] [general configuration][sh-gen]; [D] [networking][sh-net]; [S] [drain-deployment.yaml L116-L119][hc-drain]; [S] [ECS drain.tf L20][ecs-cd-drain] |
| Scheduler (standalone, legacy) | `spacelift scheduler` | Fires cron triggers (drift detection, scheduled runs, tasks and deletes) by reading the database and enqueuing messages for the drain. Folded into the drain in v6.4.0; the chart deploys it only for application versions below v6.4.0 | Instrumentation `8080` | Stateless | [D] [CloudFormation install][sh-cf]; [RN] v6.4.1, v7.0.0; [S] [scheduler-deployment.yaml L1, L115-L117][hc-sched]; [S] [_helpers.tpl L218-L225][hc-helpers] |
| VCS Gateway | `spacelift backend vcs-gateway` | Optional. Terminates gRPC connections from VCS agents (VCS agent pools) and proxies the server's and drain's requests to private VCS systems | gRPC `1984` from a load balancer; HTTP `1985` from server and drain only | Stateless | [RN] v4.1.0; [D] [EKS guide][sh-eks]; [S] [vcs-gateway-deployment.yaml L1, L94-L106][hc-vcsgw]; [S] [values.yaml L143-L151][hc-values]; [S] [ECS vcs_gateway.tf L18][ecs-cd-vcs-18] |
| Built-in MQTT broker | inside the server | Delivers work to MQTT-transport workers; deprecated with the MQTT transport | `1984` | None | [D] [MQTT broker][sh-mqtt]; [RN] v7.1.0 |
| Launcher | binary or `spacelift-launcher` image, run by the customer | Registers a worker, receives work over MQTT or long-poll, starts a run container per run; the self-hosted launcher embeds the matching worker binary. Detailed in report 10 | None | None | [D] [Docker-based workers][sh-dbw] |
| Kubernetes worker pool controller | separate chart | Runs Kubernetes workers from `WorkerPool` resources; ships on its own release channel. Detailed in report 10 | None | None | [RN] v6.4.0 |
| Cloud SQL Auth Proxy (GKE) | `gcr.io/cloud-sql-connectors/cloud-sql-proxy:2.21.0` | Optional native sidecar (an init container with `restartPolicy: Always`) on every backend pod, listening on `5432` with `--private-ip --auto-iam-authn`; health on `9801` | `9801` | None | [S] [server-deployment.yaml L47-L119][hc-server]; [S] [values.yaml L197-L205][hc-values] |
| Telemetry sidecars (ECS) | `datadog-agent`, `aws-otel-collector` | Optional per-task sidecars the ECS module adds when tracing is enabled; the backend container waits for them to start | Local only | None | [S] [ECS server.tf L1-L11][ecs-cd-server]; [S] [ECS task_definitions.tf L165-L177][ecs-td] |

The backend containers in the chart set `ENABLE_INSTRUMENTATION_ENDPOINTS=true`
and expose port `8080` named `instrumentation`; the server's readiness probe
is `GET /readiness` and the drain's and scheduler's startup probes are
`GET /startup` on that port [S] [values.yaml L42-L45, L76-L79][hc-values];
[S] [drain-deployment.yaml L121-L139][hc-drain]. The EKS module points the
ALB health check at `8080/readiness` rather than the traffic port so that a
shutting-down server leaves the target group quickly [S]
[EKS helm-values.tftpl L54-L59][eks-helm]. The server also sets
`FEATURE_FLAG_SELF_HOSTED_V3_INSTALLATION_FLOW=true`, which gates the v3
first-setup flow [S] [server-deployment.yaml L134-L135][hc-server]; [I] from
the flag name and the v3 first-setup wizard in [RN] v3.0.0.

From v6.4.0 the backend consists of two core services (server and
drain) rather than three; CloudFormation installations keep a scheduler
service [RN] v6.4.1. Server and drain are stateless and only start when they
reach a valid PostgreSQL database [D] [disaster recovery][sh-dr]. The
application runs its database migrations on startup and needs to create
roles and objects, which is why the GKE guide grants `CREATEROLE` and
`ALL ON SCHEMA public` to the database user [D] [GKE guide][sh-gke].

### 2.2 Components each module creates

Generated by a scan of the module sources (section 15 has the
script and its output). Each row is a resource or Kubernetes object that runs
a backend process.

| Flavour | Server | Drain | Scheduler | VCS Gateway | Source commit |
|---|---|---|---|---|---|
| Helm chart (`spacelift-helm-charts`) | `Deployment` `<fullname>-server`, [server-deployment.yaml L2][hc-server-2] | `Deployment` `<fullname>-drain`, [drain-deployment.yaml L2][hc-drain-2] | `Deployment` `<fullname>-scheduler`, only if `spacelift.deployStandaloneScheduler`, [scheduler-deployment.yaml L3][hc-sched-3] | `Deployment` `<fullname>-vcs-gateway`, only if `vcsGateway.enabled`, [vcs-gateway-deployment.yaml L3][hc-vcsgw-3] | `4e02e1d22e73` |
| Helm chart (`spacelift-self-hosted-helm-chart`) | same objects, [server-deployment.yaml L2][hc2-server] | [drain-deployment.yaml L2][hc2-drain] | [scheduler-deployment.yaml L3][hc2-sched] | [vcs-gateway-deployment.yaml L3][hc2-vcsgw] | `660c86391811` |
| AWS ECS | `aws_ecs_service.server`, [modules/ecs/main.tf L24][ecs-main-24]; task `server-<suffix>`, [task_definitions.tf L244][ecs-td-244] | `aws_ecs_service.drain`, [main.tf L77][ecs-main-77]; task `drain-<suffix>`, [L257][ecs-td-257] | none | `aws_ecs_service.vcs_gateway`, [main.tf L103][ecs-main-103]; task `vcs-gateway-<suffix>`, [L270][ecs-td-270]; both only when `vcs_gateway_security_group_id` is set (`count` on the next line of each) | `400fcb9b1a9a` |
| AWS EKS | Helm chart, values rendered by [helm-values.tftpl L12-L18][eks-helm] | [L20-L26][eks-helm] | none | [L28-L46][eks-helm] | `358b62dedfaa` |
| Azure AKS | Helm chart, values from [helm-values.tftpl L6-L7][aks-helm] | [L9-L10][aks-helm] | none | [L21-L32][aks-helm] | `427f0eae36dd` |
| Google GKE | Helm chart, values from [helm-values.tftpl L10-L11][gke-helm] | [L13-L14][gke-helm] | none | [L47-L61][gke-helm] | `d9cfcb7081b8` |

Two copies of the chart exist at the baseline. `spacelift-helm-charts`
(`4e02e1d22e73`, chart `0.4.0`, `appVersion` `4.0.0` before publish-time
override) adds `shared.backendImage` (registry, repository, tag, digest,
`fips`) and deprecates `shared.image`; the newer dedicated repository
`spacelift-self-hosted-helm-chart` (`660c86391811`) still takes the image only
from `shared.image` and states that chart versions follow Spacelift versions
(chart `7.2.0` installs Spacelift `7.2.0`) [S] [Chart.yaml][hc-chart];
[S] [dedicated chart README][hc2-readme]. The docs link the former [D]
[advanced configuration][sh-adv]. Which repository is canonical is not stated
*(unverified)*.

The chart decides whether to deploy the standalone scheduler from
`scheduler.enabled`: `true` or `false` decide outright; `null` (the default)
reads the `vX.Y.Z` core of the backend image tag and deploys the scheduler
when the version is below `6.4.0` or cannot be read [S]
[_helpers.tpl L189-L225][hc-helpers]. A tag counts as readable only when it
matches `^v?X.Y.Z([-+].*)?$`, so a tag such as `latest` keeps the scheduler;
with `shared.backendImage.digest` set, only an explicit
`shared.backendImage.tag` is read [S] [_helpers.tpl L189-L204][hc-helpers]. Its `NOTES.txt` warns when the
scheduler is forced off on a version below `6.4.0`, because then nothing
fires drift detection, scheduled runs, tasks or deletes [S] [NOTES.txt][hc-notes].

### 2.3 Kubernetes objects the chart renders

| Object | Name | Condition | Source |
|---|---|---|---|
| `Deployment` | `<fullname>-server`, `-drain`, `-scheduler`, `-vcs-gateway` | as in 2.2 | [S] [templates][hc-templates] |
| `Service` | `<fullname>-server`, port `80` to `server` (`1983`) | always | [S] [service.yaml][hc-svc] |
| `Service` | `<fullname>-mqtt`, port `1984` to `mqtt` | `mqttService.enabled` (default `true`) | [S] [mqtt-service.yaml][hc-mqttsvc] |
| `Service` | `<fullname>-vcs-gateway`, ports `grpc` `1984` and `http` `1985` | `vcsGateway.enabled` | [S] [vcs-gateway-service.yaml][hc-vcssvc] |
| `Ingress` | `server-v4` (path `/`, `Prefix`) and `server-v6` (path `/*`, `ImplementationSpecific`), TLS secret `cert` for `shared.serverHostname` | `ingress.enabled`, `ingressV6.enabled` (both default `true`) | [S] [server-ingress.yaml][hc-ing4]; [S] [server-ingress-v6.yaml][hc-ing6] |
| `Ingress`, or `Gateway` plus `HTTPRoute` | `vcs-gateway` | `vcsGateway.ingress.enabled` or `vcsGateway.gateway.enabled` | [S] [templates][hc-templates] |
| `PodDisruptionBudget` | one per workload | `<component>.pdb.enabled` | [S] [templates][hc-templates] |
| `ServiceAccount` | `spacelift-backend` (shared) or one per component | `serviceAccount.create` (default `true`) or `<component>.serviceAccount.create` | [S] [values.yaml L207-L212][hc-values] |
| any | `extraManifests` | rendered through `tpl` | [S] [values.yaml L258-L260][hc-values] |

The server `Deployment` uses `maxSurge: 0` and `maxUnavailable: 90%`, so a
rollout never adds a pod and replaces nearly all pods at once [S]
[server-deployment.yaml L9-L21][hc-server]. Default replica counts are 3 for
every workload; default requests are server `200m`/`200Mi`, drain
`100m`/`100Mi`, scheduler `50m`/`50Mi`, VCS Gateway `100m`/`100Mi` [S]
[values.yaml][hc-values]. The ingress sets
`nginx.ingress.kubernetes.io/proxy-body-size: "0"`, removing the request body
limit [S] [values.yaml L227-L229][hc-values].

## 3. External dependencies

| Dependency | Mandatory | Options | Built-in alternative | Source |
|---|---|---|---|---|
| PostgreSQL | Yes | Any PostgreSQL 14 to 18; managed service recommended | none | [D] [external dependencies][sh-deps] |
| Object storage | Yes | AWS S3, Google Cloud Storage, Azure Blob Storage, S3-compatible | none | [D] [external dependencies][sh-deps] |
| Message queue | No | AWS SQS | Postgres-backed queue (recommended for new installs) | [D] [message queues][sh-mq] |
| Encryption | No | AWS KMS | RSA key plus AES-256 data key | [D] [encryption][sh-enc] |
| MQTT broker | Only for MQTT-transport workers | AWS IoT Core | Built into the server | [D] [MQTT broker][sh-mqtt] |
| Container registry | Yes | Any registry reachable from the runtime | none | [D] [environment requirements][sh-env] |
| Runtime | Yes | Kubernetes (recommended) or another container platform such as ECS | none | [D] [environment requirements][sh-env] |
| Telemetry backend | No | Datadog, AWS X-Ray, OpenTelemetry | Disabled | [D] [telemetry][sh-tel] |
| Spacelift public API | No | `https://app.spacelift.io` for automatic usage reporting | manual export | [D] [usage reporting][sh-usage] |
| Email (SMTP or similar) | Not documented | none of the self-hosted pages or modules configure email | none | [I] from a search of the self-hosted installation pages and every module source for SMTP or email settings, which found none |

The components can be mixed, for example Google object storage with SQS
[D] [external dependencies][sh-deps].

### 3.1 PostgreSQL

| Aspect | Value | Source |
|---|---|---|
| Supported versions | 14.x to 18.x; 13 and older are unsupported; 19 and newer untested. The environment requirements page says only "14 or later"; the external dependencies page is the more specific | [D] [external dependencies][sh-deps]; [D] [environment requirements][sh-env] |
| Database | A dedicated database with full administrative rights; an existing server may be reused | [D] [external dependencies][sh-deps] |
| Extensions | None documented, and no module enables one | [I] from the docs and module sources; *(unverified)* whether the migrations create any extension |
| Connection | `DATABASE_URL` (required), `DATABASE_READ_ONLY_URL` (set by the Kubernetes modules); the AWS module appends `?statement_cache_capacity=0` and port `5432` to database `spacelift` | [D] [general configuration][sh-gen]; [S] [rds/main.tf L2, L16][aws-rds] |
| IAM authentication | `DATABASE_IAM_AUTH=true` (AWS only): each new connection uses a locally generated, 15-minute RDS auth token signed with SigV4; needs `rds-db:connect`; the IAM user is granted `rds_iam` and the `spacelift` role that owns the migrated tables. ECS v3.1.0 and EKS v4.2.0 or later. With the AWS module v3.2.0 or later and `rds_iam_username` set, the database secret also carries passwordless `DATABASE_IAM_URL` and `DATABASE_IAM_READ_ONLY_URL` keys, which the ECS task maps in place of the password-carrying pair | [D] [RDS IAM authentication][sh-rdsiam]; [RN] v7.0.0; [S] [rds/main.tf L183-L184][aws-rds] |
| Tuning set by modules | AWS: `statement_timeout = 120000`; Azure: `max_connections = 200`; GCP: `max_connections = 1034`, `cloudsql.iam_authentication = on` | [S] [rds/main.tf L149-L165][aws-rds]; [S] [postgres/main.tf L92-L96][az-pg]; [S] [db/main.tf L28-L35][gcp-db] |
| Major upgrades | 15 to 30 minutes of downtime; webhooks arriving during it are lost; scale workers to zero first so runs do not fail to report; services self-heal afterwards | [D] [external dependencies][sh-deps] |
| Database in Kubernetes | Possible but discouraged; needs a persistent volume | [D] [external dependencies][sh-deps] |

### 3.2 Object storage buckets

The documented bucket set, with the AWS and GCP module defaults beside it.
"Retention" is the object expiry; "Versioning" is what the docs ask for and
what each module configures.

| Bucket (docs name) | Env var | Purpose | Docs versioning | Docs retention | AWS module | GCP module | Azure container |
|---|---|---|---|---|---|---|---|
| Deliveries | `OBJECT_STORAGE_BUCKET_DELIVERIES` | Request and response bodies of outbound webhook deliveries | No | configurable, 1 day suggested | `spacelift-deliveries-<suffix>`, unversioned, 1 day, [L47][aws-s3-47] | `spacelift-deliveries-<seed>`, 1 day, [L337][gcp-s3-337] | `spacelift-deliveries`, 1 day |
| Large queue messages | `OBJECT_STORAGE_BUCKET_LARGE_QUEUE_MESSAGES` | Inbound webhook payloads too large for a queue message | No | 2 days | `spacelift-large-queue-messages-<suffix>`, 2 days, [L98][aws-s3-98] | 2 days, [L17][gcp-s3-17] | `spacelift-large-queue-messages`, 2 days |
| Metadata | `OBJECT_STORAGE_BUCKET_METADATA` | Metadata workers need to process runs | No | 2 days | 2 days, [L149][aws-s3-149] | 2 days, [L42][gcp-s3-42] | `spacelift-metadata`, 2 days |
| Modules | `OBJECT_STORAGE_BUCKET_MODULES` | Terraform modules and providers (private registry) | Suggested | none | versioned, no expiry, [L200][aws-s3-200] | versioned, no expiry, [L68][gcp-s3-68] | `spacelift-modules`, no rule |
| Policy inputs | `OBJECT_STORAGE_BUCKET_POLICY_INPUTS` | Sampled policy evaluations | No | configurable, 7 days suggested | versioned, **120 days**, [L237][aws-s3-237] | versioned, 7 days, [L80][gcp-s3-80] | `spacelift-policy-inputs`, 7 days |
| Run logs | `OBJECT_STORAGE_BUCKET_RUN_LOGS` | Run log content | Suggested | configurable, 60 days suggested | versioned, 60 days, [L298][aws-s3-298] | versioned, 60 days, [L101][gcp-s3-101] | `spacelift-run-logs`, 60 days |
| Run observability | `OBJECT_STORAGE_BUCKET_RUN_OBSERVABILITY` (optional; falls back to run logs) | Worker-side OpenTelemetry traces, CPU profiles and JSON-formatted logs; empty until the producing feature ships | Suggested | configurable, 60 days suggested | versioned, 60 days, [L360][aws-s3-360] | versioned, 60 days, [L140][gcp-s3-140] | `spacelift-run-observability`, 60 days |
| States | `OBJECT_STORAGE_BUCKET_STATES` | Terraform and OpenTofu state for the built-in state backend | Required | none | versioned, no expiry, [L422][aws-s3-422] | versioned, noncurrent versions deleted after 30 days, [L180][gcp-s3-180] | `spacelift-states`, see note |
| Uploads | `OBJECT_STORAGE_BUCKET_UPLOADS` and `OBJECT_STORAGE_BUCKET_UPLOADS_URL` | Temporary browser uploads such as state files at stack creation; the URL feeds the Content Security Policy; needs CORS for `PUT` and `POST` from the frontend origin | Suggested | 1 day | versioned, 1 day, CORS, [L459][aws-s3-459] | versioned, 1 day, CORS, [L211][gcp-s3-211] | `spacelift-uploads`, 1 day; CORS on the account |
| User uploaded workspaces | `OBJECT_STORAGE_BUCKET_USER_UPLOADED_WORKSPACES` | Workspaces uploaded for local preview | Suggested | 1 day | versioned, 1 day, [L533][aws-s3-533] | versioned, 1 day, [L257][gcp-s3-257] | `spacelift-user-uploaded-workspaces`, 1 day |
| Workspace | `OBJECT_STORAGE_BUCKET_WORKSPACE` | Workspaces of runs waiting for confirmation | Suggested | 7 days | versioned, **90 days**, [L595][aws-s3-595] | versioned, 7 days, [L297][gcp-s3-297] | `spacelift-workspaces`, 7 days |
| (not in the docs table) Binaries | none | Launcher binaries for EC2 worker pools; the CloudFormation installer's "downloads" bucket | n/a | n/a | `spacelift-binaries-<suffix>`, versioned, no encryption, [L31][aws-s3-31] | none | none |
| (not in the docs table) Usage analytics | `OBJECT_STORAGE_BUCKET_USAGE_ANALYTICS` | Set to an empty string by every module | n/a | n/a | none | none | none |

Sources: [D] [object storage][sh-obj]; [S] AWS [modules/s3/main.tf][aws-s3]
(bucket names L1-L15, expiries L17-L27); [S] GCP
[modules/storage/main.tf][gcp-s3] (names L1-L15); [S] Azure
[modules/container_storage/main.tf][az-cs] (container list L19-L33, the `for_each` resource
[`azurerm_storage_container.spacelift-container` L35][az-cs-35], rules L44-L124); [S] the empty `OBJECT_STORAGE_BUCKET_USAGE_ANALYTICS` in
[ECS task_definitions.tf L104-L105][ecs-td] and the Kubernetes secret
templates; [S] the v2-to-v3 kit, which maps the v2 `downloads` bucket to the
v3 `binaries` bucket [S] [s3_to_terraform.py L180][kit-s3].

Notes on the table:
- The AWS module's default expiries for policy inputs (120 days) and
  workspace (90 days) differ from the docs' suggested 7 days; all AWS expiries
  are overridable through `s3_bucket_configuration` [S]
  [s3/main.tf L17-L27][aws-s3]. The docs are the higher source for intent;
  the module is what an installation gets by default.
- Noncurrent versions expire after `s3_noncurrent_version_expiration_days`
  (default 7) only on the six versioned buckets that have a lifecycle
  configuration: policy inputs, run logs, run observability, uploads, user
  uploaded workspaces and workspace. The modules, states and binaries buckets
  are versioned with no lifecycle configuration, so they keep every
  noncurrent version [S] [s3/main.tf L243-L255, L316-L329][aws-s3];
  [S] [variables.tf L335][aws-vars-335].
- Versioning is on for nine AWS buckets; deliveries, large queue messages and
  metadata are unversioned [S] [s3/main.tf][aws-s3].
- AWS buckets except `binaries` are encrypted with the module's KMS master key
  and have public access blocks (eleven blocks, each gated on
  `enable_public_access_block_on_s3`, default `true`). The nine buckets with a
  lifecycle configuration also abort incomplete multipart uploads after 2
  days. The `binaries` bucket has versioning only: no encryption
  configuration, no public access block and no lifecycle [S]
  [s3/main.tf L29-L44, L76-L87][aws-s3].
- GCP buckets enforce public access prevention, and the backend service
  account gets `roles/storage.objectAdmin` on each; data access audit logs
  (`ADMIN_READ`, `DATA_READ`, `DATA_WRITE`) are enabled for Cloud Storage [S]
  [storage/iam.tf][gcp-s3-iam].
- Azure uses one storage account `spacelift<seed>` (Standard, LRS, versioning
  on, CORS for `https://<app_domain>`) holding eleven private containers, and
  expires blobs by container-name prefix [S] [container_storage/main.tf L1-L124][az-cs].
  Its `DeleteAfter2Days` rule lists `spacelift-states` among its prefixes [S]
  [L67-L85][az-cs]. Read literally, that expires current state blobs two days
  after creation; whether this is intended, and how versioning interacts with
  it, is *(unverified)*.
- Buckets need no public access. Browsers and workers reach them only through
  pre-signed URLs issued by the backend, so the object store's endpoint must be
  reachable from browsers and workers [D] [object storage][sh-obj].
- S3-compatible stores must support path-style addressing, SigV4 pre-signed
  `GET` and `PUT`, multipart upload, `x-amz-meta-*` headers whose names contain
  underscores, lifecycle expiry (Spacelift does not delete expired objects
  itself), and object versioning with version listing and copy on the states
  bucket; requests are signed for `us-east-1` unless
  `OBJECT_STORAGE_S3COMPATIBLE_REGION` says otherwise [D] [object storage][sh-obj].
- The disaster recovery guide names modules, policy inputs, run logs and states
  as the buckets whose loss matters; losing the others causes transient run
  failures only [D] [disaster recovery][sh-dr].

### 3.3 Message queues

Eight logical queues; the drain consumes all of them [D] [message queues][sh-mq];
[D] [observability][sh-obs]. The AWS module creates the SQS queues only when
`create_sqs` is `true` [S] [main.tf L116-L118][aws-main].

| Logical queue | Carries | SQS name in the AWS module | Visibility | Retention | FIFO | DLQ | Drain concurrency variable | Module line |
|---|---|---|---|---|---|---|---|---|
| `async-jobs` | Background work: run state changes, notifications, drift detection, billing | `spacelift-async-jobs` | 300 s | default | no | `dlq` | `DRAIN_CONCURRENCY_ASYNC_JOBS` | [L18][aws-sqs-18] |
| `async-jobs.fifo` | Background work that must stay ordered | `spacelift-async-jobs.fifo` | 300 s | default | yes, dedup per message group | `dlq.fifo` | `DRAIN_CONCURRENCY_ASYNC_JOBS_FIFO` | [L32][aws-sqs-32] |
| `cronjobs` | Cron triggers | `spacelift-cronjobs` | 300 s | 3600 s | no | `dlq` | `DRAIN_CONCURRENCY_CRONJOBS` | [L49][aws-sqs-49] |
| `dlq` | Messages that failed repeatedly | `spacelift-dlq` | 300 s | default | no | none | `DRAIN_CONCURRENCY_DLQ` | [L2][aws-sqs-2] |
| `dlq.fifo` | Ordered messages that failed repeatedly | `spacelift-dlq.fifo` | 300 s | default | yes | none | `DRAIN_CONCURRENCY_DLQ_FIFO` | [L9][aws-sqs-9] |
| `events-inbox` | Internal domain events fanned out to several consumers | `spacelift-events-inbox` | 300 s | default | no | `dlq` | `DRAIN_CONCURRENCY_EVENTS` | [L64][aws-sqs-64] |
| `iot` | Messages sent by workers, such as heartbeats and run progress, forwarded off the MQTT broker | `spacelift-iot` | 45 s | default | no | `dlq` | `DRAIN_CONCURRENCY_IOT` | [L78][aws-sqs-78] |
| `webhooks` | Inbound VCS and integration webhooks | `spacelift-webhooks` | 600 s | default | no | `dlq` | `DRAIN_CONCURRENCY_WEBHOOKS` | [L92][aws-sqs-92] |

- **Selection.** `MESSAGE_QUEUE_TYPE` is `sqs` or `postgres`; the docs give `sqs`
  as the application default but recommend `postgres` for new installs, and
  every module sets it explicitly: ECS and EKS choose `sqs` only when queues
  are passed or created (`create_sqs`, default `false`), AKS and GKE always
  `postgres` [D] [message queues][sh-mq]; [S] [ECS task_definitions.tf L60-L61][ecs-td];
  [S] [AKS secrets L14][aks-secrets]; [S] [GKE secrets L14][gke-secrets].
- **SQS settings.** Max receive count 3 on every redrive policy; long polling
  (`receive_wait_time_seconds = 20`) on all six queues that are not dead-letter
  queues; every queue is encrypted with the KMS master key [D] [message queues][sh-mq]; [S]
  [sqs/main.tf][aws-sqs].
- **IoT Core requires SQS**, because an IoT topic rule
  (`SELECT *, Timestamp() as timestamp, topic(3) as worker_pool_ulid, topic(4) as worker_ulid FROM 'spacelift/writeonly/#'`)
  is what moves worker messages onto the `iot` queue, base64-encoded, through a
  role allowed `sqs:SendMessage` [D] [MQTT broker][sh-mqtt]; [S]
  [sqs/iot.tf L49-L64][aws-iot].
- **Postgres queue.** No configuration beyond the type; queue data lives in the
  database, so database backups cover it [D] [message queues][sh-mq]; [D]
  [disaster recovery][sh-dr]. With telemetry on, it emits
  `postgres_queue.messages.sent`, `.received`, `.changed_visibility`,
  `.deleted` (counters) and `.total`, `.visible` (gauges) [D] [observability][sh-obs].
- **Drain tuning.** Each `DRAIN_CONCURRENCY_*` sets the receivers per queue per
  drain instance (module default `1`, which processes a queue sequentially);
  `MESSAGE_PROCESSING_TIMEOUT_SECONDS` (default 900) bounds a message's
  processing time. Webhook backlogs of several minutes are normal [D]
  [observability][sh-obs].

### 3.4 MQTT broker and worker transport

| Setting | Values | Default | Source |
|---|---|---|---|
| `MQTT_BROKER_TYPE` | `iotcore`, `builtin` | `iotcore` (application); every module sets `builtin` unless told otherwise | [D] [MQTT broker][sh-mqtt]; [S] ECS [variables.tf L516][ecs-vars-516]; [S] [AKS secrets L10][aks-secrets] |
| `MQTT_BROKER_ENDPOINT` | `<id>-ats.iot.<region>.amazonaws.com` (IoT Core) or `<host>:<port>` (built-in; any port but `1983`); the Kubernetes modules use `tls://<mqtt domain>:1984` | required | [D] [MQTT broker][sh-mqtt]; [S] [AKS secrets L11][aks-secrets] |
| `MQTT_BROKER_BUILTIN_LOG_LEVEL` | log level | `error` | [D] [MQTT broker][sh-mqtt] |
| `MQTT_BROKER_IOTCORE_NAMESPACE` | topic prefix | `spacelift` | [D] [MQTT broker][sh-mqtt] |

IoT Core topics are `<namespace>/readonly/<workerpool-ulid>/<worker-ulid>`
(server to worker) and `<namespace>/writeonly/<workerpool-ulid>/<worker-ulid>`
(worker to server) [D] [MQTT broker][sh-mqtt]. For IoT Core, the server and
drain IAM roles create and attach per-worker certificates and policies
(`iot:CreateKeysAndCertificate`, `iot:CreateCertificateFromCsr`,
`iot:AttachPolicy` and related actions) [S] [iam_server.tf][iam-server];
[S] [iam_drain_and_server.tf][iam-ds]; `AWS_SECONDARY_REGION` replicates
worker certificates to a failover region [D] [disaster recovery][sh-dr].

The MQTT broker hostname only has to be reachable by workers, so an in-cluster
DNS name works when workers run in the same cluster [D] [environment
requirements][sh-env]. Workers on the HTTP long-poll transport
(`SPACELIFT_WORKER_COMMS_PROTOCOL=poll`, available since v6.4.0) need only
outbound HTTPS to the server hostname and no broker [RN] v6.4.0;
[D] [worker pools][sh-wp]. MQTT, both IoT Core and the built-in broker, is
deprecated: the worker pools page gives a sunset of March 30, 2027 and
recommends switching by January 1, 2027 [D] [worker pools][sh-wp]. The
v7.1.0 release notes only ask installations to finish the switch by
January 1, 2027 and name neither a removal date nor a removal release
[RN] v7.1.0.

### 3.5 Encryption and KMS

| Setting | Values | Default | Source |
|---|---|---|---|
| `ENCRYPTION_TYPE` | `kms`, `rsa` | `kms` | [D] [encryption][sh-enc] |
| `ENCRYPTION_KMS_ENCRYPTION_KEY_ID` | KMS key, `ENCRYPT_DECRYPT`, `SYMMETRIC_DEFAULT` | required for `kms` | [D] [encryption][sh-enc] |
| `ENCRYPTION_KMS_SIGNING_KEY_ID` | KMS key, `SIGN_VERIFY`, `RSA_4096`, signs JWTs | required for `kms` | [D] [encryption][sh-enc] |
| `ENCRYPTION_RSA_PRIVATE_KEY` | base64 of an unencrypted PEM RSA private key | required for `rsa` | [D] [encryption][sh-enc] |

- With RSA, the key encrypts an AES-256 data key generated at first setup and
  stored in the database; that data key encrypts sensitive database fields,
  and the same RSA key serves for token signing [D] [encryption][sh-enc];
  [D] [external dependencies][sh-deps].
- The choice is permanent: switching between KMS and RSA would mean
  re-encrypting every stored secret, which is unsupported; losing the key
  loses every encrypted credential [D] [encryption][sh-enc].
- KMS keys cannot later become multi-region, which blocks region moves and
  failover; the AWS module makes all three keys multi-region by default [D]
  [encryption][sh-enc]; [S] [variables.tf L35-L50][aws-vars].
- The AWS module creates three keys: a master key for AWS resource encryption
  (S3 except `binaries`, SQS, RDS and ECR), an in-app encryption key, and a
  `SIGN_VERIFY` `RSA_4096` JWT key; the first two rotate yearly [S]
  [kms/main.tf L1, L26, L51][aws-kms]. The Secrets Manager secrets (the RDS
  connection string and the ECS shared secrets) set no KMS key, so they use
  the AWS-managed key [S] [rds/main.tf L168][aws-rds];
  [S] [ECS secrets.tf L1][ecs-secrets].
- `kms_arn` replaces the whole `kms` module, not just the master key: with it
  set, none of the three keys is created, the caller's key becomes the master
  key, and the `kms_encryption_key_arn` and `kms_signing_key_arn` outputs are
  null, so in-app encryption and JWT signing need keys supplied some other way
  [S] [main.tf L13-L25][aws-main]; [S] [outputs.tf L11-L19][aws-outputs].
- The EKS module adds a fourth key when `ebs_encryption.enabled` is set without
  a `kms_key_arn`: `aws_kms_key.ebs` for node EBS volumes, with alias
  `alias/eks-ebs-<suffix>`, yearly rotation and a 7-day deletion window [S]
  [ebs-kms.tf L6, L118-L136][eks-ebs-kms].
- AKS and GKE always use `rsa` [S] [AKS secrets L12-L13][aks-secrets];
  [S] [GKE secrets L12-L13][gke-secrets].

### 3.6 Observability

| Aspect | Value | Source |
|---|---|---|
| Logs | JSON on stdout and stderr from every component; few info-level logs because Spacelift debugs with traces; since v7.0.0 the server logs an info entry, `Executing GraphQL request`, with a sanitized query for every GraphQL request | [D] [external dependencies][sh-deps]; [D] [observability][sh-obs]; [RN] v7.0.0 |
| Tracing and metrics | `OBSERVABILITY_VENDOR` = `Datadog` (`DD_AGENT_HOST`), `AWS` (`AWS_XRAY_DAEMON_ADDRESS`; metrics to CloudWatch, needs `cloudwatch:PutMetricData`), `OpenTelemetry` (`OTEL_EXPORTER_OTLP_ENDPOINT`, gRPC, typically `http://<collector>:4317`) or `Disabled`; server, drain and workers all honour it; the service name is set automatically | [D] [telemetry][sh-tel] |
| Module defaults | ECS `observability_vendor = "Disabled"` (its description calls `AWS` deprecated in favour of OpenTelemetry, which can also feed X-Ray), with optional Datadog and ADOT sidecars that set `DD_AGENT_HOST=127.0.0.1` or `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317`; the Kubernetes modules hard-code `Disabled` in the secret | [S] [ECS variables.tf L429][ecs-vars-429]; [S] [ECS task_definitions.tf L165-L177][ecs-td]; [S] [EKS secrets L38][eks-secrets] |
| What to watch | Server and drain CPU and memory, load balancer latency and 5xx rate, database CPU, memory and connections, queue depth (`ApproximateNumberOfMessagesVisible` on SQS) | [D] [observability][sh-obs] |
| Guides | Kubernetes guides for Datadog, OpenTelemetry with Jaeger, and OpenTelemetry with Tempo and Grafana | [D] [telemetry][sh-tel] |
| Run observability | Workers upload traces, CPU profiles and JSON logs to the run observability bucket (section 3.2) | [RN] v6.4.1 |

### 3.7 Other configuration

| Setting | Meaning | Source |
|---|---|---|
| `SERVER_DOMAIN` | Hostname users reach; also the first-setup and admin-login URL | [D] [general configuration][sh-gen] |
| `ADMIN_USERNAME`, `ADMIN_PASSWORD` | Enable `/admin-login` for first setup and break-glass; empty or absent disables it | [D] [general configuration][sh-gen] |
| `AWS_ACCOUNT_ID`, `AWS_REGION`, `AWS_DEFAULT_REGION`, `AWS_PARTITION` (default `aws`), `AWS_SECONDARY_REGION` | AWS identity and failover region; credentials come from the SDK chain | [D] [general configuration][sh-gen] |
| `GCP_PROJECT` | Project holding the GCP resources; credentials from Application Default Credentials | [D] [general configuration][sh-gen] |
| `OBJECT_STORAGE_TYPE` | `aws` (default), `gcp`, `azure`, `s3compatible` (`minio` deprecated until v8.0.0) | [D] [object storage][sh-obj]; [RN] v7.1.0 |
| `OBJECT_STORAGE_AZURE_ACCOUNT_URL` | Azure storage account URL; credentials from `DefaultAzureCredential` | [D] [object storage][sh-obj] |
| `OBJECT_STORAGE_S3COMPATIBLE_ENDPOINT`, `_ACCESS_KEY_ID`, `_SECRET_ACCESS_KEY`, `_USE_SSL` (`false`), `_ALLOW_INSECURE` (`false`), `_REGION` (`us-east-1`) | S3-compatible store; endpoint as `host:port` without a scheme | [D] [object storage][sh-obj] |
| `ADDITIONAL_ROOT_CAS`, `ADDITIONAL_ROOT_CAS_FILE` | Base64 JSON `{ "caCertificates": [<base64 PEM>, ...] }` (each certificate base64-encoded twice); the file variant avoids the 128 KiB per-variable limit; the inline variable wins; an undecodable entry stops the service from starting; set on server and drain, and on VM launchers | [D] [advanced configuration][sh-adv] |
| `SPACELIFT_PUBLIC_API` | `https://app.spacelift.io` enables automatic usage reporting from the drain; empty by default | [D] [usage reporting][sh-usage] |
| `WEBHOOKS_ENDPOINT` | Public URL for inbound webhooks (server only) | [S] [ECS server.tf L55-L56][ecs-cd-server] |
| `LAUNCHER_IMAGE`, `LAUNCHER_IMAGE_TAG` | Launcher image for Kubernetes workers; needed on both server and drain since v6.4.0 so long-poll payloads carry a valid image reference | [RN] v6.4.1 |
| `VCS_GATEWAY_ENDPOINT` | `<vcs gateway domain>:443`, set when the VCS Gateway is enabled | [S] [ECS task_definitions.tf][ecs-td] |
| `GATEWAY_GRPC_PORT`, `GATEWAY_HTTP_PORT`, `VCS_GATEWAY_FORCE_IP` | VCS Gateway ports (`1984`, `1985`) and the pod IP it advertises to server and drain | [S] [vcs-gateway-deployment.yaml L99-L106][hc-vcsgw] |
| `ENVIRONMENT` | Every module sets `prod` | [S] [EKS secrets L11][eks-secrets] |
| `MESSAGE_PROCESSING_TIMEOUT_SECONDS` | Per-message processing limit, default 900 | [D] [observability][sh-obs] |
| `SLACK_APP_CLIENT_ID`, `SLACK_APP_CLIENT_SECRET`, `SLACK_SECRET` | Static Slack app credentials (client ID, client secret, signing secret); the alternative is configuring the app in organization settings. Slack calls back to `/webhooks/slack` and `/slack_oauth`, so the full integration needs the server reachable from the public internet | [D] [Slack][sh-slack] |
| `GOVSLACK_CLIENT_ID`, `GOVSLACK_CLIENT_SECRET`, `GOVSLACK_SIGNING_SECRET` | GovSlack (`slack-gov.com`) credentials; settable only through the environment, read at backend start. Both Slack variants can be configured, but only one Slack integration can be connected at a time | [D] [Slack][sh-slack] |

## 4. Networking and ports

| From | To | Port | Protocol | Required | Purpose | Source |
|---|---|---|---|---|---|---|
| Users, VCS webhooks, API clients | Load balancer | 443 | HTTPS | Yes | UI, GraphQL and REST APIs, webhooks | [D] [networking][sh-net]; [S] [server_lb.tf L33-L41][ecs-lb-server] |
| Load balancer | Server | 1983 | HTTP | Yes | Application traffic; TLS ends at the load balancer | [D] [networking][sh-net]; [S] [server_lb.tf L12-L21][ecs-lb-server] |
| Load balancer health check | Server | 8080 | HTTP | Kubernetes modules | `/readiness` | [S] [EKS helm-values.tftpl L57-L59][eks-helm] |
| Workers (MQTT) | Server, through an NLB or Kubernetes Service | 1984 | MQTT over TLS | Built-in broker only | Work delivery | [D] [networking][sh-net]; [S] [mqtt_lb.tf][ecs-lb-mqtt] |
| Workers (MQTT) | AWS IoT Core | 443 | MQTT over TLS | IoT Core only | Work delivery | [D] [networking][sh-net] |
| Workers | Server | 443 | HTTPS | Yes | Synchronous calls (state changes, log URLs) and, in poll mode, work delivery | [D] [networking][sh-net]; [D] [worker pools][sh-wp] |
| Workers, browsers | Object storage | 443 | HTTPS | Yes | Pre-signed `GET`/`PUT` | [D] [networking][sh-net]; [D] [object storage][sh-obj] |
| Workers | VCS, registries, managed infrastructure | 443 or user-defined | TCP | Yes | Source, runner and launcher images, cloud APIs | [D] [networking][sh-net] |
| VCS agents | Load balancer for the VCS Gateway | 443 | gRPC over TLS | VCS agent pools only | Agent tunnel | [D] [EKS guide][sh-eks]; [S] [vcs_gateway_lb.tf L13-L44][ecs-lb-vcs] |
| VCS Gateway load balancer | VCS Gateway | 1984 | gRPC (h2c) | VCS agent pools only | Agent tunnel | [S] [security.tf L120-L129][ecs-lb-sec] |
| Server, drain | VCS Gateway (pod IP) | 1985 | HTTP | VCS agent pools only | Requests to private VCS | [S] [security.tf L133-L155][ecs-lb-sec]; [D] [EKS guide][sh-eks] |
| Server, drain, VCS Gateway | PostgreSQL | 5432 (user-defined) | TCP | Yes | Database | [D] [networking][sh-net]; [S] [network/security_groups.tf L50-L95][aws-sg] |
| Server, drain | Object storage, VCS | 443 | HTTPS | Yes | Storage and VCS API | [D] [networking][sh-net] |
| Server, drain | SQS, IoT Core, KMS | 443 | HTTPS | Only when selected | AWS services | [D] [networking][sh-net] |
| Drain | `app.spacelift.io` | 443 | HTTPS | Only for automatic usage reporting | Usage upload | [D] [usage reporting][sh-usage] |
| Backend pods | Cloud SQL Auth Proxy (sidecar) | 5432 local; health 9801 | TCP | GKE | Database over private IP with IAM | [S] [server-deployment.yaml L47-L85][hc-server] |

Workers need no inbound access and need not share a network with the backend;
the drain needs no inbound access [D] [networking][sh-net]. The ECS module
puts the server behind an internet-facing ALB (TLS policy
`ELBSecurityPolicy-TLS13-1-2-2021-06`), the built-in broker behind an NLB on
TCP `1984` open to `0.0.0.0/0`, and the VCS Gateway behind a second ALB whose
target group uses `protocol_version = "GRPC"` [S] [server_lb.tf][ecs-lb-server];
[S] [mqtt_lb.tf][ecs-lb-mqtt]; [S] [vcs_gateway_lb.tf][ecs-lb-vcs];
[S] [security.tf L47-L57][ecs-lb-sec]. The AWS network module creates
security groups for drain, server, VCS Gateway (all with unrestricted egress)
and the database (ingress `5432` only from those three) [S]
[network/security_groups.tf][aws-sg]. GKE runs dual stack, with two static
global addresses, two `Ingress` objects, Cloud NAT for IPv4 egress, and a
private-IP Cloud SQL instance [D] [GKE guide][sh-gke]; [S]
[network/main.tf L31-L69][gcp-net]. When workers run outside the cluster, the
EKS and GKE modules turn the MQTT `Service` into a `LoadBalancer` [S]
[EKS helm-values.tftpl L64-L73][eks-helm]; [S] [GKE helm-values.tftpl L31-L45][gke-helm].
The on-prem guide recommends running workers in the same cluster so that the
broker never needs a load balancer [D] [on-prem guide][sh-onprem].

The air-gapped guide covers installations without internet access [D]
[air-gapped guide][sh-air]:

- **VCS.** Without connectivity to the VCS provider, VCS agents (through the
  VCS Gateway) bridge the networks [D] [air-gapped guide][sh-air].
- **EC2 workers.** The launcher (`bin/spacelift-launcher` or
  `bin/spacelift-launcher-arm64` from the release archive) is uploaded to a
  bucket the workers can read, such as the binaries bucket, and the worker
  pool module's `s3_uri` points at it. Each upgrade repeats the upload and
  needs the instances cycled, because `s3_uri` does not change [D]
  [air-gapped guide][sh-air].
- **Runs.** Stacks use a custom workflow tool and a runner image that
  carries the OpenTofu or Terraform binary and a provider cache [D]
  [air-gapped guide][sh-air].

## 5. Environment variables by deployment flavour

Generated by script: every backend environment variable the modules
and chart set, with the first file and line where each flavour sets it, and
whether the self-hosted reference pages name it. Variables only documented,
and not set by any module, are in sections 3.4 to 3.7.

| Variable | ECS | EKS | AKS | GKE | Helm | In reference docs |
|---|---|---|---|---|---|---|
| `ADMIN_PASSWORD` | [server.tf#L47](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/server.tf#L47) | [kubernetes-secrets.tftpl#L70](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L70) | [kubernetes-secrets.tftpl#L48](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L48) | [kubernetes-secrets.tftpl#L47](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L47) |  | yes |
| `ADMIN_USERNAME` | [server.tf#L43](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/server.tf#L43) | [kubernetes-secrets.tftpl#L69](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L69) | [kubernetes-secrets.tftpl#L47](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L47) | [kubernetes-secrets.tftpl#L46](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L46) |  | yes |
| `AOT_CONFIG_CONTENT` | [secrets.tf#L12](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/secrets.tf#L12) |  |  |  |  |  |
| `AWS_ACCOUNT_ID` | [task_definitions.tf#L12](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L12) | [kubernetes-secrets.tftpl#L8](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L8) |  |  |  | yes |
| `AWS_DEFAULT_REGION` | [task_definitions.tf#L16](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L16) | [kubernetes-secrets.tftpl#L9](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L9) |  |  |  | yes |
| `AWS_PARTITION` | [task_definitions.tf#L24](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L24) |  |  |  |  | yes |
| `AWS_REGION` | [task_definitions.tf#L20](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L20) | [kubernetes-secrets.tftpl#L10](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L10) |  |  |  | yes |
| `AWS_S3_US_EAST_1_REGIONAL_ENDPOINT` | [server.tf#L51](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/server.tf#L51) |  |  |  |  |  |
| `CSQL_PROXY_HEALTH_CHECK` |  |  |  |  | [drain-deployment.yaml#L42](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/drain-deployment.yaml#L42) |  |
| `CSQL_PROXY_HTTP_ADDRESS` |  |  |  |  | [drain-deployment.yaml#L46](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/drain-deployment.yaml#L46) |  |
| `CSQL_PROXY_HTTP_PORT` |  |  |  |  | [drain-deployment.yaml#L44](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/drain-deployment.yaml#L44) |  |
| `CSQL_PROXY_QUIET` |  |  |  |  | [drain-deployment.yaml#L48](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/drain-deployment.yaml#L48) |  |
| `DATABASE_IAM_AUTH` | [task_definitions.tf#L156](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L156) | [kubernetes-secrets.tftpl#L35](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L35) |  |  |  | yes |
| `DATABASE_READ_ONLY_URL` |  | [kubernetes-secrets.tftpl#L34](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L34) | [kubernetes-secrets.tftpl#L31](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L31) | [kubernetes-secrets.tftpl#L30](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L30) |  | yes |
| `DATABASE_URL` |  | [kubernetes-secrets.tftpl#L33](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L33) | [kubernetes-secrets.tftpl#L30](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L30) | [kubernetes-secrets.tftpl#L29](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L29) |  | yes |
| `DD_AGENT_HOST` | [task_definitions.tf#L168](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L168) |  |  |  |  | yes |
| `DD_API_KEY` | [secrets.tf#L11](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/secrets.tf#L11) |  |  |  |  |  |
| `DD_APM_NON_LOCAL_TRAFFIC` | [sidecar_datadog.tf#L9](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/sidecar_datadog.tf#L9) |  |  |  |  |  |
| `DD_DOGSTATSD_NON_LOCAL_TRAFFIC` | [sidecar_datadog.tf#L13](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/sidecar_datadog.tf#L13) |  |  |  |  |  |
| `DD_SITE` | [sidecar_datadog.tf#L17](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/sidecar_datadog.tf#L17) |  |  |  |  |  |
| `DD_TAGS` | [sidecar_datadog.tf#L21](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/sidecar_datadog.tf#L21) |  |  |  |  | yes |
| `DD_VERSION` | [sidecar_datadog.tf#L25](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/sidecar_datadog.tf#L25) |  |  |  |  |  |
| `DRAIN_CONCURRENCY_ASYNC_JOBS` | [drain.tf#L39](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L39) | [kubernetes-secrets.tftpl#L80](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L80) | [kubernetes-secrets.tftpl#L58](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L58) | [kubernetes-secrets.tftpl#L57](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L57) |  | yes |
| `DRAIN_CONCURRENCY_ASYNC_JOBS_FIFO` | [drain.tf#L43](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L43) | [kubernetes-secrets.tftpl#L81](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L81) | [kubernetes-secrets.tftpl#L59](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L59) | [kubernetes-secrets.tftpl#L58](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L58) |  | yes |
| `DRAIN_CONCURRENCY_CRONJOBS` | [drain.tf#L47](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L47) | [kubernetes-secrets.tftpl#L82](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L82) | [kubernetes-secrets.tftpl#L60](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L60) | [kubernetes-secrets.tftpl#L59](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L59) |  | yes |
| `DRAIN_CONCURRENCY_DLQ` | [drain.tf#L51](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L51) | [kubernetes-secrets.tftpl#L83](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L83) | [kubernetes-secrets.tftpl#L61](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L61) | [kubernetes-secrets.tftpl#L60](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L60) |  | yes |
| `DRAIN_CONCURRENCY_DLQ_FIFO` | [drain.tf#L55](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L55) | [kubernetes-secrets.tftpl#L84](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L84) | [kubernetes-secrets.tftpl#L62](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L62) | [kubernetes-secrets.tftpl#L61](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L61) |  | yes |
| `DRAIN_CONCURRENCY_EVENTS` | [drain.tf#L59](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L59) | [kubernetes-secrets.tftpl#L85](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L85) | [kubernetes-secrets.tftpl#L63](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L63) | [kubernetes-secrets.tftpl#L62](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L62) |  | yes |
| `DRAIN_CONCURRENCY_IOT` | [drain.tf#L63](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L63) | [kubernetes-secrets.tftpl#L86](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L86) | [kubernetes-secrets.tftpl#L64](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L64) | [kubernetes-secrets.tftpl#L63](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L63) |  | yes |
| `DRAIN_CONCURRENCY_WEBHOOKS` | [drain.tf#L67](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L67) | [kubernetes-secrets.tftpl#L87](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L87) | [kubernetes-secrets.tftpl#L65](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L65) | [kubernetes-secrets.tftpl#L64](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L64) |  | yes |
| `ECS_FARGATE` | [sidecar_datadog.tf#L29](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/sidecar_datadog.tf#L29) |  |  |  |  |  |
| `ENABLE_INSTRUMENTATION_ENDPOINTS` |  |  |  |  | [drain-deployment.yaml#L121](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/drain-deployment.yaml#L121) |  |
| `ENCRYPTION_KMS_ENCRYPTION_KEY_ID` | [task_definitions.tf#L52](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L52) | [kubernetes-secrets.tftpl#L16](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L16) |  |  |  | yes |
| `ENCRYPTION_KMS_SIGNING_KEY_ID` | [task_definitions.tf#L56](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L56) | [kubernetes-secrets.tftpl#L17](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L17) |  |  |  | yes |
| `ENCRYPTION_RSA_PRIVATE_KEY` | [task_definitions.tf#L48](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L48) |  | [kubernetes-secrets.tftpl#L13](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L13) | [kubernetes-secrets.tftpl#L13](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L13) |  | yes |
| `ENCRYPTION_TYPE` | [task_definitions.tf#L44](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L44) | [kubernetes-secrets.tftpl#L15](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L15) | [kubernetes-secrets.tftpl#L12](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L12) | [kubernetes-secrets.tftpl#L12](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L12) |  | yes |
| `ENVIRONMENT` | [task_definitions.tf#L28](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L28) | [kubernetes-secrets.tftpl#L11](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L11) | [kubernetes-secrets.tftpl#L8](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L8) | [kubernetes-secrets.tftpl#L8](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L8) |  |  |
| `FEATURE_FLAG_SELF_HOSTED_V3_INSTALLATION_FLOW` |  |  |  |  | [server-deployment.yaml#L134](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/server-deployment.yaml#L134) |  |
| `GATEWAY_GRPC_PORT` | [vcs_gateway.tf#L39](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/vcs_gateway.tf#L39) |  |  |  | [vcs-gateway-deployment.yaml#L99](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/vcs-gateway-deployment.yaml#L99) |  |
| `GATEWAY_HTTP_PORT` | [vcs_gateway.tf#L43](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/vcs_gateway.tf#L43) |  |  |  | [vcs-gateway-deployment.yaml#L101](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/vcs-gateway-deployment.yaml#L101) |  |
| `LAUNCHER_IMAGE` | [task_definitions.tf#L124](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L124) | [kubernetes-secrets.tftpl#L40](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L40) | [kubernetes-secrets.tftpl#L37](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L37) | [kubernetes-secrets.tftpl#L36](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L36) |  | yes |
| `LAUNCHER_IMAGE_TAG` | [task_definitions.tf#L128](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L128) | [kubernetes-secrets.tftpl#L41](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L41) | [kubernetes-secrets.tftpl#L38](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L38) | [kubernetes-secrets.tftpl#L37](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L37) |  | yes |
| `LICENSE_TOKEN` | [secrets.tf#L10](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/secrets.tf#L10) | [kubernetes-secrets.tftpl#L37](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L37) | [kubernetes-secrets.tftpl#L33](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L33) | [kubernetes-secrets.tftpl#L32](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L32) |  | yes |
| `LICENSE_TYPE` | [task_definitions.tf#L116](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L116) | [kubernetes-secrets.tftpl#L36](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L36) | [kubernetes-secrets.tftpl#L32](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L32) | [kubernetes-secrets.tftpl#L31](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L31) |  | yes |
| `MESSAGE_QUEUE_SQS_ASYNC_FIFO_URL` | [task_definitions.tf#L138](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L138) | [kubernetes-secrets.tftpl#L51](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L51) |  |  |  | yes |
| `MESSAGE_QUEUE_SQS_ASYNC_URL` | [task_definitions.tf#L134](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L134) | [kubernetes-secrets.tftpl#L50](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L50) |  |  |  | yes |
| `MESSAGE_QUEUE_SQS_CRONJOBS_URL` | [task_definitions.tf#L142](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L142) | [kubernetes-secrets.tftpl#L52](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L52) |  |  |  | yes |
| `MESSAGE_QUEUE_SQS_DLQ_FIFO_URL` | [drain.tf#L77](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L77) | [kubernetes-secrets.tftpl#L54](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L54) |  |  |  | yes |
| `MESSAGE_QUEUE_SQS_DLQ_URL` | [drain.tf#L73](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L73) | [kubernetes-secrets.tftpl#L53](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L53) |  |  |  | yes |
| `MESSAGE_QUEUE_SQS_EVENTS_INBOX_URL` | [task_definitions.tf#L146](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L146) | [kubernetes-secrets.tftpl#L55](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L55) |  |  |  | yes |
| `MESSAGE_QUEUE_SQS_IOT_URL` | [drain.tf#L81](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L81) | [kubernetes-secrets.tftpl#L56](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L56) |  |  |  | yes |
| `MESSAGE_QUEUE_SQS_WEBHOOKS_URL` | [task_definitions.tf#L150](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L150) | [kubernetes-secrets.tftpl#L57](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L57) |  |  |  | yes |
| `MESSAGE_QUEUE_TYPE` | [task_definitions.tf#L60](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L60) | [kubernetes-secrets.tftpl#L18](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L18) | [kubernetes-secrets.tftpl#L14](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L14) | [kubernetes-secrets.tftpl#L14](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L14) |  | yes |
| `MQTT_BROKER_ENDPOINT` | [task_definitions.tf#L40](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L40) | [kubernetes-secrets.tftpl#L14](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L14) | [kubernetes-secrets.tftpl#L11](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L11) | [kubernetes-secrets.tftpl#L11](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L11) |  | yes |
| `MQTT_BROKER_TYPE` | [task_definitions.tf#L36](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L36) | [kubernetes-secrets.tftpl#L13](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L13) | [kubernetes-secrets.tftpl#L10](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L10) | [kubernetes-secrets.tftpl#L10](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L10) |  | yes |
| `OBJECT_STORAGE_AZURE_ACCOUNT_URL` |  |  | [kubernetes-secrets.tftpl#L16](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L16) |  |  | yes |
| `OBJECT_STORAGE_BUCKET_DELIVERIES` | [task_definitions.tf#L64](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L64) | [kubernetes-secrets.tftpl#L20](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L20) | [kubernetes-secrets.tftpl#L17](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L17) | [kubernetes-secrets.tftpl#L16](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L16) |  | yes |
| `OBJECT_STORAGE_BUCKET_LARGE_QUEUE_MESSAGES` | [task_definitions.tf#L68](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L68) | [kubernetes-secrets.tftpl#L21](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L21) | [kubernetes-secrets.tftpl#L18](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L18) | [kubernetes-secrets.tftpl#L17](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L17) |  | yes |
| `OBJECT_STORAGE_BUCKET_METADATA` | [task_definitions.tf#L88](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L88) | [kubernetes-secrets.tftpl#L29](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L29) | [kubernetes-secrets.tftpl#L19](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L19) | [kubernetes-secrets.tftpl#L18](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L18) |  | yes |
| `OBJECT_STORAGE_BUCKET_MODULES` | [task_definitions.tf#L72](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L72) | [kubernetes-secrets.tftpl#L22](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L22) | [kubernetes-secrets.tftpl#L20](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L20) | [kubernetes-secrets.tftpl#L19](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L19) |  | yes |
| `OBJECT_STORAGE_BUCKET_POLICY_INPUTS` | [task_definitions.tf#L76](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L76) | [kubernetes-secrets.tftpl#L23](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L23) | [kubernetes-secrets.tftpl#L21](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L21) | [kubernetes-secrets.tftpl#L20](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L20) |  | yes |
| `OBJECT_STORAGE_BUCKET_RUN_LOGS` | [task_definitions.tf#L80](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L80) | [kubernetes-secrets.tftpl#L24](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L24) | [kubernetes-secrets.tftpl#L22](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L22) | [kubernetes-secrets.tftpl#L21](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L21) |  | yes |
| `OBJECT_STORAGE_BUCKET_RUN_OBSERVABILITY` | [task_definitions.tf#L84](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L84) | [kubernetes-secrets.tftpl#L25](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L25) | [kubernetes-secrets.tftpl#L23](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L23) | [kubernetes-secrets.tftpl#L22](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L22) |  | yes |
| `OBJECT_STORAGE_BUCKET_STATES` | [task_definitions.tf#L92](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L92) | [kubernetes-secrets.tftpl#L26](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L26) | [kubernetes-secrets.tftpl#L24](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L24) | [kubernetes-secrets.tftpl#L23](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L23) |  | yes |
| `OBJECT_STORAGE_BUCKET_UPLOADS` | [task_definitions.tf#L108](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L108) | [kubernetes-secrets.tftpl#L31](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L31) | [kubernetes-secrets.tftpl#L28](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L28) | [kubernetes-secrets.tftpl#L27](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L27) |  | yes |
| `OBJECT_STORAGE_BUCKET_UPLOADS_URL` | [task_definitions.tf#L112](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L112) | [kubernetes-secrets.tftpl#L32](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L32) | [kubernetes-secrets.tftpl#L29](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L29) | [kubernetes-secrets.tftpl#L28](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L28) |  | yes |
| `OBJECT_STORAGE_BUCKET_USAGE_ANALYTICS` | [task_definitions.tf#L104](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L104) | [kubernetes-secrets.tftpl#L30](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L30) | [kubernetes-secrets.tftpl#L27](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L27) | [kubernetes-secrets.tftpl#L26](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L26) |  |  |
| `OBJECT_STORAGE_BUCKET_USER_UPLOADED_WORKSPACES` | [task_definitions.tf#L96](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L96) | [kubernetes-secrets.tftpl#L27](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L27) | [kubernetes-secrets.tftpl#L25](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L25) | [kubernetes-secrets.tftpl#L24](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L24) |  | yes |
| `OBJECT_STORAGE_BUCKET_WORKSPACE` | [task_definitions.tf#L100](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L100) | [kubernetes-secrets.tftpl#L28](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L28) | [kubernetes-secrets.tftpl#L26](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L26) | [kubernetes-secrets.tftpl#L25](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L25) |  | yes |
| `OBJECT_STORAGE_TYPE` |  | [kubernetes-secrets.tftpl#L19](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L19) | [kubernetes-secrets.tftpl#L15](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L15) | [kubernetes-secrets.tftpl#L15](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L15) |  | yes |
| `OBSERVABILITY_VENDOR` | [task_definitions.tf#L120](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L120) | [kubernetes-secrets.tftpl#L38](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L38) | [kubernetes-secrets.tftpl#L34](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L34) | [kubernetes-secrets.tftpl#L33](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L33) |  | yes |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | [task_definitions.tf#L174](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L174) |  |  |  |  | yes |
| `SERVER_DOMAIN` | [task_definitions.tf#L32](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L32) | [kubernetes-secrets.tftpl#L12](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L12) | [kubernetes-secrets.tftpl#L9](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L9) | [kubernetes-secrets.tftpl#L9](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L9) |  | yes |
| `SPACELIFT_PUBLIC_API` | [drain.tf#L35](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L35) | [kubernetes-secrets.tftpl#L39](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L39) | [kubernetes-secrets.tftpl#L35](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L35) | [kubernetes-secrets.tftpl#L34](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L34) |  | yes |
| `VCS_GATEWAY_ENDPOINT` | [task_definitions.tf#L162](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L162) | [kubernetes-secrets.tftpl#L45](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L45) | [kubernetes-secrets.tftpl#L36](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L36) | [kubernetes-secrets.tftpl#L35](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L35) |  |  |
| `VCS_GATEWAY_FORCE_IP` |  |  |  |  | [vcs-gateway-deployment.yaml#L103](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/vcs-gateway-deployment.yaml#L103) |  |
| `WEBHOOKS_ENDPOINT` | [server.tf#L55](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/server.tf#L55) | [kubernetes-secrets.tftpl#L71](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl#L71) | [kubernetes-secrets.tftpl#L49](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl#L49) | [kubernetes-secrets.tftpl#L48](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl#L48) |  |  |

77 variable names. Every module sets `ENVIRONMENT` to `prod` [S] (row above);
the reference pages do not describe it.

## 6. The four reference deployments compared

| Aspect | AWS ECS | AWS EKS | Azure AKS | Google GKE |
|---|---|---|---|---|
| Modules | `terraform-aws-spacelift-selfhosted` (infrastructure) plus `terraform-aws-ecs-spacelift-selfhosted` (services) | `terraform-aws-eks-spacelift-selfhosted`, which calls the AWS module at `?ref=v3.3.0` | `terraform-azure-spacelift-selfhosted` | `terraform-google-spacelift-selfhosted` |
| Compute | ECS cluster on Fargate (amd64 only: the task definitions set no architecture, so Fargate defaults to `X86_64` and cannot run the arm64 backend image [D] [ECS guide][sh-ecs]); server, drain and VCS Gateway services | EKS via `terraform-aws-modules/eks/aws ~> 21.0`, Auto Mode on by default | AKS cluster with an autoscaling default node pool | GKE Autopilot, private nodes |
| Default sizes | server 1024 CPU / 2048 MiB × 3; drain 2048 / 4096 × 3; VCS Gateway 1024 / 2048 × 3 | chart defaults (section 2.3) | chart defaults | chart defaults |
| Database | Aurora PostgreSQL (`aurora-postgresql`), `rds_engine_version` required (guides use `17.7`), `db.r6g.large` primary, IAM auth enabled on the cluster, storage encrypted, global database supported | Same module; default instance class `db.serverless` (the variable's description says `db.r6g.large`, the default value wins) | Flexible Server, `postgres_version` required, `B_Standard_B2s`, 32 GiB, private access only, Entra ID auth enabled | Cloud SQL, `database_version` (guides use `POSTGRES_17`), `ENTERPRISE` edition, `db-n1-standard-8`, zonal, PITR with 7-day logs, IAM auth, reached through the proxy sidecar |
| Object storage | 12 S3 buckets (section 3.2), KMS-encrypted | Same | 1 storage account, 11 containers | 11 GCS buckets |
| Encryption | KMS (3 keys) or RSA | KMS (the same 3 keys, plus an EBS key when `ebs_encryption` asks for one, section 3.5) or RSA | RSA | RSA |
| Message queue | Postgres, or SQS when `sqs_queues` is passed | Postgres, or SQS with `create_sqs` | Postgres | Postgres |
| MQTT broker | built-in (NLB on 1984) or IoT Core | built-in (in-cluster Service, NLB for external workers) or IoT Core | built-in | built-in (L4 load balancer for external workers) |
| Registry | ECR `spacelift-backend-<suffix>` and `spacelift-launcher-<suffix>`, image-count retention | Same | ACR `selfhosted<seed>` (`AcrPull` for nodes) and `selfhostedpublic<seed>` (anonymous pull) | Artifact Registry `spacelift-<seed>` and, for external workers, `spacelift-public-<seed>` readable by `allUsers`; keep 10 versions |
| Ingress and TLS | ALB with an ACM certificate | `IngressClass` `spacelift` driving an ALB with ACM | ingress-nginx plus cert-manager with Let's Encrypt | GCE ingress on static addresses plus cert-manager |
| Workload identity | Task roles per service, task execution role reading Secrets Manager | IRSA roles for server, drain and VCS Gateway service accounts | Node managed identity granted `Storage Blob Data Contributor` | Backend service account (`roles/iam.serviceAccountTokenCreator` for URL signing, object admin on buckets), Workload Identity annotation |
| Secrets | Secrets Manager secret with `LICENSE_TOKEN` and `sensitive_env_vars` | Kubernetes secrets `spacelift-shared`, `spacelift-server`, `spacelift-drain` from an output | Same three secrets | Same three secrets |
| DNS | Customer-managed | Customer-managed | Customer-managed | Optional Cloud DNS zone with A and MQTT CNAME records |

Sources: [S] ECS [main.tf][ecs-root-main], [modules/ecs][ecs-ecs],
[variables.tf][ecs-vars]; [S] AWS [main.tf][aws-main], [rds][aws-rds],
[ecr L1, L39][aws-ecr], [kms][aws-kms]; [S] EKS [main.tf L38-L39][eks-main],
[eks.tf L19-L24][eks-eks], [variables.tf L160-L165, L342][eks-vars];
[S] Azure [main.tf][az-main], [postgres][az-pg],
[container_registry][az-acr], [container_storage][az-cs];
[S] GCP [db][gcp-db], [gke][gcp-gke], [artifacts][gcp-art],
[iam][gcp-iam], [dns][gcp-dns]; [D] the [ECS][sh-ecs], [EKS][sh-eks],
[AKS][sh-aks] and [GKE][sh-gke] guides; [RN] v6.4.0 for the Fargate
architecture. A fifth flavour, an existing on-prem Kubernetes cluster, uses
the chart with an S3-compatible store and RSA encryption; its guide uses
MinIO in examples and warns that upstream development of the open-source MinIO
server has stopped [D] [on-prem guide][sh-onprem].

### 6.1 AWS IAM by service

`terraform-aws-iam-spacelift-selfhosted` (`73db85af505f`) renders the IAM
policies the ECS and EKS modules attach, or writes them to files [S]
[README][iam-readme].

| Role | Actions (abridged) | Source |
|---|---|---|
| Server | S3 read, write, list and version reads on the buckets; SQS send; IoT certificate and policy creation | [S] [iam_server.tf][iam-server] |
| Drain | S3 read, delete, list, tag; SQS receive, delete, change visibility, send, get attributes | [S] [iam_drain.tf][iam-drain] |
| Server and drain (shared) | KMS encrypt, decrypt, data keys, sign, verify, public key; `sts:AssumeRole` and `sts:TagSession` for cloud integrations; IoT publish and certificate management; License Manager checkout; CloudWatch metrics and logs; X-Ray; `rds-db:connect` | [S] [iam_drain_and_server.tf][iam-ds] |
| Scheduler (legacy) | KMS, SQS send, STS, logs, metrics, X-Ray | [S] [iam_scheduler.tf][iam-sched] |
| VCS Gateway | Logs, metrics, X-Ray, `rds-db:connect` | [S] [iam_vcs_gateway.tf][iam-vcs] |
| ECS task execution | Secrets Manager read, KMS decrypt, CloudWatch logs | [S] [iam_execution.tf][iam-exec] |

## 7. Helm chart values

The chart's 135 leaf values, generated from `values.yaml` and typed from
`values.schema.json`, are in section 14.1. The values that shape the
topology:

| Value | Default | Effect | Source |
|---|---|---|---|
| `shared.secretRef`, `<component>.secretRef` | none | `envFrom` secrets: the modules render `spacelift-shared`, `spacelift-server`, `spacelift-drain` | [S] [server-deployment.yaml L136-L146][hc-server] |
| `shared.serverHostname` | none | Ingress host and TLS name | [S] [server-ingress.yaml][hc-ing4] |
| `shared.backendImage.*`, `shared.image` | `oci.spacelift.io/deploy/backend:<appVersion>` | Backend image; a digest drops the tag; `fips` appends `-fips` | [S] [_helpers.tpl L151-L176][hc-helpers] |
| `scheduler.enabled` | `null` | Standalone scheduler decision (section 2.2) | [S] [values.yaml L98-L110][hc-values] |
| `vcsGateway.enabled`, `.domain`, `.externalPort`, `.internalPort` | `false`, `""`, `1984`, `1985` | VCS Gateway workload and its exposure | [S] [values.yaml L143-L195][hc-values] |
| `cloudSqlProxy.enabled`, `.dbConnectionName` | `false` | Proxy sidecar on every backend pod | [S] [values.yaml L197-L205][hc-values] |
| `mqttService.enabled`, `.type`, `.port` | `true`, `ClusterIP`, `1984` | Broker exposure | [S] [values.yaml L218-L222][hc-values] |
| `ingress.*`, `ingressV6.*` | both enabled | IPv4 and IPv6 ingress objects | [S] [values.yaml L224-L256][hc-values] |

The templates also read keys that `values.schema.json` declares but
`values.yaml` leaves out, so section 14.1 does not list them [S]
[templates][hc-templates]:

- `<component>.podAnnotations`, `<component>.lifecycle` (`preStop`,
  `postStart`) and `<component>.terminationGracePeriodSeconds` for server,
  drain, scheduler and VCS Gateway [S] [server-deployment.yaml L27, L44, L161][hc-server];
- `<component>.pdb.minAvailable` and `.maxUnavailable`, written into the
  `PodDisruptionBudget` only when the key is present [S]
  [templates][hc-templates] (`*-pdb.yaml`);
- `vcsGateway.service.type` (falls back to `service.type`) and
  `vcsGateway.service.appProtocol`, which the GKE module sets to
  `kubernetes.io/h2c` [S] [vcs-gateway-service.yaml L13-L20][hc-vcssvc];
  [S] [GKE helm-values.tftpl L54][gke-helm];
- `mqttService.additionalSpec`, merged into the MQTT `Service` spec, which the
  GKE module uses [S] [mqtt-service.yaml L14][hc-mqttsvc];
  [S] [GKE helm-values.tftpl L36][gke-helm].

Every Kubernetes module passes the image as `shared.image`
(`<backend image>:<version>`), the key the `spacelift-helm-charts` copy
deprecates [S] [EKS helm-values.tftpl L3][eks-helm];
[S] [AKS helm-values.tftpl L3][aks-helm]; [S] [GKE helm-values.tftpl L3][gke-helm].

## 8. First setup

1. Enable admin login with `ADMIN_USERNAME` and `ADMIN_PASSWORD`; browse to
   `SERVER_DOMAIN`, which redirects to the admin login page [D]
   [first setup][sh-first].
2. Choose the account name: 2 to 38 characters, starting with a letter or
   digit, containing only letters, digits, `-` and `_`. It appears in the UI,
   in default AWS external IDs and session names, and in private registry
   URLs, so changing it later breaks AWS trust policies, webhooks and
   registry references [D] [first setup][sh-first].
3. Continue from the launchpad checklist (worker pool, SSO, Slack, invites),
   then remove the admin variables to disable admin login; `/admin-login`
   keeps working while they are set [D] [first setup][sh-first].

## 9. Upgrades, the v2 to v3 kit and what changed

### 9.1 What v3 changed

| v2 (CloudFormation) | v3 and later (reference architecture) | Source |
|---|---|---|
| AWS only | AWS, Azure, GCP, on-prem | [RN] v3.0.0 |
| CloudFormation stacks (`spacelift-infra-vpc`, `-vpc-config`, `-kms`, `-db`, `-db-secrets`, `-s3`, `spacelift-infra`, `spacelift-services`, `spacelift-monitoring`, `spacelift-bootstrap`) | Terraform/OpenTofu modules plus a Helm chart, or the customer's own tooling | [S] kit [scanners][kit-scanners] and [delete_cf_stacks.py][kit-delete]; [RN] v3.0.0 |
| Server, drain and scheduler on ECS Fargate | Server and drain (scheduler folded into the drain in v6.4.0) on any container platform | [D] [CloudFormation install][sh-cf]; [RN] v6.4.1 |
| IoT Core required | Built-in broker or IoT Core, and long-poll from v6.4.0 | [RN] v3.0.0, v6.4.0 |
| SQS required | Built-in Postgres queue or SQS | [RN] v3.0.0 |
| AWS License Manager | JWT licence token | [RN] v3.0.0 |
| No first-setup wizard | Admin login and account creation wizard | [RN] v3.0.0 |
| `downloads` bucket | `binaries` bucket | [S] [s3_to_terraform.py L180][kit-s3] |

### 9.2 The migration kit

`self-hosted-v2-to-v3-kit` (`bc80da197853`, MIT) is a Python tool that reads
a v2 installation's CloudFormation stacks and AWS resources and generates a
Terraform project that imports them [S] [README][kit-readme].

| Step | What happens | Source |
|---|---|---|
| Version check | Reads SSM parameter `/spacelift/install-version`; kit 3.0.0 requires at least v6.4.1 | [S] [main.py L66-L80][kit-main] |
| Scan | Scanners for EC2/VPC, KMS, S3, ECR, RDS, Secrets Manager, IoT and SQS read stack resources by logical ID (for example `DeliveriesBucket`, `DownloadsBucket`, `RunObservabilityBucket` in `spacelift-infra-s3`) | [S] [main.py L1-L60][kit-main]; [S] [s3_scanner.py L18-L31][kit-s3scan] |
| Generate | Writes `imports.tf` and module configuration targeting the AWS module plus the ECS or EKS module | [S] [main.py][kit-main] |
| Refactor | `internet_gateway_refactor.py` reconciles a structural difference in how the two tools model the internet gateway | [S] [README][kit-readme] |
| Apply | Creates a new load balancer and ECS or EKS environment on the same data stores; the message queue stays SQS and the broker stays IoT Core | [D] [migration guide][sh-cfmig] |
| Cut over | Point DNS at the new load balancer; scale down the old services; test for days | [D] [migration guide][sh-cfmig] |
| Tear down | `delete_cf_stacks.py` deletes the old stacks while retaining resources, by first failing a delete with a weak role (to reach `DELETE_FAILED`, the only state that allows `RetainResources`) and then deleting with an admin role | [D] [migration guide][sh-cfmig]; [S] [delete_cf_stacks.py L1-L20, L120-L210][kit-delete] |

Compatibility matrix at the baseline [S] [README][kit-readme]; [D]
[migration guide][sh-cfmig]:

| Target | Spacelift version | Kit | AWS module | ECS or EKS module |
|---|---|---|---|---|
| ECS | >= 2.6.0, < 4.0.0 | 1.0.1 | 1.3.1 | ECS 1.1.0 |
| ECS | >= 2.6.0 | 1.1.0, 2.0.0 | 2.1.1 | ECS 2.1.0 |
| ECS | >= 6.0.0 | 2.1.0 | 2.2.0 | ECS 2.5.0 |
| ECS | >= 6.4.1 | 3.0.0 | 3.3.0 | ECS 3.1.0 |
| EKS | >= 4.0.0 | 2.0.0 | via EKS | EKS 3.4.0 |
| EKS | >= 6.0.0 | 2.1.0 | via EKS | EKS 3.9.0 |
| EKS | >= 6.4.1 | 3.0.0 | via EKS | EKS 4.3.0 |

The migration needs no downtime because the new environment runs alongside
the old one against the same database and buckets; RDS deletion protection
and non-empty buckets protect the data [D] [migration guide][sh-cfmig].

### 9.3 Module majors tied to application versions

| Change | Minimum versions | Source |
|---|---|---|
| Scheduler resources removed | Helm chart `v0.78.0`; AWS `v3.0.0`, ECS `v3.0.0`, EKS `v4.0.0`, Azure and GCP majors; upgrade the application to v6.4.0 first, or `helm upgrade --wait` fails on the missing service account | [RN] v6.4.1 |
| Run observability bucket | AWS `v2.6.0`, ECS `v3.0.0`, EKS `v4.0.0`, Azure `v3.0.0` and GCP; infrastructure before backend | [RN] v6.4.1 |
| Launcher image on the server | ECS `v3.0.1`, EKS `v4.0.1`, Azure `v3.0.1`, GCP | [RN] v6.4.1 |
| RDS IAM authentication | ECS `v3.1.0`, EKS `v4.2.0` | [RN] v7.0.0 |
| ECS `v2.0.0`, EKS `v3.0.0` | Database credentials into `sensitive_env_vars`; licence as a write-only argument (needs OpenTofu/Terraform 1.11+); tracing sidecars; EKS and RDS versions required; EKS module v21 | [RN] v4.0.0 |
| VCS Gateway on EKS, GKE, AKS | Helm chart `v0.56.0` | [RN] v4.1.0 |

## 10. Usage reporting and the usage data exporter

| Method | How | Source |
|---|---|---|
| Automatic | Set `SPACELIFT_PUBLIC_API=https://app.spacelift.io` on the drain and allow it egress | [D] [usage reporting][sh-usage] |
| Manual | Organization settings, **Usage Export**, pick a date range, download, send to Spacelift | [D] [usage reporting][sh-usage] |
| Script | `self-hosted-usage-data-exporter` | [D] [usage reporting][sh-usage] |

The exporter (`3ed9f8feef07`, MIT) shows the self-hosted usage endpoint's shape
[S] [export.py][exp]:

| Step | Request | Source |
|---|---|---|
| Authenticate | `POST <base>/graphql` with mutation `apiKeyUser(id, secret) { jwt }`; the key needs admin rights | [S] [export.py L16-L27, L137-L138][exp] |
| Export | `GET <base>/selfhosted/metrics?start_timestamp=<unix>&end_timestamp=<unix>[&tables=<csv>]` with `Authorization: Bearer <jwt>`; the response JSON carries `account { ulid, license_id }` and `time_range { start, end }` | [S] [export.py L34-L49, L77-L88][exp] |
| Upload | `GET https://app.spacelift.io/selfhosted/metrics/upload_url?object_key=<ulid>/<license_id>/usage_data_<start>_<end>.json` returns `{ URL, Headers }`; then `PUT` the JSON to that pre-signed URL | [S] [export.py L29-L32, L55-L60][exp] |
| Batching | Date range split into batches of `--batch-size` days (default 7); end date exclusive; `--skip-tls-verification`; `--send-to-spacelift` uploads instead of saving locally | [S] [export.py L97-L149][exp] |
| Tables | `--tables`, for example `logins,heartbeats,daily_worker_usages`; the server rejects unknown names and lists the valid ones | [S] [README][exp-readme] |

The self-hosted `/selfhosted/metrics` route is a REST endpoint outside the
GraphQL schema [I] from the exporter calling it directly with a bearer JWT.
Since v7.0.0 a **Usage** page under Organization settings charts private
worker usage against licence limits, and notification policies receive a
daily `worker_usage` object [RN] v7.0.0.

## 11. Disaster recovery

| Element | Recovery concern | Source |
|---|---|---|
| Server, drain | Stateless; redeploy and point at replacement dependencies | [D] [disaster recovery][sh-dr] |
| PostgreSQL | Back up; restore, fail over, or use a global database (the AWS module supports Aurora global clusters) | [D] [disaster recovery][sh-dr]; [S] [AWS variables.tf L119-L130][aws-vars] |
| Buckets | Replicate modules, policy inputs, run logs and states; consider replicating back to the primary | [D] [disaster recovery][sh-dr] |
| Encryption key | KMS must be multi-region and replicated; RSA key must be kept | [D] [disaster recovery][sh-dr] |
| Queues | Loss tolerable; the Postgres queue is covered by the database | [D] [disaster recovery][sh-dr] |
| Workers | Built-in broker: repoint DNS; IoT Core: needs a custom domain configuration, `MQTT_BROKER_ENDPOINT` on that domain, and `AWS_SECONDARY_REGION` | [D] [disaster recovery][sh-dr] |

## 12. Self-hosted release notes affecting the architecture

| Version | Change | Source |
|---|---|---|
| v3.0.0 | Reference architecture; cloud-agnostic; built-in broker and queue; JWT licence; first-setup wizard; Datadog and OpenTelemetry | [RN] |
| v3.4.0 | VCS agent pools for self-hosted, with the VCS Gateway on ECS | [RN] |
| v4.0.0 | ECS module v2.0.0 and EKS module v3.0.0 (see 9.3) | [RN] |
| v4.1.0 | VCS Gateway in the EKS, GKE and AKS modules | [RN] |
| v5.0.0 | FIPS launcher and backend images using AWS FIPS endpoints | [RN] |
| v6.0.0 | Intent and Infra Assistant reach self-hosted (report 22) | [RN] |
| v6.4.0 | HTTP long-poll transport; arm64 release archive; drain runs cron | [RN] |
| v6.4.1 | Standalone scheduler removed from chart and modules; run observability bucket; launcher image on the server; OAuth redirect allowlist for the MCP server | [RN] |
| v6.4.2 | Images built with the latest Go 1.26 | [RN] |
| v7.0.0 | Usage page; RDS IAM authentication; GraphQL request logging; stale run cleanup; run summarization; CloudFormation and standalone-scheduler deprecations | [RN] |
| v7.1.0 | Go 1.27.3 everywhere; MQTT and IoT Core deprecated; `minio` renamed `s3compatible` | [RN] |
| v8.0.0 (announced) | Drops CloudFormation and the `OBJECT_STORAGE_MINIO_*` names; container registry distribution | [RN] v7.0.0, v7.1.0 |

## 13. GraphQL surface specific to self-hosted

| Field | Type | Meaning | Source |
|---|---|---|---|
| `Query.debugInfo` | `DebugInfo!` | Build and licence details | [G] |
| `DebugInfo.commitSha` | `String!` | Git commit of the running binary; empty on SaaS | [G] |
| `DebugInfo.selfHostedVersion` | `String!` | Self-hosted version; empty on SaaS | [G] |
| `DebugInfo.selfHostedLicence` | `SelfHostedLicence` | Licence; null means no licence | [G] |
| `SelfHostedLicence.validity` | `LicenseDateRange!` | Validity window | [G] |
| `LicenseDateRange.begin`, `.end` | `Int!`, `Int` | Unix start and optional end | [G] |
| `Query.usage`, `Query.workerUsage`, `Query.runMinutesUsage` | `Usage!`, `WorkerUsage!`, `RunMinutesUsage!` | Usage data behind the usage pages (report 19 and report 26) | [G] |

## 14. Configuration value inventory

Generated by script. Descriptions in the module tables are abridged
from the MIT-licensed module sources.

### 14.1 Helm chart values

Source: [values.yaml][hc-values] and `values.schema.json` [S]. Keys are dotted paths; `null` means the key is present without a value.

| Key | Line | Schema type | Default |
|---|---|---|---|
| `shared.secretRef` | [L2](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L2) | string/null | `null` |
| `shared.image` | [L6](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L6) | string/null | `null` |
| `shared.backendImage.registry` | [L9](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L9) | string | `oci.spacelift.io` |
| `shared.backendImage.repository` | [L10](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L10) | string | `deploy/backend` |
| `shared.backendImage.tag` | [L12](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L12) | string | `""` |
| `shared.backendImage.digest` | [L16](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L16) | string | `""` |
| `shared.backendImage.fips` | [L18](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L18) | boolean | `false` |
| `shared.imagePullSecrets` | [L25](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L25) | array | `[]` |
| `shared.serverHostname` | [L26](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L26) | string/null | `null` |
| `server.replicaCount` | [L29](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L29) | integer | `3` |
| `server.port` | [L30](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L30) | integer | `1983` |
| `server.secretRef` | [L31](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L31) | string/null | `null` |
| `server.mqttPort` | [L32](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L32) | integer | `1984` |
| `server.pullPolicy` | [L33](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L33) | string | `Always` |
| `server.securityContext.allowPrivilegeEscalation` | [L37](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L37) | boolean | `false` |
| `server.resources.requests.memory` | [L40](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L40) | string | `"200Mi"` |
| `server.resources.requests.cpu` | [L41](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L41) | string | `"200m"` |
| `server.readinessProbe.httpGet.path` | [L44](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L44) | string | `/readiness` |
| `server.readinessProbe.httpGet.port` | [L45](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L45) | string/integer | `instrumentation` |
| `server.podLabels` | [L46](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L46) | object | `{}` |
| `server.podSecurityContext` | [L47](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L47) | object | `{}` |
| `server.volumes` | [L48](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L48) | array | `[]` |
| `server.volumeMounts` | [L49](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L49) | array | `[]` |
| `server.nodeSelector` | [L50](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L50) | object | `{}` |
| `server.tolerations` | [L51](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L51) | array | `[]` |
| `server.affinity` | [L52](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L52) | object | `{}` |
| `server.serviceAccount.create` | [L56](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L56) | boolean | `false` |
| `server.serviceAccount.name` | [L57](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L57) | string | `"spacelift-server"` |
| `server.serviceAccount.automount` | [L58](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L58) | boolean | `true` |
| `server.serviceAccount.annotations` | [L59](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L59) | object/null | `{}` |
| `server.lifecycle` | [L60](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L60) | object | `{}` |
| `server.pdb.enabled` | [L62](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L62) | boolean | `false` |
| `drain.replicaCount` | [L65](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L65) | integer/null | `3` |
| `drain.secretRef` | [L66](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L66) | string/null | `null` |
| `drain.pullPolicy` | [L67](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L67) | string | `Always` |
| `drain.securityContext.allowPrivilegeEscalation` | [L71](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L71) | boolean | `false` |
| `drain.resources.requests.memory` | [L74](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L74) | string | `"100Mi"` |
| `drain.resources.requests.cpu` | [L75](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L75) | string | `"100m"` |
| `drain.startupProbe.httpGet.path` | [L78](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L78) | string | `/startup` |
| `drain.startupProbe.httpGet.port` | [L79](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L79) | string/integer | `instrumentation` |
| `drain.podLabels` | [L80](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L80) | object | `{}` |
| `drain.podSecurityContext` | [L81](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L81) | object | `{}` |
| `drain.volumes` | [L82](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L82) | array | `[]` |
| `drain.volumeMounts` | [L83](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L83) | array | `[]` |
| `drain.nodeSelector` | [L84](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L84) | object | `{}` |
| `drain.tolerations` | [L85](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L85) | array | `[]` |
| `drain.affinity` | [L86](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L86) | object | `{}` |
| `drain.serviceAccount.create` | [L90](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L90) | boolean | `false` |
| `drain.serviceAccount.name` | [L91](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L91) | string | `"spacelift-drain"` |
| `drain.serviceAccount.automount` | [L92](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L92) | boolean | `true` |
| `drain.serviceAccount.annotations` | [L93](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L93) | object/null | `{}` |
| `drain.lifecycle` | [L94](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L94) | object | `{}` |
| `drain.pdb.enabled` | [L96](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L96) | boolean | `false` |
| `scheduler.enabled` | [L110](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L110) | boolean/null | `null` |
| `scheduler.replicaCount` | [L111](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L111) | integer | `3` |
| `scheduler.pullPolicy` | [L112](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L112) | string | `Always` |
| `scheduler.securityContext.allowPrivilegeEscalation` | [L116](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L116) | boolean | `false` |
| `scheduler.resources.requests.memory` | [L119](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L119) | string | `"50Mi"` |
| `scheduler.resources.requests.cpu` | [L120](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L120) | string | `"50m"` |
| `scheduler.startupProbe.httpGet.path` | [L123](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L123) | string | `/startup` |
| `scheduler.startupProbe.httpGet.port` | [L124](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L124) | string/integer | `instrumentation` |
| `scheduler.podLabels` | [L125](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L125) | object | `{}` |
| `scheduler.podSecurityContext` | [L126](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L126) | object | `{}` |
| `scheduler.volumes` | [L127](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L127) | array | `[]` |
| `scheduler.volumeMounts` | [L128](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L128) | array | `[]` |
| `scheduler.nodeSelector` | [L129](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L129) | object | `{}` |
| `scheduler.tolerations` | [L130](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L130) | array | `[]` |
| `scheduler.affinity` | [L131](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L131) | object | `{}` |
| `scheduler.serviceAccount.create` | [L135](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L135) | boolean | `false` |
| `scheduler.serviceAccount.name` | [L136](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L136) | string | `"spacelift-scheduler"` |
| `scheduler.serviceAccount.automount` | [L137](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L137) | boolean | `true` |
| `scheduler.serviceAccount.annotations` | [L138](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L138) | object/null | `{}` |
| `scheduler.lifecycle` | [L139](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L139) | object | `{}` |
| `scheduler.pdb.enabled` | [L141](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L141) | boolean | `false` |
| `vcsGateway.enabled` | [L144](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L144) | boolean | `false` |
| `vcsGateway.domain` | [L147](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L147) | string | `""` |
| `vcsGateway.replicaCount` | [L148](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L148) | integer | `3` |
| `vcsGateway.externalPort` | [L150](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L150) | integer | `1984` |
| `vcsGateway.internalPort` | [L151](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L151) | integer | `1985` |
| `vcsGateway.secretRef` | [L152](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L152) | string | `""` |
| `vcsGateway.pullPolicy` | [L153](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L153) | string | `Always` |
| `vcsGateway.securityContext.allowPrivilegeEscalation` | [L155](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L155) | boolean | `false` |
| `vcsGateway.resources.requests.memory` | [L158](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L158) | string | `"100Mi"` |
| `vcsGateway.resources.requests.cpu` | [L159](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L159) | string | `"100m"` |
| `vcsGateway.resources.limits` | [L160](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L160) | object | `{}` |
| `vcsGateway.podLabels` | [L161](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L161) | object | `{}` |
| `vcsGateway.podAnnotations` | [L162](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L162) | object/null | `{}` |
| `vcsGateway.podSecurityContext` | [L163](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L163) | object | `{}` |
| `vcsGateway.volumes` | [L164](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L164) | array | `[]` |
| `vcsGateway.volumeMounts` | [L165](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L165) | array | `[]` |
| `vcsGateway.nodeSelector` | [L166](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L166) | object | `{}` |
| `vcsGateway.tolerations` | [L167](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L167) | array | `[]` |
| `vcsGateway.affinity` | [L168](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L168) | object | `{}` |
| `vcsGateway.serviceAccount.create` | [L172](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L172) | boolean | `false` |
| `vcsGateway.serviceAccount.name` | [L173](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L173) | string | `"spacelift-vcs-gateway"` |
| `vcsGateway.serviceAccount.automount` | [L174](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L174) | boolean | `true` |
| `vcsGateway.serviceAccount.annotations` | [L175](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L175) | object/null | `{}` |
| `vcsGateway.lifecycle` | [L176](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L176) | object | `{}` |
| `vcsGateway.service.annotations` | [L178](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L178) | object/null | `{}` |
| `vcsGateway.ingress.enabled` | [L180](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L180) | boolean | `true` |
| `vcsGateway.ingress.ingressClassName` | [L181](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L181) | string | `""` |
| `vcsGateway.ingress.annotations` | [L182](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L182) | object/null | `null` |
| `vcsGateway.gateway.enabled` | [L190](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L190) | boolean | `false` |
| `vcsGateway.gateway.gatewayClassName` | [L191](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L191) | string | `""` |
| `vcsGateway.gateway.certificateRef` | [L192](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L192) | string | `"vcs-gateway-cert"` |
| `vcsGateway.gateway.annotations` | [L193](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L193) | object/null | `{}` |
| `vcsGateway.pdb.enabled` | [L195](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L195) | boolean | `false` |
| `cloudSqlProxy.enabled` | [L198](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L198) | boolean | `false` |
| `cloudSqlProxy.quiet` | [L199](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L199) | boolean | `true` |
| `cloudSqlProxy.image` | [L200](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L200) | string | `gcr.io/cloud-sql-connectors/cloud-sql...` |
| `cloudSqlProxy.dbConnectionName` | [L201](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L201) | string/null | `null` |
| `cloudSqlProxy.resources.requests.memory` | [L204](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L204) | string | `"200Mi"` |
| `cloudSqlProxy.resources.requests.cpu` | [L205](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L205) | string | `"100m"` |
| `serviceAccount.create` | [L209](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L209) | boolean | `true` |
| `serviceAccount.name` | [L210](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L210) | string | `"spacelift-backend"` |
| `serviceAccount.automount` | [L211](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L211) | boolean | `true` |
| `serviceAccount.annotations` | [L212](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L212) | object/null | `{}` |
| `service.type` | [L215](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L215) | string | `ClusterIP` |
| `service.port` | [L216](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L216) | integer | `80` |
| `mqttService.enabled` | [L219](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L219) | boolean | `true` |
| `mqttService.type` | [L220](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L220) | string | `ClusterIP` |
| `mqttService.port` | [L221](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L221) | integer | `1984` |
| `mqttService.annotations` | [L222](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L222) | object/null | `{}` |
| `ingress.enabled` | [L225](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L225) | boolean | `true` |
| `ingress.ingressClassName` | [L226](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L226) | string | `""` |
| `ingress.annotations."nginx.ingress.kubernetes.io/proxy-body-size"` | [L229](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L229) | string | `"0"` |
| `ingress.extraPaths` | [L230](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L230) | array | `[]` |
| `ingress.extraRules` | [L238](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L238) | array | `[]` |
| `ingressV6.enabled` | [L251](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L251) | boolean | `true` |
| `ingressV6.annotations."nginx.ingress.kubernetes.io/proxy-body-size"` | [L254](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L254) | string | `"0"` |
| `ingressV6.extraPaths` | [L255](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L255) | array | `[]` |
| `ingressV6.extraRules` | [L256](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L256) | array | `[]` |
| `extraManifests` | [L260](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L260) | array | `[]` |
| `nameOverride` | [L262](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L262) | string | `""` |
| `fullnameOverride` | [L263](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml#L263) | string | `"spacelift"` |

### 14.2 Terraform module root variables

Each variable's description is at its linked line. The tables do not
reproduce the descriptions, because the module repositories have no
attribution entry in a `NOTICE` file.

#### `terraform-aws-spacelift-selfhosted` (54 root variables)

Source: [variables.tf](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf) [S].

| Variable | Line | Type | Default |
|---|---|---|---|
| `availability_zones` | [L371](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L371) | `list(string)` | `null` |
| `backend_ecr_repository_name` | [L347](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L347) | `string` | `null` |
| `create_database` | [L177](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L177) | `bool` | `true` |
| `create_sqs` | [L12](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L12) | `bool` | `false` |
| `create_vcs_gateway` | [L53](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L53) | `bool` | `false` |
| `create_vpc` | [L59](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L59) | `bool` | `true` |
| `ecr_force_delete` | [L365](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L365) | `bool` | `false` |
| `enable_dns_hostnames` | [L83](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L83) | `bool` | `false` |
| `enable_public_access_block_on_s3` | [L341](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L341) | `bool` | `true` |
| `kms_arn` | [L29](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L29) | `string` | `null` |
| `kms_encryption_key_multi_regional` | [L41](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L41) | `bool` | `true` |
| `kms_jwt_key_multi_regional` | [L47](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L47) | `bool` | `true` |
| `kms_master_key_multi_regional` | [L35](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L35) | `bool` | `true` |
| `launcher_ecr_repository_name` | [L353](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L353) | `string` | `null` |
| `mqtt_broker_type` | [L18](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L18) | `string` | `""` |
| `number_of_images_to_retain` | [L359](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L359) | `number` | `5` |
| `private_subnet_cidr_blocks` | [L77](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L77) | `list(string)` | `[]` |
| `private_subnet_tags` | [L95](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L95) | `map(string)` | `{}` |
| `public_subnet_cidr_blocks` | [L71](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L71) | `list(string)` | `[]` |
| `public_subnet_tags` | [L89](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L89) | `map(string)` | `{}` |
| `rds_allow_auto_minor_version_upgrade` | [L194](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L194) | `bool` | `false` |
| `rds_apply_immediately` | [L280](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L280) | `bool` | `true` |
| `rds_backup_retention_period` | [L243](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L243) | `number` | `5` |
| `rds_delete_protection_enabled` | [L183](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L183) | `bool` | `true` |
| `rds_enable_http_endpoint` | [L286](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L286) | `bool` | `null` |
| `rds_enabled_cloudwatch_logs_exports` | [L274](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L274) | `set(string)` | `null` |
| `rds_engine_mode` | [L200](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L200) | `string` | `"provisioned"` |
| `rds_engine_version` | [L189](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L189) | `string` | required |
| `rds_global_cluster_identifier` | [L119](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L119) | `string` | `null` |
| `rds_iam_username` | [L217](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L217) | `string` | `null` |
| `rds_instance_configuration` | [L223](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L223) | `map(object({` | `{ "primary-instance" = { instance_ide...` |
| `rds_is_global_secondary` | [L125](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L125) | `bool` | `false` |
| `rds_monitoring` | [L263](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L263) | `object({` | `{}` |
| `rds_parameter_group_description` | [L149](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L149) | `string` | `"Spacelift core product database para...` |
| `rds_parameter_group_name` | [L143](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L143) | `string` | `null` |
| `rds_password_sm_arn` | [L155](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L155) | `string` | `null` |
| `rds_performance_insights` | [L249](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L249) | `object({` | `{ enabled = false kms_key_arn = null ...` |
| `rds_preferred_backup_window` | [L237](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L237) | `string` | `"07:00-09:00"` |
| `rds_regional_cluster_identifier` | [L113](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L113) | `string` | `null` |
| `rds_replication_source_identifier` | [L131](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L131) | `string` | `null` |
| `rds_security_group_ids` | [L107](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L107) | `list(string)` | `[]` |
| `rds_serverlessv2_scaling_configuration` | [L167](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L167) | `object({` | `null` |
| `rds_snapshot_identifier` | [L161](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L161) | `string` | `null` |
| `rds_subnet_group_name` | [L137](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L137) | `string` | `null` |
| `rds_subnet_ids` | [L101](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L101) | `list(string)` | `[]` |
| `rds_username` | [L211](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L211) | `string` | `"spacelift"` |
| `region` | [L1](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L1) | `string` | required |
| `s3_bucket_configuration` | [L314](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L314) | `object({` | `null` |
| `s3_noncurrent_version_expiration_days` | [L335](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L335) | `number` | `7` |
| `s3_retain_on_destroy` | [L297](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L297) | `bool` | `true` |
| `security_group_names` | [L303](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L303) | `object({` | `null` |
| `unique_suffix` | [L6](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L6) | `string` | `""` |
| `vpc_cidr_block` | [L65](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L65) | `string` | `"10.0.0.0/18"` |
| `website_endpoint` | [L292](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L292) | `string` | required |

#### `terraform-aws-ecs-spacelift-selfhosted` (91 root variables)

Source: [variables.tf](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf) [S].

| Variable | Line | Type | Default |
|---|---|---|---|
| `additional_env_vars` | [L480](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L480) | `list(object({` | `[]` |
| `admin_password` (sensitive) | [L91](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L91) | `string` | `""` |
| `admin_username` (sensitive) | [L84](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L84) | `string` | `""` |
| `backend_image` | [L98](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L98) | `string` | required |
| `backend_image_tag` | [L103](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L103) | `string` | required |
| `byo_server_target_group_arns` | [L351](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L351) | `list(string)` | `[]` |
| `datadog_agent_config` | [L546](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L546) | `object({` | `null` |
| `datadog_api_key` (sensitive) | [L539](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L539) | `string` | `null` |
| `deliveries_bucket_name` | [L126](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L126) | `string` | required |
| `drain_concurrency_async_jobs` | [L267](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L267) | `number` | `1` |
| `drain_concurrency_async_jobs_fifo` | [L273](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L273) | `number` | `1` |
| `drain_concurrency_cronjobs` | [L279](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L279) | `number` | `1` |
| `drain_concurrency_dlq` | [L285](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L285) | `number` | `1` |
| `drain_concurrency_dlq_fifo` | [L291](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L291) | `number` | `1` |
| `drain_concurrency_events` | [L297](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L297) | `number` | `1` |
| `drain_concurrency_iot` | [L303](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L303) | `number` | `1` |
| `drain_concurrency_webhooks` | [L309](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L309) | `number` | `1` |
| `drain_container_definitions` | [L226](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L226) | `string` | `null` |
| `drain_cpu` | [L232](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L232) | `number` | `2048` |
| `drain_desired_count` | [L244](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L244) | `number` | `3` |
| `drain_log_configuration` | [L250](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L250) | `any` | `null` |
| `drain_memory` | [L238](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L238) | `number` | `4096` |
| `drain_role_arn` | [L256](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L256) | `string` | `null` |
| `drain_security_group_id` | [L262](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L262) | `string` | required |
| `drain_service_name` | [L453](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L453) | `string` | `null` |
| `ecs_service_az_rebalancing_enabled` | [L441](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L441) | `bool` | `true` |
| `ecs_subnets` | [L79](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L79) | `list(string)` | required |
| `enable_automatic_usage_data_reporting` | [L489](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L489) | `bool` | `false` |
| `enable_datadog_agent_sidecar` | [L533](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L533) | `bool` | `false` |
| `enable_otel_sidecar` | [L561](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L561) | `bool` | `false` |
| `encryption_type` | [L186](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L186) | `string` | `"kms"` |
| `execution_role_arn` | [L220](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L220) | `string` | `null` |
| `iot_endpoint` | [L510](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L510) | `string` | `null` |
| `kms_encryption_key_arn` | [L203](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L203) | `string` | `""` |
| `kms_key_arn` | [L214](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L214) | `string` | `null` |
| `kms_signing_key_arn` | [L209](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L209) | `string` | required |
| `large_queue_messages_bucket_name` | [L131](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L131) | `string` | required |
| `launcher_image` | [L108](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L108) | `string` | required |
| `launcher_image_tag` | [L113](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L113) | `string` | required |
| `license_token` (sensitive) | [L118](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L118) | `string` | `null` |
| `load_balancer_security_group_id` | [L527](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L527) | `string` | `null` |
| `metadata_bucket_name` | [L136](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L136) | `string` | required |
| `modules_bucket_name` | [L141](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L141) | `string` | required |
| `mqtt_broker_endpoint` | [L12](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L12) | `string` | `null` |
| `mqtt_broker_type` | [L516](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L516) | `string` | `"builtin"` |
| `mqtt_lb_internal` | [L67](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L67) | `bool` | `false` |
| `mqtt_lb_subnets` | [L73](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L73) | `list(string)` | `[]` |
| `observability_vendor` | [L429](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L429) | `string` | `"Disabled"` |
| `otel_config` | [L567](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L567) | `object({` | `null` |
| `policy_inputs_bucket_name` | [L146](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L146) | `string` | required |
| `rds_iam_auth` | [L583](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L583) | `object({` | `null` |
| `region` | [L1](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L1) | `string` | required |
| `rsa_private_key` | [L197](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L197) | `string` | `""` |
| `run_logs_bucket_name` | [L151](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L151) | `string` | required |
| `run_observability_bucket_name` | [L156](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L156) | `string` | required |
| `secrets_manager_secret_arns` | [L474](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L474) | `list(string)` | `[]` |
| `sensitive_env_vars` | [L465](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L465) | `list(object({` | `[]` |
| `server_container_definition` | [L315](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L315) | `string` | `null` |
| `server_cpu` | [L321](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L321) | `number` | `1024` |
| `server_desired_count` | [L333](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L333) | `number` | `3` |
| `server_domain` | [L24](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L24) | `string` | required |
| `server_lb_certificate_arn` | [L56](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L56) | `string` | `null` |
| `server_lb_internal` | [L44](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L44) | `bool` | `false` |
| `server_lb_name` | [L38](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L38) | `string` | `null` |
| `server_lb_subnets` | [L50](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L50) | `list(string)` | `null` |
| `server_log_configuration` | [L339](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L339) | `any` | `null` |
| `server_memory` | [L327](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L327) | `number` | `2048` |
| `server_role_arn` | [L345](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L345) | `string` | `null` |
| `server_security_group_id` | [L62](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L62) | `string` | required |
| `server_service_name` | [L447](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L447) | `string` | `null` |
| `sqs_queues` | [L495](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L495) | `object({` | `null` |
| `states_bucket_name` | [L161](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L161) | `string` | required |
| `unique_suffix` | [L6](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L6) | `string` | `""` |
| `uploads_bucket_name` | [L166](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L166) | `string` | required |
| `uploads_bucket_url` | [L171](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L171) | `string` | required |
| `user_uploaded_workspaces_bucket_name` | [L176](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L176) | `string` | required |
| `vcs_gateway_certificate_arn` | [L423](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L423) | `string` | `null` |
| `vcs_gateway_container_definition` | [L381](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L381) | `string` | `null` |
| `vcs_gateway_cpu` | [L375](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L375) | `number` | `1024` |
| `vcs_gateway_desired_count` | [L369](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L369) | `number` | `3` |
| `vcs_gateway_domain` | [L357](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L357) | `string` | `null` |
| `vcs_gateway_external_port` | [L399](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L399) | `number` | `1984` |
| `vcs_gateway_internal` | [L411](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L411) | `bool` | `false` |
| `vcs_gateway_internal_port` | [L405](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L405) | `number` | `1985` |
| `vcs_gateway_lb_subnets` | [L417](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L417) | `list(string)` | `[]` |
| `vcs_gateway_log_configuration` | [L363](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L363) | `any` | `null` |
| `vcs_gateway_memory` | [L387](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L387) | `number` | `2048` |
| `vcs_gateway_security_group_id` | [L393](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L393) | `string` | `null` |
| `vcs_gateway_service_name` | [L459](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L459) | `string` | `null` |
| `vpc_id` | [L33](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L33) | `string` | required |
| `workspace_bucket_name` | [L181](https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L181) | `string` | required |

#### `terraform-aws-eks-spacelift-selfhosted` (77 root variables)

Source: [variables.tf](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf) [S].

| Variable | Line | Type | Default |
|---|---|---|---|
| `admin_password` (sensitive) | [L775](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L775) | `string` | `""` |
| `admin_username` | [L769](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L769) | `string` | `""` |
| `availability_zones` | [L113](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L113) | `list(string)` | `null` |
| `aws_region` | [L3](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L3) | `string` | required |
| `backend_ecr_repository_name` | [L310](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L310) | `string` | `null` |
| `create_database` | [L132](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L132) | `bool` | `true` |
| `create_sqs` | [L296](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L296) | `bool` | `false` |
| `create_vpc` | [L71](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L71) | `bool` | `true` |
| `drain_concurrency_async_jobs` | [L703](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L703) | `number` | `1` |
| `drain_concurrency_async_jobs_fifo` | [L709](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L709) | `number` | `1` |
| `drain_concurrency_cronjobs` | [L715](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L715) | `number` | `1` |
| `drain_concurrency_dlq` | [L721](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L721) | `number` | `1` |
| `drain_concurrency_dlq_fifo` | [L727](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L727) | `number` | `1` |
| `drain_concurrency_events` | [L733](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L733) | `number` | `1` |
| `drain_concurrency_iot` | [L739](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L739) | `number` | `1` |
| `drain_concurrency_webhooks` | [L745](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L745) | `number` | `1` |
| `drain_service_account_name` | [L697](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L697) | `string` | `"spacelift-drain"` |
| `ebs_encryption` | [L647](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L647) | `object({` | `{ enabled = false }` |
| `ecr_force_delete` | [L328](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L328) | `bool` | `false` |
| `eks_auto_mode_enabled` | [L342](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L342) | `bool` | `true` |
| `eks_cluster_name` | [L336](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L336) | `string` | `null` |
| `eks_cluster_version` | [L621](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L621) | `string` | `null` |
| `eks_endpoint_private_access` | [L633](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L633) | `bool` | `true` |
| `eks_endpoint_public_access` | [L627](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L627) | `bool` | `true` |
| `eks_managed_node_groups` | [L348](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L348) | `map(object({` | `null` |
| `eks_upgrade_policy` | [L639](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L639) | `object({` | `null` |
| `enable_public_access_block_on_s3` | [L288](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L288) | `bool` | `true` |
| `encryption_type` | [L34](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L34) | `string` | `"kms"` |
| `k8s_namespace` | [L685](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L685) | `string` | `"spacelift"` |
| `kms_arn` | [L28](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L28) | `string` | `null` |
| `kms_encryption_key_arn` | [L45](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L45) | `string` | `null` |
| `kms_jwt_key_multi_regional` | [L63](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L63) | `bool` | `null` |
| `kms_master_key_multi_regional` | [L57](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L57) | `bool` | `null` |
| `kms_signing_key_arn` | [L51](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L51) | `string` | `null` |
| `launcher_ecr_repository_name` | [L316](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L316) | `string` | `null` |
| `license_token` (sensitive) | [L19](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L19) | `string` | `""` |
| `mqtt_broker_domain` | [L660](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L660) | `string` | `null` |
| `mqtt_broker_endpoint` | [L677](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L677) | `string` | `null` |
| `mqtt_broker_type` | [L666](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L666) | `string` | `"builtin"` |
| `number_of_images_to_retain` | [L322](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L322) | `number` | `5` |
| `private_subnet_cidr_blocks` | [L95](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L95) | `list(string)` | `[]` |
| `private_subnet_ids` | [L107](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L107) | `list(string)` | `[]` |
| `public_subnet_cidr_blocks` | [L89](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L89) | `list(string)` | `[]` |
| `public_subnet_ids` | [L101](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L101) | `list(string)` | `[]` |
| `rds_allow_auto_minor_version_upgrade` | [L247](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L247) | `bool` | `false` |
| `rds_apply_immediately` | [L241](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L241) | `bool` | `true` |
| `rds_backup_retention_period` | [L235](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L235) | `number` | `3` |
| `rds_delete_protection_enabled` | [L223](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L223) | `bool` | `true` |
| `rds_enable_http_endpoint` | [L253](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L253) | `bool` | `null` |
| `rds_engine_mode` | [L143](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L143) | `string` | `"provisioned"` |
| `rds_engine_version` | [L138](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L138) | `string` | required |
| `rds_iam_auth` | [L802](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L802) | `object({` | `null` |
| `rds_instance_configuration` | [L160](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L160) | `map(object({` | `{ "primary-instance" = { instance_ide...` |
| `rds_parameter_group_description` | [L199](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L199) | `string` | `null` |
| `rds_parameter_group_name` | [L193](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L193) | `string` | `null` |
| `rds_password_sm_arn` | [L211](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L211) | `string` | `null` |
| `rds_preferred_backup_window` | [L229](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L229) | `string` | `"07:00-09:00"` |
| `rds_regional_cluster_identifier` | [L187](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L187) | `string` | `null` |
| `rds_security_group_ids` | [L217](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L217) | `list(string)` | `[]` |
| `rds_serverlessv2_scaling_configuration` | [L174](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L174) | `object({` | `{ min_capacity = 0.5 max_capacity = 5...` |
| `rds_subnet_group_name` | [L205](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L205) | `string` | `null` |
| `rds_username` | [L154](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L154) | `string` | `"spacelift"` |
| `s3_bucket_configuration` | [L261](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L261) | `object({` | `null` |
| `s3_retain_on_destroy` | [L282](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L282) | `bool` | `true` |
| `security_group_names` | [L119](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L119) | `object({` | `null` |
| `server_acm_arn` | [L782](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L782) | `string` | `""` |
| `server_domain` | [L14](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L14) | `string` | required |
| `server_service_account_name` | [L691](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L691) | `string` | `"spacelift-server"` |
| `spacelift_public_api` | [L757](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L757) | `string` | `""` |
| `spacelift_version` | [L763](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L763) | `string` | `""` |
| `sqs_queue_names_override` | [L302](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L302) | `map(string)` | `null` |
| `unique_suffix` | [L8](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L8) | `string` | `null` |
| `vcs_gateway_acm_arn` | [L796](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L796) | `string` | `""` |
| `vcs_gateway_domain` | [L790](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L790) | `string` | `null` |
| `vcs_gateway_service_account_name` | [L751](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L751) | `string` | `"spacelift-vcs-gateway"` |
| `vpc_cidr_block` | [L77](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L77) | `string` | `"10.0.0.0/18"` |
| `vpc_id` | [L83](https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf#L83) | `string` | `null` |

#### `terraform-aws-iam-spacelift-selfhosted` (21 root variables)

Source: [variables.tf](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf) [S].

| Variable | Line | Type | Default |
|---|---|---|---|
| `aws_partition` | [L1](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L1) | `string` | `"aws"` |
| `deliveries_bucket_name` | [L22](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L22) | `string` | required |
| `iot_topic` | [L118](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L118) | `string` | `null` |
| `kms_encryption_key_arn` | [L7](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L7) | `string` | required |
| `kms_key_arn` | [L17](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L17) | `string` | required |
| `kms_signing_key_arn` | [L12](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L12) | `string` | required |
| `kubernetes_role_assumption_config` | [L83](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L83) | `object({` | `null` |
| `large_queue_messages_bucket_name` | [L27](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L27) | `string` | required |
| `metadata_bucket_name` | [L32](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L32) | `string` | required |
| `modules_bucket_name` | [L37](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L37) | `string` | required |
| `policy_inputs_bucket_name` | [L42](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L42) | `string` | required |
| `rds_iam_auth_config` | [L124](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L124) | `object({` | `null` |
| `run_logs_bucket_name` | [L47](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L47) | `string` | required |
| `run_observability_bucket_name` | [L52](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L52) | `string` | required |
| `secrets_manager_secret_arns` | [L112](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L112) | `list(string)` | `[]` |
| `sqs_queues` | [L97](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L97) | `object({` | `null` |
| `states_bucket_name` | [L57](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L57) | `string` | required |
| `uploads_bucket_name` | [L62](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L62) | `string` | required |
| `user_uploaded_workspaces_bucket_name` | [L67](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L67) | `string` | required |
| `workspace_bucket_name` | [L72](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L72) | `string` | required |
| `write_as_files` | [L77](https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/variables.tf#L77) | `bool` | `false` |

#### `terraform-azure-spacelift-selfhosted` (22 root variables)

Source: [variables.tf](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf) [S].

| Variable | Line | Type | Default |
|---|---|---|---|
| `admin_password` (sensitive) | [L138](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L138) | `string` | `""` |
| `admin_username` | [L132](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L132) | `string` | `""` |
| `app_domain` | [L11](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L11) | `string` | required |
| `db_sku_name` | [L22](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L22) | `string` | `"B_Standard_B2s"` |
| `drain_concurrency_async_jobs` | [L84](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L84) | `number` | `1` |
| `drain_concurrency_async_jobs_fifo` | [L90](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L90) | `number` | `1` |
| `drain_concurrency_cronjobs` | [L96](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L96) | `number` | `1` |
| `drain_concurrency_dlq` | [L102](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L102) | `number` | `1` |
| `drain_concurrency_dlq_fifo` | [L108](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L108) | `number` | `1` |
| `drain_concurrency_events` | [L114](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L114) | `number` | `1` |
| `drain_concurrency_iot` | [L120](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L120) | `number` | `1` |
| `drain_concurrency_webhooks` | [L126](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L126) | `number` | `1` |
| `encryption_rsa_private_key` (sensitive) | [L65](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L65) | `string` | `""` |
| `k8s_default_node_pool` | [L33](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L33) | `object({` | `{}` |
| `k8s_namespace` | [L16](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L16) | `string` | `"spacelift"` |
| `license_token` (sensitive) | [L58](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L58) | `string` | `""` |
| `location` | [L1](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L1) | `string` | required |
| `postgres_version` | [L28](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L28) | `string` | required |
| `resource_group_name` | [L6](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L6) | `string` | required |
| `spacelift_public_api` | [L72](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L72) | `string` | `""` |
| `spacelift_version` | [L78](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L78) | `string` | `""` |
| `vcs_gateway_domain` | [L145](https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/variables.tf#L145) | `string` | `""` |

#### `terraform-google-spacelift-selfhosted` (34 root variables)

Source: [variables.tf](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf) [S].

| Variable | Line | Type | Default |
|---|---|---|---|
| `admin_password` (sensitive) | [L204](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L204) | `string` | `""` |
| `admin_username` | [L198](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L198) | `string` | `""` |
| `app_service_account_name` | [L19](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L19) | `string` | `"spacelift-backend"` |
| `database_deletion_protection` | [L63](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L63) | `bool` | `true` |
| `database_edition` | [L43](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L43) |  | `"ENTERPRISE"` |
| `database_tier` | [L53](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L53) |  | `"db-n1-standard-8"` |
| `database_version` | [L58](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L58) |  | `null` |
| `drain_concurrency_async_jobs` | [L150](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L150) | `number` | `1` |
| `drain_concurrency_async_jobs_fifo` | [L156](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L156) | `number` | `1` |
| `drain_concurrency_cronjobs` | [L162](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L162) | `number` | `1` |
| `drain_concurrency_dlq` | [L168](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L168) | `number` | `1` |
| `drain_concurrency_dlq_fifo` | [L174](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L174) | `number` | `1` |
| `drain_concurrency_events` | [L180](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L180) | `number` | `1` |
| `drain_concurrency_iot` | [L186](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L186) | `number` | `1` |
| `drain_concurrency_webhooks` | [L192](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L192) | `number` | `1` |
| `enable_database` | [L25](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L25) | `bool` | `true` |
| `enable_external_workers` | [L69](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L69) | `bool` | `false` |
| `enable_gke` | [L31](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L31) | `bool` | `true` |
| `enable_network` | [L37](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L37) | `bool` | `true` |
| `encryption_rsa_private_key` (sensitive) | [L131](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L131) | `string` | `""` |
| `ip_cidr_range` | [L75](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L75) | `string` | `"10.0.0.0/16"` |
| `k8s_namespace` | [L13](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L13) | `string` | `"spacelift"` |
| `license_token` (sensitive) | [L124](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L124) | `string` | `""` |
| `network` | [L101](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L101) | `object({` | `null` |
| `node_service_account` | [L93](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L93) | `object({` | `null` |
| `project` | [L5](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L5) |  | required |
| `region` | [L1](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L1) |  | required |
| `secondary_ip_range_for_pods` | [L87](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L87) | `string` | `"192.168.0.0/20"` |
| `secondary_ip_range_for_services` | [L81](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L81) | `string` | `"192.168.16.0/22"` |
| `spacelift_public_api` | [L138](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L138) | `string` | `""` |
| `spacelift_version` | [L144](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L144) | `string` | `""` |
| `subnetwork` | [L111](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L111) | `object({` | `null` |
| `vcs_gateway_domain` | [L211](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L211) | `string` | `""` |
| `website_domain` | [L9](https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/variables.tf#L9) |  | required |

## 15. Verification

A scan script reads every `.tf` file in the six self-hosted module
repositories (excluding `examples/`) and the chart templates, without
running anything, and lists every bucket, queue, ECS service and task
definition, ECS container command and Kubernetes object. A second script
then checks that this report names every bucket and component the scan finds,
with a link to its file and line at the pinned commit. Results:

```text
# scan: bucket and component lines
== Buckets created per module (resource type, address, name expression, file:line)
terraform-aws-spacelift-selfhosted: 12
  aws_s3_bucket.binaries  modules/s3/main.tf:31
  aws_s3_bucket.deliveries  modules/s3/main.tf:47
  aws_s3_bucket.large_queue_messages  modules/s3/main.tf:98
  aws_s3_bucket.metadata  modules/s3/main.tf:149
  aws_s3_bucket.modules  modules/s3/main.tf:200
  aws_s3_bucket.policy_inputs  modules/s3/main.tf:237
  aws_s3_bucket.run_logs  modules/s3/main.tf:298
  aws_s3_bucket.run_observability  modules/s3/main.tf:360
  aws_s3_bucket.states  modules/s3/main.tf:422
  aws_s3_bucket.uploads  modules/s3/main.tf:459
  aws_s3_bucket.user_uploads  modules/s3/main.tf:533
  aws_s3_bucket.workspaces  modules/s3/main.tf:595
terraform-aws-ecs-spacelift-selfhosted: 0
terraform-aws-eks-spacelift-selfhosted: 0
terraform-aws-iam-spacelift-selfhosted: 0
terraform-azure-spacelift-selfhosted: 1
  azurerm_storage_container.spacelift-container  modules/container_storage/main.tf:35
terraform-google-spacelift-selfhosted: 11
  google_storage_bucket.spacelift-large-queue-messages  modules/storage/main.tf:17
  google_storage_bucket.spacelift-metadata  modules/storage/main.tf:42
  google_storage_bucket.spacelift-modules  modules/storage/main.tf:68
  google_storage_bucket.spacelift-policy-inputs  modules/storage/main.tf:80
  google_storage_bucket.spacelift-run-logs  modules/storage/main.tf:101
  google_storage_bucket.spacelift-run-observability  modules/storage/main.tf:140
  google_storage_bucket.spacelift-states  modules/storage/main.tf:180
  google_storage_bucket.spacelift-uploads  modules/storage/main.tf:211
  google_storage_bucket.spacelift-user-uploaded-workspaces  modules/storage/main.tf:257
  google_storage_bucket.spacelift-workspace  modules/storage/main.tf:297
  google_storage_bucket.spacelift-deliveries  modules/storage/main.tf:337
== ECS services and task definitions
  terraform-aws-ecs-spacelift-selfhosted aws_ecs_service.server  coalesce(var.server_service_name, "server")  modules/ecs/main.tf:24
  terraform-aws-ecs-spacelift-selfhosted aws_ecs_service.drain  coalesce(var.drain_service_name, "drain")  modules/ecs/main.tf:77
  terraform-aws-ecs-spacelift-selfhosted aws_ecs_service.vcs_gateway  coalesce(var.vcs_gateway_service_name, "vcs-gateway")  modules/ecs/main.tf:103
  terraform-aws-ecs-spacelift-selfhosted aws_ecs_task_definition.server  "server-${var.suffix}"  modules/ecs/task_definitions.tf:244
  terraform-aws-ecs-spacelift-selfhosted aws_ecs_task_definition.drain  "drain-${var.suffix}"  modules/ecs/task_definitions.tf:257
  terraform-aws-ecs-spacelift-selfhosted aws_ecs_task_definition.vcs_gateway  "vcs-gateway-${var.suffix}"  modules/ecs/task_definitions.tf:270
== ECS container commands
  drain: ["spacelift", "backend", "drain"]  modules/ecs/container_definitions/drain.tf:20
  server: ["spacelift", "backend", "server"]  modules/ecs/container_definitions/server.tf:18
  vcs-gateway: ["spacelift", "backend", "vcs-gateway"]  modules/ecs/container_definitions/vcs_gateway.tf:18
== Helm Deployments (both chart copies)
  spacelift-helm-charts spacelift-self-hosted/templates/drain-deployment.yaml:2  Deployment <fullname>-drain  cmd=[spacelift backend drain-local]
  spacelift-helm-charts spacelift-self-hosted/templates/scheduler-deployment.yaml:3  Deployment <fullname>-scheduler  cmd=[spacelift scheduler]  if include "spacelift.deployStandaloneScheduler" .
  spacelift-helm-charts spacelift-self-hosted/templates/server-deployment.yaml:2  Deployment <fullname>-server  cmd=[spacelift backend server]
  spacelift-helm-charts spacelift-self-hosted/templates/vcs-gateway-deployment.yaml:3  Deployment <fullname>-vcs-gateway  cmd=[spacelift backend vcs-gateway]  if .Values.vcsGateway.enabled
  spacelift-self-hosted-helm-chart spacelift-self-hosted/templates/drain-deployment.yaml:2  Deployment <fullname>-drain  cmd=[spacelift backend drain-local]
  spacelift-self-hosted-helm-chart spacelift-self-hosted/templates/scheduler-deployment.yaml:3  Deployment <fullname>-scheduler  cmd=[spacelift scheduler]  if include "spacelift.deployStandaloneScheduler" .
  spacelift-self-hosted-helm-chart spacelift-self-hosted/templates/server-deployment.yaml:2  Deployment <fullname>-server  cmd=[spacelift backend server]
  spacelift-self-hosted-helm-chart spacelift-self-hosted/templates/vcs-gateway-deployment.yaml:3  Deployment <fullname>-vcs-gateway  cmd=[spacelift backend vcs-gateway]  if .Values.vcsGateway.enabled

# verify: every scanned item is named in this report with a pinned link
component rows checked: 17
checks: 63, missing: 0
```

## What this means for infraweave

- The reference topology is two stateless processes from one binary (an API
  server and a queue-draining worker that also fires cron), an optional
  gateway for VCS agents, and externally run workers. The plan must decide
  how many processes infraweave runs and how cron and queue consumption are
  shared, given that Spacelift merged its scheduler into the drain.
- PostgreSQL and object storage are the only hard dependencies; queues,
  encryption and the worker broker each have a database-backed or built-in
  form. The plan must cover a Postgres-backed queue with visibility timeouts,
  receive counts, dead-letter and FIFO semantics, and per-queue concurrency.
- Eleven documented buckets (plus launcher binaries) with distinct retention
  and versioning needs, accessed by browsers and workers only through
  pre-signed URLs, must have owners; the states bucket needs versioning for
  history and rollback.
- Secrets at rest need an envelope scheme with a KMS or RSA root that cannot
  be changed after install, plus a separate JWT signing key.
- The worker transport is moving from MQTT to HTTP long-poll; the plan must
  say whether infraweave supports MQTT at all.
- Configuration is environment variables in a fixed vocabulary; first setup
  depends on a break-glass admin login and an immutable-in-practice account
  name.
- Deployment artefacts (Helm chart, Terraform modules for AWS, Azure and GCP,
  an S3-compatible on-prem path) and upgrade rules (infrastructure before
  application, zero-downtime rollouts, migrations on startup) are part of the
  product surface.
- Usage reporting to a vendor endpoint is a self-hosted concern with no
  infraweave equivalent yet; the plan must decide whether to model it.

## Inferred and undocumented

| Item | Basis | Status |
|---|---|---|
| `drain` (ECS) and `drain-local` (Helm, docs) are the same service | Both are the documented drain; the ECS module passes `drain` | *(unverified)* |
| No PostgreSQL extensions are needed | None documented; no module enables any | *(unverified)* |
| No email or SMTP configuration exists in self-hosted | Absent from docs and modules | [I] |
| `FEATURE_FLAG_SELF_HOSTED_V3_INSTALLATION_FLOW` gates the v3 first-setup flow | Flag name; v3 wizard | [I] |
| `OBJECT_STORAGE_BUCKET_USAGE_ANALYTICS` is unused | Every module sets it empty; no doc mentions it | *(unverified)* |
| The Azure `DeleteAfter2Days` rule expires state blobs | Prefix list includes `spacelift-states` | *(unverified)* |
| Which Helm chart repository is canonical | Two copies with different `shared.image` handling | *(unverified)* |
| `/selfhosted/metrics` is a REST route outside GraphQL | Exporter calls it with a bearer JWT | [I] |
| The GCP backend service account needs token creator for V4 URL signing | Role granted to the backend account; buckets accessed through pre-signed URLs | [I] |
| The EKS module's buckets come from AWS module `v3.3.0`, not the pinned AWS commit | `source = ...?ref=v3.3.0` | [S] [EKS main.tf L39][eks-main] |
| The CloudFormation pages on advanced installations, disaster recovery, PostgreSQL upgrades and Slack setup add nothing to the reference architecture | Not read for this report; CloudFormation installs are deprecated from v7.0.0 and dropped in v8.0.0 | *(unverified)* |

## Versions at the research baseline

| Component | Version or commit | Licence |
|---|---|---|
| Self-hosted Spacelift | v7.1.0 (docs `latest`) | proprietary |
| Introspection | `b3255fe38f5e` | n/a |
| spacelift-helm-charts (`spacelift-self-hosted` chart) | `4e02e1d22e73` | MIT |
| spacelift-self-hosted-helm-chart | `660c86391811` | MIT |
| terraform-aws-spacelift-selfhosted | `18c59d122853` | MIT |
| terraform-aws-ecs-spacelift-selfhosted | `400fcb9b1a9a` | MIT |
| terraform-aws-eks-spacelift-selfhosted | `358b62dedfaa` | MIT |
| terraform-aws-iam-spacelift-selfhosted | `73db85af505f` | MIT |
| terraform-azure-spacelift-selfhosted | `427f0eae36dd` | MIT |
| terraform-google-spacelift-selfhosted | `d9cfcb7081b8` | MIT |
| self-hosted-v2-to-v3-kit | `bc80da197853` | MIT |
| self-hosted-usage-data-exporter | `3ed9f8feef07` | MIT |
| Cloud SQL Auth Proxy image (chart default) | `2.21.0` | n/a |

## Sources

Documentation, read 2026-10-28:
- [Reference architecture][sh-intro], [install methods][sh-methods], [environment requirements][sh-env], [external dependencies][sh-deps]
- Reference: [general configuration][sh-gen], [networking][sh-net], [encryption][sh-enc], [message queues][sh-mq], [MQTT broker][sh-mqtt], [object storage][sh-obj], [advanced configuration][sh-adv], [telemetry][sh-tel], [usage reporting][sh-usage], [RDS IAM authentication][sh-rdsiam], [Slack][sh-slack]
- Guides: [observability][sh-obs], [disaster recovery][sh-dr], [first setup][sh-first], [ECS][sh-ecs], [EKS][sh-eks], [AKS][sh-aks], [GKE][sh-gke], [on-prem][sh-onprem], [air-gapped][sh-air]
- CloudFormation: [install][sh-cf], [migration][sh-cfmig]
- Workers: [worker pools][sh-wp], [Docker-based workers][sh-dbw]
- [Self-hosted changelog][sh-cl]

Repositories (pinned commits): every link below.

[sh-intro]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture
[sh-methods]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/install-methods
[sh-env]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/environment-requirements
[sh-deps]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/external-dependencies
[sh-gen]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/general-configuration
[sh-net]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/networking
[sh-enc]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/encryption
[sh-mq]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/message-queues
[sh-mqtt]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/mqtt-broker
[sh-obj]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage
[sh-adv]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/advanced-configuration
[sh-tel]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/telemetry
[sh-usage]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/usage-reporting
[sh-rdsiam]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/rds-iam-authentication
[sh-slack]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/slack
[sh-air]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/air-gapped
[sh-obs]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/observability
[sh-dr]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/disaster-recovery
[sh-first]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/first-setup
[sh-ecs]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/deploying-to-ecs
[sh-eks]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/deploying-to-eks
[sh-aks]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/deploying-to-aks
[sh-gke]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/deploying-to-gke
[sh-onprem]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/deploying-to-onprem
[sh-cf]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/cloudformation/install
[sh-cfmig]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/cloudformation/cloudformation-to-opentofu-terraform-migration
[sh-wp]: https://docs.spacelift.io/self-hosted/latest/concepts/worker-pools
[sh-dbw]: https://docs.spacelift.io/self-hosted/latest/concepts/worker-pools/docker-based-workers
[sh-cl]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog
[hc-values]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/values.yaml
[hc-readme]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/README.md
[hc-chart]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/Chart.yaml
[hc-templates]: https://github.com/spacelift-io/spacelift-helm-charts/tree/4e02e1d22e73/spacelift-self-hosted/templates
[hc-server]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/server-deployment.yaml
[hc-drain]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/drain-deployment.yaml
[hc-sched]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/scheduler-deployment.yaml
[hc-vcsgw]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/vcs-gateway-deployment.yaml
[hc-server-2]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/server-deployment.yaml#L2
[hc-drain-2]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/drain-deployment.yaml#L2
[hc-sched-3]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/scheduler-deployment.yaml#L3
[hc-vcsgw-3]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/vcs-gateway-deployment.yaml#L3
[hc-helpers]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/_helpers.tpl
[hc-notes]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/NOTES.txt
[hc-svc]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/service.yaml
[hc-mqttsvc]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/mqtt-service.yaml
[hc-vcssvc]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/vcs-gateway-service.yaml
[hc-ing4]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/server-ingress.yaml
[hc-ing6]: https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73/spacelift-self-hosted/templates/server-ingress-v6.yaml
[hc2-readme]: https://github.com/spacelift-io/spacelift-self-hosted-helm-chart/blob/660c86391811/README.md
[hc2-server]: https://github.com/spacelift-io/spacelift-self-hosted-helm-chart/blob/660c86391811/spacelift-self-hosted/templates/server-deployment.yaml#L2
[hc2-drain]: https://github.com/spacelift-io/spacelift-self-hosted-helm-chart/blob/660c86391811/spacelift-self-hosted/templates/drain-deployment.yaml#L2
[hc2-sched]: https://github.com/spacelift-io/spacelift-self-hosted-helm-chart/blob/660c86391811/spacelift-self-hosted/templates/scheduler-deployment.yaml#L3
[hc2-vcsgw]: https://github.com/spacelift-io/spacelift-self-hosted-helm-chart/blob/660c86391811/spacelift-self-hosted/templates/vcs-gateway-deployment.yaml#L3
[ecs-root-main]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/main.tf
[ecs-ecs]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/tree/400fcb9b1a9a/modules/ecs
[ecs-vars]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf
[ecs-vars-429]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L429
[ecs-vars-516]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/variables.tf#L516
[ecs-main-24]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/main.tf#L24
[ecs-main-77]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/main.tf#L77
[ecs-main-103]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/main.tf#L103
[ecs-td]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf
[ecs-td-244]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L244
[ecs-td-257]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L257
[ecs-td-270]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/task_definitions.tf#L270
[ecs-cd-server]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/server.tf
[ecs-cd-server-18]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/server.tf#L18
[ecs-cd-vcs-18]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/vcs_gateway.tf#L18
[ecs-cd-drain]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/container_definitions/drain.tf#L20
[ecs-lb-server]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/lb/server_lb.tf
[ecs-lb-mqtt]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/lb/mqtt_lb.tf
[ecs-lb-vcs]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/lb/vcs_gateway_lb.tf
[ecs-lb-sec]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/lb/security.tf
[aws-main]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/main.tf
[aws-vars]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf
[aws-vars-335]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L335
[aws-rds]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/rds/main.tf
[aws-ecr]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/ecr/main.tf
[aws-kms]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/kms/main.tf
[aws-outputs]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/outputs.tf
[ecs-secrets]: https://github.com/spacelift-io/terraform-aws-ecs-spacelift-selfhosted/blob/400fcb9b1a9a/modules/ecs/secrets.tf
[aws-sg]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/network/security_groups.tf
[aws-sqs]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/main.tf
[aws-iot]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/iot.tf
[aws-sqs-2]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/main.tf#L2
[aws-sqs-9]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/main.tf#L9
[aws-sqs-18]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/main.tf#L18
[aws-sqs-32]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/main.tf#L32
[aws-sqs-49]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/main.tf#L49
[aws-sqs-64]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/main.tf#L64
[aws-sqs-78]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/main.tf#L78
[aws-sqs-92]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/sqs/main.tf#L92
[aws-s3]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf
[aws-s3-31]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L31
[aws-s3-47]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L47
[aws-s3-98]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L98
[aws-s3-149]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L149
[aws-s3-200]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L200
[aws-s3-237]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L237
[aws-s3-298]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L298
[aws-s3-360]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L360
[aws-s3-422]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L422
[aws-s3-459]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L459
[aws-s3-533]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L533
[aws-s3-595]: https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L595
[gcp-s3]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf
[gcp-s3-iam]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/iam.tf
[gcp-s3-17]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L17
[gcp-s3-42]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L42
[gcp-s3-68]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L68
[gcp-s3-80]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L80
[gcp-s3-101]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L101
[gcp-s3-140]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L140
[gcp-s3-180]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L180
[gcp-s3-211]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L211
[gcp-s3-257]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L257
[gcp-s3-297]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L297
[gcp-s3-337]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/storage/main.tf#L337
[gcp-db]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/db/main.tf
[gcp-gke]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/gke/main.tf
[gcp-art]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/artifacts/main.tf
[gcp-iam]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/iam/service_account.tf
[gcp-dns]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/dns/main.tf
[gcp-net]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/modules/network/main.tf
[gke-helm]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/helm-values.tftpl
[gke-secrets]: https://github.com/spacelift-io/terraform-google-spacelift-selfhosted/blob/d9cfcb7081b8/templates/kubernetes-secrets.tftpl
[az-main]: https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/main.tf
[az-cs]: https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/modules/container_storage/main.tf
[az-cs-35]: https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/modules/container_storage/main.tf#L35
[az-pg]: https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/modules/postgres/main.tf
[az-acr]: https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/modules/container_registry/main.tf
[aks-helm]: https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/helm-values.tftpl
[aks-secrets]: https://github.com/spacelift-io/terraform-azure-spacelift-selfhosted/blob/427f0eae36dd/kubernetes-secrets.tftpl
[eks-main]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/main.tf
[eks-eks]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/eks.tf
[eks-vars]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/variables.tf
[eks-helm]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/helm-values.tftpl
[eks-secrets]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/modules/kube-outputs/kubernetes-secrets.tftpl
[eks-ebs-kms]: https://github.com/spacelift-io/terraform-aws-eks-spacelift-selfhosted/blob/358b62dedfaa/ebs-kms.tf
[iam-readme]: https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/README.md
[iam-server]: https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/iam_server.tf
[iam-drain]: https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/iam_drain.tf
[iam-ds]: https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/iam_drain_and_server.tf
[iam-sched]: https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/iam_scheduler.tf
[iam-vcs]: https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/iam_vcs_gateway.tf
[iam-exec]: https://github.com/spacelift-io/terraform-aws-iam-spacelift-selfhosted/blob/73db85af505f/iam_execution.tf
[kit-readme]: https://github.com/spacelift-io/self-hosted-v2-to-v3-kit/blob/bc80da197853/README.md
[kit-main]: https://github.com/spacelift-io/self-hosted-v2-to-v3-kit/blob/bc80da197853/main.py
[kit-s3]: https://github.com/spacelift-io/self-hosted-v2-to-v3-kit/blob/bc80da197853/converters/s3_to_terraform.py#L180
[kit-s3scan]: https://github.com/spacelift-io/self-hosted-v2-to-v3-kit/blob/bc80da197853/scanners/s3_scanner.py#L18-L31
[kit-scanners]: https://github.com/spacelift-io/self-hosted-v2-to-v3-kit/tree/bc80da197853/scanners
[kit-delete]: https://github.com/spacelift-io/self-hosted-v2-to-v3-kit/blob/bc80da197853/utils/delete_cf_stacks.py
[exp]: https://github.com/spacelift-io/self-hosted-usage-data-exporter/blob/3ed9f8feef07/export.py
[exp-readme]: https://github.com/spacelift-io/self-hosted-usage-data-exporter/blob/3ed9f8feef07/Readme.md
