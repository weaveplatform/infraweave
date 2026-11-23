# Worker pool delivery verification

Delivery gate: [#753](https://github.com/weaveplatform/infraweave/issues/753).
Epic: [#737](https://github.com/weaveplatform/infraweave/issues/737).
The [contract](worker-pool-contracts.md) specifies the behavior and its
[ownership matrix](worker-pool-contracts.json) assigns each requirement. The
[machine-readable delivery matrix](worker-pool-delivery.json) beside this audit
names every operation's executed scenarios, every requirement's evidence, the
capture gaps and the cleanup readback. Managed-cloud hosting and the final
shared-runtime rerun belong to [#759](https://github.com/weaveplatform/infraweave/issues/759);
nothing here claims cloud execution.

## Evidence boundary

Every child implementation of the epic is merged. The audit rests on three
executed sources, none of them a synthetic fixture:

| Source | Revision | Result |
| --- | --- | --- |
| Main quality gate [CI 45218707334](https://github.com/weaveplatform/infraweave/actions/runs/45218707334) | `e95e9ad3` (#810) | All 37 jobs passed: seven acceptance partitions with **1,208 scenarios / 28,606 steps**, native and capture suites, certified FIPS for every suite, the Terraform policy and Kubernetes batches and the coverage gate |
| Delivery pull request's quality gate, [#819](https://github.com/weaveplatform/infraweave/pull/819) | `739218ea`, [CI 45262242932](https://github.com/weaveplatform/infraweave/actions/runs/45262242932) | 37 of 38 jobs passed and one was skipped as not applicable: seven acceptance partitions with **1,215 scenarios / 28,808 steps**, the Kubernetes batch with **37 / 661** and empty cleanup, native and capture suites, certified FIPS, parity, and coverage at 95.2% total with every package at 90% or more |
| Local Terraform Kubernetes batch, certified FIPS | `e5feb44b`, clean tree | Passed: **37 scenarios / 661 steps**, the six ownership and injected-failure controls, and absence readback for every owner |

The local runs used native ARM64 Docker Desktop 29.6.1, official Terraform
1.17.2 and the pinned `rancher/k3s:v1.37.2-k3s1` digest. The pull request's
quality gate is the full gate on Linux: every acceptance partition, the native
and capture suites, certified FIPS, both Terraform batches and the coverage
gate. The local `make gate` was stopped during acceptance, after its
documentation, vet, lint, lab validation and unit stages passed, so that it
would not compete with that run; no count below comes from it. The focused
native suites and the four changed features also ran locally, as recorded in
the matrix.

Each report counts only scenarios whose every step passed. A scenario with no
steps, a skipped, pending or undefined step, or an empty selection fails the
report, the lab controller and the capability state check alike. No count
below includes one.

## Operation coverage

The audit reads every operation the three served documents declare for worker
pools: the Worker Pools tag, the account reads and default-image setting, the
space and stack attachable-pool reads, the three operator public-pool routes and
the twelve `/worker/v1` routes.

| Class | Operations | Evidence rule | Result |
| --- | --- | --- | --- |
| Account-scoped, authorized by feature and action | 30 | Passing `@case:success`, `@case:denial` (403 or the not-found answer that hides an unreadable pool) and `@case:failure` | All 30 complete |
| Declared stubs | `workerPoolSearchIntentProjects` and its suggestions | Answer 501 until intent projects exist (P25) | Not claimed; they keep `tag:Worker Pools` partial |
| Operator | `publicWorkerPoolCreate`, `publicWorkerPoolCredentials`, `publicWorkerPoolReset` | Operator credential; an account credential is the wrong audience and answers 401, not a role 403 | Success, credential refusal and failure all pass |
| Worker protocol | 12 `/worker/v1` routes | Pool certificate, worker session or run token; a credential of another kind, run or generation answers 401 | Success, credential refusal and failure evidenced for each; see the matrix |

The operation audit of main `e95e9ad3` found six account-scoped operations
without failure evidence: `publicWorkerPool`, `defaultPublicWorkerPoolRunnerImage`,
`outgoingIPAddresses`, `searchWorkerPoolsSuggestions`,
`workerPoolSearchUsersSuggestions` and
`workerPoolSearchSchedulableRunsSuggestions`. This delivery adds them through
the normal API against real PostgreSQL:

- `workers/pool_reads_unavailable.feature`: the public projection and default
  public runner image answer 503 while the server's own PostgreSQL is stopped,
  and the configured outgoing addresses answer 503 where the credential must be
  read from the store.
- `workers/pool_projections.feature`, *A private pool's suggestions refuse a
  predicate on a field their search does not filter*: 422 for an unknown field
  and for the ordering-only `position`, 404 for a pool that does not exist.

The worker protocol's `workerDeregister` had success evidence only. Its
revoked-session refusal was already asserted by the reset-session scenario,
which now names the operation, and
`workers/deregistration_unavailable.feature` adds its failure: a registered
worker's deregistration answers 503 while the server's own PostgreSQL is
stopped, the worker stays registered, and the same session deregisters it with
204 once PostgreSQL is back.

Without the outage scenario, `data-source:spacelift_ips` computes only `stub`.
Protocol routes keep their classification:
the run-scoped routes share the runner credential, whose refusal for a worker
session, another run and an account token `expressions/deploy.feature` asserts,
while fenced reports, uploads and artifacts answer 409/422 in
`policies/plan_reports.feature` and `vendors/sanitization_fence.feature`.

## Requirement matrix

Every requirement of the ownership matrix that this epic owns is delivered by
executed real-dependency evidence. The matrix names each requirement's
features and native `TestAcc` suites; the table summarizes them.

| Requirement | Executed proof |
| --- | --- |
| Public pool identity and credentials | `public_pool`, `public_registration`, `reset_session`; native public credential and generation-fence suites |
| Organization and account claim limits | `public_execution` (Free slot ceiling, resumed waiting work), `public_execution_admission` (stop and launcher loss release capacity); `TestAccPublicCapacityConcurrentAccountsAndOrganizations` claims concurrently across accounts and organizations |
| Public restrictions | `public_admission` (no priority, original pool after a stack move), resolved-image allowlist and registry lookalikes in `public_execution*`; native creation refusal before writes |
| Quota, default image, egress, queues, users, usage | `pool_projections`, `pool_queues`, `pool_reads_unavailable`; C292 replay; `TestAccWorkerPoolSamples` |
| Managed ownership, controller resources, capacity and finalizers | `hosting`, `kubernetes_controller`, `kubernetes_schema`, `kubernetes_chart`; native managed-ownership and binding suites |
| Portable preparation, private handoff, deadlines, Pod recovery and signals | `kubernetes_execution`, `kubernetes_phase_inputs`, `kubernetes_recovery`, `kubernetes_initialization` |
| Worker artifacts and chart | `kubernetes_chart` (two releases, upgrade, restart, drain on uninstall, refused values); the required `Worker artifacts` job |
| Worker initialization, isolated exec, signed admission | `policies/worker_initialization` (Docker), `kubernetes_initialization` (k3s), `runs/signed` |
| Availability | `kubernetes_recovery` reads a Kubernetes slot's `availableAt` absent while busy and set once idle; `protocol` and `pools` cover idle, drained, offline and gone; `TestAccWorkers`, `TestAccWorkerReap` |
| Phase-scoped inputs and vendor reporting | `phase_inputs` (Docker), `kubernetes_phase_inputs` (k3s) |
| Local Terraform lab | `kubernetes_lab` plus the two-owner and injected-failure controls below |

The concurrent-claim proof is native over real PostgreSQL: fifty public
workers poll at once across application replicas for three accounts' waiting
runs. With pooled organization grants and lower account ceilings exactly 1, 2
and 2 runs are assigned; with billing off every waiting run is assigned; no run
is assigned twice. A separate suite shows an owner at its full quota is skipped
without starving an eligible account. The tier gate is exercised in both
directions: a Bare tier is refused every pool projection with `feature-not-in-tier`, Starter and Free are refused
private pools, Business is admitted and its aggregate three private workers stop
the fourth registration with `LimitReached`.

### Existing behavior reused

Three behaviors predate the epic and are reused, not rebuilt. `available_at` is
the workers domain's P8 column, set when a worker becomes idle and cleared while
it is busy; the epic adds only its Kubernetes projection, which the recovery
scenario now asserts. The `worker-pool-controller` system role has held exactly
`SPACE_READ` and pool create/update/delete since the access domain's seeding;
the controller uses it unchanged and `kubernetes_controller` proves its refusals.
Claim ordering and exactly-once claim are WRK-06's
([#364](https://github.com/weaveplatform/infraweave/issues/364)); the public
claim adds the organization and account quota lock in the same transaction.

## Capability register

The register's Worker Pools records claimed `missing` although their operations
were served. They now record what the merged acceptance results compute:

| Record | Recorded state |
| --- | --- |
| `tag:Worker Pools` | `partial`: every served operation is complete; the two intent-project searches are 501 stubs |
| `resource:spacelift_worker_pool`, `resource:spacelift_worker_pool_recycle`, `resource:spacelift_default_runner_image`, `data-source:spacelift_worker_pool`, `data-source:spacelift_worker_pools`, `data-source:spacelift_ips` | `partial`: the API is complete; the native provider remains P24 |
| `tag:Account` | Still `partial` (`spaceliftAwsAccountId`); its notes now name the two served P14 reads |

`specref parity --states --check` passes against the merged partition reports.

## Kubernetes batch

The Terraform-owned batch ran the eight `@kubernetes-lab` features against one
owned k3s cluster, real PostgreSQL 18.7, RustFS and TLS Gitea, two application
replicas and replicated `infraweave worker-controller` processes, all built from
the tested revision under certified FIPS with OPA v1.22.4:

| Feature | Scenarios | Steps |
| --- | ---: | ---: |
| `kubernetes_chart` | 12 | 117 |
| `kubernetes_controller` | 4 | 102 |
| `kubernetes_execution` | 3 | 109 |
| `kubernetes_initialization` | 3 | 80 |
| `kubernetes_lab` | 3 | 33 |
| `kubernetes_phase_inputs` | 2 | 64 |
| `kubernetes_recovery` | 4 | 138 |
| `kubernetes_schema` | 6 | 18 |
| **Total** | **37** | **661** |

Multiple pools share the cluster: `kubernetes_controller` converges two Managed
pools side by side and `kubernetes_chart` runs two releases' pools with real
work at once. The two added availability steps are the only change from main's
37 scenarios / 659 steps.

The first two local attempts at the same revision ran every scenario
concurrently on the six-CPU host against the two-CPU cluster. Each failed
`kubernetes_initialization`'s signed-token scenario because the second helper
Pod's container started after the policy's 45-second deadline; the second
attempt also timed out `kubernetes_recovery`'s adoption scenario. The batch
then passed with CI's settings, `GOMAXPROCS=2` and `GOFLAGS=-p=1`. Neither
failed attempt is counted as evidence. Each still destroyed its owned
infrastructure and verified the readback, which is the failed-attempt cleanup
evidence below.

### Lab concurrency

[#814](https://github.com/weaveplatform/infraweave/issues/814) traced those
failures to the batch, not the product. The 45 seconds is the scenario's own
`initialization_timeout`, inside the product's 60-second limit, and the contract
counts a helper Pod's start inside the call's deadline. The batch ran as many
scenarios at once as the host's `GOMAXPROCS`, while every scenario's run Pods,
helper Pods and attach streams share the cluster's one two-CPU node. A clean
batch at `af723bf8` on a ten-CPU host, ten scenarios at once, failed 10 of 37
scenarios: the three initialization scenarios' helper bootstraps were still
running when the 45-second deadline ended, readiness probes and API reads timed
out, and execution, phase-input, recovery and chart scenarios timed out too.

The `kubernetes-lab` partition now runs at most one scenario per CPU of the
node container's Docker limit, whatever the host's `GOMAXPROCS`. Before a
scenario's worker controllers start, the suite pulls its runner image into the
node, so no helper Pod's first pull queues behind the kubelet's serialized
pulls. The deadline-refusal scenarios are unchanged. Two consecutive batches at
`92bb7500` under certified FIPS, with the host's default `GOMAXPROCS` (ten) and
another project's acceptance suite on the same Docker host, each ran two
scenarios at once and passed 37/37 scenarios and 661/661 steps. Their
signed-token scenarios' two helper commands started within 5 and 11 seconds of
the first bootstrap, and every helper found its runner image already on the
node:

| Run | Owner | Result | Before destroy | After |
| --- | --- | --- | --- | --- |
| Interrupted while building, not evidence | `iw-lab-a07187ae…` | no scenario ran | 4 containers, 4 volumes, 1 network | none; empty state |
| Baseline, `af723bf8` | `iw-lab-8fe0dd36…` | 27/37 scenarios | 4 containers, 4 volumes, 1 network | none; empty state |
| Bounded batch 1, `92bb7500` | `iw-lab-c099e047…` | 37/37, 661/661 steps | 4 containers, 4 volumes, 1 network | none; empty state |
| Bounded batch 2, `92bb7500` | `iw-lab-a54fb1e9…` | 37/37, 661/661 steps | 4 containers, 4 volumes, 1 network | none; empty state |

## Capture provenance and open gaps

No new vendor capture was taken: each open question needs an entitled private
account and a separately approved controller or worker lab. The six questions
keep their recorded status in the [reference observations](worker-pool-reference-observations.md):

| Question | Status | What infraweave proves instead |
| --- | --- | --- |
| C21 | Unavailable | infraweave's own controller traffic, ownership and registration, not the vendor controller's |
| C66 | Unavailable | The chosen `ro_`/`wo_`/`all` phase rules on Docker and k3s; not parity |
| C67 | Unavailable | infraweave's issued preparing deadline and Pod `activeDeadlineSeconds` cap |
| C68 | Unavailable | infraweave's version 1 initialization input with authored V0/V1 programs |
| C69 | Unavailable | Vendor and runtime labels for the implemented Terraform, OpenTofu and Terragrunt drivers only |
| C292 | Partial | The public queue, used-by and suggestion exchanges replay; private reprioritization stays unobserved |

None is described as a live capture or resolved. The C292 replay asserts only
what its nine exchanges recorded.

## Cleanup

Every owner the local runs created is absent, and `docker` lists no container,
volume or network carrying the `io.infraweave.lab.owner` label after them:

| Run | Owner | Before destroy | After |
| --- | --- | --- | --- |
| Ownership and failure controls, owner A | `iw-lab-aeb5a45b…` | 4 containers, 4 volumes, 1 network | none; empty state |
| Ownership and failure controls, owner B | `iw-lab-2ba1b9ad…` | nothing left at its final readback | none; empty state |
| Batch attempt 1 (failed scenario) | `iw-lab-b3180829…` | 4 containers, 4 volumes, 1 network | none; empty state |
| Batch attempt 2 (failed scenarios) | `iw-lab-8a8e0379…` | 4 containers, 4 volumes, 1 network | none; empty state |
| Passing batch | `iw-lab-e416b577…` | 4 containers, 4 volumes, 1 network | none; empty state |

The controls injected a foreign manifest, a foreign cleanup journal and a
paused cluster; each was refused without a replacement, and destroying one
owner left the other's cluster and services intact until its own destroy. Every
private key, plan and kubeconfig was removed after the absence readback. Run and
helper Pods live inside the owned cluster, so its destroy removes them; the
scenarios themselves first assert each run's Pods and helper Pods are gone.
Ordinary acceptance's testcontainers are reaped when each test process exits.
After the focused runs and the stopped local gate, no running container carries
a testcontainers session label; the exited ones Docker still lists belong to
sessions from before this delivery's runs.

## Remaining boundaries

- EKS, AKS and GKE hosting ([#755](https://github.com/weaveplatform/infraweave/issues/755),
  [#756](https://github.com/weaveplatform/infraweave/issues/756),
  [#757](https://github.com/weaveplatform/infraweave/issues/757)) and the
  concurrent three-cloud proof with the final shared-runtime rerun
  ([#759](https://github.com/weaveplatform/infraweave/issues/759)) stay open;
  the matrix keeps their requirements `unproven`.
- The epic stays open until the maintainer closes it; child closure and this
  audit do not close it.
- The two intent-project searches answer 501 until intent projects exist (P25).
