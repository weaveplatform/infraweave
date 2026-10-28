# 16: State management

Checked against: docs.spacelift.io read 2026-10-28 (SaaS pages, and self-hosted v7.1.0 pages where noted); introspection `b3255fe38f5e`; terraform-provider-spacelift v1.57.0 at `c72b9bd2f3c3`; spacectl at `6bf167cb62a6`; terraform-aws-spacelift-selfhosted at `18c59d122853`.

Research baseline: **2026-10-28**.

This report records how Spacelift stores and guards OpenTofu and Terraform
state: the managed state backend and how runs reach it, the `manage_state`
choice and why it is fixed at creation, external access to state from
outside runs, importing state at creation and later, the pre-signed upload and
download URLs, state history and rollback, workspace locks for Terragrunt
units, and what the docs say about OpenTofu state encryption. It also notes
briefly how the other vendors keep state, since Spacelift does not manage it
for them. infraweave's state backend and its state API are designed from it.
Stack settings are in report 07, the run phases that read and write state in
report 08, workers in report 10, configuration variables such as
`SPACELIFT_DISABLE_STATE_LOCK` in report 13, roles in report 14, and the
resources view built from state in report 21.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

## 1. The managed state backend

### 1.1 How a run reaches it

| Aspect | Behaviour | Source |
|---|---|---|
| Offer | Optional, for Terraform, OpenTofu and Terragrunt stacks; recommended in the creation wizard | [D](https://docs.spacelift.io/vendors/terraform/state-management); [D](https://docs.spacelift.io/concepts/stack/creating-a-stack) |
| Code requirement | The code must declare no backend. Spacelift writes a one-off backend configuration, as a `.tf` file at the project root, into every run and task | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Conflict | If the code already declares a backend, initialization fails on every run until it is removed | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Protocol | An HTTP endpoint implementing Terraform's standard `http` backend protocol | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Credentials | Generated per run or task, one-off | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Authorization | Each request is accepted only if the credentials belong to a run or task Spacelift currently considers active, and that run's state says it should be reading or changing state; then the request is passed to the object store | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Storage | Amazon S3 in the account's data region, encrypted with AWS KMS keys | [D](https://docs.spacelift.io/vendors/terraform/state-management); [D](https://docs.spacelift.io/product/security) |
| Workspaces | The stack's `workspace` setting is ignored under managed state, because each stack has its own workspace | [D](https://docs.spacelift.io/concepts/stack/stack-settings) |
| Plan locking | `terraform plan` takes the state lock by default; `SPACELIFT_DISABLE_STATE_LOCK` in the stack environment turns that off, at the risk of wrong plans during a concurrent apply | [D](https://docs.spacelift.io/vendors/terraform/state-management) |

Which run states are allowed to read and which to write is not listed. That
planning reads and takes a lock, and applying, destroying and performing
tasks may write, follows from the phases in report 08 *(unverified)* [I].
The `http` backend's endpoint paths, lock semantics and credential format are
not documented *(unverified)*.

### 1.2 Self-hosted storage

| Aspect | Self-hosted behaviour | Source |
|---|---|---|
| Bucket | `OBJECT_STORAGE_BUCKET_STATES`, required, holding state for the built-in state server | [D](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage) |
| Versioning | Required on the states bucket | [D](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage) |
| Retention | Infinite | [D](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage) |
| Backends | AWS S3, Google Cloud Storage, Azure Blob Storage, S3-compatible stores (`OBJECT_STORAGE_TYPE`) | [D](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage) |
| Out-of-backend access | Uploads of state files from the frontend use time-limited pre-signed URLs | [D](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage) |
| S3-compatible stores | Must support object versioning on the states bucket, including listing versions and reading or copying a given version, because state history and rollback rely on it; path-style addressing; multipart uploads; pre-signed `GET` and `PUT` URLs signed with SigV4; lifecycle rules for the limited-retention buckets, since Spacelift deletes no expired objects itself | [D](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage) |
| AWS module | Creates `spacelift-states-<suffix>` with KMS server-side encryption, a public access block (on unless `enable_public_access_block_on_s3` is false) and versioning `Enabled`, and gives it no lifecycle rule; its noncurrent-version expiry variable lists other buckets, not states | [S](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L422-L457); [S](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L335-L339) |

State history and rollback (section 6) are built on the states bucket's
object versions [D](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage). SaaS keeps non-current versions 30 days
(section 6); self-hosted keeps them indefinitely unless the operator adds a
rule [I] from the "Infinite" retention and the module.

## 2. `manage_state`

### 2.1 Where the flag lives

| Surface | Name | Type | Default | Notes |
|---|---|---|---|---|
| GraphQL create | `stackCreate(manageState: Boolean!)` | required argument, not a `StackInput` field | none | [G] |
| GraphQL read | `Stack.managesStateFile` | `Boolean!` | | [G] |
| GraphQL update | none | | | `stackUpdate(id, input)` has no state argument, and `StackInput` has no state field [G] |
| Terragrunt vendor | `StackConfigVendorTerragruntInput.useStateManagement` | `Boolean` | provider `false` | Terragrunt's own switch [G]; [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L826-L831) |
| Terraform provider | `spacelift_stack.manage_state` | bool | `true` | `ForceNew`: a change replaces the stack [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L621-L627) |
| Runtime file | `terragrunt.use_state_management` in `.spacelift/config.yml`; a `terragrunt` block replaces every Terragrunt value rather than merging | bool | | Listed as a runtime key [D](https://docs.spacelift.io/concepts/configuration/runtime-configuration); each run records the value it used in `RuntimeConfig.terragrunt.useStateManagement: Boolean!` [G]. Whether a run can flip state management this way, against the creation-only rule below, is *(unverified)* |
| Run record | `RuntimeConfig.opentofu.externalStateAccessEnabled` | `Boolean!` | | The value a run used [G]; the runtime file cannot override OpenTofu external state access, which always comes from the stack [D](https://docs.spacelift.io/concepts/configuration/runtime-configuration) |
| Terraform provider | `spacelift_stack.terragrunt.use_state_management` | bool | `false` | Its description says it takes precedence over `manage_state` [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L826-L831); a change inside the block forces replacement [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L100-L105) |

### 2.2 Semantics

| Rule | Source |
|---|---|
| Managed state can only be chosen when the stack is created, and cannot be switched on or off afterwards | [D](https://docs.spacelift.io/vendors/terraform/state-management); [D](https://docs.spacelift.io/vendors/opentofu) |
| Moving to self-managed state means exporting the state and recreating the stack with managed state off | [D](https://docs.spacelift.io/faq) |
| During a vendor migration (adding or removing the provider's `terragrunt` block), the effective state-management value may not change; the provider refuses the plan and asks for a destroy and recreate | [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L75-L99) |
| When migrating a Terraform stack to the Terragrunt vendor, `use_state_management` is set to match the old `manage_state` | [D](https://docs.spacelift.io/vendors/terraform/terragrunt) |
| Migrating a Terraform stack that uses the OpenTofu workflow tool to the OpenTofu vendor carries `external_state_access_enabled` over | [D](https://docs.spacelift.io/vendors/opentofu/migration) |
| CDKTF stacks under managed state must remove the local backend CDKTF adds, or the injected backend conflicts | [D](https://docs.spacelift.io/vendors/terraform/cdktf) |
| Importing a state file requires managed state | [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L897-L912) |
| External state access requires managed state. The provider checks this only for `terraform_external_state_access`, not for `opentofu.external_state_access` | [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L926-L930); [D](https://docs.spacelift.io/vendors/opentofu) |
| Without managed state, the stack may set a workspace that Spacelift selects, or creates if selecting fails | [D](https://docs.spacelift.io/concepts/stack/stack-settings) |

The provider enforces the last two rules before calling the API. Whether the
API also refuses `stackObjectID` or `externalStateAccessEnabled` on a stack
without managed state is *(unverified)*.

### 2.3 Defaults disagree

The provider defaults `manage_state` to `true`; the GraphQL argument has no
default and must be sent; the UI marks the option "recommended"
[S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L621-L627);
[G]; [D](https://docs.spacelift.io/concepts/stack/creating-a-stack). The
provider defaults `terragrunt.use_state_management` to `false` and sends both
values on create: `manageState` (default `true`) as the mutation argument and
`useStateManagement` inside the Terragrunt vendor input. Its attribute
description says the Terragrunt flag takes precedence, which would leave a
Terragrunt stack created with defaults unmanaged
[S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L826-L831). Its own pre-flight checks for `import_state`,
`import_state_file` and `terraform_external_state_access` instead treat the
stack as managed when either value is true (a logical OR)
[S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L874-L882). Which value the API honours for a Terragrunt stack
when they differ is *(unverified)*.

## 3. External state access

| Aspect | Detail | Source |
|---|---|---|
| Purpose | Read a stack's state from outside its own authorized runs: `terraform_remote_state` from other stacks, or offline analysis | [D](https://docs.spacelift.io/vendors/terraform/external-state-access) |
| Default | Off | [D](https://docs.spacelift.io/vendors/terraform/external-state-access) |
| Read-only | The creation wizard describes it as external read-only access | [D](https://docs.spacelift.io/concepts/stack/creating-a-stack) |
| GraphQL | `externalStateAccessEnabled` on `StackConfigVendorTerraformInput` and `StackConfigVendorOpenTofuInput` (`Boolean`), read back as `Boolean!` on the vendor objects | [G] |
| Provider | `terraform_external_state_access` (default `false`) for Terraform; `opentofu.external_state_access` (default `false`) for OpenTofu | [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L763-L768); [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L583-L588); [D](https://docs.spacelift.io/vendors/terraform/external-state-access) |
| Who may read | A user or stack holding `Stack: Read stack state` (`STACK_STATE_READ`) and `Stack: Download stack state` (`STACK_STATE_DOWNLOAD`) on the stack's space; Space writer and Space admin include both | [D](https://docs.spacelift.io/vendors/terraform/external-state-access); [D](https://docs.spacelift.io/concepts/authorization/assigning-roles-stacks); [G] |
| Protocol | The `remote` backend (the Terraform Cloud API shape), with `hostname` the Spacelift host (`spacelift.io`, or `us.spacelift.io` for the US instance), `organization` the account name, and `workspaces.name` the stack ID | [D](https://docs.spacelift.io/vendors/terraform/external-state-access) |
| Offline login | `terraform login spacelift.io` obtains a token, then `terraform state pull` | [D](https://docs.spacelift.io/vendors/terraform/external-state-access) |
| Change | A fix dated 2026-06-01 allows external state access with the `stack:state-read` role | [RN](https://docs.spacelift.io/product/changelog) |

Spacelift therefore serves state over two protocols: the `http` backend for
its own runs, and a subset of the Terraform Cloud `remote` backend API for
external readers [D]. Which endpoints of that API are implemented is not
documented *(unverified)*. On Terragrunt stacks under managed state, a unit
cannot read another stack's state, so Terragrunt external dependencies that
read a state outside the stack do not work
[D](https://docs.spacelift.io/vendors/terragrunt/limitations).

## 4. Pre-signed state URLs

### 4.1 Operations

| Operation | Arguments | Returns | Behaviour |
|---|---|---|---|
| `stateUploadUrl` | none | `StateUploadUrl!` | Generates a pre-signed upload URL [G] |
| `stateDownloadUrl` | `input: StateDownloadUrlInput!` | `StateDownloadUrl!` | Generates a pre-signed download URL for the stack's state; requires external state access on the stack [G] |
| `stackCreate` | `input`, `manageState: Boolean!`, `stackObjectID: String`, `slug: String` | `Stack!` | `stackObjectID` hands pre-existing state to the new stack [G] |
| `stackManagedStateImport` | `stackId: ID!`, `state: String!` | `Boolean` | Imports an uploaded file into a managed-state stack; `state` is the `objectId` from `stateUploadUrl` [G] |

| `StateUploadUrl` field | Type | Meaning |
|---|---|---|
| `objectId` | `String!` | Temporary object ID to pass to `stackCreate` or `stackManagedStateImport` [G] |
| `url` | `String!` | Pre-signed URL for the upload [G] |
| `uploadHeaders` | `StringMap` | HTTP headers to send with the upload [G] |

| `StateDownloadUrlInput` field | Type | Meaning |
|---|---|---|
| `stackId` | `ID!` | Stack whose state to download [G] |
| `version` | `String` | A state version; the schema says it is not supported yet and returns an error if sent, so only the latest state downloads [G] |

`StateDownloadUrl` has one field, `url: String!` [G].

### 4.2 How clients use them

| Client | Flow | Source |
|---|---|---|
| Terraform provider, create | Calls `stateUploadUrl`, `PUT`s the state body with `Content-Type: application/json`, treats any non-2xx as failure, then passes `objectId` as `stackObjectID` to `stackCreate`. It does not send `uploadHeaders` | [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L1524-L1551) |
| spacectl `stack state pull` | Calls `stateDownloadUrl` with only `stackId`, `GET`s the URL, expects `200`, and writes the body to stdout or to `--output`/`-o` with mode `0600` | [S](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/state.go#L17-L75) |
| UI import | Uploads through a pre-signed URL from the browser | [D](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage) |

The URLs' lifetime, and how long an uploaded `objectId` stays valid before it
is imported, are not documented *(unverified)*. On self-hosted, frontend
uploads land in the uploads bucket with a one-day retention, which bounds it
there [I] from
[D](https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage).

## 5. Importing state

### 5.1 At stack creation

| Path | Detail | Source |
|---|---|---|
| UI | "Import existing state file" under Manage State in the creation wizard | [D](https://docs.spacelift.io/concepts/stack/creating-a-stack) |
| GraphQL | `stateUploadUrl`, upload, then `stackCreate(manageState: true, stackObjectID: <objectId>)` | [G] |
| Provider | `import_state` (sensitive string, the state body) or `import_state_file` (a path), mutually exclusive; both ignored after creation, and `import_state` is blanked from Terraform state once used | [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L512-L526); [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go#L897-L924) |

### 5.2 On an existing stack

| Condition or limit | Source |
|---|---|
| The caller is a stack admin (`STACK_MANAGED_STATE_IMPORT`, default Admin) | [D](https://docs.spacelift.io/vendors/terraform/state-management); [D](https://docs.spacelift.io/concepts/authorization/rbac-system); [G] |
| The stack is locked | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| The stack has no pending runs or tasks | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| The file is at most 100 MB | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| The import lands on top of the latest managed state as a new version marked as manually imported | [D](https://docs.spacelift.io/vendors/terraform/state-management); [G] (`ManagedStateVersion.imported`) |

Stack locking is described in report 07. Which run states count as "pending"
is not listed *(unverified)*.

### 5.3 Importing resources, not files

Bringing an existing cloud resource under management uses `terraform import`
(or `terragrunt import`) in a task; this works whether or not Spacelift manages
the state [D](https://docs.spacelift.io/vendors/terraform/state-management);
[D](https://docs.spacelift.io/faq). Any `terraform state` subcommand can be run
the same way [D](https://docs.spacelift.io/faq).

## 6. State history and rollback

### 6.1 `ManagedStateVersion`

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Version ID [G] |
| `run` | `Run` | The run that produced the state; for a rollback, the run that produced the restored version [G] |
| `lastModified` | `Int!` | Unix time of the last change [G] |
| `rollback` | `Boolean!` | This version is a rollback [G] |
| `rollbackUsername` | `String` | Who rolled back [G] |
| `size` | `Int!` | Bytes [G] |
| `imported` | `Boolean!` | Manually imported [G] |
| `importedUsername` | `String` | Who imported [G] |
| `importedTimestamp` | `Int` | For a rollback to an imported version, the original import time [G] |

| Operation | Arguments | Returns |
|---|---|---|
| `Stack.searchManagedStateVersions` | `input: SearchInput!` | `ManagedStateVersionOutput` (`edges: [ManagedStateVersionConnection!]!` of `{cursor, node}`, `pageInfo`) [G] |
| `stackManagedStateRollback` | `stackId: ID!`, `managedStateVersionId: String!` | `ManagedStateVersion!`, the new version [G] |
| `Stack.isStateRollback` | | `Boolean!`, true while the current state is a rollback [G] |

The connection type's description calls each edge "a single worker pool", a
copy error in the schema [G].

### 6.2 Behaviour

| Rule | Source |
|---|---|
| The stack's **State history** tab lists state changes | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Not every run or task makes a version; a run with no changes creates none | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Non-current versions are kept 30 days (SaaS) | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Rollback changes only the state, never the infrastructure; runs or tasks must follow to reconcile | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Rollback is a break-glass action for a freshly corrupted state | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| Conditions: stack admin (`STACK_MANAGED_STATE_ROLLBACK`, default Admin), stack locked, no pending runs or tasks | [D](https://docs.spacelift.io/vendors/terraform/state-management); [D](https://docs.spacelift.io/concepts/authorization/rbac-system); [G] |
| A successful rollback adds a new version at the top, marked as a rollback | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| While the current state is a rollback, the stack header shows a warning | [D](https://docs.spacelift.io/vendors/terraform/state-management); [G] (`isStateRollback`) |

What happens when the version to restore is older than 30 days and expired,
and whether `isStateRollback` clears on the next run's write, are not
documented *(unverified)*; the second follows from "currently using a
rolled-back state" [I].

## 7. Exporting state

| Method | Detail | Source |
|---|---|---|
| Task | `terraform state pull`, piped to a destination such as `aws s3 cp - s3://…`, because the task's workspace is discarded afterwards; the stack needs write access to the destination | [D](https://docs.spacelift.io/vendors/terraform/state-management); [D](https://docs.spacelift.io/faq) |
| External access | `terraform state pull` against the `remote` backend, or spacectl `stack state pull` | [D](https://docs.spacelift.io/vendors/terraform/external-state-access); [S](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/state.go#L23-L75) |
| Before deletion | Retrieve the state with a task before deleting a stack | [D](https://docs.spacelift.io/concepts/stack/creating-a-stack) |
| Disaster continuity | Spacelift's own continuity guidance recommends self-managed, versioned, replicated state for break-glass access | [D](https://docs.spacelift.io/product/disaster-continuity) |

## 8. Terragrunt units and workspace locks

| Aspect | Detail | Source |
|---|---|---|
| Layout | Under managed state Spacelift configures a remote backend per unit; the workspace name is a base name plus the unit's path, so each unit has its own state | [D](https://docs.spacelift.io/vendors/terragrunt/reference) |
| Visibility | A **Workspaces** tab lists workspaces and their lock status | [D](https://docs.spacelift.io/vendors/terragrunt/reference) |
| Stuck locks | A run stopped before completion can leave a workspace locked; unlock one, or all | [D](https://docs.spacelift.io/vendors/terragrunt/reference) |
| GraphQL | `tofuWorkspaceUnlock(id: ID!): TofuWorkspace!`, `tofuWorkspaceUnlockAll(stack: ID!): TofuWorkspaceUnlockAllResponse!` (`unlockedWorkspaces`), and the queries `searchTofuWorkspaces(input: SearchInput!)` and `searchTofuWorkspacesSuggestions`. No mutation locks a workspace | [G] |
| `TofuWorkspace` | `id: ID!`, `name: String!`, `isLocked: Boolean!`, `lockedBy: String`, `createdAt: Int!` | [G] |
| Actions | `STACK_WORKSPACE_LOCK` and `STACK_WORKSPACE_UNLOCK`, default Admin; the lock action has no matching mutation, so it presumably governs locks taken through the backend [I] | [G]; [D](https://docs.spacelift.io/concepts/authorization/rbac-system) |

The schema describes `TofuWorkspace` as a workspace "used for local
development" [G], while the docs present the same tab for Terragrunt units
[D]. Whether these locks are the managed backend's own `http` locks, and
whether non-Terragrunt stacks expose workspaces here, is *(unverified)*.

## 9. OpenTofu state encryption

| Fact | Source |
|---|---|
| The managed backend and external access apply to native OpenTofu stacks exactly as to Terraform stacks | [D](https://docs.spacelift.io/vendors/terraform/state-management); [D](https://docs.spacelift.io/vendors/terraform/external-state-access) |
| Spacelift lists "State encryption" as an OpenTofu feature enabled only when the run's resolved OpenTofu version is 1.7 or later | [D](https://docs.spacelift.io/vendors/opentofu) |

The docs say nothing more: not how encryption is configured on Spacelift
(OpenTofu's own `encryption` block or `TF_ENCRYPTION` variable), not whether
key material should come from a context, and not what happens to features
that parse state (outputs for stack dependencies, the resources view, plan
policies) when the state Spacelift stores is encrypted. All of that is
*(unverified)*. Because encryption happens inside the OpenTofu process before
the backend call, the managed backend would store ciphertext it cannot read
[I] from OpenTofu's client-side design.

## 10. Other vendors

| Vendor | Who holds state | Source |
|---|---|---|
| Pulumi | The user's backend, given as `loginURL` (a bucket such as `s3://…`, or the Pulumi Service with `PULUMI_ACCESS_TOKEN`); runs `pulumi login` with it; `stackName` namespaces the state. Spacelift's state import does not apply; resources are imported in a task | [D](https://docs.spacelift.io/vendors/pulumi); [D](https://docs.spacelift.io/vendors/pulumi/golang); [G] (`StackConfigVendorPulumiInput`) |
| CloudFormation | AWS; no separate state. A template bucket holds processed templates | [D](https://docs.spacelift.io/vendors/cloudformation); [D](https://docs.spacelift.io/concepts/stack/creating-a-stack) |
| Kubernetes | The cluster; changes are previewed with `kubectl apply` dry runs | [D](https://docs.spacelift.io/vendors/kubernetes/getting-started) |
| Ansible | No managed state is offered [I] from the absence of any state option for Ansible; planning runs Ansible in check mode | [D](https://docs.spacelift.io/vendors/ansible/reference) |
| Terragrunt | Managed per unit when `useStateManagement` is on (section 8), otherwise the user's backend | [D](https://docs.spacelift.io/vendors/terragrunt/reference); [G] |

## 11. Typing traps

| Trap | Detail |
|---|---|
| `manageState` | A required argument of `stackCreate`, not a `StackInput` field, and absent from updates [G] |
| Read name | Written as `manageState`, read as `managesStateFile` [G]; [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/structs/stack.go#L76) |
| ID spellings | `stackObjectID` on `stackCreate`, `objectId` on `StateUploadUrl`, `state` on `stackManagedStateImport` carry the same value [G] |
| Version ID type | `managedStateVersionId` is a `String!`, while `ManagedStateVersion.id` is an `ID!` [G] |
| Import result | `stackManagedStateImport` returns a nullable `Boolean`, not the new version [G] |
| Download version | `StateDownloadUrlInput.version` exists but errors when sent [G] |
| State versus stack state | `Stack.state` is the `StackState` enum (report 07), unrelated to the state file [G] |

## What this means for infraweave

- A state backend speaking Terraform's `http` protocol to runs, with per-run
  credentials checked against the run's liveness and phase, plus locking that
  plans can opt out of.
- A read-only external path for other stacks and offline tools, gated by a
  per-stack switch and two space-scoped permissions; Spacelift uses the
  Terraform Cloud `remote` API for it.
- A managed-state choice fixed at creation, with a Terragrunt-specific twin,
  and refusals for import and external access when it is off.
- Pre-signed upload and download URLs, an import path at creation and on a
  locked, idle stack (100 MB cap), versioned history with import and rollback
  markers, rollback as a new version, and retention of non-current versions.
- Per-unit workspaces and their locks for Terragrunt, with unlock operations.
- A position on OpenTofu client-side encryption, which Spacelift leaves
  undocumented.

## Inferred and undocumented

- Self-hosted retention of non-current versions being unbounded, from the
  "Infinite" retention and the AWS module.
- Which run states may read or write state; the `http` backend's paths, lock
  semantics and credentials; the `remote` API subset served externally.
- Whether the API itself, not only the provider, refuses import and external
  access without managed state, and which of `manageState` and
  `useStateManagement` it honours for a Terragrunt stack when they differ,
  and whether `terragrunt.use_state_management` in the runtime file changes
  state management per run.
- URL lifetimes and the life of an uploaded `objectId`.
- Which runs count as pending for import and rollback; rollback to an expired
  version; when `isStateRollback` clears.
- How `TofuWorkspace` locks relate to the backend's locks.
- OpenTofu encryption: configuration and its effect on state-parsing features.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e`, fetched 2026-10-28 |
| docs.spacelift.io | read 2026-10-28; self-hosted pages at v7.1.0 |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| spacectl | `6bf167cb62a6` |
| terraform-aws-spacelift-selfhosted | `18c59d122853` |

## Sources

- [D] <https://docs.spacelift.io/vendors/terraform/state-management>, read 2026-10-28.
- [D] <https://docs.spacelift.io/vendors/terraform/external-state-access>, read 2026-10-28.
- [D] <https://docs.spacelift.io/vendors/opentofu>, <https://docs.spacelift.io/vendors/opentofu/migration>, <https://docs.spacelift.io/vendors/terraform/cdktf>, <https://docs.spacelift.io/concepts/configuration/runtime-configuration>, read 2026-10-28.
- [D] <https://docs.spacelift.io/vendors/terragrunt/reference>, <https://docs.spacelift.io/vendors/terragrunt/limitations>, <https://docs.spacelift.io/vendors/terraform/terragrunt>, read 2026-10-28.
- [D] <https://docs.spacelift.io/vendors/pulumi>, <https://docs.spacelift.io/vendors/pulumi/golang>, <https://docs.spacelift.io/vendors/cloudformation>, <https://docs.spacelift.io/vendors/kubernetes/getting-started>, <https://docs.spacelift.io/vendors/ansible/reference>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/stack/creating-a-stack>, <https://docs.spacelift.io/concepts/stack/stack-settings>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/authorization/assigning-roles-stacks>, <https://docs.spacelift.io/concepts/authorization/rbac-system>, read 2026-10-28.
- [D] <https://docs.spacelift.io/faq>, <https://docs.spacelift.io/product/security>, <https://docs.spacelift.io/product/disaster-continuity>, read 2026-10-28.
- [D] <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage>, read 2026-10-28.
- [RN] <https://docs.spacelift.io/product/changelog>, entry of 2026-06-01.
- [G] [`api/reference/spacelift/introspection.json`](../../../api/reference/spacelift/introspection.json): `StateUploadUrl`, `StateDownloadUrl`, `StateDownloadUrlInput`, `ManagedStateVersion`, `ManagedStateVersionOutput`, `ManagedStateVersionConnection`, `TofuWorkspace`, `TofuWorkspaceUnlockAllResponse`, `TerragruntRuntimeConfig`, `OpenTofuRuntimeConfig`, the vendor config inputs, `Action`, and the root fields named above. Tables generated by script from the snapshot.
- [S] terraform-provider-spacelift at `c72b9bd2f3c3` (MIT): [`spacelift/resource_stack.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_stack.go), [`spacelift/internal/structs/stack.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/internal/structs/stack.go).
- [S] spacectl at `6bf167cb62a6` (MIT): [`internal/cmd/stack/state.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/state.go).
- [S] terraform-aws-spacelift-selfhosted at `18c59d122853` (MIT): [`modules/s3/main.tf`](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/modules/s3/main.tf#L422-L457), [`variables.tf`](https://github.com/spacelift-io/terraform-aws-spacelift-selfhosted/blob/18c59d122853/variables.tf#L335-L339).
- [`api/reference/spacelift/terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json): `spacelift_stack` (`manage_state`, `import_state`, `import_state_file`, `terraform_external_state_access`).
