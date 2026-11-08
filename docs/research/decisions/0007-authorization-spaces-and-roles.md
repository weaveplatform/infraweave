# 0007: Authorization: spaces and roles

Status: Accepted

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

On Spacelift, keeping customers apart is the service operator's concern,
outside the product's model, and a customer's account is the top of
everything. infraweave is self-hosted, one deployment may serve several
customers that must not see one another's data, and one customer may run
several business entities under one agreement, such as a UK arm and a US
arm. Tenancy is therefore part of authorization rather than an accident of
deployment.

infraweave needs one evaluator that answers three questions the same way
everywhere: may this principal perform this action on this subject; which
spaces may it act in, for list filtering; and why, for the grant views
(`RESIDENT`, `PROPAGATED`, `INHERITED`). Decision
[0006](0006-authentication-and-tokens.md) supplies the principal.

## Decision

### Tenancy

infraweave's tenancy is root, organization, account and spaces, in the
glossary's sense ([GLOSSARY](../../../GLOSSARY.md)).

| Level | What it is | What it holds |
|---|---|---|
| Root | The installation's immutable top node. Only operators act on it | Organizations, and nothing else |
| Organization | A customer's agreement with the installation | Its tier and grants ([0021](0021-capability-scope-and-supersession.md)), its pooled limits and account limits, its members and identity providers ([0006](0006-authentication-and-tokens.md)), its organization keys, and one or more accounts |
| Account | A separate business entity within an organization, and the data boundary. Spacelift's account maps onto it | Its spaces, users, groups, roles, bindings, API keys, settings and data |
| Space | A node of the account's tree, as on Spacelift | The account's entities |

- **Tables.** `organizations(id, slug, name, created_at)`, whose tier and
  entitlement version sit in the `features` domain's
  `organization_entitlements` (0021), and
  `accounts(organization_id, id, slug, name, authz_version, claimed_at,
  created_at)`.
  Slugs are immutable and names can be renamed. A slug is 1 to 63
  lower-case letters, digits and single hyphens, starts with a letter and
  does not end with a hyphen; `root` and `legacy` are reserved, because
  the top space takes the account's slug where Spacelift says `root`, and
  `legacy` is the slug of the space beneath it. Account slugs are
  unique across the installation, because the registry namespace
  ([0014](0014-module-and-provider-registry.md)) and the federation audience
  (0006) are installation-wide. Organization slugs are unique among
  organizations, and an organization and its first account may share a
  slug. Every account-scoped table carries `account_id`, and every query
  filters by the principal's account; organization-scoped tables carry
  `organization_id`.
- **The principal fixes the account.** Every credential that reaches
  account data is issued for one account (decision 0006 issues them), so a
  request never names the account it acts in. A member works in one account
  at a time and switches accounts by taking a new session (0006). The
  `Account` tag's reads (`id`, `name`, `type`) therefore answer the
  principal's account. `type` is always `ORG` (DV-115): the reference's
  `AccountType` is the type of the GitHub owner a Spacelift account was
  created for, and every infraweave account belongs to an organization.
- **Accounts never see each other.** An entity of another account answers
  `404`, as one the caller cannot read does, between accounts of one
  organization as across organizations. No role, binding, admin flag or
  organization role reaches across accounts.
- **Public modules and providers** are not an exception: `modulePublish`
  and a public provider make them readable anonymously, by anyone, as
  decision 0014 says, which reveals nothing that belongs to one account
  alone.
- **The one exception: module shares.** A module may be shared with another
  account of the same organization (decision 0014). The consumer reads only
  the `SharedModule` fields and downloads the module's versions; nothing
  else of the source account becomes visible. The organization's
  entitlement must include `module-sharing`. Revoking the share hides the
  module from the consumer at once, and a run already in flight finishes
  with what it has downloaded. A share naming an account of another
  organization answers `422`, problem `validation-failed`.
