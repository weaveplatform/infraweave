# 21: Resources, scans and search

Checked against: docs.spacelift.io read 2026-10-28 (SaaS and self-hosted `latest`, v7.1.0, whose resources, bulk actions and dashboard pages match SaaS); introspection `b3255fe38f5e`; spacelift-api-bruno `7cad4574954b`; terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); spacectl `6bf167cb62a6`.

Research baseline: **2026-10-28**.

This report records how Spacelift shows and queries what it manages and what it can
find: the resources view and its managed entity model, the entity changes a run
produces (phase, type and result), the Ansible configuration management view, scans
of cloud accounts for resources no stack manages, the generic search API that every
list view and many clients use, saved filters, bulk actions and the dashboard with its
metrics. infraweave's resource inventory, its list and search endpoints and its
dashboard queries are designed from it. The REST routes for these operations are
inventoried in [03](03-deploy-core-api-inventory.md) and
[04](04-platform-api-inventory.md); run phases and targeted replan are in
[08](08-run-lifecycle.md); managed state, from which the inventory is derived, is in
[16](16-state-management.md); metrics delivery to Prometheus and Datadog and usage
data are in [19](19-events-notifications-audit.md); drift detection schedules are
report 18; resource sanitization and per-vendor behaviour are report 20.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` ([`introspection.json`](../../../api/reference/spacelift/introspection.json)) |
| **[B]** | spacelift-api-bruno at `7cad4574954b` ([`bruno.json`](../../../api/reference/spacelift/bruno.json)) |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog, <https://docs.spacelift.io/product/changelog> |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

Short names for the docs pages cited below:

| Short name | URL |
|---|---|
| resources | <https://docs.spacelift.io/concepts/resources> |
| config-mgmt | <https://docs.spacelift.io/concepts/resources/configuration-management> |
| bulk | <https://docs.spacelift.io/product/bulk-actions> |
| dashboard | <https://docs.spacelift.io/product/dashboard> |
| organizing | <https://docs.spacelift.io/concepts/stack/organizing-stacks> |
| drift | <https://docs.spacelift.io/concepts/stack/drift-detection> |
| terragrunt | <https://docs.spacelift.io/vendors/terraform/terragrunt> |
| tofu-migration | <https://docs.spacelift.io/vendors/opentofu/migration> |
| changelog | <https://docs.spacelift.io/product/changelog> |

Every enum and field table in this report is generated from the introspection snapshot
by script from the snapshot and then
annotated from the docs and sources.

## 1. Surfaces and the operations behind them

| UI surface | What it shows | Operations | Source |
|---|---|---|---|
| _Ship Infra_ > _Resources_ (account level) | Every managed entity across stacks the viewer can read, grouped by stack (default), type or provider, with a filter sidebar | `searchManagedEntities`, `searchManagedEntitiesGroupByStacks`, `searchManagedEntitiesGroupByTypes`, `searchManagedEntitiesGroupByProviders`, `searchManagedEntitiesSuggestions` on `Query` | [D] resources; [G]; [B] folder `Resources` |
| Stack _Resources_ tab | The same view limited to one stack | The same five fields on `Stack`; also `Stack.entities`, `Stack.entity(id)`, `Stack.entityCount` | [D] resources; [G] |
| _Configuration Management_ (stack tab, Resources tab, run _Tasks_ tab) | Ansible hosts, tasks and task executions | `searchAnsibleHosts`, `searchAnsibleTasks`, `ansibleHosts`, `ansibleTasks`, `ansibleHost`, `hasAnsibleStacks`, `AnsibleHost.searchAnsibleTaskExecutions` | [D] config-mgmt; [G] |
| Run _Changes_ view | Per-entity changes planned or applied by a run | `Run.changesV2`, `Run.changesV3`, `Run.searchEntityChanges`, `Run.searchEntityChangesSuggestions`, `Run.delta` | [G]; [RN] 2026-09-23 |
| Scans | Scan definitions, scan runs, discovered cloud resources and failures | `scan*` operations, `searchScans`, `ScanRun.searchResources`, `ScanRun.searchFailures` | [G]; [B] folder `Scans`; no docs page *(unverified)* |
| Every list view's filter bar | Filterable fields and their values with counts | `search…Suggestions` fields (57 in the schema) | [G]; [B] folder `Advanced/Search Suggestions` |
| Saved filter views | Named filter, search and sort sets per list view | `savedFilters`, `savedFilter`, `savedFilterCreate`, `savedFilterUpdate`, `savedFilterDelete` | [D] organizing; [G]; [B] |
| Bulk action bar and drawer | Actions over a selection of stacks, runs, modules, notifications, workers or queued runs | Per-entity mutations (section 8) | [D] bulk; [G] |
| Dashboard | Overview, Metrics and DORA views plus custom views | `metrics`, `metricsRange` (`SpaceliftMetrics`) | [D] dashboard; [G]; [B] |

## 2. The resources view and the managed entity model

### 2.1 Behaviour

- The resources view lists the lifecycle of every resource Spacelift manages, whatever
  the vendor (OpenTofu, Terraform, Terragrunt, Pulumi, CloudFormation and others)
  [D] resources.
- At stack level the default grouping reflects the structure of the project; the view
  can also group by type and by provider [D] resources.
- At account level the default grouping is by stack, one row per stack that expands to
  its resources; the filter sidebar and grouping options cover other properties
  [D] resources.
- Clicking a resource opens a details drawer with the vendor-specific representation.
  String values in it are never shown: only the first seven characters of each value's
  checksum appear, so a reader who knows a candidate value can compare it [D] resources.
- The drawer links to the run that created the resource and the run that last updated
  it, and to the commit of each run in the VCS provider [D] resources. These are
  `ManagedEntity.creator` and `ManagedEntity.updater` [G].
- A resource has a shareable link, from the drawer or the row menu (**Copy link**)
  [D] resources.
- Any cell can be added to the filters ("Add to filters") [D] resources. The filter is a
  search predicate on that field [I: the view's queries take only `SearchInput`].
- The view is built from state that runs report, and is read only [B] folder
  `Resources`: "derived from run state".
- Drifted resources show as drifted in the Resources view at stack and account
  level, whether or not drift detection reconciles them [D] drift.

### 2.2 `ManagedEntity` **[G]**

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Entity ID in Spacelift. `Stack.entity(id)` takes the entity's address as its `id` [G] |
| `address` | `String!` | Address of the entity within its module |
| `creator` | `Run` | Run that created the entity |
| `drifted` | `Int` | Unix time drift was detected on the entity, null if never |
| `name` | `String!` | Entity name |
| `parent` | `String!` | Parent: a module or another entity |
| `thirdPartyMetadata` | `String` | Custom metadata a third-party integration attached; how it is written is *(unverified)* |
| `type` | `String!` | Entity type, such as a provider resource type |
| `updater` | `Run` | Run that last updated the entity |
| `vendor` | `EntityVendor!` | Vendor-specific metadata (section 2.4) |
| `stackId` | `ID!` | Owning stack |
| `stackName` | `String!` | Owning stack's name |
| `stackVendor` | `StackVendor!` | Owning stack's vendor: `Ansible`, `CloudFormation`, `Kubernetes`, `Pulumi`, `OpenTofu`, `Terraform`, `Terragrunt` |
| `updatedAt` | `Int!` | Unix seconds of the last update |

`ManagedEntityV2` carries the first nine fields only (`id` to `updater`), without
`vendor`, the stack fields or `updatedAt`; it is the entity inside `EntityChangeV2` [G].

Stack fields over the same data [G]:

| Field | Type | Meaning |
|---|---|---|
| `entities` | `[ManagedEntity!]!` | All entities the stack manages |
| `entity(id: ID!)` | `ManagedEntity` | One entity, by address |
| `entityCount` | `Int!` | Number of entities |
| `isDrifted` | `Boolean!` | True when any entity of the stack is drifted |
| `driftDetectedBy` | `ID` | ULID of the run that detected the drift; null unless `isDrifted` |
| `delta` | `Delta` | Resource counts the blocking unconfirmed run would change (section 3.5) |
| `runResourceState(id: ID!)` | `RunResourceState` | `id`, `state: RunState!`, `finished: Boolean!` of a run; the schema says it exists so the Terraform provider can read another run's state. The provider polls it while a `spacelift_run` or `spacelift_task` waits for its run to finish [S] [wait.go#L139-L147](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/structs/wait.go#L139-L147); report 05 |

### 2.3 Groupings **[G]**

The three grouping queries return connections whose nodes implement the
`ManagedEntitiesGroup` interface (`id: ID!`, `count: Int!`, `drifted: Int!`,
`updatedAt: Int!`).

| Query | Node | `id` is | Extra fields |
|---|---|---|---|
| `searchManagedEntitiesGroupByStacks` | `ManagedEntitiesGroupedByStacks` | Stack ID | `stackName`, `stackSpaceID`, `stackSpaceName`, `stackVendor` |
| `searchManagedEntitiesGroupByTypes` | `ManagedEntitiesGroupedByTypes` | Resource type | none |
| `searchManagedEntitiesGroupByProviders` | `ManagedEntitiesGroupedByProviders` | Provider name | none |

`count` is the number of entities in the group, `drifted` the number of drifted ones
and `updatedAt` the time of the most recently updated entity in the group [G]. None of
the three has a suggestions twin; the view uses `searchManagedEntitiesSuggestions` for
all of them [I: no other suggestions field exists for managed entities].

### 2.4 Vendor metadata **[G]**

`EntityVendor` is a union of one wrapper per vendor family; each wrapper holds a union
of that vendor's entity kinds.

| Wrapper | Field | Union | Members and their fields |
|---|---|---|---|
| `EntityVendorTerraform` | `terraform` | `TerraformEntity` | `TerraformResource` (`address`, `mode`, `module`, `provider`, `tainted: Boolean!`, `values`: JSON string); `TerraformOutput` (`sensitive`, `value`: null when sensitive because sensitive values are not stored, `hash`: always present); `TerraformModule` (placeholder field `phantom` only) |
| `EntityVendorPulumi` | `pulumi` | `PulumiEntity` | `PulumiResource` (`urn`, `id`, `provider`, `parent`, `outputs`: JSON string); `PulumiOutput` (`sensitive`, `value`, `hash`, as for Terraform); `PulumiStack` (`phantom` only) |
| `EntityVendorCloudFormation` | `cloudFormation` | `CloudFormationEntity` | `CloudFormationResource` (`logicalResourceId`, `physicalResourceId`, `template`); `CloudFormationOutput` (`description`, `export`: the cross-stack export name, `value`) |
| `EntityVendorKubernetes` | `kubernetes` | `KubernetesEntity` | `KubernetesResource` (`data`); `KubernetesRoot` (`phantom` only) |
| `EntityVendorAnsible` | `ansible` | `AnsibleEntity` | `AnsibleResource` (`data`) |

OpenTofu and Terragrunt stacks have no wrapper of their own; they report through
`EntityVendorTerraform` [I: the union has five members for seven `StackVendor`
values, and `EntityChangeV3` calls Terraform, OpenTofu and Terragrunt one family].
The `phantom` members exist only because GraphQL requires an object type to have a
field; they mark a node as a module, a Pulumi stack or a Kubernetes root [G].

## 3. Entity changes

### 3.1 `EntityChangePhase` **[G]**

| Value | Meaning |
|---|---|
| `DRIFT` | Change seen by a drift detection run |
| `PLAN` | Change seen in the planning phase |
| `APPLY` | Change made in the applying phase |

### 3.2 `EntityChangeType` **[G]**

| Value | Meaning | Vendors |
|---|---|---|
| `ADD` | A new entity is created | all |
| `CHANGE` | An entity is updated in place | all |
| `DELETE` | An entity is deleted | all |
| `REPLACE_CREATE_BEFORE_DESTROY` | Replacement created first, then the original deleted | Terraform family [I: Terraform's replace orders] |
| `REPLACE_DESTROY_BEFORE_CREATE` | Original deleted first, then the replacement created | Terraform family [I] |
| `NOOP` | No change | all |
| `READ` | A read, no change | Terraform family [I: data sources] |
| `IMPORT` | An entity is imported | Terraform family [I] |
| `FORGET` | Removed from state without being destroyed | Terraform family [I: `removed` blocks] |
| `CREATE_AFTER_FORGOTTEN` | Removed from state without being destroyed, and a replacement created | Terraform family [I] |
| `OK` | Ansible task finished with status ok | Ansible [G] |
| `SKIPPED` | Ansible task skipped | Ansible [G] |
| `RESCUED` | Ansible task rescued | Ansible [G] |
| `IGNORED` | Ansible task failure ignored | Ansible [G] |
| `UNREACHABLE` | Ansible host unreachable | Ansible [G] |
| `FAILED` | Ansible task failed | Ansible [G] |

The schema groups the last six as Ansible-specific in the description of `OK` [G].

### 3.3 `EntityChangeResult` **[G]**

| Value | Meaning |
|---|---|
| `PLANNED` | Planned and not (yet) applied |
| `SUCCESSFUL` | Planned and applied |
| `FAILED` | Planned and not applied |
| `PARTIAL` | A `REPLACE_*` change where only one of its create and delete halves was applied |
| `UNPLANNED` | Applied without having been planned |
| `UNKNOWN` | Result not known |

### 3.4 Change shapes and queries **[G]**

| Type | Fields | Returned by |
|---|---|---|
| `EntityChangeV2` | `type: EntityChangeType!`, `phase: EntityChangePhase!`, `entity: ManagedEntityV2!` | `Run.changesV2(input: EntityChangeCollectionInputV2!)`: `EntityChangesV2` (`edges { cursor, node }`, `pageInfo`) |
| `EntityChangeV3` | `address: String!`, `previousAddress: String!` (set when the resource moved), `metadata: EntityChangeMetadata!` (`result`, `type`, `moved: Boolean!`), `attributeChanges: [EntityAttributeChange!]!` | `Run.changesV3(input: EntityChangesInputV3!)`: `[ResourceChange!]!`, each `ResourceChange` holding `resources: [EntityChangeV3!]!`; `Run.searchEntityChanges(input: SearchInput!, changePhase: String)`: `SearchEntityChangeOutput` |
| `EntityAttributeChange` | `path: String!` (JSON path of the property), `changeForPhase: EntityAttributeChangeForPhase!` | inside `EntityChangeV3` |
| `EntityAttributeChangeForPhase` | `metadata` (`phase`, `type`), `before: ChangeValue` (null if added), `after: ChangeValue` (null if removed), `afterUnknown: Boolean!` | inside `EntityAttributeChange` |
| `ChangeValue` | union of `ChangeValueBoolean`, `ChangeValueString`, `ChangeValueFloat`, each with a non-null `value` | inside the phase change |

Inputs [G]:

| Input | Fields |
|---|---|
| `EntityChangeCollectionInputV2` | `first: Int`, `after: String` (cursor from `pageInfo.endCursor`), `changeType: EntityChangeType`, `changePhase: EntityChangePhase` |
| `EntityChangesInputV3` | `changePhase: EntityChangePhase`, `changeType: EntityChangeType`; with no phase, the latest phase's changes are returned |

Rules:

- `afterUnknown` true means the value is only known after apply; a client renders
  "(known after apply)" and does not read `after` as a literal null [G].
- `changesV3`, `searchEntityChanges` and `searchEntityChangesSuggestions` serve only
  Terraform, OpenTofu and Terragrunt stacks, and return an explicit error for any other
  vendor; other vendors use `changesV2` [G] [RN] 2026-09-23. Before that date
  `changesV3` returned an empty list for them [RN] 2026-09-23.
- `changePhase` on `searchEntityChanges` is a `String`, not the `EntityChangePhase`
  enum [G]; the accepted spellings are *(unverified)*.
- JSON-encoded attribute values in the run's resource changes panel render as
  structured JSON diffs [RN] 2026-03-03.
- The bulk **Replan** action on the run changes view starts a targeted replan of the
  selected changes [D] bulk; it is `runTargetedReplan(stack, run, targets)` with the
  selected addresses as `targets` [G] [I: the only mutation that takes a list of
  addresses]. Targeted replan is gated by `BillingTierFeature.TARGETED_REPLAN` [G];
  report 08 owns its run semantics.

### 3.5 `Delta` **[G]**

`Run.delta` (changes this run causes) and `Stack.delta` (changes the blocking
unconfirmed run causes) count resources by kind of change.

| Field | Counts | Corresponding change type |
|---|---|---|
| `addCount` | Resources to create | `ADD` [I] |
| `changeCount` | Resources to modify in place | `CHANGE` [I] |
| `deleteCount` | Resources to destroy | `DELETE` [I] |
| `importCount` | Resources to import | `IMPORT` [I] |
| `moveCount` | Resources to move | `EntityChangeMetadata.moved` [I] |
| `forgetCount` | Resources to forget | `FORGET` [I] |
| `resources` | Resources in state before the change | none |
| `okCount`, `skipCount`, `rescueCount`, `ignoreCount`, `unreachableCount`, `failedCount` | Ansible task outcomes | `OK`, `SKIPPED`, `RESCUED`, `IGNORED`, `UNREACHABLE`, `FAILED` [I] |

How replacements count (once in add and once in delete, or otherwise) is
*(unverified)*.

## 4. Configuration management (Ansible)

- For Ansible stacks the stack page shows a _Configuration Management_ view in place of
  _Resources_; a toggle (**Enable configuration management view**) switches back to the
  resources view for historical resources [D] config-mgmt. Where the toggle is stored is
  *(unverified)*; no stack field for it exists in the schema [G].
- The account _Resources_ view has a _Configuration Management_ tab, and an Ansible
  run's view has a _Tasks_ tab with per-task execution details and logs [D] config-mgmt.
- The view shows the latest status of every inventory item from its most recent run,
  and the run list shows Ansible statuses [D] config-mgmt.
- The data is read only; Ansible runs start like any other run [B] folder `Ansible`.

Types [G]:

| Type | Fields |
|---|---|
| `AnsibleHost` | `id`, `name`, `machineID`, `architecture`, `distribution`, `distributionVersion`, `taskExecutions: [AnsibleTaskExecution!]!`, `searchAnsibleTaskExecutions(input: SearchInput!)`, `searchAnsibleTaskExecutionsSuggestions` |
| `AnsibleTask` | `name`, `path`, `action`, `resolvedAction`, `role: AnsibleRole` (`name`, `path`), `playbook: AnsiblePlaybook!` (`name`, `path`), `taskExecutions` |
| `AnsibleTaskExecution` | `timestamp: Int!`, `task`, `status: AnsibleTaskStatus!`, `checkMode: Boolean!`, `runId`, `stackId`, `stdout`, `diff`, `host` |

`AnsibleTaskStatus` [G]: `OK`, `CHANGED`, `SKIPPED`, `IGNORED`, `RESCUED`,
`UNREACHABLE`, `FAILED`. It has `CHANGED` where `EntityChangeType` uses the generic
`CHANGE` [G].

Queries [G]: `ansibleHosts(input: RunOrStackArgs)` and `ansibleTasks(input:
RunOrStackArgs)`, where `RunOrStackArgs` is `{stack: String, run: String}`;
`ansibleHost(id)`; `hasAnsibleStacks: Boolean!` (true when the account has an Ansible stack;
that the UI uses it to show the tab is [I]);
`searchAnsibleHosts`, `searchAnsibleTasks` and their suggestions. The two search
outputs and their suggestions are nullable, unlike most search fields [G].

## 5. Scans

A scan is a saved configuration that discovers resources in a cloud account; each
execution is a scan run, carried out by a worker, which records the resources it found
and whether some stack already manages each one [G]. No page in the docs mirror
describes scans; the facts below come from the schema and Bruno, and Bruno's folder
note calls them "security and compliance scans" [B]. The account feature flag is
`BillingTierFeature.SCANS` [G]. The audit trail records `SCAN` and `SCAN_RUN`
resources ([19](19-events-notifications-audit.md)) [G].

### 5.1 `Scan` and `ScanInput` **[G]**

| Field | Type | Input | Meaning and constraints |
|---|---|---|---|
| `id` | `ID!` | none | Slug |
| `space` | `Space!` | `ID!` | Owning space |
| `name` | `String!` | `String!` | Display name |
| `description` | `String!` | `String!` | Free text; an empty string means none |
| `labels` | `[String!]!` | `[String!]!` | For filtering and grouping |
| `providerConfigs` | `[ScanProviderConfig!]!` | `[ScanProviderConfigInput!]!` | At least one. One entry per provider configuration: several AWS regions are several entries, and two cloud accounts are two entries |
| `scope` | `[ScanScope!]!` | `[ScanScopeInput!]!` | Resource types to discover and per-type filters |
| `workerPool` | `ID!` | `ID!` (ULID) | Pool that runs the scan; it must be a discovery-type pool. No `WorkerPool` field or enum marks a pool as discovery-type, so how a pool becomes one is *(unverified)* |
| `workerPoolName` | `String` | none | Null only when the pool no longer exists |
| `runnerImage` | `String` | `String` | Optional custom runner image |
| `latestRun` | `ScanRun` | none | Most recent run in any state; null before the first |
| `searchRuns`, `searchRunsSuggestions` | search | none | The scan's runs |
| `deleted` | `Boolean!` | none | Soft-deletion marker |
| `createdAt`, `updatedAt` | `Int!` | none | Unix seconds |

`scanUpdate(id, input: ScanInput!)` replaces the whole configuration with the same
input type as create [G].

### 5.2 Provider configuration **[G]**

| Type | Fields and rules |
|---|---|
| `ScanProvider` (enum) | `AWS` only |
| `ScanProviderConfigInput` | `version: String!`: a provider major from `ScanProviderSchema.majorVersions`; `integration: ID!`: ULID of the cloud integration supplying credentials; `throttleInterval: String`: Go duration between list calls, from `1ms` to `1m`, default `5s`; `aws: AWSScanSettingsInput`: exactly one provider member must be set, and it selects the provider |
| `ScanProviderConfig` | `type: ScanProvider!`, `version`, `config: ScanProviderSettings!` (union, member `AWSScanSettings`), `integration`, `integrationName` (null when the integration is deleted), `throttleInterval` |
| `AWSScanSettings` / input | `region: String!`; one region per entry |
| `ScanProviderSchema` | `type`, `majorVersions: [String!]!` (one release is held per major, so a scan pins a major only), `resourceTypes: [ScanResourceType!]!` (types the provider cannot list are absent), `configSchema: ScanProviderConfigSchema!` (union, member `AWSScanConfigSchema`) |
| `AWSScanConfigSchema` | `regions: [AWSRegion!]!`; China and GovCloud regions are not supported |
| `ScanResourceType` | `type` (matches `ScanScope.type`, for example `aws_s3_bucket`), `subcategory` (the provider docs subcategory, for example "VPC (Virtual Private Cloud)"), `parameters: [ScanListParameter!]!`, `attributes: [ScanAttribute!]!` |
| `ScanListParameter` | `name`, `required: Boolean!`, `type: ScanParameterType!`. A required parameter identifies a parent resource and is autodetected from the parent listing; setting it through `ScanScopeInput.parameters` is an error |
| `ScanParameterType` | `STRING`, `BOOL`, `NUMBER` (written as a decimal string), `OTHER` (no string can express it, so the API cannot set it) |
| `ScanAttribute` | `name`, `type: ScanAttributeType!` |
| `ScanAttributeType` | `STRING` (addressed by name), `STRING_MAP` (such as tags; addressed by name plus key) |

The resource type names and the major version follow the Terraform provider for the
cloud: the Bruno example pins `version: "6"` with type `aws_s3_bucket` [B], and
`ScanResource.type` is described as the provider's type name [G]. That scans run the
Terraform AWS provider's list capability is [I] from these names and from
`ScanResource.identity` being the provider's resource identity.

`scanProviderSchemas(provider: ScanProvider)` lists what each provider can scan; Bruno
notes that `version` is a plain string in the type system but only the listed values
are accepted, and anything else is rejected when the scan runs, not when it is saved
[B] folder `Scans`.

### 5.3 Scope and filters **[G]**

| Type | Fields and rules |
|---|---|
| `ScanScopeInput` | `type: String!`; `parameters: [ScanScopeParameterInput!]`: values for the list call, applied before any resource is read; `filter: ScanScopeFilterInput`: omitted keeps every resource of the type |
| `ScanScopeParameterInput` | `name`: one of the type's parameters not marked required; `value: String!`: converted against the parameter's type; an `OTHER` parameter takes no value |
| `ScanScopeFilterInput` | `conditions`: 1 to 20; a resource is kept only when every condition holds |
| `ScanScopeConditionInput` | `attribute`: one of the type's attributes; `key`: required for a `STRING_MAP` attribute and forbidden for a `STRING` one, since a map is matched one key at a time; `operator: ScanScopeOperator!`; `values`: omitted for `EXISTS`, otherwise 1 to 50 non-empty strings, and the condition holds when any matches; `negate: Boolean`: inverts this condition only |
| `ScanScopeOperator` | `EXISTS` (attribute or key is set; no values), `EQUALS`, `CONTAINS`, `STARTS_WITH`, `ENDS_WITH`; matching is case-sensitive |

A condition on an attribute the resource lacks is false before negation, so a negated
`tags.Environment EQUALS prod` keeps a resource with no tags [G].

Bruno's create and update examples send `scope[].filters` as a list of `{key, value}`
pairs [B]; the schema has no `filters` field on `ScanScopeInput` and defines `filter`
with `conditions` [G]. The schema wins, and the Bruno example would be rejected by
input validation [I: GraphQL rejects unknown input fields].

### 5.4 Scan runs **[G]**

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | ULID |
| `scan` | `Scan!` | Parent scan |
| `providerConfigs`, `scope` | lists | Snapshot of the scan's configuration when the run was triggered |
| `state` | `ScanRunState!` | Lifecycle state |
| `workerUlid` | `String` | Worker that picked the run up |
| `resourceCount` | `Int!` | Resources recorded |
| `resourceStats` | `ScanRunResourceStats!` | `managed` (identity matches an entity in some stack's state) and `unmanaged`; counted from stored resources, so both fall to zero once retention removes them. The retention period is *(unverified)* |
| `note` | `String` | Outcome text, such as the failure reason or the note left by whoever canceled it |
| `failures`, `failuresCount` | list, `Int!` | Problems that did not stop the run |
| `searchFailures`, `searchResources` (and suggestions) | search | Paged failures and discovered resources |
| `stateChangedAt` | `Int!` | When the current state was entered; for a terminal state, when the run finished |
| `createdAt` | `Int!` | When the run was queued |
| `duration` | `Int!` | Seconds since the worker reported pickup, until finish or now; zero before pickup |

`ScanRunState` [G]: `QUEUED`, `SCANNING`, `FINISHED`, `FAILED`, `CANCELED`, `STOPPED`,
`FINISHED_WITH_ERRORS`. The schema gives no descriptions; the transitions below follow
from the mutation descriptions and field names [I]:

| From | Event | To |
|---|---|---|
| none | `scanRunTrigger(scan)` | `QUEUED` |
| `QUEUED` | `scanRunCancel(id, note)`, before a worker picks the run up | `CANCELED` |
| `QUEUED` | A worker picks the run up | `SCANNING` |
| `SCANNING` | `scanRunStop(id, note)`; the state changes once the worker reports back | `STOPPED` |
| `SCANNING` | Every provider configuration and resource type listed | `FINISHED` |
| `SCANNING` | Finished, with `failures` recorded | `FINISHED_WITH_ERRORS` [I: from `failures` being problems that did not stop the run] |
| `SCANNING` | The run could not complete | `FAILED` |

`ScanRunFailure` [G]: `resourceType: String` (null when a whole provider configuration
failed), `providerConfig: ScanProviderConfig!`, `message: String!`.

`ScanResource` [G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Opaque, unique within the run that found it |
| `name` | `String!` | Provider label, not guaranteed unique |
| `type` | `String!` | Provider type name, for example `aws_s3_bucket` |
| `identity` | `String!` | The provider's resource identity as raw JSON; region, account and ARN live here |
| `identityHash` | `String!` | Hash of the identity, for comparison and de-duplication |
| `identitySchemaVersion` | `Int` | Identity schema version the provider reported |
| `isManaged` | `Boolean!` | Whether some stack's state holds an entity with the same identity |
| `scan` | `Scan!` | Discovering scan |
| `createdAt` | `Int!` | When it was recorded |
| `providerConfig` | `ScanProviderConfig` | Provider configuration at discovery time |

Related views [G]: `AwsIntegration.attachedScans` (`AwsIntegrationScanAttachment`:
`scanId`, `scanName`, `spaceDetails`) and `WorkerPool.scans` and `scansCount`
(`WorkerPoolScanReference`: `id`, `name`, `spaceDetails`); see
[12](12-cloud-integrations-and-oidc.md) and [10](10-workers-and-execution.md).

### 5.5 Scan operations **[G]**

| Operation | Kind | Arguments | Returns |
|---|---|---|---|
| `scan` | query | `id: ID!` | `Scan!` |
| `scanRun` | query | `id: ID!` | `ScanRun!` |
| `scanProviderSchemas` | query | `provider: ScanProvider` | `[ScanProviderSchema!]!` |
| `searchScans`, `searchScansSuggestions` | query | `input` | search output, suggestions |
| `scanCreate` | mutation | `input: ScanInput!` | `Scan!` |
| `scanUpdate` | mutation | `id: ID!`, `input: ScanInput!` | `Scan!` |
| `scanDelete` | mutation | `id: ID!` | `Scan` |
| `scanRunTrigger` | mutation | `scan: ID!` | `ScanRun!` |
| `scanRunCancel` | mutation | `id: ID!`, `note: String` | `ScanRun!` |
| `scanRunStop` | mutation | `id: ID!`, `note: String` | `ScanRun!` |

No scheduled scan exists in the schema; runs start only through `scanRunTrigger` [G]
[I: no schedule field on `Scan` and no scheduled-scan type].

## 6. The generic search API

Every paged list in the API and the UI is a `search…` field that takes `SearchInput`
and returns `{edges: [{cursor, node}], pageInfo}`, paired with a `search…Suggestions`
field that describes the filterable fields [G] [B] folder `Advanced/Search
Suggestions`. The schema has 70 fields that take `SearchInput` and 57 that take
`SearchSuggestionsInput` [G], counted by script.

### 6.1 `SearchInput` **[G]**

| Field | Type | Meaning |
|---|---|---|
| `first` | `Int` | Page size |
| `after` | `String` | Cursor; pass the previous page's `pageInfo.endCursor` |
| `requestedPage` | `SearchQueryRequestedPage` | `FIRST`, `NEXT`, `PREVIOUS` or `LAST` |
| `orderBy` | `[SearchQueryOrder!]` | `{field: String!, direction: ASC \| DESC}`, direction defaulting to `ASC`. The field's description says ordering is unimplemented; see below |
| `fullTextSearch` | `String` | A free-text phrase |
| `predicates` | `[SearchQueryPredicate!]` | Field filters |

`PageInfo` [G]: `endCursor: String!`, `startCursor: String!`, `hasNextPage: Boolean!`,
`hasPreviousPage: Boolean!`. `searchRuns` alone also takes `offset: Int`: a client pages
either with `after` (keyset, sequential) or with `offset` (rows skipped, for loading
pages in parallel), never both; offset pages can skip or repeat rows when runs change
during the scan, and `SearchRunsOutput.total` gives the match count for computing page
numbers [G].

Ordering: the schema contradicts itself. The `SearchInput.orderBy` description says
ordering is not implemented [G], but the `searchRuns` description names its orderable
fields (`createdAt`, `triggeredBy`, `state`, where `state` sorts by internal ordinal,
not alphabetically) and defaults to newest first, many other search fields describe
themselves as supporting ordering, and each suggestions field marks which fields are
`orderable` [G]. A saved stacks view also stores a sort order [D] organizing. The
specific descriptions are the better evidence that ordering is honoured for the fields
marked `orderable` [I]; for other fields it is *(unverified)*. spacectl sends
`orderBy` for module versions and policy evaluation records (`createdAt` `DESC`) and
worker pools in `draw` (`position`) [S] (spacectl
`internal/cmd/module/search_version.go#L110-L113`,
`internal/cmd/policy/samples_indexed.go#L148-L152`,
`internal/cmd/draw/data/workerpools.go#L102-L107`). The `stack`, `module`, `policy`,
`blueprint` and `template` list commands build an order (`starred` `DESC` for stacks,
`name` for the rest) but their pagers copy only `first` and `fullTextSearch`, so the
order is never sent [S] (spacectl `internal/cmd/stack/list.go#L102-L108`,
`#L152-L155`; `internal/cmd/template/list.go#L55-L58`, `#L96-L99`). spacectl sends a single object where the schema declares a list, which
GraphQL input coercion accepts [I: GraphQL spec list coercion].

