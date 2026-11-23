# Worker-pool reference observations

Issue: [#739](https://github.com/weaveplatform/infraweave/issues/739).
Source baseline: `41ab59b5b417ff3d0f9767642b2db0620706640e`.
The [machine-readable inventory](worker-pool-reference-observations.json) records
capture time, schema fingerprint, classification, missing evidence and owners.

## Actual observation boundary

A logged-in Chrome DevTools MCP session inspected the pool overview, private-pool
creation form, public queue, public used-by page and first stack-wizard step.
The [partial C292 fixture](../../test/fixtures/captures/C292/README.md) contains
nine completed HTTP 200 GraphQL exchanges: eight actual UI-triggered queries and
one explicitly authored read-only inventory query. Its five text snapshots are
actual accessibility trees, not reconstructed screens. The private raw capture
also retained the standard introspection response; only its fingerprint is
committed. The fingerprint hashes compact `JSON.stringify` UTF-8 of that decoded
response, not the committed reference snapshot's file bytes.

Observed on V5_FREE:

- Public parallelism is 1; busy workers, schedulable runs, queued runs and users
  are zero. Private pool searches, stacks and root-space attachable pools are
  empty. These are observations of this account, not new catalogue defaults.
- The overview warns that private-worker jobs will not be scheduled on this
  plan. It still exposes the create form; opening it proves neither successful
  creation nor execution.
- The form exposes name, CSR upload, space, description, drift-run limits and
  labels. It exposes no cloud-provider selector. This alone does not prove how
  workers are hosted or whether independent clouds can coexist.
- Account public/private runner-image defaults are both the empty string. This
  does not identify the actual resolved vendor image. Public egress addresses
  are retained as returned; they are not infraweave defaults.
- The queue suggestions expose priority-related filters and enum values. No run
  was reprioritized, and there is no before/after private queue to compare.
- The stack wizard's later steps are disabled. Root space is visible, and the
  inventory confirms no attachable private pools; a populated worker selector
  and assignment are unobserved.

No pools, stacks, keys, policies, workers or runs were created, updated or deleted.
No infrastructure was deployed. The final read-only inventory contains no
private pools or stacks; there was no test-resource cleanup operation to perform.
Browser cookies, authentication headers, telemetry and unrelated account traffic
are omitted. The account name/subdomain is consistently replaced by `example`.
GraphQL field names, values, empty arrays, nulls, timestamps and default strings
are preserved. A reference entitlement warning is not an infraweave 403 scenario.

## View-to-operation mapping

Routes below are relative to `/api/v1`. `Declared` means an operation exists in
the served schema; it does not mean it is implemented or has acceptance evidence.

| View or action | Reference observation | infraweave contract at the source baseline | Delivery owner |
|---|---|---|---|
| Pool list/counts | SearchWorkerPools and suggestions; private list empty, separate public row | `workerPools`: `GET /worker-pools`; search/suggestion routes already declared. Private management exists; public counts and usage still need integration. | #744 |
| Public capacity/counts | GetPublicWorkerPoolDetails and read-only inventory | `publicWorkerPool`: `GET /public-worker-pool`, declared stub. | #742, #743, #744 |
| Public queue | SearchPublicWorkerPoolSchedulableRuns and suggestions; empty queue | `workerPoolSearchSchedulableRuns`: `GET /worker-pools/{workerPoolId}/search-schedulable-runs`, with corresponding suggestions route. Public selector/routing must be settled; the upstream `public-worker-pool` string is not proof of an accepted infraweave identifier. | #740, #744 |
| Used by | SearchPublicWorkerPoolUsedBy and suggestions; empty users | `workerPoolSearchUsers`: `GET /worker-pools/{workerPoolId}/search-users`, with corresponding suggestions route. Public projection still needs implementation. | #744 |
| Private pool creation/configuration | Actual unsubmitted form and private-tier execution warning | `workerPoolCreate`: `POST /worker-pools`; existing private API/CSR contract. No creation was attempted. Controller ownership is separate. | #740, #745 |
| Stack pool selection | First wizard step only; later steps disabled; root attachable list empty | `attachableWorkerPoolsInSpace`: `GET /attachable-worker-pools-in-space`; `stackAttachableWorkerPools`: `GET /stacks/{stackId}/attachable-worker-pools`; stack create/update carries its selection. Assignment was not tested here. | #740, #745, #746 |
| Account runner defaults | Explicit read-only inventory returns two empty strings | `accountUpdateDefaultWorkerPoolRunnerImages` already declares public/private settings. There is no standalone `defaultPublicWorkerPoolRunnerImage` operation or matching account read field in the served schema at this revision; the read mapping must be defined. | #740, #744 |
| Public egress | Explicit inventory returns address list | `outgoingIPAddresses` has no served-schema route at this revision. Define its public contract rather than claiming a stub already exists. | #740, #744 |
| Worker usage | Not opened or executed | `workerUsage`: `GET /worker-usage`, declared; no live usage-window comparison was captured. | #744, #753 |

The plan's statement that every public-pool operation already routes to a stub
is therefore too broad: the defaults and egress mappings above are missing.
This is source reconciliation within #740, not a reason to invent successful
public reads in #739 or add an unrelated console-management API.

## Evidence questions remain open

| Question | Status and attempted boundary | Missing evidence | Owners |
|---|---|---|---|
| C21 | Unavailable: pool inventory contains no private pool or vendor controller; private execution is not entitled. Running a controller would create/manage resources outside this read-only boundary. | Actual v0.0.47 controller traffic and auto-registration effects. | #740, #745 |
| C66 | Unavailable: no private worker, stack or run exists; no run was created. | Actual read/apply `ro_`/`wo_` environment and extra-mount visibility and writes. | #740, #752 |
| C67 | Unavailable: no real vendor-created Kubernetes pod exists. | Effective pod deadline compared with preparation/run timeout. Documentation and CRD descriptions are not a pod observation. | #740, #746, #747 |
| C68 | Unavailable: no worker-side initialization invocation exists. | Full real input and sampled deny, including the distinction between user metadata signatures and server run credentials. | #740, #749, #751 |
| C69 | Unavailable: no supported-vendor execution or observability result exists. | Actual vendor-by-vendor worker trace/metric behavior. | #740, #752 |
| C292 | Partial: public empty queue/filter/used-by observations are preserved. | Private prioritize control, mutation, queue movement and assignment ordering. | #740, #743, #744, #753 |

All six records retain their open status in the question register. Their
capture procedures link here; none receives `status: resolved`. The historical
approval of inferred policy cases is not blanket evidence for these workers.
#740 must distinguish reviewed infraweave contracts from unresolved reference
compatibility. Managed-cloud execution is not proven by these observations.

Supporting primary sources remain useful without changing that classification:

- [Worker documentation](https://docs.spacelift.io/concepts/worker-pools) and
  [Kubernetes worker documentation](https://docs.spacelift.io/concepts/worker-pools/kubernetes-workers)
  describe their model; they do not constitute an execution capture.
- The [pinned CRD](https://github.com/spacelift-io/spacelift-helm-charts/blob/4e02e1d22e73c81ea863d982c259ec7e3572a285/spacelift-workerpool-controller/config/crd/bases/workers.spacelift.io_workerpools.yaml#L126)
  describes 81 minutes 20 seconds for the pod deadline, while report 10 also
  records 70-minute documentation. The description is not a YAML `default:`
  value and neither establishes the real controller-created pod's value.
- The [pinned signed-run example](https://github.com/spacelift-io/signed-run-reference-implementation/blob/379a7b30516e/initialization-policy.rego)
  demonstrates selected input fields, not the complete input document. Its
  user-provided metadata signature must not be called a captured server run JWT.

## Reproduce without mutation

1. Use an authorized logged-in test account and Chrome DevTools MCP. Record the
   current tier, capture time and account schema fingerprint using the repository
   standard `internal/devtools/specref/introspection.graphql` query.
2. Navigate to the pool overview, open and cancel the create form without
   submitting, and inspect the public queue and used-by tabs. Wait for actual
   empty-state/table responses; do not preserve a loading skeleton as a result.
3. Inspect the first new-stack wizard step without creating a stack. If later
   selection steps are inaccessible, record that limit instead of filling
   prerequisites or creating resources silently.
4. Preserve completed pool-related UI request bodies and responses. The
   `request.graphql` file includes the separately identified read-only inventory
   query; execute it only as a query, never as a mutation.
5. Omit authentication/telemetry, consistently redact account identity and save
   snapshots plus exact field values. Compare the final resource inventory.
6. Run the existing capture loader, redaction and recorded-observation checks.
   `TestAccCaptures` uses real PostgreSQL and replays C292's public
   observations as the REST subset `worker-pool-public-projections`.

The subset classifies parallelism and the runner-image defaults as deviations,
and the tier and stack inventory as upstream, while private priority stays
missing. #753 audits API and local worker proof, and #759 owns concurrent EKS/AKS/GKE plus
native-provider regression and cloud teardown. No passing documentation check
alone proves any of those downstream behaviors.
