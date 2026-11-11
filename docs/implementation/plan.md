# Implementation plan

This plan orders the work that turns the [research](../research/README.md),
the [decision records](../research/decisions/README.md) and the
[architecture options](../research/architecture-options/README.md) into a
working infraweave. The decision records say *what* infraweave does and *why*;
report [26](../research/reference/26-scope-and-tiers.md) says *which* capabilities are in scope. This file says in
which *order* they are built, the *tasks* each phase holds and the *exit
criteria* that must hold before a phase counts as done.

Every phase is also subject to the [definition of done](definition-of-done.md).
The target package tree is in [repo-layout.md](repo-layout.md), every
feature's kind, seeded tier and owning phase is in the
[feature catalogue](../data/features.json), which the
[capability roadmap](capability-roadmap.md) explains, and every deliberate difference
from Spacelift is in the [deviations register](../data/deviations.json),
which [deviations.md](deviations.md) explains. Research
baseline: **2026-10-28**. Implementation status is reviewed against `main`
and the GitHub issues on the same date.

Decisions 0001 to 0024 are Accepted, and this plan builds them. Where a
decision marks a behaviour as infraweave's choice until a capture of report
[30](../research/reference/30-open-questions.md) settles it, the phase builds
the choice and its scenarios assert it; P28 replays the captures, and the
[deviations a capture may change](deviations.md#deviations-a-capture-may-change)
are the records that can move.

## Scope direction

- **Everything Spacelift offers is in scope**: Deploy at every tier,
  Intelligence (AI integrations, Infra Assistant, Intent and MCP), Flows and
  the console. The scope rule and the supersession test are decision
  [0021](../research/decisions/0021-capability-scope-and-supersession.md)'s. An interface is left out only when it passes that test,
  with its replacement, evidence, coverage of the original use and the path
  for stored data recorded in report 26 section 7; the
  [supersession register](capability-roadmap.md#supersession-register) lists
  every such interface.
- **Seeded tiers, billing off by default.** Every feature is an entry of one
  catalogue, [docs/data/features.json](../data/features.json), from which
  `make generate` writes `internal/features/catalogue.gen.go`: a capability,
  an option or a limit. Five tiers (Free, Starter, Starter+, Business and Enterprise) are
  seeded from report 26, and the operator can add its own. Billing is off
  by default, so every organization behaves as Enterprise: `tier` answers the
  Enterprise tier object, `tierFeatures` lists every feature not in
  `features.disabled`, and no limit applies. With billing on, a feature
  outside the organization's entitlement answers `403 feature-not-in-tier`,
  and a limit, pooled across the organization's accounts, answers
  `403 limit-reached`
  ([0021](../research/decisions/0021-capability-scope-and-supersession.md),
  [0022](../research/decisions/0022-api-errors-and-update-semantics.md), P3b).
- **Root, organization, account, spaces.** `root` is the installation's
  top node: only operators act on it, and it holds nothing but
  organizations. An organization is a customer's agreement: its tier and
  grants, limits pooled across its accounts, its members and its identity
  providers. An account is a separate business entity and the data
  boundary: its spaces, stacks, runs, state and secrets are invisible to
  every other account, and its top space is addressed by its immutable
  slug. Nothing inherits from root or from the organization. Members
  authenticate at the organization and are authorized per account by the
  account's `LOGIN` policies. Organization admins manage accounts, members
  and limits through the served `Organization` tag, outside the parity
  surface; the operator API keeps the installation-level actions
  ([0006](../research/decisions/0006-authentication-and-tokens.md),
  [0007](../research/decisions/0007-authorization-spaces-and-roles.md),
  [0021](../research/decisions/0021-capability-scope-and-supersession.md),
  P3, P3b, P4, P13b, P23).
- **infraweave's own names.** No served operationId, schema, property, enum
  value, header, environment variable, policy input key or description
  contains "spacelift". Decision 0001's rename table maps each reference name that
  does to its infraweave name, such as `spaceliftAwsAccountId` to
  `installationAwsAccountId`, and "root admin" becomes "account admin";
  `TestUnitServedNames` checks every served bundle against both from P1c.
- **REST, not GraphQL.** The served API is REST over JSON described by
  OpenAPI 3 ([0001](../research/decisions/0001-rest-first-spacelift-surface.md), [0003](../research/decisions/0003-api-conventions.md), [0022](../research/decisions/0022-api-errors-and-update-semantics.md)). The Spacelift reference
  `api/reference/spacelift/spacelift.openapi.yaml` is the parity oracle for
  Deploy and `api/reference/spacelift-flows/flows.openapi.yaml` for Flows; the
  served documents are `api/api.openapi.yaml`, which holds Deploy and Flows
  under `/api/v1`, and the operator API's `api/operator.openapi.yaml`. Each difference is
  recorded in `x-infraweave-divergence` and in the
  [deviations register](../data/deviations.json).
- **infraweave's own clients.** `pkg/sdk` wraps the generated
  `pkg/client/<spec>` packages and is the only way infraweave's Go clients
  call the API ([0001](../research/decisions/0001-rest-first-spacelift-surface.md)). It lands in P1c, where every acceptance step
  drives the API through it, and gains the token exchange in P3.
  `infraweavectl` starts in P3b with its `operator` group and covers
  report 06 section 8 and Flows in P24b; infraweave's provider is P24 and the
  console ([0023](../research/decisions/0023-console.md)) is P24c. Each is held to a parity oracle
  ([0016](../research/decisions/0016-testing-and-acceptance.md)).
- **One event model.** Deploy and Flows share decision
  [0015](../research/decisions/0015-events-notifications-webhooks-audit.md)'s event envelope and emit path from the queue phase on: P2c's
  outbox writes the `events` rows that P18 fans out and that the Flows
  engine consumes ([0018](../research/decisions/0018-flows-runtime.md)). Flows therefore follows the events phase
  directly, as P18c and P18d.
- **One expression language.** Every expression a user writes is CEL
  ([0024](../research/decisions/0024-expression-language.md)). `internal/platform/expr` and the expression endpoints land in
  P6 with environment interpolation, the first surface that uses them; each
  later surface adds its context in its own phase.
- **FIPS from the start.** The `fips-only` CI job runs `make test` and
  every acceptance partition under `GOFIPS140=certified` and
  `GODEBUG=fips140=only` from P2b, where the keyring and the object store drivers land
  ([0013](../research/decisions/0013-secrets-at-rest.md), [0016](../research/decisions/0016-testing-and-acceptance.md)).
- **Self-hosted, with SaaS behaviour.** infraweave is installed by its
  operator but mirrors SaaS: it keeps the Azure integration, an optional
  public worker pool and an optional installation AI default, and no
  capability depends on the public worker pool ([0021](../research/decisions/0021-capability-scope-and-supersession.md)).
- **Evidence over claims.** A capability is `implemented` only when decision
  [0016](../research/decisions/0016-testing-and-acceptance.md)'s evidence rule holds: every operation is served, answers
  something other than 501, and is named by passing `@op:` scenarios for a
  success, a denial and a failure. The state of each record of
  [capabilities.json](../data/capabilities.json) follows the tests through
  `specref parity --states --check`, never the other way round.

## Open items

These have no decision. The plan builds what does not depend on the answer
and invents no answer.

| Item | State | What the plan does meanwhile |
|---|---|---|
| Where the provider's checks run | Decision [0001](../research/decisions/0001-rest-first-spacelift-surface.md) places the provider's source in `weaveplatform/terraform-provider-infraweave`; decision [0016](../research/decisions/0016-testing-and-acceptance.md) defines `provider-parity` and `TestAccProvider` against a live `infraweave all` and lists the job in this repository's CI | P24 builds the provider in its own repository over `pkg/sdk`, and keeps `api/reference/provider-parity.yaml` and the `test/terraform/` configurations in this repository, which builds the provider at a pinned commit |

## Build status

Updated as phases land. "Done" means the exit criteria hold, `make gate`
passes with merged coverage of at least 95% in total and 90% per package,
and lint is clean.

| Phase | Status | Notes |
|---|---|---|
| P0 Specification reference | Done | [#5](https://github.com/weaveplatform/infraweave/issues/5): the Spacelift reference spec, its coverage report, the Bruno and Terraform provider enrichment and `make verify-reference` |
| P1 Served API skeleton | Done | Epic [#8](https://github.com/weaveplatform/infraweave/issues/8). Done: [#9](https://github.com/weaveplatform/infraweave/issues/9) bundling, [#10](https://github.com/weaveplatform/infraweave/issues/10) decision 0003 and the convention components, [#12](https://github.com/weaveplatform/infraweave/issues/12) scaffolding with Stacks seeded, [#13](https://github.com/weaveplatform/infraweave/issues/13) the strict chi server, client and 501 stubs, [#14](https://github.com/weaveplatform/infraweave/issues/14) `infraweave serve`, [#11](https://github.com/weaveplatform/infraweave/issues/11) the parity report, [#15](https://github.com/weaveplatform/infraweave/issues/15) rate limits and idempotency |
| P1b Domain layout and depguard | Done | Epic [#72](https://github.com/weaveplatform/infraweave/issues/72). Done: [#73](https://github.com/weaveplatform/infraweave/issues/73) the moves and the `deploy` to `api` rename, [#74](https://github.com/weaveplatform/infraweave/issues/74) depguard, the fixtures that prove each rule fires and the `time.Sleep` ban |
| P1c Test harness, SDK and parity tooling | Done | **M1**. Epic [#94](https://github.com/weaveplatform/infraweave/issues/94) |
| P1d Flows specification reference | Done | Epic [#82](https://github.com/weaveplatform/infraweave/issues/82). Done: [#83](https://github.com/weaveplatform/infraweave/issues/83) the product seam, [#84](https://github.com/weaveplatform/infraweave/issues/84) the Flows reference, [#85](https://github.com/weaveplatform/infraweave/issues/85) Flows parity, [#86](https://github.com/weaveplatform/infraweave/issues/86) product-scoped capabilities, [#87](https://github.com/weaveplatform/infraweave/issues/87) CI for both references |
| P2 PostgreSQL, migrations and operability | Done | Epic [#114](https://github.com/weaveplatform/infraweave/issues/114). Done: [#115](https://github.com/weaveplatform/infraweave/issues/115) the pools, transactions, `LISTEN` fan-out, ULIDs and `sqltest`, [#116](https://github.com/weaveplatform/infraweave/issues/116) IAM token authentication for AWS, Azure and GCP, [#117](https://github.com/weaveplatform/infraweave/issues/117) the migration runner, the `platform` stream, `infraweave migrate` and the schema checks, [#118](https://github.com/weaveplatform/infraweave/issues/118) `postgres-next`, [#119](https://github.com/weaveplatform/infraweave/issues/119) idempotency keys, rate limits and disabled callers shared across replicas, [#120](https://github.com/weaveplatform/infraweave/issues/120) structured logs, request IDs, probes, the shutdown order and the `scheduler` and `all` modes, [#121](https://github.com/weaveplatform/infraweave/issues/121) problem `instance` and `service-unavailable` |
| P2b Object storage and secrets at rest | Done | Epic [#149](https://github.com/weaveplatform/infraweave/issues/149). Done: [#150](https://github.com/weaveplatform/infraweave/issues/150) the sealed formats, the keyring and the `local` KEK, [#151](https://github.com/weaveplatform/infraweave/issues/151) object storage on S3, Cloud Storage, Blob Storage and a local volume, `blob_objects` with `blob.delete` and `blob.expire`, and sealed stores, [#152](https://github.com/weaveplatform/infraweave/issues/152) the `awskms`, `gcpkms`, `azurekv` and `vault` KEK providers, [#153](https://github.com/weaveplatform/infraweave/issues/153) data key rotation and reseal, `rewrap` between KEKs, `infraweave secrets` and sealed idempotency bodies, [#154](https://github.com/weaveplatform/infraweave/issues/154) the `fips-only` job, the nightly `objectstore-cloud` job and the FIPS line of `infraweave version` |
| P2c Work queue, cron, outbox and the event model | Done | Epic [#132](https://github.com/weaveplatform/infraweave/issues/132). Done: [#133](https://github.com/weaveplatform/infraweave/issues/133) transactional enqueue and claims across replicas, [#135](https://github.com/weaveplatform/infraweave/issues/135) workers with leases, retries, timeouts and the ordered shutdown, [#136](https://github.com/weaveplatform/infraweave/issues/136) five-field cron and periodic tasks, [#139](https://github.com/weaveplatform/infraweave/issues/139) the outbox, typed event kinds and fan-out to consumers, [#134](https://github.com/weaveplatform/infraweave/issues/134) audit entries in the change's transaction, [#138](https://github.com/weaveplatform/infraweave/issues/138) the queue and periodic tasks in the `scheduler` and `all` modes, [#137](https://github.com/weaveplatform/infraweave/issues/137) `messageQueueStats` |
| P3 Identity and tokens | Done | Epic [#165](https://github.com/weaveplatform/infraweave/issues/165). Done: [#166](https://github.com/weaveplatform/infraweave/issues/166) the `access` domain, the tenancy and identity tables, `account_users` and the kernel principal, [#167](https://github.com/weaveplatform/infraweave/issues/167) JWTs under rotating installation signing keys, [#168](https://github.com/weaveplatform/infraweave/issues/168) signing through AWS KMS, Cloud KMS, Key Vault and Vault, [#169](https://github.com/weaveplatform/infraweave/issues/169) the bearer-token middleware, sessions and their revocation across replicas, [#170](https://github.com/weaveplatform/infraweave/issues/170) API keys and OIDC API keys, [#171](https://github.com/weaveplatform/infraweave/issues/171) the break-glass login, browser sessions, CLI tokens and account switching, [#172](https://github.com/weaveplatform/infraweave/issues/172) the account reads, [#173](https://github.com/weaveplatform/infraweave/issues/173) the operator API with organizations, break-glass credentials and `infraweave auth hash-password` |
| P3b Organizations, members, account settings and the feature catalogue | Done | Epic [#184](https://github.com/weaveplatform/infraweave/issues/184). Done: [#185](https://github.com/weaveplatform/infraweave/issues/185) the `features` domain below `access`, its tables and `featuresapi`; [#186](https://github.com/weaveplatform/infraweave/issues/186) the generated catalogue, seeded tiers, the billing switch, the feature middleware on every operation, the dequeue skip and the `seats` check at an API key's first use; [#187](https://github.com/weaveplatform/infraweave/issues/187) the account settings no other domain owns, with the account delete; [#188](https://github.com/weaveplatform/infraweave/issues/188) tiers, organization tiers, grants, the billing switch with the default tier, dead jobs and the data key rotation through the operator API, and `infraweavectl` with its `operator` group; [#189](https://github.com/weaveplatform/infraweave/issues/189) the Billing and Feature Flags reads from the organization's entitlement, with the subscription mutations refused; [#190](https://github.com/weaveplatform/infraweave/issues/190) the Organization tag with accounts, members, organization-admin, organization keys and account limits; [#192](https://github.com/weaveplatform/infraweave/issues/192) the Managed Users tag: invitations, pending members holding seats, removal from an account, invite and role change requests on the account trail, and groups from the identity provider groups each sign-in reports; [#193](https://github.com/weaveplatform/infraweave/issues/193) personal API keys, whose tokens act as their owner in the key's account and which their owner's removal or deactivation disables as `DEPROVISIONED`; [#191](https://github.com/weaveplatform/infraweave/issues/191) the OAuth server for MCP clients: registration, consent in the console, PKCE `S256` codes and rotating refresh tokens, each grant a session of the member's |
| P4 Spaces and authorization | Done | Epic [#209](https://github.com/weaveplatform/infraweave/issues/209). Done: [#211](https://github.com/weaveplatform/infraweave/issues/211) each account's space tree, its top space and `legacy`, and the closure table; [#212](https://github.com/weaveplatform/infraweave/issues/212) the generated action catalogue, the system roles, the role tables and each account admin's `space-admin` binding; [#213](https://github.com/weaveplatform/infraweave/issues/213) the evaluator, `Authorize`, `Reachable` and the explaining variant, from per-account snapshots each replica rebuilds on `authz:<account>` or a moved `authz_version`; [#214](https://github.com/weaveplatform/infraweave/issues/214) every operation's action and subject in the spec, the route table that denies a route no operation names, the middleware that decides each operation's action on its subject's space, infraweave's account actions on API keys, sessions, managed users and groups, and the `allowedActions` of their views; [#215](https://github.com/weaveplatform/infraweave/issues/215) the Spaces operations but search, the `query:` subject, and the non-admin space creation setting with the second check it gives space admins below the top space; [#216](https://github.com/weaveplatform/infraweave/issues/216) the generic search that compiles `SearchInput` to SQL under the caller's reachable spaces, `searchSpaces` and `searchSpacesSuggestions`, the `spaces` list on the compiled filter, and the action catalogue's `GET /actions` |
| P4b Roles and role bindings | Done | Epic [#235](https://github.com/weaveplatform/infraweave/issues/235). Done: [#236](https://github.com/weaveplatform/infraweave/issues/236) custom roles created, read, listed, replaced with `PUT`, patched and deleted from the action catalogue, each slug made from its name and kept, a disabled action refused as `422` naming it and a deprecated one by the `Action` enum as `400`, a system role refused any change and a role with bindings its delete, each write moving the account's `authz_version`; [#237](https://github.com/weaveplatform/infraweave/issues/237) roles bound to managed users, groups and API keys in spaces, one at a time or in all-or-nothing batches, each needing `SPACE_ADMIN` on its space, and the access rules and role bindings that managed users, groups and API keys carry stored as bindings, removed with their actor; [#238](https://github.com/weaveplatform/infraweave/issues/238) the user, group, API key and session grant views and their suggestions, each holder and its grants found by the evaluator that decides requests, `RESIDENT`, `PROPAGATED` or `INHERITED` with `space-reader`, and each view searching its holders alone; [#239](https://github.com/weaveplatform/infraweave/issues/239) roles bound to stacks in other spaces, one at a time or in all-or-nothing batches, each needing `STACK_MANAGE` on the stack's space and `SPACE_ADMIN` on the space bound in, a top-space binding needing the stack to live in the top space, bindings kept when the stack moves and deleted with it, and `stacksWithAccessToSpace` listing a space's residents or, for its admin, every stack whose own read or binding reaches it; [#240](https://github.com/weaveplatform/infraweave/issues/240) a removed binding, a role's changed actions or a deleted group reaching a member's live session on every replica within a second, every write path audited to move the account's `authz_version`, send `authz:<account>` and drop the writing replica's snapshot on commit, and an `ACTIVE` managed user with no binding signing in with empty grants; [#241](https://github.com/weaveplatform/infraweave/issues/241) each Organization tag operation refusing an account admin without the organization role with `403` naming `ORGANIZATION_ADMIN`, and a limit cleared for no account or no limit refused; [#242](https://github.com/weaveplatform/infraweave/issues/242) the managed user, group and API key operations refusing a key bound `space-writer` on the top space with `403` naming each action, and an admin of a space below the top reading the account's users, groups and keys but changing none; [#243](https://github.com/weaveplatform/infraweave/issues/243) the account settings and `messageQueueStats` refusing a key bound `space-writer` on the top space with `403` naming `ACCOUNT_ADMIN`, the settings any principal of the account reads refused to a session no account admits with `401`, and each with its failure case; [#292](https://github.com/weaveplatform/infraweave/pull/292) the success and failure scenarios P3b's operations lacked and `spacePatch`'s denial, Billing's subscription mutations having no success to show because they are always refused |
| P5 Stacks | Done | **M2**. Epic [#227](https://github.com/weaveplatform/infraweave/issues/227). Done: [#228](https://github.com/weaveplatform/infraweave/issues/228) the `vendors` and `stacks` domains after `access`, the stacks stream, `stacksapi.Stacks`, `accessapi.Spaces.Hold`, and the space guard that refuses deleting a space a stack lives in; [#229](https://github.com/weaveplatform/infraweave/issues/229) `internal/iac` with the `terraform` and `opentofu` settings checks, refused at write time as `422 validation-failed`, the `iactest` contract suite, and the `vendor-terraform` and `vendor-opentofu` options on their `vendorConfig` members; [#230](https://github.com/weaveplatform/infraweave/issues/230) stacks created, read, listed, replaced with `PUT`, moved, reslugged, disabled, enabled, locked, unlocked, starred and deleted in spaces, each change a `stack.changed` event in the outbox; [#231](https://github.com/weaveplatform/infraweave/issues/231) a `PATCH` merge patch beside every update `PUT`, `ETag` on reads and writes, `If-Match` answering `412 precondition-failed` when stale, every input schema refusing an unknown field as `400` with its pointer, and the one label rule on stacks and spaces; [#232](https://github.com/weaveplatform/infraweave/issues/232) `searchStacks` and `searchStacksSuggestions` over the reachable spaces, the space tree read through `accessapi.Spaces.Tree`, and `searchtest.FuzzCompile` holding every entity's search to parameterized SQL; [#233](https://github.com/weaveplatform/infraweave/issues/233) the Version Catalog's `terraformVersions`, `openTofuVersions`, `terraformEffectiveVersion` and `openTofuEffectiveVersion` from `vendor_releases`, which the hourly `vendors.refresh` task reads from HashiCorp's and OpenTofu's release indexes, `vendorsapi.Catalog`, the `indextest` contract suite over fixtures from `make vendors-fixtures`, and the nightly `vendors-upstream` job; [#234](https://github.com/weaveplatform/infraweave/issues/234) `stackRuns`, `stackRunsInRange`, `stackProposedRuns` and `stackTasks` answering empty pages, and `stackRun` answering `404`, on the stack's space until P7; [#257](https://github.com/weaveplatform/infraweave/issues/257) account and organization deletion that deletes `access`' rows at once and appends `account.deleted`, which the `features` and `stacks` purges consume in batches, and the nightly `integrity.orphans` count of held IDs whose row is gone |
| P6 Contexts, configuration and expressions | Done | Epic [#254](https://github.com/weaveplatform/infraweave/issues/254). Done: [#255](https://github.com/weaveplatform/infraweave/issues/255) each flat domain's logic package named `internal/core`; [#256](https://github.com/weaveplatform/infraweave/issues/256) P6's contracts in the plan and decisions: the masker in `internal/platform/mask`, the error `range`, the `/configs/{configId}` item routes, the expression endpoints, the `environment` surface, the stack guard and back edge, and captures C98 and C101 to C103 moved to P9; [#258](https://github.com/weaveplatform/infraweave/issues/258) the `configuration` domain after `stacks`, with its stream, and the `expressions` domain last, each with its `graph-` rule; [#259](https://github.com/weaveplatform/infraweave/issues/259) decision 0013's masker for literal values in `internal/platform/mask`, which the agent may import; [#260](https://github.com/weaveplatform/infraweave/issues/260) `stacksapi.Stacks.Hold` and `Find`, the `StackGuard` a stack's move and delete ask, `accessapi.SpaceNode.Inherit`, and the stack view's contexts and config elements through the `stacksapi.StackConfiguration` back edge; [#261](https://github.com/weaveplatform/infraweave/issues/261) the expression engine in `internal/platform/expr`, the `expressions` limits, and the error `range`; [#262](https://github.com/weaveplatform/infraweave/issues/262) contexts created, read, listed, searched, replaced with `PUT`, patched and deleted in spaces, their config elements added, set by name and deleted with each value sealed to its row and a write-only value answered only by its checksum, their attachments to stacks within reach, and the space and stack guards that refuse a delete or move leaving an attachment out of reach, each change a `context.changed` event in the outbox; [#263](https://github.com/weaveplatform/infraweave/issues/263) a stack's own config elements, added, set at `PUT /stacks/{stackId}/configs/{configId}`, read and deleted, never answering a write-only value, deleted with the stack, and the contexts a stack may attach; [#264](https://github.com/weaveplatform/infraweave/issues/264) contexts attached automatically to the stacks within reach that carry the label their `autoattach:<label>` names, or every one for `autoattach:*`, following label and space changes on the context's side in its write and on the stack's through a `stack.changed` consumer, under one lock per account, an automatic attachment refused a manual detach; [#265](https://github.com/weaveplatform/infraweave/issues/265) `configurationapi.Environment.Resolve`, a stack's elements by report 13's precedence with every value opened and each phase's hooks in its nesting order, the `environment` expression surface with `env` and `stack` bound from one registry `internal/app` builds, and a value's `${{ … }}` templates checked when it is saved, a stack's read of a variable it does not resolve to refused with its range, a value reading a write-only one made write-only; `expr.Check` reports each literal read with its range; [#266](https://github.com/weaveplatform/infraweave/issues/266) the `Expressions` tag's check, complete and evaluate over every registered surface, starting with `environment`, a stack the caller cannot read answering 404, a context closing `env` to the names its stack resolves to so a misspelt one is an error with its range, a write-only value evaluated masked, and the tag's `capabilities.json` record as infraweave's own product; [#267](https://github.com/weaveplatform/infraweave/issues/267) the `configuration.purge` consumer of `account.deleted`, deleting a deleted account's attachments, config elements and contexts 500 rows a statement, each its own transaction, a purge stopped part way finishing when run again |
| P6b Sources | Done | Epic [#295](https://github.com/weaveplatform/infraweave/issues/295). Done: [#296](https://github.com/weaveplatform/infraweave/issues/296) the plan and decisions rewritten for sources before runs; [#297](https://github.com/weaveplatform/infraweave/issues/297) the `vcs` domain with the `Sources` port other domains call; [#298](https://github.com/weaveplatform/infraweave/issues/298) a Gitea fixture seeded per `@vcs` scenario; [#299](https://github.com/weaveplatform/infraweave/issues/299) refs resolved and files read from public Git repositories over HTTPS, `rawgit` passing `sourcetest` against Gitea; [#300](https://github.com/weaveplatform/infraweave/issues/300) each stack's branch head and tracked commit, `stackSyncCommit` and `stackSetCurrentCommit` |
| P7 Run state machine and scheduling | Done | Epic [#301](https://github.com/weaveplatform/infraweave/issues/301). Done: [#302](https://github.com/weaveplatform/infraweave/issues/302) the `runs` domain after `configuration`, its tables, the run types and the stack's run projection; [#303](https://github.com/weaveplatform/infraweave/issues/303) every transition of the register held as data in the machine, every other pair refused; [#304](https://github.com/weaveplatform/infraweave/issues/304) a run moved between states in one fenced transaction, its transition recorded and `runsapi.Updated` in the outbox, and created once per stack, type and commit among runs that have not ended; [#305](https://github.com/weaveplatform/infraweave/issues/305) a stack's queued runs moved to READY in priority order by one coalesced `run.schedule` job, a blocking run once it takes the stack's run lock and a proposed run whatever the lock; [#306](https://github.com/weaveplatform/infraweave/issues/306) `runTrigger` on a stack's tracked commit or one the caller names, a locked stack taking runs from its holder alone and a disabled stack none; [#307](https://github.com/weaveplatform/infraweave/issues/307) `run`, `runStack`, `searchRuns` with offset and total, `searchRunsSuggestions`, `runCancel` (`RUN_CANCEL_BLOCKING` for the lock's holder), `runPrioritySet`, each run's `allowedActions`, and a stack's run lists from the runs domain; [#308](https://github.com/weaveplatform/infraweave/issues/308) `run` on the environment surface, declared by `runs` beside `configuration`'s `env` and `stack`; [#309](https://github.com/weaveplatform/infraweave/issues/309) the `runs.purge` consumer of `account.deleted`, 500 rows a statement. Captures C25 and C33 are not yet recorded; until they are, the scenarios assert decision 0008's choices |
| P8 Worker protocol, private pools and the Docker launcher | Not started | |
| P8b Managed state backend | Not started | |
| P9 First tracked run end to end | Not started | **M3** |
| P10 Run lifecycle completeness | Not started | |
| P11 State management | Not started | |
| P12 VCS integrations | Done | Epic [#313](https://github.com/weaveplatform/infraweave/issues/313). Done: [#314](https://github.com/weaveplatform/infraweave/issues/314) the plan and decisions rewritten with P12 before runs and every provider a peer; [#315](https://github.com/weaveplatform/infraweave/issues/315) VCS integrations stored with sealed credentials, one default per provider; [#316](https://github.com/weaveplatform/infraweave/issues/316) GitHub, [#317](https://github.com/weaveplatform/infraweave/issues/317) GitLab, [#321](https://github.com/weaveplatform/infraweave/issues/321) Bitbucket Cloud, [#322](https://github.com/weaveplatform/infraweave/issues/322) Bitbucket Data Center and [#323](https://github.com/weaveplatform/infraweave/issues/323) Azure DevOps, each with its integration operations and provider; [#318](https://github.com/weaveplatform/infraweave/issues/318) repositories, branches and users browsed through every provider's integrations; [#319](https://github.com/weaveplatform/infraweave/issues/319) private stacks tracked on every provider, each run handed its source credential; [#320](https://github.com/weaveplatform/infraweave/issues/320) every provider proved by `vcs-lab` against real repositories that Terraform provisions. The Bitbucket Data Center lab is optional: it runs only where the `vcs-lab` environment holds a licence, and is skipped otherwise |
| P12b VCS events and feedback | Not started | |
| P13 Policy engine and workflow policies | Not started | **M4** |
| P13b Login, task and initialization policies, sampling and simulation | Not started | |
| P14 Public worker pool, Kubernetes workers and signed runs | Not started | |
| P15 Cloud integrations and OIDC federation | Not started | |
| P16 Scheduling, drift detection and stack dependencies | Not started | |
| P17 Module registry | Not started | |
| P17b Provider registry and GPG keys | Not started | |
| P18 Notifications, webhooks, Slack and event streams | Not started | |
| P18b Audit trail and observability | Not started | **M5** |
| P18c Flows engine and API | Not started | |
| P18d Flows apps, agents and Deploy blocks | Not started | **M6** |
| P19b VCS agent pools and other source providers | Not started | |
| P20 Other vendors | Not started | |
| P20b Ansible, OpenTofu migration and plugins | Not started | |
| P21 Blueprints, templates and external integrations | Not started | |
| P22 Resources, search and console support | Not started | |
| P22b Scans | Not started | **M7** |
| P23 Organization identity providers, SCIM and MFA | Not started | |
| P24 infraweave Terraform provider | Not started | |
| P24b infraweavectl | Not started | **M8** |
| P24c Console | Not started | **M9** |
| P25 AI integrations, Infra Assistant and MCP | Not started | |
| P25b Intent | Not started | **M10** |
| P27 Reference deployments and packaging | Not started | **M11** |
| P28 Capture replay and qualification | Not started | **M12** |

## Milestones

| Milestone | Reached after | Demonstrable outcome |
|---|---|---|
| M1 Walking skeleton | P1c | `infraweave serve`, built with `-cover`, runs as a subprocess under godog and every step drives it through `pkg/sdk`; every reference operation is classified served, served with divergence, superseded or missing by `make verify-parity`; depguard enforces decision 0004's shared rules over the whole tree |
| M2 Authorized API on PostgreSQL | P5 | An operator creates an organization with its first account, and an organization admin adds a second account whose data the first cannot see; an API key exchanges for a token that works in its own account only; spaces and roles decide every route; a feature in `features.disabled` answers `403 feature-disabled`; a stack is created, read, replaced with `PUT`, merged with `PATCH`, locked, searched and deleted, and survives a restart and a second `serve` replica |
| M3 First run | P9 | `runs_terraform.feature` and `runs_tofu.feature`: a tracked run on a private Docker worker fetches its source, plans, waits for `runConfirm`, applies through the managed state backend into the object store, masks a secret in its log and reaches `FINISHED` |
| M4 GitOps loop | P13 | Option 5's walkthrough holds end to end against Gitea: a push opens a proposed run, a plan policy warns, the check and the pull request comment appear, and an approval policy holds a tracked run until a review |
| M5 Deploy core | P14, P15, P16, P17b and P18b | Public worker pool and Kubernetes workers, cloud credentials through OIDC, schedules, drift detection, stack dependencies, the module and provider registries, notifications, webhooks, Slack, the event streams, the audit trail and the per-account metrics all pass their features |
| M6 Flows | P18d | A flow's Deploy trigger block reacts to a real run and a Trigger Run block starts another; a Go app built on `pkg/flows/appsdk` runs on a Flows agent; every expression in the flow is CEL |
| M7 Ecosystem | P19b, P21 and P22b | Every VCS provider, VCS agent pools, every vendor of report 20, blueprints and templates, the resources view, generic search, the console-support operations and scans pass their features |
| M8 Identity and clients | P24b | SAML and OIDC identity providers at the organization, SCIM, MFA and identity linking work, and each account's login policies decide who enters it; infraweave's provider passes schema parity and apply, empty plan, import, empty plan and destroy for every resource that is not superseded, Flows resources included; `infraweavectl` covers report 06 section 8 and its `flows` group covers `flowctl` |
| M9 Console | P24c | The console, embedded in `infraweave`, passes every Playwright journey against a real `infraweave all` with no CSP, other-origin or accessibility violation |
| M10 Intelligence | P25b | AI integrations, Infra Assistant, the hosted `/mcp` server and Intent work against recorded providers |
| M11 Release | P27 | Signed binaries, images, the Helm charts, the Compose file and the Terraform modules install infraweave; an upgrade from the previous release runs both versions against one database |
| M12 Qualified | P28 | Every capture of report 30 is replayed or recorded as unobtainable; report 26 has no row in `missing` or `partial` |

## Sequence

```mermaid
flowchart LR
  P0[P0 Reference] --> P1[P1 Served skeleton]
  P1 --> P1b[P1b Domain layout]
  P1b --> P1c[P1c Harness and SDK]
  P1b --> P1d[P1d Flows reference]
  P1c --> P2[P2 PostgreSQL]
  P2 --> P2c[P2c Queue, outbox and events]
  P2c --> P2b[P2b Objects and secrets]
  P2b --> P3[P3 Identity]
  P3 --> P3b[P3b Organizations and features]
  P3 --> P4[P4 Spaces and authz]
  P4 --> P5[P5 Stacks]
  P3b --> P5
  P5 --> P4b[P4b Roles and bindings]
  P3b --> P4b
  P5 --> P6[P6 Configuration and expressions]
  P5 --> P6b[P6b Sources]
  P6b --> P7[P7 Run machine]
  P6b --> P12[P12 VCS integrations]
  P7 --> P8[P8 Workers]
  P6 --> P8
  P8 --> P8b[P8b State backend]
  P6 --> P9[P9 First run]
  P8b --> P9
  P12 --> P9
  P9 --> P10[P10 Run lifecycle]
  P10 --> P11[P11 State management]
  P10 --> P12b[P12b VCS events and feedback]
  P12 --> P12b
  P12b --> P13[P13 Policies]
  P13 --> P13b[P13b Login and other policies]
  P4b --> P13b
  P13 --> P14[P14 Public worker pool and Kubernetes]
  P9 --> P15[P15 Cloud and OIDC]
  P10 --> P16[P16 Schedules and dependencies]
  P13 --> P17[P17 Module registry]
  P15 --> P17
  P17 --> P17b[P17b Provider registry]
  P13 --> P18[P18 Events, webhooks and streams]
  P18 --> P18b[P18b Audit and observability]
  P18 --> P18c[P18c Flows engine]
  P4b --> P18c
  P1d --> P18c
  P18c --> P18d[P18d Flows apps and agents]
  P21 --> P18d
  P12b --> P19b[P19b VCS agents and sources]
  P11 --> P20[P20 Vendors]
  P20 --> P20b[P20b Ansible and plugins]
  P16 --> P21[P21 Templates]
  P20b --> P22[P22 Resources and search]
  P22 --> P22b[P22b Scans]
  P14 --> P22b
  P13b --> P23[P23 Identity providers and SCIM]
  P23 --> P24[P24 Provider]
  P21 --> P24
  P22b --> P24
  P19b --> P24
  P18b --> P24
  P18d --> P24
  P17b --> P24
  P15 --> P24
  P25 --> P24
  P24 --> P24b[P24b infraweavectl]
  P24b --> P24c[P24c Console]
  P18d --> P24c
  P25b --> P24c
  P15 --> P25[P25 AI and MCP]
  P3b --> P25
  P25 --> P25b[P25b Intent]
  P14 --> P25b
  P24c --> P27[P27 Packaging]
  P25b --> P27
  P27 --> P28[P28 Qualification]
```

The chart shows the main edges; each phase's **Depends on** line is the full
list. Numbering does not serialize independent work: a phase starts as soon as
the phases it depends on are done.

**Critical path:** P0 → P1 → P1b → P1c → P2 → P2c → P2b → P3 → P4 → P5 → P6b →
P7 → P8 → P8b → P9 → P10 → P11 → P20 → P20b → P22 → P22b → P24 → P24b → P24c →
P27 → P28, the longest chain of **Depends on** edges (25 edges). Between P5
and P8, P6 is one phase shorter than P6b → P7, and between P6b and P9, P12 is
two phases shorter than P7 → P8 → P8b. From P10 to P24, the Flows chain
(P12b → P13 → P18 → P18c → P18d) ties with the vendor chain, so a slip in
either moves the release. The policy and identity chain
(P12b → P13 → P13b → P23), the registry chain (P12b → P13 → P17 → P17b) and
the audit chain (P12b → P13 → P18 → P18b) are each one phase shorter.

**Parallel lanes:**
- P2c comes before P2b, because object storage registers `blob.delete` and
  `blob.expire` on the queue and `secrets rotate` enqueues `secrets.reseal`.
  P2c also fixes the event envelope that P18 and the Flows engine share.
- P1d runs beside P1c once P1b lands; nothing waits on it before P18c.
- P3b runs beside P4. P4b follows P5, because stack role bindings bind a
  stack. P3b's authorization server is ready long before `/flows/mcp`
  (P18c, through P4b) and `/mcp` (P25) use it.
- P6 and P6b run side by side once P5 lands, and P7 follows P6b, because a
  run is created on its stack's tracked commit. P8 needs P6 and P7, because
  the run handover carries the resolved environment.
- P12 runs beside P7, P8 and P8b once P6b lands. P9 needs it, because the
  handover carries the run's source credential, and P12b needs it, because
  events arrive through integrations.
- P15 (cloud and OIDC) starts once P9 lands, and Intelligence (P25 → P25b)
  follows it.
- Once P10 lands, four lanes run independently:
  - state and vendors: P11 → P20 → P20b → P22 → P22b;
  - VCS and policy: P12b → P13, then P19b, P13b → P23, P14,
    P17 → P17b (with P15) and P18 → P18b, each from P13 or P12b as its
    **Depends on** line says;
  - events and Flows: P18 → P18c → P18d, once P4b has landed for Flows
    roles and P21 for the Deploy Template and Deploy Blueprint blocks;
  - scheduling and templates: P16 → P21, where P21 also needs P12b.
- Events (P18 → P18b) start once P13 lands, because notification policies
  route every event.
- P24 waits for every phase whose operations a provider resource calls; P24b
  follows it so the CLI and provider share fixtures, and the console (P24c)
  follows P24b because its journeys cover every surface, Flows and Intent
  included.

**Adjustments to report 26's provisional spine.** Report
[26 section 1.4](../research/reference/26-scope-and-tiers.md#14-provisional-phase-spine)
gives a provisional phase spine. Each change below follows a dependency the
research shows. Where option 5's
[domain status table](../research/architecture-options/5-modular-monolith.md#domain-status-by-phase)
assigns a domain to another phase, this plan's assignment holds.

| Change | Why |
|---|---|
| P1b and P1c inserted after P1 | Decision [0004](../research/decisions/0004-repository-architecture.md) moves today's packages into the domain layout and enables depguard; decision [0016](../research/decisions/0016-testing-and-acceptance.md) defines the harness, the test-name split and the parity outcomes. Doing both before persistence lands every later phase in its final package and inside a working test loop, as `CLAUDE.md`'s correctness-loop rule requires |
| P1d inserted after P1b | Decision [0018](../research/decisions/0018-flows-runtime.md)'s Flows reference needs only `specref` and the renamed served document; built straight after the layout moves, it holds the Flows clients' operations as a parity list from the start, and the weekly refresh reports upstream Flows changes long before P18c scaffolds from it |
| `pkg/sdk` lands in P1c | Decision [0001](../research/decisions/0001-rest-first-spacelift-surface.md) makes the SDK the base of every Go client, and decision [0016](../research/decisions/0016-testing-and-acceptance.md)'s steps drive the API through it, so every scenario from the first one also tests the SDK |
| P2 split into P2, P2c and P2b | P3 seals signing keys and idempotency bodies ([0006](../research/decisions/0006-authentication-and-tokens.md), [0005](../research/decisions/0005-persistence-postgresql.md)), so object storage and the keyring ([0013](../research/decisions/0013-secrets-at-rest.md)) come first, and they run their jobs on the queue |
| The work queue, cron, outbox and event envelope move from P7 to P2c | `blob.expire` and `blob.delete` ([0005](../research/decisions/0005-persistence-postgresql.md)), `authn.keys.rotate` and `authn.sessions.expire` ([0006](../research/decisions/0006-authentication-and-tokens.md)) and `idempotency.purge` are queue jobs and periodic tasks ([0011](../research/decisions/0011-persistent-work-queue.md)), needed long before runs. The one event model of [0015](../research/decisions/0015-events-notifications-webhooks-audit.md) and [0018](../research/decisions/0018-flows-runtime.md) is fixed with the outbox, so Deploy and Flows emit the same envelope. P7 keeps the run state machine and `run.schedule` |
| The object store emulators, the `fips-only` job and the scheduled cloud job land in P2b | Decision [0016](../research/decisions/0016-testing-and-acceptance.md) runs `objectstoretest` on RustFS, fake-gcs-server, Azurite and `fs` in every pull request and against the real services nightly, and runs the tests under `fips140=only`; each lands with the drivers and the crypto it checks ([0013](../research/decisions/0013-secrets-at-rest.md)) |
| Process logging, request IDs and the admin listener's health endpoints move from P18 to P2 | Every process mode reports `/readyz` against PostgreSQL from its first connection ([0017](../research/decisions/0017-observability-and-health.md), [0004](../research/decisions/0004-repository-architecture.md)); P18b keeps metrics, the per-account series and run tracing |
| P3 and P4 split into P3, P3b, P4 and P4b | Size: about 110 identity operations and 47 spaces and roles operations |
| The organization level, the feature catalogue, tiers, billing, the `features` operations of the operator API and the OAuth authorization server land in P3b; the operator API's document and its `access` operations land in P3 | Every operation of every served document carries `x-infraweave-feature` ([0021](../research/decisions/0021-capability-scope-and-supersession.md)), so the gate exists before the first domain beyond identity; entitlement and pooled limits belong to the organization, and `seats` counts the members and keys P3b creates across its accounts. The authorization server ([0006](../research/decisions/0006-authentication-and-tokens.md)) needs only P3's sessions and P3b's redirect allowlist, and it stays off the critical path there. The operator API is the only way to create an organization and to set its break-glass credential, so it ships with the break-glass login that needs it |
| Expressions land in P6 | Environment interpolation is the first surface of decision [0024](../research/decisions/0024-expression-language.md); `internal/platform/expr` and its endpoints exist before workflow files (P10), notification text (P18), templates (P21) and Flows (P18c) need them |
| Private pools move from P14 to P8; the public worker pool moves from P8 to P14 | Every acceptance run uses a private pool ([0016](../research/decisions/0016-testing-and-acceptance.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md)); the pool token CSR of [0008](../research/decisions/0008-run-execution-and-worker-protocol.md) is the private pool's path, and the public worker pool is an optional operator-run pool on the same protocol |
| P8b inserted before P9 | The first real run writes state through infraweave's `http` backend ([0016](../research/decisions/0016-testing-and-acceptance.md), [0010](../research/decisions/0010-state-backend.md)). P11 keeps import, rollback, external access, the signed URLs and Tofu workspaces. Its acceptance acts as a worker over `/worker/v1` and runs `terraform` and `tofu` itself, so it needs no runner before P9's |
| P6b inserted before P7; `runTrigger` moves from P9 to P7; `stackSyncCommit` and `stackSetCurrentCommit` move from P12 to P6b | A run is created on its stack's tracked commit, so the commit's source comes before the run machine: `vcs` begins with `Source` and the raw Git provider ([0012](../research/decisions/0012-vcs-integration-model.md)), served in acceptance by Gitea ([0016](../research/decisions/0016-testing-and-acceptance.md)). P7's queue and P8's claim then act on runs `runTrigger` created, and P9 adds their execution |
| The raw Git `Source` moves from P19 to P6b | The first tracked run needs a source with no webhook; decision [0016](../research/decisions/0016-testing-and-acceptance.md) serves it from Gitea |
| P12 moves before P9, after P6b; its webhooks, processing and feedback become P12b after P10; P19's providers join both, and P19 folds into them | Every provider is a peer ([0012](../research/decisions/0012-vcs-integration-model.md)): GitHub, GitLab, Bitbucket Cloud, Bitbucket Data Center and Azure DevOps each gain every capability in the same phase. Integrations need only stacks and P6b's sources, and a private repository needs an integration's credential before its first run, so P12 runs beside P7 to P8b. Webhooks create proposed runs, which P10 completes, so events and feedback wait for it. P19b keeps its name |
| Login policies and the `LOGIN_POLICIES` scheme move from P4 to P13b | They evaluate through decision [0009](../research/decisions/0009-policy-engine-opa.md)'s engine ([0007](../research/decisions/0007-authorization-spaces-and-roles.md)) |
| Flows moves from the end of the spine to P18c and P18d | Flows is built on Deploy's events from the start ([0018](../research/decisions/0018-flows-runtime.md)): the engine needs the fan-out of P18 and the queue of P2c, nothing later, and its clients join `infraweavectl` and the provider in P24 and P24b |
| P24c added for the console | Decision [0023](../research/decisions/0023-console.md) builds a console over the public API only, so it follows the last client phase and covers every surface |
| P12, P13, P17, P20, P22, P24 and P25 each split in two; P18 splits in four | Size: each part is a few pull requests. P18's parts are events, audit and observability, the Flows engine, and Flows apps and agents |
| P28 added | Decision [0016](../research/decisions/0016-testing-and-acceptance.md) replays captures as fixtures, and report [30](../research/reference/30-open-questions.md) lists 269 of them; qualification closes the loop once an authorized account is available |
| Report 26's provisional Terraform phases remapped | The reference rows `spacelift_worker_pool`, `spacelift_worker_pools` and `spacelift_worker_pool_recycle` to P8; `spacelift_ips` to P14; `spacelift_current_stack` and `spacelift_current_space` to P9, where the in-run token first exists; `spacelift_idp_group_mapping` to P3b; the Billing tag to P3b, with `usage` in P18b; the OAuth tag to P18, with the Slack redirect; the Worker Pools tag to P8. [capabilities.json](../data/capabilities.json) is the assignment |

## Adding a domain

The phase that creates a domain wires it into the
[declared graph](repo-layout.md#declared-graph) in the same pull request:

- `.golangci.yml` gains its `graph-<domain>` rule. Its entries in
  `constructors` and `shared-below-domains`, and in `core-not-adapters` for a
  hexagon, are there from P1b, and `agent-isolation` needs none; a domain
  outside the 19 adds those entries too.
- `internal/app/domains.go` constructs it at its place in the construction
  order, its migration stream registers at the same place, and
  `internal/app/router.go` embeds its `<Domain>Deploy` alias.
- Each import edge whose other end already exists lands with it. An edge to
  a domain that arrives later lands with that later phase.
- Its tables hold other domains' IDs as plain columns, never foreign keys.
  Each such column comes with a hold on the owner's `api/` where the row is
  written, a guard registered with the owner for its deletes and moves, its
  purge of a deleted account, which `internal/app` subscribes to
  `account.deleted`, and its declaration among the held columns the nightly
  orphan count reads
  ([0004](../research/decisions/0004-repository-architecture.md#repository-shape-a-modular-monolith)).
  Its store tests migrate the platform stream and its own, and fake the
  other domains' `api/`; the hold and guard race is tested in
  `internal/app`.
- A back edge, a call to a domain constructed later, is bound in
  `internal/app` by the first phase that makes the call.

`TestUnitDepguardFixtures` fails on a domain directory without its rule
entries, and `TestAccSchemaDependencies` on a key or view between two
domains.

## P0: Specification reference

Decisions: [0001](../research/decisions/0001-rest-first-spacelift-surface.md), [0002](../research/decisions/0002-reference-sources-and-licensing.md).

**Depends on:** nothing.

The Spacelift reference is the parity oracle every later phase is measured
against: a REST projection of every GraphQL root field, enriched with the Bruno
collection and the Terraform provider, with a coverage report that accounts for
every field ([#5](https://github.com/weaveplatform/infraweave/issues/5)).

**Tasks**
1. `internal/devtools/specref` and `tools/specref`: `fetch`, `generate` and `check`
   over the snapshots in `api/reference/spacelift/` (`introspection.json`,
   `bruno.json`, `terraform-schema.json`, `terraform-operations.json`,
   `sources.lock.yaml`), producing `spacelift.openapi.yaml`,
   `rest-mapping.yaml` and `coverage.md`. Tests: the golden spec in
   `internal/devtools/specref/testdata/golden/`.
2. `make reference-fetch`, `make reference` and `make verify-reference`, and
   the weekly `openapi-spec-update.yml` refresh.
3. `specref inventory`, which generates the register
   [`graphql-inventory.json`](../data/graphql-inventory.json) and report
   [29](../research/reference/29-data-model-inventory.md) that summarises it,
   and their drift check in `make verify-reference`.

**Exit criteria**
- 725 reference operations in 53 tags plus 8 untagged, 622 routed root fields
  and 43 excluded, every exclusion with its reason in `coverage.md`.
- `make verify-reference` fails on any drift of the reference, of report 29
  or of the GraphQL inventory register.
- `make gate` passes at 95% in total and 90% per package.

**Outcome: done.** The research and decision layer built on it is reports 01
to 31 and decisions 0001 to 0024.

## P1: Served API skeleton

Decisions: [0001](../research/decisions/0001-rest-first-spacelift-surface.md), [0003](../research/decisions/0003-api-conventions.md).

**Depends on:** P0.

infraweave serves its own REST API: fragments scaffolded from the reference,
bundled into `api/api.openapi.yaml` and generated into a strict chi server
in which every operation answers `501` until a family implements it (epic
[#8](https://github.com/weaveplatform/infraweave/issues/8)). Its packages
sit where P1b places them; the paths below are those.

**Tasks**
1. Done: [#9](https://github.com/weaveplatform/infraweave/issues/9) `internal/devtools/openapibundle` and `tools/openapi-bundle` bundle
   `api/src/api/*.yaml`; [#12](https://github.com/weaveplatform/infraweave/issues/12) `specref scaffold` and `make scaffold`
   seed `api/src/api/paths-stacks.yaml` (69 Stacks and 3 Runs
   operations); [#13](https://github.com/weaveplatform/infraweave/issues/13) `make generate` writes the strict server and
   `UnimplementedStrict` into `internal/platform/gen/api` and the client
   into `pkg/client/api`, with `make verify-generate`; [#10](https://github.com/weaveplatform/infraweave/issues/10) decision
   0003 and `api/src/api/components-common.yaml`.
2. Done: [#14](https://github.com/weaveplatform/infraweave/issues/14) `cmd/infraweave` with `serve` and `version`;
   `internal/app` and `internal/platform/httpkit` build the chi router with request ID and recovery,
   mounts the strict handler under `/api/v1`, turns `404` and `405` into
   problem details and serves `GET /livez` on the admin listener
   (`server.admin_listen`, `:9090`), never on the API listener
   ([0017](../research/decisions/0017-observability-and-health.md#health)).
   Strict YAML config with flag over environment over file. Tests:
   unit tests for `/livez`, a `501` stub, `404`, `405`
   and recovery; config precedence and unknown-key
   rejection; `api_served.feature`.
3. Done: [#15](https://github.com/weaveplatform/infraweave/issues/15) `internal/platform/httpkit/middleware/ratelimit`,
   `internal/platform/httpkit/middleware/idempotency` and
   `internal/platform/httpkit/page`: token buckets per caller and cost class
   from `x-infraweave-cost`, `X-RateLimit-*` headers and `429` with
   `Retry-After`; `Idempotency-Key` replay, `409` in flight and `422` reuse;
   the `api-disabled` switch; opaque versioned cursors with
   `invalid-cursor`. In-memory stores behind interfaces until P2; settings
   under `server.rate_limit` and `server.idempotency`. Tests: a
   `-race` test of concurrent reservations, limiter refill with an injected
   clock, cursor tamper cases, `api_served.feature` scenarios for `429`,
   replay and the disabled caller. Records DV-003 to DV-007.
4. Done: [#11](https://github.com/weaveplatform/infraweave/issues/11) `specref parity` in `internal/devtools/specref` with `make parity` and
   `make parity-baseline`: present, missing, moved and infraweave-only
   operations by tag, against `api/parity-baseline.deploy.json`; the `api/` test
   fails on a regression and on an entry that is now present.

**Exit criteria**
- The epic's acceptance holds on `main`: `make generate` reproduces the
  committed bundle, server, client and stubs byte for byte; a Stacks route
  answers a `501` problem, `/livez` answers `200` on the admin listener and an unknown path answers a
  `404` problem; a retried `POST` acts once; an exhausted budget answers
  `429` with `Retry-After`.
- The parity report shows the 72 scaffolded operations present and every
  other reference operation in the baseline.
- `make gate` passes at 95% in total and 90% per package.

## P1b: Domain layout and depguard

Decisions: [0004](../research/decisions/0004-repository-architecture.md), [0016](../research/decisions/0016-testing-and-acceptance.md).

**Depends on:** P1.

Today's packages move into option 5's tree before any domain exists, so no
later phase pays for a move, and depguard starts enforcing the boundaries
with a clean run over the whole tree.

**Tasks**
1. Move per option 5's [package mapping](../research/architecture-options/5-modular-monolith.md#package-mapping)
   and the [repo layout](repo-layout.md#where-todays-packages-go):
   `internal/buildinfo` to `internal/platform/buildinfo`,
   `internal/server/problem` to `internal/platform/httpkit/problem`,
   `internal/server/{ratelimit,idempotency,pagination}` to
   `internal/platform/httpkit/{middleware,page}`, `internal/specref`,
   `internal/openapibundle`, `internal/stubgen`, `internal/docscheck` and
   `internal/dataview` to `internal/devtools/...`. The rest of
   `internal/server` splits four ways: the listener lifecycle and the
   two-listener drain to `internal/platform/httpkit/serve`, which the agent
   process modes share from P8; `GET /livez` to `internal/platform/observe`;
   recovery and the `404` and `405` problem handlers to
   `internal/platform/httpkit/middleware`; routing and the operation table
   the guards read (`operations.go`) to `internal/app`. Update the `Makefile` (`LDFLAGS`, `generate`,
   `verify-generate`, `docs-check`), `.testcoverage.yml` and the `tools/`
   imports.
2. Rename the served document from `deploy` to `api`
   ([#73](https://github.com/weaveplatform/infraweave/issues/73)), since it
   holds Deploy and Flows alike ([0018](../research/decisions/0018-flows-runtime.md)):
   `api/src/deploy` becomes `api/src/api`, `api/oapi-server-deploy.yaml`
   and `api/oapi-client-deploy.yaml` become `api/oapi-server-api.yaml` and
   `api/oapi-client-api.yaml`, the bundle becomes `api/api.openapi.yaml`,
   `pkg/client/deploy` becomes `pkg/client/api`, `api/parity-baseline.json`
   becomes `api/parity-baseline.deploy.json`, `TestUnitDeployParity` and
   `TestUnitDeployParityChecks` become `TestUnitAPIParity` and
   `TestUnitAPIParityChecks`, and `api/deploy_test.go` becomes
   `api/api_test.go`, its `TestUnitDeployBundleCurrent`,
   `TestUnitDeployConventionComponents` and `TestUnitDeployProblemTypes`
   becoming `TestUnitAPIBundleCurrent`, `TestUnitAPIConventionComponents`
   and `TestUnitAPIProblemTypes`. The same pull request updates the
   documents that describe the code as it stands and so name the old files:
   the `README.md` sections on the served API and the `make` targets, report
   26's state computation and versions, report 01's scaffold row, this
   plan's REST bullet, decision 0004's "Today it holds" sentence, decision
   0001's implemented checks, decision 0022's problem-type check and
   `docs/data/README.md`. Regenerate the strict server into
   `internal/platform/gen/api` (package `api`) through the renamed server
   configuration's output; stubgen reads the new path.
   `internal/app/router.go` composes an `apiServer` struct that
   embeds each domain's partial and `UnimplementedStrict` one level deeper,
   with `var _ api.StrictServerInterface = apiServer{}`. An embedded
   field takes its type's name, so each domain's `assembly/` package
   re-exports its partial under its domain's name (`StacksAPI`); no
   domain exists yet, so the struct holds only the stubs.
3. Create `internal/kernel` with `page.go` (decision 0003's cursor and page,
   which `httpkit/page` encodes); `ids.go` lands with P2 and `principal.go`
   with P3. `internal/app/config.go` becomes the package `internal/app/config`
   with its keys, flags and strictness unchanged, and `internal/app/modes.go`
   holds `app.Serve`; `platform.go` lands with P2 and `domains.go` with the
   first domain. The server process modes are cobra files in
   `cmd/infraweave` (`serve`, `version`; the others land with their phases).
   From P8, the agent process modes' commands live in `internal/agent/cli`
   and register through the one `cmd/infraweave/agent.go`, which imports no
   `internal/app` ([repo layout](repo-layout.md#annotated-tree)).
4. `.golangci.yml`: enable depguard with the shared rules of option 5's
   [tests and enforcement](../research/architecture-options/5-modular-monolith.md#tests-and-enforcement)
   and the [repo layout](repo-layout.md#depguard-rules): `api-packages`,
   `constructors`, `core-not-adapters`, `shared-below-domains`,
   `agent-isolation`, `devtools` and `public-packages`, the `pkg/` rule of
   decision 0004 that nothing under `pkg/` imports `internal/`.
   `agent-isolation` is an allow list: outside its own tree, `internal/agent`
   imports only `internal/kernel`, `internal/iac`, `pkg/flows/app/v1` and the
   platform packages `buildinfo`, `observe`, `rego`, `httpkit/client`,
   `httpkit/serve`, `seal` and `expr`. No domain exists yet, so no
   `graph-<domain>` rule lands here; each domain's phase adds its own
   ([adding a domain](#adding-a-domain)). depguard rejects a rule with an
   empty list, so `constructors` and `shared-below-domains` name all 19
   domains and `core-not-adapters` all 8 hexagons from the start. Add
   forbidigo for `time.Sleep` in `_test.go` files
   ([0016](../research/decisions/0016-testing-and-acceptance.md)), and lint
   the whole tree, tests, `cmd/` and `tools/` included.
5. `make lint-fixtures` lints `test/depguard`, a module of its own, with the
   repository's `.golangci.yml`: a domain that imports another domain's
   `internal/` and `assembly/` packages, an `api/` package that imports a
   sibling of an allowed package (`internal/platform/outboxutil`), a
   hexagon's core that imports its adapters, a platform package that
   imports a domain, an agent package that imports `internal/platform/sql`
   and `pkg/sdk`, a domain that imports `internal/devtools`, a `pkg/`
   package that imports `internal/`, and a test that calls `time.Sleep`.
   It fails unless the findings are exactly `test/depguard/expected.txt`,
   and the blocking lint job runs it. `internal/app/depguard_test.go`
   (`TestUnitDepguardFixtures`): the seven shared rules exist, every allow
   entry in `.golangci.yml` ends in `$` or `/` and every deny entry is bare
   ([repo layout](repo-layout.md#depguard-rules)), and every domain
   directory under `internal/` has its graph rule and its entries in
   `constructors` and `shared-below-domains`, and in `core-not-adapters` for
   a hexagon.

**Exit criteria**
- `GOWORK=off go list ./...` shows no package under `internal/server`, and
  every package it lists sits where the [repo layout](repo-layout.md) places
  it.
- `make verify-generate` checks `internal/platform/gen/api` and
  `pkg/client/api`, and no file under `api/`, `internal/` or `pkg/` names
  `deploy.openapi.yaml`; the parity report is unchanged.
- `README.md`, reports 01 and 26, decisions 0001, 0004 and 0022, this plan
  and `docs/data/README.md` name `api/api.openapi.yaml`, `api/src/api`,
  `pkg/client/api`, `api/parity-baseline.deploy.json` and the `TestUnitAPI…`
  tests wherever they describe the code as it stands.
- depguard runs clean over the whole tree, and `make lint-fixtures` reports
  each fixture violation and nothing else.
- `make gate` passes at 95% in total and 90% per package.

## P1c: Test harness, SDK and parity tooling

Decisions: [0016](../research/decisions/0016-testing-and-acceptance.md), [0001](../research/decisions/0001-rest-first-spacelift-surface.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0022](../research/decisions/0022-api-errors-and-update-semantics.md).

**Depends on:** P1b.

The acceptance harness, the SDK it drives the API through and the parity
outcomes of decision 0016 exist before the first domain, so every later
operation is proved by the same rule: served, not `501`, and named by
passing success, denial and failure scenarios.

**Tasks**
1. Test names: rename every top-level test to `TestUnit...` or `TestAcc...`;
   `api/testnames_test.go` (`TestUnitTestNames`) fails on any other name.
   `make test` selects `-run '^TestUnit'` across all packages and
   `make accept` selects `-run '^TestAcc'`, so store and domain suites beside
   the code run in the acceptance job.
2. `test/acceptance`: `TestAccFeatures` runs godog with `Strict: true` over
   `features/<area>_<topic>.feature`; the harness builds `cmd/infraweave`
   once with `-cover -covermode=atomic`, runs `infraweave all` (serve plus
   scheduler) as a subprocess per scenario on a free port with `GOCOVERDIR`
   set to `cover/accept`, starts `@postgres` (PostgreSQL 18),
   `@objectstore` (RustFS) and `@oidc` dependencies lazily through
   testcontainers-go, and writes logs to
   `test/acceptance/out/<scenario>/` on failure. `test/acceptance/images.go`
   pins every image by digest. `acceptance.Eventually` is the only wait.
   Feature: `acceptance_harness.feature` (skip without Docker, failure with
   `INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`, evidence on failure).
3. `internal/devtools/specref`: `specref parity` decides served, served with
   divergence (`x-infraweave-divergence`), superseded (report 26 section 7)
   and missing for each of the 731 Deploy and 50 Flows reference
   operations, failing on an unrecorded divergence or a served superseded
   operation; `--states` computes report 26's state per tag and per
   Terraform row from `cover/accept/cucumber.json` and the
   `@op:<operationId>`, `@case:` and `@provider:` tags; `--check` fails when
   a record claims more. `make verify-parity`. The P1 baseline
   file becomes the computed `missing` list. A reference name that decision
   0001's rename table renames is matched to its served name, so a renamed
   operation counts as served with divergence, never as missing. The
   operations of the `Organization` tag are infraweave-only, outside the
   parity surface.
4. `api/api_test.go`: `TestUnitFeaturePartitions` (every scenario tag
   maps to one CI partition), `TestUnitServedDeprecations`,
   `TestUnitSupersededEnums`, `TestUnitSupersededFields` and
   `TestUnitAPIProblemTypes`; the problem
   slug registry in `internal/platform/httpkit/problem` gains every slug of
   decision 0022 (`TestUnitProblemRegistry`). `TestUnitServedNames`, the
   rename-table test, fails when an operationId, schema, property, enum
   value, header, environment variable, policy input key or description of
   any served bundle contains "spacelift" in any case, and when a row of decision
   0001's rename table is served under its reference name rather than its
   infraweave name. Each later bundle (worker, registry, operator)
   joins the test in the phase that adds it. Records DV-114.
5. CI: split the acceptance job into `accept-core`, `accept-runs` and
   `accept-integrations`, add `parity` and a scheduled `nightly`, and list
   every job it adds in the `Quality gate` job's `needs`.
6. `test/fixtures/captures/` layout with `TestUnitCaptureRedaction` and an
   empty `TestAccCaptures` replayer, ready for P28.
7. `pkg/sdk` over `pkg/client/api` ([0001](../research/decisions/0001-rest-first-spacelift-surface.md)): one client for every
   served document, `Idempotency-Key` on every `POST` and `PATCH` reused
   across its retries, retries on `429` and on `503` with `Retry-After`,
   iterators that follow `nextCursor`, and a typed error per problem slug of
   decision 0022. A ready token is used as is; the API key exchange lands in
   P3. Every acceptance step calls the API through it. Tests: unit tests
   against a chi test server, and a test that fails when the SDK's slugs
   differ from the problem registry.

**Exit criteria**
- `api_served.feature`, `reference_spec.feature` and `api_bundle.feature`
  run against the subprocess binary, and its coverage reaches the merged
  profile.
- `make verify-parity` reports the 72 scaffolded operations served and every
  other reference operation superseded or missing, and a deliberately
  removed operation fails it.
- No acceptance step calls `pkg/client` directly; `pkg/sdk` meets the
  per-package coverage bar.
- `TestUnitServedNames` passes on `api/api.openapi.yaml`, and a fixture
  bundle with a property named `spaceliftAwsAccountId` and a description
  that names Spacelift fails it.
- `make gate` passes at 95% in total and 90% per package; CI fails a run in
  which any scenario was skipped.

## P1d: Flows specification reference

Decisions: [0018](../research/decisions/0018-flows-runtime.md), [0001](../research/decisions/0001-rest-first-spacelift-surface.md); report [02](../research/reference/02-graphql-to-rest-mapping.md#flows-rpc-to-rest)'s Flows: RPC to REST.

**Depends on:** P1b, for the package moves and the `deploy` to `api` rename of
[#73](https://github.com/weaveplatform/infraweave/issues/73).

The Flows reference exists long before any Flows operation is served, so
the Flows families are scaffolded from it when P18c starts, and an upstream
change to `flowctl` or the Flows provider shows up in the weekly refresh
rather than in the middle of the Flows phases. It needs only `specref` and
the renamed served document, not the acceptance harness, so it runs beside
P1c.

**Tasks**
1. The product seam in `internal/devtools/specref`: one reference per
   Spacelift product, each with its own fetch, sources, build, entries in
   its own `sources.lock.yaml`, document title and report text. The Deploy
   reference, `api/reference/spacelift/`, regenerates byte for byte, and
   `specref scaffold` reads either product's tags, so
   `make verify-reference` and the golden files under
   `internal/devtools/specref/testdata/golden/` show no diff.
2. The Flows reference, `api/reference/spacelift-flows/`, beside
   `api/reference/spacelift/`
   ([02](../research/reference/02-graphql-to-rest-mapping.md#flows-rpc-to-rest)):
   `specref fetch` pins the `terraform-provider-flows` Go structs (read
   with `go/ast`) and `flowctl`'s `src/api.ts` (path literals and interface
   types) in `sources.lock.yaml`, and validates the 46 published templates
   and a live registry manifest against the overlay `schemas.yaml`
   (`FlowDefinition`, `RegistryManifest`, `AppVersions`); `specref
   inventory` writes the `@slflows/sdk` identifiers to the register
   `docs/data/flows-sdk-inventory.json`, which feeds no REST operation. The
   hand-written `rest-mapping.yaml` maps every RPC path by the verb rules
   and overrides, or excludes it with a reason, and `specref generate`
   writes `flows.openapi.yaml`, each operation marked `x-rpc`, a list of
   `{path, client}` with `client` `provider` or `flowctl`
   (`/cli/flows/export` and `/provider/flows/export_definition` share one
   operation), and `coverage.md`, failing on an unmapped or stale RPC path.
3. Parity: `TestUnitFlowsParity` compares the Flows reference with the
   served `api/api.openapi.yaml`. `api/parity-baseline.flows.json` accepts
   every Flows reference operation as missing, since none is served before
   P18c, which shrinks it; the five test-scenario operations stay accepted
   until PD-16 is decided. `make parity-baseline` rewrites both baselines.
4. Product-scoped capabilities: each record of
   [capabilities.json](../data/capabilities.json) gains `product` (`deploy`
   or `flows`), the `Flows `-prefixed tags join as `flows` records, and
   docscheck checks each reference's tags against its own product's records.
5. CI: `openapi-spec-update.yml` also moves the `terraform-provider-flows`
   commit, the `flowctl` version and `@slflows/sdk`; `make reference-fetch`,
   `make reference`, `make reference-inventory` and `make verify-reference`
   cover both products; and the `Quality gate` job lists `generated` in its
   `needs`, so drift in generated code blocks a merge.

**Exit criteria**
- `make verify-reference` regenerates the Deploy reference byte for byte.
- Every `/provider/...` and `/cli/...` RPC path of the pinned sources is
  mapped in `rest-mapping.yaml` or excluded with its reason.
- The 46 published templates validate against `schemas.yaml`.
- `TestUnitFlowsParity` passes against `api/parity-baseline.flows.json`.
- docscheck fails on a Flows reference tag with no `flows` record, and on a
  Deploy reference tag with no `deploy` record.
- `make gate` passes at 95% in total and 90% per package.

## P2: PostgreSQL, migrations and operability

Decisions: [0005](../research/decisions/0005-persistence-postgresql.md), [0017](../research/decisions/0017-observability-and-health.md), [0020](../research/decisions/0020-reference-deployments-and-packaging.md), [0003](../research/decisions/0003-api-conventions.md).

**Depends on:** P1c.

PostgreSQL becomes the only store, with the migration runner every domain
uses, and the binary gains the process modes, logs and health endpoints an operator
needs from its first database connection.

**Tasks**
1. `internal/platform/sql`: `pgxpool` from `database.url`, the optional
   `database.read_url`, statement and idle timeouts; `internal/platform/tx`
   with `tx.Do`, retries on `40001` and `40P01`, and the lock order.
   `internal/platform/sql/notify`: one `LISTEN` connection per replica with
   reconnect. `internal/kernel/ids.go`: decision 0005's ULIDs, monotonic
   within a millisecond, in `uuid` columns and as 26-character text on the
   wire, and slugs. Tests: `internal/platform/sql/sqltest` gives each case a
   database from the `iw_template` template; `TestAccTxRetry`,
   `TestAccNotifyReconnect`.
2. `database.iam_auth`
   ([0005](../research/decisions/0005-persistence-postgresql.md)): `aws`
   signs an RDS IAM token, `azure` takes an azidentity token for scope
   `https://ossrdbms-aad.database.windows.net/.default`, and `gcp` takes an
   Application Default Credentials token with the `sqlservice.login`
   scope. Each fetches a token for every new connection in pgx's
   `BeforeConnect` and uses it as the password; there is no Cloud SQL
   connector, and a URL without TLS fails startup. Tests:
   `TestUnitIAMAuth` with a fake credential source per provider, a token
   refreshed between two connections, and the TLS refusal.
3. `internal/platform/sql/migrate`: embedded streams of
   `NNNNN_snake_case.sql`, `schema_migrations` and `schema_state`, the
   advisory lock, `-- infraweave:no-transaction` and
   `-- infraweave:compatible-from`, refusal on checksum mismatch, a too-old
   binary or a skipped major. `internal/app/platform.go` opens the pool and
   migrates every stream in the
   [declared graph](repo-layout.md#declared-graph)'s construction order.
   `infraweave migrate up` and `infraweave migrate status`;
   `database.migrate: auto | check`. Tests: `TestUnitMigrationNames`,
   `TestAccMigrateConcurrent` (two replicas apply once),
   `TestAccMigrateRefusals`.
   `TestAccSchemaDependencies`
   ([0004](../research/decisions/0004-repository-architecture.md#verification))
   applies each stream in registration order to a fresh database and reads
   the `graph-<domain>` rules from `.golangci.yml`. It fails when a foreign
   key in `pg_constraint`, or a view's dependency in `pg_depend`, goes from
   one domain's relation to another's or from the platform to a domain, when
   the graph has a cycle, when a stream registers before the platform's, and
   when a domain's stream does not apply on the platform stream alone.
   Fixture streams and rules prove each failure.
4. Move decision 0003's stores to PostgreSQL in
   `internal/platform/httpkit/middleware`: `idempotency_keys` (body sealed
   from P2b; plaintext behind the store interface until then),
   `rate_limit_buckets` (`UNLOGGED`, one upsert per request) and
   `api_disabled_callers` with cache invalidation on `infraweave_invalidate`.
   `serve` requires `database.url` and keeps all three there. Tests: the store
   contracts against PostgreSQL, and `api_idempotency_replicas.feature`,
   `api_rate_limit_replicas.feature` and `api_disabled_callers.feature`
   against two `serve` replicas on one database.
5. `internal/platform/observe`: `log/slog` JSON to stdout with the standard
   fields, `log.level` re-read on `SIGHUP`, the redacting handler and
   rate-limited warnings; `httpkit/middleware`: `X-Request-Id` validation
   and echo, and the access log on the API listener; `/readyz` and
   `/startupz` beside P1's `/livez` on the admin listener, from a check
   registry each process mode adds to (P2's: the configuration and the
   schema at startup, PostgreSQL for readiness); decision 0017's shutdown
   order, with `server.shutdown.ready_delay`. `infraweave scheduler` serves
   only the admin listener and `infraweave all` both, with no consumers
   yet; the acceptance harness runs `infraweave all`. Tests:
   `TestUnitRedactHandler`, `TestUnitRequestID`, `TestUnitServerListeners`,
   the registry with fake checks, the `SIGHUP` reload, and
   `observability_health.feature` (readiness fails while the scenario's own
   PostgreSQL container is paused and recovers when it resumes; the
   shutdown sequence with a request in flight; the log fields; the
   `scheduler` listeners). Records DV-009 and DV-062.
6. CI: `postgres-next` runs the `sqltest` suites, `queuetest` once P2c lands and
   `accept-core` on the next PostgreSQL major, its beta or release
   candidate, without blocking ([0016](../research/decisions/0016-testing-and-acceptance.md)).
7. Problem bodies carry `instance` as `urn:infraweave:request:<id>`
   ([0022](../research/decisions/0022-api-errors-and-update-semantics.md)),
   read from the response's `X-Request-Id`, and so do the spec's problem
   examples. `sql.Unavailable` marks a connection failure, a dropped or
   timed-out connection and a shutting-down server (`57P01`–`57P03`, class
   `08`) with `kernel.ErrUnavailable`, which the problem writer answers with
   `503 service-unavailable` and `Retry-After: 2`; any other failure stays
   `500`. Tests: the classifier against pgconn errors and SQLSTATEs, the
   writer's `instance`, and `observability_health.feature` (a `POST` answers
   `503` while the scenario's PostgreSQL is stopped and succeeds once it
   starts; `pkg/sdk`'s `DefaultRetry` rides out the outage).

**Exit criteria**
- Store suites run on PostgreSQL 18, the floor, in `accept-core`, and
  `postgres-next` reports them on the next major.
- A second `serve` replica shares idempotency and rate-limit state; a
  replay through either replica returns the stored response.
- `infraweave all` refuses to start on a database migrated by a newer
  incompatible binary and says why.
- `make gate` passes at 95% in total and 90% per package.

## P2b: Object storage and secrets at rest

Decisions: [0005](../research/decisions/0005-persistence-postgresql.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0011](../research/decisions/0011-persistent-work-queue.md), [0016](../research/decisions/0016-testing-and-acceptance.md).

**Depends on:** P2, P2c.

Object storage and envelope encryption are platform services every later
domain writes through: state, logs, artifacts, archives and every sealed
column. The FIPS 140-3 job and the object store emulators join CI with
them, so no later phase can add an algorithm or a driver call that the
checks have not seen.

**Tasks**
1. `internal/platform/objectstore`: the `Store` port (`Put`, `Get`, `Head`,
   `Delete`, `SignURL`) with the `s3`, `gcs`, `azureblob` and `fs` drivers,
   the logical stores and prefixes of decision 0005, `GET` and `PUT
   /blobs/{token}` for `fs`. `blob_objects` with `pending`, `live` and
   `deleting`, the `blob.delete` job and the `blob.expire` periodic task on
   P2c's queue and ticker. Tests:
   `internal/platform/objectstore/objectstoretest`, run as `TestAcc` suites
   for `s3` on RustFS, `gcs` on fake-gcs-server and `azureblob` on Azurite,
   and as a `TestUnit` suite for `fs`. Records DV-010 and DV-011.
2. `internal/platform/seal`, which holds the formats and no keys: the
   column format, the object stream (`IWSB`, 64 KiB segments) and the
   pool envelope, each with a known-answer vector. `internal/platform/crypt`,
   which holds the keys: the keyring in `encryption_keys` with `active`,
   `retiring` and `retired` keys, loaded before any listener binds; `Seal`,
   `Open` and `DeriveKey`; `crypt.Value` that renders `[redacted]`;
   `SealObject` and `OpenObject` under per-object content keys;
   `crypt.Registry` of sealed columns. `internal/platform/crypt/kek`: the
   `Provider` interface and the `local` provider, which `serve`, `scheduler`
   and `all` require through `secrets.kek.provider`. Tests: round trips,
   wrong key and AAD, truncated and reordered segments, a 1 GiB stream in
   bounded memory, replicas racing to create the first key, a wrong KEK,
   `TestAccSealedColumnRegistry`.
3. `internal/platform/crypt/kek`: the `awskms`, `gcpkms`, `azurekv` and
   `vault` providers. Tests: `vault` and `awskms` (moto) as `TestAcc`
   suites, recorded fixtures for the other two.
4. `cmd/infraweave` `secrets generate-kek`, `secrets rewrap` and
   `secrets rotate`, which enqueues `secrets.reseal` on the `maintenance`
   queue; the daily `secrets.rotate` periodic task under
   `secrets.rotation_interval`; replicas reload the keyring on
   `infraweave_invalidate`; the idempotency store seals `body_sealed`.
   Feature: `secrets_key_rotation.feature` (`@vault`: rotate with two
   replicas, rewrap from `local` to Vault). Records DV-048 to DV-050.
5. CI ([0016](../research/decisions/0016-testing-and-acceptance.md)): the required `fips-only` job runs `make test` and every
   acceptance partition with the binary built with `GOFIPS140=certified`
   and every process run with `GODEBUG=fips140=only`, outside the merged
   coverage; the scheduled `objectstore-cloud` job runs `objectstoretest`
   against Amazon S3, Google Cloud Storage and Azure Blob Storage through
   OIDC federation, in buckets with a one-day lifecycle rule, and opens or
   updates an issue when it fails.

**Exit criteria**
- `objectstoretest` passes on RustFS, fake-gcs-server, Azurite and `fs`; a
  pending object older than 24 hours disappears with its row.
- `fips-only` passes and is in the `needs` of the `Quality gate` job; a test
  that calls MD5 or SHA-1 fails it.
- A sealed value copied to another row fails to open; a rotation leaves every
  value readable and deletes the old key.
- `make gate` passes at 95% in total and 90% per package.

## P2c: Work queue, cron, outbox and the event model

Decisions: [0011](../research/decisions/0011-persistent-work-queue.md), [0015](../research/decisions/0015-events-notifications-webhooks-audit.md), [0005](../research/decisions/0005-persistence-postgresql.md).

**Depends on:** P2.

Background work, periodic tasks and the transactional outbox run on
PostgreSQL in the `scheduler` process mode, so that storage, identity and every later
domain can enqueue work in the transaction that asks for it. The event
envelope that Deploy and Flows share is fixed here, with the outbox that
writes it.

**Tasks**
1. `internal/platform/queue`: the `jobs` table and indexes, `EnqueueTx` and
   `Enqueue` with `pg_notify('infraweave_jobs', ...)`, `Coalesce` and `Ignore`
   duplicates, ordering keys, leases with `generation` fencing,
   `CompleteTx`, `Snooze`, `Permanent`, backoff with full jitter, the
   `dead` state, pruning; the seven logical queues with per-replica worker
   counts. Tests: `internal/platform/queue/queuetest` (`TestAccQueue...`
   for every case decision 0011 lists).
2. `internal/platform/cron`: the five-field grammar, time zones, next-fire
   computation and the ticker that claims due rows of `periodic_tasks` with
   `FOR UPDATE SKIP LOCKED`. Tests: table tests across daylight-saving
   changes in `Europe/London`, `America/New_York` and
   `Australia/Lord_Howe`; `FuzzParse`.
3. `internal/platform/outbox`: decision 0015's envelope (`id`, `kind`, `account`, subject, `occurred_at`,
   `document`) and the kind registry, each kind with a JSON Schema
   (`TestUnitEventSchemas`), which Flows events use unchanged from P18c;
   `outbox.Append` writes an `events` row and an `events.fanout` job in the
   caller's transaction and notifies `infraweave_events`;
   `Subscribe(kind, consumer, handler)`;
   `internal/platform/audit` records `audit_trail_entries` in the caller's
   transaction (the audit API lands in P18b).
4. Periodic tasks: `queue.prune` and `idempotency.purge`. The
   `scheduler` process mode starts queue workers and the ticker. The
   queue's list, retry and discard of dead jobs, which the operator API
   serves from P3b. Records DV-037 to DV-040.
5. `messageQueueStats` (Infrastructure tag) reads the queue's depth by
   logical queue.

**Exit criteria**
- `queue_periodic_tasks.feature`: two `infraweave all` instances never run
  one task twice; a job that exhausts its attempts is `dead` with its last
  error.
- An enqueue rolled back with its transaction leaves no job, and a killed
  handler's job is retried by the other instance within one lease.
- `make gate` passes at 95% in total and 90% per package.

## P3: Identity and tokens

Decisions: [0006](../research/decisions/0006-authentication-and-tokens.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0022](../research/decisions/0022-api-errors-and-update-semantics.md), [0013](../research/decisions/0013-secrets-at-rest.md).

**Depends on:** P2b, P2c.

Every API request carries a principal: API keys exchange for tokens, sessions
can be listed and revoked, the operator creates organizations with their
first account through the operator API, and each organization's
break-glass admin, whose credential only the operator sets, can log in
before any identity provider exists. The `access` domain is created with
the tenancy model: root, the organizations it holds, their accounts, their
members and the members each account admits.

**Tasks**
1. `internal/access` skeleton (`api/`, `assembly/`, `internal/core`,
   `internal/adapters/{http,pg}`) with its migration stream:
   `organizations` (an immutable slug and a display name), `accounts` (its
   organization, an immutable slug and a display name), `members` and
   `member_identities` (one row per identity provider identity of a
   member), `account_users` (the members an account admits, one row per
   account and member of its organization), `api_keys` (each belonging to
   one account), `sessions`, `signing_keys` and
   `organization_break_glass`. `root` has no row: it is the installation's top node,
   which only operator principals reach. `internal/kernel`
   `organization.go`, `account.go` and `principal.go`, whose principal names
   its organization, its account and, for a person, its member.
2. `internal/access/internal/core/token`: JWTs with go-jose, the
   `api-token` ES256 key, `next`, `active`, `retiring` and `retired`
   states and the daily `authn.keys.rotate` task.
   `internal/platform/crypt/signer` signs with a local key or a KMS
   signing key from the providers of `crypt/kek`
   ([0006](../research/decisions/0006-authentication-and-tokens.md)).
   The issuer defaults to `server.public_url`, which `serve` and `all`
   require ([0020](../research/decisions/0020-reference-deployments-and-packaging.md)).
   Tests: `TestUnitTokenClaims`, `TestUnitKeyRotation` in virtual time.
3. `internal/access/internal/core/apikey`: `iwk_` secrets hashed with
   SHA-256, `POST /auth/token` exchange (`apiKeyUser`), reset, disable,
   delete, `last_used_at`; OIDC API keys verified against the issuer's JWKS
   with the allowlist. `pkg/sdk` gains the exchange and exchanges again
   before `validUntil`. Operations: `apiKeys`, `apiKeyCreate`, `apiKey`,
   `apiKeyUpdate`, `apiKeyDelete`, `apiKeyReset`,
   `apiKeyEnableNonAdminManagers` (the reference's
   `apiKeyEnableNonRootManagers`, at `/account/api-key-non-admin-managers`),
   `apiKeyUser`. An API key belongs to one account, and its token works
   there only. `ApiKeyInput` has no
   `admin` field: a body that sets it answers `400 invalid-request`
   ([0006](../research/decisions/0006-authentication-and-tokens.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md)); the key's `admin` reads whether it holds a
   `space-admin` binding on the account's top space, which P4 seeds the
   `role_bindings` table for and reads, and P4b's API key bindings
   write. Feature: `auth_api_keys.feature` (`@postgres`,
   `@oidc`). Records DV-013 and DV-014.
4. `internal/access/internal/core/session` and the chi
   `authn.Middleware`: session rows, the `__Host-infraweave_session` cookie
   with the `Origin` rule, the 60-second cache cleared by
   `infraweave_invalidate`, forced revocation, `authn.sessions.expire`;
   break-glass login at `/admin-login/{organization}`, an
   organization-admin session with no account and no seat that reaches
   only the `Organization` and `SSO` tags, its credential in
   `organization_break_glass` set through task 8's operator API; CLI
   loopback tokens. A session or token works in one account at a time:
   entering another account of the member's organization issues a session
   bound to that account when `account_users` admits the member, which
   P4b's bindings and P13b's login policies extend. Operations:
   `sessions`, `sessionDelete`, `sessionDeleteAll`, `logout`,
   `cliConfirmationToken`, `viewer`. Feature: `auth_sessions.feature`.
5. Account reads: `id`, `name`, `type` and the vendor-bound
   `defaultPrivateWorkerPoolRunnerImage` placeholder. The rate-limit and
   idempotency caller becomes `<kind>:<actor ID>`; `401` with
   `WWW-Authenticate: Bearer` on every authenticated route.
6. Remove the "auth not enforced" note from `README.md`; `bearerAuth` is now
   enforced by the middleware on every route except health, discovery and
   login callbacks.
7. The [domain wiring](#adding-a-domain) of `access`, a
   hexagon, so it also takes a `core-not-adapters` entry; it is the first
   domain in `domains.go` and the migration order. Its `features` edge lands with
   P3b and its `policiesapi.Evaluator` back edge with P13b
   ([declared graph](repo-layout.md#declared-graph)).
8. The operator API ([0021](../research/decisions/0021-capability-scope-and-supersession.md)):
   `api/src/operator/` bundled to `api/operator.openapi.yaml` under
   `/operator/v1`, generated into `internal/platform/gen/operator` and
   `pkg/client/operator`, outside the parity surface, with `access`'
   operations: create an organization with its first account (which
   starts unclaimed) and optionally its break-glass credential; list,
   read and delete organizations; set, replace and remove an
   organization's break-glass credential. Operator principals from
   `auth.operator.keys` exchange at `POST /auth/token` for a token only
   `/operator/v1` accepts ([0006](../research/decisions/0006-authentication-and-tokens.md)), and any other credential answers `401`
   there. `infraweave auth hash-password` reads a password on standard
   input and prints its PBKDF2-HMAC-SHA256 hash; a hash below 600,000
   iterations answers `422 validation-failed`. Feature:
   `operator_organizations.feature`.

**Exit criteria**
- Every operation listed above is served and passes its success and failure
  scenarios; the parity report shows them served. Their `403` denial
  scenarios need P4's authorizer, so P4's exit holds them to decision 0016's
  evidence rule.
- A revoked session stops working on a second replica within a second.
- A key or session of one account reads nothing of another account, in the
  same organization or in another.
- On an empty installation, an operator key creates an organization with
  its first account and sets its break-glass credential, and the
  break-glass admin signs in and reaches no account route.
- Captures C22 and C119 replay through `TestAccCaptures` once recorded;
  until then the scenarios assert decision 0006's choices.
- `make gate` passes at 95% in total and 90% per package.

## P3b: Organizations, members, account settings and the feature catalogue

Decisions: [0006](../research/decisions/0006-authentication-and-tokens.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0022](../research/decisions/0022-api-errors-and-update-semantics.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0001](../research/decisions/0001-rest-first-spacelift-surface.md), [0019](../research/decisions/0019-ai-provider-boundary.md).

**Depends on:** P3.

The organization level and its served `Organization` tag; members,
managed users, groups, personal keys and the account-wide settings that
have no other owner; decision 0021's feature catalogue, with entitlement
and pooled limits per organization, the seeded tiers, the billing switch,
the `features` operations of the operator API and the answers for the
commercial operations; and
decision 0006's OAuth authorization server, which `/flows/mcp` and `/mcp`
use.

**Tasks**
1. Members, managed users and groups in
   `internal/access/internal/core/{member,user,group}`. A managed user is a
   member of the organization as one account sees it: inviting adds the
   person to the organization if it is not yet a member and admits it to
   the account, and a role in one account grants nothing in another.
   Operations: `managedUsers`, `managedUser`, `managedUserInviteV2`,
   `managedUserUpdate`, `managedUserDelete`, `managedUserResendInvite`,
   `managedUserRequestInvite`, `managedUserRequestRoleChange`,
   `managedUserPersonalApiKeys`, `managedUserGroups`,
   `managedUserGroupCreateV2`, `managedUserGroup`,
   `managedUserGroupUpdateV2`, `managedUserGroupDelete`,
   `managedUserGroupManagedUsers`, `availableUserGroups`. Invitations are
   recorded without e-mail delivery (capture C251). Feature:
   `access_managed_users.feature`.
2. Personal API keys: `personalApiKeys`, `personalApiKeyCreate`,
   `personalApiKeysCount`, `personalApiKeyDisable`, with
   `DEPROVISIONED` on owner removal. Feature: `auth_personal_keys.feature`.
3. Account settings without another owner: `personalApiKeysEnabled`,
   `accountSetPersonalApiKeysEnabled`,
   `accountRequestPersonalApiKeysEnabling`, `apiKeysManageableByNonHumans`,
   `accountToggleAPIKeyManagementFromNonHumans`,
   `apiKeysManagedByNonAdmins` (the reference's
   `apiKeysManagedByNonRootAdmins`), `securityEmail`,
   `accountUpdateSecurityEmail`, `oauthRedirectAllowedHosts`,
   `accountSetOAuthRedirectAllowlist`, `awarenessSourceSurvey`,
   `accountUpdateAwarenessSourceSurvey`, `markedForDeletion`,
   `accountToggleDeletionMark`, `accountCanBeDeleted`,
   `accountCanBeDeletedAt`, `accountConfirmDelete`. API keys and runs
   manage API keys only once a member allows it, which is off by default
   and which no key or run may change. An account is deleted only once
   marked, at once and never as its organization's last; the deletion
   revokes its sessions, removes its rows and records an organization
   trail entry, leaving the platform's trails, events and jobs as an
   organization delete does. Feature: `account_settings.feature`.
4. `internal/features` ([0021](../research/decisions/0021-capability-scope-and-supersession.md)): `catalogue.gen.go`, which `make generate`
   writes from [docs/data/features.json](../data/features.json), the
   catalogue's one source, with the `BillingTierFeature` values each feature
   covers from the `tier-feature:` records of
   [capabilities.json](../data/capabilities.json): every capability, option
   and limit, its requirements, those values and its seeded tiers, and
   `make verify-generate` fails when the file is stale; the five seeded
   tiers and their limits, installed once; `x-infraweave-feature` on every operation of every
   served document and on each gated schema property. The chi middleware
   after authentication answers `403 feature-disabled` and, with billing
   on, `403 feature-not-in-tier`; input validation checks options.
   Entitlement belongs to the organization: its tier plus its grants,
   which every account inherits, with snapshots rebuilt on
   `organization_entitlements.entitlement_version` through `infraweave_invalidate`; the
   dequeue check completes a job of a feature outside the entitlement as
   skipped, counted in `infraweave.features.skipped`. Limits are pooled: a
   limit counts its thing across every account of the organization, an
   organization admin may set an account limit below the pool for any
   account, and creating the thing beyond either answers
   `403 limit-reached`, whose `scope` member names the organization or the
   account. `seats` counts an active member once however many accounts
   admit it, and API keys as Spacelift counts them: a key that is not
   personal holds a seat while used in the last 30 days, and its first use
   in the window, the token exchange, is checked against the limit. Strict
   `features.disabled`. Tests: `TestUnitFeatureCatalogue`,
   `TestUnitEntitlement`, `TestUnitPooledLimits`,
   `TestUnitFeaturesConfig`. Records DV-018 and DV-101.
5. The `Organization` tag in `api/src/api/paths-organization.yaml`:
   infraweave-only operations, outside the parity surface, gated by the
   `organization` capability and reached by organization admins (members
   that hold an organization role) and organization keys. They cover the
   organization's display name (its slug is immutable), its accounts
   (create with an immutable slug, list, remove), its members and their
   organization roles, account limits, and usage of every pooled limit
   across its accounts. No operation of the tag reads an account's stacks,
   runs, state or secrets. Organization keys exchange at `POST /auth/token`
   for a token that only the `Organization` tag accepts, and an account
   credential answers `401` there. Features:
   `organization_accounts.feature`, `organization_limits.feature`. Records
   DV-109.
6. The operator API's `features` operations, in the document P3 task 8
   creates, for the installation-level actions `access` does not serve:
   tiers, the billing switch, organization tiers, grants, the queue's dead
   jobs and `POST /operator/v1/secrets/rotate`, which rotates the data key
   as `infraweave secrets rotate` does (decision
   [0013](../research/decisions/0013-secrets-at-rest.md)). Creating an
   organization, which P3 serves, gains its `organization_entitlements`
   row with the default tier, which the billing switch names, in the same
   transaction through
   `featuresapi`, and organizations created before it get theirs from a
   migration. The first account starts unclaimed until an organization
   admin claims it (`accounts.claimed_at`, a second claim answers `409`,
   decision [0007](../research/decisions/0007-authorization-spaces-and-roles.md)).
   `cmd/infraweavectl` starts, its command tree in `internal/ctl` over
   `pkg/sdk` only, with `profile`, `whoami`, `version`, `api` and the
   `operator` group (`tier`, `organization`, `grant`, `billing`). Features:
   `platform_tiers.feature`, `queue_dead_letters.feature`,
   `cli_operator.feature`.
7. Billing reads per decision 0021's vendor-bound table: `tier` as a tier
   object, `tierFeatures` as the entitlement in infraweave feature names,
   `seats`, `billedExternally`, `billingSubscription`, `onTrialUntil`,
   `availableBillingAddons`, `availableSelfServicePlans`, and the five
   subscription mutations answering `installation-unsupported`;
   `evaluateFeatureFlags` answers an empty set. Features
   `platform_features.feature`, `platform_billing.feature`. Records DV-079
   and DV-080.
8. The OAuth authorization server of decision 0006 in
   `internal/access/internal/core/oauth`, for MCP clients of `/mcp` and
   `/flows/mcp`: RFC 8414 metadata, RFC 7591 dynamic client registration,
   and the authorization and token endpoints; PKCE with `S256` only; the
   consent step as an API operation that the console's consent page calls
   on the console session (the page lands in P24c); access tokens of about
   an hour with rotating refresh tokens; the `mcp:read` and `mcp:write`
   scopes, which never reach beyond the member's own permissions; redirect
   URIs limited to loopback and the hosts of `oauthRedirectAllowedHosts`,
   which account admins maintain. Tests: `TestUnitPKCE`,
   `TestUnitRefreshRotation`, `TestUnitRedirectURIs`. Feature:
   `auth_oauth_server.feature`. Records DV-113.
9. The [domain wiring](#adding-a-domain) of `features`,
   which imports no domain and goes first in `domains.go` and the
   migration order, ahead of `access`; its tables carry organization and
   account IDs as plain columns. `access`' edge to `features` lands
   here: `access` checks its features and limits through `featuresapi`
   ([declared graph](repo-layout.md#declared-graph)).

**Exit criteria**
- The operations of tasks 1 to 8 are served and pass their success and
  failure scenarios. P4b's exit holds them to decision 0016's evidence rule
  once the authorizer and bindings exist: Managed Users less its role-binding
  reads (P4b) and group integrations (P18), Personal API Keys, Billing less
  `usage` (P18b), Feature Flags and the `Organization` tag.
- An operator creates an organization with its first account; an
  organization admin creates a second account, sees the usage of both and
  reads no stack, run, state or secret of either; a member of one account
  sees nothing of the other.
- With billing off, `tier` answers the Enterprise tier object and
  `tierFeatures` every feature; `ai-integrations` in `features.disabled`
  answers `403 feature-disabled` on a gated route only; an unknown name,
  `billing` or a Spacelift spelling such as `SCANS` fails startup.
- With billing on, a member admitted to two accounts takes one seat of the
  organization's `seats`; a seat beyond the pool, or beyond an account
  limit while the pool has room, answers `403 limit-reached`; a tier change
  applies without a restart; and an account credential on the operator API
  or the `Organization` tag answers `401`. Each later phase adds the
  `feature-not-in-tier` scenario of the capabilities it serves to
  `platform_tiers.feature`.
- An MCP client registers, completes the authorization code flow with PKCE
  `S256` and refreshes once; `plain` PKCE, a reused refresh token and a
  redirect URI outside loopback and the allowlist are refused.
- Captures C118, C123, C124 and C125 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P4: Spaces and authorization

Decisions: [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0022](../research/decisions/0022-api-errors-and-update-semantics.md), [0005](../research/decisions/0005-persistence-postgresql.md).

**Depends on:** P3.

Spaces and the evaluator decide every route from here on: deny by default,
`404` for what the caller cannot read, `403` for what it can read but not do.

**Tasks**
1. `internal/access/internal/core/space`: `spaces`, the closure table,
   each account's top space, seeded with the account and addressed by its
   immutable slug, and the `legacy` space beneath it; slug rules; moves
   refused when a stack would lose a reachable entity. Nothing sits above an
   account's top space, so no grant reaches from one account into another,
   and no served space is named `root`. The account admin is `space-admin`
   on the account's top space. Operations: `spaces`, `space`,
   `spaceCreate`, `spaceUpdate`, `spaceDelete`, `searchSpaces`,
   `searchSpacesSuggestions`, `allowNonAdminSpaceCreation` and
   `accountToggleAllowNonAdminSpaceCreation` (the reference's
   `allowNonRootAdminSpaceCreation` and
   `accountToggleAllowNonRootAdminSpaceCreation`). Feature:
   `authz_spaces.feature`. Records DV-111.
2. The action catalogue: `specref` generates
   `internal/access/api/actions.gen.go` from the 150-value `Action` enum and
   `internal/access/api/actions.yaml` gives each its subject, fallback and
   flags; `make verify-reference` fails on a mismatch. The `roles`,
   `role_actions` and `role_bindings` tables, with the five system roles
   seeded per account. Claiming an account (P3b's organization admin) and
   creating one write the `space-admin` binding on its top space as a row of
   `role_bindings`, in the same transaction. An API key's `admin` reads
   whether it holds `space-admin` on the account's top space (DV-013).
3. `internal/access/internal/core/authz`: `Authorize` and `Reachable` over
   resident, propagated and inherited grants, the stack self-read, the
   in-run restriction; per-account snapshots rebuilt on `authz:<account>`.
   Tests: table tests from report 14's worked cases;
   `TestUnitClosureProperty`.
4. Enforcement point: `x-infraweave-action` and `x-infraweave-subject` on
   every operation of `api/api.openapi.yaml`, the scaffolder writing them
   from the catalogue, the generated route table and `authz.Require`
   middleware. `api/api_test.go` fails on an operation without both.
   Every resource the API returns carries `allowedActions`, computed by the
   evaluator that enforces them ([0023](../research/decisions/0023-console.md)). Records DV-108.
   The `User` view's `admin` and `actions` join its composition in the
   HTTP adapter as the evaluator's answers, beside the `apikey` service's
   `apiKeys`, `ownedApiKeys` and `canCreatePersonalApiKey` (decision
   0007's grant views).
5. Every list and search filters with `Reachable` as
   `space_id = ANY($n)`.
6. Generic search: `internal/kernel/search` predicate types and
   `internal/platform/sql/search`, which compiles `SearchInput` predicates,
   ordering and cursors to SQL under `Reachable`. Its first users are
   `searchSpaces` and `searchSpacesSuggestions`; `role_actions` data source
   support through `GET` of the catalogue.

**Exit criteria**
- The Spaces operations listed above, and P3's operations with their `403`
  denial scenarios, meet decision 0016's evidence rule; report 26 shows
  Viewer `implemented` and API Keys, Auth and Sessions and Security Keys
  `partial`.
- A route without a route-table entry answers `403`; a hidden space and a
  missing one both answer `404 not found` (`api_errors.feature`).
- A `space-admin` binding on one account's top space grants nothing in
  another account of the same organization.
- Captures C115, C126 and C127 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P4b: Roles and role bindings

Decisions: [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0006](../research/decisions/0006-authentication-and-tokens.md).

**Depends on:** P4, P3b, P5.

Custom roles and every binding kind, with the grant views that explain why a
caller can do something.

**Tasks**
1. `internal/access/internal/core/role`: `roles`, `role`, `roleCreate`,
   `roleUpdate`, `roleDelete`; disabled and deprecated actions refused with
   `422`. Feature: `authz_roles.feature`.
2. `internal/access/internal/core/binding`: user, group, API key and stack
   bindings, single and batch: `userRoleBinding*` (5),
   `userGroupRoleBinding*` (5), `apiKeyRoleBinding*` (5),
   `stackRoleBinding*` (5), `apiKeyApiKeyRoleBindings`,
   `managedUserUserRoleBindings`, `managedUserGroupUserRoleBindings`,
   `stackRoleBindings`, over P4's `role_bindings` table, which already
   holds the `space-admin` binding claiming or creating an account writes.
   Legacy `SpaceAccessRule` inputs stored as bindings
   with `origin = access_rule`. `stacks` imports
   `access` in the [declared graph](repo-layout.md#declared-graph), so a
   stack binding holds the stack's ID as a plain column, `stacks`
   removes a deleted stack's bindings through `accessapi`, and the stack
   binding operations and `stacksWithAccessToSpace`, which name stacks,
   are `stacks`' over `accessapi`. Feature: `authz_bindings.feature`.
3. Grant views with the explaining evaluator: `usersWithAccessToSpace`,
   `userGroupsWithAccessToSpace`, `apiKeysWithAccessToSpace`,
   `apiKeySessionsWithAccessToSpace`, `userSessionsWithAccessToSpace`,
   `stacksWithAccessToSpace` and their suggestion operations.
4. Binding and role changes apply to live sessions at once (decision 0007's
   snapshot invalidation). Records DV-019 to DV-023.

**Exit criteria**
- Roles, the binding and grant-view operations of Spaces, API Keys and
  `stackRoleBindings`, and P3b's operations with their `403` denial
  scenarios, meet decision 0016's evidence rule.
- Captures C116, C117 and C128 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P5: Stacks

Decisions: [0022](../research/decisions/0022-api-errors-and-update-semantics.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0005](../research/decisions/0005-persistence-postgresql.md), [0004](../research/decisions/0004-repository-architecture.md).

**Depends on:** P4.

Stacks are created, configured, locked and searched on PostgreSQL, with the
update semantics, input strictness and search every later family reuses.

**Tasks**
1. `internal/stacks` (flat domain) with its migration stream (`stacks`,
   `stack_hooks`, labels); `api/` with `Stacks.Get`, `ByRepository`,
   `LockForRun`, `Settings` and `StackChanged`. Operations: `stacks`,
   `stack`, `stackCreate`, `stackUpdate`, `stackDelete`, `stackReslug`,
   `stackDisable`, `stackEnable`, `stackLock`, `stackUnlock`, `starSet`.
   Feature: `stacks_crud.feature`.
2. `internal/iac` with `terraform` and `opentofu` settings validation
   (`iac.ValidateSettings`), so vendor configuration is checked at write
   time; `iactest` contract suite. Each vendor is an option on its
   `vendorConfig` member (`vendor-terraform`, `vendor-opentofu`).
3. Update semantics in `internal/platform/httpkit/strict`: `PATCH` with
   `application/merge-patch+json` through the `PUT` path, `ETag` from the
   `version` column, `If-Match` with `412`; every input schema
   `additionalProperties: false`; label rules. Tests:
   `api_updates.feature`, `api_strictness.feature`. Records DV-085 to
   DV-089.
4. Stack search over P4's generic search: `searchStacks` and
   `searchStacksSuggestions`.
5. Version Catalog in `internal/vendors`: `terraformVersions`,
   `terraformEffectiveVersion`, `openTofuVersions`,
   `openTofuEffectiveVersion`, from a release index refreshed by a periodic
   task.
6. Stack reads that later phases fill answer from empty data until then:
   `stackRuns`, `stackRunsInRange`, `stackProposedRuns`, `stackTasks` and
   `stackRun` return empty pages or `404`.
7. The [domain wiring](#adding-a-domain) of `vendors` and
   `stacks`, in that order after `access` in `domains.go` and the migration
   order. `vendors` imports `features` and `access`; `stacks` imports
   `features`, `access` and `vendors`. A stack holds its space's ID through
   `accessapi.Spaces.Hold` and registers a space guard, and holds its VCS
   integration's ID as a plain column, since `vcs` imports `stacks`. The
   `stacks` edge to `workers` lands with P8, and `stacks`'
   `stacksapi.StackStatus` back edge with P7
   ([declared graph](repo-layout.md#declared-graph)).
8. Account and organization deletion becomes
   [decision 0004](../research/decisions/0004-repository-architecture.md#repository-shape-a-modular-monolith)'s
   purge: `access` deletes its rows of the account or organization,
   revoking its sessions and keys so it answers `404`, and appends an
   `account.deleted` event per account in the same transaction. `features`
   and `stacks` consume it, each deleting its rows of the account in
   batches, and the outbox retries a purge stopped part way until it
   finishes. The nightly `integrity.orphans` task counts held IDs whose
   row is gone, per column, logging each column with any at warn until
   P18b makes the counts a metric. Feature: `account_settings.feature`
   gains a deletion that leaves no stack or entitlement row of the
   account, and `operator_organizations.feature` the same of an
   organization.

**Exit criteria**
- The Stacks operations of task 1 and the Version Catalog operations of
  task 5 meet decision 0016's evidence rule; the parity baseline drops them.
- `PUT` without an optional field resets it, `PATCH` keeps it, and a stale
  `If-Match` answers `412`.
- Captures C24, C25 and C26 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P6: Contexts, configuration and expressions

Decisions: [0013](../research/decisions/0013-secrets-at-rest.md), [0022](../research/decisions/0022-api-errors-and-update-semantics.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0024](../research/decisions/0024-expression-language.md).

**Depends on:** P5, P2b.

Environment variables, mounted files and hooks on stacks and contexts, with
auto-attachment by label and one resolver that hands a run its ordered
environment, and the expression engine every later surface uses.

**Tasks**
1. `internal/configuration` (flat domain): `config_elements` with
   `value_sealed` for every element and `checksum`, `write_only`;
   `contexts`, `context_attachments` with priorities and
   `is_autoattached`. Operations: `contexts`, `context`, `contextCreateV2`,
   `contextUpdateV2`, `contextDelete`, `contextAttach`, `contextDetach`,
   `contextAttachedStack`, `contextConfigElement`, `contextConfigAdd`,
   `contextConfigDelete`, `searchContexts`, `searchContextsSuggestions`,
   `attachableContextsInSpace`, `searchSpaceContexts`,
   `searchSpaceContextsSuggestions`, and the item
   `PUT /contexts/{contextId}/configs/{configId}`; `contextConfigAdd`
   answers `201` or `200` as an upsert (decision 0022). Feature:
   `config_contexts.feature`.
2. Stack configuration: `stackConfigElement`, `stackConfigAdd`,
   `stackConfigDelete`, `stackAttachableContexts`, and the item
   `PUT /stacks/{stackId}/configs/{configId}` answering `201` then `200`;
   `stackConfigAdd` answers `201` or `200` as an upsert (decision 0022).
   Write-only values are never readable. Feature:
   `config_stack_elements.feature`.
3. Auto-attachment: `autoattach:<label>` matching with `text[]` and GIN
   indexes, reaching child spaces by decision 0007's inheritance.
4. `configuration/api.Environment.Resolve(target)`: the ordered elements and
   hooks report 13 defines, with `${{ env.NAME }}` text templates rendered
   in `serve` at the run handover in P8 and `${NAME}` left to the shell;
   `Resolve` hands the templates on unrendered. `configuration` declares
   the `environment` surface
   (`env`, `stack`; `runs` adds `run` in P7), and a value's templates are
   type-checked against it when the value is saved.
   Tests: `TestUnitEnvironmentPrecedence` from report 13's tables. Records
   DV-105.
5. `internal/platform/expr` ([0024](../research/decisions/0024-expression-language.md)): CEL through cel-go and text templates, one
   environment per set of declared types, type-checking against the surface's context,
   the cost limit and timeout, `now` and the random seed fixed per
   evaluation, results masked by decision 0013's masker,
   `internal/platform/mask`, which this phase creates for literal values
   and P9 extends with the well-known patterns. The `expressions` domain,
   `internal/expressions`, serves `POST /expressions/check`, `/complete`
   and `/evaluate` over the surfaces each domain's assembly lists through
   `Surfaces()`, as decision 0024 specifies them. Tests:
   `TestUnitExprLibrary`, `TestUnitExprTemplates`,
   `TestUnitExprDeterminism`, `TestUnitExprCostLimit`,
   `TestUnitExprTimeout`, `TestUnitExprSchemaTypes`, `TestUnitMaskOverlaps`
   and `TestUnitMaskMinimum` (the 4-byte rule, DV-051). Feature:
   `expressions_editor.feature` against a stack, which later phases extend.
   Records DV-103.
6. The [domain wiring](#adding-a-domain) of
   `configuration` and `expressions`. `configuration` follows `stacks` in
   `domains.go` and the migration order and imports `features`, `access`
   and `stacks`; its attachments and stack elements reference `stacks`.
   `runs`' edge to `configuration` lands here if P7 has landed.
   `stacksapi` gains `Stacks.Hold`, a `StackGuard` whose `LostOnMove`
   refuses a stack move that would lose an attachment and whose `Deleting`
   removes the holder's rows in the stack delete's transaction, and
   `Stacks.Find` by space and label; `accessapi.SpaceNode` gains `Inherit`.
   The stack view's `attachedContexts` and `config` come through the back
   edge `stacks` → `configuration`: `stacksapi.StackConfiguration`, which
   `configuration` implements and `internal/app` binds, since an `api/`
   package imports only the standard library, `kernel` and `outbox`.
   `expressions` has no `api/` and no migration stream, imports no domain
   and is constructed last, from every domain's `Surfaces()`
   ([declared graph](repo-layout.md#declared-graph)).

**Exit criteria**
- Contexts and the stack configuration operations meet decision 0016's
  evidence rule; the parity baseline drops them.
- A secret element's value never appears in any response, log line or
  audit entry; its checksum does.
- Captures C97 and C99 replay once recorded. C98 and C101 to C103 need a
  run, and replay with P9.
- `make gate` passes at 95% in total and 90% per package.

## P6b: Sources

Decisions: [0012](../research/decisions/0012-vcs-integration-model.md), [0004](../research/decisions/0004-repository-architecture.md), [0016](../research/decisions/0016-testing-and-acceptance.md).

**Depends on:** P5.

The `vcs` domain begins as the source of a stack's commits: a stack on a
public Git repository shows its branch head and the commit its runs use,
before any VCS integration exists.

**Tasks**
1. The [domain wiring](#adding-a-domain) of `vcs`, a hexagon, after
   `configuration` in `domains.go`; `runs`, `state`, `cloud` and `registry`
   take their places before it as they arrive. Its edges to `features`,
   `access` and `stacks`; its `runs` edge lands with P7, its `policies`
   edge with P13 and its `registry` edge with P17. It has no migration
   stream until P12's integrations. `vcsapi.Sources` resolves a ref of a
   stack's repository and names its source URL.
2. The `@vcs` fixture: a Gitea container with a repository seeded per
   scenario, served over HTTPS with a CA the `infraweave` subprocess trusts,
   and steps that push commits and read a branch head.
3. `internal/vcs/internal/core/port`'s `Source` and the `rawgit` provider
   (public HTTPS, no credential) in `internal/vcs/internal/adapters/rawgit`:
   `ResolveRef`, `ReadFile` and `AffectedFiles`; the `sourcetest` contract
   suite every provider passes.
4. The tracked commit: `trackedCommit`, `trackedBranchHead` and
   `trackedCommitSetBy` in `stacks`' table, which `vcs` writes through
   `stacksapi` when a stack is created or its repository, branch or provider
   changes, and through `stackSyncCommit` and `stackSetCurrentCommit`, which
   `vcs`' partial serves since `stacks` imports no `vcs`. Feature:
   `vcs_sources.feature`.

**Exit criteria**
- `vcs_sources.feature` (`@postgres`, `@vcs`): a `GIT` stack on a Gitea
  repository shows the branch head as `trackedBranchHead` and
  `trackedCommit`; after a push `stackSyncCommit` moves both, and
  `stackSetCurrentCommit` pins `trackedCommit` to an older commit.
- `stackSyncCommit` and `stackSetCurrentCommit` meet decision 0016's
  evidence rule; the parity baseline drops them.
- `rawgit` passes `sourcetest` against Gitea.
- `make gate` passes at 95% in total and 90% per package.

## P7: Run state machine and scheduling

Decisions: [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0011](../research/decisions/0011-persistent-work-queue.md), [0004](../research/decisions/0004-repository-architecture.md), [0015](../research/decisions/0015-events-notifications-webhooks-audit.md), [0024](../research/decisions/0024-expression-language.md).

**Depends on:** P6b, P5, P2c.

The `runs` domain owns every table a run transition writes, and one function
moves a run between states in one transaction. Runs can be created and
queued; nothing executes them until P8.

**Tasks**
1. `internal/runs` (hexagon) with its migration stream: `runs`,
   `run_state_transitions`, `stack_run_locks`, `stack_run_state`,
   `stack_outputs`. Each transition writes the stack's `state` and
   `blocked` through `stacksapi` in its transaction, so stack lists filter,
   sort and count by state from `stacks`' own table, and
   `stacksapi.StackStatus`, which `runs` implements, serves the blocker and the run lock to stack reads
   ([0004](../research/decisions/0004-repository-architecture.md#constraints)).
   `internal/kernel/run.go` holds the 21 `RunState`
   values (`PENDING` absent, `UNKNOWN` never assigned), `RunType`,
   `StackState` and `RunPriority`.
2. `internal/runs/internal/core/machine`: the edges of the
   [run-transitions register](../data/run-transitions.json), report 08's
   T1 to T43 and decision 0008's T44, as data, with guards. Tests:
   `TestUnitTransitionTable` over every edge and every illegal pair;
   `TestUnitTransitionRegister` reads the register and fails on an edge the
   machine is missing or has beyond it.
3. `internal/runs/internal/core/lifecycle`: `Transition` with
   `state_version` fencing, the stack state update, `outbox.Append` of
   `run_updated`, terminal lock release and the `run.schedule` follow-up;
   `Create` with deduplication on stack, commit and run type.
4. `internal/runs/internal/core/queuegate` and `schedule`: `run.schedule`
   with unique key `stack:<id>`, the stack lock for blocking runs, the
   disabled-stack and dependency gates (approval and dependencies are
   no-ops until P13 and P16), the seven priority levels.
5. Run creation and the read and queue operations: `runTrigger`, which
   creates a `TRACKED` or `PROPOSED` run on the commit it names or on the
   stack's tracked commit (P6b), only the lock owner creating runs on a
   locked stack; `run`, `runStack`, `searchRuns`,
   `searchRunsSuggestions`, `runCancel`, `runPrioritySet`, and the stack
   run lists left empty by P5, which `runs`' partial serves, since `stacks`
   imports no `runs` package. Feature: `runs_queue.feature` (a blocking
   run takes the lock, a second waits, cancel releases it). A run's
   `allowedActions` include only the transitions its state allows.
6. The [domain wiring](#adding-a-domain) of `runs`, a
   hexagon, after `configuration` in `domains.go` and the migration order:
   its edges to `features`, `access`, `stacks`, `configuration` and
   `vendors`, with `runs` and `stack_run_locks` referencing `stacks`, and
   `vcs`' edge to `runs`. Its `workers` edge lands with P8 and its
   `policies` edge with P13. `stacks` reads run state through
   `stacksapi.StackStatus`, and `runTrigger` resolves a named commit through
   `vcsapi.Sources`, back edges that `internal/app` sets after
   construction (`SetStackStatus`, `SetSources`), adding no import
   ([declared graph](repo-layout.md#declared-graph)).
7. `runs` adds `run` to the environment surface through its `Surfaces()`
   ([0024](../research/decisions/0024-expression-language.md)), so a
   variable's template naming a run field is checked when the variable is
   saved; P8 renders it at the handover.

**Exit criteria**
- `runs_queue.feature` passes on two `infraweave all` instances; one
  `run.schedule` evaluation per stack burst.
- `runTrigger`, `run`, `runStack`, `searchRuns`, `runCancel` and
  `runPrioritySet` meet decision 0016's evidence rule.
- Captures C25 and C33 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P8: Worker protocol, private pools and the Docker launcher

Decisions: [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0006](../research/decisions/0006-authentication-and-tokens.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0004](../research/decisions/0004-repository-architecture.md), [0020](../research/decisions/0020-reference-deployments-and-packaging.md).

**Depends on:** P7, P6, P4, P2b.

Private worker pools, the `/worker/v1` protocol and `infraweave launcher`, which
claims a run, prepares it and starts a container from the same binary. The
container runs a stub runner until P9.

**Tasks**
1. `internal/workers` (hexagon) with `worker_pools`, `workers`,
   `worker_nonces`, `worker_pool_ca`: pool creation from a CSR for a P-256
   key signed by the installation pool CA, the pool token, reset by
   certificate serial; the `private-worker-pools` capability and the
   `private-workers` limit, pooled across the organization's accounts,
   which a worker reconnecting under its registration does not count
   against. Records DV-093.
   Operations: `workerPools`, `workerPool`, `workerPoolCreate`,
   `workerPoolUpdate`, `workerPoolDelete`, `workerPoolReset`,
   `workerPoolCycle`, `workerDrainSet`, `searchWorkerPools`,
   `searchWorkerPoolsSuggestions`, `workerPoolSearchWorkers`,
   `workerPoolSearchWorkersSuggestions`, `attachableWorkerPoolsInSpace`,
   `stackAttachableWorkerPools`, `accountUpdateDefaultWorkerPoolRunnerImages`,
   `defaultPrivateWorkerPoolRunnerImage`. Feature: `workers_pools.feature`.
2. `api/src/worker/` bundled to `api/worker.openapi.yaml`, generated into
   `internal/platform/gen/worker` and `internal/agent/wire`;
   `internal/workers/internal/adapters/protocol` serves `register`, `poll`,
   `deregister`, `runs/{run}/spec`, `reports`, `upload-urls`, `log-chunks`,
   `artifacts` and `workspace`. Held polls woken by `infraweave_runs`; the
   claim with `FOR UPDATE SKIP LOCKED` in
   `internal/runs/internal/core/schedule`.
   Tests: `TestUnitWorkerRegistration` (foreign chain, reset serial,
   replayed `jti`, skewed `iat`), `TestAccRunClaim` (50 concurrent pollers,
   one claim per run). Records DV-008, DV-024, DV-027.
3. `internal/runs/internal/core/handover`: the run specification with the
   environment resolved by P6's `configuration/api.Environment`, the run
   token, the in-run API token, and the secret part sealed to the pool key
   with decision 0013's pool envelope (ECDH P-256, HKDF-SHA256,
   AES-256-GCM); the per-run artifact key.
4. `internal/agent`: `config`, `launcher` (register, poll, signals, drain,
   cycle), `executor/docker` (the launcher's own binary bind-mounted
   read-only as the container's root process, UID 1983) and `agenttest`.
   `internal/agent/cli` holds `infraweave launcher` and the hidden
   `infraweave runner` stub, which `cmd/infraweave/agent.go` registers.
5. Liveness: a worker unseen for `worker.offline_after` (3 minutes) is
   `OFFLINE` and receives no new work; `worker.reap` fails a run whose
   worker stays offline past `run.lost_after` (60 minutes) as a lost run
   with the note `worker lost`; drain, cycle and reset as decision 0008's
   table. Records DV-025 and DV-092.
6. `worker.sample`, a periodic task in the `scheduler` process mode that
   runs every minute and writes each pool's busy workers, registered
   workers and queued runs to `worker_pool_samples`, kept for
   `worker.samples.retention` (35 days). `workers/api` serves the samples,
   which P18's `worker_usage` events and P18b's usage read
   ([0008](../research/decisions/0008-run-execution-and-worker-protocol.md),
   [0015](../research/decisions/0015-events-notifications-webhooks-audit.md)). Tests: `TestUnitWorkerPoolSample` in virtual time,
   and a retention case.
7. The [domain wiring](#adding-a-domain) of `workers`, a
   hexagon, after `access` in `domains.go` and the migration order: its
   edges to `features` and `access`, and the edges waiting on it, `stacks`
   to `workers` (a stack's worker pool references `worker_pools`) and
   `runs` to `workers` (`runs`' `run_assignments` references `workers`).
   The protocol claims and reports through `runsapi.Scheduler` and
   `runsapi.Reports`, back edges that `internal/app` sets after
   construction (`SetScheduler`, `SetReports`), adding no import
   ([declared graph](repo-layout.md#declared-graph)).

**Exit criteria**
- `worker_protocol.feature` (`@postgres`, `@objectstore`): a launcher
  registers, claims a run, receives a sealed specification, starts a
  container that reports and exits; a killed launcher's worker turns
  `OFFLINE` and, in virtual time past `run.lost_after`, its run is lost
  with the note `worker lost`.
- The Worker Pools operations of task 1 meet decision 0016's evidence rule.
- Two instances write one `worker_pool_samples` row per pool per minute.
- The interpolation scenario of `expressions_deploy.feature`: a
  `${{ env.NAME }}` value reaches the run's specification rendered, and one
  that reads a secret arrives sealed and write-only.
- Captures C58, C59, C60, C61, C62, C63 and C64 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P8b: Managed state backend

Decisions: [0010](../research/decisions/0010-state-backend.md), [0006](../research/decisions/0006-authentication-and-tokens.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0005](../research/decisions/0005-persistence-postgresql.md).

**Depends on:** P8.

Terraform and OpenTofu reach infraweave's `http` state backend with the run
token, so the first run stores its state in infraweave rather than in its
workspace.

**Tasks**
1. `internal/state` (flat domain) with `state_workspaces`, `state_locks` and
   `managed_state_versions`; `api/` with `Versions.Latest` and `Outputs`.
2. `internal/state/internal/backend`: `GET`, `POST`, `LOCK` and `UNLOCK` on
   `/state/v1/stacks/{stack}` and the workspace form, registered with
   `chi.RegisterMethod`; the authorization table per run state through
   `runs/api.StateAccess`; shared and exclusive locks with `423` and the
   holder's `LockInfo`; plain-text errors. Tests:
   `TestUnitStateAccessTable`, `TestUnitLockConflicts`.
3. Writing a version: `pending` blob row, SHA-256 of the body and the
   `Content-MD5` check, which `fips140=only` accepts without checking
   (DV-094), the size limit,
   sealing into `states`, client-encrypted bodies, no new version for an
   identical body, `state.indexed` enqueued. Reading streams and unseals.
4. The runner's `infraweave_backend.tf` with `TF_HTTP_USERNAME` and
   `TF_HTTP_PASSWORD`. `state` releases the locks a run still holds when
   `runs` emits that run's terminal event, since a run transition writes
   only `runs`' tables, and `runsapi.StateAccess` refuses a lock to a run
   that is no longer active
   ([0004](../research/decisions/0004-repository-architecture.md#constraints)).
   Records DV-032 to DV-035.
5. The [domain wiring](#adding-a-domain) of `state`, after
   `runs` in `domains.go` and the migration order: its edges to
   `features`, `access`, `stacks` and `runs`, with `state_workspaces`
   referencing `stacks`. `runs` reads `stateapi.Versions` through a back
   edge bound by its first use at handover, P16's output references
   ([declared graph](repo-layout.md#declared-graph)).

**Exit criteria**
- `state_backend.feature` (`@terraform`, `@tofu`): the scenario acts as a
  worker over `/worker/v1`, claims a run and runs `terraform` and `tofu` in a
  container with its run token; init, plan and apply go through the
  backend, and a second apply is blocked by `423` until the first ends.
- A state with a known secret output is unreadable in the object store.
- Captures C105 and C111 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P9: First tracked run end to end

Decisions: [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0010](../research/decisions/0010-state-backend.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0016](../research/decisions/0016-testing-and-acceptance.md), [0012](../research/decisions/0012-vcs-integration-model.md),
[0020](../research/decisions/0020-reference-deployments-and-packaging.md).

**Depends on:** P6, P12, P8b.

A tracked Terraform and OpenTofu run goes from trigger to `FINISHED` on a
private Docker worker: source fetched, phases executed with hooks, logs
masked and stored, plan recorded, confirmation honoured and state written
through the backend.

**Tasks**
1. `internal/agent/exec`: the phases of `runner`, with
   `before_*` and `after_*` hooks in one shell session, reports per phase,
   `pause` and `exit` answers. `internal/agent/vendors/{terraform,opentofu}`
   and `internal/iac/{terraform,opentofu}` with plan JSON parsing into
   changes.
2. `internal/agent/tools`: download and verification of Terraform (HashiCorp
   PGP key) and OpenTofu (OpenTofu signing key) per version, the cache and
   `launcher.tool_mirror`. Tests: `TestUnitToolVerification` with a
   tampered sum.
3. Log masking through `internal/platform/mask`: secret values,
   `::add-mask` words and the well-known patterns, which this phase adds;
   log chunks of 1 MiB or two seconds uploaded by pre-signed `PUT`
   and indexed in `run_log_chunks`; sealed artifacts with the run's artifact
   key; the encrypted workspace saved for confirmation. Feature:
   `secrets_run_delivery.feature`.
4. Local workspaces: `uploadLocalWorkspace` and `runProposeLocalWorkspace`
   for local preview, beside the tracked commit P6b's `rawgit` source
   resolves.
5. Run operations: `runConfirm`, `runDiscard`, `runLogs`,
   `runLogsDownloadURL`, `runChangesV2`, `runChangesV3`, plus
   `stackRun`, `stackRuns`, `stackRunsInRange`, `stackProposedRuns` with
   data. The plan decision after `PLANNING` takes T20 or T21 with no policy
   attached.
6. Runner images: `deploy/images/runner-terraform` and
   `deploy/images/runner-opentofu` definitions meeting decision 0008's
   contract; the harness builds `infraweave-runner:acceptance`. In-run
   `INFRAWEAVE_API_TOKEN` with `sub` `<stack>/<run>`, so the provider's
   `infraweave_current_stack` and `infraweave_current_space` have their
   data.
7. The handover asks `runsapi.RunSources`, the back edge P7 binds and `vcs`
   implements, for the run's source URL and its source credential, which
   travels in the run's sealed secret material; the worker fetches the source itself
   ([declared graph](repo-layout.md#declared-graph)).

**Exit criteria**
- `runs_terraform.feature` and `runs_tofu.feature` (`@postgres`,
  `@objectstore`, `@terraform` or `@tofu`, `@vcs`): a tracked run passes
  through report 08's states to `FINISHED`, the stored state version
  matches, the log shows five asterisks for each secret, and `run_updated`
  events are in the outbox.
- `runs_runner_image.feature`: a Spacelift public runner image runs
  unchanged.
- The operations of task 5 meet decision 0016's evidence rule.
- Captures C17, C39, C42 and C104 replay once recorded.
- Captures C98, C101, C102 and C103, which need a run, replay once
  recorded.
- `make gate` passes at 95% in total and 90% per package.

## P10: Run lifecycle completeness

Decisions: [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0010](../research/decisions/0010-state-backend.md), [0022](../research/decisions/0022-api-errors-and-update-semantics.md).

**Depends on:** P9.

Every run type and run action of report 08, the timers, and the run reads
that depend on them.

**Tasks**
1. Run types: proposed, tracked, task, destroy and testing paths in
   `internal/runs/internal/core/lifecycle`; `taskCreate` with
   `skipInitialization`;
   `proposeWithOverrides`; `runTriggerBatch`; `stackTasks`.
2. Actions: `runRetry`, `runStop`, `runKill` (signals through the poll),
   `runTargetedReplan` with `PREPARING_REPLAN` and T44, `runPromote`,
   `runDiscardAll`, `runComment`, `runLogsDelete`.
3. `internal/runs/internal/core/timers`: `run.timers` every minute for the
   2, 7 and 30 day rules; the run timeout and no-log timeout enforced by the
   launcher; `runLogRetentionDays`.
4. Outputs and diagnostics: `stack_outputs` sealed per output with
   `sensitive`; `runWorkerMetrics`, `runCpuProfileDownloadURL` from
   `run-observability`.
5. Workflow files: `.infraweave/workflow.yml` command templates rendered
   by the runner in `internal/agent/exec` at initialization through
   `internal/platform/expr`, with every interpolated value shell-quoted.
   `vendors` contributes the templates' editor surface through
   `Surfaces()`, from each `internal/iac` vendor's workflow keys
   ([option 5](../research/architecture-options/5-modular-monolith.md#expressions)).
   Tests: `TestUnitWorkflowQuoting`, and
   the hostile workspace scenario of `expressions_deploy.feature`. Records
   DV-104.
6. Feature: `runs_lifecycle.feature` (proposed, tracked with confirm,
   autodeploy, task, targeted replan, stop, kill, lost run, timers in
   virtual time).

**Exit criteria**
- The Runs operations of tasks 1 to 4 meet decision 0016's evidence rule,
  and report 26 shows `spacelift_run`, `spacelift_task` and
  `spacelift_stack_outputs` with their API `implemented` (the provider
  follows in P24).
- Captures C10, C34, C36, C37, C38 and C44 replay once recorded; until then
  the scenarios assert decision 0008's gap table.
- `make gate` passes at 95% in total and 90% per package.

## P11: State management

Decisions: [0010](../research/decisions/0010-state-backend.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md).

**Depends on:** P10, P8b.

Everything around the backend: import, history and rollback, external
read-only access, signed URLs, Terragrunt workspaces and retention.

**Tasks**
1. `stateUploadUrl` and `stateDownloadUrl` with tokens under
   `DeriveKey("state-upload-url")`; `PUT /state/v1/uploads/{token}` and
   `GET /state/v1/download/{token}`.
2. Import at creation, by `state` on `StackCreated` carrying the
   `stackObjectID` of `stackCreate`, and later
   (`stackManagedStateImport`), with decision 0010's checks; rollback
   (`stackManagedStateRollback`) as a new version; `isStateRollback`;
   `stackSearchManagedStateVersions`.
3. External access with `STACK_STATE_READ` and `STACK_STATE_DOWNLOAD`;
   `terraform_remote_state` through the `http` backend with a token. No
   Terraform Cloud service is served or listed in discovery ([0010](../research/decisions/0010-state-backend.md)).
4. Tofu Workspaces: `searchTofuWorkspaces`,
   `searchTofuWorkspacesSuggestions`, `tofuWorkspaceUnlock`,
   `tofuWorkspaceUnlockAll`; retention with `state.noncurrent_retention`
   (90 days by default) applied by the daily `state.retention` task, and
   `state.purge` on stack deletion. Records DV-036.

**Exit criteria**
- `state_external_access.feature`, `state_import.feature`,
  `state_rollback.feature` and `state_terragrunt_units.feature` pass.
- Tofu Workspaces and the state operations of the Stacks tag meet decision
  0016's evidence rule.
- Captures C106, C107, C108, C109, C110, C112, C113 and C114 replay once
  recorded.
- `make gate` passes at 95% in total and 90% per package.

## P12: VCS integrations

Decisions: [0012](../research/decisions/0012-vcs-integration-model.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0016](../research/decisions/0016-testing-and-acceptance.md).

**Depends on:** P6b.

GitHub, GitLab, Bitbucket Cloud, Bitbucket Data Center and Azure DevOps
connect through integrations, as peers: a stack on a private repository of
any of them shows its branch head and tracked commit, and each run is handed
a source credential for its repository.

**Tasks**
1. `internal/vcs` gains its migration stream: `vcs_integrations` with sealed
   credentials and webhook secrets, one default per provider in the top
   space, space reach, more than one integration per provider and
   space-level integrations under `multiple-vcs-integrations`, and
   detaching on delete through `stacks.vcs_detached`, which `vcs` writes
   through `stacksapi`; a stack's `vcsIntegrationId` is checked by a guard
   `internal/app` binds. `vcsapi.Sources` resolves a stack's integration at
   use time. Operations: `vcsIntegrations`, `vcsIntegration`,
   `vcsIntegrationTest`, `searchVCSIntegrations`,
   `searchVCSIntegrationsSuggestions`,
   `attachableVCSIntegrationsForProviderInSpace`.
2. `internal/vcs/internal/adapters/github`: `Source`, `Browse` and `Tester`
   over the operator's GitHub App and per-account custom Apps, with the chi
   fake `githubfake`. Operations: `githubIntegration`,
   `githubIntegrationUpdate`, `githubEnterpriseIntegrationCreate`,
   `githubEnterpriseIntegration`, `githubEnterpriseIntegrationUpdate`,
   `githubEnterpriseIntegrationDelete`, `githubEnterpriseIntegrationTest`,
   `githubAppGenerateManifest`, `githubAppCreateFromManifest`.
3. `internal/vcs/internal/adapters/gitlab`: the same capabilities over a
   GitLab access token, with `gitlabfake`. Operations:
   `gitlabIntegrationCreate`, `gitlabIntegration`, `gitlabIntegrationUpdate`,
   `gitlabIntegrationDelete`, `gitlabIntegrationTest`.
4. `internal/vcs/internal/adapters/bitbucketcloud` and `bitbucketdc`: the
   same capabilities over an app password or access token, and over a
   Bitbucket Data Center HTTP access token, with `bitbucketcloudfake` and
   `bitbucketdcfake`. Operations: `bitbucketCloudIntegrationCreateV2`,
   `bitbucketCloudIntegration`, `bitbucketCloudIntegrationUpdateV2`,
   `bitbucketCloudIntegrationDelete`, `bitbucketCloudIntegrationTest`,
   `bitbucketDatacenterIntegrationCreate`,
   `bitbucketDatacenterIntegration`,
   `bitbucketDatacenterIntegrationUpdate`,
   `bitbucketDatacenterIntegrationDelete`,
   `bitbucketDatacenterIntegrationTest`.
5. `internal/vcs/internal/adapters/azuredevops`: the same capabilities over
   a personal access token, with `azuredevopsfake`. Operations:
   `azureDevOpsRepoIntegrationCreate`, `azureDevOpsRepoIntegration`,
   `azureDevOpsRepoIntegrationUpdate`, `azureDevOpsRepoIntegrationDelete`,
   `azureDevOpsRepoIntegrationTest`.
6. `repositories`, `branches`, `vcsIntegrationSearchUsers` and
   `vcsIntegrationSearchUsersSuggestions` through each integration's
   `Browse`.
7. Private stacks and the run's source credential:
   `vcsapi.Sources.Credential` mints a token for the run's one repository
   where the provider can (a GitHub installation token, a GitLab project
   access token, a Bitbucket Data Center repository access token), revoked
   when the run ends, and hands the integration's token otherwise.
   Feature: `vcs_sources.feature` gains a private stack on each provider.
8. The VCS lab: Terraform roots under `test/terraform/vcs/` that create
   a repository per run on GitHub, GitLab, Bitbucket Cloud and Azure DevOps,
   a Bitbucket Data Center container the job starts with a licence from its
   secrets, and the scheduled `vcs-lab.yml` that applies them, runs
   `sourcetest` and the `@vcs-lab` scenarios against them, and destroys
   them.

**Exit criteria**
- `vcs_integrations.feature` (`@postgres`, `@vcs`): each provider's
  integration is created, read, updated, tested and deleted; deleting one in
  use detaches its stacks; a second default for a provider answers `422`; a
  credential appears in no answer, log line, audit entry or event.
- `vcs_sources.feature`: a private stack on each provider shows its branch
  head through its integration.
- The operations of tasks 1 to 6 meet decision 0016's evidence rule; the
  parity baseline drops them.
- Every provider passes `sourcetest` against its fake, and against a real
  repository in a green `vcs-lab` run.
- `make gate` passes at 95% in total and 90% per package.

## P12b: VCS events and feedback

Decisions: [0012](../research/decisions/0012-vcs-integration-model.md), [0011](../research/decisions/0011-persistent-work-queue.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0015](../research/decisions/0015-events-notifications-webhooks-audit.md).

**Depends on:** P10, P12.

Every provider of P12, as peers: webhooks stored before they are
acknowledged, default push behaviour creating tracked and proposed runs,
checks, pull request comments and deployments.

**Tasks**
1. Webhook secrets and endpoints: `githubEnterpriseIntegrationRegenerateWebhookSecret`,
   `githubEnterpriseWebhooksEndpoint`,
   `gitlabIntegrationRegenerateWebhookSecret`, `gitlabWebhooksEndpoint`,
   `bitbucketCloudIntegrationRegenerateWebhookSecret`,
   `bitbucketCloudWebhooksEndpoint`,
   `bitbucketDatacenterIntegrationRegenerateWebhookSecret`,
   `bitbucketDatacenterWebhooksEndpoint`, `azureDevOpsWebhooksEndpoint`,
   with the previous secret valid until its expiry.
2. Intake at `POST /webhooks/vcs/github` and
   `/webhooks/vcs/{provider}/{integrationId}`: body bound, verification
   against the current and previous secret by each provider's scheme
   (decision 0012's table), filtering, `vcs_deliveries` stored with
   `vcs.process_delivery` in one transaction. Tests: each provider's
   `verify_test.go` (`TestUnitGitHubSignature` and its peers) and
   `parse_test.go`, against P12's fakes. Records DV-041 to DV-044.
3. Processing: `vcs.process_delivery` and `vcs.evaluate_event` with the
   default push behaviour (push policies in P13), run deduplication,
   `vcsEventTriggeredRunsLimit`, `accountUpdateVCSEventTriggeredRunsLimit`,
   ignored triggers (`stackSearchRunIgnoredTriggers`), superseded proposed
   runs, a push to a stack's branch moving its tracked commit through
   P6b's projection, and a renamed repository followed on every provider by
   the stable repository ID its events carry.
4. Feedback: `vcs.report_check` with individual and aggregated checks,
   check actions (Preview and Deploy) as GitHub check-run buttons and as
   pull request comment commands on every other provider, `Comments.Upsert`
   with the hidden marker, GitHub Deployments and GitLab environments.
   Records DV-047.
5. Features: `vcs_webhooks.feature` and `vcs_feedback.feature` (`@vcs`,
   Gitea for real webhooks and statuses, each provider's fake for its own
   shapes, recorded payloads driving a run and a check on every provider),
   and their `@vcs-lab` scenarios against P12's lab.

**Exit criteria**
- On every provider, a signed push creates one tracked run; a bad signature
  or secret answers `401` and stores nothing; a duplicate delivery creates
  nothing; a replica stopped after acknowledging leaves the run to another.
- A pull request comment command starts a proposed or tracked run on every
  provider but GitHub, as GitHub's check action does.
- VCS Integrations meets decision 0016's evidence rule.
- Captures C72 to C76, C79 and C80 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P13: Policy engine and workflow policies

Decisions: [0009](../research/decisions/0009-policy-engine-opa.md), [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0012](../research/decisions/0012-vcs-integration-model.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md).

**Depends on:** P12b.

Embedded OPA evaluates plan, approval, push and trigger policies at the run's
state points, with receipts committed in the transition they justify.

**Tasks**
1. `internal/platform/rego`: OPA v1 as a library, `REGO_V0` and `REGO_V1`,
   the capability set with removed built-ins, the compilation cache,
   timeouts and print capture. `policyRuntime` (Infrastructure tag).
   Tests: `TestUnitRegoEngines`, `TestUnitRemovedBuiltins`,
   `TestUnitPolicyTimeout`.
2. `internal/policies` (flat domain): `policies`, attachments and
   auto-attachment by label; `run_policy_receipts` and `run_flags` are in
   `runs`' stream, since a run transition commits them. Operations:
   `policies`, `policy`, `policyCreatev2`, `policyUpdatev2`,
   `policyDelete`, `policyAttach`, `policyDetach`, `policyAttachedStack`,
   `attachablePoliciesInSpace`, `autoattachmentForLabels`, `searchPolicies`,
   `searchPoliciesSuggestions`, `searchSpacePolicies`,
   `searchSpacePoliciesSuggestions`, `stackAttachablePolicies`,
   `stackPolicyAttachment`. `ACCESS` and the superseded aliases are absent
   from `PolicyType`. Records DV-028 to DV-031.
3. `internal/policies/internal/{inputs,combine}`: per-type input structs
   matching report 09 section 4, with the reference's `spacelift.*` input
   keys named `infraweave.*`, `GET /.well-known/policy-contract.json`,
   combination rules. Tests: `TestUnitPolicyContract`,
   `TestUnitCombination`. `POST /policies/check`, served by `expressions`
   over the surface `policies` lists in `Surfaces()`, compiles a body
   with the preamble and returns diagnostics with ranges ([0024](../research/decisions/0024-expression-language.md)).
4. Evaluation points in `internal/runs/internal/core/gates`: approval on
   `QUEUED`, `UNCONFIRMED`, `PENDING_REVIEW` and each `runReview`; plan after
   `PLANNING` and each targeted replan; push in `vcs.evaluate_event`;
   trigger through the durable `policy.trigger` job. `runReview`,
   `runPolicyReceiptSample`.
5. Features: `policy_plan.feature`, `policy_approval.feature`,
   `policy_push.feature`, `policy_trigger.feature`, against a real Terraform
   run.
6. The [domain wiring](#adding-a-domain) of `policies`,
   after `configuration` and before `runs` in `domains.go` and the
   migration order: its edges to `features`, `access` and `stacks`, with
   stack attachments referencing `stacks`, and the edges waiting on it,
   `runs` to `policies` (`run_policy_receipts` references `policies`) and
   `vcs` to `policies` (push policies through `policiesapi.Evaluator`),
   and `registry`'s and `intelligence`'s if P17 or P25 has landed.
   `policies` imports none of its callers
   ([declared graph](repo-layout.md#declared-graph)).

**Exit criteria**
- Option 5's walkthrough holds against Gitea: a push opens a proposed run, a
  plan policy warns, and the check and pull request comment appear.
- The operations of task 2 and task 4 meet decision 0016's evidence rule.
- Captures C40, C41, C49, C50, C51, C55 and C57 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P13b: Login, task and initialization policies, sampling and simulation

Decisions: [0009](../research/decisions/0009-policy-engine-opa.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0006](../research/decisions/0006-authentication-and-tokens.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md).

**Depends on:** P13, P4b.

The policy types outside the run workflow, the `LOGIN_POLICIES` scheme, and
the tooling around evaluation: samples, captures, simulation and templates.

**Tasks**
1. Login policies through `internal/access/internal/core/login`:
   authorization per account, after the organization has authenticated the
   member. The account's `LOGIN` policies, evaluated on its top space, admit or refuse
   the member and compute its roles when it enters the account, at session
   creation and API key exchange; `session_grants`, `deny_message`
   aggregation, revocation on change; `authorizationScheme` and
   `accountUpdateAuthorizationScheme`. The accounts that admit a member are
   the entries of its account switcher, which is empty when none does.
   `access` evaluates them through `policiesapi.Evaluator`, a back edge
   that `internal/app` sets after construction (`SetEvaluator`), adding no
   import
   ([declared graph](repo-layout.md#declared-graph)). Feature:
   `policy_login.feature`, `authz_login_policy.feature`.
2. `TASK` and server-side `INITIALIZATION` types, live but deprecated with
   `Deprecation` headers. Feature: `platform_deprecation.feature`.
3. Sampling and captures: `policy_evaluations`, hourly sample budgets in
   `policy_sample_budgets`, sealed samples in `policy-inputs`;
   `policyEvaluationSample`, `policySearchEvaluationRecords`,
   `policySearchEvaluationRecordSuggestions`, `policyInputCaptures`,
   `policyInputCapture`. Test: a sealing test that finds no input value in
   the object store.
4. `policySimulatev2` with no receipt and no sample; `policyTemplate` and
   `searchPolicyTemplates`, `searchPolicyTemplatesSuggestions` from the MIT
   example library. Test: `TestUnitPolicyLibrary` runs its 67 templates and
   61 tests.

**Exit criteria**
- The Policies tag (less its Intent attachments) meets decision 0016's
  evidence rule.
- A member whom one account's login policy admits and another's refuses
  enters only the first; a member no account admits gets a session that
  lists no account.
- Captures C47, C48, C52, C53 and C54 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P14: Public worker pool, Kubernetes workers and signed runs

Decisions: [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0009](../research/decisions/0009-policy-engine-opa.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0016](../research/decisions/0016-testing-and-acceptance.md).

**Depends on:** P13.

The optional operator-run public worker pool, Kubernetes workers on the same
protocol, and the worker-side initialization policy that signed runs rest on.

**Tasks**
1. Public worker pool in `internal/workers`, on when
   `publicWorkerPool.enabled` is set: an installation-level pool token from
   the operator API, the `public-worker-slots` limit, pooled across the
   organization's accounts and applied in the claim so a run beyond it
   waits, the image registry allowlist, no
   priorities and no scheduled or drift runs. Records DV-078. Operations:
   `publicWorkerPool`, `workerUsage`, `defaultPublicWorkerPoolRunnerImage`,
   `outgoingIPAddresses`, `workerPoolSearchUsers`,
   `workerPoolSearchUsersSuggestions`, and `workerPoolSearchSchedulableRuns`
   and `workerPoolSearchSchedulableRunsSuggestions`, which `runs`' partial
   serves, since `workers` imports no `runs` package. Feature: `workers_public_pool.feature`.
2. `internal/agent/executor/kubernetes`: a controller that creates a pod per
   run and acts as a launcher, with `WorkerPool` and `Worker` resources.
   Feature: `workers_kubernetes.feature` (`@kubernetes`, k3s).
3. `internal/agent/initpolicy`: the worker-side initialization policy
   compiled at start over `internal/platform/rego`, with the `exec`
   built-in in a short-lived container of the run's image; a `deny` fails
   the run from `PREPARING`. Feature: `runs_signed.feature` with a
   signed-run JWT, and `policy_worker_initialization.feature`.
4. `availableAt` for autoscalers.

**Exit criteria**
- The Worker Pools operations of task 1 meet decision 0016's evidence rule.
- With billing on, a run of an organization at its `public-worker-slots`
  waits on the public worker pool, in whichever of its accounts it starts,
  while another organization's run proceeds.
- Captures C21, C66, C67, C68 and C69 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P15: Cloud integrations and OIDC federation

Decisions: [0006](../research/decisions/0006-authentication-and-tokens.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md).

**Depends on:** P9.

AWS, Azure and GCP credentials for runs, the OIDC federation token in every
run and the discovery document cloud providers verify it against.

**Tasks**
1. Federation: the `federation` signing key, `GET
   /.well-known/openid-configuration` and `GET /.well-known/jwks`; the token
   with report 12's claims and scope table, injected as
   `INFRAWEAVE_OIDC_TOKEN` and `/mnt/workspace/infraweave.oidc` and refreshed
   in long runs; the subject template with `accountSetOIDCSubjectTemplate`
   and its validation. Tests: golden claims and placeholders. Feature:
   `oidc_federation.feature` (a Vault container's `jwt` auth accepts the
   token with `bound_claims` on `spacePath`). Records DV-016.
2. `internal/cloud` (flat domain): AWS integrations and attachments with the
   fixed external ID and `RoleSessionName` formats, the role trust check
   with decision 0022's matched text, and credentials either assumed by the
   server and handed over through `cloudapi.Credentials` or, when the
   integration says so, assumed on the worker by `internal/agent/cloudauth`
   with the run's OIDC token. Operations: `awsIntegrations`, `awsIntegration`,
   `awsIntegrationByName`, `awsIntegrationCreate`, `awsIntegrationUpdate`,
   `awsIntegrationDelete`, `awsIntegrationAttach`, `awsIntegrationDetach`,
   `awsIntegrationAttachmentUpdate`, `awsIntegrationAttachedStack`,
   `awsIntegrationExternalIdForStack`, `awsIntegrationsEnabled`,
   `attachableAwsIntegrationsInSpace`, `stackAttachableAwsIntegrations`,
   `installationAwsAccountId` (the reference's `spaceliftAwsAccountId`);
   the legacy
   `stackIntegrationAwsCreate` and `stackIntegrationAwsDelete` served and
   deprecated. STS fake in `internal/cloud/internal/aws/awsfake`.
3. Wiring ([domain wiring](#adding-a-domain)): `cloud`'s
   graph rule and shared-rule entries, its place after `state` in
   `domains.go` and the migration order, and its edges to `features`,
   `access` and `stacks`; attachments hold their stacks through
   `stacksapi`, with a stack guard and a purge.
   `internal/app` binds the `cloudapi.Credentials` back edge after
   construction for `runs` to read at handover
   ([declared graph](repo-layout.md#declared-graph)).
4. Azure and GCP: `azureIntegrations`, `azureIntegration`,
   `azureIntegrationByName`, `azureIntegrationCreate`,
   `azureIntegrationUpdate`, `azureIntegrationDelete`,
   `azureIntegrationAttach`, `azureIntegrationDetach`,
   `azureIntegrationAttachmentUpdate`, `azureIntegrationAttachedStack`,
   `azureIntegrationsEnabled`, `attachableAzureIntegrationsInSpace`,
   `stackAttachableAzureIntegrations` (the module reads land in P17), with
   the Entra applications created through the operator-registered
   application in `cloud.azure`, or `installation-unsupported` without one;
   GCP through the OIDC token only. `integrationsCount`.
5. Feature: `cloud_integrations.feature` (`@postgres`, `@tofu`, `@oidc`) with
   read and write attachments and the external ID suffix per run type.

**Exit criteria**
- Cloud Integrations (less its Intent attachments) meets decision 0016's
  evidence rule.
- Captures C81 to C96 replay once recorded; until then the scenarios assert
  decision 0006's formats.
- `make gate` passes at 95% in total and 90% per package.

## P16: Scheduling, drift detection and stack dependencies

Decisions: [0011](../research/decisions/0011-persistent-work-queue.md), [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0022](../research/decisions/0022-api-errors-and-update-semantics.md).

**Depends on:** P10, P2c.

Scheduled runs, tasks and deletes, drift detection, and stack dependencies
with output references and external dependencies.

**Tasks**
1. `internal/schedules` (flat domain): `scheduled_jobs` with `next_fire_at`
   claimed by the cron ticker; missed firings coalesced. Operations:
   `stackScheduledRun`, `stackScheduledRunCreate`,
   `stackScheduledRunUpdate`, `stackScheduledRunDelete`,
   `stackScheduledTask`, `stackScheduledTaskCreate`,
   `stackScheduledTaskUpdate`, `stackScheduledTaskDelete`,
   `stackScheduledDelete`, `stackScheduledDeleteCreate`,
   `stackScheduledDeleteUpdate`, `stackScheduledDeleteDelete`. Records
   DV-090.
2. Wiring ([domain wiring](#adding-a-domain)):
   `schedules`' graph rule and shared-rule entries, its place after `vcs`
   in `domains.go` and the migration order, and its edges to `features`,
   `access`, `stacks` and `runs`, holding their stacks through `stacksapi`
   with a stack guard and a purge; scheduled
   and drift runs start through `runsapi.Runs`
   ([declared graph](repo-layout.md#declared-graph)).
3. Drift detection: `drift_detection` rows, drift runs at priority levels 6 and 7 on
   private pools, each pool's drift run limits enforced in the claim,
   reconcile. Operations:
   `stackIntegrationDriftDetectionCreate`,
   `stackIntegrationDriftDetectionUpdate`,
   `stackIntegrationDriftDetectionDelete`.
4. `internal/stacks/internal/dependency`: the dependency DAG with cycle
   rejection and references to outputs. Operations: `stackDependency`,
   `stackDependencyCreate`, `stackDependencyUpdate`,
   `stackDependencyDelete`, `stackDependenciesCreateInBatch`,
   `stackDependenciesAddReference`, `stackDependenciesAddReferences`,
   `stackDependenciesUpdateReference`, `stackDependenciesUpdateReferences`,
   `stackDependenciesDeleteReference`,
   `stackDependenciesDeleteReferences`. An output reference may carry an
   expression over `value` and `outputs`, evaluated in `serve` at the
   downstream run's handover, which reads the upstream outputs through
   `stateapi.Versions`, a back edge that `internal/app` sets after
   construction (`SetVersions`); its scenario joins
   `expressions_deploy.feature`. Records DV-107.
5. `internal/runs/internal/core/dependencies`: dependent runs, skipping on
   unchanged outputs, `SKIPPED` on upstream failure; external dependencies
   with `runExternalDependencyMarkAsCompleted`.
6. Features: `scheduler_scheduled_runs.feature`,
   `scheduler_missed_firings.feature`, `drift_detection.feature`,
   `stacks_dependencies.feature`.

**Exit criteria**
- Drift Detection and the schedule and dependency operations of the Stacks
  tag meet decision 0016's evidence rule.
- Two instances fire each scheduled time once.
- Captures C151 and C153 to C168 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P17: Module registry

Decisions: [0014](../research/decisions/0014-module-and-provider-registry.md), [0012](../research/decisions/0012-vcs-integration-model.md), [0006](../research/decisions/0006-authentication-and-tokens.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0009](../research/decisions/0009-policy-engine-opa.md).

**Depends on:** P13, P15.

Private modules from VCS, versioned and tested by runs, served through the
Terraform module registry protocol and `terraform login`.

**Tasks**
1. `internal/registry` (hexagon) with modules, versions and test cases;
   archives built once per version into `modules`. Operations: `modules`,
   `module`, `moduleCreate`, `moduleUpdateV2`, `moduleDelete`,
   `moduleDisable`, `moduleEnable`, `modulePublish`, `moduleRun`,
   `moduleRunsInRange`, `moduleVersion`, `moduleVersionFind`,
   `moduleVersions`, `moduleVersionsCount`, `moduleSearchModuleVersions`,
   `moduleSearchModuleVersionsSuggestions`, `versionCreate`,
   `versionDelete`, `versionAnnotate`, `versionTrigger`, `versionYank`,
   `versionProposeLocalWorkspace`, `versionPush`, `searchModules`,
   `searchModulesSuggestions`, `moduleConfigElement`,
   `moduleAttachableContexts`, `moduleAttachablePolicies`,
   `moduleAttachableVcsIntegrations`, `moduleAttachableWorkerPools`,
   `modulePolicyAttachment`, `moduleAttachableAwsIntegrations`,
   `moduleAttachableAzureIntegrations`.
2. Wiring ([domain wiring](#adding-a-domain)): `registry`'s
   graph rule and shared-rule entries, `core-not-adapters` included, its
   place after `cloud` in `domains.go` and the migration order, and its
   edges to `features`, `access`, `workers`, `configuration`, `policies`,
   `runs` and `cloud`. A module's configuration elements and its context,
   policy and cloud integration attachments are `registry`'s rows, with
   foreign keys to `modules` and holds on the context, policy or
   integration, because `registry` imports those domains; a module holds
   its VCS integration's ID as a plain column, as a stack does, and `vcs`
   serves `moduleAttachableVcsIntegrations` over `registryapi`. The waiting
   `vcs`→`registry` edge lands here too: `vcs` routes tag pushes to
   `registryapi.Modules` once the push policy sets the version
   ([declared graph](repo-layout.md#declared-graph)).
3. Sharing: `moduleShare`, `sharedModulesV2`, reads through shared spaces.
   A share is the one exception to account isolation, and only between
   accounts of the same organization: a share naming an account of another
   organization answers `422 validation-failed`. It exposes the module's
   fields and version downloads only, needs `module-sharing` in both
   accounts' entitlement, and its revocation hides the module while runs in
   flight finish. Records DV-112.
4. Protocol in `internal/registry/internal/adapters/protocol`:
   `/.well-known/terraform.json`, `/registry/modules/v1/...` versions and
   download with signed `X-Terraform-Get`, lenient version segments, the
   documented answers for unauthenticated callers, as hand-written chi
   routes ([0004](../research/decisions/0004-repository-architecture.md))
   described by `api/src/registry/` bundled to `api/registry.openapi.yaml`
   for the contract tests ([0014](../research/decisions/0014-module-and-provider-registry.md));
   `internal/app/discovery.go` assembles the discovery document from each
   domain's contribution. `login.v1` (`/terraform-login`,
   `POST /terraform/token`) in `access`. Tests:
   `TestUnitVersionNumberRules`, `TestUnitVersionSegmentParse`. Records
   DV-052, DV-054, DV-055.
5. Test cases as `TESTING` runs created through `runsapi`, which carry the
   module's configuration elements, since `runs` cannot read `registry`;
   negative cases included.
6. Features: `registry_protocol.feature`, `registry_modules.feature`
   (`@vcs`, `@terraform`, `@tofu`).

**Exit criteria**
- Modules meets decision 0016's evidence rule; `terraform init` and
  `tofu init` resolve a module through an API key token.
- A module shared with another account of the organization resolves there,
  a share to an account of another organization answers
  `422 validation-failed`, and a revoked share stops new downloads.
- Captures C129 to C134 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P17b: Provider registry and GPG keys

Decisions: [0014](../research/decisions/0014-module-and-provider-registry.md).

**Depends on:** P17.

Private Terraform providers published from GoReleaser-shaped archives and
verified with registered GPG keys.

**Tasks**
1. Providers and versions in `internal/registry/internal/core/provider`:
   `terraformProviders`, `terraformProvider`, `terraformProviderCreate`,
   `terraformProviderUpdate`, `terraformProviderDelete`,
   `terraformProviderSetVisibility`, `terraformProviderVersion`,
   `terraformProviderVersionCreate`, `terraformProviderVersionUpdate`,
   `terraformProviderVersionDelete`,
   `terraformProviderVersionRegisterPlatformV2`,
   `terraformProviderVersionPublish`, `terraformProviderVersionRevoke`,
   `terraformProviderVersionUploadDocs`, `terraformProviderVersionVerify`,
   `providers`, `providersInSpace`. Records DV-053.
2. GPG keys: `gpgKeys`, `gpgKeyCreate`, `gpgKeyUpdate`, `gpgKeyRevoke`.
   Tests: `TestUnitVerifyProviderVersion`,
   `TestUnitGPGKeyRejectsPrivateKey`.
3. Provider protocol: `/registry/providers/v1/...` versions and download
   metadata, public providers without credentials, and
   `/registry/files/{token}` in `proxy` mode.
4. Feature: `registry_providers.feature` (`terraform init` installs a
   provider signed by a test key; revoking removes it).

**Exit criteria**
- Terraform Providers and GPG Keys meet decision 0016's evidence rule.
- Captures C135 to C138 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P18: Notifications, webhooks, Slack and event streams

Decisions: [0015](../research/decisions/0015-events-notifications-webhooks-audit.md), [0009](../research/decisions/0009-policy-engine-opa.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0011](../research/decisions/0011-persistent-work-queue.md).

**Depends on:** P13.

The fan-out of every event to the inbox, notification policy routes,
webhooks, Slack and pull request comments, and the server-sent-event
streams that observers read. `internal/events` holds these sinks and the
audit trail's search and shipper (P18b); every domain writes its audit
entries through `internal/platform/audit` in the change's own transaction
([0015](../research/decisions/0015-events-notifications-webhooks-audit.md)).
The Flows engine (P18c) adds its own consumer of the same fan-out.

**Tasks**
1. `internal/events` (hexagon): `events.fanout` and `events.deliver` with
   `event_deliveries` keyed by event, sink, target and rule; notification
   policy selection by space; the `run_updated`, `module_version`,
   `internal_error` and `worker_usage` kinds, the last computed from P8's
   `worker_pool_samples` through `workers/api`. Tests:
   `TestUnitPolicySelection`, `TestUnitGroupingKey`,
   `TestUnitNotificationInputConformsToContract`.
2. Wiring ([domain wiring](#adding-a-domain)): `events`'
   graph rule and shared-rule entries, `core-not-adapters` included, its
   place after `schedules` in `domains.go` and the migration order, and its
   edges to `features`, `access`, `workers`, `stacks`, `runs`, `policies`
   and `vcs`, stack webhooks holding their stacks through `stacksapi`;
   `worker_usage` reads `worker_pool_samples` through `workersapi`
   ([declared graph](repo-layout.md#declared-graph)).
3. HTTP delivery: signatures, secret headers, rule headers, timeouts, no
   redirects, retries with `retryOnFailure`, auto-disable after ten
   failures, the egress guard, delivery bodies in `deliveries`. Operations:
   `namedWebhooksIntegrationCreate`, `namedWebhooksIntegration`,
   `namedWebhooksIntegrationUpdate`, `namedWebhooksIntegrationDelete`,
   `namedWebhooksIntegrationSetHeaders`,
   `namedWebhooksIntegrationDeleteHeaders`,
   `namedWebhooksIntegrationDeliveries`,
   `namedWebhooksIntegrationDeliveryDetails`,
   `searchNamedWebhooksIntegrations`,
   `searchNamedWebhooksIntegrationsSuggestions`, and per-stack
   `webhooksIntegrationCreate`, `webhooksIntegrationUpdate`,
   `webhooksIntegrationDelete`. Tests: `TestUnitSignature`,
   `TestUnitHeaderPrecedence`, `TestUnitRetrySchedule`,
   `TestUnitAutoDisable`. The SHA-1 header is left out with
   `events.webhooks.fips: true` and under `fips140=only`. Records DV-056
   to DV-058.
4. Inbox: `searchNotifications`, `searchNotificationsSuggestions`,
   `notificationCount`, `dismissNotifications`,
   `dismissNotificationGroup`, `dismissAllNotifications`.
5. Slack and Teams: `slackAppConfig`, `slackAppConfigSet`,
   `slackAppConfigDelete`, `slackAppManifest`, `slackIntegration`,
   `slackOauthRedirect`, message editing per run with
   `last_state_version`; Slack chi fake. Slack channel access rules on
   managed user groups: `managedUserGroupIntegrations`,
   `managedUserGroupIntegration`, `managedUserGroupIntegrationCreate`,
   `managedUserGroupIntegrationUpdate`,
   `managedUserGroupIntegrationDelete`. Pull request comments from
   notification policy `pull_request` rules through P12b's `Comments`.
6. Notification text: every string a notification policy returns, and
   every default message, is a text template over `event` and
   `logs(phase)` ([0024](../research/decisions/0024-expression-language.md)), rendered in the delivery job. Tests:
   `TestUnitMessageTemplates`. The option `notification-policies` gates
   policy `type: NOTIFICATION`. Records DV-097.
7. Streams ([0015](../research/decisions/0015-events-notifications-webhooks-audit.md)): run logs, run state and worker pool queue
   streams with `Last-Event-ID` resume, `reset` past retention, the
   per-identity limit and `end` on shutdown, woken by `infraweave_events`
   and `infraweave_logs`; polling stays. Records DV-095.
8. Features: `events_webhooks.feature`, `events_notifications.feature`,
   `events_slack.feature`, `events_streams.feature`, and the notification
   scenario of `expressions_deploy.feature`.

**Exit criteria**
- Notifications, Webhooks, Slack, OAuth and the group integrations of
  Managed Users meet decision 0016's evidence rule.
- A delivery survives a node stopped mid-delivery and arrives at least once
  with a stable `X-Infraweave-Delivery`.
- Captures C170 to C179 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P18b: Audit trail and observability

Decisions: [0015](../research/decisions/0015-events-notifications-webhooks-audit.md), [0017](../research/decisions/0017-observability-and-health.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md).

**Depends on:** P18.

The audit trail with its ordered webhook, usage reporting from infraweave's
own records, and the metrics and traces operators run infraweave with.

**Tasks**
1. Audit: `x-infraweave-audit-action` on every mutating operation and an
   `internal/platform/audit` `Record` call in each, in the change's own
   transaction, write-only fields as `SecretSHA` under
   `DeriveKey("audit-secret-sha")`; the trail's search, retention and
   ordered shipper in `internal/events/internal/core/audit`; the
   organization's trail in `organization_audit_trail_entries`, keyed by
   organization
   ([0015](../research/decisions/0015-events-notifications-webhooks-audit.md)).
   Operations: `searchAuditTrailEntries`, `searchAuditTrailEntriesSuggestions`,
   `auditTrailSetWebhook` (with `webhook-verification-failed`),
   `auditTrailWebhook`, `auditTrailDeleteWebhook`,
   `auditTrailWebhookSetHeaders`, `auditTrailWebhookDeleteHeaders`,
   `auditTrailRetentionDays`. Test: `TestUnitAuditActionsCoverMutations`.
   Feature: `events_audit.feature` (ordered shipping across a receiver that
   fails three times).
2. Usage: `usage` and the usage CSV from a trailing 30-day window of run
   records and P8's `worker_pool_samples`; `runMinutesUsage`; the
   `worker_usage` event at 60% of the configured limit. The `Organization`
   tag's usage gains the same figures per account, so organization admins
   see run and worker usage across accounts without any account's data.
   Records DV-059.
3. Metrics in `internal/platform/observe`: the OpenTelemetry metric API,
   `/metrics` on the admin listener, infraweave's own instruments, among
   them `integrity.orphans`' counts per held column as a gauge, and
   `/metrics/accounts` with the exporter's series under the `infraweave_`
   prefix and with `account` and `organization` labels.
   Tests: `TestUnitAccountCollectors`, `TestUnitAccountMetricNames`.
   Feature: `observability_metrics.feature`. Records DV-060, DV-061.
4. Traces: OTLP export, sampling, spans for HTTP, pgx, object storage,
   jobs and policy evaluations; trace context carried into runs and the
   launcher's phase spans. Feature: `observability_tracing.feature`.
   Records DV-063, DV-064.

**Exit criteria**
- Audit Trail meets decision 0016's evidence rule, and every mutating
  operation in `api/api.openapi.yaml` carries an audit action.
- One trace ID spans a run's API request, its job, the launcher's phases and
  the runner's environment.
- Captures C180 to C184 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P18c: Flows engine and API

Decisions: [0018](../research/decisions/0018-flows-runtime.md), [0015](../research/decisions/0015-events-notifications-webhooks-audit.md), [0024](../research/decisions/0024-expression-language.md), [0011](../research/decisions/0011-persistent-work-queue.md), [0005](../research/decisions/0005-persistence-postgresql.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md).

**Depends on:** P18, P4b, P10, P1d.

Flows, built on Deploy's events and belonging to the account: projects,
flows, blocks, events, invocations and timers on the work queue, with
Deploy events reaching trigger blocks through the fan-out of P18 and every
expression in CEL.

**Tasks**
1. `internal/flows` (hexagon) with the `flows` PostgreSQL schema and its
   own migration stream, UUIDv7 identifiers; Flows belongs to the account,
   which takes the place of Spacelift Flows' organization, so projects,
   apps and agent pools are the account's; `flows_memberships`, a personal
   sandbox per member on
   first request, sealed `flows_secrets`, named expressions, data tables
   and KV with locks; event bodies above 256 KiB in object storage under
   `flows/events/`. Records DV-065.
2. Wiring ([domain wiring](#adding-a-domain)): `flows`'
   graph rule and shared-rule entries, `core-not-adapters` included, its
   place last before `expressions` in `domains.go` and the migration order,
   and its edges to `features`, `access`, `stacks` and `runs`, with
   `flows_projects.account_id` referencing `access`' accounts. The
   `templates` edge lands here if P21 has landed, and with P21 otherwise
   ([declared graph](repo-layout.md#declared-graph)).
3. The Flows reference, `api/reference/spacelift-flows/`, comes from
   [P1d](#p1d-flows-specification-reference); the Flows families are
   scaffolded from its tags.
4. The Flows operations in the served document: `api/src/api/` fragments
   under `/api/v1/flows/...`, tagged `Flows Projects`, `Flows Apps`,
   `Flows Data Tables` and the other `Flows `-prefixed tags, bundled into
   `api/api.openapi.yaml` and served through `internal/platform/gen/api`
   and `pkg/client/api`, which `pkg/sdk` wraps; the operations of report 23
   and the Flows reference, each carrying the feature
   (`flows-engine`, `flows-data-tables`, `flows-forms` or
   `flows-mcp-tools`) that gates it. The operations no public client
   reaches (invocations and event trees, retry and re-emit, members, teams,
   service accounts and API keys, agent pools, registry subscriptions,
   OIDC, audit and OTLP export) are designed from the docs and decision
   0018's data model and carry `x-upstream: none`. The four project roles,
   and the account admin in place of Spacelift Flows' organization admin,
   are enforced in the handlers. Each operation that lands leaves
   `api/parity-baseline.flows.json` in the same pull request, so
   `TestUnitFlowsParity` holds the baseline to what is still missing; the
   five test-scenario operations stay in it until PD-16 is decided.
   Records DV-069.
5. The engine on the `flows` logical queue: `flows.deliver`, `flows.timer`,
   `flows.lifecycle` and `flows.invocation.status` jobs, effects committed
   with the delivery, schedules through the cron ticker, limits and
   retention. Tests: `TestUnitFlowsDelivery`,
   `TestUnitFlowsInvocationStatus`, `TestUnitFlowsLimits`,
   `TestUnitFlowsDefinitionParse`.
6. One event model: a fan-out consumer inside `internal/flows` treats
   every enabled Deploy trigger block whose filter matches as a target and
   inserts the root `flow_events` row in the fan-out's transaction, so
   `events` never imports `flows`; `flows.event` feeds the
   `GET /api/v1/flows/flows/{flowId}/events/stream` stream and the audit trail
   under the `flows.` prefix. Input and static configuration, output
   customization and Transform are CEL, checked at save with the
   expression endpoints of P6. Feature: `expressions_flows.feature`.
   Records DV-096 and DV-103.
7. Core blocks in Go in the `scheduler` process mode, with Transform's
   steps form and no Script block; HTTP Endpoint blocks, forms at
   `/flows/forms/<block>`, and the flow and platform Flows MCP servers at
   `/flows/mcp/<flow>` and `/flows/mcp` (DV-066), authorized by P3b's
   OAuth authorization server. Deploy-side reads:
   `flowsIntegration`, `flowsProjects`, `flowsInstances`,
   `flowsProjectRoleBinding`, `flowsProjectRoleBindingBatchCreate`,
   `flowsProjectRoleBindingDelete`.
8. Feature: `flows_engine.feature` (`@postgres`, `@flows-agent`: fan-out
   and merge, echo, pending events, Sleep across a restart, manual retry
   and re-emit, limits, retention).

**Exit criteria**
- The Deploy-side `Flows` tag and every `Flows `-prefixed tag of
  `api/api.openapi.yaml` meet decision 0016's evidence rule, and every
  Flows operation the engine serves passes its scenarios.
- `TestUnitFlowsParity` passes, and every reference operation outside
  `api/parity-baseline.flows.json` is served.
- A Deploy `run_updated` event and the Flows event it starts share one
  envelope, and the flow's event stream shows it.
- `flows-engine` in `features.disabled` answers `403 feature-disabled` on
  every Flows route and pauses the flows' schedules and timers without
  deleting them.
- Captures C228, C230, C240 and C241 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P18d: Flows apps, agents and Deploy blocks

Decisions: [0018](../research/decisions/0018-flows-runtime.md), [0020](../research/decisions/0020-reference-deployments-and-packaging.md), [0016](../research/decisions/0016-testing-and-acceptance.md), [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0013](../research/decisions/0013-secrets-at-rest.md).

**Depends on:** P18c, P21.

App blocks run in containers on Flows agents, apps are OCI images built on
the Go app SDK, and Deploy blocks act on runs, templates and blueprints.

**Tasks**
1. The `App` and `Host` gRPC services in `api/proto/flows/app/v1/`,
   generated into `pkg/flows/app/v1`, with `buf breaking` against the last
   release in CI (`TestUnitAppContractCompatible`); `pkg/flows/appsdk` with
   `appsdk.Serve`, schemas from Go types, KV, locks, timers, messages and
   widgets, and `appsdk/apptest`.
2. The agent gateway in `internal/flows/internal/adapters/gateway`:
   WebSocket at `wss://<host>/flows/agent/v1/connect` on every `serve`
   replica, `flows.agent_task` jobs partitioned by pool and claimed up to
   each agent's capacity, the one queue `serve` consumes
   ([0004](../research/decisions/0004-repository-architecture.md#process-topology-one-binary-several-process-modes));
   agent pools with SHA-256 hashed tokens, rotation and the default pool.
   `internal/agent/flowsagent`, which calls apps through `pkg/flows/app/v1`,
   the one package outside `internal/` on the agent's allow list
   ([0004](../research/decisions/0004-repository-architecture.md)), and
   `infraweave flows-agent` with the `docker` (gVisor when configured),
   `kubernetes` and `process` executors and their isolation rules. Feature:
   `flows_agents.feature` (two `serve` replicas, an agent killed mid-task,
   token rotation, a container never reused for another installation).
3. Apps: an app version is an OCI image plus a manifest captured at
   publish; registries fetched hourly, with the digest checked by the agent;
   custom apps from image references, and a JavaScript bundle refused with
   `422 validation-failed`; declarative widgets validated against their
   type's schema. The `flows-apps` and `flows-agents` features gate them.
   Feature: `flows_apps.feature`, with a fixture app built from the SDK and
   a registry served by a chi test server. Records DV-098 and DV-099.
4. Deploy blocks: trigger blocks on decision 0015's kinds, Trigger Run,
   Trigger Task, Confirm Run, Review Run, Deploy Template, Deploy Blueprint
   and the API Request kind that calls a REST operation by `operationId`
   through `internal/platform/httpkit/dispatch`, which `internal/app` binds
   to the composed router after construction, so the call passes the
   middleware, authorization and feature gate of an external request
   ([0004](../research/decisions/0004-repository-architecture.md)); each
   block acts with the Deploy role bindings of its Flows project. Feature:
   `flows_deploy.feature` (`@tofu`). Records DV-067 and DV-068.

**Exit criteria**
- A Run Events block reacts to a real run's state change and to a drift
  result, and a Trigger Run block starts another run.
- A fixture app built on `pkg/flows/appsdk` passes `apptest` and runs on a
  Flows agent under the `docker` and `kubernetes` executors.
- Captures C229, C231 to C237, C239 and C242 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P19b: VCS agent pools and other source providers

Decisions: [0012](../research/decisions/0012-vcs-integration-model.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0013](../research/decisions/0013-secrets-at-rest.md).

**Depends on:** P12b.

VCS systems the internet cannot reach, through `infraweave vcs-agent`, and the
`BLOB`, `ORIGIN` and `INFRAWEAVE` source providers.

**Tasks**
1. `internal/vcs/internal/adapters/agentgateway`: `vcs_agent_pools`,
   connections and the `UNLOGGED` `vcs_agent_requests`, the long-poll
   `POST /vcs-agent/v1/poll` and `/responses/{id}`, routing across replicas
   on `infraweave_vcs_agent`, `private://` addressing. Operations:
   `vcsAgentPools`, `vcsAgentPool`, `vcsAgentPoolCreate`,
   `vcsAgentPoolUpdate`, `vcsAgentPoolDelete`, `vcsAgentPoolReset`.
2. `internal/kernel/vcsroutes`, each provider's API route table, which
   `vcs` and the agent share; `internal/agent/vcsagent` and
   `infraweave vcs-agent` with the allowlist compiled from it,
   `--validation` and `--allowed-projects`. Test: `allowlist_test.go`.
   Feature: `vcs_agent.feature`. Records DV-045, DV-046.
3. `BLOB` provider: `blobStorageIntegrationCreate`,
   `blobStorageIntegration`, `blobStorageIntegrationUpdate`,
   `blobStorageIntegrationDelete`, with `x-infraweave-basis: schema-only`.
   `ORIGIN` provider from `vcs.origin`: `originIntegration`,
   `originIntegrationUpdate`, `originIntegrationDelete`,
   `originIntegrationInstallURL`.
4. The Repos tag, the `INFRAWEAVE` source provider (the reference's
   `SPACELIFT`), stored in object storage:
   `repos`, `repo`, `repoCreate`, `repoUpdate`, `repoDelete`, `repoCommit`,
   `repoCommitCreate`, `repoCommits`, `searchRepoCommits`,
   `searchRepoCommitsSuggestions`, `repoFileHistory`, `searchRepoFiles`,
   `searchRepoFilesSuggestions`, `searchRepos`, `searchReposSuggestions`.

**Exit criteria**
- VCS Agent Pools, Blob Storage, Origin Integration and Repos meet decision
  0016's evidence rule.
- A request outside the allowlist is refused on every provider, Azure DevOps
  included.
- Captures C11, C12, C77 and C78 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P20: Other vendors

Decisions: [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0004](../research/decisions/0004-repository-architecture.md), [0010](../research/decisions/0010-state-backend.md).

**Depends on:** P11.

Terragrunt, Pulumi, CloudFormation and Kubernetes, each an `iac` package and an
agent command set behind the same contract.

**Tasks**
1. `internal/iac/terragrunt` and `internal/agent/vendors/terragrunt`:
   run-all, units with workspaces in the state backend, `terragruntVersions`,
   `terragruntEffectiveVersion`. Feature: `vendors_terragrunt.feature`.
2. `internal/iac/pulumi` and the agent command set, following option 5's
   walkthrough. Feature: `vendors_pulumi.feature`.
3. `internal/iac/cloudformation` (CDK, SAM, Serverless) and
   `internal/iac/kubernetes` (Helm, Kustomize, `kubectlVersions`). Features:
   `vendors_cloudformation.feature`, `vendors_kubernetes.feature`
   (`@kubernetes`).
4. Every vendor passes `iactest`; runner images for each in
   `deploy/images/`; plan sanitization per vendor. Each vendor is an option
   on its `vendorConfig` member: `vendor-terragrunt`, `vendor-pulumi`,
   `vendor-cloudformation` and `vendor-kubernetes`.

**Exit criteria**
- Terragrunt, Pulumi, CloudFormation and Kubernetes each run a tracked run
  to `FINISHED` in acceptance.
- Captures C185, C188, C189, C190, C192 and C193 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P20b: Ansible, OpenTofu migration and plugins

Decisions: [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md).

**Depends on:** P20.

Ansible with its inventory views, the OpenTofu migration tooling, and plugins
that run inside runs.

**Tasks**
1. `internal/iac/ansible` and its agent command set, the option
   `vendor-ansible`; inventory records.
   Operations: `ansibleHosts`, `ansibleHost`,
   `ansibleHostSearchAnsibleTaskExecutions`,
   `ansibleHostSearchAnsibleTaskExecutionsSuggestions`,
   `searchAnsibleHosts`, `searchAnsibleHostsSuggestions`, `ansibleTasks`,
   `searchAnsibleTasks`, `searchAnsibleTasksSuggestions`,
   `hasAnsibleStacks`.
2. OpenTofu migration: `openTofuFeatures`, `openTofuMigrateSearch`,
   `openTofuMigrateSuggestions`, `openTofuMigrationCounts`,
   `openTofuMigrationQueueSearch`, `openTofuMigrationQueueSearchSuggestions`,
   `openTofuMigrate`, `openTofuMigrationQueueClean`, `stackMigrateVendor`,
   `stackMigrateVendorAll`.
3. Plugins with templates from `spacelift-io/plugins` at a pinned commit
   plus `plugins.catalog_paths`: `pluginTemplates`, `pluginTemplate`,
   `pluginTemplateCreate`, `pluginTemplateDelete`, `plugins`, `plugin`,
   `pluginInstall`, `pluginUpdate`, `pluginDelete`,
   `runPluginLogsDownloadURL`. Records DV-081.
4. Features: `vendors_ansible.feature`, `vendors_opentofu_migration.feature`,
   `vendors_plugins.feature`.

**Exit criteria**
- Ansible, OpenTofu Migration and Plugins meet decision 0016's evidence rule.
- Captures C186, C187, C191, C199 and C252 to C256 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P21: Blueprints, templates and external integrations

Decisions: [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0016](../research/decisions/0016-testing-and-acceptance.md), [0024](../research/decisions/0024-expression-language.md).

**Depends on:** P16, P12b.

Single-body blueprints, templates with versions and deployments, and the
ServiceNow and Backstage integrations that attach to them.

**Tasks**
1. `internal/templates` (flat domain), whose bodies are text templates of
   `internal/platform/expr` ([0024](../research/decisions/0024-expression-language.md)), with time and randomness fixed per rendered
   change and stored with it (DV-106). Blueprints: `blueprint`,
   `blueprintCreate`, `blueprintUpdate`, `blueprintDelete`, `blueprintClone`,
   `blueprintCreateStack`, `blueprintParseTemplate`, `blueprintSchema`,
   `templateSchema`, `blueprintWithGroupCreate`,
   `blueprintWithGroupUpdate`, `blueprintWithGroupDelete`,
   `searchBlueprints`, `searchBlueprintsSuggestions`,
   `blueprintAttachToIntegration`, `blueprintDetachIntegration`.
2. Wiring ([domain wiring](#adding-a-domain)):
   `templates`' graph rule and shared-rule entries, its place after
   `events` in `domains.go` and the migration order, and its edges to
   `features`, `access`, `stacks` and `runs`, deployments holding the
   stacks they create through `stacksapi`. The `flows`→`templates` edge of
   P18d's Deploy Template and Deploy Blueprint blocks lands here if P18c
   has landed, and with P18c otherwise
   ([declared graph](repo-layout.md#declared-graph)).
3. Templates: `templateCreate`, `template`, `templateUpdate`,
   `templateDelete`, `templateVersionCreate`, `templateVersionUpdate`,
   `templateVersionDelete`, `templateVersionDeprecate`,
   `templateVersionParseTemplate`, `templateCompatibleVersions`,
   `templateTemplateVersion`, `templateTemplateVersionByNumber`,
   `templateSearchTemplateVersions`,
   `templateSearchTemplateVersionsSuggestions`, `searchTemplates`,
   `searchTemplatesSuggestions`.
4. Deployments that create stacks with dependencies:
   `templateDeploymentCreate`, `templateDeployment`,
   `templateDeploymentUpdate`, `templateDeploymentDelete`,
   `templateDeploymentRollback`, `templateDeploymentSearchChangeHistory`,
   `searchTemplateDeployments`.
5. External integrations: `externalIntegrations`, `externalIntegration`,
   `externalIntegrationCreate`, `externalIntegrationDelete`,
   `attachableExternalIntegrationInSpace`; the Backstage key with its seat
   exemption.
6. Features: `templates_blueprints.feature`, `templates_versions.feature`,
   `templates_deployments.feature`, `external_integrations.feature`.

**Exit criteria**
- Blueprints, Templates and External Integrations meet decision 0016's
  evidence rule.
- Captures C139 to C150, C152, C257 and C258 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P22: Resources, search and console support

Decisions: [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0010](../research/decisions/0010-state-backend.md).

**Depends on:** P20b, P11.

The resources view built from state and plans, the remaining searches and
suggestions, and the operations the console uses, which the `console`
capability gates.

**Tasks**
1. `internal/resources` (flat domain): managed entities indexed from
   `state.indexed` and plan changes. Operations: `searchManagedEntities`,
   `searchManagedEntitiesSuggestions`,
   `searchManagedEntitiesGroupByProviders`,
   `searchManagedEntitiesGroupByStacks`,
   `searchManagedEntitiesGroupByTypes`, `stackEntity`,
   `stackRunResourceState`, `stackSearchManagedEntities`,
   `stackSearchManagedEntitiesSuggestions`,
   `stackSearchManagedEntitiesGroupByProviders`,
   `stackSearchManagedEntitiesGroupByStacks`,
   `stackSearchManagedEntitiesGroupByTypes`, `runSearchEntityChanges`,
   `runSearchEntityChangesSuggestions`, `runResourceCreate`.
2. Wiring ([domain wiring](#adding-a-domain)):
   `resources`' graph rule and shared-rule entries, its place after
   `templates` in `domains.go` and the migration order, and its edges to
   `features`, `access`, `stacks`, `runs`, `state` and `workers`, managed
   entities holding their stacks and runs through those domains' `api/`
   ([declared graph](repo-layout.md#declared-graph)).
3. `metrics` and `metricsRange` (Spaces tag) for the console's overview,
   answering `AccountMetrics` (the reference's `SpaceliftMetrics`). Every
   Search Suggestions operation lands with the search it completes, in
   that search's phase.
4. Saved filters and UI state: `savedFilters`, `savedFilter`,
   `savedFilterCreate`, `savedFilterUpdate`, `savedFilterDelete`,
   `uiConfigGet`, `uiConfigStore`.
5. Forms and user guides: `genericFormsList`, `completeGenericForm`,
   `isGenericFormCompleted`, and the 20 User Guide operations from a guide
   catalogue the operator can extend.
6. Features: `resources_view.feature`, `search_generic.feature`,
   `console_support.feature`.

**Exit criteria**
- Resources, Saved Filters, UI State, Forms and User Guide meet decision
  0016's evidence rule.
- Captures C196 to C211 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P22b: Scans

Decisions: [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0008](../research/decisions/0008-run-execution-and-worker-protocol.md).

**Depends on:** P22, P14.

Scans built to the schema's shape: scan configurations and scan runs on
discovery-type workers, recording discovered resources against managed
entities.

**Tasks**
1. `internal/resources/internal/scans`: scan configurations and runs with
   `x-infraweave-basis: schema-only`. Operations: `scanCreate`, `scan`,
   `scanUpdate`, `scanDelete`, `searchScans`, `searchScansSuggestions`,
   `scanRunTrigger`, `scanRun`, `scanRunCancel`, `scanRunStop`,
   `scanSearchRuns`, `scanSearchRunsSuggestions`, `scanRunSearchFailures`,
   `scanRunSearchFailuresSuggestions`, `scanRunSearchResources`,
   `scanRunSearchResourcesSuggestions`, `scanProviderSchemas`.
2. Discovery-type pools; a scan run reaches its pool through `runsapi`,
   so workers claim it on the same path as every other job and the
   launcher executes it ([declared graph](repo-layout.md#declared-graph)).
3. Feature: `resources_scans.feature`.

**Exit criteria**
- Scans meets decision 0016's evidence rule.
- Captures C194 and C195 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P23: Organization identity providers, SCIM and MFA

Decisions: [0006](../research/decisions/0006-authentication-and-tokens.md), [0007](../research/decisions/0007-authorization-spaces-and-roles.md), [0013](../research/decisions/0013-secrets-at-rest.md).

**Depends on:** P13b, P3b.

Authentication at the organization: its identity providers, provisioning
through SCIM, enforced multi-factor authentication with security keys and
the session lifetime, with explicit linking of one person's identities.
Each account still authorizes for itself (P13b).

**Tasks**
1. `internal/access/internal/adapters/{oidc,saml}` and
   `internal/access/internal/core/sso`: OIDC SSO with PKCE, SP-initiated
   SAML, the GitHub OAuth front end. Identity providers belong to the
   organization, which may hold more than one, in `identity_providers`
   with sealed secrets; the `Organization` tag manages every provider, and
   the SSO tag's operations configure the organization's providers, so
   they need an organization role. Operations: `oidcSettings`,
   `oidcCreate`, `oidcUpdate`, `oidcDelete`, `oidcSubjectTemplate`,
   `samlSettings`, `samlCreate`, `samlUpdate`, `samlDelete`, `hasSSO`,
   `identityFederationUnlink`, `oauthRedirect`, `oauthToken`, `oauthUser`.
   Feature: `auth_sso.feature` (`@oidc`, and a SAML IdP served by the
   test). Records DV-110.
2. IdP groups at login map onto each account's managed user groups (P3b),
   which `infraweave_idp_group_mapping` manages, when the member enters
   that account.
3. Identity linking in the `Organization` tag: two identities from two
   identity providers are two members, and two seats, until an
   organization admin links them. Linking is explicit, needs one sign-in
   through each provider, and merges them into one member ID; nothing is
   linked by e-mail address. Feature:
   `organization_identity_linking.feature`. Records DV-110.
4. `internal/access/internal/core/scim`: SCIM v2 users and groups, which
   provision the organization's members, with filters, `PATCH` and
   reconciliation; `scimSettings`,
   `createOauthClientForSCIM`, `resetOauthClientForSCIM`,
   `deleteOauthClientForSCIM`; deactivation revokes sessions and personal
   keys. Feature: `auth_scim.feature`.
5. `internal/access/internal/core/mfa`: WebAuthn registration and login,
   `enforceMFA`, `accountToggleEnforcingMFA`, `securityKeys`,
   `securityKeyDelete`, `userSecurityKeyDeleteAll`. MFA enforcement and the
   session lifetime are the organization's and apply in every one of its
   accounts. Feature: `auth_mfa.feature`.

**Exit criteria**
- SSO, and the Auth and Sessions and Security Keys operations above, meet
  decision 0016's evidence rule.
- Provision, group assignment, login and deactivation change effective
  access, and deactivation ends every session of the member in every
  account.
- One sign-in through an organization identity provider reaches every
  account whose login policies admit the member, and none other; two
  unlinked identities hold two seats, and linked ones hold one.
- Captures C14, C118, C119, C120 and C121 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P24: infraweave Terraform provider

Decisions: [0016](../research/decisions/0016-testing-and-acceptance.md), [0001](../research/decisions/0001-rest-first-spacelift-surface.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0018](../research/decisions/0018-flows-runtime.md).

**Depends on:** P3 to P23, Flows (P18c and P18d) included, and P25, for the
operations each resource calls.

infraweave's own Terraform and OpenTofu provider, covering Deploy and
Flows, held to `terraform-schema.json` by schema parity and to Spacelift's
own acceptance configurations by behaviour. Its source lives in
`weaveplatform/terraform-provider-infraweave` ([0001](../research/decisions/0001-rest-first-spacelift-surface.md)); where its
checks run is an [open item](#open-items).

**Tasks**
1. The provider over `pkg/sdk`, imported at a pinned version of this
   module, with the Terraform plugin framework and protocol 6: every
   resource and data source record of
   [capabilities.json](../data/capabilities.json) that is not superseded,
   named with the `infraweave_` prefix, with import IDs from report 05, and the Flows resources as
   `infraweave_flows_<name>`. Records DV-002.
2. `specref provider-parity`: compare `terraform providers schema -json`
   with `api/reference/spacelift/terraform-schema.json`;
   `api/reference/provider-parity.yaml` records each rename, type change and
   omission with its reason. CI job `provider-parity`.
3. `test/terraform/<resource>/`: configurations translated from the
   Spacelift provider's acceptance configurations, attributed in `NOTICE`.
   `TestAccProvider` builds the provider into a filesystem mirror and runs
   `apply`, `plan -detailed-exitcode`, `import`, `plan` and `destroy`
   against `infraweave all`, with `@op:` tags naming the operations each
   resource calls.
4. A scenario for each behaviour of report 05 section 6 worth copying
   (eventual consistency, trust relationship retries, write-only values).
5. The provider's half of `flows_clients.feature`: apply, an empty plan
   and import of a flow, held to the `terraform-provider-flows` operations
   that the Flows reference, `api/reference/spacelift-flows/`, maps.

**Exit criteria**
- Schema parity passes with every difference recorded.
- Every resource and data source not superseded is `implemented` in report
  26's terms: its operations are, and its provider scenario passes.
- Capture C18 replays once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P24b: infraweavectl

Decisions: [0001](../research/decisions/0001-rest-first-spacelift-surface.md), [0016](../research/decisions/0016-testing-and-acceptance.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0004](../research/decisions/0004-repository-architecture.md), [0006](../research/decisions/0006-authentication-and-tokens.md), [0018](../research/decisions/0018-flows-runtime.md).

**Depends on:** P24, P25.

`infraweavectl`, infraweave's own CLI, completes over `pkg/sdk`: the
commands of report 06 section 8, the `flows` group, strict profiles and
browser login.

**Tasks**
1. `cmd/infraweavectl`, started in P3b, with cobra and viper over
   `pkg/sdk`; profiles in `$XDG_CONFIG_HOME/infraweave/config.yaml`,
   refusing two credential methods without
   `INFRAWEAVE_API_PREFERRED_METHOD`. Records DV-083.
2. Login: API key, token and the loopback browser flow with
   `cliConfirmationToken`; the `INFRAWEAVECTL` session channel.
3. The command tree of decision 0001: `stack`, `module`, `policy`,
   `provider`, `workerpool`, `blueprint`, `template`, `audit-trail`,
   `run-external-dependency`, `ai`, `profile`, `registry`, `whoami`,
   `version` and `api`, with the subcommands report 06 section 8 lists;
   run log tailing over the run log stream. The `flows` group mirrors
   `flowctl`, publishing an app version from an image reference and
   exporting a flow; its half of `flows_clients.feature`.
4. `infraweavectl mcp`: stdio proxied to `/mcp` with the CLI's credentials
   and no tools of its own ([0019](../research/decisions/0019-ai-provider-boundary.md)); its scenario joins
   `mcp_server.feature`.
5. `test/acceptance/features/cli_<area>.feature`, one per row, driving the
   built binary with `@op:` tags.

**Exit criteria**
- Every row of report 06 section 8 has a passing `cli_<area>.feature`.
- `make build` cross-compiles `cmd/infraweavectl` for every platform.
- Captures C22 and C23 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P24c: Console

Decisions: [0023](../research/decisions/0023-console.md), [0006](../research/decisions/0006-authentication-and-tokens.md), [0001](../research/decisions/0001-rest-first-spacelift-surface.md), [0015](../research/decisions/0015-events-notifications-webhooks-audit.md), [0019](../research/decisions/0019-ai-provider-boundary.md), [0024](../research/decisions/0024-expression-language.md), [0016](../research/decisions/0016-testing-and-acceptance.md).

**Depends on:** P24b, P18d, P25b.

infraweave's console: a React client of the public REST API, embedded in
the `infraweave` binary, that covers every surface and calls no operation
of its own.

**Tasks**
1. `web/console/`: React 19 and TypeScript in strict mode built with Vite,
   TanStack Router and TanStack Query, Radix primitives styled with
   Tailwind 4 and owned in the repository, React Flow and CodeMirror 6.
   Node.js pinned to an exact LTS release, npm with a committed lockfile
   and `npm ci`, Dependabot for `npm`, and the pin in `deps-refresh.yml`.
   orval generates the TypeScript client and hooks from the served bundles;
   they are committed, regenerated by `make generate` and checked by
   `make verify-generate`.
2. Serving in `internal/platform/httpkit/console`: Vite writes the build
   to its `dist/`, embedded through `embed.FS` with a committed marker
   file, since `//go:embed` reads only below its own package's directory
   and so cannot reach `web/console`; `internal/app/router.go` mounts it
   at `/` after every other route. `index.html` for console routes only,
   never under a prefix another handler owns; immutable caching for hashed
   assets and `no-cache` for `index.html`; the Content Security Policy
   with a per-response style nonce; the placeholder page when `dist/` holds
   no `index.html`. Tests: Go unit tests for each.
3. Surfaces: every operation family the inventory lists, with controls
   shown, hidden or disabled from `allowedActions` alone and refusals shown
   from their problem response; the account switcher, which lists the
   accounts that admit the member and shows the organization level only
   once a second account exists or in organization settings; the OAuth
   consent page of P3b's authorization server; live run logs and run state
   over the streams of P18, with polling when a stream closes; the Flows canvas and
   app widgets; Rego, CEL and text template editors backed by the
   expression and policy check endpoints; the Infra Assistant panel with
   page context, navigation and confirmation of destructive calls; English
   messages from a catalogue; WCAG 2.2 level AA. Records DV-108.
4. Quality gate: the blocking `console` job (`tsc --strict`, ESLint with
   typescript-eslint's `strict` configuration, Vitest at 95% in total and
   90% per directory, the production build) and the blocking
   `console-e2e` job, whose Playwright journeys start their dependencies
   with testcontainers and run `infraweave all` with the console embedded
   and built with `-cover`; both in the `needs` of the `Quality gate` job.

**Exit criteria**
- Every area has a success, a `403` denial and a failure journey, including
  login through the `@oidc` issuer, a switch between two accounts, an MCP
  client's consent, live run logs, a policy edited with
  server diagnostics, a flow edited on the canvas and an assistant
  confirmation.
- No journey records a CSP violation, a request to another origin or an
  axe-core level AA violation.
- `make verify-generate` fails when the committed console client differs
  from what orval generates.
- `make gate` passes at 95% in total and 90% per package, and the console's
  own thresholds hold.

## P25: AI integrations, Infra Assistant and MCP

Decisions: [0019](../research/decisions/0019-ai-provider-boundary.md), [0013](../research/decisions/0013-secrets-at-rest.md), [0006](../research/decisions/0006-authentication-and-tokens.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md).

**Depends on:** P15, P3b.

Bring-your-own-model AI integrations, an optional installation default with a
budget, Infra Assistant and run summaries, and the hosted `/mcp` server.

**Tasks**
1. `internal/intelligence` (hexagon) with the `Provider` interface and the
   Anthropic, OpenAI, Google and Bedrock adapters in
   `internal/intelligence/internal/adapters/providers`, each with a chi fake.
   Operations: `aiIntegrations`, `aiIntegration`, `aiIntegrationCreate`,
   `aiIntegrationUpdate`, `aiIntegrationDelete`, `aiIntegrationToggle`,
   `availableAIProviders`, `availableAIModels`. Tests:
   `TestUnitAIProviderConformance`, `TestUnitAIRedaction`,
   `TestUnitAIBudget`. The installation default reports
   `isInstallationProvided`, and its spend counts against the `ai-budget`
   limit (USD per 30 days), pooled across the organization's accounts,
   answering `403 limit-reached` beyond it.
   Feature: `ai_integrations.feature` (`@ai`). Records DV-071.
2. Wiring ([domain wiring](#adding-a-domain)):
   `intelligence`'s graph rule and shared-rule entries,
   `core-not-adapters` included, its place after `resources` in
   `domains.go` and the migration order, and its edges to `features`,
   `access`, `workers`, `stacks`, `runs` and `cloud`, and to `policies` if
   P13 has landed (P13 adds it otherwise); `stackCreateFromIntent` creates
   its stack through `stacksapi`, and P25b's Intent attachments hold their
   cloud integrations, worker pools and policies through those domains' `api/`
   ([declared graph](repo-layout.md#declared-graph)).
3. Account controls: `aiEnabled`, `accountSetAiEnabled`,
   `aiTermsAccepted` and `aiTermsLatestVersion` (the reference's
   `spaceliftIntelligence*` names), `infraAssistantEnabled`,
   `accountSetInfraAssistantEnabled`, `infraAssistantBuildModeEnabled`,
   `accountSetInfraAssistantBuildModeEnabled`,
   `accountRequestInfraAssistantEnabling`.
4. Infra Assistant over one tool layer, streamed over SSE, with
   confirmation of destructive calls; run summaries with masked logs. The
   `query` and `mutate` tools run a REST operation by `operationId` as the
   calling member through `internal/platform/httpkit/dispatch`, which
   `internal/app` binds to the composed router after construction
   ([0004](../research/decisions/0004-repository-architecture.md)).
   Feature: `ai_assistant.feature`. Records DV-072, DV-073.
5. The hosted `/mcp` server with its protected-resource metadata naming
   P3b's OAuth authorization server, `mcp:read` and `mcp:write` narrowed by
   the member's permissions, and `tools` narrowing. Feature:
   `mcp_server.feature` (`@oidc`, an MCP client that registers and
   authorizes through the code flow). `infraweavectl mcp`, which proxies stdio to it, lands in P24b.

**Exit criteria**
- AI Integrations and the AI settings of Account Settings meet decision
  0016's evidence rule.
- Captures C212, C213, C215, C216, C217 and C225 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P25b: Intent

Decisions: [0019](../research/decisions/0019-ai-provider-boundary.md), [0009](../research/decisions/0009-policy-engine-opa.md), [0008](../research/decisions/0008-run-execution-and-worker-protocol.md), [0010](../research/decisions/0010-state-backend.md).

**Depends on:** P25, P14.

Intent projects: AI-driven resource operations through worker runs, with
policies, locks, review and export to a stack.

**Tasks**
1. Intent projects and configuration: `intentProjects`, `intentProject`,
   `intentProjectCreate`, `intentProjectUpdate`, `intentProjectDelete`,
   `intentProjectActive`, `intentProjectActivate`, `intentProjectEnable`,
   `intentProjectDisable`, `intentProjectUnlock`,
   `intentProjectConfigAdd`, `intentProjectConfigUpdate`,
   `intentProjectConfigDelete`, `intentProjectHcl`,
   `intentProjectAttachWorkerPool`, `intentProjectDetachWorkerPool`,
   `intentProjectAttachablePolicies`, `searchIntentProjects`,
   `searchIntentProjectsSuggestions`, `workerPoolSearchIntentProjects`,
   `workerPoolSearchIntentProjectsSuggestions`,
   `awsIntegrationIntentProjectAttach`,
   `awsIntegrationIntentProjectDetach`, `policyIntentProjectAttach`,
   `policyIntentProjectDetach`.
2. Resources and operations with `INTENT` policies and review:
   `intentModuleResources`, `intentPolicyInputSchema`,
   `intentResourceDependencyAdd`, `intentResourceDependencyRemove`,
   `intentResourceOperationReview`, `intentResourceStateEject`,
   `intentProjectSearchIntentResources`,
   `intentProjectSearchIntentResourcesSuggestions`,
   `intentProjectSearchIntentResourcesOperations`,
   `intentProjectSearchIntentResourcesOperationsSuggestions`,
   `searchIntentResources`, `searchIntentResourcesSuggestions`,
   `searchIntentResourcesOperations`,
   `searchIntentResourcesOperationsSuggestions`, `stackCreateFromIntent`.
   An Intent operation reaches the project's worker pool through
   `runsapi`, so workers claim it on the same path as every other job
   ([declared graph](repo-layout.md#declared-graph)); provider signatures
   are verified on the worker (DV-074).
3. Conversations: `intentChatConversations`, `intentChatConversation`,
   `intentChatConversationCreate`, `intentChatConversationUpdate`,
   `intentChatConversationDelete`, `intentChatConversationTranscript`;
   the hosted `intent` tool. Records DV-070.
4. Feature: `intent_projects.feature` (`@tofu`, lock contention, allow,
   deny and review, import and create through a worker, export to a stack).

**Exit criteria**
- Intent meets decision 0016's evidence rule, and `/intent/mcp` answers
  `404`.
- Captures C214 and C218 to C224 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P27: Reference deployments and packaging

Decisions: [0020](../research/decisions/0020-reference-deployments-and-packaging.md), [0005](../research/decisions/0005-persistence-postgresql.md), [0017](../research/decisions/0017-observability-and-health.md), [0004](../research/decisions/0004-repository-architecture.md).

**Depends on:** P24c, P25b, P18b.

The release artefacts, the reference deployments and the upgrade and recovery
rules that make infraweave installable and upgradable.

**Tasks**
1. Release: archives for every platform with `SHA256SUMS`, the distroless
   server image with the console embedded (the build fails when the
   embedded directory has no `index.html`), which runs in FIPS 140-3 mode
   by default so no separate FIPS image ships, runner images,
   and one image per first-party Flows app version listed in infraweave's
   first-party app registry, all signed with Sigstore and carrying SLSA
   provenance and an SPDX SBOM; the release workflow verifies each before
   it publishes. Records DV-076 and DV-100.
2. Launcher distribution: `GET /downloads/launcher/<version>/linux-<arch>`
   and its sums and bundles, from the server image. Records DV-077.
3. `deploy/helm/infraweave` and `deploy/helm/infraweave-workers`,
   `deploy/compose/compose.yaml`, `deploy/terraform/modules/<cloud>` for AWS,
   GCP and Azure with examples, each setting `database.iam_auth` to its
   cloud's value (`aws`, `gcp`, `azure`) over TLS, and `deploy/check.sh` in
   the gate.
   Features: `deploy_compose.feature`, `deploy_helm.feature`
   (`@kubernetes`). Records DV-075.
4. Upgrades and recovery: the `upgrade_floor` per stream, mixed-version
   operation, backup and restore guidance. Feature:
   `deploy_upgrade.feature`. `debugInfo`, `installationId` (Debug and
   Infrastructure tags).

**Exit criteria**
- `deploy/check.sh` passes, a tracked run finishes on the Compose file and on
  the Helm chart, and the previous release and the candidate serve one
  database together.
- Debug and Infrastructure meet decision 0016's evidence rule.
- Captures C243 to C250 replay once recorded.
- `make gate` passes at 95% in total and 90% per package.

## P28: Capture replay and qualification

Decisions: [0016](../research/decisions/0016-testing-and-acceptance.md), [0021](../research/decisions/0021-capability-scope-and-supersession.md), [0002](../research/decisions/0002-reference-sources-and-licensing.md).

**Depends on:** P27, and an account Spacelift has authorized for captures.

Every behaviour that infraweave tested against its own recorded choice is
checked against Spacelift where a capture can settle it, and report 26 is
qualified against the tests.

**Tasks**
1. Take the captures of report 30 with an authorized account, redact them
   and check them in under `test/fixtures/captures/C<n>/`; mark each entry
   resolved in report 30 and change the report's marker.
2. `TestAccCaptures` replays every `expect.yaml`; a failing expectation is a
   parity bug, fixed in the owning domain with its deviation row if
   infraweave keeps its choice.
3. Amend in place each decision whose behaviour a capture changes, with
   its deviation row; amend the report 26 cells decision 0021's
   constraints name (section 7.8's MQTT transport, section 7.7's legacy AWS
   mutations).
4. Run the `nightly` suite on the newest PostgreSQL, Terraform and OpenTofu
   releases and close what it finds.

**Exit criteria**
- Every capture of report 30 is replayed or recorded as unobtainable with
  its reason.
- `specref parity --states --check` shows no tag or Terraform record of
  [capabilities.json](../data/capabilities.json) in `missing` or `partial`.
- `make gate` passes at 95% in total and 90% per package.