Page size: spacectl pages with `first` at most 50 and follows `endCursor` while
`hasNextPage` [S] (spacectl `internal/cmd/stack/list.go#L142-L182`), and the Terraform
provider's `spacelift_stacks` data source pages at 50 [S]
(`spacelift/data_stacks.go#L84-L87`); spacectl's `draw` command asks for 100 worker
pools per page [S] (spacectl `internal/cmd/draw/data/workerpools.go#L103`).
`intentModuleResources` documents a 50-item maximum page [G]. Whether the search fields enforce a maximum, and what `first`
defaults to, is *(unverified)*.

### 6.2 Predicates and constraints **[G]**

`SearchQueryPredicate`: `field: String!`, `exclude: Boolean` (true excludes the
matching values instead of keeping them), `constraint: SearchQueryFieldConstraint!`.

| Constraint | Type | Semantics |
|---|---|---|
| `booleanEquals` | `[Boolean!]` | Equal to one of the values |
| `enumEquals` | `[String!]` | Equal to one of the enum values |
| `stringEquals` | `[String!]` | Exactly equal to one of the values; case-sensitive, no wildcards, and able to use an index, so preferred when the exact value is known (for example a slug from suggestions) |
| `stringMatches` | `[String!]` | Matches one of the values, where `*` stands for any run of characters |
| `hierarchyNodeValueEquals` | `[String!]` | Contained in one of the given hierarchy nodes, by node `id` |
| `hierarchyNode` | `SearchQueryFieldConstraintHierarchyNodeList` | `nodes: [{equals: String!, includeDescendants: Boolean!}]`; contained in one of the nodes, optionally with descendants |
| `timeInRange` | `SearchQueryFieldConstraintTimeInRange` | `start: Int`, `end: Int` |
| `timeInLast` | `SearchQueryFieldConstraintTimeInLast` | `LAST15M`, `LAST30M`, `LAST1H`, `LAST4H`, `LAST1D`, `LAST7D`, `LAST14D` |

