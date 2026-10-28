# 0007: Authorization: spaces and roles

Status: Proposed

## Context

Spacelift authorizes per space. Spaces form one tree under `root`; a role is a
set of `Action` values; a role binding grants a role to an actor in a space and
every space below it; read access also climbs to parents that inherit
entities; stacks read their own space without any binding; and an action no
role grants can still be allowed by the legacy access level named as its
fallback ([14](../reference/14-spaces-rbac-and-identity.md#1-model-in-one-page)).
Two authorization schemes exist side by side: Identity Access Management
(`MANAGED_USERS`), where bindings decide, and `LOGIN_POLICIES`, where a Rego
policy assigns roles at login and the result is frozen for the session
([14 section 5](../reference/14-spaces-rbac-and-identity.md#5-authorization-strategies)).
Access policies, the older per-stack Rego rules, are disabled and superseded
([26 section 7.4](../reference/26-scope-and-tiers.md#74-access-policies-and-legacy-access-rules-replaced-by-spaces-and-rbac)).

infraweave needs one evaluator that answers three questions the same way
everywhere: may this principal perform this action on this subject; which
spaces may it act in, for list filtering; and why, for the grant views
(`RESIDENT`, `PROPAGATED`, `INHERITED`). Decision
[0006](0006-authentication-and-tokens.md) supplies the principal.

## Decision

### Spaces

- **Table** `spaces(account_id, id, slug, name, description, labels text[],
  inherit_entities, parent_id, created_at)`, unique `(account_id, slug)`,
  with `parent_id` null only for `root`.
- **Seeded spaces.** Every account starts with `root` and `legacy` (slugs
  without a suffix; `legacy` is a child of `root`). Neither can be deleted,
  and `root` cannot be changed. `legacy` is an ordinary space: it carries no
  special access semantics in infraweave, but exists because imported data
  and OIDC subjects name it.
- **Slugs.** A created space's slug is its name, lowercased and hyphenated,
  followed by `-` and its ULID, the shape report 12 records for space IDs in
  federation subjects. It never changes.
- **Closure table** `space_closure(account_id, ancestor_id, descendant_id,
  depth)`, primary key `(ancestor_id, descendant_id)`, index
  `(descendant_id)`. Each space has a row to itself at depth 0. Create, move
  and delete maintain it in the same transaction as the `spaces` change:
  - **create** inserts the parent's ancestor rows plus the self row;
  - **move** (a `parent_id` change) refuses a target inside the moved
    subtree, deletes the rows that link outside ancestors to the subtree and
    inserts the cross product of the new ancestors and the subtree;
  - **delete** is refused unless no row of any space-scoped table names the
    space and it has no children.
- **Moves keep stacks working.** A space move, or a move of a stack or an
  attachable entity, is refused with `422`, problem `validation-failed`, whose
  `errors` list each stack and the entity it would lose (worker pool, cloud
  integration, context, policy), when the inheritance rules below would no
  longer let the stack reach it.
- **Who changes the tree.** Root admins anywhere. Non-root space admins only
  when the account setting `allowNonRootAdminSpaceCreation` is on, and then
  only below spaces they administer; the spaces they create always inherit.
  With the setting off, a non-root admin edits the name, description and
  labels of spaces they administer and nothing else.

### Roles and the action catalogue

- **Source.** The action catalogue is the `Action` enum of the committed
  introspection snapshot (150 values). `specref` generates
  `internal/authz/actions_gen.go` from it, and a hand-maintained
  `internal/authz/actions.yaml` gives each value its subject, legacy fallback
  (`reader`, `writer`, `admin`), and `disabled` and `deprecated` flags, from
  report 14's table
  ([3.3](../reference/14-spaces-rbac-and-identity.md#33-the-action-catalogue)).
  `make verify-reference` fails when an enum value has no row or a row has no
  enum value.
- **Tables** `roles(account_id, id, slug, name, description, is_system)` and
  `role_actions(role_id, action)`, unique `(account_id, slug)`.
- **Custom roles.** Root admins create, update and delete them. Assigning a
  `disabled` or `deprecated` action answers `422`, problem
  `validation-failed`, naming the action. A role with bindings can be
  deleted only after its bindings are; the answer otherwise is `409`, problem
  `invalid-state`.
- **System roles** are seeded per account, immutable, and defined from the
  fallback column, so legacy levels and system roles are one thing:

  | Slug | Actions |
  |---|---|
  | `space-reader` | every assignable action whose fallback is `reader` |
  | `space-writer` | `space-reader` plus every fallback `writer` action |
  | `space-admin` | every assignable action |
  | `worker-pool-controller` | `SPACE_READ`, `WORKER_POOL_CREATE`, `WORKER_POOL_UPDATE`, `WORKER_POOL_DELETE` |
  | `drift-detection-manager` | `SPACE_READ` and the three `DRIFT_DETECTION_INTEGRATION_*` actions |

### Bindings

- **Table** `role_bindings(account_id, id, actor_kind, actor_id, role_id,
  space_id, origin, created_at, created_by)`, unique `(actor_kind, actor_id,
  role_id, space_id)`, indexes `(space_id)` and `(actor_kind, actor_id)`.
- **Actor kinds:** `user`, `group`, `api_key`, `stack`, `flows_project`. A
  `group` binding names a row of `user_groups`, matched to session teams by
  exact string, as Spacelift matches IdP groups.
- **Origin** is `binding` for role bindings and `access_rule` for the legacy
  `SpaceAccessRule` inputs (V1 user, group and API key inputs, Terraform
  `policy` blocks), which are stored as bindings to `space-reader`,
  `space-writer` or `space-admin`. `accessRules` reads back the `access_rule`
  rows; the binding views list all rows.
- **Stack bindings** need `STACK_MANAGE` on the stack's space and
  `SPACE_ADMIN` on the target space; a binding on `root` needs the stack to
  live in `root`; bindings stay where they are when the stack moves.
- **Non-root admins** create and delete bindings only in spaces they
  administer.

### Evaluation

`authz.Authorize(ctx, principal, action, spaceID) Decision` decides one check:

1. **Admin.** A principal whose session has `admin` (an SSO admin, the
   break-glass admin, a GitHub organization admin, a login policy `admin`
   result, or any holder of `space-admin` on `root`) is allowed every action,
   less the in-run restriction below.
2. **Actors.** The principal's actor set is: a user and the groups named by
   its session teams; an API key and its `teams` as virtual groups (a
   personal key uses its owner's set); a run principal's stack.
3. **Grants.** For target space S, a role applies when it is:
   - **resident**: bound to an actor on S;
   - **propagated**: bound on an ancestor of S (closure rows with depth
     above 0);
   - **inherited**: bound on a descendant D of S where every space from D up
     to the child of S on that path has `inherit_entities`; an inherited
     grant contributes only `space-reader`'s actions, whatever role is bound.
4. **Stacks** also hold an implicit `space-reader` grant on their home space,
   which propagates down and climbs through inheriting parents like any
   other read.
5. **Decision.** The action is allowed when any applying role contains it.
   Because legacy levels are bindings to the system roles, the legacy
   fallback needs no separate step.

Under `LOGIN_POLICIES`, user, group and API key bindings are ignored. The
session's grants are the policy's result, stored at login as
`session_grants(session_id, space_id, role_id, origin)` with `origin`
`ASSIGNED_ROLE` for the `roles` rule and `ACCESS_LEVEL` for the deprecated
`space_admin`, `space_write` and `space_read` rules mapped to system roles.
They are frozen until the session ends. Stack bindings apply under both
schemes.

**Reachable spaces.** `authz.Reachable(ctx, principal, action) []uuid.UUID`
returns every space in which the action is allowed. List and search handlers
pass it to SQL as `space_id = ANY($n)` so that the database never returns a
row the caller may not see, and paging (decision 0003) stays exact.

**In-run restriction.** A `run` principal can never perform the actions in
`authz.RunTokenDenied`: `STACK_STATE_READ`, `STACK_STATE_DOWNLOAD`, every
`RUN_*` action, `TASK_CREATE`, and reads of runs, tasks and logs. A stack with
no binding gets only its implicit read. Write-only values are never readable
by anyone (decision [0013](0013-secrets-at-rest.md)).

**Grant views** (`Space.accessLevel`, `Space.allowedActions`, `User.actions`,
`usersWithAccessToSpace`, `stacksWithAccessToSpace` and their siblings) call
the same evaluator with an explaining variant that returns each grant's
source (`RESIDENT`, `PROPAGATED`, `INHERITED`), backing binding and, for an
inherited grant, the synthetic `space-reader` role.

### Snapshots and caching

- Each replica holds, per account, an immutable snapshot of the space tree
  (parent, `inherit_entities`), the roles with their actions, and the
  bindings indexed by actor. A check walks at most the depth of the tree.
- Every write to `spaces`, `roles`, `role_actions`, `role_bindings`,
  `user_groups` or the scheme bumps `accounts.authz_version` in the same
  transaction and sends `authz:<account>` on `infraweave_invalidate`
  ([0005](0005-persistence-postgresql.md)). A replica rebuilds the snapshot on
  the message, and also compares the version every 5 seconds in case a
  message was lost.
- Under `MANAGED_USERS` this makes binding and role changes apply to live
  sessions at once, as Spacelift does since 2026-03-19.

### Schemes and login

| Step | `MANAGED_USERS` | `LOGIN_POLICIES` |
|---|---|---|
| Who may log in | admins; any `ACTIVE` managed user, with empty grants if it holds no binding; any identity whose teams match a group with a binding | whoever the login policies admit |
| Role source | bindings | the policy's `roles` rule, frozen in `session_grants` |
| Evaluation | none at login | every `LOGIN` policy in `root` through [0009](0009-policy-engine-opa.md), input per report 14 section 5.2 |

- Login policy rules are report 14's
  ([5.2](../reference/14-spaces-rbac-and-identity.md#52-login-policy-rules)):
  `deny` and `deny_admin` override; `admin` implies `allow`; with no policy,
  `allow` equals `session.member`; non-empty `team` replaces the session's
  teams; `deny_message` values from all policies are de-duplicated, sorted,
  joined with `;` and cut at 1,024 characters with `...`.
- `accountUpdateAuthorizationScheme` revokes every session but the caller's;
  changing any login policy revokes every session but the editor's.
- API keys under `LOGIN_POLICIES` need a policy that admits them, except keys
  with `space-admin` on `root`. Personal keys behave as ordinary keys there.

### Enforcement point

- **In the spec.** Every operation in `api/deploy.openapi.yaml` carries
  `x-infraweave-action` (an `Action` value, or the pseudo-actions
  `ROOT_ADMIN` and `AUTHENTICATED`) and `x-infraweave-subject`, which says
  where the subject's space comes from:

  | `x-infraweave-subject` | Space used |
  |---|---|
  | `path:<param>:<entity>` | the space of the entity named by a path parameter, such as `path:stackId:stack` |
  | `body:<field>` | a space ID in the request body, for creates |
  | `root` | `root`, for account-wide settings |
  | `list` | none: the handler filters with `Reachable` |

- **In the server.** The scaffolder generates a route table from these
  extensions, and one chi middleware, `authz.Require`, runs after
  `authn.Middleware` on every route: it resolves the subject's space through
  a small per-entity lookup (`stack`, `module`, `context`, `policy`,
  `worker_pool`, `run`, and so on) and calls `Authorize`. Handlers make extra
  checks only where an operation needs two (stack bindings, moves, module
  sharing).
- **Deny by default.** A route with no route-table entry answers `403`, and
  `api/deploy_test.go` fails when any operation lacks either extension.
- **Answers.** A subject in a space where the caller lacks `SPACE_READ`
  answers `404`, as if it did not exist; a readable subject whose action is
  not allowed answers `403`, problem `forbidden`, naming the action. Decision
  [0022](0022-api-errors-and-update-semantics.md) owns both bodies.

### Superseded interfaces

- **Access policies** are never evaluated. Whether the API stores or refuses
  `ACCESS` policies is decision 0021's (P2), which proposes not serving the
  type; either way they grant nothing,
  and `User.cannotEvaluateAccessPolicies` is always `true`.
- **The stack `administrative` flag** is accepted and reads back `false`. An
  importer turns a `true` flag into a `space-admin` binding on the stack's own
  space, as Spacelift's own migration did.
- **Login policy `space_*` rules** still work, mapped to system roles, because
  login policies themselves are live.

## Rationale

- **Spacelift's model, evaluated natively.** The semantics (propagation,
  inheritance, implicit stack reads, fallback levels, two schemes) are fixed
  by the product and its grant views; a general policy language would only
  re-express them. OPA stays where Spacelift uses Rego: login policies.
- **Legacy levels as system-role bindings.** Defining the system roles from
  the fallback column makes "a role grants it, else the fallback level
  decides" a single set-membership test, and `accessRules` round-trips
  through the same rows.
- **Closure table plus an in-memory snapshot.** SQL needs subtree joins for
  emptiness checks and the access listing queries; per-request checks need
  speed. A recursive CTE per request would do neither cheaply. Trees are
  small (hundreds of spaces), so a snapshot per account fits in memory.
- **Authorization in the query, not after it.** Filtering with `Reachable`
  before `LIMIT` keeps cursor pages full and never leaks counts, which
  post-filtering in Go cannot do.
- **Actions in the spec.** A reviewer sees each route's permission beside its
  shape, the scaffolder cannot forget one, and a test proves coverage.
- **Rejected: PostgreSQL row-level security.** It cannot explain a decision
  for the grant views, needs per-connection settings that conflict with
  pooling, and still needs the action catalogue in Go.
- **Rejected: an external authorization service** (SpiceDB, OpenFGA). It adds
  a stateful dependency and a consistency boundary for a model that fits in
  one process.
- **Rejected: Cedar or OPA for RBAC.** Every policy would be a generated
  translation of a binding.

Departures from Spacelift, for the plan's deviations register:

| Departure | Why |
|---|---|
| `legacy` has no special semantics | Access policies, its only special behaviour, are superseded |
| System role contents are derived from the fallback column | Spacelift does not publish them (report 14 section 3.2); parity is checked against `Role.actions` once captured |
| The worker pool controller and drift detection system roles' slugs are `worker-pool-controller` and `drift-detection-manager` | Spacelift's slugs for both are unrecorded (report 14 section 3.2) |
| Disabled actions cannot be assigned to roles | The schema marks them "do not use"; operations they would govern use their fallback |
| An `ACTIVE` managed user with no binding logs in with empty grants | The 2026-04-14 changelog is the newer of two conflicting statements (report 14 section 4.2) |

## Constraints

- **Tenancy.** Snapshots are per account; whether an installation hosts more
  than one account is outside this record.
- **Staleness bound.** A lost invalidation message leaves a replica at most 5
  seconds behind.
- **Unverified facts** this record proposes around: system role contents,
  the worker pool controller and drift detection roles' slugs, what non-root admins may edit with the
  creation setting off, and the login conflict above (report 14,
  *(unverified)* markers). Each is a constant or a table row to change.
- **Out of scope.** Flows-side permissions for `flows_project` bindings are
  decision 0018's; tier gates on custom roles or login policies are 0021's;
  seat limits are 0021's.

## Verification

- `internal/authz` unit tests, table-driven from report 14's worked cases:
  propagation from `root`; inheritance read-up through a chain and stopping
  at the first space without `inherit_entities`; an inherited grant folded to
  `space-reader`; stack self-read; group and direct bindings combining;
  the in-run restriction; login policy snapshots frozen while IAM changes
  apply live.
- A property test: after random creates, moves and deletes, `space_closure`
  equals the ancestors computed from `parent_id`, and `Reachable` equals
  `Authorize` over every space.
- `api/deploy_test.go`: every operation has `x-infraweave-action` and
  `x-infraweave-subject`, and every action named exists in the catalogue.
- `make verify-reference`: `actions.yaml` covers the `Action` enum exactly.
- Gherkin acceptance against PostgreSQL in testcontainers:
  `test/acceptance/features/authz_spaces.feature` (tree changes, refused
  moves, grant views), `authz_bindings.feature` (each actor kind, stack
  escalation rules), `authz_login_policy.feature` (rules, `deny_message`
  aggregation, session revocation on change).

## References

- [14](../reference/14-spaces-rbac-and-identity.md#2-spaces): spaces, inheritance, roles, the action catalogue, bindings, schemes and login policy rules.
- [26 section 7.4](../reference/26-scope-and-tiers.md#74-access-policies-and-legacy-access-rules-replaced-by-spaces-and-rbac): what spaces and RBAC supersede.
- [28 section 12.2](../reference/28-data-model.md#122-account-and-spaces): the account and space tables.
- [27 section 12](../reference/27-reference-materials.md#12-cross-tool-comparison): no reference tool models space inheritance.
