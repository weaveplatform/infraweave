# 18: Scheduling, drift detection and dependencies

Checked against: docs.spacelift.io read 2026-10-28 (SaaS pages, and self-hosted `latest`, which is v7.1.0, where noted); introspection `b3255fe38f5e`; terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); spacectl `6bf167cb62a6`; the REST projection `spacelift.openapi.yaml` at the same pin.

Research baseline: **2026-10-28**.

This report records how Spacelift starts work on a stack without a VCS event or a
person: scheduled runs, scheduled tasks and scheduled deletes, with their cron and
timezone rules; drift detection, which runs proposed runs on a schedule and can
reconcile with tracked runs; stack dependencies and output references, which order
tracked runs across stacks and pass outputs downstream; force apply
(`ForceApplyMode`); external dependencies, which hold a run until something outside
Spacelift reports back; and where trigger policies fit beside these. infraweave's
scheduler, its run queue and its dependency resolver are designed from it. Run states
and phases are report 08; policy types, rules and inputs are report 09; worker pools
and queue order are report 10; stack settings are report 07; contexts and environment
precedence are report 13; managed state is report 16; webhooks, audit and the Datadog
tags are report 19; the resources view that shows drifted entities is report 21;
schedules and dependencies declared inside templates are report 17. The Terraform
resources are listed in [05-terraform-provider.md](05-terraform-provider.md).

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` ([`introspection.json`](../../../api/reference/spacelift/introspection.json)) |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog, <https://docs.spacelift.io/product/changelog> |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

Short names for the docs pages cited below:

| Short name | URL |
|---|---|
| scheduling | <https://docs.spacelift.io/concepts/stack/scheduling> |
| drift | <https://docs.spacelift.io/concepts/stack/drift-detection> |
| deps | <https://docs.spacelift.io/concepts/stack/stack-dependencies> |
| ext-deps | <https://docs.spacelift.io/concepts/policy/push-policy/run-external-dependencies> |
| trigger | <https://docs.spacelift.io/concepts/policy/trigger-policy> |
| run | <https://docs.spacelift.io/concepts/run> |
| tracked | <https://docs.spacelift.io/concepts/run/tracked> |
| environment | <https://docs.spacelift.io/concepts/configuration/environment> |
| rbac | <https://docs.spacelift.io/concepts/authorization/rbac-system> |
| worker-pools | <https://docs.spacelift.io/concepts/worker-pools> |
| k8s-workers | <https://docs.spacelift.io/concepts/worker-pools/kubernetes-workers> |
| cloudformation | <https://docs.spacelift.io/vendors/cloudformation> |
| template-config | <https://docs.spacelift.io/concepts/template/configuration> |

The schema snapshot omits `isDeprecated` on input fields: no input field in
`introspection.json` carries the key [G]. Input-field deprecations below therefore come
from the changelog [RN], and output-field deprecations from the schema [G].

## 1. Overview

| Feature | What it starts | Trigger | Run type | Tier | Worker requirement |
|---|---|---|---|---|---|
| Scheduled run | A tracked run, optionally with a custom runtime config | Cron list or one Unix timestamp | `TRACKED` | Business and above [D] scheduling | Private workers; schedules on public workers are stored but never fire [D] scheduling |
| Scheduled task | A task running one command | Cron list or one Unix timestamp | `TASK` | Business and above [D] scheduling | Private workers [D] scheduling |
| Scheduled delete | Stack deletion, optionally after a destroy run | One Unix timestamp | `DESTROY` when resources are deleted | Business and above [D] scheduling | Private workers [D] scheduling |
| Drift detection | A proposed run; on drift, optionally a tracked reconciliation run | Cron list | `PROPOSED` then `TRACKED` | Starter+ and above [D] drift | Private workers only [D] drift |
| Stack dependency | Tracked runs on downstream stacks after an upstream tracked run | Upstream tracked run creation and completion | `TRACKED` | none stated [D] deps | none stated |
| External dependency | Nothing; it holds a run in `QUEUED` | Push policy `external_dependency` rule | any run created by the push policy | none stated [D] ext-deps | none stated |

The self-hosted v7.1.0 pages for scheduling, drift detection and external
dependencies match the SaaS pages except that they drop the plan notices; the
self-hosted stack dependency page adds one note on CloudFormation-deployed worker pools
(section 4.5) [D] (`https://docs.spacelift.io/self-hosted/latest/concepts/stack/stack-dependencies`).

## 2. Scheduled runs, tasks and deletes

### 2.1 Objects