Combination: within one predicate the values are alternatives ("one of") [G]. Across
predicates the conditions are combined with AND *(unverified)*; the Terraform
provider's `spacelift_stacks` data source sends one predicate per filter block, so a
list of `labels` blocks becomes several `label` predicates [S]
(`spacelift/data_stacks.go#L61-L72`). Whether
`stringMatches` is case-sensitive is *(unverified)*; only `stringEquals` is described
as case-sensitive [G].

Field names come from the suggestions output, not from the GraphQL type: for example
`searchStacks` takes `label` (singular), `projectRoot`, `workerPool`, `repository`,
`branch`, `commit`, `name`, `state`, `vendor`, `locked` and `administrative` [S]
(`spacelift/data_stacks.go#L63-L73`), and spacectl filters module versions with
`state` excluding `FAILED` and modules with `terraformProvider`, `space`, `labels` and
`public` [S] (spacectl `internal/cmd/module/search_version.go#L114-L123`,
`internal/cmd/module/mcp.go#L405-L436`). Bruno's note on the suggestions folder makes
the same point: suggestions are how a client discovers valid filter keys [B].

### 6.3 Suggestions **[G]**

`SearchSuggestionsInput`: `fullTextSearch`, `predicates` (the filters already applied,
so counts reflect them), and `fields` (the fields whose values to return; unknown names
are ignored). `SearchSuggestionsOutput`: `filteredCount: Int!` and `fields:
[SearchSuggestionsField!]!`.

