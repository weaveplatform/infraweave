# infraweave acceptance deployment batches

`infraweave-lab` extends the existing acceptance harness. Terraform provisions
real PostgreSQL, RustFS object storage and TLS Gitea, plus a private network and
three owned data volumes. The same covered application, server replicas,
launchers, workers, OPA/Rego programs and Gherkin assertions exercise them.
The controller never substitutes an API fake for these services.

The application under test is built from the current checkout. Runner images
supply the infrastructure tools; application/worker executables remain separate
current-source artifacts. The [target architecture](../../../docs/diagrams/infraweave-lab-test-architecture.drawio.svg)
shows the broader deployment design. Application and launcher processes still
use the harness lifecycle, and the GitLab/DC fixture servers retain their existing
startup helpers. Those parts of the infrastructure migration remain in progress.

## Run a batch

Use the official Terraform release pinned by `test/deps/tools.go`, Python 3 and
a reachable Docker daemon. The provider is pinned to `kreuzwerker/docker` 4.7.0
with checksums for local ARM64 and both Linux architectures. Service images come
from the same immutable pins as ordinary acceptance. The lab uses local Docker
infrastructure and creates no cloud resources.

```sh
GOWORK=off go build -o /private/tmp/infraweave-lab ./test/acceptance/cmd/infraweave-lab
python3 test/terraform/lab/labctl.py run --batch policies --fips \
  --helper /private/tmp/infraweave-lab --directory /private/tmp/new-policy-batch
```

The directory must be new. Each deployment gets a unique owner, Terraform
backend, provider-data directory, TLS fixture and service ports. Existing
batch directories are never adopted or destroyed by a new invocation.
Tests validate the manifest's schema, running containers, Terraform ownership
labels and exact loopback port bindings before connecting. They never silently
start replacement dependencies. Each test process gets its own database template
and fixture namespace, including nested capture replays.

| Batch | Existing Gherkin selection |
|---|---|
| `kubernetes` | Required `kubernetes-lab` partition: real owned k3s API/node/namespace, the worker CRDs, and replicated `infraweave worker-controller` processes reconciling pools against two servers; the Pod executor remains separate |
| `policies` | `@policies` across partitions: real Rego V0/V1, approvals, warnings, flags, isolation and limits |
| `core`, `terraform-plans`, `terraform-vcs`, `terraform`, `runs`, `integrations` | The named existing Cucumber partition |
| `vcs` | The existing real-provider partition, narrowed with `--tags '@lab:github'` and `--features features/vcs/feedback.feature` |
| `workers-aws`, `workers-azure`, `workers-google` | Required `managed-cloud-workers` partition against one cloud's committed `test/terraform/cloud/workers/<cloud>` root and the local platform's control plane |
| `workers-multicloud` | The same partition with all three clouds attached concurrently under one owner and one control plane |

These are Gherkin batches. The ordinary quality gate still runs every existing
component `TestAcc`, unit suite, partition and FIPS check. No existing check or
coverage threshold is removed. The added required Terraform policy batch runs
under certified FIPS and contributes coverage separately from its retained
Cucumber/evidence report.

`--fips` builds and runs infraweave's covered application and acceptance harness
with `GOFIPS140=certified` and `GODEBUG=fips140=only`. The helper is built with the
same settings in CI. These settings are scoped to those processes: official
Terraform's provider installer uses OpenPGP key IDs that require SHA-1 and cannot
inherit the application's FIPS-only runtime switch. Provider signature and locked
package-checksum verification remain enabled.

`--terraform /absolute/path` chooses the control-plane Terraform executable;
`--helper /absolute/path` names the built lab helper. `up` and `down` support
manual inspection of a deployment. A second successful `down` verifies absence
again without requiring private files already deleted by the first cleanup.

## Evidence and cleanup

The controller streams test progress and retains its complete log. A run passes
only if at least one scenario and step executed, every step passed, and cleanup
succeeded. Failed commands and cancellation still enter the cleanup path.
Terraform destroy is followed by empty-state and label-filtered Docker container,
volume and network readback. Images stay cached for other owners. Private server
keys, the saved deployment plan and the state backup are deleted only after
absence is verified. A failed cleanup retains its private journal and fails the
batch. The workflow also retries cleanup from that journal after interruption.

The private deployment directory contains `acceptance.json`, `readiness.json`,
`result.json`, `cleanup.json`, covered profiles under `coverage`, and executed
reports/diagnostics under `evidence`. CI uploads reports, sanitized Terraform failure diagnostics and coverage;
it excludes Terraform state, plans, private variables and TLS keys.

