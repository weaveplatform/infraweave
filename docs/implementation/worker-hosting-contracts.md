# Managed Kubernetes worker hosting contract

Status: specified; the lab coordinator for the cloud roots is implemented and
controller-tested, while cloud modules, acceptance roots and live cloud evidence
remain undelivered. This is the shared interface required by [#754](https://github.com/weaveplatform/infraweave/issues/754),
under [ADR 0020](../research/decisions/0020-reference-deployments-and-packaging.md#worker-hosting-subset)
and the [worker protocol/chart contract](worker-pool-contracts.md). The
[requirement matrix](worker-pool-contracts.json) keeps specification separate
from executed acceptance. No cloud resources or credentials are needed to review
this document.

## Delivery and ownership

Every cloud delivery includes reusable modules **and** its real acceptance root
in the same PR. A module alone, a rendered chart, local k3s, a mocked provider or
a skipped cloud job cannot complete that delivery.

| Owner | Module or artifact | Acceptance responsibility |
|---|---|---|
| [#748](https://github.com/weaveplatform/infraweave/issues/748) | `deploy/helm/infraweave-workers` | One strict values schema, CRDs, controller, scoped RBAC, resources and Pod security contract across clouds; install/upgrade/finalizer proof |
| [#755](https://github.com/weaveplatform/infraweave/issues/755) | `deploy/terraform/modules/workers/aws` | Ship `test/terraform/platform/workers/aws`, EKS Auto Mode create and explicit existing EKS cases, exact pins, cloud cleanup/readback |
| [#756](https://github.com/weaveplatform/infraweave/issues/756) | `deploy/terraform/modules/workers/azure` | Ship `test/terraform/platform/workers/azure`, AKS create and explicit existing AKS cases, exact pins, cloud cleanup/readback |
| [#757](https://github.com/weaveplatform/infraweave/issues/757) | `deploy/terraform/modules/workers/google` | Ship `test/terraform/platform/workers/google`, GKE Standard create with an explicit node pool and explicit existing GKE cases, exact pins, cloud cleanup/readback |
| [#741](https://github.com/weaveplatform/infraweave/issues/741) | `test/terraform/lab` Kubernetes extension | Local Kubernetes lifecycle and real application execution; supplies no managed-cloud evidence |
| [#758](https://github.com/weaveplatform/infraweave/issues/758) | Existing lab lifecycle extended to cloud roots | Common manifest validation, private journals, failure/interruption cleanup, two-owner isolation and evidence retention |
| [#759](https://github.com/weaveplatform/infraweave/issues/759) | Concurrent acceptance through those roots | Distinct stable pools executing simultaneously on all three clouds, selected-pool loss with no fallback, complete cleanup |

Cloud owners implement the same semantic fields below, exact Terraform type
validation and examples. Cloud identifiers stay explicit in discriminated
objects. Each cloud module supplies a `cluster` submodule for infrastructure
bootstrap and a deployment entry point for an already identified cluster.
Each acceptance root contains `cluster/` and `deployment/` stages calling those
shared modules, without duplicating resources in the test tree. These stages
use separate states and provider initialization: cluster creation completes
before Kubernetes/Helm configuration or CRD discovery. Existing-cluster mode
performs identity/capability readback and creates no cluster infrastructure.
Provider configurations and authentication are supplied by the caller, not
embedded in reusable modules.

The cloud owner chooses verified Terraform/provider/Kubernetes/Helm versions,
commits `required_version`, provider source/constraints and root
`.terraform.lock.hcl` checksums for supported execution platforms, and records
resolved versions. This specification selects no unverified provider version.
Test roots use this checkout's relative module source; published callers pin a
release Git ref to its immutable commit. Roots use `init -lockfile=readonly`;
a missing lock or unresolved artifact fails before apply.

## Common semantic inputs

Names and shapes in this section are the common contract, not executable
Terraform or new application configuration keys. Required fields have no
implicit cloud/account lookup. Objects reject incompatible alternatives and
unknown keys in serialized lab/chart configuration. Null means absent, never
an instruction to discover credentials, clusters or another pool.

| Input shape | Required meaning and validation |
|---|---|
| `ownership = {owner: string, batch: string, expires_at: RFC3339, labels: map(string)}` | Unique lab owner, existing batch name and explicit future UTC TTL. Preserve `io.infraweave.lab.owner`, `.batch` and `.managed-by=terraform` semantics. Owner is immutable per deployment; names/tag encodings must not collide. Provider-specific expiry tag/label encoding maps back to this timestamp. TTL marks sweep eligibility; it does not prove deletion. |
| `cloud = {provider: aws\|azure\|google, identifiers: object}` | Exactly that cloud's explicit identifiers in the mapping below. Verify credentials resolve to these IDs before mutation. A mismatch fails closed. |
| `cluster = {mode: create\|existing, name: string, cluster_id: string, kubernetes_version: string, existing_id: string\|null, managed_profile: string}` | `cluster_id` is the installation-assigned immutable 1..63-character DNS label from the controller contract; it is not the provider resource ID. Create forbids `existing_id`; existing requires it and verifies name/location/profile/version against cloud readback. Kubernetes version is explicit and supported at implementation time; record resolved patch/build. |
| `network = {mode: create\|existing, existing: object\|null, create: object\|null, api_access: object, egress: object}` | Exactly one existing-ID or new-CIDR branch, as mapped below. `cluster.mode=existing` requires `network.mode=existing` and the expected existing network IDs; no import or takeover. `api_access={private: bool, authorized_cidrs: list(string)}` states pipeline reachability. `egress={https_hosts: list(string), cidrs: list(string), dns: object}` states required server/store/source/tool/verifier/registry and DNS paths. No unrestricted access is inferred from a missing field. |
| `namespace = {name: string, setup_owned: bool}` | Existing namespace consumed by the deployment module. The separate setup owner creates it before Secrets when `setup_owned=true`, or verifies explicit delegation when false. A delegated namespace is retained on destroy. Use a unique namespace per owner in an existing cluster. It cannot be a provider system namespace. Watch scope is explicit and cannot overlap another controller's scope. |
| `installation = {release_name: string, crds: owned\|existing, controller_service_account: string, run_service_account: string}` | A dedicated deployment can own CRDs; shared cluster callers require compatible existing CRDs owned by its administrator. Every release/Lease/RBAC name and observed Kubernetes UID is bound to the owner. Run/helper service accounts have no API rights and no automounted token. |
| `artifacts = {module_revision: string, chart: {source: string, version: string, sha256: string}, controller_image: string, bootstrap_image: string, runner_images: map(string), image_pull_secret_refs: list(string)}` | Exact immutable module commit, verified chart package hash or OCI digest and version, and `@sha256:` image references. Controller/bootstrap use the same infraweave release; runner digests reuse the existing GHCR image pins. A release's `worker-artifacts.json` ([worker artifacts](worker-pool-contracts.md#worker-artifacts)) supplies `chart`, `controller_image`, `bootstrap_image` and `runner_images`. Verify signatures/provenance under ADR 0020. Local current-source builds record their content digests and revision; mutable tags/`latest` alone are refused. |
| `server = {endpoint: string, tls: {system_roots: bool, ca_secret_ref: secretKeyRef\|null}}` | Existing HTTPS origin, no URL userinfo, query credential or TLS bypass. `system_roots=false` requires the configured CA reference; custom trust does not disable hostname/chain validation. Kubernetes API TLS trust/authentication is separately configured by the caller. |
| `management_credentials_ref = {id: secretKeyRef, secret: secretKeyRef}\|null` | Same-namespace references to the scoped controller API-key ID/secret. Required for Managed pools; forbidden when only Public pools are configured. Mixed modes must not expose the key to Public pool reconciliation or run/helper Pods. |
| `pools = list({resource_name: string, mode: Managed\|Existing\|Public, pool_id: string\|null, account: string\|null, space: string\|null, capacity: int, credentials_ref: string, settings: object\|null})` | Nonempty list with distinct resource names/bindings. `capacity` is explicitly configured logical slots, 0..2147483647; never node count, controller replicas or queue length. Map exactly to the strict WorkerPool spec: Existing/Public require pool ULID and token/privateKey Secret; Managed takes private settings and learns ULID from status. Public forbids account/space/settings. Managed alone may start with externally supplied privateKey and no token, as specified in the worker contract. |
| `resources = {controller: resourceBudget, bootstrap: resourceBudget, runner: resourceBudget, policy_helper: resourceBudget, storage: object, scheduling: object}` | Each `resourceBudget={requests:{cpu,memory,ephemeral_storage}, limits:{cpu,memory,ephemeral_storage}}` uses explicit positive Kubernetes quantities with requests <= limits. Storage declares each Pod-local emptyDir size and controller staging budget at preparation concurrency. Scheduling is the chart's typed architecture/node-selector/toleration/compute-profile subset; no arbitrary PodSpec or privilege escape. Admit and retain observed resources after provider mutation. |
| `timeouts = {provision_seconds: int, readiness_seconds: int, cleanup_seconds: int}` | Positive bounded infrastructure/lab waits, separate from issued preparation/run/hard deadlines. Expiry fails the phase and enters cleanup; it never reports absence or permits a replacement execution. |

`secretKeyRef = {name: string, key: string}` always addresses an existing Secret
in the watched namespace. Pool `credentials_ref` is a Secret name with fixed
`token` and `privateKey` keys. Optional policy references follow the same chart
contract and are distinct from server CA trust. Secret names/references are
configuration; secret values are never inputs to these deployment modules.

Business retains **three aggregate private workers** across accounts, pools and
clusters, with an explicit organization grant of four for the four-worker lab.
Public enablement stays false by default and creates no capacity. Account/run
quotas, organization grants and public-pool operator bootstrap belong to the
application/operator setup, never to a cloud-specific tier or cloud module.

## Three-cloud mapping and compatibility

| Semantic field | AWS | Azure | Google Cloud |
|---|---|---|---|
| Cloud IDs/location | `account_id`, `region` | `tenant_id`, `subscription_id`, `location`, `resource_group_name` | `project_id`, `project_number`, regional `location` |
| Cluster ID | EKS cluster ARN and name | Full AKS ARM resource ID and name | `projects/{project}/locations/{location}/clusters/{name}` |
| Create profile | EKS Auto Mode | AKS with explicitly selected supported Linux node pool/profile and network-policy implementation | Regional GKE Standard with an explicit node pool sized for 12 GiB ephemeral storage per run |
| Existing profile | Explicit EKS cluster; Auto Mode or declared managed-node profile verified by this delivery | Explicit AKS cluster and supported Linux node pools | Explicit GKE Standard cluster and declared node pool verified by this delivery |
| Existing network branch | `vpc_id`, control-plane/worker `subnet_ids`, security-group IDs and API access policy | VNet/subnet ARM IDs, resource-group ownership, network-policy/profile and API access policy | Network/subnetwork resource IDs and Pod/Service secondary-range names, API access policy |
| Create network branch | Named VPC CIDR, subnet CIDRs/AZs, explicit routes/NAT or endpoints | Named VNet/subnet CIDRs, explicit egress/NAT and network-policy/profile | Named VPC/subnet CIDRs and Pod/Service secondary CIDRs, explicit egress/NAT |
| Bootstrap/cloud identity | Caller IAM role ARN and short-lived authentication; cluster/node roles identified separately | Caller Entra principal/object ID; cluster/kubelet managed identity resource/principal IDs separately | Caller principal/service account and federation configuration; cluster/node service account resource separately |
| Cloud absence readback | EKS plus tagged EC2/VPC/IAM/other owned resources and exact recorded IDs | AKS plus owned resource groups, managed node resource group and exact network/identity IDs | GKE plus labeled compute/network/identity resources and exact recorded IDs |

Existing managed profiles are explicit opt-ins with their own real admission
case; targeting a cluster by name is not proof that it meets this contract.
No EKS self-managed-node, GKE Autopilot or alternate managed mode is silently
substituted when the requested create profile is unavailable. No Windows,
privileged DinD, host socket, hostPath, host namespace, node login or
controller-to-run shared volume is required. All three profiles must support
cluster-scoped CRD definitions with namespaced WorkerPool/Worker instances,
RBAC/Lease, exact Pod UID attachment/exec, non-root UID/GID 1983,
read-only roots, Pod-local staging, network policy and the complete lifecycle.

[EKS Auto Mode's SELinux isolation](https://docs.aws.amazon.com/eks/latest/userguide/auto-troubleshoot.html)
can prevent sharing a volume across Pods; the contract transfers assets into
one execution Pod instead. [AKS API access](https://learn.microsoft.com/en-us/azure/aks/concepts-security#secure-api-server-access-for-deployment-pipelines)
requires both network reachability and authorization; cloud authentication does
not replace scoped Kubernetes RBAC or the infraweave management key.
The chart's Pods meet the Kubernetes [restricted Pod Security Standard](https://kubernetes.io/docs/concepts/security/pod-security-standards/),
so a GKE Standard node pool asks nothing of them that EKS or AKS does not. These
facts motivate the portable design; they are not infraweave execution evidence.

The storage contract distinguishes `emptyDir.sizeLimit`, container resource
requests/limits, and actual node allocatable/free storage. An [emptyDir size
limit](https://kubernetes.io/docs/concepts/storage/volumes/#emptydir) is not a
reservation and does not prove node capacity. Preserve the worker contract's
12 GiB default resource budget; a 12 GiB emptyDir ceiling alone does not require
a particular compute class.

Google's [Autopilot resource contract](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/autopilot-resource-requests#minimums_and_maximums)
limits ordinary Autopilot Pods to 10 GiB total ephemeral-storage requests, below
the 12 GiB run budget, so the Google create profile is GKE Standard with an
explicit node pool instead. On Standard, ephemeral storage is a node pool
property rather than an admission rule: the pool's machine type and boot disk
(or local SSD ephemeral storage) are sized so each node's allocatable ephemeral
storage holds 12 GiB for every run it hosts at once, plus the controller's
staging and the node's system reservations. The Google delivery resolves the
actual rendered 12 GiB resource limit against that allocatable capacity, not
against the emptyDir limit alone. It records its machine type, disk type and
size, GKE version, actual minimum CPU/memory, init/main requests and limits,
node allocatable capacity, runs per node, preparation concurrency and cost
envelope before the normal maintainer-approved apply. It must not silently
shrink the default budget, add accelerators, substitute Autopilot, introduce
shared storage or relax source/tool bounds to fit a node. A budget
or storage-design change requires explicit reconciliation here and in the
worker contract. Controller staging receives the same admission/capacity check.

Compatibility is checked at the selected release/location, including Kubernetes
minor, API/CRD versions, policy enforcement, image architecture, registry pulls,
init/main TLS attach, signals, staging and scheduling/deadlines. A Helm render
or successful cluster API health check cannot establish it.

## Authority and secret transport

| Identity | Authority | Transport and exclusion |
|---|---|---|
| Terraform provisioner | Approved cloud infrastructure and install-time Kubernetes access, scoped to the target environment | Caller OIDC/short-lived cloud authentication and exec credential flow. Never copied into chart values, controller environment, pool credentials or run Pods. Cluster/node identities are infrastructure identities, not run credentials. |
| Controller management API identity | Existing worker-pool-controller role within declared account/space, for Managed resources | Pre-created referenced Secret; existing account API-key login. No account administrator, pool reset or cluster-provisioning power. Kubernetes service-account RBAC is separate. |
| Pool protocol identity | Pool certificate/token and private key, stable pool/owner binding and `/worker/v1` | Existing same-namespace token/privateKey Secret; matching key is read together, never generated by Terraform. Managed response-loss recovery may patch only token on that Secret. Public credentials are explicitly operator-issued. |
| Worker-session and run identities | Their existing distinct protocol/session and run audiences, subject, expiry and fencing | Acquired in process through the existing protocol. Run handoff over private TLS stdin contains only its permitted credentials and pool public key; no management, provisioner or pool private key. |

A separate authorized setup process creates/labels a lab-owned namespace (or
verifies its delegated existing namespace), then creates credential Secrets after
cluster/bootstrap and before deployment. The deployment module consumes that
namespace with chart namespace creation disabled; no targeted apply is needed. It reads private input files or a
configured secret manager without printing values or placing them in shell
arguments. Modules must not read Secret data via Terraform data sources or
accept `set_sensitive` values carrying pool keys. Validate references and let
the controller fail closed on missing/mismatched credentials. Existing Secrets,
CA/policy bundles and image-pull credentials retain their external ownership.
A lab-owned temporary Secret can be deleted by its setup owner after finalizers
finish; a borrowed Secret is never deleted by namespace cleanup.

Terraform `sensitive` redacts ordinary display; it does **not** exclude values
from [state and plan files](https://developer.hashicorp.com/terraform/language/manage-sensitive-data).
Provider authentication/cluster metadata, generated infrastructure values or
provider diagnostics may still be sensitive. Use restricted encrypted remote
state with locking and owner-specific keys, or private local directories (0700)
and files (0600); protect state backups, plans, provider data and kubeconfigs
likewise. Do not export raw state/plan JSON to public evidence. Disable verbose
credential logging and sanitize diagnostics by allowlist. Never commit tfvars,
keys, kubeconfigs or tokens. Protect Kubernetes Secrets with encryption at rest,
scoped reads and audited attach/exec access; [base64 is no encryption](https://kubernetes.io/docs/concepts/security/secrets-good-practices/).
Workload cloud integration and full server/database/object-store/key-root
modules stay in their later phases.

## Outputs and manifest boundary

Both module stages return non-secret identifiers and explicit ownership for
created resources and borrowed references;
they do not output cloud access tokens, kubeconfig contents, management keys,
pool private keys or plaintext run inputs. The deployment's common output is
`hosting`, with this shape on every cloud:

| Field | Shape and source |
|---|---|
| `schema`, `kind`, `owner`, `batch`, `expires_at` | `1`, `worker-hosting`, exact ownership input |
| `cloud` | `{provider, identifiers}` as above, with verified cloud IDs |
| `cluster` | `{id, name, cluster_id, kubernetes_version, managed_profile, owned: bool, network_ids: list(string)}`; provider ID separate from controller ID |
| `namespace` | `{name, uid, owned: bool}`; UID is observed after setup and ownership matches `namespace.setup_owned` |
| `installation` | `{release_name, chart_version, chart_sha256, controller_service_account, run_service_account, crds_owned: bool}` |
| `artifacts` | `{module_revision, controller_image, bootstrap_image, runner_images}` with resolved immutable pins |
| `pools` | `list({resource_name, resource_uid, mode, requested_pool_id: string\|null, capacity, credentials_ref})`; no invented Managed ULID before reconciliation |
| `resources` | Requested resource/storage/scheduling budgets; observed admission changes are retained separately in readiness evidence |
| `inventory` | `list({kind, id, scope, owned: bool, owner, lifecycle: cluster\|deployment\|setup\|external})`; exact cloud/Kubernetes IDs, including provider-created dependents, plus expected retained borrowed resources |

The lab enriches the inventory from cloud/Kubernetes readback for dependents
not directly represented in Terraform state. Terraform cannot declare readiness
from desired values. The lab reads actual WorkerPool status to produce `readiness.json`: `{schema:1, owner, passed,
cluster_id, namespace_uid, pools:[{resource_uid,pool_id,management_identity,desired,registered,
busy,conditions}], admitted_pods, checked_at}` with only non-secret status.
Here `cluster_id` is the verified provider resource ID from `hosting.cluster.id`;
the controller ID remains in the pool management identity, not a fictional
Kubernetes cluster UID.
Validate stable pool ID and cluster/namespace/resource UID binding against
normal API reads, desired/registered capacity and real Pod resources. A mismatch,
missing status, skipped cloud, absent credential or zero executed scenarios fails
acceptance. Managed `pool_id` is known only after this reconciliation; Existing
and Public must match their requested ULID.

Keep the existing local `acceptance.json` schema 1, containing
`owner`/`batch` and PostgreSQL/objectstore/Gitea endpoints (plus the optional
owned local Kubernetes fixture supplied by its extension), valid for its current
consumer. The lab extension writes a separate `workers.json` envelope
`{schema:1, owner, batch, hosting:[hosting]}` and validates it before any remote
execution. This avoids pretending cloud clusters are Docker container IDs or
adding fields rejected by an existing strict consumer. The application harness
still owns its source-built server/acceptance processes and real dependencies.
The server HTTPS endpoint, store and source must actually be reachable from
remote Pods, with declared TLS/egress; loopback endpoints from the local
manifest are not usable remotely by assumption. The cloud tests run against the
platform scenario deployed in one cloud's dedicated account
(`test/terraform/platform/<cloud>`), whose public HTTPS endpoint, object store
and source the worker Pods on every cloud reach.

## Apply, run and destroy procedure

These are delivery requirements for the roots above, not commands implemented
by this documentation change. Each cloud owner ships a runnable README and
its exact acceptance invocation in the same PR; lifecycle automation follows in
its assigned lab owner. Run cloud applies only with the existing protected
GitHub environment/maintainer approval and authorized short-lived credentials.
Publishing this contract grants no provisioning or account-mutation authority.

1. The lab operator creates a fresh private deployment directory and unique
   owner/namespace/cluster ID, expiry, state keys and provider-data directories
   for each stage. Verify cloud identity/location against explicit inputs. Keep
   another owner's deployment B running for the isolation control. Prepare the
   existing application lab with an externally reachable trusted HTTPS endpoint;
   obtain the separately authorized pool management/bootstrap inputs. Apply any
   four-worker organization grant explicitly through the application setup.
2. For create mode, initialize/plan/apply the cloud root's `cluster/` stage;
   record exact cluster/network/identity IDs and pins. For existing mode verify
   the exact borrowed cluster/network and prerequisites and retain an inventory
   with `owned=false`. Establish short-lived Kubernetes access only after the
   cluster exists. The cloud stage does not initialize Kubernetes/Helm against
   a not-yet-created endpoint. In a shared cluster its administrator owns compatible CRDs and
   deployers verify them; a dedicated deployment installs chart-owned CRDs
   before its custom resources.
3. The setup owner first creates/labels its owned namespace or verifies the
   delegated namespace, then installs/reconciles explicitly referenced Secrets
   and policy/CA inputs. The deployment module must not recreate or adopt it. Initialize/plan/apply `deployment/`, using the observed
   cluster ID and caller Kubernetes/Helm providers. Save its `hosting` output
   privately, validate `workers.json`, and wait for actual binding/readiness.
4. The cloud test owner runs its documented normal API/SDK acceptance command
   against the same real application lab and selected cloud pool, retaining
   revision, exact executable/image/chart/provider versions, nonzero executed
   scenarios/steps, operation classifications and sanitized reports. Exercise
   create/existing network and cluster modes, identity denials, secret absence,
   a real plan/apply and workspace restore, drain/cycle/stop/kill and controlled
   capacity/cleanup failure. Cross-cloud simultaneous proof remains separate.
5. On success, failure or cancellation, the lab cleanup owner first quiesces
   new claims and deletes only owned WorkerPool resources while the controller,
   Secrets, API and cluster still work. Wait for owned Worker/Pod absence,
   acknowledged deregistration, released binding and Managed-only remote pool
   deletion. Existing/Public remote pools survive. Do not strip finalizers or
   destroy the cluster as a shortcut for acknowledgement.
6. Destroy the `deployment/` state after protocol cleanup, then remove only
   setup-owned Secrets/namespaces. Borrowed namespaces/Secrets/cluster/network
   survive; borrowed namespaces must have no owned resources left. Destroy the
   `cluster/` state last, only in create mode, after the installation cleanup
   owner explicitly removes retained owned CRDs and confirms no remaining
   dependent resources. Shared CRDs are retained. Capture Kubernetes absence
   and retained-resource readback before deleting its API endpoint.
7. Require empty owned Terraform resource state **and** authenticated cloud
   absence readback of recorded inventory and ownership tags/labels, together
   with the pre-destroy Kubernetes readback. In existing mode repeat the live
   Kubernetes checks against the surviving cluster. Confirm
   other owner B still executes and shared prerequisites survive, then clean B
   independently. Persist failed/unknown cleanup and retry from the same private
   journal; permission-denied, timeout, not-yet-deleted or missing credentials
   cannot be reported as absence. Delete private keys/plans/backups only after
   verified absence; retain sanitized evidence and the cleanup result.

For each stage, the runnable root README supplies non-secret backend/input
paths for this ordinary Terraform sequence (the same pattern applies to
`cluster/` and `deployment/` with separate paths):

```sh
terraform -chdir="$stage_root" init -input=false -lockfile=readonly \
  -backend-config="$private_backend_file"
terraform -chdir="$stage_root" plan -input=false \
  -var-file="$private_variables_file" -out="$private_plan_file"
terraform -chdir="$stage_root" apply -input=false "$private_plan_file"
```

After the acceptance invocation and ordered protocol cleanup, the cleanup
owner uses the same stage's backend/variables to destroy:

```sh
terraform -chdir="$stage_root" destroy -input=false \
  -var-file="$private_variables_file"
terraform -chdir="$stage_root" state list
```

The deployment README must include its actual API/Kubernetes/cloud readback
commands and failure cleanup path. An empty state alone is insufficient.
Never reuse an owner's state path, sweep by name prefix alone, import a foreign
resource or destroy an existing shared cluster/network. Helm does not
[delete CRDs on uninstall](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/);
CRD absence/retention is an explicit installation cleanup responsibility.
The deployment phase must retain the controller until WorkerPool finalizers finish, using a
separate cleanup phase rather than relying on unordered chart uninstall.

## Lab coordinator interface

[#758](https://github.com/weaveplatform/infraweave/issues/758) implements the
lifecycle above in `test/terraform/lab/cloudlab.py`, selected through the
`workers-aws`, `workers-azure`, `workers-google` and `workers-multicloud`
batches; the [lab README](../../test/terraform/lab/README.md#managed-cloud-worker-batches)
is its runnable reference. The cloud roots meet it as follows:

- Each stage declares `backend "local" {}`; the coordinator supplies an
  owner-private state path, `TF_DATA_DIR`, plan and log per stage, with
  `init -lockfile=readonly`.
- The coordinator adds `ownership`, `cloud` and, for `deployment/`, the
  validated `cluster_stage` to the operator's stage variables, and refuses
  inputs that set them or carry private key text.
- `cluster/` outputs `cluster = {schema: 1, kind: "worker-cluster", owner,
  batch, provider, id, name, endpoint, ca_certificate, owned, inventory}`. The
  coordinator requires the explicit cluster's provider ID from the mapping
  above and a verified TLS handshake with `endpoint` before `deployment/`.
- `deployment/` outputs `hosting` exactly as specified above; its owner, TTL,
  cloud identifiers and cluster ID must match before `workers.json` exists.
- Owned resources carry `io.infraweave.lab.owner` as a tag; Google encodes the
  key as the label `io_infraweave_lab_owner`. Cleanup searches by that key and
  reads the exact owned cluster; a denied or failed readback is unknown.

Preflight before the first apply verifies committed stages and locks, private
inputs, credentials against the explicit IDs, regional vCPU and EKS cluster
quota headroom, and any existing cluster's readiness. Every selected cloud is
required; none is skipped.

## Required evidence and acceptance limits

Each cloud owner records cluster create and existing-cluster results at the
actual tested revision; separate-state cleanup on apply/test failure and
interruption; exact pins and cloud identity; observed managed admission and
resource mutations; normal API/SDK success, authorization denial and failure;
protocol/operator credential classifications; and secret-positive/negative
assertions without retained plaintext. Existing network retention, external
Secret retention, shared CRD survival, TTL sweep safety and two-owner cleanup
are required controls. The cleanup report names every owned/borrowed inventory
entry, readback time and outcome; an omitted dependency is not presumed absent.

Live proof requires actual scheduled Pods, bootstrap/handoff, execution and
terminal acknowledgement; images or logs alone do not prove backend support.
Keep all existing native, OS, configured golangci-lint, certified-FIPS and
>=95% total/>=90% per gated package checks. This specification's docs/register
checks establish consistency only. It does not close cloud acceptance or any
unavailable reference capture, and it adds no cross-cloud fallback or
queue-driven autoscaler.