| `SearchSuggestionsField` field | Meaning |
|---|---|
| `name` | Field name to use in a predicate or `orderBy` |
| `orderable` | Usable in `orderBy` |
| `filterable` | Usable in a predicate |
| `type` | `SearchSuggestionsFieldType`: `BOOLEAN`, `ENUM`, `STRING`, `HIERARCHY`, `TIME`; set only when filterable |
| `possibleValuesBoolean`, `possibleValuesEnum`, `possibleValuesString` | `values`, `counts` (per value in the current result set), `directCounts` (always null for these) |
| `possibleValuesHierarchy` | `values: [{id, parentId, displayValue}]` (null `parentId` is the root), `counts` (IDs at the node and all descendants), `directCounts` (IDs at the node only) |
| `possibleValuesTime` | `Boolean`; true marks a time field |

The constraint to use follows the type [I]: `booleanEquals` for `BOOLEAN`,
`enumEquals` for `ENUM`, `stringEquals` or `stringMatches` for `STRING`,
`hierarchyNode` or `hierarchyNodeValueEquals` for `HIERARCHY` (the space tree is the
evident hierarchy, *(unverified)*), and `timeInRange` or `timeInLast` for `TIME`.

### 6.4 Every search field **[G]**

Generated by script from the snapshot. "Suggestions field" is the twin on the
same type; "none" means the schema has no twin.