`ScheduledRun` [G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Schedule identifier, unique globally |
| `name` | `String!` | Name of the schedule |
| `timezone` | `String` | Timezone the cron expressions are read in |
| `cronSchedule` | `[String!]` | Cron expressions; this or `timestampSchedule` must be set |
| `timestampSchedule` | `Float` | One Unix timestamp; this or `cronSchedule` must be set |
| `nextSchedule` | `Int` | Unix time of the next firing; null when done or in progress |
| `customRuntimeConfig` | `RuntimeConfig` | The runtime configuration the run uses |

`ScheduledRun` has no `stack` field; the other two schedule types do [G].

`ScheduledTask` [G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Schedule identifier |
| `stack` | `ID!` | Stack the schedule runs against |
| `command` | `String!` | Command run as a task |
| `timezone` | `String` | Timezone for the cron expressions |
| `cronSchedule` | `[String!]` | Cron expressions; this or `timestampSchedule` must be set |
| `timestampSchedule` | `Float` | One Unix timestamp |
| `nextSchedule` | `Int` | Next firing; null when done or in progress |

`ScheduledDelete` [G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Schedule identifier |
| `stack` | `ID!` | Stack to delete |
| `shouldDeleteResources` | `Boolean!` | Run a destroy before deleting the stack |
| `timezone` | `String` | Present on the object; deletes take no timezone (section 2.3) |
| `cronSchedule` | `[String!]` | Present on the object; deletes take no cron (section 2.3) |
| `timestampSchedule` | `Float` | When to delete |
| `nextSchedule` | `Int` | Next firing; null when done or in progress |

Each `nextSchedule` description in the schema speaks of a "detection run", text copied
from the drift detection type; it means the next firing of that schedule [I: the
description is identical on all four types].

### 2.2 Inputs

| Input | Field | Type | Constraint |
|---|---|---|---|
| `ScheduledRunInput` | `name` | `String!` | Required [G] |
| | `cronSchedule` | `[String!]` | One of `cronSchedule` or `timestampSchedule` [G] |
| | `timestampSchedule` | `Int` | Unix seconds [G] |
| | `timezone` | `String` | Applies to the cron expressions [G] |
| | `runtimeConfig` | `RuntimeConfigInput` | Either `yaml` or the individual fields, not both [G] |
| `ScheduledTaskInput` | `command` | `String!` | Required [G] |
| | `cronSchedule`, `timestampSchedule`, `timezone` | as above | as above [G] |
| `ScheduledDeleteInput` | `shouldDeleteResources` | `Boolean!` | Required [G] |
| | `timestampSchedule` | `Int` | The delete time [G] |
| | `cronSchedule` | `[String!]` | Deprecated: deletes do not support cron [RN] (2026-07-16) |
| | `timezone` | `String` | Deprecated: deletes do not support a timezone [RN] (2026-07-16) |

`RuntimeConfigInput` carries `yaml`, `environment` (`[EnvVarInput!]`), `projectRoot`,
`runnerImage`, the eleven hook lists (`beforeInit` to `afterRun`) and
`gitSparseCheckoutPaths` [G]. The runtime YAML format is report 07; a scheduled run's
custom runtime config is the same document a manual tracked run can take [D] scheduling
(`https://docs.spacelift.io/concepts/run/tracked#triggering-runs-with-a-custom-runtime-config`).

Typing traps:
- `timestampSchedule` is `Int` on inputs and `Float` on the objects [G].
- `ScheduledRunInput.name` is required by the schema [G], but the UI marks the name
  optional [D] scheduling, and the provider's `name` is optional and is sent only when
  set [S]
  ([`resource_scheduled_run.go#L247-L250`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_scheduled_run.go#L247-L250)).
  What the API does with an empty name is *(unverified)*.

### 2.3 Operations

| GraphQL | Kind | Arguments | REST projection | Action |
|---|---|---|---|---|
| `stackScheduledRunCreate` | M | `stack`, `input: ScheduledRunInput!` | `POST /stacks/{stackId}/scheduled-runs` | `STACK_SCHEDULED_RUN_CREATE` |
| `stackScheduledRunUpdate` | M | `stack`, `scheduledRun`, `input` | `PUT /stacks/{stackId}/scheduled-runs/{scheduledRunId}` | `STACK_SCHEDULED_RUN_UPDATE` |
| `stackScheduledRunDelete` | M | `stack`, `scheduledRun` | `DELETE /stacks/{stackId}/scheduled-runs/{scheduledRunId}` | `STACK_SCHEDULED_RUN_DELETE` |
| `stackScheduledTaskCreate` | M | `stack`, `input: ScheduledTaskInput!` | `POST /stacks/{stackId}/scheduled-tasks` | `STACK_SCHEDULED_TASK_CREATE` |
| `stackScheduledTaskUpdate` | M | `stack`, `scheduledTask`, `input` | `PUT /stacks/{stackId}/scheduled-tasks/{scheduledTaskId}` | `STACK_SCHEDULED_TASK_UPDATE` |
| `stackScheduledTaskDelete` | M | `stack`, `scheduledTask` | `DELETE /stacks/{stackId}/scheduled-tasks/{scheduledTaskId}` | `STACK_SCHEDULED_TASK_DELETE` |
| `stackScheduledDeleteCreate` | M | `stack`, `input: ScheduledDeleteInput!` | `POST /stacks/{stackId}/scheduled-deletes` | `STACK_SCHEDULED_DELETE_CREATE` |
| `stackScheduledDeleteUpdate` | M | `stack`, `scheduledDelete`, `input` | `PUT /stacks/{stackId}/scheduled-deletes/{scheduledDeleteId}` | `STACK_SCHEDULED_DELETE_UPDATE` |
| `stackScheduledDeleteDelete` | M | `stack`, `scheduledDelete` | `DELETE /stacks/{stackId}/scheduled-deletes/{scheduledDeleteId}` | `STACK_SCHEDULED_DELETE_DELETE` |
| `Stack.scheduledRuns`, `Stack.scheduledRun(id)` | field | | `GET /stacks/{stackId}/scheduled-run` | |
| `Stack.scheduledTasks`, `Stack.scheduledTask(id)` | field | | `GET /stacks/{stackId}/scheduled-task` | |
| `Stack.scheduledDeletes`, `Stack.scheduledDelete(id)` | field | | `GET /stacks/{stackId}/scheduled-delete` | |

Operation names, arguments and return types are [G]; the REST paths are the projection in
[`spacelift.openapi.yaml`](../../../api/reference/spacelift/spacelift.openapi.yaml); the
action names are `Action` enum values [G]. Deletes return the nullable object; creates
and updates return it non-null [G].

### 2.4 Permissions

The `Action` enum has nine scheduling actions, `STACK_SCHEDULED_{RUN,TASK,DELETE}_{CREATE,UPDATE,DELETE}` [G].
`Stack.allowedActions` returns the viewer's grants on the stack, including these, so a
client can gate the scheduling controls [G]. The RBAC page lists eight scheduling
actions, each with an Admin legacy fallback, and has no row for updating a scheduled
delete [D] rbac. The schema wins: `STACK_SCHEDULED_DELETE_UPDATE` exists [G]. The role
editor gained these permissions in July 2026 [RN] (2026-07-10).

### 2.5 Behaviour

- **Fire condition.** Schedules fire only for stacks on private worker pools; a stack on
  the public pool can hold schedules that never fire until it moves [D] scheduling.
- **Timestamp or cron.** Each run or task schedule is either one timestamp or a list of
  cron expressions [G]. The provider enforces this with `at` conflicting with `every`
  and `timezone`, and errors when neither is set [S]
  ([`resource_scheduled_run.go#L51-L71`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_scheduled_run.go#L51-L71),
  [`#L254-L267`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_scheduled_run.go#L254-L267);
  [`resource_scheduled_task.go#L59-L80`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_scheduled_task.go#L59-L80)).
- **Scheduled run.** Creates a tracked run at each firing, with the schedule's custom
  runtime config when one is attached [D] scheduling. It follows the normal tracked run
  rules (report 08) [I: it is a `TRACKED` run created by `stackScheduledRunCreate`, which
  report 08 lists as a tracked run source].
- **Scheduled task.** Runs the command as a task at each firing [D] scheduling; the task
  model (`sh -c`, no concurrency with tracked runs) is report 08.
- **Scheduled delete.** At the timestamp, either deletes the stack outright, or triggers a
  destroy run and deletes the stack only once that run succeeds [D] scheduling. The UI
  takes the time in the browser's timezone or UTC [D] scheduling; the API takes Unix
  seconds [G]. What happens when the destroy run fails, or when the stack has downstream
  dependants (which block deletion, section 4.9), is *(unverified)*.
- **One-shot completion.** `nextSchedule` is null once a timestamp schedule has fired
  [G]. Whether the fired schedule object is then removed or kept is *(unverified)*.
- **Run trigger attribution.** `TF_VAR_spacelift_run_trigger` lists a schedule
  identifier among its example values [D] environment; the exact format is
  *(unverified)*.
- **Audit.** `AuditTrailResourceType` has `SCHEDULED_RUN`, `SCHEDULED_TASK` and
  `SCHEDULED_DELETE`, with metadata `{id, stackId}` [G]; report 19 owns the audit trail.

### 2.6 Cron and timezone

| Rule | Value | Source |
|---|---|---|
| Expressions per schedule | A list; each expression adds firing times | `[String!]` [G]; the drift detection page allows several cron rules [D] drift |
| Field count | Five: minute, hour, day of month, month, day of week | The template schema's `cron_schedule` pattern requires exactly five whitespace-separated fields [D] template-config; the provider examples use five fields (`0 7 * * 1-5`, `*/15 * * * *`) [S] ([examples/resources/spacelift_scheduled_run/resource.tf](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/examples/resources/spacelift_scheduled_run/resource.tf)) |
| Field syntax | `*`, a number, `a-b`, `a/b`, `*/b`, or a comma list of numbers | The template schema pattern [D] template-config. Names (`MON`, `JAN`), `?`, `L`, `#` and macros such as `@daily` do not match it. Whether the API itself rejects them is *(unverified)* |
| Timezone default | `UTC` | `DriftDetectionIntegrationInput.timezone` says so [G]; every provider resource defaults `timezone` to `UTC` [S] ([`resource_drift_detection.go#L58-L63`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_drift_detection.go#L58-L63)). The API default for run and task schedules is *(unverified)* |
| Timezone names | IANA names; the provider example uses `CET` | [S] (provider examples). The accepted list is *(unverified)* |
| Timezone with a timestamp | Not allowed by the provider | `timezone` conflicts with `at` [S] |
| Minimum interval | none documented | The provider example fires drift detection every 15 minutes [S] ([examples/resources/spacelift_drift_detection/resource.tf](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/examples/resources/spacelift_drift_detection/resource.tf)); any lower bound is *(unverified)* |
| Daylight saving | not documented | *(unverified)* |
| Missed firings (worker down, stack locked) | not documented | *(unverified)* |

The template JSON schema (draft-07) puts `maxLength: 1` beside the `$ref` on each drift
detection `cron` item and `minLength: 1` on each task `cron` item [D] template-config.
Draft-07 ignores keywords that sit beside `$ref`, and a `maxLength` of 1 would reject
every five-field expression, so neither keyword constrains anything as written [I: JSON
Schema draft-07 `$ref` semantics]. Its task schedule takes either `cron` or
`timestamp_unix` (minimum `1600000000`) [D] template-config. Report 17 owns templates.

The pattern has no combined range-with-step (`1-5/2`) and no comma list of ranges
(`1-3,5`) [D] template-config.

### 2.7 Terraform resources

| Resource | Attributes | ID format | Source |
|---|---|---|---|
| `spacelift_scheduled_run` | `stack_id` (required, forces new), `name`, `at`, `every`, `timezone` (default `UTC`), `runtime_config` block (max 1; the `RuntimeConfigInput` fields plus computed `terraform_version` and `terraform_workflow_tool`), computed `schedule_id`, `next_schedule` | `<stack_id>/<schedule_id>` | [S] [`resource_scheduled_run.go#L107`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_scheduled_run.go#L107) |
| `spacelift_scheduled_task` | `stack_id` (required, forces new), `command` (required), `at`, `every`, `timezone` (default `UTC`), computed `schedule_id` | `<stack_id>/<schedule_id>` | [S] [`resource_scheduled_task.go#L120`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_scheduled_task.go#L120) |
| `spacelift_scheduled_delete_stack` | `stack_id` (required, forces new), `at` (required), `delete_resources` (default `true`), computed `schedule_id` | `<stack_id>/<schedule_id>` | [S] [`resource_scheduled_delete_stack.go#L45-L56`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_scheduled_delete_stack.go#L45-L56), [`#L81`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_scheduled_delete_stack.go#L81) |

Each has a data source of the same name keyed by `<stack_id>/<schedule_id>` [S]
([`terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json)). The
provider default for `delete_resources` is `true`, while the API field
`shouldDeleteResources` is required with no default [G].

## 3. Drift detection

### 3.1 What it does

Drift is a difference between the declared and the real infrastructure, from changes
made outside the tool or from external data sources that change [D] drift. Spacelift
detects it by running proposed runs on a schedule against stacks in a stable state,
normally `FINISHED`, and checking whether they report changes [D] drift. The runs carry
`Run.driftDetection = true` [G] and `drift_detection: true` in policy input [D] drift.

### 3.2 Configuration

One drift detection configuration per stack, held under `Stack.integrations.driftDetection` [G].

`DriftDetectionIntegration` [G]:

| Field | Type | Meaning |
|---|---|---|
| `schedule` | `[String!]!` | Cron expressions (section 2.6) |
| `timezone` | `String!` | Timezone of the expressions |
| `reconcile` | `Boolean!` | Start a tracked reconciliation run when drift is found |
| `ignoreState` | `Boolean!` | Run detection whatever the stack state; when false, only on stacks in `FINISHED` |
| `nextSchedule` | `Int` | Next detection; null while detection or reconciliation is in progress |
| `deleted` | `Boolean!` | Set on the object a delete returns |

`DriftDetectionIntegrationInput` [G]:

| Field | Type | Default |
|---|---|---|
| `schedule` | `[String!]!` | required |
| `reconcile` | `Boolean!` | required; the provider sends `false` when unset [I: provider attribute is optional with no default, [`resource_drift_detection.go#L30-L40`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_drift_detection.go#L30-L40)] |
| `ignoreState` | `Boolean` | `false` [I: schema description says detection then runs only on finished stacks]. The provider's description narrows "ignore state" to any final state rather than only `FINISHED` [S] ([`resource_drift_detection.go#L36-L40`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_drift_detection.go#L36-L40)); the schema says the stack state is ignored [G]. Which states qualify is *(unverified)* |
| `timezone` | `String` | `UTC` [G] |

| GraphQL | Arguments | REST projection | Action |
|---|---|---|---|
| `stackIntegrationDriftDetectionCreate` | `stack`, `input` | `POST /stacks/{stackId}/drift-detection` | `DRIFT_DETECTION_INTEGRATION_CREATE` |
| `stackIntegrationDriftDetectionUpdate` | `stack`, `input` | `PUT /stacks/{stackId}/drift-detection` | `DRIFT_DETECTION_INTEGRATION_UPDATE` |
| `stackIntegrationDriftDetectionDelete` | `stack` | `DELETE /stacks/{stackId}/drift-detection` | `DRIFT_DETECTION_INTEGRATION_DELETE` |

[G]; the three granular actions arrived on 2026-01-28 and a system role for managing drift
detection on 2026-02-16 [RN]; each falls back to Admin [D] rbac. Audit resource type
`DRIFT_DETECTION_INTEGRATION` with metadata `{id, stackId}` [G].

`spacelift_drift_detection` takes `stack_id` (required, forces new), `schedule`
(required, non-empty strings), `reconcile`, `ignore_state`, `timezone` (default `UTC`);
its ID is the stack ID, and a read that finds an empty `schedule` treats the
configuration as absent [S]
([`resource_drift_detection.go#L99`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_drift_detection.go#L99),
[`#L185-L188`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_drift_detection.go#L185-L188)).
A data source of the same name reads it [S].

### 3.3 Detection and reconciliation flow

| Step | Behaviour | Source |
|---|---|---|
| Firing | At each cron time, if the stack is in `FINISHED` (or any state with `ignoreState`), a proposed run with `driftDetection = true` is queued | [D] drift, scheduling; [G] |
| Worker | Only private workers execute it | [D] drift |
| Queue order | Normal and low drift detection runs take positions 6 and 7 of the seven-step pool order, behind even low-priority tracked and proposed runs. A drift detection run raised to high priority joins position 1, with every other high-priority run, oldest first | [D] run (run prioritization); `RunPriorityPreset.low` [G]; report 10 |
| Pool limit | `WorkerPool.driftDetectionRunLimit` caps concurrent drift detection runs; null or negative means no limit | [G]; set by `workerPoolCreate` and `workerPoolUpdate` [G] |
| Over the limit | Further drift detection runs are skipped until running ones complete; no queue builds up | [D] drift |
| Disabled on the pool | Drift detection runs are not executed; failed runs with an explanatory note are created as a record; new schedules cannot be created for stacks on that pool | [D] drift. How "disabled" maps onto `driftDetectionRunLimit` (for example `0`) is *(unverified)* |
| No drift | The proposed run finishes; nothing else happens; it is visible in the account runs list filtered by drift detection | [D] drift |
| Drift, `reconcile = false` | Drifted resources are marked in the stack and account resources views | [D] drift; `ManagedEntity.drifted` (Unix time) [G]; report 21 |
| Drift, `reconcile = true` | A tracked reconciliation run is triggered, unless the stack's head commit changed during detection | [D] drift |
| Reconciliation run | Behaves as a manually triggered tracked run: obeys autodeploy (else stops in `UNCONFIRMED`), plan policy `warn` stops it in `UNCONFIRMED`, trigger policies see it | [D] drift; report 08, report 09 |
| Stack flag | `Stack.isDrifted` is true while any entity is drifted; `Stack.driftDetectedBy` holds the detecting run's ULID | [G] |
| Webhooks | Drift detection runs fire run webhooks with a `driftDetection` field | [D] drift; report 19 |
| `nextSchedule` | Null while detection or reconciliation is in progress | [G] |

The drift page says reconciliation runs respect "trigger plan policies" [D] drift;
whether that means trigger policies, plan policies or both is ambiguous on the page, but
the same page's policy input section names both plan and trigger policies [D] drift.

Per vendor:
- CloudFormation stacks detect drift with AWS's `DetectStackDrift` API and are excluded
  from reconciliation [D] cloudformation.
- Drift detection support for Kubernetes, Ansible and Terragrunt is not stated on the
  drift page *(unverified)*; report 20 owns vendor detail.

The worker pool limit is also settable on auto-registered Kubernetes pools through
`driftDetectionRunLimits: {disabled, maxRuns}`; `maxRuns` must be unset when `disabled`
is true [D] k8s-workers.

Related figures: the dashboard shows drift detection coverage and drifted resources
(report 21); the Datadog integration tags runs with `drift_detection` (report 19); the
notification policy input carries `drift_detection` (report 09).

## 4. Stack dependencies

### 4.1 Model

A stack dependency is a directed edge from a stack to a stack it depends on. Only
tracked runs follow edges; proposed runs and tasks ignore them [D] deps. Dependencies do
not create or delete stacks [D] deps.

`StackDependency` [G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Immutable; described as the dependency's slug |
| `stack` | `StackDependencyDetail!` | The stack that has the dependency (downstream) |
| `dependsOnStack` | `StackDependencyDetail!` | The stack depended on (upstream) |
| `stackId` | `ID!` | Deprecated: use `stack` [G][RN] |
| `dependsOnStackId` | `ID!` | Deprecated: use `dependsOnStack` [G][RN] |
| `references` | `[StackDependencyReference!]!` | Output-to-input references |
| `reference(id, inputName)` | `StackDependencyReference` | One reference |
| `referenceCount` | `Int!` | Number of references |
| `triggerAlways` | `Boolean!` | Trigger downstream on every upstream completion even when no referenced output changed |

`StackDependencyDetail` carries `id`, `name`, `space`, `vendorConfig` and
`isLockedByTemplateDeployment`; a stack managed by an active template deployment cannot
have its dependencies edited from stack settings [G] (report 17).

`StackDependencyReference` [G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Reference ID |
| `outputName` | `String!` | Upstream output name |
| `inputName` | `String!` | Name of the downstream input (environment variable or file) |
| `type` | `ConfigType!` | `ENVIRONMENT_VARIABLE` or `FILE_MOUNT` |
| `triggerAlways` | `Boolean!` | Trigger downstream even when this output's value has not changed |

On inputs, `type` and `triggerAlways` are optional [G]. The default `type` is
*(unverified)*; the docs describe only environment variables [D] deps, and the provider
does not expose `type` [S] ([`terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json)).

Graph fields: `Stack.dependsOn`, `Stack.isDependedOnBy`, `Stack.dependenciesFullGraph`
(transitive, both directions), `Stack.dependency(id, dependsOnStackId)` [G].
`ConfigElementWithSource.stackDependency` marks a configuration element that came from a
dependency reference [G] (report 13).

### 4.2 Operations

| GraphQL | Arguments | REST projection |
|---|---|---|
| `stackDependencyCreate` | `input: StackDependencyInput! {stackId, dependsOnStackId, triggerAlways}` | `POST /stack-dependencies` |
| `stackDependenciesCreateInBatch` | `input: StackDependencyBatchInput! {dependencies: [StackDependencyInput!]!}` | `POST /stack-dependencies/batch` |
| `stackDependencyUpdate` | `input: StackDependencyUpdateInput! {id, triggerAlways}` | `PUT /stack-dependencies/{stackDependencyId}` |
| `stackDependencyDelete` | `id` | `DELETE /stack-dependencies/{stackDependencyId}` |
| `stackDependenciesAddReference` | `stackDependencyID`, `reference: StackDependencyReferenceInput!` | `POST /stack-dependencies/{stackDependencyId}/references` |
| `stackDependenciesAddReferences` | `stackDependencyID`, `references: [...]!` | `POST /stack-dependencies/{stackDependencyId}/references/batch` |
| `stackDependenciesUpdateReference` | `reference: StackDependencyReferenceUpdateInput! {id, outputName, inputName, type, triggerAlways}` | `PUT /stack-dependency-references/{referenceId}` |
| `stackDependenciesUpdateReferences` | `references: [...]!` | `PUT /stack-dependency-references` |
| `stackDependenciesDeleteReference` | `id` | `DELETE /stack-dependency-references/{referenceId}` |
| `stackDependenciesDeleteReferences` | `ids: [ID!]!` | `DELETE /stack-dependency-references` |
| `Stack.dependency` | `id`, `dependsOnStackId` | `GET /stacks/{stackId}/dependency` |

[G]; REST paths from [`spacelift.openapi.yaml`](../../../api/reference/spacelift/spacelift.openapi.yaml).
Only `triggerAlways` is updatable on a dependency; changing either end means delete and
create [G], which the provider mirrors by forcing replacement on `stack_id` and
`depends_on_stack_id` [S]
([`resource_stack_dependency.go#L85-L114`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack_dependency.go#L85-L114)).
There are no stack dependency actions in the `Action` enum [G]; access is governed by
stack roles (section 4.10). Audit resource type `STACK_DEPENDENCY`, with metadata naming
both stacks by ID and name [G].

### 4.3 Graph rules

- The dependency graph is a DAG: a stack may have many upstreams and many downstreams,
  and adding an edge that closes a cycle fails [D] deps.
- A stack with downstream dependants cannot be deleted until they are removed; a stack
  with only upstreams can [D] deps.
- Runs never propagate upwards: a run on a middle stack queues only stacks below it [D] deps.

### 4.4 Run ordering and states

| Situation | Behaviour | Source |
|---|---|---|
| A tracked run is created on a stack with downstream dependants | Each downstream stack queues a tracked run that waits until the upstream run ends | [D] deps |
| Several upstreams | A downstream run waits for every upstream run in the same chain; siblings with no edge between them run in parallel | [D] deps (scenarios 1 and 2) |
| Downstream not reachable from an upstream that has no run | It does not wait for that upstream | [D] deps (scenario 2: services do not wait on `Storage`) |
| One push touches several stacks of one graph | Ordering still follows the graph; each stack runs once in order | [D] deps (scenarios 4 and 5) |
| Batch trigger | `runTriggerBatch` resolves dependants across all requested stacks together; a shared dependant gets one run | [G] `runTriggerBatch` |
| Upstream run fails | The chain breaks; runs below the failure are cancelled (overview) or skipped (scenario 3); stacks at the same level are unaffected | [D] deps. Which terminal state they end in is *(unverified)*: the page uses both words |
| All referenced outputs unchanged, no `triggerAlways`, no referenceless edge | The downstream run is skipped | [D] deps; `Run.triggeredRuns` excludes runs in `SKIPPED` [G] |
| A referenceless edge into the downstream stack exists | The downstream runs whenever that upstream finishes, regardless of references on other edges | [D] deps (scenario 2) |
| Upstream lacks a referenced output, but another edge forces the run | The input is not injected | [D] deps (scenario 2) |
| Retry of an upstream run | Never triggers dependants, even on success; trigger a new run instead | [D] deps |
| Priority | Dependent runs start at the priority the awaited runs share; mixed priorities fall back to normal | [D] deps; [D] run |
| Merged pull request | Tracked runs from a merge are attributed to the commit, like push runs, so downstream stacks are not wrongly skipped | [RN] (2026-09-09) |

Run-side fields: `Run.dependsOn`, `Run.isDependedOnBy` and `Run.triggeredRuns`, each
`[RunDependency!]!` with `id`, `runId`, `state`, `stackId`, `stackName` and
`runUpdatedAt` [G]. `Run.workflowId` is a ULID shared by every run in one chain, whether
the chain came from stack dependencies, trigger policies or promotion; a standalone run's
`workflowId` is its own ID [G].

### 4.5 Output references

| Rule | Detail | Source |
|---|---|---|
| Injection | The upstream output value becomes the downstream input named `inputName`; for Terraform the name needs the `TF_VAR_` prefix to reach a variable | [D] deps |
| Trigger on change | With references, the downstream runs only when a referenced output was created or changed, unless `triggerAlways` is on (per reference or per dependency) | [D] deps; [G] |
| Adding and removing | Adding a reference always triggers a run; removing one does not unless `triggerAlways` is on | [D] deps |
| Not-yet-existing outputs | A reference may name an output the upstream will create later | [D] deps |
| Source of values | Outputs of the last successful tracked run with an apply phase; outputs are uploaded during apply | [D] deps |
| Missing output | Job assignment fails with `the following inputs are missing: <stack>.<input> => <input>` | [D] deps |
| Imported outputs | Uploaded only in an apply phase; a run with no changes does not upload them, hence force apply (section 5) | [D] deps |
| Outputs before 2023-08-21 | Not uploaded; the stack must run again | [D] deps |
| Sensitive outputs | Uploaded only when the stack enables `enableSensitiveOutputUpload` and the worker sets `SPACELIFT_SENSITIVE_OUTPUT_UPLOAD_ENABLED=true`; on public workers the worker side is on | [D] deps; [G] `Stack.enableSensitiveOutputUpload`; report 07 |
| Self-hosted CloudFormation worker pools | Export `SPACELIFT_SENSITIVE_OUTPUT_UPLOAD_ENABLED=true` in the `CustomUserDataSecretName` secret | [D] `https://docs.spacelift.io/self-hosted/latest/concepts/stack/stack-dependencies` |
| Vendors without outputs | Ansible and Kubernetes stacks cannot be referenced, but can receive references | [D] deps |
| CloudFormation output-only changes | A no-op for CloudFormation; a resource change is needed to upload outputs | [D] deps |
| Size | Each injected value is limited by the kernel's 128 kB per-argument limit (`argument list too long`) | [D] deps |
| Stored outputs | `Stack.outputs: [StackOutput!]!` with `id`, `sensitive`, `value` (empty when sensitive), `description` | [G] |

### 4.6 Trigger chain in policy input

When a stack dependency causes a run, policy input carries `run.trigger` with
`run.id` and `stack.id` of the upstream run, recursively in `trigger.trigger` up to 20
levels; the outermost level is the direct parent [D] deps. The `stack` object at each
level arrived on 2026-10-22 [RN]. The field is present in trigger, plan, approval,
notification and push policy input, and null when no stack dependency caused the run
[D] deps, trigger. Report 09 owns the input documents.

### 4.7 Terraform resources and spacectl

| Resource | Attributes | ID | Source |
|---|---|---|---|
| `spacelift_stack_dependency` | `stack_id`, `depends_on_stack_id` (both required, replace on change), `trigger_always` (default `false`) | `<stack_id>/<dependency_id>`; import also accepts `<stack_id>/<depends_on_stack_id>`, with a ULID second part selecting the legacy lookup | [S] [`resource_stack_dependency.go#L50`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack_dependency.go#L50), [`#L240-L252`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack_dependency.go#L240-L252) |
| `spacelift_stack_dependency_reference` | `stack_dependency_id` (required, forces new), `output_name`, `input_name` (required), `trigger_always` (default `false`) | `<stack_id>/<dependency_id>/<reference_id>` | [S] [`resource_stack_dependency_reference.go#L50-L56`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack_dependency_reference.go#L50-L56), [`#L85`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack_dependency_reference.go#L85) |

`spacectl stack dependencies on` lists the stacks a stack depends on and `off` the
stacks that depend on it, each with `--id`, `--run` and an output format flag [S]
([`internal/cmd/stack/stack.go#L886-L935`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/stack.go#L886-L935)).

### 4.8 Ordered creation and deletion

Dependencies do not order stack creation or deletion. The documented workaround is the
provider's `spacelift_stack_destructor` with `depends_on`, so `terraform destroy` removes
references and dependencies, then destroys each stack's resources and the stack from
the bottom of the graph up [D] deps. The destructor resource is report 05.

### 4.9 Stack deletion

Deleting an upstream stack fails while downstream dependencies exist [D] deps. The
interaction with a scheduled delete on such a stack is *(unverified)* (section 2.5).

### 4.10 Permissions

The docs ask for reader permission on the dependent stack and admin permission on the
other stack to create a dependency [D] deps; which side needs which is ambiguous in the
wording *(unverified)*. Creating a dependency no longer needs read access to the space
[RN] (2026-06-23).

## 5. Force apply

`ForceApplyMode` [G]:

| Value | Meaning |
|---|---|
| `SINGLE` | Apply this run even when the plan has no changes; do not propagate |
| `CASCADE` | Apply this run even with no changes, and propagate force apply to every dependent run it triggers |

| Where | Field | Source |
|---|---|---|
| `runTrigger` | `forceApply: ForceApplyMode` | [G] |
| `runTriggerBatch` | `RunTriggerBatchInput.forceApply`; null keeps the normal behaviour, in which a run with no changes stops after planning | [G] |
| `Run` | `forceApplyMode: ForceApplyMode` (null for normal runs) | [G] |
| UI | **Trigger with force apply**, with **This run only** and **Cascade to dependents** | [D] deps |
| spacectl | `spacectl stack deploy --force-apply single` or `cascade`, spacectl v1.21.0 or later; the flag exists only on the command variant for servers 5.1.0 and later | [D] deps; [S] [`internal/cmd/stack/stack.go#L165-L180`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/stack.go#L165-L180), [`run_trigger.go#L172-L179`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/run_trigger.go#L172-L179) |
| Terraform provider | `spacelift_run` takes `stack_id`, `commit_sha`, `proposed`, `keepers`; no force apply | [S] ([`terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json)) |

The documented purpose is to make an apply phase happen so outputs, including imported
ones, are uploaded for references [D] deps. Without force apply a no-change tracked run
goes from planning straight to `FINISHED` [D] tracked. Whether a force-applied
zero-change run passes through `UNCONFIRMED` when autodeploy is off is *(unverified)*.
The older workaround is to add and remove a dummy output [D] deps.

## 6. External dependencies

A push policy can attach named external dependencies to the runs it creates, so a run
waits for work outside Spacelift, such as a CI build [D] ext-deps.

| Aspect | Detail | Source |
|---|---|---|
| Declaration | Push policy rule `external_dependency contains <string>`; any run the policy creates carries the set | [D] ext-deps; report 09 |
| Naming | IDs should embed something unique per VCS event, such as the commit SHA | [D] ext-deps |
| Object | `RunExternalDependency {id, status, createdAt, completedAt}`; `Run.externalDependencies` | [G] |
| Statuses | `PENDING`, `FINISHED`, `FAILED`, `SKIPPED` (`RunExternalDependencyStatus`) | [G] |
| Initial status | `PENDING` | [I: the only non-terminal value] |
| Completion | `runExternalDependencyMarkAsCompleted(dependency: ID!, status: RunExternalDependencyStatus!)`, marked experimental; returns `{phantom, status}` | [G]; REST projection `POST /run-external-dependencies/{dependencyId}/complete` |
| Run gating | The run stays `QUEUED` until every dependency is `FINISHED` or `SKIPPED` | [D] ext-deps |
| Failure | One `FAILED` dependency fails the run, with a note naming the failed dependency | [D] ext-deps |
| Scope of a mark | Marking needs write access to every space holding runs with that dependency ID | [D] ext-deps |
| spacectl | `spacectl run-external-dependency mark-completed --id <id> --status <s>`, with `<s>` one of `finished`, `failed`, `skipped` | [D] ext-deps; [S] [`mark_completed.go#L24-L32`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/run_external_dependency/mark_completed.go#L24-L32) |

One ID marks every run that carries it [I: the write-access rule spans all spaces with
runs holding the ID]. spacectl's `--status` help text names only `finished` and
`failed`, but the code also accepts `skipped` [S]
([`flags.go#L11-L15`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/run_external_dependency/flags.go#L11-L15)).
A run held by an unresolved dependency is subject to the 30-day unfinished-run
termination [I: the rule applies to every non-terminal run, [RN] 2026-09-17]. Marking
an ID before any run carries it, and re-marking a completed ID, are *(unverified)*.

## 7. Trigger policies beside these features

Report 09 owns trigger policy rules and input. The interplay that matters here:

| Point | Detail | Source |
|---|---|---|
| Evaluation time | Each time a blocking run (tracked run or task) reaches a terminal state: `FINISHED`, `CANCELED`, `DISCARDED`, `STOPPED`, `FAILED` | [D] trigger |
| Scheduled tasks and runs | Their terminal states therefore evaluate trigger policies too | [I: they are tasks and tracked runs] |
| Drift detection | Drift detection proposed runs are not blocking, so they do not evaluate trigger policies; reconciliation tracked runs do, with `drift_detection` set | [I: evaluation is on blocking runs only, [D] trigger]; [D] drift |
| Workflow | A trigger policy sees every run in the same workflow, across stacks | [D] trigger; `Run.workflowId` [G] |
| Relation to dependencies | The two features are independent and should not be combined, because combining them can confuse or loop | [D] deps |
| Recommendation | Stack dependencies are the simpler choice for most cases | [D] deps, trigger |
| Priority | A trigger policy `priority` rule sets `high`, `normal` or `low` for the runs it triggers; conflicting levels fall back to normal | [D] trigger; [D] run |
| Retries | A trigger policy can retry failed runs, which stack dependencies never do | [D] trigger (automated retries), deps |

## What this means for infraweave

- A scheduler that fires per-stack cron lists in a named timezone and one-off timestamps,
  computes `nextSchedule`, and creates tracked runs, tasks, destroy-then-delete jobs and
  drift detection runs; firing only for stacks on private pools.
- A cron grammar at least as wide as the five-field pattern in section 2.6, and IANA
  timezones with a UTC default.
- Drift detection as proposed runs flagged `driftDetection`, queued after all other runs,
  capped per worker pool, with optional reconciliation through the ordinary tracked run
  path (autodeploy, plan and trigger policies), and per-entity drift timestamps for the
  resources view.
- A stack dependency DAG with cycle rejection, deletion blocking, downstream-only
  propagation, waiting on every upstream run in the chain, skip-on-unchanged-outputs,
  break-on-failure, priority inheritance, and no propagation on retry.
- An output store fed only by apply phases, with sensitive-output gating on both stack and
  worker, references injected as environment variables (and possibly files), and the
  missing-input failure at job assignment.
- `ForceApplyMode` on single and batch triggers, carried on the run and propagated in
  cascade mode.
- External dependency records with four statuses, a completion endpoint, and queue gating.
- Workflow IDs and a recursive trigger chain in policy input.
- RBAC actions for schedules and drift detection, and audit resource types for each
  object here.

## Inferred and undocumented

- The `nextSchedule` descriptions on run, task and delete schedules reuse drift detection
  wording; read as "next firing" [I].
- `ignoreState` defaults to false and the provider sends `reconcile = false` when unset
  [I].
- `RunExternalDependencyStatus.PENDING` is the initial status [I].
- One external dependency ID resolves every run that carries it [I].
- Scheduled tracked runs and tasks evaluate trigger policies at their terminal state;
  drift detection proposed runs do not [I].
- Unresolved external dependencies fall under the 30-day termination rule [I].
- The template schema's `maxLength: 1` on drift detection `cron` items has no effect
  under draft-07 [I].
- *(unverified)*: API handling of an empty `ScheduledRunInput.name`; the API default
  timezone for run and task schedules; accepted timezone names; whether the API accepts
  cron forms beyond the template pattern; minimum interval; daylight-saving handling;
  missed firings; the fate of a fired one-shot schedule; the destroy-failure path of a
  scheduled delete and its interaction with downstream dependants; the
  `TF_VAR_spacelift_run_trigger` value for scheduled runs; how "disabled" maps onto
  `driftDetectionRunLimit`; which stack states `ignoreState` admits; drift detection
  for Kubernetes, Ansible and Terragrunt;
  whether downstream runs end `CANCELED` or `SKIPPED` after an upstream failure; the
  default `ConfigType` of a reference; which side of a dependency needs admin; whether a
  zero-change force apply stops in `UNCONFIRMED`; marking an external dependency before
  any run carries it, and re-marking it.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS schema | introspection `b3255fe38f5e`, fetched 2026-10-28 |
| docs.spacelift.io | read 2026-10-28; self-hosted `latest` is v7.1.0 |
| terraform-provider-spacelift | v1.57.0 at `c72b9bd2f3c3` |
| spacectl | `6bf167cb62a6`; force apply needs v1.21.0+ and server 5.1.0+ |

## Sources

- <https://docs.spacelift.io/concepts/stack/scheduling>
- <https://docs.spacelift.io/concepts/stack/drift-detection>
- <https://docs.spacelift.io/concepts/stack/stack-dependencies>
- <https://docs.spacelift.io/self-hosted/latest/concepts/stack/stack-dependencies>
- <https://docs.spacelift.io/concepts/policy/push-policy/run-external-dependencies>
- <https://docs.spacelift.io/concepts/policy/trigger-policy>
- <https://docs.spacelift.io/concepts/run>
- <https://docs.spacelift.io/concepts/run/tracked>
- <https://docs.spacelift.io/concepts/configuration/environment>
- <https://docs.spacelift.io/concepts/authorization/rbac-system>
- <https://docs.spacelift.io/concepts/worker-pools>
- <https://docs.spacelift.io/concepts/worker-pools/kubernetes-workers>
- <https://docs.spacelift.io/vendors/cloudformation>
- <https://docs.spacelift.io/concepts/template/configuration>
- <https://docs.spacelift.io/product/changelog> (entries 2026-01-28, 2026-02-16, 2026-06-23, 2026-07-10, 2026-07-16, 2026-09-09, 2026-09-17, 2026-10-22)
- [`introspection.json`](../../../api/reference/spacelift/introspection.json): `ScheduledRun`, `ScheduledTask`, `ScheduledDelete` and their inputs; `DriftDetectionIntegration`, `DriftDetectionIntegrationInput`; `StackDependency*`; `RunDependency`; `RunExternalDependency*`; `ForceApplyMode`; `RunTriggerBatch*`; `ConfigType`; `RunPriorityPreset`; `Action`; `AuditTrailResourceType`; fields on `Stack`, `Run`, `WorkerPool`, `ManagedEntity`
- [`spacelift.openapi.yaml`](../../../api/reference/spacelift/spacelift.openapi.yaml) and [`terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json)
- terraform-provider-spacelift at `c72b9bd2f3c3`: <https://github.com/spacelift-io/terraform-provider-spacelift/tree/c72b9bd2f3c3/spacelift> (`resource_scheduled_run.go`, `resource_scheduled_task.go`, `resource_scheduled_delete_stack.go`, `resource_drift_detection.go`, `resource_stack_dependency.go`, `resource_stack_dependency_reference.go`) and <https://github.com/spacelift-io/terraform-provider-spacelift/tree/c72b9bd2f3c3/examples/resources>
- spacectl at `6bf167cb62a6`: <https://github.com/spacelift-io/spacectl/tree/6bf167cb62a6/internal/cmd/run_external_dependency>, <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/stack.go>, <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/run_trigger.go>