- **Nothing inherits from root or from the organization.** No policy, role,
  binding, context, integration or setting placed above an account applies
  inside it. What an account takes from its organization is not account
  data: the entitlement, the limits, the members and their authentication.
  Organization-wide policies are a possible later catalogue feature, and
  this record does not decide them.
- **Organization admins.** An organization admin is a member holding the
  organization role `organization-admin`, a row of
  `organization_admins(organization_id, member_id, created_by,
  created_at)`, or a sign-in that holds it without a row: an identity
  provider's `adminLogin`, a GitHub organization admin and the break-glass
  admin (0006). Organization admins create,
  rename and delete accounts; manage members, identity linking, identity
  providers, MFA and the session lifetime (0006); set account limits and
  read usage across the organization's accounts (0021); and hold
  organization keys. They see none of any account's stacks, runs, state or
  secrets: the organization role grants nothing inside an account, and an
  organization admin who works in an account is admitted to it like any
  other member. The `Organization` tag's operations carry the pseudo-action
  `ORGANIZATION_ADMIN` and the subject `organization` (below).
- **The operator runs the installation.** The operator creates and deletes
  organizations, gives each its tier and grants, and issues the public
  worker pool's token, through the operator API (0021), and sets
  `features.disabled` in the installation's configuration. An operator
  principal holds no role in any organization or account and reads no
  account's stacks, state or secrets.
- **Creating an organization** creates its first account in the same
  transaction, and the operator sets the break-glass credential it holds
  (0006). The operator names no member, because a member exists only once
  one of the organization's identity providers asserts it, and naming one
  by email would be the email match 0006 refuses. The break-glass admin
  configures the first identity provider, whose `adminLogin` then signs in
  as an organization admin.
- **Claiming an account.** The first account starts unclaimed: no principal
  holds a binding in it. An organization admin claims it once, naming the
  member that gets `space-admin` on its top space as an ordinary binding
  that account admins can later remove. A claimed account stays claimed
  (`accounts.claimed_at`), so removing every account admin later never
  reopens it to the organization role; claiming it again answers `409`,
  problem `invalid-state`.
- **Creating an account** seeds its top space, its `legacy` space and its
  system roles, and binds `space-admin` on the top space to the member the
  creating organization admin names, so an account an organization admin
  creates is claimed at creation. Deleting an account deletes its data;
  deleting the last account of an organization answers `409`, problem
  `invalid-state`, and deleting an organization deletes its accounts.
- **Organization roles reach only the organization's own operations.** The
  `Organization` tag and the reference's `SSO` tag need
  `organization-admin`, reads included, because both describe what every
  account of the organization shares. Spacelift's account admin managed
  SSO for its account; an infraweave account admin without the
  organization role gets `403`, problem `forbidden`.
- **The console** shows the organization level only once the organization
  has a second account, or in organization settings
  ([0023](0023-console.md)); an organization with one account reads as a
  single account.

### Spaces

- **Table** `spaces(account_id, id, slug, name, description, labels text[],
  inherit_entities, parent_id, created_at)`, unique `(account_id, slug)`,
  with `parent_id` null only for the account's top space.
- **The top space.** Every account's tree has one top space, the space
  Spacelift calls `root`. Its slug, and so its ID on the wire, is the
  account's immutable slug, so no space ID says root, and one ID names the
  same space in exports, OIDC subjects and imports. Its key is the
  account's ID, so the account names its top space without a lookup. The
  importer maps a Spacelift `root` to the account's slug. The top space
  cannot be deleted or changed.