| On | Field | Extra args | Returns | Suggestions field | Status |
|---|---|---|---|---|---|
| `Query` | `apiKeySessionsWithAccessToSpace` | space: ID! | `ApiKeySessionsWithAccessToSpaceOutput!` | `apiKeySessionsWithAccessToSpaceSuggestions` | current |
| `Query` | `apiKeysWithAccessToSpace` | space: ID! | `ApiKeysWithAccessToSpaceOutput!` | `apiKeysWithAccessToSpaceSuggestions` | current |
| `Query` | `openTofuMigrateSearch` | target: StackVendor! | `SearchStacksOutput!` | `openTofuMigrateSuggestions` | current |
| `Query` | `openTofuMigrationQueueSearch` |  | `OpenTofuMigrationQueueSearchOutput!` | `openTofuMigrationQueueSearchSuggestions` | current |
| `Query` | `searchAnsibleHosts` |  | `SearchAnsibleHostsOutput` | `searchAnsibleHostsSuggestions` | current |
| `Query` | `searchAnsibleTasks` |  | `SearchAnsibleTasksOutput` | `searchAnsibleTasksSuggestions` | current |
| `Query` | `searchAuditTrailEntries` |  | `SearchAuditTrailEntriesOutput!` | `searchAuditTrailEntriesSuggestions` | current |
| `Query` | `searchBlueprintDeployments` |  | `SearchBlueprintDeploymentsOutput!` | none | deprecated |
| `Query` | `searchBlueprintVersionedGroups` |  | `SearchBlueprintVersionedGroupsOutput!` | `searchBlueprintVersionedGroupsSuggestions` | deprecated |
| `Query` | `searchBlueprints` |  | `SearchBlueprintsOutput!` | `searchBlueprintsSuggestions` | current |
| `Query` | `searchContexts` |  | `SearchContextsOutput!` | `searchContextsSuggestions` | current |
| `Query` | `searchIntentProjects` |  | `SearchIntentProjectsOutput!` | `searchIntentProjectsSuggestions` | current |
| `Query` | `searchIntentResources` |  | `SearchIntentResourcesOutput!` | `searchIntentResourcesSuggestions` | current |
| `Query` | `searchIntentResourcesOperations` |  | `SearchIntentResourcesOperationsOutput!` | `searchIntentResourcesOperationsSuggestions` | current |
| `Query` | `searchManagedEntities` |  | `SearchManagedEntitiesOutput!` | `searchManagedEntitiesSuggestions` | current |
| `Query` | `searchManagedEntitiesGroupByProviders` |  | `SearchManagedEntitiesGroupedByProvidersOutput!` | none | current |
| `Query` | `searchManagedEntitiesGroupByStacks` |  | `SearchManagedEntitiesGroupedByStacksOutput!` | none | current |
| `Query` | `searchManagedEntitiesGroupByTypes` |  | `SearchManagedEntitiesGroupedByTypesOutput!` | none | current |
| `Query` | `searchModules` | returnModuleSharing: Boolean | `SearchModulesOutput!` | `searchModulesSuggestions` | current |
| `Query` | `searchNamedWebhooksIntegrations` |  | `SearchNamedWebhooksIntegrationOutput!` | `searchNamedWebhooksIntegrationsSuggestions` | current |
| `Query` | `searchNotifications` |  | `SearchNotificationsOutput!` | `searchNotificationsSuggestions` | current |
| `Query` | `searchPolicies` |  | `SearchPoliciesOutput!` | `searchPoliciesSuggestions` | current |
| `Query` | `searchPolicyTemplates` |  | `SearchPolicyTemplatesOutput!` | `searchPolicyTemplatesSuggestions` | current |
| `Query` | `searchRepoCommits` | repoID: ID! | `SearchRepoCommitsOutput!` | `searchRepoCommitsSuggestions` | current |
| `Query` | `searchRepoFiles` | repoID: ID!, commit: String | `SearchRepoFilesOutput!` | `searchRepoFilesSuggestions` | current |
| `Query` | `searchRepos` |  | `SearchReposOutput!` | `searchReposSuggestions` | current |
| `Query` | `searchRevisions` | repoID: ID! | `SearchRevisionsOutput!` | `searchRevisionsSuggestions` | deprecated |
| `Query` | `searchRuns` | offset: Int | `SearchRunsOutput!` | `searchRunsSuggestions` | current |
| `Query` | `searchScans` |  | `SearchScansOutput!` | `searchScansSuggestions` | current |
| `Query` | `searchSpaceContexts` | space: ID! | `SearchSpaceContextsOutput!` | `searchSpaceContextsSuggestions` | current |
| `Query` | `searchSpacePolicies` | space: ID! | `SearchSpacePoliciesOutput!` | `searchSpacePoliciesSuggestions` | current |
| `Query` | `searchSpaces` |  | `SearchSpacesOutput!` | `searchSpacesSuggestions` | current |
| `Query` | `searchStacks` |  | `SearchStacksOutput!` | `searchStacksSuggestions` | current |
| `Query` | `searchTemplateDeployments` |  | `SearchTemplateDeploymentsOutput!` | none | current |
| `Query` | `searchTemplates` |  | `SearchTemplatesOutput!` | `searchTemplatesSuggestions` | current |
| `Query` | `searchTofuWorkspaces` |  | `SearchTofuWorkspacesOutput!` | `searchTofuWorkspacesSuggestions` | current |
| `Query` | `searchVCSIntegrations` |  | `SearchVCSIntegrationsOutput!` | `searchVCSIntegrationsSuggestions` | current |
| `Query` | `searchWorkerPools` |  | `SearchWorkerPoolsOutput!` | `searchWorkerPoolsSuggestions` | current |
| `Query` | `stacksWithAccessToSpace` | space: ID!, mode: StackAccessMode! | `StacksWithAccessToSpaceOutput!` | `stacksWithAccessToSpaceSuggestions` | current |
| `Query` | `userGroupsWithAccessToSpace` | space: ID! | `UserGroupsWithAccessToSpaceOutput!` | `userGroupsWithAccessToSpaceSuggestions` | current |
| `Query` | `userSessionsWithAccessToSpace` | space: ID! | `UserSessionsWithAccessToSpaceOutput!` | `userSessionsWithAccessToSpaceSuggestions` | current |
| `Query` | `usersWithAccessToSpace` | space: ID! | `UsersWithAccessToSpaceOutput!` | `usersWithAccessToSpaceSuggestions` | current |
| `AnsibleHost` | `searchAnsibleTaskExecutions` |  | `SearchAnsibleTaskExecutionsOutput` | `searchAnsibleTaskExecutionsSuggestions` | current |
| `BlueprintDeployment` | `searchChangeHistory` |  | `SearchBlueprintDeploymentChangeHistoryOutput!` | none | current |
| `BlueprintVersionedGroup` | `searchBlueprintVersions` |  | `SearchBlueprintsOutput!` | `searchBlueprintVersionsSuggestions` | current |
| `IntentProject` | `searchIntentResources` |  | `SearchIntentResourcesOutput!` | `searchIntentResourcesSuggestions` | current |
| `IntentProject` | `searchIntentResourcesOperations` |  | `SearchIntentResourcesOperationsOutput!` | `searchIntentResourcesOperationsSuggestions` | current |
| `Module` | `searchModuleVersions` |  | `SearchVersionsOutput!` | `searchModuleVersionsSuggestions` | current |
| `Mutation` | `openTofuMigrate` | target: StackVendor!, triggerRun: Boolean | `OpenTofuMigrateResult!` | `openTofuMigrateSuggestions` (shared with `openTofuMigrateSearch`) | current |
| `Policy` | `searchEvaluationRecords` |  | `SearchPolicyEvaluationRecordOutput!` | `searchEvaluationRecordSuggestions` | current |
| `PublicWorkerPool` | `searchIntentProjects` |  | `SearchIntentProjectReferencesOutput!` | `searchIntentProjectsSuggestions` | current |
| `PublicWorkerPool` | `searchSchedulableRuns` |  | `SearchSchedulableRunsOutput` | `searchSchedulableRunsSuggestions` | current |
| `PublicWorkerPool` | `searchUsers` |  | `SearchWorkerPoolUsersOutput!` | `searchUsersSuggestions` | current |
| `Run` | `searchEntityChanges` | changePhase: String | `SearchEntityChangeOutput!` | `searchEntityChangesSuggestions` | current |
| `Scan` | `searchRuns` |  | `SearchScanRunsOutput!` | `searchRunsSuggestions` | current |
| `ScanRun` | `searchFailures` |  | `SearchScanRunFailuresOutput!` | `searchFailuresSuggestions` | current |
| `ScanRun` | `searchResources` |  | `SearchScanResourcesOutput!` | `searchResourcesSuggestions` | current |
| `Stack` | `searchManagedEntities` |  | `SearchManagedEntitiesOutput!` | `searchManagedEntitiesSuggestions` | current |
| `Stack` | `searchManagedEntitiesGroupByProviders` |  | `SearchManagedEntitiesGroupedByProvidersOutput!` | none | current |
| `Stack` | `searchManagedEntitiesGroupByStacks` |  | `SearchManagedEntitiesGroupedByStacksOutput!` | none | current |
| `Stack` | `searchManagedEntitiesGroupByTypes` |  | `SearchManagedEntitiesGroupedByTypesOutput!` | none | current |
| `Stack` | `searchManagedStateVersions` |  | `ManagedStateVersionOutput` | none | current |
| `Stack` | `searchRunIgnoredTriggers` |  | `SearchRunIgnoredTriggersOutput` | none | current |
| `Template` | `searchTemplateVersions` |  | `SearchTemplateVersionsOutput!` | `searchTemplateVersionsSuggestions` | current |
| `TemplateDeployment` | `searchChangeHistory` |  | `SearchTemplateDeploymentChangeHistoryOutput!` | none | current |
| `VCSIntegration` | `searchUsers` |  | `SearchVCSIntegrationUsersOutput!` | `searchUsersSuggestions` | current |
| `WorkerPool` | `searchIntentProjects` |  | `SearchIntentProjectReferencesOutput!` | `searchIntentProjectsSuggestions` | current |
| `WorkerPool` | `searchSchedulableRuns` |  | `SearchSchedulableRunsOutput` | `searchSchedulableRunsSuggestions` | current |
| `WorkerPool` | `searchUsers` |  | `SearchWorkerPoolUsersOutput!` | `searchUsersSuggestions` | current |
| `WorkerPool` | `searchWorkers` |  | `SearchWorkersOutput` | `searchWorkersSuggestions` | current |

