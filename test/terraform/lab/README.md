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
| `kubernetes` | Required `kubernetes-lab` partition: real owned k3s API/node/namespace plus current worker-pool API success/denial/failure; controller/executor delivery remains separate |
| `policies` | `@policies` across partitions: real Rego V0/V1, approvals, warnings, flags, isolation and limits |
| `core`, `terraform-plans`, `terraform-vcs`, `terraform`, `runs`, `integrations` | The named existing Cucumber partition |
| `vcs` | The existing real-provider partition, narrowed with `--tags '@lab:github'` and `--features features/vcs/feedback.feature` |

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
CSR refusal. They do not claim a Kubernetes worker controller/executor, managed
cloud deployment or signed-run admission, which are not implemented by this lab.
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