- **Seeded spaces.** Every account starts with its top space and `legacy`, a
  child of the top space whose slug has no suffix. `legacy` cannot be
  deleted. It is an ordinary space: it carries no special access semantics
  in infraweave, but exists because imported data and OIDC subjects name
  it.
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
- **Account admins.** An account admin is a holder of `space-admin` on the
  account's top space: Spacelift's root admin. A non-admin is any other
  principal, including a space admin below the top space, which Spacelift
  calls a non-root admin. The reference's names for both are renamed by
  [0001](0001-rest-first-spacelift-surface.md#names)'s table, as in
  `accountToggleAllowNonAdminSpaceCreation` and
  `/account/api-key-non-admin-managers`.
- **Who changes the tree.** Account admins anywhere. Space admins below the
  top space only when the account setting that `allowNonAdminSpaceCreation`
  reads is on, and then only below spaces they administer; the spaces they
  create always inherit. With the setting off, such an admin edits the
  name, description and labels of spaces they administer and nothing
  else.

### Roles and the action catalogue

- **Source.** The action catalogue is the `Action` enum of the committed
  introspection snapshot (150 values). `specref` generates
  `internal/authz/actions.gen.go` from it, and a hand-maintained
  `internal/authz/actions.yaml` gives each value its subject, legacy fallback
  (`reader`, `writer`, `admin`), and `disabled` and `deprecated` flags, from
  report 14's table
  ([3.3](../reference/14-spaces-rbac-and-identity.md#33-the-action-catalogue)).
  `make verify-reference` fails when an enum value has no row or a row has no
  enum value.
- **Tables** `roles(account_id, id, slug, name, description, is_system)` and
  `role_actions(role_id, action)`, unique `(account_id, slug)`.
- **Custom roles.** Account admins create, update and delete them. Assigning a
  `disabled` or `deprecated` action answers `422`, problem
  `validation-failed`, naming the action. A role with bindings can be
  deleted only after its bindings are; the answer otherwise is `409`, problem
  `invalid-state`.
- **System roles** are infraweave's own: seeded per account, immutable, and
  defined from the fallback column, so legacy levels and system roles are
  one thing. Their slugs and contents are fixed here, and nothing in this
  record waits on a capture of Spacelift's:

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
  `SPACE_ADMIN` on the target space; a binding on the top space needs the
  stack to live in the top space; bindings stay where they are when the
  stack moves.
- **Space admins below the top space** create and delete bindings only in
  spaces they administer.

### Evaluation

`authz.Authorize(ctx, principal, action, spaceID) Decision` decides one check:

1. **Admin.** A principal whose session has `admin` in this account (a
   holder of `space-admin` on the account's top space, or a login policy
   `admin` result of the account's policies) is allowed every action, less
   the in-run restriction below. An identity provider's `adminLogin` and a
   GitHub organization admin are organization admins (0006): each is an
   account admin only where an account binds or admits it as one. The
   break-glass admin is never admitted to an account.
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

Admission runs when a member enters an account (decision 0006): the
organization has authenticated the member, and the account's scheme decides
whether it may work there and with which grants. Under `LOGIN_POLICIES`,
user, group and API key bindings are ignored. The
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

The rule extends to every field of a view that says what the caller may do
or holds, whether a role action decides it or a precondition of one
operation does: the field is the owning service's answer from the rule its
operation enforces, never a copy of that rule in the view. A view that
gathers several such fields, such as `User`, is composed by the HTTP
adapter from each owner's answer. `User.canCreatePersonalApiKey` is the
`apikey` service's reading of `personalApiKeyCreate`'s rule (a member's own
session, admitted and active, with `personalApiKeysEnabled` on), the same
query the creation runs under its locks, and `User.apiKeys` and
`ownedApiKeys` are that service's too; `User.admin` and `User.actions` are
the evaluator's.

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
| Who may enter the account | account admins; any `ACTIVE` user of the account, with empty grants if it holds no binding; any member whose teams match a group with a binding | whoever the account's login policies admit |
| Role source | bindings | the policy's `roles` rule, frozen in `session_grants` |
| Evaluation | none on entry | every `LOGIN` policy in the account's top space through [0009](0009-policy-engine-opa.md), input per report 14 section 5.2 |

A member that no account admits keeps its organization session with no
account. The account switcher lists only the accounts that admit the member,
so that member sees an empty list.

- Login policy rules are report 14's
  ([5.2](../reference/14-spaces-rbac-and-identity.md#52-login-policy-rules)):
  `deny` and `deny_admin` override; `admin` implies `allow`; with no policy,
  `allow` equals `session.member`; non-empty `team` replaces the session's
  teams; `deny_message` values from all policies are de-duplicated, sorted,
  joined with `;` and cut at 1,024 characters with `...`.
- `accountUpdateAuthorizationScheme` revokes every session but the caller's;
  changing any login policy revokes every session but the editor's.
- API keys under `LOGIN_POLICIES` need a policy that admits them, except keys
  with `space-admin` on the account's top space. Personal keys behave as
  ordinary keys there.

### Enforcement point

- **In the spec.** Every operation in `api/api.openapi.yaml` carries
  `x-infraweave-action` (an `Action` value, or the pseudo-actions
  `ACCOUNT_ADMIN`, `ORGANIZATION_ADMIN` and `AUTHENTICATED`) and
  `x-infraweave-subject`, which says where the subject's space comes from:

  | `x-infraweave-subject` | Space used |
  |---|---|
  | `path:<param>:<entity>` | the space of the entity named by a path parameter, such as `path:stackId:stack` |
  | `body:<field>` | a space ID in the request body, for creates |
  | `account` | the account's top space, for account-wide settings |
  | `organization` | none: the caller's organization, for the operations of the `Organization` and `SSO` tags, which take `ORGANIZATION_ADMIN` |
  | `list` | none: the handler filters with `Reachable` |

- **In the server.** The scaffolder generates a route table from these
  extensions, and one chi middleware, `authz.Require`, runs after
  `authn.Middleware` on every route: it resolves the subject's space through
  a small per-entity lookup (`stack`, `module`, `context`, `policy`,
  `worker_pool`, `run`, and so on) and calls `Authorize`. Handlers make extra
  checks only where an operation needs two (stack bindings, moves, module
  sharing).
- **Deny by default.** A route with no route-table entry answers `403`, and
  `api/api_test.go` fails when any operation lacks either extension.
- **Answers.** A subject in a space where the caller lacks `SPACE_READ`
  answers `404`, as if it did not exist; a readable subject whose action is
  not allowed answers `403`, problem `forbidden`, naming the action. Decision
  [0022](0022-api-errors-and-update-semantics.md) owns both bodies.

### Superseded interfaces

- **Access policies** are never evaluated. Whether the API stores or refuses
  `ACCESS` policies is decision 0021's (PD-02), which does not serve the
  type; they grant nothing,
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
- **The organization above the account.** One agreement covering several
  business entities needs one place for its tier, limits, members and
  sign-in, and a boundary between the entities' data. Placing the agreement
  above the account keeps Spacelift's account as the data boundary, so every
  reference name and behaviour that says account keeps its meaning.
- **Nothing inherits downward.** An organization-level policy, context or
  binding would let the agreement's holder change what runs inside an entity
  whose data it cannot see, and would make an account's behaviour depend on
  data outside its boundary.
- **Module shares as the one exception.** Spacelift shares modules between
  accounts, and a share exposes only the module's read model and versions;
  confining it to one organization means data crosses only between entities
  under one agreement.
- **The top space named by the account.** A slug that says root would claim
  the installation's node for every account, and an export or a federation
  subject naming `root` could not say which account it came from.

Departures from Spacelift, for the plan's deviations register:

| Departure | Why |
|---|---|
| `legacy` has no special semantics | Access policies, its only special behaviour, are superseded |
| infraweave defines its own system roles, with contents derived from the fallback column | Spacelift does not publish their contents ([14 section 3.2](../reference/14-spaces-rbac-and-identity.md#32-system-roles)); one rule keeps the roles and the legacy levels consistent |
| The worker pool controller and drift detection system roles' slugs are `worker-pool-controller` and `drift-detection-manager` | Spacelift's slugs for both are unrecorded (report 14 section 3.2); these are infraweave's names |
| One installation hosts many organizations, each with one or more accounts separated by `account_id` | Spacelift keeps customers apart on its own service and has no level above the account; a self-hosted product needs the separation inside one deployment, and one agreement may cover several business entities |
| The top space is addressed by the account's slug, not `root` | Root is the installation's node; an account's top space is the account's own |
| Identity provider admins, GitHub organization admins and the break-glass admin are organization admins, not account admins | Authentication belongs to the organization and authorization to each account, so a sign-in alone grants nothing in an account |
| The reference's SSO operations need the organization admin role, and an account admin alone cannot read them | The identity providers belong to the organization and serve every account in it |
| An account created with its organization starts unclaimed until an organization admin names its first account admin | No member exists before an identity provider asserts one, and the operator never reaches account data |
| Disabled actions cannot be assigned to roles | The schema marks them "do not use"; operations they would govern use their fallback |
| An `ACTIVE` managed user with no binding logs in with empty grants | The 2026-04-14 changelog is the newer of two conflicting statements (report 14 section 4.2) |

## Constraints

- **Tenancy is logical.** Organizations and accounts share one database and
  one set of processes; separation rests on `account_id` filters and the
  per-account snapshot. An operator who needs physical separation runs one
  installation per organization.
- **Staleness bound.** A lost invalidation message leaves a replica at most 5
  seconds behind.
- **Unverified facts** this record decides around: the slugs Spacelift gives
  the worker pool controller and drift detection system roles, what space
  admins below the top space may edit with the creation setting off, and the
  login conflict above (report 14, *(unverified)* markers). Each is a constant or a table row to
  change.
- **Out of scope.** Flows-side permissions for `flows_project` bindings are
  decision 0018's; tier gates on custom roles or login policies are 0021's;
  seat limits are 0021's.

## Verification

- `internal/authz` unit tests, table-driven from report 14's worked cases:
  an entity of another account answering as hidden;
  propagation from the top space; inheritance read-up through a chain and stopping
  at the first space without `inherit_entities`; an inherited grant folded to
  `space-reader`; stack self-read; group and direct bindings combining;
  the in-run restriction; login policy snapshots frozen while IAM changes
  apply live.
- A property test: after random creates, moves and deletes, `space_closure`
  equals the ancestors computed from `parent_id`, and `Reachable` equals
  `Authorize` over every space.
- `api/api_test.go`: every operation has `x-infraweave-action` and
  `x-infraweave-subject`, and every action named exists in the catalogue.
- `make verify-reference`: `actions.yaml` covers the `Action` enum exactly.
- Gherkin acceptance against PostgreSQL in testcontainers:
  `test/acceptance/features/authz_spaces.feature` (tree changes, refused
  moves, grant views), `authz_bindings.feature` (each actor kind, stack
  escalation rules), `authz_login_policy.feature` (rules, `deny_message`
  aggregation, session revocation on change), `authz_tenancy.feature` (two
  organizations, one with two accounts: each account sees only its own
  spaces and stacks, and another account's stack answers `404` within the
  organization and across it; a new account starts with its top space named
  by its slug, `legacy`, its system roles and its first admin; the first
  account of a new organization is claimed once and a second claim answers
  `409`; an account admin without the organization role gets `403` on the
  `SSO` tag; an
  organization admin reads usage across accounts and gets `404` on their
  stacks until an account admits it; a policy or context placed in one
  account never reaches another; a member no account admits sees an empty
  account switcher; a module share to an account of another organization
  answers `422`).

## References

- [14](../reference/14-spaces-rbac-and-identity.md#2-spaces): spaces, inheritance, roles, the action catalogue, bindings, schemes and login policy rules.
- [26 section 7.4](../reference/26-scope-and-tiers.md#74-access-policies-and-legacy-access-rules-replaced-by-spaces-and-rbac): what spaces and RBAC supersede.
- [28 section 12.2](../reference/28-data-model.md#122-account-and-spaces): the account and space tables.
- [27 section 12](../reference/27-reference-materials.md#12-cross-tool-comparison): no reference tool models space inheritance.
- [GLOSSARY](../../../GLOSSARY.md): installation, root, organization, account, member, operator, role.