`searchRevisions`, `searchBlueprintVersionedGroups` and `searchBlueprintDeployments`
are deprecated in favour of `searchRepoCommits`, `searchTemplates` and
`searchTemplateDeployments` [G] [RN] (deprecation lists of 2026-10-20 and 2026-07-16).
`openTofuMigrate` is a mutation that selects its stacks with `SearchInput`; its
suggestions twin `openTofuMigrateSuggestions` breaks the naming pattern and also serves
`openTofuMigrateSearch` [G]; report 20 owns it. The fields on `BlueprintDeployment` and
`BlueprintVersionedGroup` carry no deprecation flag, but their parent types are reachable
only through deprecated fields [G] (report 17).

## 7. Saved filters

The UI calls them filter views [D] organizing.

| `SavedFilter` field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Global ID |
| `isPublic` | `Boolean!` | Visible to every user of the account |
| `createdBy` | `String!` | Login of the creator |
| `name` | `String!` | Name |
| `data` | `String!` | JSON of the filter |
| `type` | `String!` | Which list view the filter belongs to |

Operations [G]: `savedFilters(type: String)` (all filters, or those of one view),
`savedFilter(id)`, `savedFilterCreate(input: SavedFilterInput!)` (`isPublic`, `name`,
`data`, `type`, all required), `savedFilterUpdate(id, isPublic: Boolean!, name: String,
data: String)` (visibility always sent, name and data optional),
`savedFilterDelete(id)`.