The renamed `.github/workflows/infraweave-lab.yml` applies each real provider's
existing Terraform repository/hook root, then uses a fresh platform deployment
for each source, webhook and feedback batch. The protected `vcs-lab` environment,
main-only rule, stable concurrency key, provider-specific cleanup and historical
failure-issue titles are retained. They preserve existing secret permissions and
prevent old/new workflow versions from sharing a GitHub App concurrently.

## Local proof — 2026-11-21

The actual Terraform deployment and policy batch passed on native ARM64 Docker
Desktop 29.6.1 / API 1.55 with official Terraform 1.17.2 and linked OPA v1.22.4:
**15 scenarios / 561 steps under certified FIPS**. All steps passed. Both the
explicit deployment probe and complete batch controller verified zero owned
containers, volumes and networks plus empty Terraform state after destroy.
Repeated cleanup passed after private-input removal. A controlled two-owner probe
then destroyed A, verified all three B services and shared image pins survived,
and required B's 15-scenario policy matrix and cleanup to pass. An actual empty
selection returned failure and still verified complete infrastructure cleanup;
it is a negative control, not a passing application scenario. The
[machine-readable proof](../../../docs/implementation/infraweave-lab-verification.json)
records the report hash and scope. These are local candidate
results; the final exact-head CI and real-provider walkthrough remain required.

