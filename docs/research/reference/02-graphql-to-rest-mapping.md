# 02: Mapping GraphQL root fields to REST

Checked against: the snapshots pinned in
[`sources.lock.yaml`](../../../api/reference/spacelift/sources.lock.yaml): introspection
`b3255fe38f5e` (fetched 2026-10-28), spacelift-api-bruno `7cad4574954b` and
terraform-provider-spacelift v1.57.0.

Research baseline: **2026-10-28**.

How `internal/specref` turns each of Spacelift's 665 root fields into a
resource-oriented route, and how `rest-mapping.yaml` handles the fields that
no rule can infer. Decision [0001](../decisions/0001-rest-first-spacelift-surface.md)
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

## From the reference to the served API

These rules shape the reference only. infraweave's served API
(`api/deploy.openapi.yaml`) starts from it and adds the conventions in
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
