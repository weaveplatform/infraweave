# 14: Spaces, RBAC and identity

Checked against: docs.spacelift.io read 2026-10-28 (SaaS pages and `self-hosted/latest`, self-hosted v7.1.0); introspection `b3255fe38f5e` (fetched 2026-10-28); terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); spacectl `6bf167cb62a6`; spacelift.io/pricing read 2026-10-28.

Research baseline: **2026-10-28**.

This report records Spacelift's access model: the spaces tree and how access
reaches a space, roles and the complete action catalogue, role bindings for
users, IdP groups, API keys and stacks, the two authorization strategies
(Identity Access Management and login policies) and the retired access
policies, single sign-on with SAML and OIDC, SCIM, MFA, backup credentials,
API keys, managed users and invitations, sessions, and the tier gate of each.
It is the input for infraweave's identity and authorization design. The policy
engine and the inputs of login and access policies belong to report 09; the
audit events that identity changes emit belong to report 19; OIDC federation
tokens issued to runs belong to report 12. Every field and enum table
introduced as `` `Type` (object) [G] `` or `` `Enum`, N values [G] `` is
generated from the introspection snapshot (see
[Inferred and undocumented](#inferred-and-undocumented)).

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` in `api/reference/spacelift/introspection.json` |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog, <https://docs.spacelift.io/product/changelog> |
| **[C]** | spacelift.io marketing pages, read 2026-10-28 |
| **[I]** | Inferred; the sentence says from what |
| *(unverified)* | No primary source confirms it; listed in the notes for 30-open-questions |

## 1. Model in one page

| Concept | What it is | Source |
|---|---|---|
| Space | A node in a single tree rooted at `root`; holds stacks, policies, contexts, modules, worker pools and cloud integrations | [D] [spaces] |
| Role | A named set of `Action` values; system roles are immutable, custom roles are account-defined | [D] [rbac], [G] `Role` |
| Actor | User, IdP group, API key or stack; each can hold role bindings | [D] [authz] |
| Role binding | (actor, role, space); takes effect on the space and every descendant | [D] [how-access], [G] `RoleBinding` |
| Authorization scheme | `MANAGED_USERS` (Identity Access Management) or `LOGIN_POLICIES`; exactly one is active | [G] `AuthorizationScheme`, [D] [iam-admin] |
| Session | A JWT-backed login with a precomputed per-space access snapshot | [G] `Session`, `SessionSpaceAccessRoleGrant` |
| Legacy access level | `SpaceAccessLevel` `NONE`/`READ`/`WRITE`/`ADMIN`; still the fallback when no role grants an action | [G] `SpaceAccessLevel`, [D] [rbac] |

Authorization is evaluated per space. An action on a subject is allowed when
the actor holds, in the subject's space, a role whose action set contains the
action; failing that, the legacy access level named as the action's fallback
decides [D] [rbac]. Entity-level permissions (one stack inside a space) do not
exist; the space is the smallest scope [D] [rbac].

## 2. Spaces

### 2.1 The tree, `root` and `legacy`

- Every account starts with two spaces, `root` and `legacy`. `root` is the top
  of the tree and cannot be changed; `legacy` holds resources created before
  spaces existed and is the only space where access policies applied
  [D] [spaces], [D] [legacy-space].
- Neither `root` nor `legacy` can be deleted. Any other space can be deleted
  only when it is empty: no child spaces, stacks, modules, worker pools,
  integrations or other attachable entities [D] [create-space].
- Space IDs are slugs, immutable and unique within the account; `name` is
  mutable display text [G] `Space.id`, `Space.name`.
- `parentSpace` is described as immutable on the object, but `spaceUpdate`
  takes a full `SpaceInput` including `parentSpace`, and the docs describe
  moving a space by changing its parent [G] `Space.parentSpace`,
  `spaceUpdate`; [D] [structuring]. The docs win for behaviour: a space can be
  re-parented, and its whole subtree moves with it. The Terraform provider
  agrees: `parent_space_id` is not `ForceNew`, and an update sends the new
  parent through `spaceUpdate` [S] [tf-space-update].
- A re-parent is refused unless every stack in the moved subtree can still
  reach the entities attached to it from outside the subtree under the
  inheritance rules [D] [structuring].
- Moving a stack or an attachable resource to another space is refused when a
  stack would lose access to something it uses (worker pool, cloud
  integration, context, policy) [D] [structuring], [D] [legacy-space].
- A space diagram above 100 spaces renders only the top of the tree in the UI
  [RN] 2026-06-10.

`Space` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `description` | `String!` | Description of the space |
| `id` | `ID!` | Unique (within a single organization), immutable name of the space. |
| `inheritEntities` | `Boolean!` | Toggle whether the space should inherit entities from its parent space |
| `name` | `String!` | Fancy, mutable name of the Space to show in the UI. |
| `parentSpace` | `ID` | Immutable identifier of the space that this space is child of |
| `accessLevel` | `SpaceAccessLevel!` | This user's access level to this Space. |
| `labels` | `[String!]!` | Optional list of Space labels. |
| `allowedActions` | `[Action!]!` | Actions that the user can perform in this space |

`SpaceInput` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `parentSpace` | `ID!` | Slug of the space that this space will be child of |
| `name` | `String!` | Name of the space |
| `description` | `String!` | Description of the space |
| `inheritEntities` | `Boolean!` | Toggle whether the space should inherit entities from its parent space |
| `labels` | `[String!]` | Optional list of Space labels. |

`SearchSpacesInput.actions` filters `spaces` to the spaces where the caller can
perform all of the listed actions [G] `SearchSpacesInput`.

Space operations [G]:

| Root field | Kind | Arguments | Returns |
|---|---|---|---|
| `space` | query | `id: ID!` | `Space` |
| `spaces` | query | `input: SearchSpacesInput` | `[Space!]!` (only spaces the caller can see) |
| `searchSpaces` / `searchSpacesSuggestions` | query | `SearchInput!` / `SearchSuggestionsInput!` | paged search |
| `spaceCreate` | mutation | `input: SpaceInput!` | `Space!` |
| `spaceUpdate` | mutation | `space: ID!`, `input: SpaceInput!` | `Space!` |
| `spaceDelete` | mutation | `space: ID!` | `Space!` |
| `allowNonRootAdminSpaceCreation` | query | none | `Boolean!` |
| `accountToggleAllowNonRootAdminSpaceCreation` | mutation | none | `Boolean!` |

Terraform: `spacelift_space` (`name` required; `parent_space_id` default
`root`; `inherit_entities` default `false`; `description`, `labels`) and data
sources `spacelift_space`, `spacelift_space_by_path` (path of names joined by
`/`, must be unique; absolute from `root` or relative to the current run's
space; usable only from a stack whose space and every non-root ancestor have
inheritance on), `spacelift_spaces` (filter by `labels`) and
`spacelift_current_space` [S] [tf-space], [S] `terraform-schema.json`. The resource needs root admin
[S] `terraform-schema.json` description of `spacelift_space`.

| Default disagreement | Value |
|---|---|
| UI "Enable inheritance" on create | on [D] [create-space] |
| Terraform `inherit_entities` | `false` [S] [tf-space] |
| GraphQL `SpaceInput.inheritEntities` | required, no default [G] |

### 2.2 Who may change the tree

| Actor | Create | Edit | Delete | Move | Toggle inheritance |
|---|---|---|---|---|---|
| Root space admin | anywhere | any space | any empty space except `root`/`legacy` | any | yes |
| Non-root space admin, setting off (default) | no | the spaces they administer [D] [create-space]; the settings page says they cannot modify spaces at all [D] [nonroot-spaces] *(unverified which holds)* | no | no | no |
| Non-root space admin, setting on | children of spaces they administer | those spaces and their children | those spaces and their children | within them | cannot disable; their new spaces always inherit |

Sources: [D] [nonroot-spaces], [D] [create-space], [D] [structuring]. The
setting is `allowNonRootAdminSpaceCreation` [G].

### 2.3 How access reaches a space

Three mechanisms extend a binding beyond the space it names [D] [how-access]:

| Mechanism | Direction | Always on? | What travels | Notes |
|---|---|---|---|---|
| Resident | none | yes | the bound role | A binding directly on the space |
| Access propagation | down to every descendant | yes; ignores the inheritance toggle | the bound role, unchanged | A binding on `root` reaches the whole account |
| Resource inheritance (`inheritEntities`) | up to the parent | per-space toggle; resource use chains while every link inherits [D]; Read reaching a grandparent follows by applying the rule again at the parent [I] from the same page | Read only | Anyone with any access to an inheriting space gets Read on the parent; it does not spread sideways to siblings. Stacks in the child can use the parent's attachable resources |
| Implicit stack self-read | the stack's home space, then down | yes, not removable | Read | No binding exists behind it |
| Implicit stack read-up | to each inheriting parent | follows the toggle | Read, folded to the Space reader role whatever the bound role | Shown as `Inherited from child space` |

The grant views expose these sources [G]:

`StackSpaceAccessGrantSource`, 3 values [G]:

| Value | Notes |
|---|---|
| `RESIDENT` | The grant lives on the queried space itself. |
| `PROPAGATED` | The grant lives on an ancestor space and reaches the queried space by propagating down. |
| `INHERITED` | The grant lives on a descendant space and reaches the queried space by inheritance up the tree (the descendant chain has inherit_entities enabled). |

`StackAccessMode`, 2 values [G]:

| Value | Notes |
|---|---|
| `RESIDING` | Only stacks whose home space is the queried space. |
| `ALL_WITH_ACCESS` | Every stack that can act in the queried space: residents, ancestor residents (implicit self-read propagates down), inheriting-descendant residents (read propagates up), and explicit binding holders. |

`StackSpaceAccessRoleGrant` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `role` | `Role!` | The role effective on the queried space. For INHERITED grants only read climbs up an inherit_entities edge, so this is the synthetic "Space reader" system role, which may differ from binding.role (the bound custom role). |
| `space` | `Space!` |  |
| `source` | `StackSpaceAccessGrantSource!` | How this grant reaches the queried space (resident, propagated, or inherited). |
| `binding` | `StackRoleBinding` | The explicit binding backing this grant. Null for the implicit self-read every stack has on its own home space. For INHERITED grants it names the originating custom role and descendant space the read climbed from, so binding.role intentionally differs from the effective role above. |

`UserSpaceAccessRoleGrant` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `role` | `Role!` |  |
| `space` | `Space!` |  |
| `propagated` | `Boolean!` | True when the grant lives on an ancestor space rather than the queried space. |
| `grantedVia` | `SpaceAccessGrantVia!` |  |

`SpaceAccessGrantVia` is a union of `UserRoleBinding`, `SpaceAccessGroup` [G].

`SessionGrantOrigin`, 2 values [G]:

| Value | Notes |
|---|---|
| `ASSIGNED_ROLE` | The login policy assigned this role via its roles rule. |
| `ACCESS_LEVEL` | Synthesized from the highest space_admin/space_write/space_read level present in the session's precomputed space access. |

`SessionSpaceAccessRoleGrant` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `role` | `Role!` |  |
| `origin` | `SessionGrantOrigin!` |  |

Space access listing queries [G]:

| Query | Lists | Strategy | Permission |
|---|---|---|---|
| `usersWithAccessToSpace(space, input)` | managed users with a binding on the space or an ancestor | `MANAGED_USERS` | admin on the space [D] [how-access] |
| `userGroupsWithAccessToSpace` | IdP groups | `MANAGED_USERS` | admin |
| `apiKeysWithAccessToSpace` | API keys | `MANAGED_USERS` | admin |
| `stacksWithAccessToSpace(space, input, mode)` | stacks, by `StackAccessMode` | both | read for `RESIDING`; admin for all [D] [how-access] |
| `userSessionsWithAccessToSpace` | people holding live sessions that reach the space | `LOGIN_POLICIES`; empty page under `MANAGED_USERS` | space admin |
| `apiKeySessionsWithAccessToSpace` | API keys with live sessions that reach the space | `LOGIN_POLICIES`; empty under `MANAGED_USERS` | space admin |

Each has a `...Suggestions` twin for search fields [G].

The UI "Granted via" labels map to the sources: `Current space` (resident,
including stack self-read), `Inherited from parent space` (propagation),
`Inherited from child space` (inheritance read-up), `Granted via group` (IdP
group) [D] [how-access].

### 2.4 SpaceAccessLevel

`SpaceAccessLevel`, 4 values [G]:

| Value | Notes |
|---|---|
| `NONE` |  |
| `READ` |  |
| `WRITE` |  |
| `ADMIN` |  |

`SpaceAccessLevel` is the pre-RBAC per-space level. It survives in four places:
`Space.accessLevel` (the caller's level) [G]; `SpaceAccessRule`, used by
the V1 user, group, Slack-channel and API key inputs [G]; the Terraform
`policy` blocks on `spacelift_user` and `spacelift_idp_group_mapping`, which
accept `READ`, `WRITE`, `ADMIN` and are deprecated in favour of
`spacelift_role_attachment` [S] `terraform-schema.json`; and the legacy
fallback column of the action catalogue (section 3.3). The legacy roles map
onto system roles as Reader to Space reader, Writer to Space writer and Admin
to Space admin [D] [rbac]. `managedUserRequestRoleChange(spaceId,
requestedAccessLevel)` asks for a level change [G]. `ManagedUserUpdateInput`
still carries only `accessRules`, so `managedUserUpdate` sets legacy levels,
not role bindings [G].

Semantics: `NONE` means no access; `READ`, `WRITE` and `ADMIN` are the
Reader, Writer and Admin levels whose meaning the legacy-fallback column of
section 3.3 defines action by action [D] [rbac]. The docs' worked example
grants levels (Write, Admin) and shows them propagating down and Read climbing
through inheritance exactly as role bindings do [D] [how-access]; that
legacy levels follow the same propagation rules in every case is [I] from that
example. The space diagram shows each user's level per space, except to a
`root` admin, who is admin everywhere [D] [create-space].

`SpaceAccessRule` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `space` | `ID!` | Slug of the space that this access rule affects. |
| `spaceAccessLevel` | `SpaceAccessLevel!` | Access level to be granted within the space. |

### 2.5 Root space admin and account-level rights

Space admin on `root` is account administration. The permission matrix
[D] [access-control]:

| Capability | Root space admin | Space admin | Space writer | Space reader |
|---|---|---|---|---|
| Set up SSO, VCS, manage sessions, manage login policies and IAM controls, manage audit trails | yes | no | no | no |
| Invite and revoke users; create, modify, delete roles | yes | no | no | no |
| Create, modify, delete API keys and IdP group mappings | yes | no | no | no |
| View roles, users, API keys, IdP group mappings | yes | yes | no | no |
| Manage role bindings | yes | only in spaces they administer | no | no |
| Manage spaces | yes | only spaces they administer | no | no |
| Manage stack settings, worker pools, contexts | yes | yes | no | no |
| Manage stack environment variables; trigger runs | yes | yes | yes | no |
| View stacks, spaces, worker pools, contexts | yes | yes | yes | yes |

Non-root admins gained read-only visibility of roles, users, keys and group
mappings, and binding management in their spaces, on 2025-11-17 [RN].

Two account-level overrides sit outside both strategies. GitHub organization
admins, SSO admins (the integration's first successful login,
`SAMLSettings.adminLogin` / `OIDCSettings.adminLogin`) and owners of private
GitHub accounts always log in as admins, and login policies are not evaluated
for them [D] [iam], [D] [iam-admin], [D] [login-policy], [D] [sso],
[G] `SAMLSettings`.

## 3. Roles and actions

### 3.1 Role object and operations

`Role` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` |  |
| `slug` | `String!` |  |
| `isSystem` | `Boolean!` |  |
| `name` | `String!` |  |
| `description` | `String!` |  |
| `actions` | `[Action!]!` |  |
| `roleBindingsCount` | `Int!` |  |
| `roleBindings` | `[RoleBinding!]!` |  |

`RoleInput` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `name` | `String!` | Human-readable, free-form name. |
| `description` | `String` | Human-readable, free-form description. |
| `actions` | `[Action!]!` | Permissions associated with the role. |

`RoleUpdateInput` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `name` | `String` | Human-readable, free-form name. |
| `description` | `String` | Human-readable, free-form description. |
| `Actions` | `[Action!]` | Permissions associated with the role. |

`RoleUpdateInput` spells its action list `Actions` with a capital A, unlike
every other input field [G].

| Root field | Kind | Notes |
|---|---|---|
| `roles` | query | All roles visible to the caller [G] |
| `role(id)` | query | [G] |
| `roleCreate(input: RoleInput!)` | mutation | Root admin only [D] [access-control], [S] `spacelift_role` description |
| `roleUpdate(id, input: RoleUpdateInput!)` | mutation | System roles cannot be modified [D] [rbac] |
| `roleDelete(id)` | mutation | System roles cannot be deleted [D] [rbac] |

Identifiers: `id` is a ULID; `slug` is generated from `name` (for example
`space-admin`) and is what login policies and policy inputs use
[S] `terraform-schema.json` `spacelift_role`, [D] [login-policy].
Terraform: `spacelift_role` (`name`, `actions` required; `description`;
computed `slug`), data sources `spacelift_role` (by `role_id` or `slug`) and
`spacelift_role_actions` (every assignable action) [S] `terraform-schema.json`.

### 3.2 System roles

| Role | Slug | Contents | Source |
|---|---|---|---|
| Space reader | `space-reader` | View stacks, contexts, policies, runs; comment on runs | [D] [rbac]; slug from the example in [D] [roles-stacks] |
| Space writer | `space-writer` | Reader plus trigger runs, run tasks, edit stack environment variables; includes `STACK_STATE_READ` and `STACK_STATE_DOWNLOAD` | [D] [rbac], [D] [roles-stacks]; slug from [D] [migrate-spaces] |
| Space admin | `space-admin` | Writer plus create and change stacks, contexts, policies, Intent projects, space settings, bindings in its spaces | [D] [rbac]; slug from [D] [roles-stacks] and [S] `terraform-schema.json` `spacelift_role` |
| Worker pool controller | *(unverified)* | `SPACE_READ`, `WORKER_POOL_CREATE`, `WORKER_POOL_UPDATE`, `WORKER_POOL_DELETE`; for the Kubernetes controller | [D] [rbac] |
| Drift detection role | *(unverified)* | A system role for managing a stack's drift detection settings | [RN] 2026-02-16 |

The invite form offers Space reader, Space writer, Space admin and Worker pool
controller [D] [invite]. The exact action set of each built-in role is not
published; `Role.actions` returns it at run time [G] *(unverified)*.

### 3.3 The action catalogue

`Action` has 150 values [G]. The table groups them by subject, gives the UI
label and the legacy access level that grants the action when no role does
[D] [rbac]. Labels marked "not documented" appear only in the schema.

| `Action` value | UI label | Legacy fallback | Schema note |
|---|---|---|---|
| **Space** | | | |
| `SPACE_ADMIN` | Admin | Admin | |
| `SPACE_READ` | Read (required to see anything in the space) | Reader | |
| `SPACE_WRITE` | Write | Writer | |
| `SPACE_SHARE_MODULE` | Share module (modules from other spaces may be shared into it) | Writer | |
| **Runs** | | | |
| `RUN_CANCEL` | Cancel run | Reader | |
| `RUN_CANCEL_BLOCKING` | Cancel blocking run | Writer | |
| `RUN_COMMENT` | Comment on run | Reader | |
| `RUN_CONFIRM` | Confirm run | Writer | |
| `RUN_DISCARD` | Discard run | Writer | |
| `RUN_KILL` | Kill run | Reader | |
| `RUN_KILL_BLOCKING` | Kill blocking run | Writer | |
| `RUN_PRIORITIZE_SET` | Prioritize run | Writer | |
| `RUN_PROMOTE` | Promote run | Writer | |
| `RUN_PROPOSE_LOCAL_WORKSPACE` | Propose run from local workspace | Writer | |
| `RUN_PROPOSE_WITH_OVERRIDES` | Propose run with overrides | Writer | |
| `RUN_RETRY` | Retry run | Reader | |
| `RUN_RETRY_BLOCKING` | Retry blocking run | Writer | |
| `RUN_REVIEW` | Review run (does not replace the approval policy) | Writer | |
| `RUN_STOP` | Stop run | Reader | |
| `RUN_STOP_BLOCKING` | Stop blocking run | Writer | |
| `RUN_TARGETED_REPLAN` | Replan targeted run | Writer | |
| `RUN_TRIGGER` | Trigger run | Writer | |
| `RUN_TRIGGER_WITH_CUSTOM_RUNTIME_CONFIG` | Trigger run with custom runtime config | Admin | |
| **Tasks** | | | |
| `TASK_CREATE` | Create task | Writer | |
| **Stacks** | | | |
| `STACK_ADD_CONFIG` | Add stack config (environment variables, mounted files) | Writer | |
| `STACK_CREATE` | Create stack | Admin | |
| `STACK_DELETE` | Delete stack | Admin | |
| `STACK_DELETE_CONFIG` | Delete stack config | Writer | |
| `STACK_DISABLE` | Disable stack | Admin | |
| `STACK_ENABLE` | Enable stack | Admin | |
| `STACK_LOCK` | Lock stack | Writer | |
| `STACK_MANAGE` | Manage stack (also required to bind a role to the stack) | Admin | |
| `STACK_MANAGED_STATE_IMPORT` | Import managed state | Admin | |
| `STACK_MANAGED_STATE_ROLLBACK` | Rollback stack managed state | Admin | |
| `STACK_RESLUG` | Re-slug stack | Admin | |
| `STACK_SET_CURRENT_COMMIT` | Set stack current commit | Writer | |
| `STACK_SET_STAR` | Set stack star | Reader | |
| `STACK_STATE_DOWNLOAD` | Download stack state | Writer | |
| `STACK_STATE_READ` | Read stack state (state backend) | Writer | |
| `STACK_SYNC_COMMIT` | Sync stack commit | Writer | |
| `STACK_UNLOCK` | Unlock stack (own lock only) | Writer | |
| `STACK_UNLOCK_FORCE` | Force unlock stack (any lock) | Admin | |
| `STACK_UPDATE` | Update stack | Admin | |
| `STACK_UPLOAD_LOCAL_WORKSPACE` | Upload local workspace for stack | Writer | |
| `STACK_WORKSPACE_LOCK` | Lock stack workspace (state lock) | Admin | |
| `STACK_WORKSPACE_UNLOCK` | Unlock stack workspace | Admin | |
| **Contexts** | | | |
| `CONTEXT_CREATE` | Create context | Admin | |
| `CONTEXT_DELETE` | Delete context | Admin | |
| `CONTEXT_UPDATE` | Update context | Admin | |
| **Worker pools** | | | |
| `WORKER_DRAIN_SET` | Drain worker | Admin | |
| `WORKER_POOL_CREATE` | Create worker pool | Admin | |
| `WORKER_POOL_CYCLE` | Cycle worker pool | Admin | |
| `WORKER_POOL_DELETE` | Delete worker pool | Admin | |
| `WORKER_POOL_RESET` | Reset worker pool | Admin | |
| `WORKER_POOL_UPDATE` | Update worker pool | Admin | |
| **Modules** | | | |
| `MODULE_CREATE` | Create module | Admin | |
| `MODULE_DISABLE` | Disable module | Admin | |
| `MODULE_ENABLE` | Enable module | Admin | |
| `MODULE_MARK_AS_BAD` | Mark module as bad | Writer | |
| `MODULE_PROPOSE_LOCAL_WORKSPACE` | Propose local workspace for module | Writer | |
| `MODULE_PUBLISH` | Publish module | Admin | |
| `MODULE_TRIGGER_VERSION` | Trigger module version | Writer | |
| `MODULE_UPLOAD_LOCAL_WORKSPACE` | Upload local workspace for module | Writer | |
| `MODULE_VERSION_DELETE` | Delete module version | Writer | |
| **Terraform providers** | | | |
| `TERRAFORM_PROVIDER_CREATE` | Create Terraform provider | Admin | |
| `TERRAFORM_PROVIDER_DELETE` | Delete Terraform provider | Admin | |
| `TERRAFORM_PROVIDER_SET_VISIBILITY` | Set Terraform provider visibility | Admin | |
| `TERRAFORM_PROVIDER_UPDATE` | Update Terraform provider | Admin | |
| `TERRAFORM_PROVIDER_VERSION_CREATE` | Create Terraform provider version | Writer | |
| `TERRAFORM_PROVIDER_VERSION_DELETE` | Delete Terraform provider version | Writer | |
| `TERRAFORM_PROVIDER_VERSION_PUBLISH` | Publish Terraform provider version | Writer | |
| `TERRAFORM_PROVIDER_VERSION_REGISTER_PLATFORM` | Register Terraform provider version platform | Writer | |
| `TERRAFORM_PROVIDER_VERSION_REVOKE` | Revoke Terraform provider version | Writer | |
| `TERRAFORM_PROVIDER_VERSION_UPDATE` | Update Terraform provider version | Writer | |
| **Intent** | | | |
| `INTENT_DEPENDENCIES_ADD` | Add intent dependencies | Admin | |
| `INTENT_DEPENDENCIES_REMOVE` | Remove intent dependencies | Admin | |
| `INTENT_POLICY_CREATE` | Create intent policies | Admin | |
| `INTENT_POLICY_DELETE` | Delete intent policies | Admin | |
| `INTENT_POLICY_UPDATE` | Update intent policies | Admin | |
| `INTENT_PROJECT_CLOUD_INTEGRATION_ATTACH` | Attach AWS integration to intent project | Admin | |
| `INTENT_PROJECT_CLOUD_INTEGRATION_DETACH` | Detach AWS integration from intent project | Admin | |
| `INTENT_PROJECT_CONFIG_ADD` | Add intent project config | Admin | |
| `INTENT_PROJECT_CONFIG_DELETE` | Delete intent project config | Admin | |
| `INTENT_PROJECT_CONFIG_UPDATE` | Update intent project config | Admin | |
| `INTENT_PROJECT_CREATE` | Create intent project | Admin | |
| `INTENT_PROJECT_DELETE` | Delete intent project | Admin | |
| `INTENT_PROJECT_DISABLE` | Disable intent project | Admin | |
| `INTENT_PROJECT_ENABLE` | Enable intent project | Admin | |
| `INTENT_PROJECT_LOCK` | Lock intent project | Admin | |
| `INTENT_PROJECT_POLICY_ATTACH` | Attach policy to intent project | Admin | |
| `INTENT_PROJECT_POLICY_DETACH` | Detach policy from intent project | Admin | |
| `INTENT_PROJECT_UNLOCK` | Unlock intent project | Admin | |
| `INTENT_PROJECT_UPDATE` | Update intent project | Admin | |
| `INTENT_RESOURCE_CREATE` | Create intent resources | Admin | |
| `INTENT_RESOURCE_DELETE` | Delete intent resources | Admin | |
| `INTENT_RESOURCE_IMPORT` | Import intent resources | Admin | |
| `INTENT_RESOURCE_OPERATION_REVIEW` | Review intent resource operation | Admin | |
| `INTENT_RESOURCE_REFRESH` | Refresh intent resources | Admin | |
| `INTENT_RESOURCE_RESUME` | Resume intent resources | Admin | |
| `INTENT_RESOURCE_UPDATE` | Update intent resources | Admin | |
| `INTENT_STATE_EJECT` | Eject from intent state | Admin | |
| `INTENT_STATE_READ` | Read intent state | Admin | |
| **Drift detection** | | | |
| `DRIFT_DETECTION_INTEGRATION_CREATE` | Create drift detection integration | Admin | |
| `DRIFT_DETECTION_INTEGRATION_UPDATE` | Update drift detection integration | Admin | |
| `DRIFT_DETECTION_INTEGRATION_DELETE` | Delete drift detection integration | Admin | |
| **Templates** (named `BLUEPRINT` in the enum) | | | |
| `CREATE_BLUEPRINTS_VERSIONED_GROUP` | Create template | Admin | |
| `UPDATE_BLUEPRINTS_VERSIONED_GROUP` | Update template | Admin | |
| `DELETE_BLUEPRINTS_VERSIONED_GROUP` | Delete template | Admin | |
| `CREATE_BLUEPRINT_DEPLOYMENT` | Create template deployment | Admin | |
| `DELETE_BLUEPRINT_DEPLOYMENT` | Delete template deployment | Admin | |
| `UPGRADE_BLUEPRINT_DEPLOYMENT` | Upgrade deployment version | Admin | |
| `UPDATE_BLUEPRINT_DEPLOYMENT` | covered by Upgrade | Admin | Deprecated: use `UPGRADE_BLUEPRINT_DEPLOYMENT` |
| `UPDATE_BLUEPRINT_DEPLOYMENT_INPUTS` | covered by Upgrade | Admin | Deprecated: use `UPGRADE_BLUEPRINT_DEPLOYMENT` |
| `ROLLBACK_BLUEPRINT_DEPLOYMENT` | Rollback template deployment | Admin | |
| `TEST_BLUEPRINT_DEPLOYMENT` | Test template deployment | Admin | |
| **Stack attachments and integrations** | | | |
| `STACK_CONTEXT_ATTACH` | Attach context | Admin | "Disabled. Do not use." |
| `STACK_CONTEXT_DETACH` | Detach context | Admin | "Disabled. Do not use." |
| `STACK_POLICY_ATTACH` | Attach policy | Admin | "Disabled. Do not use." |
| `STACK_POLICY_DETACH` | Detach policy | Admin | "Disabled. Do not use." |
| `STACK_GCP_INTEGRATION_CREATE` | Create GCP integration | Admin | "Disabled. Do not use." |
| `STACK_GCP_INTEGRATION_DELETE` | Delete GCP integration | Admin | "Disabled. Do not use." |
| `STACK_AWS_INTEGRATION_ATTACH` | Attach AWS integration | Admin | "Disabled. Do not use." |
| `STACK_AWS_INTEGRATION_DETACH` | Detach AWS integration | Admin | "Disabled. Do not use." |
| `STACK_AWS_INTEGRATION_UPDATE` | Update AWS integration | Admin | "Disabled. Do not use." |
| `STACK_AZURE_INTEGRATION_ATTACH` | Attach Azure integration | Admin | "Disabled. Do not use." |
| `STACK_AZURE_INTEGRATION_DETACH` | Detach Azure integration | Admin | "Disabled. Do not use." |
| `STACK_AZURE_INTEGRATION_UPDATE` | Update Azure integration | Admin | "Disabled. Do not use." |
| **Stack role bindings** | | | |
| `STACK_ROLE_BINDING_CREATE` | Create stack role binding | Admin | "Disabled. Do not use." |
| `STACK_ROLE_BINDING_UPDATE` | Update stack role binding | Admin | "Disabled. Do not use." |
| `STACK_ROLE_BINDING_DELETE` | Delete stack role binding | Admin | "Disabled. Do not use." |
| **Stack webhooks** | | | |
| `STACK_WEBHOOK_CREATE` | Create webhook | Admin | "Disabled. Do not use." |
| `STACK_WEBHOOK_UPDATE` | Update webhook | Admin | "Disabled. Do not use." |
| `STACK_WEBHOOK_DELETE` | Delete webhook | Admin | "Disabled. Do not use." |
| **Scheduling** | | | |
| `STACK_SCHEDULED_DELETE_CREATE` | Create scheduled delete | Admin | |
| `STACK_SCHEDULED_DELETE_UPDATE` | not documented | *(unverified)* | |
| `STACK_SCHEDULED_DELETE_DELETE` | Delete scheduled delete | Admin | |
| `STACK_SCHEDULED_TASK_CREATE` | Create scheduled task | Admin | |
| `STACK_SCHEDULED_TASK_UPDATE` | Update scheduled task | Admin | |
| `STACK_SCHEDULED_TASK_DELETE` | Delete scheduled task | Admin | |
| `STACK_SCHEDULED_RUN_CREATE` | Create scheduled run | Admin | |
| `STACK_SCHEDULED_RUN_UPDATE` | Update scheduled run | Admin | |
| `STACK_SCHEDULED_RUN_DELETE` | Delete scheduled run | Admin | |
| **Repos** | | | |
| `REPO_CREATE` | Create repo | Admin | |
| `REPO_UPDATE` | Update repo | Writer | |
| `REPO_DELETE` | Delete repo | Admin | |
| `REVISION_CREATE` | Create commit | Writer | |
| **AI integrations** | | | |
| `AI_INTEGRATION_CREATE` | Create AI integration | Admin | |
| `AI_INTEGRATION_UPDATE` | Update AI integration | Admin | |
| `AI_INTEGRATION_DELETE` | Delete AI integration | Admin | |
| `AI_INTEGRATION_TOGGLE` | Toggle AI integration | Admin | |

Notes on the catalogue:
- The 18 stack attachment, stack role binding and stack webhook actions carry
  the schema description "Disabled. Do not use." while the docs list them as
  assignable [G] `Action`, [D] [rbac]. The schema is the higher source for
  the current API: these values exist but are not usable in roles yet
  *(unverified)*.
- AI integration actions are checked in the integration's own space; the
  Spacelift-provided integration has no space, so toggling it is checked in
  `root`, and it cannot be created, updated or deleted [D] [rbac].
- Assigning a deprecated action is expected to fail [D] [rbac].
- Legacy blueprints (not templates) have no RBAC actions; only Space admin
  can manage them [D] [rbac].
- `MODULE_UPLOAD_LOCAL_WORKSPACE` and `MODULE_PROPOSE_LOCAL_WORKSPACE` were
  added on 2026-08-04 so local preview does not need `SPACE_WRITE` [RN].
  Drift detection actions arrived 2026-01-28 and `SPACE_SHARE_MODULE`
  2026-01-09 [RN].
- `Space.allowedActions` and `User.actions` (`[SpaceActions!]!`) report what
  the caller can do per space [G].

## 4. Role bindings

### 4.1 Binding objects

`RoleBinding` is a union of `UserRoleBinding`, `ApiKeyRoleBinding`, `UserGroupRoleBinding`, `StackRoleBinding` [G].

`RoleBindingInput` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `roleID` | `ID!` | ID of the role. |
| `spaceID` | `ID!` | ID of the space. |

`UserRoleBinding` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` |  |
| `roleID` | `ID!` |  |
| `role` | `Role!` |  |
| `spaceID` | `ID!` | **Deprecated:** Use space field instead. |
| `space` | `Space!` |  |
| `userID` | `ID!` |  |
| `user` | `ManagedUser!` |  |

`UserGroupRoleBinding` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` |  |
| `roleID` | `ID!` |  |
| `spaceID` | `ID!` | **Deprecated:** Use space field instead. |
| `space` | `Space!` |  |
| `userGroupID` | `ID!` | **Deprecated:** Use userGroup field instead. |
| `userGroup` | `ManagedUserGroup!` |  |

`ApiKeyRoleBinding` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` |  |
| `role` | `Role!` |  |
| `spaceID` | `ID!` | **Deprecated:** Use space field instead. |
| `space` | `Space!` |  |
| `apiKeyID` | `ID!` |  |
| `apiKey` | `ApiKey!` |  |

`StackRoleBinding` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` |  |
| `role` | `Role!` |  |
| `space` | `Space!` |  |
| `stackID` | `ID!` |  |
| `stackName` | `String!` |  |

`FlowsProjectRoleBinding` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | The binding ID. |
| `flowsProjectID` | `ID!` | The external Flows project UUID this binding applies to. |
| `space` | `Space!` | The space this binding grants access to. |
| `role` | `Role!` | The role granted in the space. |

Binding inputs are uniform: `roleID`, `spaceID` and one actor ID (`userID`,
`userGroupID`, `apiKeyID`, `stackID`, `flowsProjectID`) [G]
`UserRoleBindingInput`, `UserGroupRoleBindingInput`, `ApiKeyRoleBindingInput`,
`StackRoleBindingInput`, `FlowsProjectRoleBindingInput`. Each batch input
wraps a `bindings` list [G].

| Actor | Get | Create | Batch create | Update | Delete |
|---|---|---|---|---|---|
| User | `userRoleBinding` | `userRoleBindingCreate` | `userRoleBindingBatchCreate` | `userRoleBindingUpdate` | `userRoleBindingDelete` |
| IdP group | `userGroupRoleBinding` | `userGroupRoleBindingCreate` | `userGroupRoleBindingBatchCreate` | `userGroupRoleBindingUpdate` | `userGroupRoleBindingDelete` |
| API key | `apiKeyRoleBinding` | `apiKeyRoleBindingCreate` | `apiKeyRoleBindingBatchCreate` | `apiKeyRoleBindingUpdate` | `apiKeyRoleBindingDelete` |
| Stack | `stackRoleBinding` | `stackRoleBindingCreate` | `stackRoleBindingBatchCreate` | `stackRoleBindingUpdate` (role and space only) | `stackRoleBindingDelete` |
| Flows project | `flowsProjectRoleBinding` | none | `flowsProjectRoleBindingBatchCreate` (admin on every target space) | none | `flowsProjectRoleBindingDelete` |

Source: [G] root fields. Terraform has one resource, `spacelift_role_attachment`,
with exactly one of `api_key_id`, `idp_group_mapping_id`, `stack_id`,
`user_id`; its ID is `<PREFIX>/<binding id>` with prefix `API`, `IDP`,
`STACK` or `USER` [S] [tf-role-attachment]. It calls the user batch-create
mutation for users and the single-create mutations for the rest
[S] `terraform-operations.json`.

### 4.2 Rules common to all actors

- Bindings are additive: several roles in one space, different roles in
  different spaces, and group-derived plus direct roles all combine
  [D] [roles-users], [D] [roles-groups].
- Under Identity Access Management, binding and role changes apply at once;
  Spacelift recomputes the per-session space access instead of ending
  sessions (since 2026-03-19) [RN] 2026-03-19, 2026-03-30. The role-assignment
  page still says such changes force re-authentication when needed
  [D] [roles-users]; the later changelog entry is the newer statement.
- Under login policies, roles come from the `roles` rule at login and stay
  fixed for the session; a newly created space is visible to its creator at
  once, but to others only after they log in again [D] [roles-users].
- A non-root space admin manages bindings only in spaces they administer
  [D] [rbac].
- A managed user with no space access receives a JWT with an empty access
  snapshot rather than HTTP 403 (since 2026-04-14) [RN]. Under IAM, a
  non-admin with no role anywhere is refused at login [D] [iam-admin]. The two
  statements disagree; the later changelog entry is recorded and the
  conflict is an open question *(unverified)*.

### 4.3 Users and IdP groups

- A user's effective permissions are the union of direct bindings and the
  bindings of the IdP groups their identity provider reports at login
  [D] [iam]. Manually granted higher permissions survive a lower group
  mapping [D] [iam].
- Group matching is exact, including case and white space
  [D] [iam-admin]. Microsoft Entra ID sends group object IDs, so mappings use
  IDs [D] [entra-oidc].
- Group membership comes only from the login assertion (SAML `Teams` or OIDC
  `groups`); SCIM-provisioned groups do not populate it [D] [scim].
- GitHub team membership is transitive: a member of a child team is reported
  as a member of its parent [D] [login-policy].
- Slack channels can hold access rules through
  `ManagedUserGroupIntegration` with `integrationType` `SLACK`
  [G] `ManagedUserGroupIntegration`, [D] [iam-admin].

### 4.4 API keys

API keys get roles by direct binding, by virtual IdP group membership
(`teams`) or through the login policy [D] [roles-apikeys]. See section 8.

### 4.5 Stacks

Stack role bindings let runs of a stack act on Spacelift through the
Terraform provider, replacing the `administrative` flag [D] [roles-stacks].

| Rule | Detail |
|---|---|
| Permission to bind | `STACK_MANAGE` (or Space admin) on the stack's space and Space admin on the target space, to prevent escalation |
| Scope | The bound role propagates to the target space and all descendants |
| Cross-space | A stack may hold roles in sibling spaces |
| `root` restriction | A role on `root` is allowed only when the stack itself lives in `root` |
| Stack moves | Bindings do not follow a stack to another space |
| Policy input | `stack.roles` lists `id` (slug), `name`, `slug`, `ulid` |
| Audit | Audit webhooks carry `actor_roles` (role slugs) for stack actors (report 19) |
| External state access | Reading another stack's state needs `STACK_STATE_READ` and `STACK_STATE_DOWNLOAD` in that stack's space |

Sources: [D] [roles-stacks], [D] [stack-access-policy] (input shape).

The `administrative` flag was deprecated on 2025-11-10 and disabled on
2026-06-01. Each stack that had it received a Space admin binding on its own
space; the flag is accepted but behaves as `false`. Terraform users import the
created binding as `STACK/<binding id>` [D] [roles-stacks], [RN] 2025-11-10.
Self-hosted announces the same migration for an upcoming major version
[D] [sh-changelog].

## 5. Authorization strategies

### 5.1 Identity Access Management and login policies

`AuthorizationScheme`, 2 values [G]:

| Value | Notes |
|---|---|
| `LOGIN_POLICIES` |  |
| `MANAGED_USERS` |  |

| Aspect | `MANAGED_USERS` (Identity Access Management) | `LOGIN_POLICIES` |
|---|---|---|
| Who may log in | Admins, plus anyone holding at least one role binding | Whoever the login policies `allow` or `admin` |
| Role source | Bindings to users, groups, API keys | The policy `roles` rule; bindings ignored |
| Invitations | Sent by email | Queued (`QUEUED`) and sent on switch to IAM |
| Personal API keys | Active | Become ordinary API keys |
| API keys | Bindings apply | Keys with root admin keep working; every other key needs a login policy that lets it in |
| Deactivated user | Cannot log in [I] from IAM requiring a role binding | Still logs in unless the policy denies |

Sources: [D] [iam-admin], [D] [login-policy]. The switch is
`accountUpdateAuthorizationScheme(schema)` and invalidates every session
except the caller's; both configurations persist side by side
[G], [D] [iam-admin]. Changing any login policy also invalidates every other
session [D] [login-policy].

### 5.2 Login policy rules

Login policies live only in `root`, apply account-wide and cannot be attached
to stacks [D] [login-policy], [D] [access-control]. Only admins of `root` or
`legacy`, and stacks holding a Space admin role on `root`, can change them
[D] [access-control]. Policy engine details are in report 09.

| Rule | Type | Effect |
|---|---|---|
| `allow` | boolean | Log in as non-admin |
| `admin` | boolean | Log in as account admin (implies allow) |
| `deny` | boolean | Refuse, overriding allow and admin |
| `deny_admin` | boolean | Refuse admin, overriding other rules |
| `deny_message` | set of strings | Shown when the final decision is a denial; messages from all policies are de-duplicated, sorted, joined with `;` and truncated at 1,024 characters with `...`; recorded on the `account.login_failed` audit event |
| `roles[space_id]` | set of role slugs | RBAC role assignment per space |
| `team` | set of strings | Replaces `session.teams` when non-empty |
| `space_admin`, `space_write`, `space_read` | sets of space IDs | Deprecated legacy level assignment |

Sources: [D] [login-policy], [RN] 2026-10-14. With no rule matching, the
login is denied; with no login policy at all, Spacelift behaves as if
`allow` were `input.session.member` [D] [login-policy]. Some denials happen
before policy evaluation (plan without login policies, or no policies, for a
non-member) and carry no message [D] [login-policy].

Input highlights [D] [login-policy]:

| Field | Meaning |
|---|---|
| `session.login` | Username; for OIDC API keys `api::oidc::<client-id>::<key-id>` |
| `session.member` | GitHub: organization membership (or login equals account name for personal accounts); SSO: always true |
| `session.teams` | GitHub teams, SAML `Teams`, OIDC `groups` (after claim mapping) |
| `session.idp_subject` | Raw upstream `sub`; GitHub node ID for GitHub; empty only for secret API keys (added 2026-05-19 [RN]) |
| `roles` | Every role with `id`, `name`, `slug`, `ulid` |
| `spaces` | The account's spaces |
| `request.remote_ip`, `request.timestamp_ns` | Request context |
| `slack` | Replaces `session` for Slack sign-in; `request` then has only a timestamp |

The full schema is published as the `LOGIN` entry of
`https://app.spacelift.io/.well-known/policy-contract.json` [D] [login-policy];
the mirror does not inline it (the page includes a snippet file that is not
in the mirror).

### 5.3 Access policies (retired)

Access policies were per-stack or per-module Rego rules (`read`, `write`,
`deny`, `deny_write`) evaluated per request in the `legacy` space, with a
500 ms budget for evaluating all of them in one `stacks` call
[D] [stack-access-policy]. They were deprecated and fully disabled on
2026-05-30 [D] [stack-access-policy], [D] [migrate-spaces]. Residue in the
schema: `BillingTierFeature.ACCESS_POLICIES` and
`User.cannotEvaluateAccessPolicies` [G]. Once a login policy assigns a role in
`legacy` to a user, access policies stop applying to that user
[D] [legacy-space].

Migration path [D] [migrate-spaces], [D] [legacy-space]:

| Step | Action |
|---|---|
| 1 | Audit `policies(input: {type: ACCESS})` or `spacelift_policy` with `type = "ACCESS"` |
| 2 | Design spaces along permission boundaries |
| 3 | Create a child of `root` with inheritance on |
| 4 | Move attachables from `legacy` to `root`, then stacks to the new space, then attachables to it |
| 5 | Grant access by IAM group mappings or login policy `roles` |
| 6 | Test, then delete the access policies |

Patterns without a direct equivalent: per-request time or IP checks (login
policies evaluate once per session), different access to stacks in one space
(split into spaces), and denial based on runtime stack properties (move to
approval policies) [D] [migrate-spaces].

## 6. Single sign-on

### 6.1 Login providers

`IdentityProvider`, 8 values [G]:

| Value | Notes |
|---|---|
| `UNKNOWN` |  |
| `GITHUB` |  |
| `GOOGLE` |  |
| `GITLAB` |  |
| `MICROSOFT` |  |
| `OIDC` |  |
| `SAML` |  |
| `EMPTY` | Default setting for self-hosted installations before SAML or OIDC is configured. Not valid in SaaS. |

Accounts are created with a managed provider: GitHub, GitLab, Google or
Microsoft [D] [sso], [D] [security]. Configuring SSO disables that provider
and its admin; the first successful SSO login becomes the SSO admin;
unlinking SSO restores the managed provider and ends existing sessions. A new
provider creates new usernames, which take new seats [D] [sso]. IdP-initiated
SAML is not supported: such requests get `303 See Other` to an SP-initiated
flow [D] [sso]. `hasSSO` reports whether SSO is on [G].

### 6.2 SAML

| Setting | Values | Source |
|---|---|---|
| IdP metadata | Static XML (`idpMetadata`) or a URL (`idpMetadataURL`, "dynamic") | [G] `SAMLSettings`, `samlCreate(metadata, dynamic, nameIDFormat, claimMapping)` |
| NameID format | `TRANSIENT`, `EMAIL_ADDRESS`, `PERSISTENT`; the NameID is the login and the seat key | [G] `SAMLNameIDFormat`, [D] [sso] |
| Attributes | `FirstName`, `LastName` (display name), `Teams` (groups, case-sensitive name) | [D] [sso] |
| Attribute mapping | IdP attribute name to Spacelift attribute (`Teams`, `FirstName`, `LastName`), since 2025-10-15 | [D] [sso], [RN] |
| SP endpoints | ACS `https://<account>.app.spacelift.io/saml/acs`; entity ID `https://<account>.app.spacelift.io/saml/metadata`; US region uses `app.us.spacelift.io` | [D] [okta-saml] |
| Bindings | HTTP-Redirect and HTTP-POST | [D] [sso] |
| Admin | `adminLogin`, always admin | [G] |

`SAMLSettings` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `adminLogin` | `String` | Login of the user who always gets an administrative access to the account. This is meant to prevent locking yourself out of the account if the settings are incorrect. |
| `idpMetadata` | `String` | Identity provider metadata, if statically provided. |
| `idpMetadataURL` | `String` | Identity provider metadata URL, if the provider supports dynamic metadata retrieval. |
| `nameIDFormat` | `SAMLNameIDFormat!` | The format used when requesting the NameID (user/login name) from the identity provider. |
| `claimMapping` | `ClaimMapping!` | contains custom claims mapping |

### 6.3 OIDC

| Setting | Values | Source |
|---|---|---|
| Client | `clientId`, `clientCredentials` (secret), `identityProviderHost` (issuer or discovery URL) | [G] `oidcCreate` |
| Scopes | `email` required with an `email` claim; `profile` and `groups` requested when the discovery document lists them | [D] [sso] |
| Claim mapping | IdP claim name to `groups` (for example `cognito:groups`), since 2025-10-03 | [D] [sso], [RN] |
| Redirect | The "Authorized redirect URL" shown in the setup drawer | [D] [gitlab-oidc] |
| Save | Saving triggers a first login; SSO becomes active only if it succeeds | [D] [entra-oidc] |

`OIDCSettings` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `adminLogin` | `String` | Login of the user who always gets an administrative access to the account. This is meant to prevent locking yourself out of the account if the settings are incorrect. |
| `clientId` | `String!` | client id authorized by Identity Provider to initialize Oauth 2.0 authorization flow |
| `identityProviderHost` | `String!` | address of the Identity Provider to initialize Oauth 2.0 authorization flow (e.g. https://accounts.google.com) |
| `claimMapping` | `ClaimMapping!` | contains custom claims mapping |

`ClaimMapping` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `entries` | `[ClaimMappingEntry!]!` |  |

`ClaimMappingEntry` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `name` | `String!` |  |
| `value` | `String!` |  |

SSO operations [G]: `samlSettings`, `oidcSettings` (queries); `samlCreate`,
`samlUpdate(input: SAMLUpdateInput!)` and `oidcUpdate(input: OIDCUpdateInput!)`
(claim mapping only), `samlDelete`, `oidcDelete`, `oidcCreate`. Changing
metadata, client or host means delete and re-create [I] from the update
inputs carrying only `claimMapping`.

### 6.4 Per-IdP setup guides

Spacelift publishes guides for AWS IAM Identity Center and Okta (SAML) and for
GitLab, Okta, OneLogin and Microsoft Entra ID (OIDC), and states that any SAML
2.0 provider, or any OIDC provider that supports the `email` scope, works
[D] [sso]. Every guide recommends backup credentials first (section 7.3) and
needs a Spacelift admin to open **Organization settings** > **Single Sign-On**.

What Spacelift shows and what it needs back:

| Protocol | Spacelift shows (copy to the IdP) | Spacelift needs (from the IdP) | Source |
|---|---|---|---|
| SAML | `Single sign-on URL` (ACS, `https://<account>.app.spacelift.io/saml/acs`) and `Entity ID (audience)` (`https://<account>.app.spacelift.io/saml/metadata`); `app.us.spacelift.io` in the US region | IdP metadata URL (with "Dynamic configuration" on) or static metadata XML; a NameID format | [D] [okta-saml], [D] [aws-saml], [G] `samlCreate` |
| OIDC | `Authorized redirect URL` | Client ID, client secret, provider URL (issuer or discovery document URL) | [D] [gitlab-oidc], [D] [okta-oidc], [D] [onelogin-oidc], [D] [entra-oidc] |

Per provider:

| IdP | Protocol | IdP-side settings | Identity and name | Groups | Provider URL and other notes | Source |
|---|---|---|---|---|---|---|
| AWS IAM Identity Center | SAML | Custom SAML 2.0 application; ACS URL and audience entered by hand from Spacelift; assign users or groups to the app | NameID format `Persistent`; `Subject` mapped to `${user:subject}` (persistent); `FirstName` to `${user:givenName}`, `LastName` to `${user:familyName}` (basic) | `Teams` mapped to `${user:groups}` (basic); AWS does not officially support it and it yields group GUIDs, not names | Metadata URL from "IAM Identity Center SAML metadata file", dynamic configuration on. Saving in Spacelift starts a login at once, so the docs say to save last | [D] [aws-saml] |
| Okta | SAML | SAML 2.0 app integration; Single Sign-On URL also used as Recipient and Destination; Audience URI = Spacelift entity ID | NameID `EmailAddress` suggested; `Transient` and `Persistent` also work | A group attribute statement named exactly `Teams`, name format Unspecified, filter regex (`.*` for all groups, or a prefix such as `^Spacelift-.*`); without it group mappings and `session.teams` are empty | App metadata URL `https://<org>.okta.com/app/<app-id>/sso/saml/metadata`, dynamic configuration on. Group changes apply at the user's next login | [D] [okta-saml] |
| GitLab | OIDC | GitLab Application with the Spacelift redirect URL; scopes `openid`, `profile`, `email` | From the `email` claim | Not covered by the guide | Application ID is the client ID, Secret the secret; provider URL `https://gitlab.com` for GitLab.com, with no trailing slash | [D] [gitlab-oidc] |
| Okta | OIDC | OIDC Web Application; redirect URL in "Sign-in redirect URIs"; assignments choose who may log in | From the `email` claim | Groups claim type set under the legacy token-claims configuration, for example "Matches regex" `.*` | Provider URL is the Okta domain with an `https://` prefix. Okta exposes SCIM only on SAML or SWA apps, so SCIM with Okta OIDC needs a second SWA app (section 7.1) | [D] [okta-oidc], [D] [scim] |
| OneLogin | OIDC | "OpenId Connect" app; login URL and redirect URI set in its configuration | From the `email` claim | Not covered by the guide | Client ID, client secret and issuer URL from the app's SSO tab; the admin must be assigned to the app or the first login fails as unauthorized | [D] [onelogin-oidc] |
| Microsoft Entra ID | OIDC | App registration, single- or multi-tenant, "Web" redirect URL; client secret, which expires and must be renewed | Optional claim `upn` on the ID token, with the Microsoft Graph profile permission | Optional `groups` claim (Security groups or groups assigned to the application); at most 200 groups fit in the token; Entra sends group object IDs, so IdP group mappings use IDs | Provider URL is the "OpenID Connect metadata document" URL; client ID is the Application (client) ID. Assigning groups to the enterprise application limits the token to the intersection, which needs a paid Entra plan | [D] [entra-oidc] |

Per-IdP differences the SSO page itself names: Okta sends arbitrary group
lists natively for SAML, Google Workspace needs custom user-schema attributes
for SAML and does not return groups over OIDC, and AWS Cognito names its OIDC
group claim `cognito:groups`, which claim mapping renames to `groups`
[D] [sso]. Group membership must arrive on the login assertion even when SCIM
pushes groups; SCIM-provisioned groups do not populate `session.teams`
[D] [scim], [D] [okta-saml].

### 6.5 External accounts (federations)

Users can link one GitHub, Slack and Microsoft Teams account each; each
external account links to one user [D] [ext-accounts].

`FederationProvider`, 8 values [G]:

| Value | Notes |
|---|---|
| `OIDC` |  |
| `SAML` |  |
| `GITHUB_OAUTH` |  |
| `GOOGLE_OAUTH` |  |
| `GITLAB_OAUTH` |  |
| `MICROSOFT_OAUTH` |  |
| `SLACK_OAUTH` |  |
| `MICROSOFT_TEAMS_OAUTH` |  |

`IdentityFederation` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `provider` | `FederationProvider!` | Federation provider. |
| `externalID` | `String!` | External ID from the identity provider. |
| `externalUsername` | `String` | External username from the identity provider, if available. |

| Provider | Stable ID stored | Display name stored | Scopes |
|---|---|---|---|
| GitHub | node ID | login | `read:org` |
| Slack | user ID | display name or full name, may be empty | `users:read`; token revoked after use |
| Microsoft Teams | Entra object ID | profile name | `openid`, `profile` |

Source: [D] [ext-accounts]. Slack linking requires the account's Slack
workspace and refuses a different workspace; replacing the workspace removes
all Slack links. The login provider's own link cannot be removed. Links and
unlinks are audited as `identity.link` / `identity.unlink` (report 19).
`identityFederationUnlink(provider)` removes a link [G]. Policy inputs carry
an `identity` object (`ulid`, `federations` keyed `oidc`, `saml`,
`github-oauth`, `google-oauth`, `gitlab-oauth`, `microsoft-oauth`,
`slack-oauth`, `microsoft-teams-oauth`) in plan, approval, trigger and
notification policies, but not in push, login, access or task policies
[D] [ext-accounts].

## 7. SCIM, MFA and backup credentials

### 7.1 SCIM 2.0

| Item | Detail |
|---|---|
| Prerequisite | SSO configured |
| Credentials | OAuth 2.0 client credentials; secret shown once; reset issues a new ID and secret; scoped to SCIM only |
| Base URL | `https://<account>.app.spacelift.io/scim/v2` |
| Users | `POST`/`GET`/`PUT`/`PATCH`/`DELETE /Users[/{id}]`; attributes `userName` (required, matched to the SSO login), `externalId`, `displayName`, `name` (`givenName`, `familyName`, `formatted`), `emails`, `active`; other RFC 7643 attributes silently ignored |
| Groups | `POST`/`GET`/`PUT`/`PATCH`/`DELETE /Groups[/{id}]`; `displayName` (required), `externalId`, `members`; PATCH adds and removes members |
| Discovery | `GET /ServiceProviderConfig`, `/ResourceTypes`, `/Schemas` |
| Filters | Users: `userName` `eq`/`co`, `active` `eq`, `emails` `eq`/`co`; Groups: `displayName` `eq`/`co`; paging by `startIndex`, `count` |
| Unsupported | Bulk, password change, sorting, custom schema extensions |
| Deprovision | Hard delete revokes sessions; soft delete (`active` false) deactivates and revokes sessions, record kept for reactivation; deleting a group removes its member associations |
| Okta OIDC | Okta offers SCIM only on SAML and SWA apps, so an Okta OIDC sign-in pairs with a separate SWA app for provisioning (create, update and deactivate users) |
| Authorization | Provisioning grants nothing; roles must be bound (or assigned by login policy); a provisioned user with no role is refused at login |

Source: [D] [scim]. Schema [G]:

`ScimOauthClient` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `clientID` | `String!` | The OAuth2 client ID |
| `clientSecret` | `String` | The OAuth2 client secret, only returned on creation |
| `connnectorBaseURL` | `String!` | The SCIM connector base URL |
| `accessTokenEndpoint` | `String!` | The OAuth2 access token endpoint |

`SCIMUserMetadata` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `externalId` | `String!` | External ID assigned by the SCIM identity provider |
| `givenName` | `String!` | Given (first) name from the SCIM identity provider |
| `familyName` | `String!` | Family (last) name from the SCIM identity provider |
| `formattedName` | `String!` | Formatted full name from the SCIM identity provider |
| `displayName` | `String!` | Display name from the SCIM identity provider (RFC 7643 §4.1.1) |
| `provisionedAt` | `Int!` | Unix timestamp (seconds) at which the user was provisioned via SCIM |
| `email` | `String!` | Email address from the SCIM identity provider |

`SCIMGroupMetadata` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `externalId` | `String!` | External ID assigned by the SCIM identity provider |

Operations: `scimSettings`, `createOauthClientForSCIM`,
`resetOauthClientForSCIM`, `deleteOauthClientForSCIM` [G].
`ManagedUser.scimGroupMemberships` and `scimMetadata` expose provisioned data
[G].

### 7.2 MFA and security keys

| Item | Detail |
|---|---|
| Mechanism | FIDO2 security keys registered in Spacelift, independent of the IdP |
| Requirements | Enterprise plan with SSO enabled |
| Per user | Enable under Personal settings; register keys by name and key ID; delete keys |
| Organization | Admins list every key and may delete any user's key |
| Enforcement | `accountToggleEnforcingMFA`; every active user must keep at least one key; enabling ends all other sessions; users register a key at next login and cannot remove their last key |

Source: [D] [mfa]. Schema [G]:

`SecurityKey` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | Unique ID of the security key. |
| `createdAt` | `Int!` | Timestamp representing the security key registration. |
| `currentOwner` | `Boolean!` | currentOwner returns true if the current user is the owner of the security key. |
| `name` | `String!` | Name of the security key. |
| `owner` | `String!` | Owner of the security key. |

Operations: `enforceMFA`, `securityKeys` (account), `User.securityKeys`
(own), `securityKeyDelete(id)`, `userSecurityKeyDeleteAll`,
`accountToggleEnforcingMFA` [G]. The registration ceremony itself is not in
GraphQL *(unverified)*: no mutation registers a key.

### 7.3 Backup credentials

The documented break-glass path is a secret API key with admin on `root`,
exchanged for a browser session at `https://<account>.app.spacelift.io/apikeytoken`
[D] [backup-creds]. The same page with a non-admin key shows what a given
team membership sees, for debugging login policies [D] [backup-creds],
[D] [roles-apikeys]. Self-hosted adds a static admin login
(`ADMIN_USERNAME`, `ADMIN_PASSWORD` on the server container) at
`https://<SERVER_DOMAIN>/admin-login`, used to create the account and kept as
a break-glass path; removing both variables or setting them empty disables it
[D] [sh-first-setup], [D] [sh-gen]. `IdentityProvider.EMPTY` is the self-hosted state
before SSO exists [G].

## 8. API keys

### 8.1 Types and fields

`APIKeyType`, 2 values [G]:

| Value | Notes |
|---|---|
| `SECRET` | Classic API key with a secret. |
| `OIDC` | API key that can dynamically generate tokens using an external, OIDC-compliant identity provider. |

`ApiKey` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | Globally unique ID of the API key. |
| `admin` | `Boolean!` | Should tokens generated from this API key have admin permissions? |
| `createdAt` | `Int!` | Creation timestamp. |
| `creator` | `String!` | The creator of the API key. |
| `deleted` | `Boolean!` | Indicates whether the API key is deleted. |
| `disabled` | `Boolean!` | Indicates whether the API key has been disabled. |
| `isPersonal` | `Boolean!` | Indicates whether the API key is personal, owned by a person rather than the organization. |
| `disabledReason` | `ApiKeyDisabledReason` | Who disabled the API key. Null while the key is active. |
| `disabledAt` | `Int` | When the API key was disabled. Null while the key is active. |
| `owner` | `String` | Login of the user who owns the API key. Null for organization-level keys. |
| `lastUsedAt` | `Int` | Last usage (token generation) timestamp, if any. |
| `name` | `String!` | Human-readable name of this API key. |
| `oidcSettings` | `ApiKeyOIDCSettings` | The OIDC settings for this API key. |
| `secret` | `String` | API key secret - only visible when the key is created and not persisted on Spacelift end. Make sure to save it! |
| `teamCount` | `Int!` | Number of teams this key "belongs to" |
| `teams` | `[String!]!` | List of teams this key "belongs to". This allows non-admin API keys to be treated the same way as regular users by stack-level policies. |
| `spaceCount` | `Int!` | Number of spaces api key was explicitly given permissions to. |
| `accessRules` | `[SpaceAccessRule!]!` | List of permissions given explicitly to this api key. |
| `isMachineUser` | `Boolean!` | Indicates whether the api key is a machine user. |
| `type` | `APIKeyType!` | Type of the API key. |
| `description` | `String!` | Free-form description of the API key. |
| `apiKeyRoleBindings` | `[ApiKeyRoleBinding!]!` |  |
| `expiresAt` | `Int` |  |

`ApiKeyInput` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `admin` | `Boolean!` |  |
| `name` | `String!` |  |
| `teams` | `[String!]!` |  |
| `machine` | `Boolean` |  |
| `oidc` | `APIKeyInputOIDC` |  |
| `accessRules` | `[SpaceAccessRuleInput!]` | Access rules for api key. |
| `description` | `String` |  |
| `roleBindings` | `[RoleBindingInput!]` |  |
| `expiresAt` | `Int` |  |

`APIKeyInputOIDC` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `issuer` | `String!` |  |
| `clientId` | `String!` |  |
| `subjectExpression` | `String!` |  |
| `claimMappings` | `ClaimMappingInput` |  |

`ApiKeyOIDCSettings` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `issuer` | `String!` | The OIDC issuer URL. |
| `clientId` | `String!` | The OIDC client ID (audience). |
| `subjectExpression` | `String!` | The regular expression against which the subject of the OIDC token will be matched. |
| `claimMapping` | `ClaimMapping!` | Claim mappings for mapping IdP JWT claim keys to Spacelift claim keys. For example, mapping "cognito_groups" to "groups" allows dynamic team extraction from the OIDC token. |

`ApiKeyUpdateInput` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `name` | `String` |  |
| `teams` | `[String!]` |  |
| `description` | `String` |  |
| `oidcClaimMappings` | `ClaimMappingInput` |  |

`PersonalApiKeyInput` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `name` | `String!` |  |
| `description` | `String` |  |
| `expiresAt` | `Int` |  |

`ApiKeyDisabledReason`, 4 values [G]:

| Value | Notes |
|---|---|
| `ADMIN` | A root admin revoked the key. |
| `ACCOUNT` | An account admin turned personal API keys off and disabled existing keys. |
| `DEPROVISIONED` | The owner was deprovisioned. |
| `SCHEME` | The account moved to login policies. |

The changelog lists `ApiKeyInput.admin` as deprecated (2026-07-16, use access
rules or role bindings with root admin) but the snapshot does not mark it
deprecated [RN], [G]. The schema wins for the current shape; the field is
still required (`Boolean!`). The Terraform provider always sends `false` and
grants access only through role attachments [S] [tf-api-key-admin].

### 8.2 Lifecycle

| Stage | Secret key | OIDC key | Source |
|---|---|---|---|
| Create | `apiKeyCreate`; secret returned once, never stored; a downloaded file holds the secret and a `.terraformrc` snippet for the module registry | `apiKeyCreate` with `oidc`: `issuer` (`iss`), `clientId` (`aud`), `subjectExpression` (regex anchored to the whole `sub`, since 2026-07-21), optional claim mapping to `groups` | [D] [api], [RN] 2026-07-21 |
| Scope | `accessRules` or `roleBindings`; admin on `root` makes the key administrative; `teams` give virtual group membership | same | [D] [api], [G] |
| Expiry | Optional `expiresAt` (since 2026-03-31) | same | [G], [RN] |
| Exchange | `apiKeyUser(id, secret)` returns `User` with `jwt` and `validUntil` | the same mutation, with the IdP-issued JWT as `secret` | [D] [api], [S] [spacectl-apikey], [S] [spacectl-oidc] |
| Use | `Authorization: Bearer <jwt>` to `/graphql` | same; no module registry access | [D] [api] |
| Rotate | `apiKeyReset(id)` resets the secret in place (since 2026-05-07) | n/a | [G], [RN] |
| Update | `apiKeyUpdate`: `name`, `teams`, `description`, `oidcClaimMappings` | same | [G] |
| Delete | `apiKeyDelete(id)` | same | [G] |
| Login policy | Treated as a virtual user and evaluated by login policy, except admin keys in `root` | `session.login` is `api::oidc::<client-id>::<key-id>`, `session.idp_subject` the JWT `sub` | [D] [login-policy] |

Other facts:
- Keys count as seats. An API key used in the last rolling 30 days takes a
  seat; Free counts keys against a separate limit; a personal key rides on its
  owner's seat unless the owner has not logged in during the window
  [D] [roles-apikeys], [D] [iam-admin]. `seats` returns `apiKey` and `user`
  limits [G] `Seats`.
- `spacectl` refreshes a token 30 seconds before expiry and, with
  `SPACELIFT_API_KEY_OIDC_PROVIDER=github-actions`, mints a new GitHub OIDC
  token on each exchange; the audience defaults to the endpoint host and is
  overridden by `SPACELIFT_API_KEY_OIDC_AUDIENCE` [S] [spacectl-token],
  [D] [api]. The JWT's single audience is the API endpoint
  [S] [spacectl-token].
- Run details name the key as `api::<short id>::<name>` [RN] 2025-09-16.

Account settings for keys [G]:

| Setting | Read | Write | Meaning |
|---|---|---|---|
| Non-root managers | `apiKeysManagedByNonRootAdmins` | `apiKeyEnableNonRootManagers(enable)` | Non-root space admins create keys up to their own level; a creator can always delete their key |
| Non-human management | `apiKeysManageableByNonHumans` | `accountToggleAPIKeyManagementFromNonHumans` | API key and admin-stack sessions may manage keys |
| Personal keys | `personalApiKeysEnabled`, `personalApiKeysCount` | `accountSetPersonalApiKeysEnabled(enabled, disableExistingKeys)` | Allow personal keys; optionally disable all existing ones |

### 8.3 Personal API keys

Personal keys belong to a user (`isPersonal`, `owner`), are created with
`personalApiKeyCreate(PersonalApiKeyInput)` (`name`, `description`,
`expiresAt`), inherit their owner's access, and are listed by
`User.ownedApiKeys`, `ManagedUser.personalApiKeys` and `personalApiKeys`
(root admins) [G]. A root admin can disable one with `personalApiKeyDisable`;
then only the owner can delete it [G]. `disabledReason` records why
(`ADMIN`, `ACCOUNT`, `DEPROVISIONED`, `SCHEME`) [G]. They are active only
under IAM and become ordinary keys under login policies [D] [iam-admin].
`User.canCreatePersonalApiKey` and `accountRequestPersonalApiKeysEnabling`
support the UI [G]. The docs say little else *(unverified)* (owner removal
behaviour beyond `DEPROVISIONED`).

### 8.4 Other ways to get a token

| Method | Operation | Notes |
|---|---|---|
| spacectl | `spacectl profile login`, `spacectl profile export-token` | [D] [api] |
| GitHub personal access token | `oauthUser(token)` | Only for GitHub-login accounts without SSO; tokens last about 10 hours [D] [api] |
| Browser OAuth | `oauthRedirect`, `oauthToken(code, state, temporaryToken)` | [G] |

Terraform: `spacelift_api_key` (`name`; `description`; `idp_groups`; an
`oidc` block with `issuer`, `client_id`, `subject_expression`,
`claim_mappings`; computed sensitive `secret`; `type`); adding or removing the
`oidc` block, or changing its `issuer`, `client_id` or `subject_expression`,
replaces the key, while
`claim_mappings`, `name`, `description` and `idp_groups` update in place
through `apiKeyUpdate` [S] [tf-api-key], [S] [tf-api-key-update]. Keys created
through Terraform land in state [S] `terraform-schema.json`.

## 9. Managed users, groups and invitations

`ManagedUser` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | Globally unique, immutable ID of ManagedUser. |
| `username` | `String!` | Username from account's identity provider. |
| `invitationEmail` | `String` | Email through which user was invited to given account. Can be empty. |
| `spaceCount` | `Int!` | Number of spaces user was explicitly given permissions to. |
| `accessRules` | `[SpaceAccessRule!]!` | List of permissions given explicitly to given user. |
| `groupCount` | `Int!` | Number of groups in your identity provider that this user was a member of on last login. |
| `latestGroups` | `[String!]!` | Groups in your identity provider that this user was a member of on last login. |
| `latestInvitation` | `ManagedUserInvitation` | Invitation details for the latest invitation sent to given user. Can be empty. |
| `lastLoginTime` | `Int` | Unix timestamp (in seconds) at which user has last authenticated. |
| `loginMethod` | `IdentityProvider!` | Identity provider used for authenticating given user. |
| `status` | `UserStatus!` | Status is user status. |
| `role` | `UserRole!` | Role describes if user is an admin or ordinary user. |
| `slackMemberID` | `String` | ID of slack user that will have the same permissions as given managed user when using Spacelift through Slack integration. |
| `userRoleBindings` | `[UserRoleBinding!]!` | Roles that are binded to this user. |
| `scimGroupMemberships` | `[String!]!` | Names of groups the user is a SCIM-provisioned member of. |
| `scimMetadata` | `SCIMUserMetadata` | SCIM metadata for users provisioned via SCIM. Null if not SCIM-provisioned. |
| `personalApiKeys` | `[ApiKey!]!` | Personal API keys owned by this user. Root admins only. |

`UserStatus`, 4 values [G]:

| Value | Notes |
|---|---|
| `QUEUED` |  |
| `PENDING` |  |
| `ACTIVE` |  |
| `INACTIVE` |  |

`UserRole`, 3 values [G]:

| Value | Notes |
|---|---|
| `USER` |  |
| `ADMIN` |  |
| `OWNER` |  |

`ManagedUserInvitation` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `createdAt` | `Int!` | Unix timestamp (in seconds) at which this invitation was issued. |
| `sentAt` | `Int` | Unix timestamp (in seconds) at which the invitation was sent. Can be empty for unsent invitations. |

`ManagedUserInviteInputV2` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `invitationEmail` | `String` | Email address of the user to invite. |
| `username` | `String!` | Unique identifier of a user within an identity provider. |
| `roleBindings` | `[RoleBindingInput!]!` | Access rules for the user. |
| `slackMemberID` | `String` | new slack member ID |

`ManagedUserUpdateInput` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | Globally unique, immutable ID of ManagedUser to update. |
| `accessRules` | `[SpaceAccessRuleInput!]!` | new access rules |
| `slackMemberID` | `String` | new slack member ID |

`ManagedUserGroup` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | Globally unique, immutable ID of ManagedUserGroup. |
| `groupName` | `String!` | Name of user group from account's identity provider. |
| `spaceCount` | `Int!` | Number of spaces user group was explicitly given permissions to. |
| `memberCount` | `Int!` | Number of members of this group who logged into this account. |
| `members` | `[String!]!` | Members of this group |
| `managedUsers` | `[ManagedUser!]!` | Members of this group, returned as ManagedUser resolvers. |
| `accessRules` | `[SpaceAccessRule!]!` | List of permissions given explicitly to given user group. |
| `identityProvider` | `IdentityProvider!` | Identity provider authorized with group permissions. |
| `lastLoginTime` | `Int` | Unix timestamp (in seconds) at which user from this group was last authenticated. |
| `status` | `UserGroupStatus!` | Status is user group status. |
| `description` | `String!` | Description is optional, user-provided text describing the group resource or serving as a human-readable name. |
| `userRoleBindings` | `[UserGroupRoleBinding!]!` | List of user group role bindings for this group. |
| `scimMetadata` | `SCIMGroupMetadata` | SCIM metadata for groups provisioned via SCIM. Null if not SCIM-provisioned. |

`UserGroupStatus`, 2 values [G]:

| Value | Notes |
|---|---|
| `ACTIVE` |  |
| `INACTIVE` |  |

`ManagedUserGroupCreateInputV2` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `groupName` | `String!` |  |
| `roleBindings` | `[RoleBindingInput!]!` |  |
| `description` | `String` |  |

`ManagedUserGroupUpdateInputV2` (input) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | Globally unique, immutable ID of ManagedUserGroup. |
| `description` | `String` |  |

`ManagedUserGroupIntegration` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | Globally unique, immutable ID of ManagedUserGroupIntegration. |
| `integrationName` | `String!` | Human-readable name of the integration. |
| `integrationType` | `ManagedUserGroupIntegrationType!` | Specifies which fields of this integration are relevant and how they should be utilized. |
| `accessRules` | `[SpaceAccessRule!]!` | List of permissions given explicitly to given user group. |
| `slackChannelID` | `String` | ID of slack channel that this ManagedUserGroupIntegration provides permissions to. |

`ManagedUserGroupIntegrationType`, 1 values [G]:

| Value | Notes |
|---|---|
| `SLACK` |  |

Status lifecycle [D] [iam-admin], [G] `UserStatus`:

| Status | Meaning | Entered by |
|---|---|---|
| `QUEUED` | Invited while login policies are active; email held | invite under `LOGIN_POLICIES` |
| `PENDING` | Invitation sent | invite under IAM, or switch to IAM |
| `EXPIRED` | Not accepted within 24 hours; must be re-issued | time (docs only; not in `UserStatus`) |
| `ACTIVE` | Accepted; IAM permissions apply | first login through the invitation |
| `INACTIVE` | The account's identity provider changed; must be re-invited | provider change |

`UserStatus` has no `EXPIRED` value [G]; the docs show it in the list
[D] [iam-admin]. Expiry is presumably derived from `latestInvitation.sentAt`
[I] from `ManagedUserInvitation`.

Rules: a new invite for an email is refused while a pending or expired one
exists; "Resend invite" is `managedUserResendInvite`; "Revoke access" removes
all bindings and frees the seat at once on seat-limited plans
[D] [iam-admin], [G]. The V1 inputs (`managedUserInvite`,
`managedUserGroupCreate`, `managedUserGroupUpdate`) are deprecated for the V2
inputs that take `roleBindings` [G], [RN] 2026-07-16; the Terraform
`spacelift_user` and `spacelift_idp_group_mapping` resources still call the V1
mutations [S] `terraform-operations.json`. `managedUserRequestInvite(email,
userName)` lets a non-member ask for an invite [G].

| Root field | Kind |
|---|---|
| `managedUsers`, `managedUser(id)` | query |
| `managedUserInviteV2`, `managedUserInvite` (deprecated), `managedUserResendInvite`, `managedUserUpdate`, `managedUserDelete` | mutation |
| `managedUserGroups`, `managedUserGroup(id)` | query |
| `managedUserGroupCreateV2`, `managedUserGroupUpdateV2`, `managedUserGroupDelete` (and deprecated V1) | mutation |
| `managedUserGroupIntegrations(integrationType)`, `managedUserGroupIntegration(id)` | query |
| `managedUserGroupIntegrationCreate`, `...Update`, `...Delete` | mutation |
| `managedUserRequestInvite`, `managedUserRequestRoleChange` | mutation |

Source: [G].

## 10. Sessions

`Session` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | Unique session ID, the same as JWT ID. |
| `admin` | `Boolean!` | Admin means that the user is an admin of the organization. |
| `createdAt` | `Int!` | Time of the session creation. |
| `current` | `Boolean!` | Current indicates whether the session is the current one. |
| `email` | `String!` | Email of the session creator if applicable. |
| `expiresAt` | `Int!` | Time of the session expiration. |
| `ipAddress` | `String!` | IP address of the session creator. |
| `login` | `String!` | Login of the session creator. |
| `name` | `String!` | Full name of the session creator. |
| `teams` | `[String!]!` | Team names which the user has access to. |
| `type` | `String!` | Type of the session |
| `channel` | `SessionChannel!` | Channel the session came through. |
| `isMachineUser` | `Boolean!` | Indicates whether the session is a machine user. |

`SessionChannel`, 4 values [G]:

| Value | Notes |
|---|---|
| `UNKNOWN` |  |
| `WEB` |  |
| `TERRAFORM_CLI` |  |
| `SPACECTL` |  |

`User` (object) [G]:

| Field | Type | Notes |
|---|---|---|
| `id` | `ID!` | Username. |
| `actions` | `[SpaceActions!]!` | actions that the user can perform in spaces. |
| `admin` | `Boolean!` | Whether the user has org-wide admin privileges. |
| `apiKeys` | `[ApiKey!]!` | List of API keys created by the user. |
| `ownedApiKeys` | `[ApiKey!]!` | List of personal API keys owned by the user. Unlike apiKeys, this does not depend on the account allowing non-root API keys. |
| `canCreatePersonalApiKey` | `Boolean!` | Whether the current user may create a personal API key right now. Runs the same checks as personalApiKeyCreate. |
| `avatarURL` | `String!` | URL of the user avatar. |
| `email` | `String!` | Email of the user, may be empty. |
| `jwt` | `String!` | Temporary access token as a string. |
| `name` | `String!` | Full name of the GitHub user. |
| `since` | `Int` | Timestamp of the first login, only populated for human users. |
| `validUntil` | `Int!` | Unix timestamp of token validity expiration. |
| `teams` | `[String!]!` | Team names which the user has access to. |
| `cannotEvaluateAccessPolicies` | `Boolean!` | Whether the access policies can be evaluated for this user. |
| `identityProvider` | `String!` | Identity provider. |
| `isFirstUser` | `Boolean!` | Indicates whether the user is the first to log in for this account. Is guaranteed to be true only for the first user to log in and before any other user has logged in. |
| `securityKeys` | `[SecurityKey!]!` | Returns list of the security keys for the current user. |
| `identity` | `Identity` | V2 identity for the current user, if available. |

| Behaviour | Detail | Source |
|---|---|---|
| Token | A JWT bearer token; session `id` equals the JWT ID; GitHub-token logins last about 10 hours | [G] `Session.id`, [D] [api] |
| Channels | Web, spacectl and `terraform login` keep separate sessions; a new login replaces only the earlier session of the same channel; a spacectl session expires with the browser session it came from but survives browser logout | [RN] 2026-10-12 |
| Listing | `sessions` lists active sessions; empty on Starter | [G] |
| Ending | `logout` (own), `sessionDelete(id)` (not on Starter), `sessionDeleteAll` (all but the caller's) | [G] |
| Forced invalidation | Switching strategy; any login policy change; enforcing MFA; unlinking SSO; SCIM delete or deactivate | [D] [iam-admin], [D] [login-policy], [D] [mfa], [D] [sso], [D] [scim] |
| Access snapshot | Each session carries precomputed space access; under IAM it is recomputed on role and topology changes, under login policies it is fixed until re-login | [G] `SessionSpaceAccessRoleGrant`, [RN] 2026-03-19, 2026-06-11 |
| Root-admin only | Managing sessions | [D] [access-control] |

The session lifetime for browser logins is not documented
*(unverified)*. The docs mention shorter sessions as a mitigation for login-time IP
checks, which implies a configurable timeout, but no setting exists in the
schema [D] [migrate-spaces], [G] *(unverified)*.

## 11. Tier gates

`BillingTierFeature` names the gated features [G]; the docs and the pricing
page say which plan has each.

`BillingTierFeature`, 31 values [G]:

| Value | Notes |
|---|---|
| `ACCESS_POLICIES` |  |
| `ADDITIONAL_PUBLIC_WORKER` |  |
| `ADVANCED_SCHEDULING` |  |
| `AGGREGATED_VCS_CHECKS` |  |
| `API_KEYS_OIDC` |  |
| `AUDITING` | Indicates whether your account can use audit functionality, including audit webhooks. |
| `AUDIT_WEBHOOKS` | **Deprecated:** Use the AUDITING feature instead. |
| `BLUEPRINTS` |  |
| `CAN_UPDATE_SSO_SAML` |  |
| `CLOUD_CREDENTIAL_GENERATION` |  |
| `CUSTOM_SSO_SAML` |  |
| `DRIFT_DETECTION` |  |
| `MFA` |  |
| `MULTIPLE_VCS` |  |
| `NOTIFICATION_POLICIES` |  |
| `PRIVATE_MODULE_SHARING` |  |
| `PRIVATE_VCS` |  |
| `PRIVATE_WORKERS` |  |
| `RUN_STATE_CHANGE_WEBHOOKS` |  |
| `TARGETED_REPLAN` |  |
| `TERRAFORM_PROVIDER_VERSIONS` |  |
| `UNLIMITED_SEATS` |  |
| `WORKFLOW_POLICIES` |  |
| `OIDC_FEDERATION_TOKEN_GENERATION` |  |
| `RUN_IGNORED_TRIGGERS` |  |
| `ACCOUNT_AI_CONTROL` |  |
| `SPACELIFT_INTELLIGENCE` |  |
| `SERVICE_NOW_INTEGRATION` |  |
| `BACKSTAGE_INTEGRATION` |  |
| `SCIM` | Indicates whether the account can use SCIM v2 provisioning APIs. |
| `SCANS` | Indicates whether the account can use scans. |

| Capability | Gate | Source |
|---|---|---|
| Spaces | Listed from Starter+ on the pricing page; no gate in the docs or schema | [C] [pricing] *(unverified for Free)* |
| Custom roles and bindings | No gate stated anywhere | *(unverified)* |
| Login policies | Policies need a paid account; some plans exclude login policies | [D] [policy], [D] [login-policy] *(unverified which plan)* |
| Access policies | `ACCESS_POLICIES`; disabled for all on 2026-05-30 | [G], [D] [stack-access-policy] |
| SAML SSO | Enterprise; `CUSTOM_SSO_SAML`, `CAN_UPDATE_SSO_SAML` | [D] [sso], [G], [C] [pricing] |
| OIDC SSO | Paid accounts | [D] [sso] |
| SCIM | Enterprise; `SCIM` | [D] [scim], [G] |
| MFA | Enterprise with SSO; `MFA` | [D] [mfa], [G], [C] [pricing] |
| OIDC API keys | Enterprise; `API_KEYS_OIDC` | [D] [api], [G], [C] [pricing] |
| Session listing and deletion | Not on Starter | [G] `sessions`, `sessionDelete` |
| Seats | Free: 2 users; Free and Starter have seat limits; `UNLIMITED_SEATS` | [C] [pricing], [D] [iam-admin], [G] |
| Self-hosted | The Enterprise tier's features, except those that make no sense self-hosted (such as the public worker pool); new SaaS features arrive with a later self-hosted release ([self-hosted](https://docs.spacelift.io/self-hosted)) | [D] |

The `BillingTierFeature` name in each row is paired with the capability by
name [I]; only `SCIM` and `AUDITING` carry a description that names the
feature [G]. The plan statements come from each feature's docs page notice and
from the pricing page's plan lists (Enterprise lists SAML SSO, OIDC API keys
and MFA; Starter+ lists Spaces), re-read 2026-10-28 [C] [pricing].

`tier` and `tierFeatures` report the account's tier and features [G]; the
`BillingTier` enum holds generations from `FREE` to `V5_STRATEGIC` (report 26
owns tiers).

## What this means for infraweave

- A space tree with an immutable root, a legacy space for back-compatibility
  or none, deletion only when empty, re-parenting with reachability checks,
  and a per-space inheritance toggle.
- Three access paths to evaluate (propagation down, Read up through
  inheritance, implicit stack read) and the grant views that explain them
  (`RESIDENT`, `PROPAGATED`, `INHERITED`; via group).
- A 150-value action catalogue (18 of them marked disabled), each with a
  legacy fallback level, system roles with fixed slugs, custom roles, and a
  role-actions listing.
- Bindings for five actor kinds (user, IdP group, API key, stack, Flows
  project) with batch create, the stack escalation rules and the `root`
  restriction.
- Two exclusive authorization strategies with a switch that ends sessions,
  login policy rules including `deny_message` aggregation and `team`
  rewriting, and the admin overrides that bypass both.
- SAML and OIDC SSO with claim mapping, a SCIM 2.0 server, FIDO2 MFA with
  enforcement, external account federation, and a break-glass path.
- Secret and OIDC API keys with expiry, reset, personal keys and the
  account-level key settings; seat accounting for keys.
- Sessions per channel with a precomputed access snapshot that is
  recomputed live under one strategy and frozen under the other.
- Feature gates mapped to `BillingTierFeature` names.

## Inferred and undocumented

- Generated tables: every table introduced as `` `Type` (object) [G] ``,
  `` `Type` (input) [G] `` or `` `Enum`, N values [G] `` comes from
  `introspection.json`; an independent script re-derived all 55 tables
  (308 rows) with no differences. The `Action` table is hand-grouped; a
  script confirmed it holds each of the 150 enum values once and that all
  147 documented legacy fallbacks match the docs tables.
- SSO settings other than claim mapping cannot be updated in place [I] from
  `SAMLUpdateInput` and `OIDCUpdateInput`.
- Invitation expiry is derived, not stored as a status [I] from `UserStatus`
  lacking `EXPIRED`.
- Open items, marked *(unverified)* above: slugs and action sets of the
  Worker pool controller and drift detection system roles; the fallback for
  `STACK_SCHEDULED_DELETE_UPDATE`; whether the 18 "Disabled" actions can be
  put in roles; whether a managed user with no roles is refused at login or
  given an empty token; browser session lifetime and whether it is
  configurable; how security keys are registered; the tier of login policies,
  custom roles and spaces on Free;
  personal key behaviour when the owner is removed; whether a non-root space
  admin can edit their spaces while non-root space management is off.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e`, fetched 2026-10-28 |
| docs.spacelift.io | read 2026-10-28 |
| Self-hosted Spacelift | v7.1.0 (docs `self-hosted/latest`) |
| terraform-provider-spacelift | v1.57.0, commit `c72b9bd2f3c3` |
| spacectl | `6bf167cb62a6` |

## Sources

Documentation (read 2026-10-28):

- `spaces`: <https://docs.spacelift.io/concepts/spaces>
- `access-control`: <https://docs.spacelift.io/concepts/spaces/access-control>
- `how-access`: <https://docs.spacelift.io/concepts/spaces/how-access-works>
- `create-space`: <https://docs.spacelift.io/concepts/spaces/creating-a-space>
- `nonroot-spaces`: <https://docs.spacelift.io/concepts/spaces/allowing-non-root-admins-to-manage-spaces>
- `structuring`: <https://docs.spacelift.io/concepts/spaces/structuring-your-spaces-tree>
- `legacy-space`: <https://docs.spacelift.io/concepts/spaces/migrating-out-of-the-legacy-space>
- `authz`: <https://docs.spacelift.io/concepts/authorization>
- `rbac`: <https://docs.spacelift.io/concepts/authorization/rbac-system>
- `roles-users`: <https://docs.spacelift.io/concepts/authorization/assigning-roles-users>
- `roles-groups`: <https://docs.spacelift.io/concepts/authorization/assigning-roles-groups>
- `roles-apikeys`: <https://docs.spacelift.io/concepts/authorization/assigning-roles-api-keys>
- `roles-stacks`: <https://docs.spacelift.io/concepts/authorization/assigning-roles-stacks>
- `iam`: <https://docs.spacelift.io/concepts/identity-access-management>
- `iam-admin`: <https://docs.spacelift.io/concepts/identity-access-management/admin>
- `ext-accounts`: <https://docs.spacelift.io/concepts/identity-access-management/external-accounts>
- `invite`: <https://docs.spacelift.io/getting-started/invite-teammates>
- `policy`: <https://docs.spacelift.io/concepts/policy>
- `login-policy`: <https://docs.spacelift.io/concepts/policy/login-policy>
- `stack-access-policy`: <https://docs.spacelift.io/concepts/policy/stack-access-policy>
- `migrate-spaces`: <https://docs.spacelift.io/concepts/policy/deprecated/migrate-to-spaces>
- `sso`: <https://docs.spacelift.io/integrations/single-sign-on>
- `okta-saml`: <https://docs.spacelift.io/integrations/single-sign-on/okta-saml-setup-guide>
- `aws-saml`: <https://docs.spacelift.io/integrations/single-sign-on/aws-iam-identity-saml-setup-guide>
- `okta-oidc`: <https://docs.spacelift.io/integrations/single-sign-on/okta-oidc-setup-guide>
- `onelogin-oidc`: <https://docs.spacelift.io/integrations/single-sign-on/onelogin-oidc-setup-guide>
- `gitlab-oidc`: <https://docs.spacelift.io/integrations/single-sign-on/gitlab-oidc-setup-guide>
- `entra-oidc`: <https://docs.spacelift.io/integrations/single-sign-on/microsoft-entra-id-oidc-setup-guide>
- `backup-creds`: <https://docs.spacelift.io/integrations/single-sign-on/backup-credentials>
- `scim`: <https://docs.spacelift.io/integrations/single-sign-on/scim>
- `mfa`: <https://docs.spacelift.io/product/security/mfa>
- `security`: <https://docs.spacelift.io/product/security>
- `api`: <https://docs.spacelift.io/integrations/api>
- `sh-first-setup`: <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/first-setup>
- `sh-changelog`: <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog>
- `sh-gen`: <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/general-configuration>
- `changelog` [RN]: <https://docs.spacelift.io/product/changelog>

Schema and snapshots:

- `api/reference/spacelift/introspection.json` (`b3255fe38f5e`) [G]
- `api/reference/spacelift/terraform-schema.json` and `terraform-operations.json` (provider v1.57.0)

Source code:

- `tf-space`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_space.go#L33-L56>
- `tf-space-update`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_space.go#L143-L152>
- `tf-role-attachment`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_role_attachment.go#L17-L20>
- `tf-api-key`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_api_key.go>
- `tf-api-key-admin`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_api_key.go#L105-L109>
- `tf-api-key-update`: <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_api_key.go#L233-L275>
- `spacectl-apikey`: <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/api_key.go#L57-L60>
- `spacectl-oidc`: <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/oidc_provider.go#L61-L74>
- `spacectl-token`: <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/api_token.go#L16-L35>

Other:

- `pricing`: <https://spacelift.io/pricing> (read 2026-10-28) [C]

[spaces]: https://docs.spacelift.io/concepts/spaces
[access-control]: https://docs.spacelift.io/concepts/spaces/access-control
[how-access]: https://docs.spacelift.io/concepts/spaces/how-access-works
[create-space]: https://docs.spacelift.io/concepts/spaces/creating-a-space
[nonroot-spaces]: https://docs.spacelift.io/concepts/spaces/allowing-non-root-admins-to-manage-spaces
[structuring]: https://docs.spacelift.io/concepts/spaces/structuring-your-spaces-tree
[legacy-space]: https://docs.spacelift.io/concepts/spaces/migrating-out-of-the-legacy-space
[authz]: https://docs.spacelift.io/concepts/authorization
[rbac]: https://docs.spacelift.io/concepts/authorization/rbac-system
[roles-users]: https://docs.spacelift.io/concepts/authorization/assigning-roles-users
[roles-groups]: https://docs.spacelift.io/concepts/authorization/assigning-roles-groups
[roles-apikeys]: https://docs.spacelift.io/concepts/authorization/assigning-roles-api-keys
[roles-stacks]: https://docs.spacelift.io/concepts/authorization/assigning-roles-stacks
[iam]: https://docs.spacelift.io/concepts/identity-access-management
[iam-admin]: https://docs.spacelift.io/concepts/identity-access-management/admin
[ext-accounts]: https://docs.spacelift.io/concepts/identity-access-management/external-accounts
[invite]: https://docs.spacelift.io/getting-started/invite-teammates
[policy]: https://docs.spacelift.io/concepts/policy
[login-policy]: https://docs.spacelift.io/concepts/policy/login-policy
[stack-access-policy]: https://docs.spacelift.io/concepts/policy/stack-access-policy
[migrate-spaces]: https://docs.spacelift.io/concepts/policy/deprecated/migrate-to-spaces
[sso]: https://docs.spacelift.io/integrations/single-sign-on
[okta-saml]: https://docs.spacelift.io/integrations/single-sign-on/okta-saml-setup-guide
[aws-saml]: https://docs.spacelift.io/integrations/single-sign-on/aws-iam-identity-saml-setup-guide
[okta-oidc]: https://docs.spacelift.io/integrations/single-sign-on/okta-oidc-setup-guide
[onelogin-oidc]: https://docs.spacelift.io/integrations/single-sign-on/onelogin-oidc-setup-guide
[gitlab-oidc]: https://docs.spacelift.io/integrations/single-sign-on/gitlab-oidc-setup-guide
[entra-oidc]: https://docs.spacelift.io/integrations/single-sign-on/microsoft-entra-id-oidc-setup-guide
[backup-creds]: https://docs.spacelift.io/integrations/single-sign-on/backup-credentials
[scim]: https://docs.spacelift.io/integrations/single-sign-on/scim
[mfa]: https://docs.spacelift.io/product/security/mfa
[security]: https://docs.spacelift.io/product/security
[api]: https://docs.spacelift.io/integrations/api
[sh-first-setup]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/first-setup
[sh-changelog]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog
[sh-gen]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/general-configuration
[tf-space-update]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_space.go#L143-L152
[tf-api-key-admin]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_api_key.go#L105-L109
[tf-api-key-update]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_api_key.go#L233-L275
[tf-space]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_space.go#L33-L56
[tf-role-attachment]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_role_attachment.go#L17-L20
[tf-api-key]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_api_key.go
[spacectl-apikey]: https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/api_key.go#L57-L60
[spacectl-oidc]: https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/oidc_provider.go#L61-L74
[spacectl-token]: https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/client/session/api_token.go#L16-L35
[pricing]: https://spacelift.io/pricing
