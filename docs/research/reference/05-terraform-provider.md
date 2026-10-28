# 05: The Spacelift Terraform provider

Checked against: docs read 2026-10-28; schema `b3255fe38f5e`; the snapshots pinned in
[`sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml) (spacelift-api-bruno
`7cad4574954b`, terraform-provider-spacelift v1.57.0); terraform-provider-spacelift source at
`c72b9bd2f3c3` (tag v1.57.0).

Research baseline: **2026-10-28**.

This report records what
[terraform-provider-spacelift](https://github.com/spacelift-io/terraform-provider-spacelift)
v1.57.0 sends to Spacelift's GraphQL API: how it authenticates, which root fields
each of its 56 resources and 62 data sources calls, which REST routes of the
specification reference those fields map to, how each resource is imported, and
which attributes have no input field of the same name. It also records the
provider behaviour that a REST-native infraweave provider has to match or
deliberately replace. Report [01](01-spacelift-landscape.md) covers the snapshots
and the specification pipeline, [02](02-graphql-to-rest-mapping.md) how root
fields become routes, and reports 03 and 04 the operations by family. Engineers
building infraweave's provider and its parity tests need this report.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` |
| **[S]** | terraform-provider-spacelift source at `c72b9bd2f3c3`, or this repository's committed snapshots and `internal/specref` code |
| **[I]** | Inferred; the sentence says from what |

## 1. What is extracted

| Snapshot | How | Gives |
|---|---|---|
| `terraform-schema.json` | `terraform providers schema -json` against the pinned release, in a scratch directory | 56 resources and 62 data sources: attribute types, required/optional/computed, sensitivity, deprecation, descriptions, nested blocks |
| `terraform-operations.json` | `go/ast` over `spacelift/resource_*.go` and `data_*.go` at the release tag | the GraphQL root fields each resource calls (54 resources and 56 data sources call at least one) |

Loading a provider's schema never configures the provider, so no Spacelift
credentials are involved **[S]** (`FetchTerraformSchema` in
[`internal/specref/terraform.go`](../../../internal/specref/terraform.go)).

The provider declares its operations as struct tags, for example
`graphql:"stackCreate(input: $input)"`. Tags that name a nested selection
rather than a root field are discarded by `RetainRootFields`.

Two limits of `terraform-operations.json` matter when reading it **[S]**:

- **Selection names collide with root fields.** `id`, `name` and `type` are root
  queries (the account's identity) **[G]**, so the extractor keeps them wherever
  the provider selects a nested field of that name. Only `spacelift_account` calls
  `name` as a root field ([`data_account.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/data_account.go#L37-L44)).
  The tables in section 4 list the others as "nested".
- **Calls in shared helpers are missed.** Eight entries have no operations in the
  file because their calls live in another file: the two deprecated aliases
  `spacelift_stack_aws_role` and `spacelift_stack_gcp_service_account` (resource and
  data source each), and the data sources `spacelift_current_stack`,
  `spacelift_drift_detection`, `spacelift_gcp_service_account` and
  `spacelift_role_actions`. The run polling in
  [`internal/structs/wait.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/structs/wait.go#L140-L160)
  is missed for `spacelift_run` and `spacelift_task`. Section 4 supplements these
  from the source.

## 2. Attribute matching

For each resource, `specref` gathers the input objects of the mutations the
resource calls, in this order:

1. create and attach mutations;
2. update mutations;
3. all other mutations.

Within each group, it gathers the nested input objects first, then the
`<Op>Request` component, then the path parameters (`path:<op>`).

Each attribute then lands on the first input field matching one of these
candidates:

1. the camelCase name (`administrative` → `administrative`);
2. the name with a write-only `_wo` suffix dropped (`secret_wo` → `secret`);
3. `_id` dropped, then `_id` replaced by `ID` (`space_id` → `space`, then `spaceID`);
4. `_ids` replaced by `s` (`worker_pool_ids` → `workerPools`).

The input objects are tried in the order above, and within each, the candidates
in this order **[S]** (`matchField` and `inputNamespaces` in
[`internal/specref/enrich.go`](../../../internal/specref/enrich.go)). The `id`
attribute and computed-only attributes are skipped.

Where the provider names a field differently, the `terraform:` section of
[`rest-mapping.yaml`](../../../api/reference/spacelift/rest-mapping.yaml) holds the rename; for example `spacelift_stack`
`enable_local_preview` → `localPreviewEnabled`. A rename that points at a field
that does not exist fails generation. The section holds five renames **[S]**:

| Resource | Attribute | Input field |
|---|---|---|
| `spacelift_module` | `enable_local_preview` | `localPreviewEnabled` |
| `spacelift_stack` | `allow_run_promotion` | `githubActionDeploy` |
| `spacelift_stack` | `enable_local_preview` | `localPreviewEnabled` |
| `spacelift_stack` | `enable_sensitive_outputs_upload` | `enableSensitiveOutputUpload` |
| `spacelift_worker_pool` | `csr` | `certificateSigningRequest` |

Matched properties carry `x-terraform: [resource.attribute]` and fall back to
the provider's description when GraphQL has none.

### What does not map

[`coverage.md`](../../../api/reference/spacelift/coverage.md) lists, per resource:

- **Unmapped attributes.** For example the `spacelift_context` hook lists
  (`before_init` and the rest), which the provider sends through a separate
  `hooks` input. Another example is the `module_id` versus `stack_id` choice on
  attachment resources, which GraphQL takes as one generic ID.
- **Terraform-only attributes.** The `*_wo_version` counters of write-only
  arguments exist only to make Terraform re-send a secret, and `keepers` only
  forces replacement, so no API field matches them.
- **Nested blocks.** Examples are `vendor` blocks on `spacelift_stack` and the
  VCS blocks on `spacelift_module`. The provider folds these into the GraphQL
  `vendorConfig` and `provider` fields. They are listed but not mapped yet.

The last column of the section 4 tables copies these lists. Matching is by name
only, so many "unmapped" attributes are sent under another name or as a plain
argument. Section 5 records how the provider actually sends the ones that matter.

## 3. Provider configuration and transport

### Settings

| Setting | Environment variable | Sensitive | Purpose |
|---|---|---|---|
| `api_key_endpoint` | `SPACELIFT_API_KEY_ENDPOINT` | no | Account URL, such as `https://<account>.app.spacelift.io`, used outside Spacelift |
| `api_key_id` | `SPACELIFT_API_KEY_ID` | no | API key ID, used outside Spacelift |
| `api_key_secret` | `SPACELIFT_API_KEY_SECRET` | yes | API key secret, used outside Spacelift |
| `api_token` | `SPACELIFT_API_TOKEN` | yes | Bearer token Spacelift injects into runs, used inside Spacelift |

All four are optional, and each defaults from its environment variable **[S]**
([`provider.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/provider.go#L25-L54)).
The docs describe the same pair of modes: inside Spacelift the provider needs no
configuration because runs of stacks with attached roles receive
`SPACELIFT_API_TOKEN`, and outside Spacelift it takes an API key and the account
endpoint; provider aliases address several accounts **[D]**
(<https://docs.spacelift.io/vendors/terraform/terraform-provider>).

Selection rules **[S]**
([`validateProviderConfig`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/provider.go#L210-L236)):

1. When all three `api_key_*` settings are set, the API key wins.
2. Otherwise, when `api_token` is set, the token is used.
3. Otherwise configuration fails and names the missing `api_key_*` settings.

### Authentication flow

- **API key.** The provider trims a trailing `/` from the endpoint, appends
  `/graphql`, and sends the `apiKeyUser(id, secret)` mutation, selecting `jwt`. A
  null result means the key ID is wrong. This exchange uses its own HTTP client,
  with retries and without the authorization header **[S]**
  ([`buildClientFromAPIKeyParams`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/provider.go#L272-L300)).
  `apiKeyUser` is a root mutation **[G]**.
- **Token to endpoint.** For both modes the provider parses the JWT without
  verifying it and takes the API endpoint from the `aud` claim, which must hold
  exactly one value. The client then posts to `<aud>/graphql` **[S]**
  ([`buildClientFromToken`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/provider.go#L238-L261)).
  The docs confirm that the injected token holds the full API endpoint address
  **[D]** (<https://docs.spacelift.io/vendors/terraform/terraform-provider>); that
  `aud` is the claim holding it is shown only by the provider code **[S]**.
- **In-run token limits.** The docs say the injected token is valid for three
  hours, but only while the run that produced it is in the Planning, Applying or
  Performing state. A stack without role attachments receives a token that
  cannot modify any Spacelift resource. With role attachments, the token manages
  stacks, contexts, integrations and configuration, but cannot create or read
  state, runs, tasks or their logs, and cannot read write-only configuration
  elements **[D]** (<https://docs.spacelift.io/vendors/terraform/terraform-provider>).
  `spacelift_run` and `spacelift_task` still call `runTrigger` and `taskCreate`,
  so they need a token with more access than an in-run stack token, such as an
  API key **[I]** (from the docs' limits and the mutations those resources send).
- **In-run identity.** `spacelift_current_stack` and `spacelift_current_space` read
  the token's claims without an API call for the stack: `iss` must be
  `spacelift`, and `sub` is a path whose directory part is the stack or module ID
  **[S]** ([`getStackIDFromToken`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/data_current_space.go#L58-L79)).
  The last path segment of `sub` is the run ID *(unverified)*: the provider
  discards it, and no docs page names it.
- **No refresh.** The provider exchanges the API key once per configure and holds
  the JWT for the rest of the process **[S]** (no refresh path in
  [`internal/client.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/client.go#L16-L43)).

### Requests

Every request after authentication carries these headers **[S]**
([`getRequestOptions`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/client.go#L95-L108),
[`Mutate` and `Query`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/client.go#L45-L61)):

| Header | Value |
|---|---|
| `Authorization` | `Bearer <jwt>` |
| `Spacelift-Client-Type` | `provider` |
| `Spacelift-Provider-Commit` | build commit |
| `Spacelift-Provider-Version` | build version |
| `Spacelift-GraphQL-Query` or `Spacelift-GraphQL-Mutation` | a client-chosen operation label, such as `StackRead` |
| `Spacelift-Provider-Max-RPS`, `Spacelift-Provider-Max-Request-Burst` | sent only when client-side rate limiting is on |

Transport behaviour **[S]**
([`client`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/client.go#L63-L89),
[`getRateLimit`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/provider.go#L302-L322)):

- Each HTTP attempt times out after one minute.
- Requests go through `go-retryablehttp` with its default policy, so connection
  errors and retryable HTTP statuses are retried by the library, not by
  resource code.
- Client-side rate limiting is off unless both `SPACELIFT_MAX_REQUESTS_PER_SECOND`
  and `SPACELIFT_MAX_REQUESTS_BURST` are set; then a token bucket throttles every
  request. The provider has no handling of its own for a server-side rate limit.

### Errors and missing objects

- **Not found is not an error on reads.** `Query` returns success with an empty
  result when the error text contains `not found` **[S]**
  ([`Query`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/client.go#L52-L61)).
  Reads then drop the resource from state with a warning naming the resource and
  ID **[S]** ([`removeFromState`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/remove_from_state.go#L10-L20)).
  So the provider relies on the server's error message text, and a GraphQL
  query for a missing object answers with an error whose message holds
  `not found` **[I]** (from this string match being the only not-found test).
- **Error formatting.** GraphQL errors are joined as `message: key: value` with
  their `extensions`; an error containing `unauthorized` instead gains a hint
  that a stack's role can override space-level permissions and that a space ID,
  not a name, is expected **[S]**
  ([`FromSpaceliftError`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/error.go#L11-L48)).
- **Failed updates keep the old state.** When a stack update or vendor migration
  fails, the provider re-reads the stack and, if that read fails too, marks the
  state partial so Terraform keeps the previous values rather than the plan
  **[S]** ([`readAfterFailedMutation`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1058-L1070)).

### Plugin protocol

The binary serves protocol 6 through `tf6muxserver`. It muxes the SDKv2 provider,
upgraded from protocol 5, with a Plugin Framework provider **[S]**
([`main.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/main.go#L28-L41)).
At v1.57.0 the Framework side serves one resource, `spacelift_stack_dependency`,
and no data sources **[S]**
([`provider_framework.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/provider_framework.go#L126-L134)).
That is why `spacelift_stack_dependency` is absent from the SDKv2 `ResourcesMap`
in `provider.go` but present in `terraform-schema.json`.

## 4. Resources and data sources by family

The tables were generated by script from `terraform-schema.json`,
`terraform-operations.json`, `introspection.json`, `coverage.md` and
`spacelift.openapi.yaml`, with the provider's `examples/resources/*/import.sh`
files for import IDs **[S]**. Columns:

- **GraphQL operations.** Root fields from `terraform-operations.json`, checked
  against the schema: `(q)` a query, `(m)` a mutation, `deprecated` when the
  schema deprecates the field **[G]**. "nested" lists names the extractor kept
  that the provider only selects inside another field (section 1). Rows with a
  trailing note are supplemented from the cited source file.
- **REST routes (spec).** The specification reference's routes whose `x-graphql`
  names that root field, plus field routes for nested fields with arguments
  that the provider selects under one of its root fields, such as
  `GET /stacks/{stackId}/config-element` for `stack(id).configElement(id)`.
  "excluded" means `rest-mapping.yaml` drops the field as deprecated or
  superseded.
- **Import ID.** The `terraform import` ID from the provider's example, or, where
  it has none, from the resource's importer and the ID its create sets **[S]**.
  "Passthrough" means the importer stores the string as the ID unchanged.
- **Attributes with no input field.** From `coverage.md` (section 2). "Terraform-only"
  attributes never reach the API.
- **Arguments** (data sources). Required attributes and optional attributes that
  are not also computed: the lookup keys and filters. Where a data source looks
  up by optional and computed attributes, or filters with nested blocks, the cell
  names those as well **[S]** (`terraform-schema.json`).

<!-- 56 resources, 62 data sources -->

### Stacks, runs and dependencies

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_run` | `runTrigger` (m); `stack` (q); `stack(id).runResourceState(id)` is polled from `internal/structs/wait.go` while `wait` is set | `GET /stacks/{stackId}/run-resource-state`<br>`POST /stacks/{stackId}/runs`<br>`GET /stacks/{stackId}` | No importer | `proposed`; Terraform-only: `keepers`; blocks: `runtime_config`, `timeouts`, `wait` |
| `spacelift_stack` | `stack` (q); `stackCreate` (m); `stackDelete` (m); `stackMigrateVendor` (m); `stackUpdate` (m); `stateUploadUrl` (m) | `GET /stacks/{stackId}`<br>`POST /stacks`<br>`DELETE /stacks/{stackId}`<br>`POST /stacks/{stackId}/migrate-vendor`<br>`PUT /stacks/{stackId}`<br>`POST /state/upload-url` | `$STACK_ID` | `import_state`, `import_state_file`, `prevent_changes_when_locked`, `terraform_external_state_access`, `terraform_smart_sanitization`, `terraform_workflow_tool`, `terraform_workspace`; blocks: `ansible`, `azure_devops`, `bitbucket_cloud`, `bitbucket_datacenter`, `cloudformation`, `github_enterprise`, `gitlab`, `kubernetes`, `opentofu`, `origin`, `pulumi`, `raw_git`, `showcase`, `spacelift_repo`, `terragrunt` |
| `spacelift_stack_activator` | `stack` (q); `stackDisable` (m); `stackEnable` (m) | `GET /stacks/{stackId}`<br>`POST /stacks/{stackId}/disable`<br>`POST /stacks/{stackId}/enable` | Passthrough of `activator-<unix time>`; import does not set `stack_id` | `enabled`, `stack_id` |
| `spacelift_stack_dependency` | `stack` (q); `stackDependencyCreate` (m); `stackDependencyDelete` (m); `stackDependencyUpdate` (m) | `GET /stacks/{stackId}/dependency`<br>`GET /stacks/{stackId}`<br>`POST /stack-dependencies`<br>`DELETE /stack-dependencies/{stackDependencyId}`<br>`PUT /stack-dependencies/{stackDependencyId}` | `stack-id/depends-on-stack-id` | none |
| `spacelift_stack_dependency_reference` | `stack` (q); `stackDependenciesAddReference` (m); `stackDependenciesDeleteReference` (m); `stackDependenciesUpdateReference` (m); nested: `id` | `GET /stacks/{stackId}/dependency`<br>`GET /stacks/{stackId}`<br>`POST /stack-dependencies/{stackDependencyId}/references`<br>`DELETE /stack-dependency-references/{referenceId}`<br>`PUT /stack-dependency-references/{referenceId}` | `stack-id/depends-on-stack-id/input-name` | none |
| `spacelift_stack_destructor` | `runDiscardAll` (m); `stack` (q); `stackDelete` (m); nested: `id` | `POST /stacks/{stackId}/runs/discard-all`<br>`GET /stacks/{stackId}`<br>`DELETE /stacks/{stackId}` | Passthrough of `destructor-<unix time>`; import does not set `stack_id` | `deactivated`, `discard_runs`; blocks: `timeouts` |
| `spacelift_task` | `stack` (q); `taskCreate` (m); `stack(id).runResourceState(id)` is polled from `internal/structs/wait.go` while `wait` is set | `GET /stacks/{stackId}/run-resource-state`<br>`GET /stacks/{stackId}`<br>`POST /stacks/{stackId}/tasks` | Passthrough of `$STACK_ID/$RUN_ID`; read is a no-op | `init`; Terraform-only: `keepers`; blocks: `timeouts`, `wait` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_current_stack` | none; no API call: reads the stack ID from the `sub` claim of the in-run token (`data_current_stack.go`) | none | none |
| `spacelift_stack` | `stack` (q) | `GET /stacks/{stackId}` | `additional_project_globs`, `after_run`, `git_sparse_checkout_paths`, `stack_id` |
| `spacelift_stack_outputs` | `stack` (q) | `GET /stacks/{stackId}` | `stack_id` |
| `spacelift_stacks` | `searchStacks` (q) | `POST /stacks/search` | filter blocks: `administrative`, `branch`, `commit`, `labels`, `locked`, `name`, `project_root`, `repository`, `state`, `vendor`, `worker_pool` |


### Scheduling and drift detection

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_drift_detection` | `stack` (q); `stackIntegrationDriftDetectionCreate` (m); `stackIntegrationDriftDetectionDelete` (m); `stackIntegrationDriftDetectionUpdate` (m); nested: `id` | `GET /stacks/{stackId}`<br>`POST /stacks/{stackId}/drift-detection`<br>`DELETE /stacks/{stackId}/drift-detection`<br>`PUT /stacks/{stackId}/drift-detection` | `stack/$STACK_ID` or `module/$MODULE_ID` | none |
| `spacelift_scheduled_delete_stack` | `stack` (q); `stackScheduledDeleteCreate` (m); `stackScheduledDeleteDelete` (m); `stackScheduledDeleteUpdate` (m) | `GET /stacks/{stackId}/scheduled-delete`<br>`GET /stacks/{stackId}`<br>`POST /stacks/{stackId}/scheduled-deletes`<br>`DELETE /stacks/{stackId}/scheduled-deletes/{scheduledDeleteId}`<br>`PUT /stacks/{stackId}/scheduled-deletes/{scheduledDeleteId}` | `$STACK_ID/$SCHEDULED_DELETE_STACK_ID` | `at`, `delete_resources`, `schedule_id` |
| `spacelift_scheduled_run` | `stack` (q); `stackScheduledRunCreate` (m); `stackScheduledRunDelete` (m); `stackScheduledRunUpdate` (m) | `GET /stacks/{stackId}/scheduled-run`<br>`GET /stacks/{stackId}`<br>`POST /stacks/{stackId}/scheduled-runs`<br>`DELETE /stacks/{stackId}/scheduled-runs/{scheduledRunId}`<br>`PUT /stacks/{stackId}/scheduled-runs/{scheduledRunId}` | `$STACK_ID/$SCHEDULED_RUN_ID` | `at`, `every`, `schedule_id`; blocks: `runtime_config` |
| `spacelift_scheduled_task` | `stack` (q); `stackScheduledTaskCreate` (m); `stackScheduledTaskDelete` (m); `stackScheduledTaskUpdate` (m) | `GET /stacks/{stackId}/scheduled-task`<br>`GET /stacks/{stackId}`<br>`POST /stacks/{stackId}/scheduled-tasks`<br>`DELETE /stacks/{stackId}/scheduled-tasks/{scheduledTaskId}`<br>`PUT /stacks/{stackId}/scheduled-tasks/{scheduledTaskId}` | `$STACK_ID/$SCHEDULED_TASK_ID` | `at`, `every`, `schedule_id` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_drift_detection` | `stack` (q); reuses the resource read, `stack(id).integrations.driftDetection` (`data_drift_detection.go`) | `GET /stacks/{stackId}` | `ignore_state`, `stack_id` |
| `spacelift_scheduled_delete_stack` | `stack` (q) | `GET /stacks/{stackId}/scheduled-delete`<br>`GET /stacks/{stackId}` | `scheduled_delete_stack_id` |
| `spacelift_scheduled_run` | `stack` (q) | `GET /stacks/{stackId}/scheduled-run`<br>`GET /stacks/{stackId}` | `scheduled_run_id` |
| `spacelift_scheduled_task` | `stack` (q) | `GET /stacks/{stackId}/scheduled-task`<br>`GET /stacks/{stackId}` | `scheduled_task_id` |


### Contexts and configuration

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_context` | `context` (q); `contextCreateV2` (m); `contextDelete` (m); `contextUpdateV2` (m) | `GET /contexts/{contextId}`<br>`POST /contexts`<br>`DELETE /contexts/{contextId}`<br>`PUT /contexts/{contextId}` | `$CONTEXT_ID` | `after_apply`, `after_destroy`, `after_init`, `after_perform`, `after_plan`, `after_run`, `before_apply`, `before_destroy`, `before_init`, `before_perform`, `before_plan` |
| `spacelift_context_attachment` | `context` (q); `contextAttach` (m); `contextDetach` (m) | `GET /contexts/{contextId}/attached-stack`<br>`GET /contexts/{contextId}`<br>`PUT /stacks/{stackId}/contexts/{contextId}`<br>`DELETE /context-attachments/{attachmentId}` | `$CONTEXT_ID/$STACK_ID` | `context_id`, `module_id` |
| `spacelift_environment_variable` | `context` (q); `contextConfigAdd` (m); `contextConfigDelete` (m); `module` (q); `stack` (q); `stackConfigAdd` (m); `stackConfigDelete` (m) | `GET /contexts/{contextId}/config-element`<br>`GET /modules/{moduleId}/config-element`<br>`GET /stacks/{stackId}/config-element`<br>`GET /contexts/{contextId}`<br>`POST /contexts/{contextId}/configs`<br>`DELETE /contexts/{contextId}/configs/{configId}`<br>`GET /modules/{moduleId}`<br>`GET /stacks/{stackId}`<br>`POST /stacks/{stackId}/configs`<br>`DELETE /stacks/{stackId}/configs/{configId}` | `context/$CONTEXT_ID/$ENVIRONMENT_VARIABLE_NAME` or `module/$MODULE_ID/$ENVIRONMENT_VARIABLE_NAME` or `stack/$STACK_ID/$ENVIRONMENT_VARIABLE_NAME` | `module_id`, `name`; Terraform-only: `value_wo_version` |
| `spacelift_mounted_file` | `context` (q); `contextConfigAdd` (m); `contextConfigDelete` (m); `module` (q); `stack` (q); `stackConfigAdd` (m); `stackConfigDelete` (m) | `GET /contexts/{contextId}/config-element`<br>`GET /modules/{moduleId}/config-element`<br>`GET /stacks/{stackId}/config-element`<br>`GET /contexts/{contextId}`<br>`POST /contexts/{contextId}/configs`<br>`DELETE /contexts/{contextId}/configs/{configId}`<br>`GET /modules/{moduleId}`<br>`GET /stacks/{stackId}`<br>`POST /stacks/{stackId}/configs`<br>`DELETE /stacks/{stackId}/configs/{configId}` | `context/$CONTEXT_ID/$MOUNTED_FILE_ID` or `module/$MODULE_ID/$MOUNTED_FILE_ID` or `stack/$STACK_ID/$MOUNTED_FILE_ID` | `content`, `content_wo`, `module_id`, `relative_path`; Terraform-only: `content_wo_version` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_context` | `context` (q) | `GET /contexts/{contextId}` | `after_run`, `context_id` |
| `spacelift_context_attachment` | `context` (q) | `GET /contexts/{contextId}/attached-stack`<br>`GET /contexts/{contextId}` | `context_id`, `module_id`, `stack_id` |
| `spacelift_contexts` | `contexts` (q) | `GET /contexts` | filter block: `labels` |
| `spacelift_environment_variable` | `context` (q); `module` (q); `stack` (q) | `GET /contexts/{contextId}/config-element`<br>`GET /modules/{moduleId}/config-element`<br>`GET /stacks/{stackId}/config-element`<br>`GET /contexts/{contextId}`<br>`GET /modules/{moduleId}`<br>`GET /stacks/{stackId}` | `context_id`, `module_id`, `name`, `stack_id` |
| `spacelift_mounted_file` | `context` (q); `module` (q); `stack` (q) | `GET /contexts/{contextId}/config-element`<br>`GET /modules/{moduleId}/config-element`<br>`GET /stacks/{stackId}/config-element`<br>`GET /contexts/{contextId}`<br>`GET /modules/{moduleId}`<br>`GET /stacks/{stackId}` | `context_id`, `module_id`, `relative_path`, `stack_id` |


### Policies

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_policy` | `policy` (q); `policyCreatev2` (m); `policyDelete` (m); `policyUpdatev2` (m) | `GET /policies/{policyId}`<br>`POST /policies`<br>`DELETE /policies/{policyId}`<br>`PUT /policies/{policyId}` | `$POLICY_ID` | none |
| `spacelift_policy_attachment` | `policy` (q); `policyAttach` (m); `policyDetach` (m) | `GET /policies/{policyId}/attached-stack`<br>`GET /policies/{policyId}`<br>`PUT /stacks/{stackId}/policies/{policyId}`<br>`DELETE /policy-attachments/{attachmentId}` | `$POLICY_ID/$STACK_ID` | `module_id`, `policy_id` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_policies` | `policies` (q); `space` (q); nested: `id`, `name`, `type` | `GET /policies`<br>`GET /spaces/{spaceId}` | `labels`, `type` |
| `spacelift_policy` | `policy` (q) | `GET /policies/{policyId}` | `policy_id` |


### Spaces

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_space` | `space` (q); `spaceCreate` (m); `spaceDelete` (m); `spaceUpdate` (m) | `GET /spaces/{spaceId}`<br>`POST /spaces`<br>`DELETE /spaces/{spaceId}`<br>`PUT /spaces/{spaceId}` | `$SPACE_ID` | none |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_current_space` | `module` (q); `stack` (q) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}` | none |
| `spacelift_space` | `space` (q) | `GET /spaces/{spaceId}` | `space_id` |
| `spacelift_space_by_path` | `spaces` (q) | `GET /spaces` | `space_path` |
| `spacelift_spaces` | `spaces` (q) | `GET /spaces` | `labels` |


### Worker pools

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_default_runner_image` | `accountUpdateDefaultWorkerPoolRunnerImages` (m); `defaultPrivateWorkerPoolRunnerImage` (q); `defaultPublicWorkerPoolRunnerImage` (q) | `POST /account/update-default-worker-pool-runner-images`<br>`GET /default-private-worker-pool-runner-image`<br>`GET /default-public-worker-pool-runner-image` | Passthrough; the ID is a timestamp and the read ignores it (account singleton) | `private`, `public` |
| `spacelift_worker_pool` | `workerPool` (q); `workerPoolCreate` (m); `workerPoolDelete` (m); `workerPoolReset` (m); `workerPoolUpdate` (m) | `GET /worker-pools/{workerPoolId}`<br>`POST /worker-pools`<br>`DELETE /worker-pools/{workerPoolId}`<br>`POST /worker-pools/{workerPoolId}/reset`<br>`PUT /worker-pools/{workerPoolId}` | `$WORKER_POOL_ID` | none |
| `spacelift_worker_pool_recycle` | `workerPoolCycle` (m) | `POST /worker-pools/{workerPoolId}/cycle` | Passthrough of `worker_pool_recycle_$WORKER_POOL_ID`; read is a no-op | `worker_pool_id`; Terraform-only: `keepers` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_worker_pool` | `workerPool` (q) | `GET /worker-pools/{workerPoolId}` | `worker_pool_id` |
| `spacelift_worker_pools` | `workerPools` (q) | `GET /worker-pools` | none |


### Modules and registry

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_module` | `module` (q); `moduleCreate` (m); `moduleDelete` (m); `modulePublish` (m); `moduleShare` (m); `moduleUpdateV2` (m) | `GET /modules/{moduleId}`<br>`POST /modules`<br>`DELETE /modules/{moduleId}`<br>`POST /modules/{moduleId}/publish`<br>`POST /modules/{moduleId}/share`<br>`PUT /modules/{moduleId}` | `$MODULE_ID` | `public`, `space_shares`; blocks: `azure_devops`, `bitbucket_cloud`, `bitbucket_datacenter`, `github_enterprise`, `gitlab`, `origin`, `raw_git`, `spacelift_repo` |
| `spacelift_terraform_provider` | `terraformProvider` (q); `terraformProviderCreate` (m); `terraformProviderDelete` (m); `terraformProviderSetVisibility` (m); `terraformProviderUpdate` (m) | `GET /terraform-providers/{terraformProviderId}`<br>`POST /terraform-providers`<br>`DELETE /terraform-providers/{terraformProviderId}`<br>`POST /terraform-providers/{terraformProviderId}/set-visibility`<br>`PUT /terraform-providers/{terraformProviderId}` | `$PROVIDER_ID` (passthrough) | none |
| `spacelift_version` | `module` (q); `versionCreate` (m) | `GET /modules/{moduleId}/version`<br>`GET /modules/{moduleId}`<br>`POST /modules/{moduleId}/versions` | No importer | `version_number`; Terraform-only: `keepers` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_module` | `module` (q) | `GET /modules/{moduleId}` | `git_sparse_checkout_paths`, `module_id` |
| `spacelift_modules` | `searchModules` (q) | `POST /modules/search` | filter blocks: `administrative`, `branch`, `commit`, `labels`, `name`, `project_root`, `repository`, `worker_pool` |


### Cloud integrations

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_aws_integration` | `awsIntegration` (q); `awsIntegrationCreate` (m); `awsIntegrationDelete` (m); `awsIntegrationUpdate` (m) | `GET /aws-integrations/{awsIntegrationId}`<br>`POST /aws-integrations`<br>`DELETE /aws-integrations/{awsIntegrationId}`<br>`PUT /aws-integrations/{awsIntegrationId}` | `$INTEGRATION_ID` | none |
| `spacelift_aws_integration_attachment` | `awsIntegration` (q); `awsIntegrationAttach` (m); `awsIntegrationAttachmentUpdate` (m); `awsIntegrationDetach` (m) | `GET /aws-integrations/{awsIntegrationId}/attached-stack`<br>`GET /aws-integrations/{awsIntegrationId}`<br>`PUT /stacks/{stackId}/aws-integrations/{awsIntegrationId}`<br>`PUT /aws-integration-attachments/{attachmentId}`<br>`DELETE /aws-integration-attachments/{attachmentId}` | `$INTEGRATION_ID/$PROJECT_ID` | `integration_id`, `module_id` |
| `spacelift_aws_role` (deprecated) | `module` (q); `stack` (q); `stackIntegrationAwsCreate` (m); `stackIntegrationAwsDelete` (m) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}`<br>`PUT /stacks/{stackId}/aws-role`<br>`DELETE /stacks/{stackId}/aws-role` | `stack/$STACK_ID` or `module/$MODULE_ID` | `module_id`, `stack_id` |
| `spacelift_azure_integration` | `azureIntegration` (q); `azureIntegrationCreate` (m); `azureIntegrationDelete` (m); `azureIntegrationUpdate` (m) | `GET /azure-integrations/{azureIntegrationId}`<br>`POST /azure-integrations`<br>`DELETE /azure-integrations/{azureIntegrationId}`<br>`PUT /azure-integrations/{azureIntegrationId}` | `$INTEGRATION_ID` | none |
| `spacelift_azure_integration_attachment` | `azureIntegration` (q); `azureIntegrationAttach` (m); `azureIntegrationAttachmentUpdate` (m); `azureIntegrationDetach` (m) | `GET /azure-integrations/{azureIntegrationId}/attached-stack`<br>`GET /azure-integrations/{azureIntegrationId}`<br>`PUT /stacks/{stackId}/azure-integrations/{azureIntegrationId}`<br>`PUT /azure-integration-attachments/{attachmentId}`<br>`DELETE /azure-integration-attachments/{attachmentId}` | `$INTEGRATION_ID/$STACK_ID` or `$INTEGRATION_ID/$MODULE_ID` | `integration_id`, `module_id` |
| `spacelift_gcp_service_account` | `module` (q); `stack` (q); `stackIntegrationGcpCreate` (m, deprecated); `stackIntegrationGcpDelete` (m, deprecated) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}`<br>`stackIntegrationGcpCreate` excluded<br>`stackIntegrationGcpDelete` excluded | `stack/$STACK_ID` or `module/$MODULE_ID` | `module_id`, `stack_id`, `token_scopes` |
| `spacelift_stack_aws_role` (deprecated) | `module` (q); `stack` (q); `stackIntegrationAwsCreate` (m); `stackIntegrationAwsDelete` (m); same implementation as `spacelift_aws_role` (`resource_aws_role.go`) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}`<br>`PUT /stacks/{stackId}/aws-role`<br>`DELETE /stacks/{stackId}/aws-role` | `stack/$STACK_ID` or `module/$MODULE_ID` (shares `importIntegration`) | not in `coverage.md` |
| `spacelift_stack_gcp_service_account` | `module` (q); `stack` (q); `stackIntegrationGcpCreate` (m, deprecated); `stackIntegrationGcpDelete` (m, deprecated); same implementation as `spacelift_gcp_service_account` (`resource_gcp_service_account.go`) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}`<br>`stackIntegrationGcpCreate` excluded<br>`stackIntegrationGcpDelete` excluded | `stack/$STACK_ID` or `module/$MODULE_ID` (shares `importIntegration`) | not in `coverage.md` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_aws_integration` | `awsIntegration` (q); `awsIntegrationByName` (q) | `GET /aws-integrations/{awsIntegrationId}`<br>`GET /aws-integration-by-name` | `integration_id` or `name` (optional, computed) |
| `spacelift_aws_integration_attachment` | `awsIntegration` (q) | `GET /aws-integrations/{awsIntegrationId}/attached-stack`<br>`GET /aws-integrations/{awsIntegrationId}` | `integration_id`, `module_id`, `stack_id` |
| `spacelift_aws_integration_attachment_external_id` | `awsIntegration` (q); nested: `id` | `GET /aws-integrations/{awsIntegrationId}/external-id-for-stack`<br>`GET /aws-integrations/{awsIntegrationId}` | `integration_id`, `module_id`, `read`, `stack_id`, `write` |
| `spacelift_aws_integrations` | `awsIntegrations` (q) | `GET /aws-integrations` | `labels` |
| `spacelift_aws_role` | `module` (q); `stack` (q) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}` | `module_id`, `stack_id` |
| `spacelift_azure_integration` | `azureIntegration` (q); `azureIntegrationByName` (q) | `GET /azure-integrations/{azureIntegrationId}`<br>`GET /azure-integration-by-name` | `integration_id` or `name` (optional, computed) |
| `spacelift_azure_integration_attachment` | `azureIntegration` (q) | `GET /azure-integrations/{azureIntegrationId}/attached-stack`<br>`GET /azure-integrations/{azureIntegrationId}` | `integration_id`, `module_id`, `stack_id` |
| `spacelift_azure_integrations` | `azureIntegrations` (q) | `GET /azure-integrations` | none |
| `spacelift_gcp_service_account` | `module` (q); `stack` (q); reuses the resource read, `integrations.gcp` (`data_gcp_service_account.go`) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}` | `module_id`, `stack_id` |
| `spacelift_stack_aws_role` | `module` (q); `stack` (q); same implementation as `spacelift_aws_role` (`data_aws_role.go`) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}` | `module_id`, `stack_id` |
| `spacelift_stack_gcp_service_account` | `module` (q); `stack` (q); same implementation as `spacelift_gcp_service_account` (`data_gcp_service_account.go`) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}` | `module_id`, `stack_id` |


### VCS integrations and Repos

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_azure_devops_integration` | `azureDevOpsRepoIntegration` (q); `azureDevOpsRepoIntegrationCreate` (m); `azureDevOpsRepoIntegrationDelete` (m); `azureDevOpsRepoIntegrationUpdate` (m) | `GET /azure-dev-ops-repo-integrations/{azureDevOpsRepoIntegrationId}`<br>`POST /azure-dev-ops-repo-integrations`<br>`DELETE /azure-dev-ops-repo-integrations/{azureDevOpsRepoIntegrationId}`<br>`PUT /azure-dev-ops-repo-integrations/{azureDevOpsRepoIntegrationId}` | `spacelift_azure_devops_integration_id` | `organization_url`; Terraform-only: `personal_access_token_wo_version` |
| `spacelift_bitbucket_datacenter_integration` | `bitbucketDatacenterIntegration` (q); `bitbucketDatacenterIntegrationCreate` (m); `bitbucketDatacenterIntegrationDelete` (m); `bitbucketDatacenterIntegrationUpdate` (m) | `GET /bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}`<br>`POST /bitbucket-datacenter-integrations`<br>`DELETE /bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}`<br>`PUT /bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}` | `spacelift_bitbucket_datacenter_integration_id` | Terraform-only: `access_token_wo_version` |
| `spacelift_gitlab_integration` | `gitlabIntegration` (q); `gitlabIntegrationCreate` (m); `gitlabIntegrationDelete` (m); `gitlabIntegrationUpdate` (m) | `GET /gitlab-integrations/{gitlabIntegrationId}`<br>`POST /gitlab-integrations`<br>`DELETE /gitlab-integrations/{gitlabIntegrationId}`<br>`PUT /gitlab-integrations/{gitlabIntegrationId}` | `spacelift_gitlab_integration_id` | Terraform-only: `private_token_wo_version` |
| `spacelift_repo` | `repo` (q); `repoCreate` (m); `repoDelete` (m); `repoUpdate` (m) | `GET /repos/{repoId}`<br>`POST /repos`<br>`DELETE /repos/{repoId}`<br>`PUT /repos/{repoId}` | `$REPO_ID` | none |
| `spacelift_repo_file` | `repoFileHistory` (q); `revisionCreate` (m, deprecated) | `GET /repos/{repoId}/file-history`<br>`revisionCreate` excluded | `$REPO_ID/$FILE_PATH` | `author_email`, `author_name`, `commit_message`, `content`, `encrypt`, `file_mode`, `path`, `repo_id` |
| `spacelift_vcs_agent_pool` | `vcsAgentPool` (q); `vcsAgentPoolCreate` (m); `vcsAgentPoolDelete` (m); `vcsAgentPoolUpdate` (m) | `GET /vcs-agent-pools/{vcsAgentPoolId}`<br>`POST /vcs-agent-pools`<br>`DELETE /vcs-agent-pools/{vcsAgentPoolId}`<br>`PUT /vcs-agent-pools/{vcsAgentPoolId}` | `$VCS_AGENT_POOL_ID` | none |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_azure_devops_integration` | `azureDevOpsRepoIntegration` (q) | `GET /azure-dev-ops-repo-integrations/{azureDevOpsRepoIntegrationId}` | `id` (optional; the default integration when unset) |
| `spacelift_bitbucket_cloud_integration` | `bitbucketCloudIntegration` (q); `space` (q); nested: `id`, `name` | `GET /bitbucket-cloud-integrations/{bitbucketCloudIntegrationId}`<br>`GET /spaces/{spaceId}` | `id` (optional; the default integration when unset) |
| `spacelift_bitbucket_datacenter_integration` | `bitbucketDatacenterIntegration` (q); `space` (q); nested: `id`, `name` | `GET /bitbucket-datacenter-integrations/{bitbucketDatacenterIntegrationId}`<br>`GET /spaces/{spaceId}` | `id` (optional; the default integration when unset) |
| `spacelift_github_enterprise_integration` | `githubEnterpriseIntegration` (q); `space` (q); nested: `id`, `name` | `GET /github-enterprise-integrations/{githubEnterpriseIntegrationId}`<br>`GET /spaces/{spaceId}` | `id` (optional; the default integration when unset) |
| `spacelift_gitlab_integration` | `gitlabIntegration` (q); `space` (q); nested: `id`, `name` | `GET /gitlab-integrations/{gitlabIntegrationId}`<br>`GET /spaces/{spaceId}` | `id` (optional; the default integration when unset) |
| `spacelift_gitlab_webhook_endpoint` | `gitlabWebhooksEndpoint` (q) | `GET /gitlab-webhooks-endpoint` | none |
| `spacelift_repo` | `repo` (q) | `GET /repos/{repoId}` | `repo_id` |
| `spacelift_repos` | `repos` (q) | `GET /repos` | `labels`, `space_id` |
| `spacelift_vcs_agent_pool` | `vcsAgentPool` (q) | `GET /vcs-agent-pools/{vcsAgentPoolId}` | `vcs_agent_pool_id` |
| `spacelift_vcs_agent_pools` | `vcsAgentPools` (q) | `GET /vcs-agent-pools` | none |


### Identity and access

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_api_key` | `apiKey` (q); `apiKeyCreate` (m); `apiKeyDelete` (m); `apiKeyUpdate` (m) | `GET /api-keys/{apiKeyId}`<br>`POST /api-keys`<br>`DELETE /api-keys/{apiKeyId}`<br>`PUT /api-keys/{apiKeyId}` | `$API_KEY_ID` | `idp_groups`; blocks: `oidc` |
| `spacelift_idp_group_mapping` | `managedUserGroup` (q); `managedUserGroupCreate` (m, deprecated); `managedUserGroupDelete` (m); `managedUserGroupUpdate` (m, deprecated) | `GET /managed-user-groups/{managedUserGroupId}`<br>`managedUserGroupCreate` excluded<br>`DELETE /managed-user-groups/{managedUserGroupId}`<br>`managedUserGroupUpdate` excluded | `$IDP_GROUP_MAPPING_ID` | `description`, `name`; blocks: `policy` |
| `spacelift_role` | `role` (q); `roleCreate` (m); `roleDelete` (m); `roleUpdate` (m) | `GET /roles/{roleId}`<br>`POST /roles`<br>`DELETE /roles/{roleId}`<br>`PUT /roles/{roleId}` | `$ROLE_ID` | none |
| `spacelift_role_attachment` | `apiKeyRoleBinding` (q); `apiKeyRoleBindingCreate` (m); `apiKeyRoleBindingDelete` (m); `stackRoleBinding` (q); `stackRoleBindingCreate` (m); `stackRoleBindingDelete` (m); `userGroupRoleBinding` (q); `userGroupRoleBindingCreate` (m); `userGroupRoleBindingDelete` (m); `userRoleBinding` (q); `userRoleBindingBatchCreate` (m); `userRoleBindingDelete` (m) | `GET /api-key-role-bindings/{apiKeyRoleBindingId}`<br>`POST /api-key-role-bindings`<br>`DELETE /api-key-role-bindings/{apiKeyRoleBindingId}`<br>`GET /stack-role-bindings/{stackRoleBindingId}`<br>`POST /stack-role-bindings`<br>`DELETE /stack-role-bindings/{stackRoleBindingId}`<br>`GET /user-group-role-bindings/{userGroupRoleBindingId}`<br>`POST /user-group-role-bindings`<br>`DELETE /user-group-role-bindings/{userGroupRoleBindingId}`<br>`GET /user-role-bindings/{userRoleBindingId}`<br>`POST /user-role-binding-batches`<br>`DELETE /user-role-bindings/{userRoleBindingId}` | `API/$ROLE_ATTACHMENT_ID` or `IDP/$ROLE_ATTACHMENT_ID` or `STACK/$ROLE_ATTACHMENT_ID` or `USER/$ROLE_ATTACHMENT_ID` | `idp_group_mapping_id`, `user_id` |
| `spacelift_security_email` | `accountUpdateSecurityEmail` (m); `securityEmail` (q) | `POST /account/update-security-email`<br>`GET /security-email` | Passthrough; the ID is a timestamp and the read ignores it (account singleton) | `email` |
| `spacelift_user` | `managedUser` (q); `managedUserDelete` (m); `managedUserInvite` (m, deprecated); `managedUserUpdate` (m) | `GET /managed-users/{managedUserId}`<br>`DELETE /managed-users/{managedUserId}`<br>`managedUserInvite` excluded<br>`PUT /managed-users/{managedUserId}` | `$USER_ID` | `invitation_email`, `username`; blocks: `policy` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_idp_group_mapping` | `managedUserGroups` (q) | `GET /managed-user-groups` | `name` |
| `spacelift_role` | `roles` (q) | `GET /roles` | `role_id` or `slug` (optional, computed) |
| `spacelift_role_actions` | none in `terraform-operations.json`; an introspection query on `__schema` that lists every type with deprecated enum values included, filtered to the `Action` enum (`data_role_actions.go`, `internal/introspection.go`) | none | none |
| `spacelift_user` | `managedUsers` (q) | `GET /managed-users` | `username` |


### Webhooks and audit

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_audit_trail_webhook` | `auditTrailDeleteWebhook` (m); `auditTrailSetWebhook` (m); `auditTrailWebhook` (q) | `POST /audit-trail/delete-webhook`<br>`POST /audit-trail/set-webhook`<br>`GET /audit-trail/webhook` | Passthrough; the ID is a timestamp and the read ignores it (account singleton), so any string imports | Terraform-only: `secret_wo_version` |
| `spacelift_named_webhook` | `namedWebhooksIntegration` (q); `namedWebhooksIntegrationCreate` (m); `namedWebhooksIntegrationDelete` (m); `namedWebhooksIntegrationUpdate` (m); nested: `id` | `GET /named-webhooks-integrations/{namedWebhooksIntegrationId}`<br>`POST /named-webhooks-integrations`<br>`DELETE /named-webhooks-integrations/{namedWebhooksIntegrationId}`<br>`PUT /named-webhooks-integrations/{namedWebhooksIntegrationId}` | `$WEBHOOK_ID` (passthrough) | Terraform-only: `secret_wo_version` |
| `spacelift_named_webhook_secret_header` | `namedWebhooksIntegration` (q); `namedWebhooksIntegrationDeleteHeaders` (m); `namedWebhooksIntegrationSetHeaders` (m); nested: `id` | `GET /named-webhooks-integrations/{namedWebhooksIntegrationId}`<br>`POST /named-webhooks-integrations/{namedWebhooksIntegrationId}/delete-headers`<br>`POST /named-webhooks-integrations/{namedWebhooksIntegrationId}/set-headers` | `$WEBHOOK_ID/$HEADER_KEY` (passthrough; read splits on the first `/`) | `key`, `value`, `value_wo`, `webhook_id`; Terraform-only: `value_wo_version` |
| `spacelift_webhook` | `module` (q); `stack` (q); `webhooksIntegrationCreate` (m); `webhooksIntegrationDelete` (m); `webhooksIntegrationUpdate` (m); nested: `id` | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}`<br>`POST /stacks/{stackId}/webhooks-integrations`<br>`DELETE /stacks/{stackId}/webhooks-integrations/{webhooksIntegrationId}`<br>`PUT /stacks/{stackId}/webhooks-integrations/{webhooksIntegrationId}` | `stack/$STACK_ID/$WEBHOOK_ID` or `module/$MODULE_ID/$WEBHOOK_ID` (the importer accepts both; the example shows the stack form) | `module_id`; Terraform-only: `secret_wo_version` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_named_webhook` | `namedWebhooksIntegration` (q) | `GET /named-webhooks-integrations/{namedWebhooksIntegrationId}` | `webhook_id` |
| `spacelift_webhook` | `module` (q); `stack` (q) | `GET /modules/{moduleId}`<br>`GET /stacks/{stackId}` | `module_id`, `stack_id`, `webhook_id` |


### Templates and blueprints

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_blueprint` | `blueprint` (q); `blueprintCreate` (m); `blueprintDelete` (m); `blueprintUpdate` (m); nested: `id` | `GET /blueprints/{blueprintId}`<br>`POST /blueprints`<br>`DELETE /blueprints/{blueprintId}`<br>`PUT /blueprints/{blueprintId}` | `$BLUEPRINT_ID` (passthrough) | none |
| `spacelift_template` | `template` (q); `templateCreate` (m); `templateDelete` (m); `templateUpdate` (m) | `GET /templates/{templateId}`<br>`POST /templates`<br>`DELETE /templates/{templateId}`<br>`PUT /templates/{templateId}` | `$TEMPLATE_ID` (passthrough) | none |
| `spacelift_template_deployment` | `templateDeployment` (q); `templateDeploymentCreate` (m); `templateDeploymentDelete` (m); `templateDeploymentUpdate` (m) | `GET /templates/{templateId}/deployments/{deploymentId}`<br>`POST /templates/{templateId}/deployments`<br>`DELETE /templates/{templateId}/deployments/{deploymentId}`<br>`PUT /templates/{templateId}/deployments/{deploymentId}` | `$TEMPLATE_ID/$DEPLOYMENT_ID` (passthrough; read parses both parts) | `template_version_id`; blocks: `input`, `timeouts` |
| `spacelift_template_version` | `template` (q); `templateVersionCreate` (m); `templateVersionDelete` (m); `templateVersionParseTemplate` (q); `templateVersionUpdate` (m) | `GET /templates/{templateId}/template-version`<br>`GET /templates/{templateId}`<br>`POST /template-versions`<br>`DELETE /template-versions/{templateVersionId}`<br>`GET /template-version-parse-template`<br>`PUT /template-versions/{templateVersionId}` | `$TEMPLATE_ID-$VERSION_ULID` (the last 27 characters are `-` and the ULID) | none |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_template` | `template` (q) | `GET /templates/{templateId}` | `template_id` |
| `spacelift_template_deployment` | `templateDeployment` (q) | `GET /templates/{templateId}/deployments/{deploymentId}` | `deployment_id`, `template_id` |
| `spacelift_template_version` | `template` (q) | `GET /templates/{templateId}/template-version`<br>`GET /templates/{templateId}/template-version-by-number`<br>`GET /templates/{templateId}` | `template_id`, `version_id`, or `version_number` (optional, computed) |


### AI integrations

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_ai_integration` | `aiIntegration` (q); `aiIntegrationCreate` (m); `aiIntegrationDelete` (m); `aiIntegrationToggle` (m); `aiIntegrationUpdate` (m); nested: `id` | `GET /ai-integrations/{aiIntegrationId}`<br>`POST /ai-integrations`<br>`DELETE /ai-integrations/{aiIntegrationId}`<br>`POST /ai-integrations/{aiIntegrationId}/toggle`<br>`PUT /ai-integrations/{aiIntegrationId}` | `$AI_INTEGRATION_ID` | blocks: `anthropic`, `bedrock`, `google`, `openai`, `spacelift` |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_ai_integration` | `aiIntegration` (q) | `GET /ai-integrations/{aiIntegrationId}` | `integration_id` |
| `spacelift_ai_integrations` | `aiIntegrations` (q) | `GET /ai-integrations` | `ai_provider`, `labels`, `spacelift_provided` |


### Plugins

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_plugin` | `plugin` (q); `pluginDelete` (m); `pluginInstall` (m); `pluginTemplate` (q) | `GET /plugins/{pluginId}`<br>`DELETE /plugins/{pluginId}`<br>`POST /plugins`<br>`GET /plugin-templates/{pluginTemplateId}` | `$PLUGIN_ID` | `stack_label` |
| `spacelift_plugin_template` | `pluginTemplate` (q); `pluginTemplateCreate` (m); `pluginTemplateDelete` (m) | `GET /plugin-templates/{pluginTemplateId}`<br>`POST /plugin-templates`<br>`DELETE /plugin-templates/{pluginTemplateId}` | `$PLUGIN_TEMPLATE_ID` | none |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_plugin` | `plugin` (q) | `GET /plugins/{pluginId}` | `plugin_id` |
| `spacelift_plugin_template` | `pluginTemplate` (q) | `GET /plugin-templates/{pluginTemplateId}` | `plugin_template_id` |


### Account and lookups

| Resource | GraphQL operations | REST routes (spec) | Import ID | Attributes with no input field |
|---|---|---|---|---|
| `spacelift_saved_filter` | `savedFilter` (q); `savedFilterCreate` (m); `savedFilterDelete` (m); `savedFilterUpdate` (m) | `GET /saved-filters/{savedFilterId}`<br>`POST /saved-filters`<br>`DELETE /saved-filters/{savedFilterId}`<br>`PUT /saved-filters/{savedFilterId}` | `$FILTER_ID` | none |

| Data source | GraphQL operations | REST routes (spec) | Arguments |
|---|---|---|---|
| `spacelift_account` | `name` (q); `spaceliftAwsAccountId` (q); `tier` (q) | `GET /name`<br>`GET /spacelift-aws-account-id`<br>`GET /tier` | none |
| `spacelift_ips` | `outgoingIPAddresses` (q) | `GET /outgoing-ip-addresses` | none |
| `spacelift_saved_filter` | `savedFilter` (q); nested: `id`, `name`, `type` | `GET /saved-filters/{savedFilterId}` | `filter_id` |
| `spacelift_saved_filters` | `savedFilters` (q) | `GET /saved-filters` | `filter_name`, `filter_type` |
| `spacelift_tool_versions` | `kubectlVersions` (q); `openTofuVersions` (q); `terraformVersions` (q); `terragruntVersions` (q) | `GET /kubectl-versions`<br>`GET /open-tofu-versions`<br>`GET /terraform-versions`<br>`GET /terragrunt-versions` | `tool` |


### Deprecations in the provider schema

| Kind | Name | Deprecated resource or attributes |
|---|---|---|
| Resource | `spacelift_ai_integration` | `anthropic.api_key`, `google.api_key`, `openai.api_key` |
| Resource | `spacelift_audit_trail_webhook` | `secret` |
| Resource | `spacelift_aws_role` | whole resource |
| Resource | `spacelift_azure_devops_integration` | `personal_access_token` |
| Resource | `spacelift_azure_integration` | `object_id` |
| Resource | `spacelift_bitbucket_datacenter_integration` | `access_token` |
| Resource | `spacelift_gitlab_integration` | `private_token` |
| Resource | `spacelift_idp_group_mapping` | `policy` block |
| Resource | `spacelift_named_webhook` | `secret` |
| Resource | `spacelift_named_webhook_secret_header` | `value` |
| Resource | `spacelift_stack` | `administrative`, `github_action_deploy`, `terragrunt.skip_replan_when_run_all` |
| Resource | `spacelift_stack_aws_role` | whole resource |
| Resource | `spacelift_user` | `policy` block |
| Resource | `spacelift_webhook` | `secret` |
| Data source | `spacelift_azure_integration` | `object_id` |
| Data source | `spacelift_bitbucket_cloud_integration` | `username` |
| Data source | `spacelift_idp_group_mapping` | `policy` |
| Data source | `spacelift_stack` | `administrative` |

### Deprecated or excluded root fields the provider calls

| Root field | Called by | Upstream deprecation reason | In the spec |
|---|---|---|---|
| `managedUserGroupCreate` | `spacelift_idp_group_mapping` | Please use managedUserGroupCreateV2. | excluded |
| `managedUserGroupUpdate` | `spacelift_idp_group_mapping` | Please use managedUserGroupUpdateV2. | excluded |
| `managedUserInvite` | `spacelift_user` | Please use managedUserInviteV2. | excluded |
| `revisionCreate` | `spacelift_repo_file` | Use repoCommitCreate instead. | excluded |
| `stackIntegrationGcpCreate` | `spacelift_gcp_service_account`, `spacelift_stack_gcp_service_account` | Use OIDC integration instead. | excluded |
| `stackIntegrationGcpDelete` | `spacelift_gcp_service_account`, `spacelift_stack_gcp_service_account` | Use OIDC integration instead. | excluded |

<!-- distinct root fields called: 211; routed in spec: 205 -->

## 5. How unmatched attributes are sent

The name matcher in section 2 misses these; the source shows where each one goes
**[S]**.

| Resource | Attributes | Sent as |
|---|---|---|
| `spacelift_environment_variable` | `name`, `value`, `module_id` | `ConfigInput.id` and `ConfigInput.value` with `type: ENVIRONMENT_VARIABLE`; a module's variable goes through `stackConfigAdd(stack: <module ID>)`, because modules take the stack config mutations ([`resource_environment_variable.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go#L156-L196)) |
| `spacelift_mounted_file` | `relative_path`, `content`, `content_wo`, `module_id` | `ConfigInput.id` (the relative path) and `ConfigInput.value` (Base64 content) with `type: FILE_MOUNT`; modules as above ([`resource_mounted_file.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_mounted_file.go#L153-L193)) |
| `spacelift_context_attachment`, `spacelift_policy_attachment`, `spacelift_aws_integration_attachment`, `spacelift_azure_integration_attachment` | `module_id`, `context_id`, `policy_id`, `integration_id` | The attach mutation's object ID and its `stack` argument, which takes a stack or a module ID |
| `spacelift_scheduled_task`, `spacelift_scheduled_run`, `spacelift_scheduled_delete_stack` | `at`, `every`, `delete_resources` | `timestampSchedule`, `cronSchedule`, `shouldDeleteResources` in the schedule inputs; `schedule_id` is the ID the create returns ([`internal/structs/scheduled_task.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/structs/scheduled_task.go#L10-L24)) |
| `spacelift_stack_activator` | `stack_id`, `enabled` | `stackEnable(id)` or `stackDisable(id)`, sent only when the stack's `isDisabled` disagrees with `enabled` ([`resource_stack_activator.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack_activator.go#L67-L130)) |
| `spacelift_stack_destructor` | `deactivated`, `discard_runs` | Client-side switches; see section 6 |
| `spacelift_stack` | `terraform_workflow_tool`, `terraform_workspace`, `terraform_smart_sanitization`, `terraform_external_state_access` | Folded into the Terraform `vendorConfig` as `workflowTool`, `workspace`, `useSmartSanitization` and `externalStateAccessEnabled` ([`getVendorConfig`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1276-L1436)) |
| `spacelift_stack` | `import_state`, `import_state_file` | Uploaded through `stateUploadUrl`, then passed as `stackObjectID` to `stackCreate` (section 6) |
| `spacelift_stack` | `prevent_changes_when_locked` | Client-side plan guard (section 6) |
| `spacelift_security_email` | `email` | `accountUpdateSecurityEmail(securityEmail)` ([`resource_security_email.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_security_email.go#L38-L83)) |
| `spacelift_default_runner_image` | `private`, `public` | `accountUpdateDefaultWorkerPoolRunnerImages(privateWorkerPoolRunnerImage, publicWorkerPoolRunnerImage)` ([`resource_default_runner_image.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_default_runner_image.go#L51-L146)) |
| `spacelift_named_webhook_secret_header` | `webhook_id`, `key`, `value`, `value_wo` | `namedWebhooksIntegrationSetHeaders(id, input)` and `namedWebhooksIntegrationDeleteHeaders(id, headerKeys)` ([`resource_named_webhook_secret_header.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_named_webhook_secret_header.go#L78-L176)) |
| `spacelift_gcp_service_account` | `token_scopes`, `stack_id`, `module_id` | `stackIntegrationGcpCreate(id, tokenScopes)`, a deprecated mutation the spec excludes ([`resource_gcp_service_account.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_gcp_service_account.go#L79-L125)) |
| `spacelift_user`, `spacelift_idp_group_mapping` | `invitation_email`, `username`, `name`, `description` | `managedUserInvite`, `managedUserGroupCreate` and `managedUserGroupUpdate`, deprecated mutations the spec excludes, so no input field exists in the spec ([`resource_user.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_user.go#L83-L114), [`resource_idp_group_mapping.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_idp_group_mapping.go#L78-L154)) |
| `spacelift_repo_file` | all eight | `revisionCreate(input)`, a deprecated mutation the spec excludes; reads use `repoFileHistory` ([`resource_repo_file.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_repo_file.go#L138-L257)) |
| `spacelift_worker_pool_recycle` | `worker_pool_id` | `workerPoolCycle(id)` ([`resource_worker_pool_recycle.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_worker_pool_recycle.go#L46-L65)) |

## 6. Behaviour worth copying

### Resource shapes

- **Attachments are separate resources** (`spacelift_context_attachment`,
  `spacelift_policy_attachment`, the AWS and Azure integration attachments,
  `spacelift_role_attachment`). This is why REST models them as sub-resources with
  `PUT`/`DELETE` rather than as fields of the stack **[S]**. Their IDs join the
  attached object and the attachment: context and policy attachments store
  `<context or policy ID>/<attachment ID>` after import and accept
  `<context or policy ID>/<stack or module ID>` as the import ID, looking the
  attachment up and setting `stack_id` or `module_id` from its `isModule` flag
  **[S]** ([`resourceContextAttachmentImport`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_context_attachment.go#L142-L169)).
- **Role attachments are one resource over four binding kinds.** The ID prefix
  (`API`, `IDP`, `STACK`, `USER`) selects `apiKeyRoleBinding*`,
  `userGroupRoleBinding*`, `stackRoleBinding*` or `userRoleBinding*`; user bindings
  are created through `userRoleBindingBatchCreate` **[S]**
  ([`resource_role_attachment.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_role_attachment.go#L88-L220)).
- **Account singletons** (`spacelift_audit_trail_webhook`,
  `spacelift_default_runner_image`, `spacelift_security_email`) use a timestamp as
  the resource ID and ignore it on read. Deleting `spacelift_default_runner_image`
  sets both images to the empty string; deleting `spacelift_security_email` only
  drops it from state, with a warning that the address stays configured **[S]**
  ([`resourceSecurityEmailDelete`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_security_email.go#L84-L90)).
- **Action resources** model one-off operations: `spacelift_run`, `spacelift_task`,
  `spacelift_version` and `spacelift_worker_pool_recycle` call one mutation on
  create, have no-op reads, and their deletes either do nothing or only drop
  the resource from state (`spacelift_run`, `spacelift_version`), and use a `keepers` map whose change forces replacement, so a new run,
  task, version or recycle happens **[S]**
  ([`resource_run.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_run.go#L16-L147)).

### Runs, tasks and versions

- `spacelift_run` sends `runTrigger(stack, commitSha, runType, runtimeConfig)` with
  `runType` `TRACKED`, or `PROPOSED` when `proposed` is true; `commit_sha` and a
  `runtime_config` block are optional **[S]**
  ([`resourceRunCreate`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_run.go#L107-L147)).
- With a `wait` block, `spacelift_run` and `spacelift_task` poll
  `stack(id).runResourceState(id)` every 10 seconds, after a 10-second delay,
  until its `finished` flag is true or its state is `unconfirmed`. `unconfirmed`
  counts as an end state because it needs a human. The run then passes only if
  its final state, lower-cased, is in `continue_on_state` (default `finished`). A timeout fails unless
  `continue_on_timeout` is true. The create timeout defaults to 30 minutes **[S]**
  ([`Wait`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/structs/wait.go#L52-L116)).
- `spacelift_task` sends `taskCreate(stack, command, skipInitialization)`, with
  `skipInitialization` sent as the negation of `init` **[S]**
  ([`resourceTaskCreate`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_task.go#L100-L130)).
- `spacelift_version` sends `versionCreate(module, commitSha, version)` and polls
  `module(id).version(id).state` every 5 seconds until `ACTIVE`, failing on
  `FAILED` **[S]**
  ([`waitForVersionCreate`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_version.go#L101-L139)).

### Stack lifecycle

- **Asynchronous delete.** `stackDelete` returns before the stack is gone, with
  `deleting` set **[G]** (`Stack.deleting`). The provider polls `stack(id)` every
  5 seconds until it returns null, and fails if `deleting` turns false first,
  because dependants such as the parent space cannot be deleted while the stack
  still exists **[S]**
  ([`resourceStackDelete`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1072-L1103)).
- **`spacelift_stack_destructor`** does its work on delete. Create only sets the
  ID and reads the stack, update is a no-op, and a read that finds no stack drops
  the destructor from state. On delete, unless `deactivated`, it optionally sends `runDiscardAll(stack)` and
  waits up to two minutes, polling `stack.blocker` every 2 seconds, for the
  stack to be unblocked. It then sends `stackDelete(id, destroyResources: true)`
  and waits for the destroy as above, under a two-hour delete timeout. Its docs
  advise `depends_on` so credentials and contexts outlive the destroy run **[S]**
  ([`resource_stack_destructor.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack_destructor.go#L17-L208)).
- **`spacelift_stack_activator`** reads `isDisabled` and sends `stackEnable` or
  `stackDisable` only on a mismatch; delete is a no-op **[S]**
  ([`resource_stack_activator.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack_activator.go#L17-L130)).
- **State import on create.** `import_state` (inline) or `import_state_file` (a
  path) require `manage_state`, or `terragrunt.use_state_management`. The provider
  sends `stateUploadUrl`, `PUT`s the state as `application/json` to the returned
  URL, and passes the returned `objectId` as `stackObjectID` to `stackCreate`.
  The inline value is blanked in state after create **[S]**
  ([`resourceStackCreate`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L869-L940),
  [`uploadStateFile`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1524-L1552)).
- **Vendor migration.** Moving a stack between the Terraform and Terragrunt
  vendor blocks sends `stackMigrateVendor(id, targetVendor, config)` before the
  update **[S]**
  ([`resourceStackUpdate`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1020-L1057),
  [`vendorMigrationDirection`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1472-L1512)).
- **Lock guard.** With `prevent_changes_when_locked`, a plan that changes anything
  other than that flag and the `lock` block fails while the stack is locked; on
  destroy the check runs at apply time because SDKv2 has no plan hook for
  deletes **[S]**
  ([`resource_stack.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L53-L72),
  [`checkStackLock`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L981-L1006)).
- **Vendor blocks conflict.** The vendor blocks (`ansible`, `cloudformation`,
  `kubernetes`, `opentofu`, `pulumi`, `terragrunt`) and the Terraform-only
  attributes are mutually exclusive through `ConflictsWith`, and so are the VCS
  blocks **[S]**
  ([`conflictingVCSProviders`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1580-L1599)).

### Configuration values and secrets

- **Environment variables and mounted files are immutable.** Neither resource has
  an update; every attribute forces replacement **[S]**
  ([`resource_environment_variable.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go#L21-L114)).
  Their IDs are `<context|module|stack>/<owner ID>/<name or relative path>`.
- **`write_only` defaults to true.** A write-only value is not returned by the
  API; the read stores the API's SHA-256 `checksum` in `value` instead, and a
  diff suppressor compares the hex checksum in state with the SHA-256 of the
  configured value, so an unchanged secret shows no diff **[S]**
  ([`resourceEnvironmentVariableRead`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go#L198-L248),
  [`suppressValueChange`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go#L352-L362)).
  `ConfigElement.checksum` is a non-null string described as the SHA256 checksum
  of the value **[G]**, so every config element carries one, write-only or not.
  That it is hex-encoded **[I]** follows from the diff suppressor decoding it as
  hex.
- **Write-only arguments** (Terraform and OpenTofu 1.11+) carry secrets without
  storing them: `value_wo`, `content_wo`, `secret_wo`, `api_key_wo`, `access_token_wo`,
  `private_token_wo`, `personal_access_token_wo`, each with a `*_wo_version`
  counter that forces a resend. The older plain attributes are deprecated
  (section 4, deprecations table) **[S]**.
- **Worker pool keys can be generated by the provider.** Without `csr`,
  `spacelift_worker_pool` generates an RSA 4096 key and a CSR, sends the CSR as
  `certificateSigningRequest`, and stores the Base64 private key and the returned
  `config` as sensitive attributes. A changed `csr` sends `workerPoolReset`
  **[S]** ([`resourceWorkerPoolCreate`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_worker_pool.go#L99-L188),
  [`resourceWorkerPoolUpdate`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_worker_pool.go#L226-L290)).
- **OIDC API keys.** `spacelift_api_key` with an `oidc` block (`issuer`,
  `client_id`, `subject_expression`, optional `claim_mappings`) creates an OIDC key
  instead of a secret key; `type` reads back `SECRET` or `OIDC` **[S]**
  ([`terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json)),
  matching the `APIKeyType` enum **[G]**.

### Scheduling and drift detection

- `spacelift_scheduled_task` and `spacelift_scheduled_run` hold either a
  one-off `at` (Unix time) or `every` (cron list), with `timezone` defaulting to
  `UTC`. `spacelift_scheduled_delete_stack` takes only a required `at`, has no
  `every` or `timezone`, and defaults `delete_resources` to true. The resource ID
  of all three is `<stack ID>/<schedule ID>` **[S]**
  ([`resource_scheduled_task.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_scheduled_task.go#L25-L124)).
- `spacelift_drift_detection` sends `stackIntegrationDriftDetectionCreate`,
  `Update` and `Delete` with `schedule` (cron list, required), `timezone`,
  `reconcile` and `ignore_state`. Its importer accepts `module/<ID>` as well as
  `stack/<ID>`, although the mutations take a stack argument **[S]**
  ([`resource_drift_detection.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_drift_detection.go#L15-L160)).

### Eventual consistency and validation

- **AWS trust retries.** Attaching an AWS integration, and the deprecated
  `spacelift_aws_role`, make up to five attempts, sleeping 10 seconds between
  them, while the error says `you need to configure trust relationship`, because a freshly
  created IAM role's trust policy takes time to propagate **[S]**
  ([`resourceAWSIntegrationAttachmentCreate`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_aws_integration_attachment.go#L76-L97),
  [`resourceAWSRoleCreate`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_aws_role.go#L100-L136)).
  So Spacelift tests the role assumption at attach time and rejects the attach
  when it fails **[I]** (from the retried error text).
- **Role actions are validated against the live schema.** `spacelift_role` and
  `spacelift_role_actions` introspect the `Action` enum, including deprecated
  values, and reject unknown actions before sending `roleCreate` **[S]**
  ([`validateActions`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_role.go#L200-L218)).
  The `Action` enum has 150 values **[G]**.
- **Template version validation at plan time.** When a `spacelift_template_version`
  whose planned `state` is `PUBLISHED` changes its `template`, the provider sends the
  `templateVersionParseTemplate` query during plan and fails with its `errors`
  **[S]** ([`resource_template_version.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_version.go#L27-L45)).
  The same name is also a deprecated mutation **[G]**.
- **Policy types.** `spacelift_policy` accepts the current `PolicyType` values
  and documents `STACK_ACCESS`, `TASK_RUN` and `TERRAFORM_PLAN` as deprecated,
  which the schema marks deprecated with `ACCESS`, `TASK` and `PLAN` as their
  replacements **[G]** **[S]**.

### Deprecated calls

The provider still sends six deprecated root fields (generated table above).
Four of them have V2 or replacement mutations in the schema
(`managedUserInviteV2`, `managedUserGroupCreateV2`, `managedUserGroupUpdateV2`,
`repoCommitCreate`) **[G]**, and the GCP pair has no replacement other than
OIDC-based integrations **[G]**. Because the spec excludes these, the routes column
shows them as excluded: an infraweave provider cannot reach those behaviours
through the specification reference as it stands **[I]** (from the exclusions in
`coverage.md`).

## 7. Later: provider parity in `deploy/ci`

Decision [0001](../decisions/0001-rest-first-spacelift-surface.md) makes
infraweave's clients its own, including a REST-native Terraform provider. The
planned `deploy/ci` runs that provider against a live infraweave server,
mirroring `weaveplatform-artifact-registry/deploy/ci`.

`terraform-schema.json` is the parity oracle. Every Spacelift resource and
attribute should have an infraweave equivalent, or a recorded reason why not.

## What this means for infraweave

- **Coverage.** 56 resources and 62 data sources call 211 distinct root fields;
  205 of them have routes in the specification reference and six are excluded
  as deprecated (section 4). The plan has to decide how the provider reaches the
  behaviour behind the six.
- **Field routes.** Provider reads select nested fields with arguments
  (`configElement`, `runResourceState`, `scheduledTask`, `dependency`,
  `templateVersion` and others); the REST API needs equivalent sub-resource reads.
- **Authentication.** A provider and CLI need both modes: an API key exchanged for
  a token, and a token injected into runs whose claims identify the stack and the
  API endpoint.
- **Not-found and asynchronous semantics.** Reads must distinguish "gone" from
  "error" without string matching, and deletes of stacks are asynchronous with a
  `deleting` flag that clients poll.
- **Write-only values.** Config elements need a server-computed checksum so
  clients can detect drift on secrets they cannot read.
- **Import IDs.** Composite import IDs (`context/<id>/<name>`, `<stack>/<schedule>`,
  `API/<binding>`, `<template>-<ULID>`) are part of the provider's user-facing
  contract.
- **Action resources** (runs, tasks, versions, recycles, destructors,
  activators) and their wait semantics are part of what users automate.

## Inferred and undocumented

| Claim | Inferred from |
|---|---|
| The API base URL a Spacelift JWT carries is in its `aud` claim | The docs say the token holds the endpoint; the provider takes it from `aud` alone ([`buildClientFromToken`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/provider.go#L238-L261)) |
| A missing object answers a GraphQL error whose message contains `not found` | The provider's only not-found test is that string match ([`Query`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/client.go#L52-L61)) |
| A config element's `checksum` is hex-encoded | The provider's diff suppressor hex-decodes it and compares it with SHA-256 of the configured value (`ConfigElement.checksum` itself is non-null in the schema **[G]**) |
| Spacelift tests an AWS role's trust policy when an integration is attached | The provider retries on the `you need to configure trust relationship` error |
| `spacelift_run` and `spacelift_task` need more than an in-run stack token | The docs say stack tokens cannot access runs or tasks; these resources send `runTrigger` and `taskCreate` |
| The last segment of the in-run token's `sub` is the run ID *(unverified)* | The provider splits `sub` as a path and keeps only the directory part |
| `terraform-operations.json` over-reports `id`, `name` and `type` and misses calls in shared helpers | Comparing it with the provider source (section 1) |

## Versions at the research baseline

| Component | Version | Note |
|---|---|---|
| terraform-provider-spacelift | v1.57.0, commit `c72b9bd2f3c3` (2026-10-09) | MIT |
| Spacelift GraphQL schema | introspection `b3255fe38f5e`, fetched 2026-10-28 | `demo.app.spacelift.io` |
| spacelift-api-bruno | `7cad4574954b` | MIT |
| Terraform plugin protocol | 6, muxed SDKv2 and Plugin Framework | [`main.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/main.go#L28-L41) |
| Write-only arguments | Terraform and OpenTofu 1.11+ | attribute descriptions in `terraform-schema.json` |

## Sources

- [`api/reference/spacelift/sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml): the pinned inputs.
- [`api/reference/spacelift/terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json): the extracted provider schema.
- [`api/reference/spacelift/terraform-operations.json`](../../../api/reference/spacelift/terraform-operations.json): the extracted resource to root-field map.
- [`api/reference/spacelift/coverage.md`](../../../api/reference/spacelift/coverage.md): attribute matching results.
- [`api/reference/spacelift/rest-mapping.yaml`](../../../api/reference/spacelift/rest-mapping.yaml): exclusions and Terraform renames.
- [`api/reference/spacelift/spacelift.openapi.yaml`](../../../api/reference/spacelift/spacelift.openapi.yaml): routes per root field.
- [`api/reference/spacelift/introspection.json`](../../../api/reference/spacelift/introspection.json): root fields, deprecations, `Action`, `PolicyType`, `APIKeyType`.
- <https://github.com/spacelift-io/terraform-provider-spacelift/tree/c72b9bd2f3c3> (MIT): provider source, `examples/resources/*/import.sh`, `docs/`.
- <https://docs.spacelift.io/vendors/terraform/terraform-provider>, read 2026-10-28: provider modes and configuration.