The required Terraform policy job also passed on Linux in
[CI run 44880876138](https://github.com/weaveplatform/infraweave/actions/runs/44880876138)
for candidate `7363b14c`: 15 scenarios / 561 steps with certified FIPS and verified
cleanup. Its retained report records the actual checkout revision, image pins,
OPA version and empty-state/absence results. This is passing evidence for that
candidate; subsequent changes still require their own final-head gate.

Controller unit tests separately prove empty/skipped/undefined reports fail,
failed test commands still clean up, cleanup failure cannot report success, and
another invocation's deployment directory is left untouched. Go race/FIPS
controls reject malformed manifests, foreign/stopped containers, mismatched
service ports and invalid CAs while preserving independent fixture namespaces.

## Local Kubernetes batch

The `kubernetes` batch adds a Terraform-owned privileged **control-plane fixture**
using the committed multi-architecture `ImageK3s` digest. This infrastructure
privilege is separate from the unprivileged run/controller contract; no DinD
worker or controller-host mount is introduced. It uses the existing pinned Docker
provider and committed lock, an owned network/volume, loopback-only dynamic API
port, 2 CPUs and 4096 MiB limit (configurable within 1–4 CPUs/2048–8192 MiB).
Bundled image-pulling add-ons are disabled. K3s applies the Terraform-supplied
owner namespace. The [official server flags](https://docs.k3s.io/cli/server)
describe the disabled components and kubeconfig behavior; the digest is the
executed pin, not a floating latest tag.

```sh
python3 test/terraform/lab/labctl.py run --batch kubernetes --fips \
  --helper /private/tmp/infraweave-lab --directory /private/tmp/new-kubernetes-batch
```

The private manifest includes cluster container ID, exact HTTPS loopback endpoint,
owner namespace and private JSON kubeconfig path. The controller exports embedded
CA/client certificate/key without printing them, waits at most 180 seconds for
actual API/node/namespace readiness, and the normal Go harness independently
validates Docker labels, exact published port, TLS identity and namespace owner.
An absent, stopped, foreign or unreachable cluster fails before application
scenarios. Neither attachment path provisions a replacement. The helper's
`verify-lab --manifest /private/path/acceptance.json` performs the same live checks.
TLS material, Terraform state/plans and kubeconfig stay private. Verified destroy
removes private keys, saved plans and kubeconfig; the empty state and public
certificates remain as readback evidence. Repeated cleanup needs no removed files.

The required Linux lab matrix retains the existing policy batch and adds this
partition, including certified-FIPS application builds and separate coverage.
Ordinary/native acceptance partitions retain their scenarios; only the new
`@kubernetes-lab` cases require this explicitly provisioned cluster. The three
cases run actual current-source `infraweave` servers through normal API/SDK paths
and prove existing worker-pool create/delete, authorization denial and malformed
CSR refusal. The worker CRD schemas are installed and exercised through real
admission, and `kubernetes_controller.feature` runs replicated
`infraweave worker-controller` processes in per-scenario namespaces against two
servers behind an HTTPS front: managed and existing pools, leader replacement,
ownership refusals, the private-workers limit and finalizer cleanup. They do
not claim a Kubernetes Pod executor, managed cloud deployment or signed-run
admission, which are not implemented by this lab.
Empty, skipped and undefined scenario reports still fail the controller census.

Before that CI batch, the real failure controls run:

```sh
python3 test/terraform/lab/verify_kubernetes.py \
  --helper /private/tmp/infraweave-lab --directory /private/tmp/new-kubernetes-controls
```

They provision two independent owners, reject a foreign manifest and a mismatched
Terraform cleanup journal before destroy, pause the real first cluster and require
attachment failure, resume the same container and recheck readiness, then destroy
and repeatedly clean the first owner while the second stays live. A final cleanup
verifies both owners absent even after an earlier failure. `controls.json` retains
only outcome/ownership/readback metadata, never kubeconfig. Terraform/Docker
commands have bounded timeouts; shared cached images remain for other invocations.

The [local Kubernetes proof](../../../docs/implementation/kubernetes-lab-verification.json)
records the dirty candidate baseline, immutable image, report hashes and scope:
3 Kubernetes scenarios / 33 steps and the existing policy batch's 51 scenarios /
902 steps passed with certified FIPS. All six real ownership/readiness controls
passed, and destroy verified empty state, absence and private-input removal for
both owners. This is local candidate evidence; required Linux CI and the final
exact-head coverage gate still apply. It proves infrastructure and existing pool
API behavior, leaving controller/executor delivery to its implementation work.

## Managed cloud worker batches

The `workers-*` batches attach Terraform-managed EKS, AKS and GKE worker
hosting to the same lab. Each selects the `@managed-cloud-workers` scenarios
in `features/workers/managed_cloud_workers.feature`, which ordinary and other
lab partitions exclude. The committed definition fixes the clouds and the
bounds a reviewer approves before any billable apply: parallel clouds, owner
TTL, the vCPU quota headroom each cloud must have, configured pool capacity per
cloud, and provisioning, readiness and cleanup time. `cloudlab.py` refuses a
definition outside the controller's limits and records its SHA-256 in the
journal and review.

```sh
python3 test/terraform/lab/labctl.py preflight --batch workers-multicloud \
  --inputs aws=/private/tmp/aws.json --inputs azure=/private/tmp/azure.json \
  --inputs google=/private/tmp/google.json
python3 test/terraform/lab/labctl.py run --batch workers-multicloud --fips \
  --helper /private/tmp/infraweave-lab --directory /private/tmp/new-cloud-batch \
  --inputs aws=/private/tmp/aws.json --inputs azure=/private/tmp/azure.json \
  --inputs google=/private/tmp/google.json
```

`preflight` mutates nothing and prints the review. `run` and `up` repeat it in
the new deployment directory and stop before the platform or any cloud is
provisioned unless every selected cloud passes:

| Check | Fails when |
|---|---|
| Root | `test/terraform/cloud/workers/<cloud>/{cluster,deployment}` lacks `versions.tf` or a committed `.terraform.lock.hcl` |
| Inputs | No `--inputs` file, a file readable by group or others, over 64 KiB, carrying private key text, naming another cloud, missing or malformed explicit identifiers, setting a coordinator variable, or requesting pool capacity above the bound |
| Credentials | The provider CLI's current identity is absent or resolves to another AWS account, Azure tenant/subscription or Google project number |
| Quota | Regional vCPU headroom (`ec2` quota `L-1216C47A`, `az vm list-usage` cores, the region's `CPUS` quota) is below the bound, or the EKS cluster quota is full in create mode |
| Cluster | An existing-mode cluster is unreadable or not `ACTIVE`, `Succeeded`/`Running` or `RUNNING` |

A missing cloud is a failure, never a skip; the review names every problem of
every cloud at once without account IDs.

### Inputs and stage interface

Each `--inputs CLOUD=/absolute/path` file is operator configuration that names
accounts but holds no credential:

```json
{"cloud": {"provider": "aws", "identifiers": {"account_id": "...", "region": "..."}},
 "cluster": {"...": "cluster/ stage variables"},
 "deployment": {"cluster": {"mode": "create", "name": "..."}, "pools": [{"capacity": 2}], "...": "deployment/ stage variables"}}
```

Identifiers follow the [hosting contract's three-cloud mapping](../../../docs/implementation/worker-hosting-contracts.md#three-cloud-mapping-and-compatibility):
AWS `account_id`/`region`; Azure `tenant_id`/`subscription_id`/`location`/`resource_group_name`;
Google `project_id`/`project_number`/`location`. The controller adds three
variables to every stage and refuses inputs that set them: `ownership`
(`owner`, `batch`, an RFC 3339 `expires_at` from the TTL bound, and the
`io.infraweave.lab.*` labels), `cloud`, and for `deployment/` the validated
`cluster_stage` output. Each stage root declares `backend "local" {}`; the
controller supplies an owner-private state path.

The `cluster/` stage outputs `cluster = {schema: 1, kind: "worker-cluster",
owner, batch, provider, id, name, endpoint, ca_certificate, owned, inventory}`.
`id` must be the provider ID of the explicit cluster (EKS ARN, AKS ARM ID or
`projects/<project>/locations/<location>/clusters/<name>`), `endpoint` a bare
HTTPS origin and `ca_certificate` its PEM trust anchor. The controller
completes a verified TLS handshake with that endpoint within the readiness
bound before the deployment stage runs. The `deployment/` stage outputs the
contract's `hosting` object; the controller requires its exact field set,
ownership and TTL, the inputs' cloud identifiers, the cluster stage's ID,
DNS-label names, `@sha256` artifacts, ULIDs on Existing/Public pools only,
capacity within the bound, owner-matching inventory and no private key text.
It then writes `workers.json` with each selected cloud exactly once and
validates it before any scenario. The Go harness reads it through
`INFRAWEAVE_LAB_WORKERS` and checks its owner and batch against the platform
manifest. Owned cloud resources carry `io.infraweave.lab.owner`; Google labels,
which admit no dots, carry `io_infraweave_lab_owner`.

### Parallel apply and cleanup

The platform deployment comes first, then up to `parallelism` clouds apply
`cluster/` and `deployment/` at once. Each stage has its own private
directory, `TF_DATA_DIR`, state, variables, plan and log under
`clouds/<cloud>/<stage>`, and Terraform runs without verbose logging. A
failing cloud lets the others finish their in-flight apply; interruption
terminates every owned Terraform process group first. The journal records
`applying` before each apply, so cleanup reads back a partially applied cloud.

Cleanup runs per cloud in parallel, independently of the platform and of every
other cloud's failure. It destroys `deployment/` before `cluster/` and never
destroys a cluster whose deployment failed to destroy. Before destroy it
refuses a state whose managed resource tags or recorded variables name another
owner. Absence then needs both empty state and cloud readback: an owner tag or
label search (`resourcegroupstaggingapi get-resources`, `az resource list` and
`az group list`, `gcloud asset search-all-resources`) and the exact owned
cluster lookup, retried within the cleanup bound. A denied, timed-out or
missing CLI is `unknown`, never absent. `cleanup.json` names every recorded
inventory entry with its outcome; borrowed entries are listed as `borrowed`.
Only verified absence removes plans, state backups and variables, so a
repeated `down` reads the empty state and the cloud again without them. A failed
cleanup keeps the journal for a retry from the same directory.

`evidence/` receives `review.json`, `cleanup.json`, `result.json` and, after a
failure, each stage's Terraform log, with private keys removed and account,
tenant, subscription and project identifiers replaced by their names. The
journal, `workers.json`, state, plans and inputs stay private.

### Credentials and CI

Locally the provider CLIs' own sign-in supplies credentials: `aws sso login`
or a short-lived profile, `az login`, `gcloud auth login` with
`gcloud auth application-default login` for Terraform. The identity must be
allowed to read quotas, the tagging/resource/asset search and the cluster
APIs, and to apply the roots.

`.github/workflows/managed-cloud-workers.yml` runs one batch on main when a
maintainer dispatches it, in the `workers-cloud` environment, whose required
reviewers approve the billable run and whose branch rule admits only main.
GitHub's OIDC token is federated to each cloud. The environment's secrets are
`WORKERS_AWS_ROLE`, `WORKERS_AWS_REGION`, `WORKERS_AZURE_CLIENT_ID`,
`WORKERS_AZURE_TENANT_ID`, `WORKERS_AZURE_SUBSCRIPTION_ID`,
`WORKERS_GOOGLE_WORKLOAD_IDENTITY_PROVIDER`, `WORKERS_GOOGLE_SERVICE_ACCOUNT`
and one `WORKERS_<CLOUD>_INPUTS` inputs file per cloud. The AWS role must
allow four-hour sessions. A selected cloud without its secrets fails
preflight. Only `evidence/` is uploaded.

### Scope

The required Linux lab job runs `test_cloudlab.py` with the real pinned
Terraform: fixture `cluster/` and `deployment/` roots built from
`terraform_data` go through actual init, plan, apply, output, destroy and
state readback, against a real TLS listener and recorded provider CLI answers.
They prove a passing three-cloud batch with redacted evidence and repeated
cleanup; one cloud's failed apply still cleaning all three; a hosting
manifest mismatch and an untrusted cluster API failing before any scenario;
a foreign journal refused before destroy; unknown or remaining cloud resources
never reported absent; interruption stopping owned Terraform; and an existing
directory never adopted. They are controller evidence, not cloud evidence.
The `test/terraform/cloud/workers` roots ship with their cloud deliveries;
until then every `workers-*` batch fails preflight. The scenarios prove the
attachment and the control plane's pool API beside it; worker execution on
the cloud pools needs a server endpoint those Pods can reach, which the
concurrent cloud proof supplies.