Behaviour:

- A view saves the selected filters, a search query and the sort order [D] organizing.
  Bruno's example `data` is `{"predicates":[]}` with `type: "STACKS"` [B]; the full
  `data` format and the set of `type` values are *(unverified)*.
- New views are private. An admin can share a view with the whole account [D]
  organizing; this is `isPublic` [I]. Whether the API restricts `isPublic: true` to
  admins is *(unverified)*.
- A user can mark one view as a personal default, applied the next time they open the
  list, and can reset to the Spacelift default view with no filters [D] organizing.
  `SavedFilter` has no default flag [G], so where the default is stored is
  *(unverified)*; the UI has a key/value store for layout preferences,
  `uiConfigGet`/`uiConfigStore` [G] [B] folder `Advanced/UI State`.
- A view can be updated with the current filters, renamed or deleted; a shared view
  cannot be deleted [D] organizing.
- The stacks list searches by name, ID (slug) and label, filters by state with
  predefined tabs, and treats `folder:` labels as nestable folders [D] organizing;
  report 07 owns labels.
- Separately from views, a user can reorder and hide list columns (not the name
  column) and reset them to the default [D] organizing; where that layout is stored is
  *(unverified)*, with `uiConfigStore`, a per-user JSON blob that each call overwrites,
  the evident candidate [G] [I].

## 8. Bulk actions

The UI shows, for a selection, only the actions that apply to at least one selected
entity, marks actions that apply to only some, offers a drawer to inspect and trim the
selection, and asks for confirmation (with optional details such as a lock note)
[D] bulk. Results are shown per item; queued items can be stopped individually or all
at once, and a new action can start from the completed or failed subset [D] bulk. The
user must stay on the results view until every action finishes, or in-progress actions
stop [D] bulk; the execution is therefore driven by the browser, one API call per
entity [I: from that rule and from the absence of bulk mutations other than those
listed below].

| List | Action | Mutation [G] |
|---|---|---|
| Stacks | Approve, Reject | `runReview(stack, run, decision, note)` on the stack's run needing approval |
| Stacks | Confirm, Discard | `runConfirm(stack, run, note)`, `runDiscard(stack, run, note)` on the blocking unconfirmed run |
| Stacks | Disable, Enable | `stackDisable(id)`, `stackEnable(id)` |
| Stacks | Lock, Unlock | `stackLock(id, note)`, `stackUnlock(id)` |
| Stacks | Run task | `taskCreate(stack, command, skipInitialization)` |
| Stacks | Sync commit | `stackSyncCommit(id)`: point the stack at its tracked branch's HEAD |
| Stacks | Trigger | `runTrigger(stack, …)`; `runTriggerBatch(input)` also exists [G], whether the UI uses it is *(unverified)* |
| Stack runs, Runs | Approve, Reject, Confirm, Discard | `runReview`, `runConfirm`, `runDiscard` |
| Modules | Disable, Enable | `moduleDisable(id)`, `moduleEnable(id)` |
| Modules | Favorite, Unfavorite | `starSet(id, star)`; its description says it stars a stack or a module, although it returns `Stack`; `Module.starred` reads the result [G] |
| Notifications | Dismiss | `dismissNotifications(ids)`, also `dismissNotificationGroup(groupingKey)` and `dismissAllNotifications` |
| Private worker pool workers | Drain, Undrain | `workerDrainSet(workerPool, id, drain)` |
| Worker pool queued runs | High, Normal, Low priority (private pools only; a level is offered only if some selected run is not at it) | `runPrioritySet(stack, run, preset)` |
| Worker pool queued runs | Discard (runs in `READY`) | `runDiscard` |
| Run changes | Replan | `runTargetedReplan(stack, run, targets)` |

On the **Runs** page every filtered run is part of the selection [D] bulk. Run
priority values and their semantics are report 08 and report 10.

## 9. Dashboard

### 9.1 Behaviour

- The dashboard is the default landing page; a user can choose another landing page and
  rearrange pinned sidebar pages in personal settings [D] dashboard.
- Every user can see it; the Launch Pad and User Activity widgets are admin-only
  [D] dashboard. Opening it to non-admins dates from 2025-10-17 [RN].
- Users see only stacks and runs in spaces where they have `read` [D] dashboard.
- Predefined views are **Overview**, **Metrics** and **DORA**; users can add their own
  views and pick their widgets and layout [D] dashboard. Where custom views are stored
  is *(unverified)*; no dashboard type exists in the schema, and `uiConfigStore` is the
  evident candidate [I].
- The Stacks state widget excludes modules [RN] (`fix(dashboard): exclude modules from
  Stacks state dashboard element`, 2026-01-13).

### 9.2 Widgets and their data

| View | Widget | Definition | `SpaceliftMetrics` field [G] |
|---|---|---|---|
| Overview | Stack states | Stacks by state | `stacksCountByState` |
| Overview | Runs to be terminated | Runs due for automatic termination in the next seven days, with **Go to runs**, and up to ten closest with a countdown [D] dashboard [RN] 2026-09-17 | `runsScheduledForTermination`: `RunsScheduledForTerminationTile` (`runs`, capped; `totalCount`); runs already past `scheduledTerminationAt` are left out |
| Overview | Approvals, attention lists | Approval policy evaluations and runs needing attention [D] dashboard | `needsApprovalRuns`, `runsRequiringAttention`, `runsRecentlyApproved` (`RunTileWithApproval`) |
| Overview | Drift detection runs | [D] dashboard | `upcomingDriftDetectionSchedules` (`DriftDetectionIntegrationTile`), `recentDriftDetectionProposedRuns` |
| Overview | Favourite and recent stacks, largest stacks | [I: field names] | `favouriteStacks`, `favouriteModules`, `recentStacks(states)`, `largestStacks` (`StackTileWithResources`) |
| Overview | Migrate stacks to native vendors, **Terragrunt** tab | Stacks still to migrate, with one-click migrate all [D] terragrunt | `stacksRequiringAttention.stackTerragruntMigration` (`migrated`, `readyToMigrate`) [I: field names] |
| Overview | Migrate stacks to native vendors, **OpenTofu** tab | Stacks ready to migrate and the migration queue, linking to a filter, preview and migrate page [D] tofu-migration | none in `SpaceliftMetrics`; `openTofuMigrationCounts` (described as meant for the migration dashboard), `openTofuMigrateSearch` and `openTofuMigrationQueueSearch` [G]; report 20 |
| Overview | Active users, Launch Pad, User Activity | [D] dashboard | none in `SpaceliftMetrics`; source *(unverified)* |
| Metrics | Drift detection coverage | Share and number of stacks with drift detection on | `driftDetectionCoverage` |
| Metrics | Stack failures | Stacks entering `failed` per day, 30 days | `stackFailuresRange` |
| Metrics | Number of deployments | Tracked runs finishing successfully per day, 30 days | `numberOfDeploymentsRange` |
| Metrics | Managed resources | Resources by vendor | `resourcesCountByVendor` |
| Metrics | Median run duration | Median of run durations, 30 days | `medianRunDuration`, `medianRunDurationRange` |
| Metrics | Average run duration | Mean of run durations, 30 days | `averageRunDuration` (nanoseconds [G]), `averageRunDurationRange` |
| Metrics | Resources health | Healthy against drifted resources | `resourcesCountByDrift` |
| DORA | Deployment frequency | `applying` to `finished` transitions per day | `deploymentFrequencyRateValue` |
| DORA | Lead time for changes | From PR opened to deployment finished: (run created minus PR opened) plus run duration; `finished` runs only | `averageLeadTimeForChangesRange`/`Value`, `medianLeadTimeForChangesRange`/`Value` |
| DORA | Change failure rate | `failed` over (`failed` plus `finished`) | `changeFailureRateRange`, `changeFailureRateValue` |
| DORA | Mean time to recovery | From a `failed` run to the next successful run on the same stack | `averageTimeToRecoveryRange`/`Value`, `medianTimeToRecoveryRange`/`Value` |

