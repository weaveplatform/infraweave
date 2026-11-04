# 02: Mapping GraphQL root fields to REST

Checked against: the snapshots pinned in
[`sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml): introspection
`b3255fe38f5e` (fetched 2026-10-28), spacelift-api-bruno `7cad4574954b` and
terraform-provider-spacelift v1.57.0; for Flows, flowctl `96bdd03303a6` (read
2026-11-04) and terraform-provider-flows v0.7.0 (`9d1a7ad4d937`).

Research baseline: **2026-10-28**; flowctl re-read 2026-11-04.

How `internal/specref` turns each of Spacelift's 665 root fields into a
resource-oriented route, and how `rest-mapping.yaml` handles the fields that
no rule can infer. Flows has no GraphQL API, so its reference is projected
from RPC paths by the rules of [Flows: RPC to REST](#flows-rpc-to-rest).
Decision [0001](../decisions/0001-rest-first-spacelift-surface.md)
records why the surface is REST.

## Invariants

- **Every root field is accounted for.** Each one is either routed or excluded
  with a reason.
- **Every mutation has a resource shape.** A mutation that matches no rule or
  override would become RPC-shaped (`POST /<operation-kebab>`). `rpcBudget` is
  0, so such a mutation fails generation until it gets an override.
- **Two routes can never share a method and path.** A collision fails
  generation, so a new upstream operation cannot silently shadow an existing
  one.
- **Stale entries fail generation.** If `rest-mapping.yaml` names a root field
  (or Terraform attribute) that upstream no longer has, generation fails.
- **The output is deterministic.** Regenerating unchanged snapshots gives
  byte-identical files, and `make verify-reference` enforces this.

## Resources

A *resource* is either of these:

- the return type of a root query `x(id: ID!)` that returns a single object,
  for example `stack(id)`, giving `Stack`;
- a name with a `<name>Create` or `<name>Add` mutation.

A plural form gives the collection: `stack` → `/stacks`. Irregular plurals go
in `plurals:`.

The `splitResource` helper reads an operation name as a resource plus a verb
by trying the longest resource-name prefix first:

- `stackConfigAdd` → resource `stack`, verb `ConfigAdd`;
- `contextAttach` → resource `context`, verb `Attach`.

## Rules

| Rule (`x-rest-style`) | GraphQL shape | REST | Example |
|---|---|---|---|
| item | `x(id)` → object | `GET /xs/{xId}` | `stack` → `GET /stacks/{stackId}` |
| list | `xs` → `[X]` | `GET /xs` | `stacks` → `GET /stacks` |
| search | `searchXs(input)` | `POST /xs/search` | `searchRuns` → `POST /runs/search` |
| read | other query on a resource | `GET /xs/{xId}/<kebab>` or `GET /<kebab>` | |
| sub-read | query returning a related resource | `GET /xs/{xId}/<child>` | `runStack` → `GET /runs/{runId}/stack` |
| singleton | operations on an account-wide object | `GET/PUT/DELETE /<name>` | `/oidc` |
| create | `xCreate` / `xAdd` | `POST /xs` (201) | `stackCreate` → `POST /stacks` |
| update | `xUpdate(id, input)` | `PUT /xs/{xId}` | |
| update (id in input) | `xUpdate(input: {id, …})` | `PUT /xs/{xId}`; the body drops the id | `managedUserUpdate` → `PUT /managed-users/{managedUserId}` |
| delete | `xDelete(id)` | `DELETE /xs/{xId}` | |
| attach | `xAttach(id, parent)` | `PUT /parents/{pId}/xs/{xId}` | `contextAttach` → `PUT /stacks/{stackId}/contexts/{contextId}` |
| detach | `xDetach` | `DELETE` on the same path | |
| action | other mutation with an item argument | `POST /xs/{xId}/<verb-kebab>` | `stackDisable` → `POST /stacks/{stackId}/disable` |
| field | object field with arguments | `GET /xs/{xId}/<field-kebab>?args` | `Stack.runs(before:)` → `GET /stacks/{stackId}/runs` |
| override | named in `rest-mapping.yaml` | as written | `apiKeyUser` → `POST /auth/token` |
| rpc | nothing above applies | `POST /<operation-kebab>` | none: `rpcBudget` is 0 |

### Ids inside inputs

Some updates carry the target's id inside an input object rather than as an
argument:

- `managedUserUpdate(input: {id, …})`;
- `gitlabIntegrationUpdate(…, customInput: {id, …})`;
- `bitbucketCloudIntegrationUpdateV2(input: {…, customInput: {id, …}})`.

For an `xUpdate` with no item argument, the planner looks for the one
non-null `id` inside its non-list input-object arguments, at most three
levels deep and shallowest first. Two candidates at the same depth are
ambiguous, so the mutation falls through to the other rules.

The path parameter records the binding as a dotted path in `x-graphql-arg`
(`input.customInput.id`). The body uses a `<Input>Body` component, which is
the input without that field and is marked with `x-graphql-in-path`. Inputs
on the way to the field become required, because the server needs them to
put the id back. Bruno examples lose the moved value.

A VCS update that leaves out `customInput` targets the account's default
integration in GraphQL. REST names every integration, default included, by
its ID, so `coverage.md` reports those Bruno examples as dropped.

### Parents

A non-null ID argument named after another resource nests the route under
that resource:

- `runCancel(stack: ID!, run: ID!)` → `POST /stacks/{stackId}/runs/{runId}/cancel`;
- `runTrigger(stack:)`, through an override → `POST /stacks/{stackId}/runs`.

The item argument is found before the parent, so `version` in
`moduleVersion…(module, version)` stays the item and does not become a parent.

Arguments listed in `attributeArgs` (`space`, `spaceId`, `spaceID`) never
nest. Every resource lives in a space, so nesting on it would put the whole API
under `/spaces/{id}`.

### Versions and deprecation

- **Deprecated fields** are excluded automatically with the upstream reason.
- **Superseded versions.** When `xV2` exists and plain `x` is not deprecated,
  the older one is excluded as superseded and the route is named without the
  version (`moduleUpdateV2` → `PUT /modules/{moduleId}`).

## Schemas

| GraphQL | OpenAPI 3.0.3 |
|---|---|
| `T!` | listed in `required` |
| `T` (nullable) | `nullable: true` |
| enum | `type: string` + `enum` |
| union / interface | `oneOf` + `discriminator: {propertyName: kind}` |
| object field returning another resource | `$ref: <Type>Ref` (`{id}` only), which also breaks recursion |
| input argument default | `default`, wrapped in `allOf` when on a `$ref` |
| deprecated field | dropped; listed under `x-graphql-omitted` on the schema |

### Request bodies

- A single input-object argument becomes the request body.
- Several arguments are gathered into a `<Operation>Request` component.
- Path arguments are taken out of the body; a field bound by a dotted path
  is taken out of its input through a `<Input>Body` component.
- Input-object arguments on `GET` routes are passed as JSON-encoded query
  parameters.

### Errors

Every operation documents a `default` response using an RFC 9457 `Problem`.
Operations with path parameters also document `404`.

## Extensions

| Extension | On | Meaning |
|---|---|---|
| `x-graphql` | operation | `{operation, field, type}` of the source root or object field |
| `x-graphql-arg` | parameter, property | the GraphQL argument a REST name came from |
| `x-graphql-omitted` | schema | deprecated fields left out, with reasons |
| `x-rest-style` | operation | the rule that produced the route |
| `x-bruno` | operation | the Bruno request(s) the example and docs came from |
| `x-terraform` | property, parameter | the `resource.attribute` values that land here |
| `x-sources` | info | the upstream pins |

## rest-mapping.yaml

This hand-curated file is decoded strictly.

| Key | Purpose |
|---|---|
| `rpcBudget` | ceiling on RPC-shaped routes; 0 |
| `plurals` | irregular plurals |
| `singletons` | account-wide objects addressed without an ID |
| `attributeArgs` | ID arguments that never nest |
| `operations` | per-field `method`, `path`, `params`, `public`, `tag`, `created` (answer 201) |
| `exclude` | field → reason |
| `terraform` | per-resource attribute → GraphQL input field renames |

Path parameters in an override bind to arguments by these candidates, in
order:

1. an explicit entry in `params`, which may be a dotted path into an input
   (`{referenceId: reference.id}`);
2. the parameter name itself;
3. the name without its `Id` suffix;
4. the name with `ID` or `Id` appended.

An unbound parameter fails generation, as does a dotted path that passes
through a list or a scalar.

### Override families

| Family | Shape |
|---|---|
| Sign-in | `/auth/token`, `/auth/github/{redirect,token,user}` (public), `/auth/cli-confirmation-tokens` |
| Account settings | `/scim-settings` (POST, DELETE, `/reset`), `/ui-config` (GET, PUT), `/account/api-key-non-root-managers` |
| Notifications | `POST /notifications/dismiss`, `/notifications/dismiss-all`, `/notification-groups/{groupingKey}/dismiss` |
| Active user guide | `POST /active-user-guide-progress/{abandon,complete,next-step,previous-step}` |
| Stack dependencies | `/stack-dependencies/{id}/references` (+ `/batch`), `/stack-dependency-references/{referenceId}`, batch `PUT`/`DELETE` on `/stack-dependency-references` |
| Stacks and runs | `/stacks/{stackId}/proposed-runs`, `/stacks/{stackId}/aws-role`, `/stacks/{stackId}/state/download-url`, `/runs/batch` |
| Intent projects | `/intent-projects/{intentProjectId}/configs/{configId}`, `/stacks`, and the resource dependency, review and eject routes |

## Flows: RPC to REST

From P18c (decision [0018](../decisions/0018-flows-runtime.md#api-and-clients)),
`specref` projects Flows onto REST as this section describes.
Spacelift Flows has no GraphQL API and no published specification. Its
first-party clients call a private RPC interface: `POST <base>/cli/...` from
`flowctl` and `POST <base>/provider/...` from `terraform-provider-flows`, each
with a JSON body, `Authorization: Bearer` and a `{data, error}` envelope
([23 section 10](23-flows.md#10-the-flows-api)). Its web UI's private
`/api/...` calls are not a source: linking a Flows organization needs a paid
Spacelift plan, so they are not captured
([23 section 10.5](23-flows.md#105-web-ui-api), C271). None of the GraphQL rules
above applies, so `specref` builds Flows as a second reference product,
`api/reference/spacelift-flows/`, beside `api/reference/spacelift/`, from
these pinned sources:

| Source | Licence | Read as | Feeds |
|---|---|---|---|
| `terraform-provider-flows` request and response structs | MIT | Go source parsed with `go/ast`: each RPC path with its request and response fields | the REST document, `client: provider` |
| `flowctl` `src/api.ts` | MIT | path literals and interface types | the REST document, `client: flowctl` |
| Overlay `schemas.yaml` | infraweave's own | `FlowDefinition`, `RegistryManifest` and `AppVersions`; `specref fetch` validates the 46 published templates and a live registry manifest against them | the schemas those operations carry |
| `@slflows/sdk` `index.d.ts` | none declared | identifiers only, recorded in the register `docs/data/flows-sdk-inventory.json` | nothing in the REST document |

The hand-written `api/reference/spacelift-flows/rest-mapping.yaml` gives each
RPC path its `operationId`, its tag (prefixed `Flows `, such as
`Flows Projects` or `Flows Data Tables`, so that no Flows tag collides with
the Deploy-side `Flows` tag) and its parameters. The method and path come from
the verb that opens the last segment of the RPC path, and the resource from
the noun after it, or from the family (`flows`, `apps`, `datatables`) when
the segment is a bare verb:

| Verb | REST | Example |
|---|---|---|
| `create`, `create_<noun>` | `POST` on the collection | `/provider/flows/create` → `POST /api/v1/flows/projects/{projectId}/flows` |
| `get`, `get_<noun>`, `read_<noun>` | `GET` on the item | `/provider/flows/get` → `GET /api/v1/flows/flows/{flowId}` |
| `update`, `update_<noun>` | `PATCH` on the item | `/provider/organization/update_secret` → `PATCH /api/v1/flows/projects/{projectId}/secrets/{key}` |
| `delete`, `delete_<noun>` | `DELETE` on the item | `/provider/apps/delete_installation` → `DELETE /api/v1/flows/app-installations/{appInstallationId}` |
| `list_<nouns>` | `GET` on the collection | `/cli/flows/list_flows` → `GET /api/v1/flows/projects/{projectId}/flows` |
| `start_<noun>` | `POST` on the `<noun>` collection | `/cli/flows/start_test_scenario_run` → `POST /api/v1/flows/test-scenarios/{testScenarioId}/runs` |
| `start_<noun>_batch` | `POST` of a list body on the collection's `/batch` route, decision [0003](../decisions/0003-api-conventions.md)'s `expensive` batch paths | `/cli/flows/start_test_scenario_run_batch` → `POST /api/v1/flows/flows/{flowId}/test-scenario-runs/batch` |
| `cancel_<noun>`, `confirm_<noun>` | an action `POST` on the item, as the action rule above | `/provider/apps/confirm_installation` → `POST /api/v1/flows/app-installations/{appInstallationId}/confirm` |
| `attach_to_<parent>`, `detach_from_<parent>` | `PUT` and `DELETE` on the sub-resource | `/provider/datatables/attach_to_flow` → `PUT /api/v1/flows/flows/{flowId}/data-tables/{dataTableId}` |

- **The parent comes from the request's keys**, not from the RPC family: a
  request that carries `projectId` nests its collection under
  `/projects/{projectId}`, so `/provider/organization/*_secret` is
  project-scoped. A noun that starts with its parent's name drops it
  (`create_datatable_column` → `POST /api/v1/flows/data-tables/{dataTableId}/columns`).
- **The item ID comes from `id` or `<resource>Id`**, whichever the request
  carries: `/provider/flows/update` sends `id`, `/provider/flows/get` sends
  `flowId`, and both address `{flowId}`. An item named by a key inside its
  parent keeps the parent in its path, as secrets do with `{key}`.

These are overrides, with `method` and `path` in their entry:

| RPC path | REST |
|---|---|
| `/cli/projects` (no verb) | `GET /api/v1/flows/projects` |
| `/provider/apps/get_version_id` | a query on the versions collection: `GET /api/v1/flows/app-versions` with `name`, `registry` and `version` |
| `/provider/flows/get_entity_lifecycle_status`, `/provider/apps/get_installation_status`, `/provider/apps/get_installation_config_field` | sub-resource reads: `GET /api/v1/flows/blocks/{blockId}/lifecycle-status`, `GET /api/v1/flows/app-installations/{appInstallationId}/status`, `GET /api/v1/flows/app-installations/{appInstallationId}/config-fields/{key}` |
| `/provider/apps/update_installation_config`, `update_installation_metadata`, `update_installation_version` | three sub-resources, not one `PATCH` on the installation: `PATCH /api/v1/flows/app-installations/{appInstallationId}/config`, `/metadata` and `/version` |
| `/provider/flows/confirm_entity_lifecycle` | `POST /api/v1/flows/blocks/{blockId}/confirm`, the entity being a block |
| `/provider/flows/plan_changes` | `POST /api/v1/flows/flows/{flowId}/plan` |
| `/provider/flows/apply_config` | `PUT /api/v1/flows/flows/{flowId}/definition` |
| `/cli/flows/export`, `/provider/flows/export_definition` | one operation: `GET /api/v1/flows/flows/{flowId}/definition`, with `omitIds`, `omitLayout` and `omitAppSchemas` as query parameters |

Paths are written in full here because the reference and the served document
share the server URL `/api/v1` and Flows' paths begin `/flows/` in both, which
is also the prefix of the Flows surfaces outside OpenAPI (the agent
WebSocket, `/flows/mcp`, `/flows/e/...` and `/flows/forms/...`).

- **Every RPC path is mapped.** An RPC path a source names that has no entry,
  and an entry whose RPC path no source names any more, fail generation, as
  stale GraphQL entries do.
- **Outputs.** `api/reference/spacelift-flows/flows.openapi.yaml` and
  `coverage.md`. Each operation carries `x-rpc`, a list of `{path, client}`
  with `client` either `provider` or `flowctl`, because several RPC paths
  can map to one operation: the export operation lists `/cli/flows/export`
  (`flowctl`) and `/provider/flows/export_definition` (`provider`).
- **Operations no client reaches.** A served Flows operation that no
  public client calls (invocations and event trees, retry and re-emit,
  members, teams, service accounts and API keys, agent pools, registry
  subscriptions, OIDC, audit and OTLP export) is designed by infraweave from
  the docs and decision 0018's data model and carries `x-upstream: none`;
  parity treats it as infraweave-only.
- **Parity.** `TestUnitFlowsParity` compares the Flows reference with the
  served `api/api.openapi.yaml` against `api/parity-baseline.flows.json`,
  as `TestUnitAPIParity` compares the Deploy reference against
  `api/parity-baseline.deploy.json`. The five test-scenario operations are
  accepted there as missing until test scenarios are designed (PD-16).
- **Capabilities.** Each record of
  [`capabilities.json`](../../data/capabilities.json) names its `product`
  (`deploy` or `flows`), and docscheck checks each reference's tags against
  the records of its own product.

## From the reference to the served API

These rules shape the reference only. infraweave's served API
(`api/api.openapi.yaml`, Deploy and Flows in one document) starts from it and adds the conventions in
[decision 0003](../decisions/0003-api-conventions.md):
- list routes return a `<Type>Page` envelope with `limit` and `cursor`, where
  the reference returns bare arrays;
- `POST` and `PATCH` accept an optional `Idempotency-Key`;
- every operation can answer `429` and carries `X-RateLimit-*` headers, with
  its cost class in `x-infraweave-cost`.

Searches keep the reference's `SearchInput` with `first`/`after` and
`pageInfo`.

`specref scaffold --tag <Tag>` (`make scaffold TAG=Stacks FAMILY=stacks`)
starts a family by copying from the reference:
- **Paths:** every path holding an operation with the tag. A path lives in
  one fragment, so operations under another tag that share it come along and
  are reported.
- **Components:** the closure of what those paths reach, through `$ref` and
  discriminator mappings. Anything another fragment of the service defines is
  referenced, not copied.
- **Conventions:** the decision 0003 conventions are applied to each
  operation and listed in its `x-infraweave-divergence`.
- **Known gap:** the 8 reference operations without a tag can't be
  scaffolded this way; they are added to a fragment by hand.

## Open work

- **Nested Terraform blocks** are listed but not mapped
  ([05](05-terraform-provider.md)).

## Sources

- [`api/reference/spacelift/sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml): the pinned inputs.
- [`internal/specref/`](../../../internal/specref/): the mapping rules, the generator and the scaffolder.
- [`api/reference/spacelift/rest-mapping.yaml`](../../../api/reference/spacelift/rest-mapping.yaml): overrides and exclusions.
- [`api/reference/spacelift/coverage.md`](../../../api/reference/spacelift/coverage.md): the generated coverage report.
- [Decision 0003](../decisions/0003-api-conventions.md): the conventions the scaffolder applies.
- [23 section 10](23-flows.md#10-the-flows-api): the Flows RPC interface, read from flowctl `96bdd03303a6` and terraform-provider-flows v0.7.0 (`9d1a7ad4d937`).
- [Decision 0018](../decisions/0018-flows-runtime.md#api-and-clients): infraweave's Flows operations in the served document.