Definitions in the Metrics and DORA rows are [D] dashboard. DORA counts only tracked
runs in stacks that are neither deleted nor modules, and `canceled`, `stopped`,
`discarded` and `skipped` runs count in no DORA metric [D] dashboard.

Other `SpaceliftMetrics` fields [G]: `maxStackSizeByResourceCount`,
`avgStackSizeByResourceCount`, `resourcesCountRange`, `hasStacks`, `hasRuns`, and
`averageTimeToMergeRange` and `medianTimeToMergeRange` (seconds per day), which no docs
page names.

Units [G]: the lead time and recovery series are seconds; the change failure rate is a
ratio; run durations are nanoseconds for `averageRunDuration` (the unit of the other
duration series is *(unverified)*). `…Range` fields are per-day series and `…Value`
fields a single figure for the whole range [G].

`DataPoint` [G]: `timestamp: Int!`, `value: Float!`, `labels: [String!]!`.
`metrics` takes no arguments; `metricsRange(input: MetricsInput)` takes `startTime` and
`endTime` (`Int!`, Unix seconds per the Bruno example) [G] [B]. The window `metrics`
uses when no range is given is *(unverified)*; the docs describe 30-day widgets [D]
dashboard.

Tiles [G]:

| Tile | Fields |
|---|---|
| `StackTile` | `name`, `slug`, `state: StackState!`, `spaceTile` (`SpaceTile`: `name`, `slug`) |
| `StackTileWithResources` | `stackTile`, `resourcesCount` |
| `RunTile` | `id` (run ULID), `title`, `state: RunState!`, `createdAt`, `type: RunType!`, `needsApproval`, `stackTile` |
| `RunTileWithApproval` | `runTile`, `approval: Boolean!` |
| `RunScheduledForTerminationTile` | `runTile`, `scheduledTerminationAt: Int!` |
| `DriftDetectionIntegrationTile` | `driftDetetionIntegration` (spelled so in the schema), `stackTile` |
| `StacksAttentionTile` | `stackTerragruntMigration` |

## What this means for infraweave

- **Entity inventory.** A per-stack and account-wide store of managed entities, filled
  from run state for all seven vendors, with creator and updater runs, drift time,
  vendor metadata and the three groupings with counts and drift counts.
- **Value hiding.** The entity drawer never shows string values, only a seven-character
  checksum prefix; sensitive outputs keep only a hash.
- **Change records.** Per-run entity changes by phase (`DRIFT`, `PLAN`, `APPLY`), the
  16 change types, the six results and attribute-level before and after values with an
  "unknown after apply" flag; the Terraform-family-only rule for the attribute-level
  view; `Delta` counters on runs and stacks.
- **Ansible view.** Hosts, tasks and task executions with seven statuses.
- **Scans.** Scan definitions per space, provider schemas with resource types,
  parameters and filterable attributes, the filter grammar and its limits (20
  conditions, 50 values, throttle 1 ms to 1 min, default 5 s), runs on worker pools
  with seven states, failures, discovered resources with identity hashing, and the
  managed or unmanaged match against stack state. The tier flag is `SCANS`.
- **One search contract.** One request shape for every list: cursor and page paging,
  full-text search, typed predicates with exclusion, wildcards, hierarchy and time
  constraints, ordering, and a suggestions twin that returns filterable fields with
  per-value counts. The field vocabulary per entity is part of the contract that
  clients such as spacectl and the Terraform provider depend on.
- **Saved filters.** Per-view, private or account-public, with a personal default.
- **Bulk actions.** Bulk actions need per-entity endpoints that report per-item success
  or failure. Spacelift drives them from the browser; infraweave must decide whether
  to offer a server-side batch.
- **Dashboard.** Metrics and DORA series over a time range, respecting space read
  access, with the documented DORA formulas.

## Inferred and undocumented

- Scans have no docs page; everything in section 5 is from the schema and Bruno. The
  state transitions, the use of Terraform provider list capabilities and the meaning
  of `FINISHED_WITH_ERRORS` are inferred.
- How a worker pool becomes discovery-type is undocumented, and the schema has no
  marker for it.
- Predicates combining with AND, whether `orderBy` is honoured for fields not marked
  `orderable`, the maximum page size, and the case sensitivity of `stringMatches` are
  unverified. That ordering works for `orderable` fields is inferred from the
  `searchRuns` description and the suggestions flags, against the generic `orderBy`
  description.
- Bulk actions running as one browser-driven call per entity is inferred from the docs'
  warning about leaving the results view.
- Where personal default filter views, column layouts, custom dashboard views and the
  configuration management toggle are stored is unverified.
- `ManagedEntity.thirdPartyMetadata` has no documented writer.
- Mapping `Delta` counters to change types is inferred from names.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e` |
| Self-hosted docs | `latest`, v7.1.0 |
| spacelift-api-bruno | `7cad4574954b` |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| spacectl | `6bf167cb62a6` |

## Sources

- <https://docs.spacelift.io/concepts/resources>
- <https://docs.spacelift.io/concepts/resources/configuration-management>
- <https://docs.spacelift.io/product/bulk-actions>
- <https://docs.spacelift.io/product/dashboard>
- <https://docs.spacelift.io/concepts/stack/organizing-stacks>
- <https://docs.spacelift.io/concepts/stack/drift-detection>
- <https://docs.spacelift.io/vendors/terraform/terragrunt>
- <https://docs.spacelift.io/vendors/opentofu/migration>
- <https://docs.spacelift.io/vendors/ansible/configuration-management> (an older page on the
  same Ansible view; it adds nothing to config-mgmt)
- <https://docs.spacelift.io/product/changelog> (entries of 2026-10-20, 2026-09-23,
  2026-09-17, 2026-07-16, 2026-03-03, 2026-01-13, 2025-10-17 and 2025-10-15)
- [`introspection.json`](../../../api/reference/spacelift/introspection.json)
- [`bruno.json`](../../../api/reference/spacelift/bruno.json): folders `Resources`,
  `Ansible`, `Scans`, `Saved Filters`, `Advanced/Search Suggestions`,
  `Advanced/UI State`; requests `Get Metrics`, `Get Metrics Range`
- <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/structs/search.go>
- <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/list.go#L102-L182>
- <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/stack_selector.go#L167-L196>
- <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/module/search_version.go#L106-L125>
- <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/module/mcp.go#L400-L447>
- <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/policy/samples_indexed.go#L138-L155>
- <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/template/list.go#L55-L99>
- <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/draw/data/workerpools.go#L100-L108>
- <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/structs/wait.go#L139-L147>
- <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/data_stacks.go#L36-L87>
- <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/data_modules.go#L35-L64>
- <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/structs/search/predicates/predicates.go>
- <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/structs/search/predicates/fields.go>
