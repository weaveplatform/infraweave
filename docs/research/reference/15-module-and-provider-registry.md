# 15: Module and provider registry

Checked against: docs.spacelift.io read 2026-10-28 (SaaS and `self-hosted/latest`, local mirror); live registry endpoints on `spacelift.io`, `app.spacelift.io`, `app.us.spacelift.io` and `registry.spacelift.io` read 2026-10-28; introspection `b3255fe38f5e`; terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); spacectl `6bf167cb62a6`.

Research baseline: **2026-10-28**.

This report records Spacelift's private Terraform registry: the module registry and the
provider registry. For modules it covers the module as a stack-like entity and every
setting it carries, the `.spacelift/config.yml` keys that only modules read, test cases
and the test runs they produce, versions with their states and transitions, marking a
version as bad, sharing with spaces, other accounts and the public, and the policies that
act on modules. For providers it covers providers, versions and their status, platforms
and artifacts, GPG keys, verification and documentation upload. It then records the
Terraform registry protocol that Spacelift serves (service discovery, module and provider
endpoints, host names and authentication), external module sources, and the Terraform
provider resources and spacectl commands that drive both registries. infraweave's registry
service, its module test pipeline and its registry protocol handlers are designed from it.
The operations themselves are inventoried with their REST routes in report 03; run types
and phases, including the `TESTING` run type, are in report 08; push, trigger and
notification policy semantics are in report 09; workers and runner images in report 10;
VCS providers in report 11; cloud integrations in report 12; contexts and mounted files in
report 13; spaces and RBAC in report 14; Intent's module deployment in report 22. Stack
settings that modules share are defined in report 07, and the provider extraction in
[05-terraform-provider.md](05-terraform-provider.md).

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` ([`introspection.json`](../../../api/reference/spacelift/introspection.json)) |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog, <https://docs.spacelift.io/product/changelog> |
| **[C]** | A live capture of a public, unauthenticated registry endpoint, taken 2026-10-28 with `curl` |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

Short names for the pages and files cited below:

| Short name | URL |
|---|---|
| module-registry | <https://docs.spacelift.io/vendors/terraform/module-registry> |
| provider-registry | <https://docs.spacelift.io/vendors/terraform/provider-registry> |
| external-modules | <https://docs.spacelift.io/vendors/terraform/external-modules> |
| cli-configuration | <https://docs.spacelift.io/vendors/terraform/cli-configuration> |
| test-case | <https://docs.spacelift.io/concepts/run/test-case> |
| runtime-yaml | <https://docs.spacelift.io/concepts/configuration/runtime-configuration/runtime-yaml-reference> |
| rbac | <https://docs.spacelift.io/concepts/authorization/rbac-system> |
| push-policy | <https://docs.spacelift.io/concepts/policy/push-policy> |
| trigger-policy | <https://docs.spacelift.io/concepts/policy/trigger-policy> |
| worker-pools | <https://docs.spacelift.io/concepts/worker-pools> |
| sh-module-registry | <https://docs.spacelift.io/self-hosted/latest/vendors/terraform/module-registry> |
| sh-provider-registry | <https://docs.spacelift.io/self-hosted/latest/vendors/terraform/provider-registry> |
| sh-first-setup | <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/first-setup> |
| sh-object-storage | <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage> |
| sh-changelog | <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog> |
| tf-module | <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_module.go> |
| tf-provider | <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_terraform_provider.go> |
| tf-version | <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_version.go> |
| ctl-module | <https://github.com/spacelift-io/spacectl/tree/6bf167cb62a6/internal/cmd/module> |
| ctl-provider | <https://github.com/spacelift-io/spacectl/tree/6bf167cb62a6/internal/cmd/provider> |

## 1. Tiers and where the registries live

| Fact | Evidence |
|---|---|
| The module registry is available to paid accounts only. | [D] module-registry |
| The provider registry is available on the Business plan and above. | [D] provider-registry |
| Neither tier note appears on the self-hosted pages; self-hosted has both registries. | [D] sh-module-registry, sh-provider-registry |
| Modules and providers are created under **Terraform registry** in the account view; providers also under **Share Code & Config > Providers**. | [D] module-registry, provider-registry |
| Module source archives and provider binaries share one object store bucket on self-hosted (`OBJECT_STORAGE_BUCKET_MODULES`, the "Modules" bucket: versioning suggested, infinite retention). | [D] sh-object-storage |
| Uploaded local-preview workspaces go to the "User uploaded workspaces" bucket, kept 1 day. | [D] sh-object-storage |

## 2. The module entity

A module is a stack-like object: it tracks a repository and branch, has a project root,
labels, a space, a worker pool, a runner image, attached contexts, policies and cloud
integrations, environment variables and mounted files, and produces runs. It differs from
a stack in four ways: it holds no state and manages no resources, its runs are test cases
that apply and then destroy, a successful tracked change produces a version, and it is
consumed through the registry protocol rather than deployed [D] module-registry; [D]
test-case. The schema describes `Module` as a versioned, tested, reusable collection of
resources [G].

### 2.1 Naming and addressing

| Item | Rule | Evidence |
|---|---|---|
| Registry address | `spacelift.io/<organization>/<module_name>/<provider>`: registry host, owning organization, module name, main Terraform provider. | [D] module-registry |
| Organization part | The account name; self-hosted docs state the account name is part of the URL for modules and providers in the private registry. | [D] sh-first-setup |
| `Module.moduleSource` | The value to put in a Terraform `source` argument. | [G] |
| Name and provider inference | Inferred from a repository named `terraform-<provider>-<name>`; otherwise set explicitly. | [D] module-registry |
| Module ID (slug) | `terraform-<provider>-<name>`, built from the name and provider. | [D] module-registry |
| ID immutability | `Module.id` is unique within one organization and immutable. | [G] |
| Name format in the Terraform provider | `^[a-z][a-z0-9-_]*[a-z0-9]$`: starts with a lowercase letter, ends with a lowercase letter or digit, and contains only lowercase letters, digits, dashes and underscores. `name` and `terraform_provider` force replacement when changed. | [S] [tf-module L233](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_module.go#L233) |
| Repository filtering in the UI | Repositories whose names do not follow the convention are filtered out of the create wizard's repository picker. | [D] module-registry |
| Monorepos | A module may live in a subdirectory, selected by the project root; one repository can hold several modules. | [D] module-registry |
| Symbolic links | Only work when a runner image is set to process module metadata. | [D] module-registry |
| OpenTofu | Modules cannot use the native OpenTofu vendor. To test with OpenTofu, keep the Terraform vendor and set `workflowTool` to `OPEN_TOFU`. OpenTofu stacks consume modules normally. | [D] module-registry |

### 2.2 Writable settings

The create input nests most settings in `updateInput` (`ModuleUpdateInput`); the v1 update
cannot change the source; `moduleUpdateV2` can [G]. The Terraform provider creates with
`moduleCreate` and updates with `moduleUpdateV2` [S] [tf-module](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_module.go#L476).

| Setting | `ModuleCreateInput` | `ModuleUpdateInput` | `ModuleUpdateV2Input` | Terraform attribute | Default and constraints |
|---|---|---|---|---|---|
| Repository | `repository: String!` | none | `repository: String!` | `repository` (required) | Repository name without the owner [S] |
| Repository URL | `repositoryURL` | none | `repositoryURL` | `raw_git.url` | Used by raw Git sources [S] |
| Namespace | `namespace` | none | `namespace` | `namespace` inside the VCS block | Defaults to the linked GitHub account [G] |
| VCS provider | `provider: VCSProvider` | none | `provider` | one of the VCS blocks (`azure_devops`, `bitbucket_cloud`, `bitbucket_datacenter`, `github_enterprise`, `gitlab`, `raw_git`, `spacelift_repo`, `origin`) | Values are in report 11 [G]; a `spacelift_repo` source needs `branch = "main"` and the repo in the module's space [S] [tf-module L328](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_module.go#L328) |
| VCS integration | `vcsIntegrationId` | none | `vcsIntegrationId` | `id` inside the VCS block | Only for a non-default (space-level) integration [G] |
| Name | `name` | none | none | `name` | Optional, inferred; immutable through the provider [S] |
| Terraform provider | `terraformProvider` | none | none | `terraform_provider` | Optional, inferred; immutable through the provider [S] |
| Space | `space` (deprecated 2026-10-20 in favour of `updateInput.space` [RN]) | `space` | `space` | `space_id` | Computed when unset [S] |
| Workflow tool | `workflowTool: TerraformWorkflowTool` | none | `workflowTool` | `workflow_tool` | `TERRAFORM_FOSS` when unset: the attribute is `Computed` with no provider-side default, and its description names the server default [S]; values in 2.4 |
| Branch | in `updateInput` | `branch: String!` | `branch: String!` | `branch` (required) | Tracked branch |
| Administrative | in `updateInput` | `administrative` | `administrative` | `administrative` | `false` [S]; only needed when the module manages Spacelift resources [D]; unaffected by the stack administrative-flag retirement [RN] 2025-11-10 |
| Description | in `updateInput` | `description` | `description` | `description` | Markdown [G] |
| GitHub Deploy action | in `updateInput` | `githubActionDeploy` | `githubActionDeploy` | none | `true` [G] |
| Disabled | in `updateInput` | `isDisabled` | none (use `moduleDisable` and `moduleEnable`) | none | A disabled module creates no runs [G] |
| Labels | in `updateInput` | `labels` | `labels` | `labels` | Drive `autoattach:` for contexts, policies, AWS and Azure integrations [D] module-registry |
| Local preview | in `updateInput` | `localPreviewEnabled` | `localPreviewEnabled` | `enable_local_preview` | `false` [S] |
| Skip vendor binary download | in `updateInput` | `skipVendorBinaryDownload` | `skipVendorBinaryDownload` | none | No default in the schema |
| Project root | in `updateInput` | `projectRoot` | `projectRoot` | `project_root` | Directory of the module root [G] |
| Sparse checkout paths | in `updateInput` | `gitSparseCheckoutPaths` | `gitSparseCheckoutPaths` | `git_sparse_checkout_paths` | Empty means a full checkout [G] |
| Protect from deletion | in `updateInput` | `protectFromDeletion` | `protectFromDeletion` | `protect_from_deletion` | `false` [S] |
| Shared accounts | in `updateInput` | `sharedAccounts` | `sharedAccounts` | `shared_accounts` | Account subdomains; see section 7 |
| Worker pool | in `updateInput` | `workerPool` | `workerPool` | `worker_pool_id` | Empty means the public pool; the provider says it is required on self-hosted [S] |
| Runner image | in `updateInput` | `runnerImage` | `runnerImage` | `runner_image` | Image used to parse module metadata [G]; the docs name `public.ecr.aws/spacelift/runner-terraform:latest` as the public option [D] |
| Public | none | none | none (use `modulePublish`) | `public` | `false`; settable only at creation, forces replacement [S] [tf-module L261](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_module.go#L261) |
| Space shares | none (use `moduleShare`) | none | none | `space_shares` | Space IDs; see section 7 |

Environment variables, mounted files, contexts, policies and cloud integrations attach to
a module exactly as to a stack; the environment and mounted files reach every test case
[D] module-registry. The policy types the module wizard offers are approval, plan, push and
trigger [D] module-registry.

### 2.3 The `Module` object

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Unique (within a single organization), immutable name of the Module. |
| `space` | `ID!` | ID of the space this Module belongs to. |
| `spaceDetails` | `Space!` | Details of the space this Module belongs to. |
| `administrative` | `Boolean!` | Administrative modules can manage other Stacks at runtime. |
| `apiHost` | `String` | API host, useful for building links in the GUI. It may be empty if the Module's original VCS provider has been removed. |
| `attachedAzureIntegrations` | `[StackAzureIntegrationAttachment!]!` | List of Azure integrations attached to this Module. |
| `attachedContexts` | `[StackContextAttachment!]!` | List of contexts attached to this Module. |
| `attachedPolicies` | `[StackPolicyAttachment!]!` | List of policies attached to this Module. |
| `attachablePolicies` | `[Policy!]!` | List of policies that can be attached to this Module. |
| `attachableWorkerPools` | `[WorkerPool!]!` | List of worker pools that can be attached to this Module. |
| `attachableContexts` | `[Context!]!` | List of contexts that can be attached to this Module. |
| `attachableVcsIntegrations` | `[VCSIntegration!]!` | List of vcs integrations that can be attached to this Module. |
| `attachableAwsIntegrations` | `[AwsIntegration!]!` | List of aws integrations that can be attached to this Module. |
| `attachableAzureIntegrations` | `[AzureIntegration!]!` | List of azure integrations that can be attached to this Module. |
| `branch` | `String!` | Git branch of the repo that the Module is tracking. |
| `canWrite` | `Boolean!` | Whether the user has write privileges for this Module. |
| `canRead` | `Boolean!` | Whether the user has read privileges for this Module. |
| `configElement(id: ID!)` | `ConfigElement` | Single Module config element. |
| `config` | `[ConfigElement!]!` | Full list of module config elements. |
| `createdAt` | `Int!` | Timestamp representing the module creation. |
| `lastPulledAt` | `Int` | LastPulledAt is unix timestamp (in seconds) at which any version of this module was most recently downloaded. Null if no version has ever been pulled. For shared or public modules this timestamp reflects usage across all consumers, not just the requesting account. |
| `current` | `Version` | Current module version. |
| `description` | `String` | Free-form module description for the users, supports Markdown. |
| `githubActionDeploy` | `Boolean!` | Indicates whether the GitHub Checks "Deploy" action is enabled. |
| `integrations` | `StackIntegrations` | Integrations between a Module and external systems. |
| `isDisabled` | `Boolean!` | If module is disabled, it will not be able to create any runs. |
| `labels` | `[String!]!` | Labels applied to a Module. |
| `localPreviewEnabled` | `Boolean!` | Whether triggering local workspace based Versions on this Module is enabled. |
| `skipVendorBinaryDownload` | `Boolean!` | Whether vendor binary downloads should be skipped during runs. |
| `latest` | `Version` | Latest module version. |
| `moduleSource` | `String!` | The value to use for the Terraform `source` property when accessing the module. |
| `name` | `String!` | Provider-independent name of the Terraform module. |
| `namespace` | `String!` | Repository namespace. |
| `ownerSubdomain` | `String!` | Subdomain of the account owner, if the module is owned by another account and just shared with the current one, this will be used to point the user to the "canonical" location of the module. |
| `pullRequests` | `[PullRequest!]!` | List of up to 50 most recent open Pull Requests targeting Module's tracked branch. |
| `projectRoot` | `String` | Project root is the optional directory relative to the workspace root containing the root directory of the module. |
| `gitSparseCheckoutPaths` | `[String!]` | The list of paths that are used for sparse checkout, if any. If empty sparse checkout is not used, whole repository is checked out. Note: this field is only used if git checkout is enabled. See https://docs.spacelift.io/concepts/stack/stack-settings#labels |
| `protectFromDeletion` | `Boolean!` | Indicates whether deletion protection is currently "on" for the stack. |
| `provider` | `VCSProvider!` | Provider of the repository |
| `public` | `Boolean!` | Public indicates that the Module is shared with the entire world. Public Modules cannot be deleted or marked private. |
| `repository` | `String!` | GitHub repository tracked by this Module, as account/repo. |
| `repositoryTags` | `[String!]!` | A list of tags for the tracked repository. |
| `repositoryURL` | `String` | Git repository URL tracked by this Module. |
| `run(id: ID!)` | `Run` | Individual Run (or task) by ID. |
| `runsInRange(start: String!, end: String!)` | `[Run!]!` | List of runs from a start timestamp inclusive to an end timestamp exclusive. Caveat: It uses Ints serialized to Strings, because serializing Ints to JSON is apparently really hard in JavaScript. Also, note that the maximum supported range is 8 days (691,200 seconds). |
| `runtimeConfig` | `[ConfigElementWithSource!]!` | Calculated runtime config containing elements coming from contexts, Module and various integrations. |
| `policyAttachment(id: ID!)` | `StackPolicyAttachment` | Retrieve module policy attachment by a known ID. |
| `sharedAccounts` | `[String!]!` | List of the accounts (subdomains) which have access to the Module. Deprecated: Use moduleShares field instead. |
| `moduleShares` | `[ModuleShare!]!` | List of the accounts and spaces which have access to the Module. |
| `terraformProvider` | `String!` | TerraformProvider represents Module's Terraform provider |
| `vcsIntegration` | `VCSIntegration` | The VCS integration used by this module. This may either be a default integration, or a space-level integration. If a space-level integration is being used by the module, and that integration is deleted, this property will be null. Additionally, if the module is using a default integration, and none exists, this property will be null. |
| `starred` | `Boolean!` | Is the module starred by the current user. |
| `versions(before: ID, includeFailed: Boolean)` | `[Version!]!` | Paginate through all Versions of the current Module. By default, only ACTIVE and PENDING versions are shown, but setting includeFailed to true will show all versions. |
| `searchModuleVersions(input: SearchInput!)` | `SearchVersionsOutput!` | No description in the schema |
| `searchModuleVersionsSuggestions(input: SearchSuggestionsInput!)` | `SearchSuggestionsOutput!` | No description in the schema |
| `version(id: ID!)` | `Version` | Retrieve a single Version by its ID. |
| `versionFind(term: String!)` | `Version` | Find a Version by either its ID or version number. |
| `workerPool` | `WorkerPool` | WorkerPool associated with the Module, if any. If the Module is associated with the shared default worker pool, this field is empty. |
| `workflowTool` | `TerraformWorkflowTool!` | The tool used by the stack for executing workflow stages. |
| `versionsCount(state: VersionState)` | `Int!` | VersionsCount returns the number of versions of the module in a specified state. If state is not provided, it will return the count of active versions. |
| `runnerImage` | `String` | Optional runner image used to process the module metadata during parsing Run. |

### 2.4 Workflow tool

| Value | Meaning [G] |
|---|---|
| `TERRAFORM_FOSS` | The open source licensed version of Terraform. |
| `CUSTOM` | A custom workflow tool. |
| `OPEN_TOFU` | OpenTofu. |

The custom workflow tool reads `.spacelift/workflow.yml` and is described in report 20.

## 3. Module operations

| Kind | Operation | Arguments | Returns | Meaning [G] |
|---|---|---|---|---|
| Q | `sharedModulesV2` | none | `[ModuleShare!]!` | List of all the Modules shared with this account or with spaces inside the account. |
| Q | `module` | `id: ID!` | `Module` | Details of a single Module. |
| Q | `modules` | none | `[Module!]!` | List of all the Modules managed by this account. |
| Q | `searchModules` | `input: SearchInput!`, `returnModuleSharing: Boolean` | `SearchModulesOutput!` | Search Modules with a set of predicates, ordering and paging. |
| Q | `searchModulesSuggestions` | `input: SearchSuggestionsInput!`, `returnModuleSharing: Boolean` | `SearchSuggestionsOutput!` | Information about fields available for searching Modules. |
| Q | `sharedModules` | none | `[SharedModule!]!` | List of all the Modules shared with this account. Deprecated: Use sharedModulesV2 field instead. |
| M | `uploadLocalWorkspace` | `stack: ID!` | `LocalWorkspaceUpload!` | Upload workspace for later processing in other mutations. |
| M | `moduleShare` | `id: ID!`, `input: ModuleShareInput!` | `[ModuleShare!]!` | Share a module with another account or another space |
| M | `moduleCreate` | `input: ModuleCreateInput!` | `Module!` | Create a new Module. |
| M | `moduleDelete` | `id: ID!` | `Module` | Delete an existing Module, assuming it has no active versions. |
| M | `moduleDisable` | `id: ID!` | `Module!` | Disable the module, so it cannot create new runs. |
| M | `moduleEnable` | `id: ID!` | `Module!` | Enable the module. |
| M | `modulePublish` | `id: ID!` | `Module!` | Change Terraform registry from private to public. |
| M | `moduleUpdate` | `id: ID!`, `input: ModuleUpdateInput!` | `Module!` | Update an existing Module (v1 API, does not support updating source). |
| M | `moduleUpdateV2` | `id: ID!`, `input: ModuleUpdateV2Input!` | `Module!` | Update an existing Module (v2 API). |
| M | `versionAnnotate` | `id: ID!`, `module: ID!`, `notes: String` | `Version!` | Set notes on the Version. |
| M | `versionCreate` | `module: ID!`, `commitSha: String`, `version: String` | `Version!` | Create Version based on optional SHA or branch head. |
| M | `versionDelete` | `id: ID!`, `module: ID!` | `Version` | Delete version. This action cannot be undone. |
| M | `versionProposeLocalWorkspace` | `module: ID!`, `workspace: ID!`, `testIds: [String!]` | `[Run!]!` | Create Version test run based on provided local workspace. |
| M | `versionPush` | `module: ID!`, `commitSha: String!`, `commitMessage: String!`, `version: String!`, `tarballURL: String!` | `Version!` | No description in the schema |
| M | `versionTrigger` | `id: ID!`, `module: ID!` | `Version!` | Trigger failed Version. |
| M | `versionYank` | `id: ID!`, `module: ID!`, `note: String` | `Version!` | Yank the version (mark it as bad). This action cannot be undone. |

RBAC actions that gate these operations, with their legacy role fallback [D] rbac. The
mapping of action to mutation is by name [I].

| Action | Legacy fallback | Gates |
|---|---|---|
| `MODULE_CREATE` | Admin | `moduleCreate` |
| `MODULE_DISABLE` | Admin | `moduleDisable` |
| `MODULE_ENABLE` | Admin | `moduleEnable` |
| `MODULE_PUBLISH` | Admin | `modulePublish` |
| `MODULE_TRIGGER_VERSION` | Writer | `versionTrigger` (and, by name, `versionCreate`) [I] |
| `MODULE_VERSION_DELETE` | Writer | `versionDelete` (the docs call it deleting a published version) |
| `MODULE_MARK_AS_BAD` | Writer | `versionYank` |
| `MODULE_UPLOAD_LOCAL_WORKSPACE` | Writer | `uploadLocalWorkspace` for a module |
| `MODULE_PROPOSE_LOCAL_WORKSPACE` | Writer | `versionProposeLocalWorkspace` |
| `SPACE_SHARE_MODULE` (space action "Share module") | Writer | `moduleShare` on the target space |

`MODULE_DISABLE`, `MODULE_ENABLE`, `MODULE_TRIGGER_VERSION` and `MODULE_MARK_AS_BAD` were
added 2025-12-10, `SPACE_SHARE_MODULE` 2026-01-09, and the two local-preview actions
2026-08-04 [RN]. Stack local-preview actions do not grant module local preview; a custom
role needs Space Read plus both module local-preview actions [D] module-registry.

## 4. `.spacelift/config.yml` for modules

Every module needs `.spacelift/config.yml`; the general file format and the stack keys are
in report 07. The module-only keys are [D] runtime-yaml; [D] module-registry:

| Key | Type | Required | Meaning |
|---|---|---|---|
| `version` | integer | no | File format version. The SaaS reference calls it optional and ignored; self-hosted v7.0.0 fails the module version with an explicit error when it is anything other than `1` or `2` [D] sh-changelog. |
| `module_version` | string | yes | The version to release. Plain `major.minor.patch` only; pre-release and build suffixes are rejected. Must change to release a new version. |
| `test_defaults` | map of runtime settings | no | Defaults applied to every test case; any key a test case sets overrides it. |
| `tests` | list | no | Test cases. Each accepts every runtime setting plus `name`, `negative`, `id`, `depends_on`. |

Runtime settings accepted by `test_defaults` and each test case are the `stack_defaults`
keys [D] runtime-yaml:

| Key | Type |
|---|---|
| `before_init`, `after_init`, `before_plan`, `after_plan`, `before_apply`, `after_apply`, `before_destroy`, `after_destroy`, `before_perform`, `after_perform`, `after_run` | list of strings |
| `environment` | map of string to string; `TF_VAR_` entries pass inputs to the module under test |
| `project_root` | string; a test case may point at a submodule or an example directory |
| `runner_image` | string |
| `terraform_version` | string |

Test-case-only keys [D] runtime-yaml:

| Key | Required | Type | Meaning |
|---|---|---|---|
| `name` | yes | string | Shown as the commit status name. Uniqueness is not checked [D] module-registry, although the YAML reference calls it unique. |
| `negative` | no | bool | The test case is expected to fail. Surfaces as `Run.expectFailure` [G]. |
| `id` | no | string | Unique identifier other test cases refer to. |
| `depends_on` | no | list of strings | `id`s that must run before this test case. |

Failure modes recorded in the docs: when the file cannot be read (missing, or named
`config.yaml`) the module falls back to version `0.1.0`, and a second attempt fails with
"version already exists" [D] module-registry. With no `tests`, a version turns green at
once [D] module-registry.

## 5. Test cases and test runs

### 5.1 Execution

| Behaviour | Evidence |
|---|---|
| A test case is a run of type `TESTING` on a module [G]; it behaves like an autodeployed tracked run that then destroys what it created. | [D] test-case |
| Phases: initialize, plan, apply, then destroy. The destroying phase runs whether apply succeeded or failed, because a partial apply may have created resources. | [D] test-case |
| `SPACELIFT_SKIP_DESTROYING=true` in the module environment skips the destroying phase. | [D] test-case |
| Success: the run reaches `FINISHED` only if every phase succeeds; any failed phase marks it `FAILED`. | [D] test-case |
| Negative test cases are expected to fail. How the outcome inverts for a negative case is not described beyond "expected to fail". | [D] module-registry; *(unverified)* |
| Spacelift checks that at least some resources were created, without checking which. | [D] module-registry |
| Test cases run in parallel, limited by the available workers, and in `depends_on` order when set. | [D] module-registry |
| Each test case gets its own commit status in GitHub or GitLab. | [D] module-registry |
| Test cases run on both proposed and tracked changes; only a tracked change creates a version. | [D] module-registry |
| Module test runs cannot be cancelled by a push policy `cancel` rule. | [D] push-policy |
| On self-hosted v7.0.0, proposed and module test runs left in `READY` with no worker are cancelled after 2 days (other runs after 7). Whether SaaS applies the same rule is *(unverified)*. | [D] sh-changelog |
| A version's metadata may be produced by a separate parsing run (`Version.metadataParsingRun`), populated only when parsing happens on a worker. | [G] |

### 5.2 Local preview

`spacectl module local-preview --id <module>` packages the working tree (respecting
`.gitignore` and `.terraformignore`), calls `uploadLocalWorkspace(stack: <module id>)`,
uploads the archive to the returned `uploadUrl` with `uploadHeaders`, then calls
`versionProposeLocalWorkspace(module, workspace, testIds)` and follows the returned runs
[S] [ctl-module local_preview.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/module/local_preview.go#L62).
`testIds` restricts the run to selected test cases (`--tests`) [S]. The module must have
`localPreviewEnabled` [D] module-registry.

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | No description in the schema |
| `uploadUrl` | `String!` | URL to upload the archived workspace to. |
| `uploadHeaders` | `StringMap` | HTTP headers that should be sent with the upload request. |

## 6. Versions

### 6.1 The `Version` object

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Unique ID of the Version. |
| `downloadLink` | `String` | Link to the source code to download the version. Will only work for ACTIVE versions. |
| `metadata` | `ModuleRepositoryMetadata` | String representation of the metadata generated by parsing the module source code. |
| `module` | `VersionModuleInfo!` | Module to which this version belongs. |
| `notes` | `String` | User-provided notes associated with the version. |
| `number` | `String!` | Number of the version conforming to the semantic versioning scheme (major, minor, patch). |
| `runs` | `[Run!]!` | List of runs |
| `commit` | `Commit!` | Commit which triggered this version. |
| `metadataParsingRun` | `Run` | Run parsing the module source code to generate the metadata. This will only be populated if we're parsing the module source on the worker. |
| `sourceURL` | `String!` | Link to the source code from which the version was built. |
| `createdAt` | `Int!` | CreatedAt is unix timestamp (in seconds) at which the version was created |
| `lastPulledAt` | `Int` | LastPulledAt is unix timestamp (in seconds) at which this version was most recently downloaded. Null if the version has never been pulled. For shared or public modules this timestamp reflects usage across all consumers, not just the requesting account. |
| `state` | `VersionState!` | Current state of the Version. |
| `triggeredBy` | `String` | The user who manually triggered this Version, if any. |
| `consumers` | `[Stack!]!` | Stacks in your account which use this module version. |
| `consumerCount` | `Int!` | Number of stacks, regardless of account, using this module version. |
| `versionCount` | `Int!` | Terraform versions tested for this version. |
| `yanked` | `Boolean!` | Indicates whether the version has been marked as "bad". |
| `yankedBy` | `String` | Login of the user who marked the version as "bad", if it has been marked |
| `yankNote` | `String` | Note associated with the version when it was marked as "bad", if it has |

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | The module's ID. |
| `name` | `String!` | Provider-independent name of the Terraform module. |
| `owner` | `String!` | The name of the module owner. |

### 6.2 Version state

| Value | Meaning [G] |
|---|---|
| `ACTIVE` | The version is active and can be used. |
| `PENDING` | The version is currently being tested. |
| `FAILED` | Testing the version failed. |

`Module.versions` returns only `ACTIVE` and `PENDING` versions unless `includeFailed` is
true; `Module.versionsCount` counts `ACTIVE` versions when no state is given [G].
`yanked` is a separate flag, not a state [G].

### 6.3 Transitions

| From | Event | To | Evidence |
|---|---|---|---|
| (none) | Tracked change on the tracked branch with a new `module_version`; or `versionCreate(module, commitSha?, version?)`; or a push policy `module_version` with `track` | `PENDING` | [D] module-registry; [G]; [D] push-policy |
| `PENDING` | All test cases succeed, or the module has no test cases | `ACTIVE` | [D] module-registry |
| `PENDING` | A test case fails | `FAILED` | [G] `FAILED` means testing failed |
| `FAILED` | `versionTrigger(id, module)` | Re-tested; the resulting state follows the new test runs *(unverified: whether it passes through `PENDING`)* | [G] "Trigger failed Version" |
| any | `versionYank(id, module, note)` | Same state, `yanked = true`, `yankedBy` and `yankNote` set; cannot be undone | [G]; [D] module-registry |
| any | `versionAnnotate(id, module, notes)` | Same state, `notes` set | [G] |
| any | `versionDelete(id, module)` | Deleted; cannot be undone. Refused while an Intent module deployment uses the version; `moduleDelete` is refused likewise for the module | [G]; [D] <https://docs.spacelift.io/concepts/intelligence/intent/spacelift-modules> |

The Terraform provider's `spacelift_version` resource polls the version every 5 seconds
and treats `ACTIVE` as success, `FAILED` as an error, and anything else as still running
[S] [tf-version L101](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_version.go#L101).

### 6.4 Numbering rules

| Rule | Evidence |
|---|---|
| Numbers are `major.minor.patch` only; no pre-release or build metadata. | [D] runtime-yaml |
| A number already used by a successful version cannot be reused. | [D] module-registry |
| Versions must not skip: going from `0.2.0` to `0.4.0` without `0.3.0` is rejected. | [D] module-registry |
| `versionCreate`'s `version` argument (spacectl `--version`) overrides `module_version` from the file; `commitSha` (`--sha`) selects the commit, defaulting to the branch head. | [G]; [S] [ctl-module flags.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/module/flags.go) |
| A push policy can set the number with `module_version`, for example from a Git tag with a `v` prefix stripped. When the module has a project root, the tagged commit must touch a file under it or the push policy is skipped. | [D] push-policy |
| Docs suggest importing history by looping over tags with `spacectl module create-version`. | [D] module-registry |

`versionPush(module, commitSha, commitMessage, version, tarballURL)` creates a version
from an archive URL; the schema gives it no description and no client in the sources
calls it *(unverified purpose)* [G].

### 6.5 Marking a version as bad

A version marked as bad (`versionYank`) can no longer be downloaded or used by new
consumers; the action is permanent and should carry a note [D] module-registry. Whether
existing consumers pinned to an exact yanked version still download it is
*(unverified)*. The protocol capture in section 9.3 suggests that versions which are not
usable leave the versions list and stop downloading [I].

### 6.6 Metadata

Parsing the module source produces `ModuleRepositoryMetadata`: the root module, its
submodules and its examples, each with inputs, outputs, module dependencies, provider
dependencies and resources [G]. The UI builds the module's documentation page from it
[D] module-registry.

| Field | Type | Meaning [G] |
|---|---|---|
| `root` | `ModuleMetadata!` | No description in the schema |
| `submodules` | `[ModuleMetadata!]!` | No description in the schema |
| `examples` | `[ModuleMetadata!]!` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `path` | `String!` | No description in the schema |
| `name` | `String!` | No description in the schema |
| `readme` | `String!` | No description in the schema |
| `changelog` | `String!` | No description in the schema |
| `empty` | `Boolean!` | No description in the schema |
| `inputs` | `[ModuleInput!]!` | No description in the schema |
| `outputs` | `[ModuleOutput!]!` | No description in the schema |
| `dependencies` | `[String!]!` | No description in the schema |
| `providerDependencies` | `[ModuleProviderDependency!]!` | No description in the schema |
| `resources` | `[ModuleResource!]!` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `name` | `String!` | No description in the schema |
| `type` | `String!` | No description in the schema |
| `description` | `String!` | No description in the schema |
| `default` | `String!` | No description in the schema |
| `required` | `Boolean!` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `name` | `String!` | No description in the schema |
| `description` | `String!` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `name` | `String!` | No description in the schema |
| `type` | `String!` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `name` | `String!` | No description in the schema |
| `namespace` | `String!` | No description in the schema |
| `source` | `String!` | No description in the schema |
| `version` | `String!` | No description in the schema |

### 6.7 Consumers

`Version.consumers` lists stacks in the caller's account using the version and
`Version.consumerCount` counts stacks in any account [G]. `Stack.moduleVersionsUsed`
lists the Spacelift-hosted module versions the stack's latest run used [G].
`lastPulledAt` on both `Module` and `Version` records the latest download, across all
consumers for shared and public modules [G]. A stack becomes a tracked consumer only
after one successful (initialized) run [D] trigger-policy.

## 7. Sharing and availability

### 7.1 Mechanisms

| Mechanism | How it is set | Who can use the module | Evidence |
|---|---|---|---|
| Space hierarchy | The module's own space | Stacks in that space and in child spaces that inherit | [D] module-registry |
| Space availability (recommended) | `moduleShare(id, {accounts, spaces})`; UI **Module settings > Availability**; Terraform `space_shares` | Stacks in each target space and its inheriting children | [D] module-registry; [G]; added 2025-10-17 [RN] |
| Cross-account sharing | `moduleShare` `accounts`, or `sharedAccounts` in the update inputs; Terraform `shared_accounts` | Other Spacelift accounts, by subdomain; kept for compatibility and may be deprecated | [D] module-registry |
| Public | `modulePublish(id)`; Terraform `public` at creation | Anyone, including outside Spacelift | [D] module-registry; [G] |

Rules:
- Making a module available to a space needs Admin on the module's space and Write or
  `SPACE_SHARE_MODULE` on the target space; Read on the target space makes it visible in
  the space picker [D] module-registry.
- Consumers see a shared module in the list and search, greyed out, and can only consume
  it: no details, settings, versions or availability changes [D] module-registry. The
  module list has an availability filter by space [D].
- `searchModules` and `searchModulesSuggestions` take `returnModuleSharing` [G].
- A public module cannot be deleted or made private again [G]. Public modules answer the
  registry protocol without credentials [C] (section 9.3).
- `moduleShare` takes the complete lists of accounts and spaces and returns the resulting
  `ModuleShare` set. The Terraform provider always sends both full sets [S]
  [tf-module L499](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_module.go#L499),
  from which replace semantics are inferred [I]. The provider calls it only when
  `space_shares` changes; `shared_accounts` alone travels in `moduleUpdateV2` [S].
- `Module.sharedAccounts` is deprecated in favour of `moduleShares` (2026-07-16) and the
  `sharedModules` query in favour of `sharedModulesV2` (2026-08-21) [RN] [G].
- `ModuleShareSpace` exposes only `id`, `name` and `parentSpace` of the source space so
  that target-space users learn nothing else about it [G].

### 7.2 Sharing types

| Field | Type | Meaning [G] |
|---|---|---|
| `accounts` | `[ID!]!` | No description in the schema |
| `spaces` | `[ID!]!` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Unique identifier of the module share. |
| `from` | `ModuleShareScope!` | Source of the module share (where it's shared from). |
| `to` | `ModuleShareScope!` | Target of the module share (where it's shared to). |
| `module` | `ModuleShareModule!` | Module information for the shared item. |

| Field | Type | Meaning [G] |
|---|---|---|
| `account` | `ModuleShareAccount!` | Account information, if the sharing scope is an account. |
| `space` | `ModuleShareSpace` | Space information, if the sharing scope is a space. |

| Field | Type | Meaning [G] |
|---|---|---|
| `subdomain` | `String!` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | No description in the schema |
| `name` | `String!` | No description in the schema |
| `parentSpace` | `ID` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `String!` | No description in the schema |
| `name` | `String!` | No description in the schema |
| `description` | `String` | No description in the schema |
| `labels` | `[String!]!` | No description in the schema |
| `current` | `SharedVersion` | No description in the schema |
| `latest` | `SharedVersion` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Unique (within a single organization), immutable name of the Module. |
| `current` | `SharedVersion` | Current module version. |
| `description` | `String` | Free-form module description for the users, supports Markdown. |
| `labels` | `[String!]!` | Labels applied to a Module. |
| `latest` | `SharedVersion` | Latest module version. |
| `name` | `String!` | Provider-independent name of the Terraform module. |
| `ownerSubdomain` | `String!` | Subdomain of the account owner, if the module is owned by another account and just shared with the current one, this will be used to point the user to the "canonical" location of the module. |
| `terraformProvider` | `String!` | TerraformProvider represents Module's Terraform provider |
| `version(id: ID!)` | `SharedVersion` | Retrieve a single Version by its ID. |
| `versions(before: ID)` | `[SharedVersion!]!` | Paginate through all Versions of the current Module. By default, only ACTIVE and PENDING versions are shown, but setting includeFailed to true will show all versions. |

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Unique ID of the Version. |
| `metadata` | `ModuleRepositoryMetadata` | String representation of the metadata generated by parsing the module source code. |
| `notes` | `String` | User-provided notes associated with the version. |
| `number` | `String!` | Number of the version conforming to the semantic versioning scheme (major, minor, patch). |
| `state` | `VersionState!` | Current state of the Version. |
| `yanked` | `Boolean!` | Indicates whether the version has been marked as "bad". |
| `yankedBy` | `String` | Login of the user who marked the version as "bad", if it has been marked |
| `yankNote` | `String` | Note associated with the version when it was marked as "bad", if it has |

## 8. Policies on modules

| Policy | Module behaviour | Evidence |
|---|---|---|
| Push | Receives `module` instead of `stack` (with `current_version`, `0.0.0` before the first version); `track`, `propose` and `module_version` decide test runs and the release number. | [D] push-policy; report 09 |
| Trigger | Runs when a new version is released. Input `module` carries the new `version` (`commit`, `created_at`, `id`, `latest`, `number`, `state`, `test_runs[]`), and `stacks` lists the consumers of the previously newest version, assumed to be potential consumers of the new one. Triggering them upgrades the module where the stack's version constraint allows. | [D] trigger-policy |
| Notification | A `module_version` event exists in notification policy input. | report 09 |
| Approval and plan | Attachable to modules like stacks. | [D] module-registry |

## 9. Registry protocol

Spacelift serves the Terraform remote service discovery and the module and provider
registry protocols. The docs show how to consume them but do not document the endpoints;
the rows marked [C] come from live captures, the response shapes marked [I] follow
HashiCorp's protocol specifications
(<https://developer.hashicorp.com/terraform/internals/remote-service-discovery>,
<https://developer.hashicorp.com/terraform/internals/module-registry-protocol>,
<https://developer.hashicorp.com/terraform/internals/provider-registry-protocol>), which
Terraform requires any registry to implement.

### 9.1 Host names

| Host | Role | Evidence |
|---|---|---|
| `spacelift.io` | The registry host written in `source` addresses and in `terraform login spacelift.io`; serves `/.well-known/terraform.json` as a static object (S3 behind CloudFront) | [D] module-registry; [C] |
| `app.spacelift.io` | Serves discovery dynamically and hosts the protocol APIs, the login page and the token endpoint; also the registry URL for Dependabot and Renovate | [C]; [D] module-registry |
| `app.us.spacelift.io` | The same for US-region accounts | [C] |
| `registry.spacelift.io` | CloudFront distribution serving module archives through signed URLs; workers must reach it | [C]; [D] worker-pools |
| `registry.us.spacelift.io` | US-region equivalent workers must reach | [D] worker-pools |
| `<account>.app.spacelift.io` | Account API host, for example the `SPACELIFT_API_KEY_ENDPOINT` used to publish providers | [D] provider-registry |
| Self-hosted | The provider page uses `your-hostname/your-org/yourprovider`; the self-hosted module page still shows `spacelift.io` | [D] sh-provider-registry; [D] sh-module-registry |

Which host a US-region account writes in `source` addresses is *(unverified)*:
`spacelift.io` discovery points to the EU `app.spacelift.io` endpoints [C].

### 9.2 Service discovery

`GET https://spacelift.io/.well-known/terraform.json` returns [C]:

| Key | Value on `spacelift.io` | Value on `app.spacelift.io` | Value on `app.us.spacelift.io` |
|---|---|---|---|
| `login.v1.authz` | `https://spacelift.io/terraform-login` | `/terraform-login` | `/terraform-login` |
| `login.v1.client` | `terraform-cli` | `terraform-cli` | `terraform-cli` |
| `login.v1.grant_types` | `["authz_code"]` | `["authz_code"]` | `["authz_code"]` |
| `login.v1.token` | `https://app.spacelift.io/terraform/token` | `https://app.spacelift.io/terraform/token` | `https://app.us.spacelift.io/terraform/token` |
| `modules.v1` | `https://app.spacelift.io/registry/modules/v1/` | same | `https://app.us.spacelift.io/registry/modules/v1/` |
| `providers.v1` | `https://app.spacelift.io/registry/providers/v1/` | same | `https://app.us.spacelift.io/registry/providers/v1/` |
| `state.v2`, `tfe.v2`, `tfe.v2.1`, `tfe.v2.2` | `https://app.spacelift.io/state.v2` | same | `https://app.us.spacelift.io/state.v2` |

The `state.v2` and `tfe.v2*` services belong to state access (report 16). The
`spacelift.io` object is served with `Last-Modified: Mon, 09 Sep 2024` and the
`app.*` responses with `Cache-Control: no-cache, no-store` [C].

### 9.3 Module endpoints

Paths are relative to the `modules.v1` base; `namespace` is the account name.

| Method | Path | Observed behaviour | Evidence |
|---|---|---|---|
| GET | `{namespace}/{name}/{provider}/versions` | `200` (`application/json`) with `{"modules":[{"source":"<ns>/<name>/<provider>","versions":[{"version":"x.y.z","root":{"path":"","providers":[{"name","namespace","source","version"}],"dependencies":[]},"submodules":[]}]}]}`. Each version carries root and submodule provider dependencies beyond the protocol's minimum `version`. | [C] |
| GET | same, unknown module | `200` with an empty `versions` array. The protocol specification calls for `404` here; Terraform treats both as "no versions" [I] | [C] |
| GET | `{namespace}/{name}/{provider}/{version}/download` | `204 No Content` with `X-Terraform-Get: https://registry.spacelift.io/<ULID>.tar.gz?Expires=…&Signature=…&Key-Pair-Id=…`, a CloudFront signed URL valid for about ten minutes (`Expires` was 588 seconds after the request) | [C] |
| GET | same, version written `9.1` or `v9.1.0` | `204` with the same archive as `9.1.0`: the version segment is parsed leniently | [C] |
| GET | same, version not in the list (unknown, or a number missing from the list) | `404` with the plain-text body `404 page not found` | [C] |
| GET | same, version that is not a version (`abc`) | `400` with the plain-text body `invalid module version: "abc"` | [C] |
| GET | `{namespace}/{name}/{provider}/download` (latest) | `404` with `{"errors":[{"message":"not found"}]}`, served as `text/plain` | [C] |
| GET | `{namespace}/{name}/{provider}`, `{namespace}`, the base, `search` | `404` with the same JSON body; no listing or search endpoints | [C] |

The archive is a gzipped tarball whose entries sit at the module root, with no wrapping
directory [C]. Private modules require a bearer token; an empty module or version list is
the documented symptom of missing space access or a malformed token [D] module-registry.

The captured list for `spacelift-io/spacelift-workerpool-on-ec2/aws` holds 89 versions with
gaps (`2.11.0`, `3.0.2`, `4.4.0`, `5.6.0` and `6.0.2` are absent), and the download
endpoint answers `404` for each gap [C]. Because Spacelift rejects skipped numbers [D]
module-registry, those numbers must exist as versions in some other state, so the
protocol list and download both leave out versions that are not usable, most likely
`FAILED` or yanked ones [I]. Which of the two causes each gap is *(unverified)*.

### 9.4 Provider endpoints

Paths are relative to the `providers.v1` base; `namespace` is the account name and `type`
the provider type.

| Method | Path | Observed behaviour | Response per the protocol [I] |
|---|---|---|---|
| GET | `{namespace}/{type}/versions` | `401` with body `unauthorized` (`text/plain`) without credentials, even for an unknown provider [C] | `versions[]` of `version`, `protocols` (from `protocolVersions`) and `platforms[]` of `os` and `arch` |
| GET | `{namespace}/{type}/{version}/download/{os}/{arch}` | `401` without credentials [C] | `protocols`, `os`, `arch`, `filename` (`terraform-provider-<type>_<version>_<os>_<arch>.zip`), `download_url`, `shasums_url`, `shasums_signature_url`, `shasum` (the registered `archiveChecksum`), and `signing_keys.gpg_public_keys[]` (`key_id`, `ascii_armor`) from the version's GPG key |

Only `PUBLISHED` versions are usable, and a version constraint is resolved against them
[D] provider-registry; that drafts and revoked versions are left out of `versions` is
inferred [I]. Whether a public provider answers without credentials is *(unverified)*.

### 9.5 Authentication

| Client | Mechanism | Evidence |
|---|---|---|
| Runs inside Spacelift | Automatically authenticated to the registry while the run is in progress. During the preparing phase Spacelift writes `~/.terraformrc` with the credentials; mounted files whose names end in `.terraformrc` are appended. OpenTofu reads the same file when no `.tofurc` exists, so a mounted `.tofurc` would hide the credentials. | [D] provider-registry; [D] cli-configuration |
| Interactive | `terraform login spacelift.io`: browser login, credentials stored in `~/.terraform.d/credentials.tfrc.json`. Since 2026-10-12 this session is independent of the web and spacectl sessions. | [D] module-registry; [RN] |
| Automation | A Spacelift API key: `credentials "spacelift.io" { token = "<token>" }` in `.terraformrc`, where the token is base64 of `api:<API_KEY_ID>:<API_KEY_SECRET>` without padding; the API key's credentials file contains a ready-made block. | [D] module-registry |
| Dependabot | `terraform-registry` registry with `url: https://app.spacelift.io` and the token above | [D] module-registry |
| Renovate | `hostRules` with `matchHost: app.spacelift.io`, `hostType: terraform-module`, and `registryUrls: ["https://app.spacelift.io"]` | [D] module-registry |

Authorization: an API key needs at least Read on the module's or provider's space; with
login policies, non-admin keys must be allowed and granted `space_read` by the policy
[D] module-registry; [D] provider-registry.

## 10. The provider registry

### 10.1 Providers

| Rule | Evidence |
|---|---|
| A provider is identified by its `type`, unique in the account and used as its ID; lowercase letters only. | [D] provider-registry; [G] `terraformProviderCreate` |
| The Terraform provider forces replacement when `type` changes. | [S] [tf-provider L21](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_terraform_provider.go#L21) |
| A provider lives in a space; runs of stacks in that space or inheriting children can read it. | [D] provider-registry |
| `public: true` makes it usable by anyone; this is the only way to share a provider across accounts. Set with `terraformProviderSetVisibility`. | [D] provider-registry; [G] |
| Address: `spacelift.io/<account>/<type>` in `required_providers`; without a version constraint the latest published version is used. | [D] provider-registry |
| Created through the Terraform provider (preferred), the API (`terraformProviderCreate`), or the UI. | [D] provider-registry |

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Immutable ID of the provider, unique within the scope of a single account. |
| `createdAt` | `Int!` | Creation timestamp of the provider. |
| `deleted` | `Boolean!` | Whether the provider has just been deleted. |
| `description` | `String` | Human-readable description of the provider, if set by the user. Supports Markdown. |
| `labels` | `[String!]!` | Set of labels attached to the provider. |
| `latestVersionNumber` | `String` | The semver number of the last version of the provider that was published. |
| `public` | `Boolean!` | Whether the provider can be consumed outside of this account. |
| `space` | `ID!` | ID of the space this provider belongs to. |
| `spaceDetails` | `Space!` | Details of the space this provider belongs to. |
| `updatedAt` | `Int!` | Last update timestamp of the provider. |
| `version(number: String!)` | `TerraformProviderVersion` | Get a particular version by its semver number. |
| `versions` | `[TerraformProviderVersion!]!` | List of versions of the provider. |
| `allowedActions` | `[Action!]!` | Actions that the user can perform on this TerraformProvider. |

### 10.2 Provider versions

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Immutable, globally unique ID of the provider version. |
| `artifacts` | `[TerraformProviderVersionArtifact!]!` | List of artifacts associated with this provider version. |
| `artifactDownloadUrl(filename: String!)` | `String!` | Temporary presigned URL for downloading the artifact. |
| `createdAt` | `Int!` | Creation timestamp of the provider version. |
| `deleted` | `Boolean!` | Indicates whether the version is deleted. |
| `description` | `String` | Free-form, human-readable description of the provider version. Supports Markdown. Can be used for info like release notes, changelog, warnings or deprecations. |
| `number` | `String!` | Semantically versioned number. |
| `platforms` | `[TerraformProviderPlatform!]!` | List of platforms for which the provider was built. |
| `protocolVersions` | `[String!]!` | List of Terraform plugin protocol versions supported by the provider binaries. |
| `signingKeyId` | `ID!` | ID of the GPG key used to sign provider binaries. |
| `status` | `TerraformProviderVersionStatus!` | Current status of the Terraform provider version. |
| `updatedAt` | `Int!` | Last update timestamp of the provider version. |
| `verifiedAt` | `Int` | Timestamp at which the artifacts were verified (GPG signature + checksums). Null means the version has not been verified yet. |
| `verificationFailure` | `String` | Failure message from the last failed verification attempt. Null means no verification has been attempted yet, or the last attempt succeeded (if verifiedAt is set). |
| `docs` | `TerraformProviderDocs!` | Documentation for this provider version. |
| `allowedActions` | `[Action!]!` | Actions that the user can perform on this TerraformProviderVersion. |

| Value | Meaning [G] |
|---|---|
| `DRAFT` | The version currently being built. |
| `PUBLISHED` | The version is published, and ready to be used. |
| `REVOKED` | The version has been revoked and can no longer be used. |

| From | Event | To | Evidence |
|---|---|---|---|
| (none) | `terraformProviderVersionCreate(provider, input)` | `DRAFT` | [D] provider-registry; [G] |
| `DRAFT` | `terraformProviderVersionPublish(version)` | `PUBLISHED` | [D]; [G] |
| `DRAFT` | `terraformProviderVersionDelete(version)` | Deleted; the number can be reused | [D] provider-registry |
| `PUBLISHED` | `terraformProviderVersionRevoke(version)` | `REVOKED`; stays listed, never usable again, number never reusable | [D] provider-registry |
| `PUBLISHED` | delete | Rejected: published versions cannot be deleted | [D] provider-registry |
| any | `terraformProviderVersionVerify(version)` | Same status; sets `verifiedAt` or `verificationFailure` | [G] |
| any | `terraformProviderVersionUpdate(version, description)` | Same status; sets the description (release notes) | [G] |
| any | `terraformProviderVersionUploadDocs(version, archive)` | Same status; replaces `docs` | [G] |

Whether a `DRAFT` can be revoked or a `REVOKED` version deleted is *(unverified)*.

| Field | Type | Meaning [G] |
|---|---|---|
| `number` | `String!` | Semantically versioned number, unique for this provider. |
| `protocolVersions` | `[String!]!` | List of Terraform plugin protocol versions supported by the provider binaries. These must correspond to whatever is supported by Terraform, and Spacelift does not validate these; Terraform will. You can learn more about Terraform plugin protocol versions at https://developer.hashicorp.com/terraform/plugin/how-terraform-works#terraform-plugin-protocol |
| `signingKeyId` | `ID!` | ID of the GPG key used to sign the provider binaries. This key must be registered with the Spacelift account, and may not be revoked. |
| `shaSumsFileSHA` | `String!` | Hex-encoded SHA 256 checksum of the checksums file. |
| `signatureFileSHA` | `String!` | Hex-encoded SHA 256 checksum of the checksums signature file. |

| Field | Type | Meaning [G] |
|---|---|---|
| `version` | `TerraformProviderVersion` | Newly created version. |
| `sha256SumsUploadURL` | `String!` | Upload URL for the SHA256SUMS file. Only populated when creating the version. |
| `sha256SumsUploadHeaders` | `StringMap` | Upload Headersfor the SHA256SUMS file that should be send with the presign request. Only populated when creating the version. |
| `sha256SumsSigUploadURL` | `String!` | Upload URL for the SHA256SUMS.sig file. Only populated when creating the version. |
| `sha256SumsSigUploadHeaders` | `StringMap` | Upload Headers for the SHA256SUMS.sig file, that should be send with the presign request. Only populated when creating the version. |

| Field | Type | Meaning [G] |
|---|---|---|
| `archiveChecksum` | `String!` | Hex-encoded SHA 256 checksum of the zip archive. |
| `binaryChecksum` | `String!` | Hex-encoded SHA 256 checksum of the archived binary. |
| `architecture` | `String!` | CPU architecture for which the provider binary is built. Note that Spacelift does not validate the correctness of the reported architecture; Terraform will do it. |
| `os` | `String!` | Operating system for which the provider binary is built. Note that Spacelift does not validate the correctness of the reported operating system; Terraform will do it. |

| Field | Type | Meaning [G] |
|---|---|---|
| `uploadUrl` | `String!` | No description in the schema |
| `uploadHeaders` | `StringMap` | No description in the schema |

| Field | Type | Meaning [G] |
|---|---|---|
| `architecture` | `String!` | Architecture of the platform. |
| `os` | `String!` | Operating system of the platform. |

| Field | Type | Meaning [G] |
|---|---|---|
| `filename` | `String!` | Name of the artifact. |
| `sizeBytes` | `Int!` | Artifact size in bytes. |

`terraformProviderVersionRegisterPlatform` is idempotent per platform and returns a
presigned upload URL; once a platform is registered its binary must be uploaded [G].
Spacelift does not validate the OS, architecture or protocol versions; Terraform does [G].

### 10.3 Publishing flow

`spacectl provider create-version --type <type>` is designed for CI and runs these steps
[S] [ctl-provider create_version.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/provider/create_version.go#L22):

| Step | What happens |
|---|---|
| 1 | Read GoReleaser output from `--goreleaser-dir` (default `dist`): `artifacts.json`, `metadata.json` and an optional `CHANGELOG.md` [S] [goreleaser.go L33](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/provider/internal/goreleaser.go#L33). |
| 2 | Find the `Checksum` and `Signature` artifacts and hash both files (SHA-256, hex). |
| 3 | Extract the signer's long key ID (16 hex characters, upper-cased) from the signature, unless `--gpg-key-id` overrides it [S] [L317](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/provider/create_version.go#L317). |
| 4 | `terraformProviderVersionCreate` with `number` from the GoReleaser metadata, `protocolVersions` from `--protocols` (default `5.0`), both file hashes and `signingKeyId`. |
| 5 | Upload `SHA256SUMS` and `SHA256SUMS.sig` to the returned presigned URLs with the returned headers. Before spacectl's 3.0.0 server support, headers were `x-amz-meta-*` metadata built client-side instead. |
| 6 | For each `Archive` artifact, require the name `terraform-provider-<type>_<version>_<os>_<arch>.zip` [S] [goreleaser.go L203](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/provider/internal/goreleaser.go#L203), then `terraformProviderVersionRegisterPlatformV2` with the archive and binary SHA-256 and upload the zip. |
| 7 | On current servers, `terraformProviderVersionVerify`; the result prints as valid or failed but does not stop the command. |
| 8 | If `CHANGELOG.md` exists, `terraformProviderVersionUpdate` sets it as the description. |
| 9 | Print the draft version ID (`--quiet` prints only the ID). Publishing is a separate `publish-version` step. |

The documented GoReleaser configuration builds `darwin` and `linux` for `amd64` and
`arm64`, zips each as `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`, writes
`{{ .ProjectName }}_{{ .Version }}_SHA256SUMS`, and detach-signs the checksums file with
the key in `GPG_FINGERPRINT` [D] provider-registry. Tags must be semantic versions without
build metadata [D]. The API key used must be admin of the provider's space (root admin for
`root`) [D].

### 10.4 GPG keys

| Rule | Evidence |
|---|---|
| Only root admins manage GPG keys; keys belong to the account. | [D] provider-registry |
| A key must be registered before a version signed with it can be created; `signingKeyId` must name a registered, unrevoked key. | [D]; [G] `TerraformProviderVersionInput` |
| Registration sends the ASCII-armored public key (`gpgKeyCreate(name, asciiArmor, description)`). | [G]; [D] |
| The Terraform provider deliberately has no GPG key resource, so private keys never land in state; spacectl registers keys instead. | [D] provider-registry |
| `spacectl provider add-gpg-key --import --path <private key>` derives and sends the public key; `--generate --email` creates an RSA 4096 key without a passphrase, writes the private key (default `gpg_key.asc`, mode 0600) and sends the public key. | [S] [add_gpg_key.go L66](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/provider/add_gpg_key.go#L66); [D] |
| Revoking a key blocks new versions signed with it and leaves published versions untouched; revoking those is a separate step. | [D] provider-registry |
| `gpgKeyUpdate` changes only the description. | [G] |
| The key's `id` is the value passed as `signingKeyId`, which spacectl takes from the signature's key ID. | [I] from spacectl step 3 |

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Globally unique key identifier. |
| `createdAt` | `Int!` | Timestamp of when the key was created. |
| `createdBy` | `String!` | Login of the user who created the key. |
| `description` | `String` | Optional, human-readable description of the GPG key. |
| `name` | `String!` | Name of the key. |
| `revokedAt` | `Int` | Timestamp of when the key was revoked, if it has been revoked. |
| `revokedBy` | `String` | Login of the user who revoked the key, if it has been revoked. |
| `updatedAt` | `Int!` | Timestamp of when the key was last updated. |

### 10.5 Verification

`terraformProviderVersionVerify` checks the GPG signature of the `SHA256SUMS` file,
confirms the file lists every registered platform archive, and checks their checksums
[G]. Verification does not affect availability: a failed verification blocks neither
publishing nor use, although Terraform may later reject the provider [D]
provider-registry. A non-empty `verificationFailure` means failure; an empty one with
`verifiedAt` set means success; both empty means never verified [D]; [G]. The UI shows a
badge and a **Verify version** action [D].

### 10.6 Provider documentation

`terraformProviderVersionUploadDocs(version, archive)` takes a base64-encoded gzipped tar
archive (`spacectl provider upload-docs --docs-archive`) [S]
[upload_docs.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/provider/upload_docs.go).
How files in the archive are classified into the doc types is *(unverified)*.

| Field | Type | Meaning [G] |
|---|---|---|
| `files` | `[TerraformProviderDoc!]!` | All documentation files with metadata |
| `content(key: String!)` | `String!` | Get content for a specific documentation file by key |
| `overviewContent` | `String!` | Convenience field that returns the content of the overview documentation file. Returns empty string if no overview doc exists or if there's an error fetching it. This allows the frontend to always display other docs even if overview is unavailable. |

| Field | Type | Meaning [G] |
|---|---|---|
| `key` | `String!` | Key (full path) of the documentation file |
| `filename` | `String!` | Filename of the documentation file |
| `type` | `TerraformProviderDocType!` | Type of documentation (guide, resource, data source) |

| Value | Meaning [G] |
|---|---|
| `UNKNOWN` | Unknown/unclassified documentation |
| `OVERVIEW` | Provider overview documentation |
| `RESOURCE` | Resource documentation |
| `DATA_SOURCE` | Data source documentation |

### 10.7 Provider operations

| Kind | Operation | Arguments | Returns | Meaning [G] |
|---|---|---|---|---|
| Q | `gpgKeys` | none | `[GPGKey!]!` | List the GPG keys used to sign binaries. |
| Q | `terraformProvider` | `id: ID!` | `TerraformProvider` | Details of a single Terraform provider. |
| Q | `terraformProviders` | none | `[TerraformProvider!]!` | List of all Terraform providers managed by this account. |
| M | `gpgKeyCreate` | `name: String!`, `asciiArmor: String!`, `description: String` | `GPGKey!` | Creates a new GPG key to be used for signing binaries. |
| M | `gpgKeyRevoke` | `id: ID!` | `GPGKey!` | Revokes a GPG key to be used for signing binaries. |
| M | `gpgKeyUpdate` | `id: ID!`, `description: String` | `GPGKey!` | Updates the description on the GPG key. |
| M | `terraformProviderCreate` | `type: ID!`, `space: ID!`, `description: String`, `labels: [String!]` | `TerraformProvider!` | Creates a new Terraform provider. Note that the type needs to be unique within the account, and it can then be used as the provider's ID. |
| M | `terraformProviderDelete` | `id: ID!` | `TerraformProvider` | Deletes a Terraform Provider |
| M | `terraformProviderSetVisibility` | `id: ID!`, `public: Boolean!` | `TerraformProvider!` | Changes public visibility of the Terraform Provider |
| M | `terraformProviderUpdate` | `id: ID!`, `space: ID!`, `description: String`, `labels: [String!]` | `TerraformProvider!` | Updates an existing Terraform Provider |
| M | `terraformProviderVersionCreate` | `provider: ID!`, `input: TerraformProviderVersionInput!` | `NewTerraformProviderVersion!` | Create a new Terraform provider version. |
| M | `terraformProviderVersionDelete` | `version: ID!` | `TerraformProviderVersion!` | Delete a draft Terraform provider version. |
| M | `terraformProviderVersionPublish` | `version: ID!` | `TerraformProviderVersion!` | Publish an existing Terraform provider version. |
| M | `terraformProviderVersionVerify` | `version: ID!` | `TerraformProviderVersion!` | Verify the GPG signature of the SHA256SUMS file, confirm the file contains entries for all registered platform archives and verify their checksums. |
| M | `terraformProviderVersionRegisterPlatform` | `version: ID!`, `input: TerraformProviderVersionPlatformInput!` | `String!` | Register a platform supported by the version. The result is presigned upload URL that can be used to upload the platform binary. This call is idempotent so registering the same platform multiple times will not create multiple copies, but once a platform is registered, the binary must be uploaded. |
| M | `terraformProviderVersionRegisterPlatformV2` | `version: ID!`, `input: TerraformProviderVersionPlatformInput!` | `TerraformProviderVersionRegisterPlatformV2Output!` | This mutation is similar to terraformProviderVersionRegisterPlatform, but it also returns the optional uploadHeaders required for the upload request. |
| M | `terraformProviderVersionRevoke` | `version: ID!` | `TerraformProviderVersion!` | Revoke a published Terraform provider version. |
| M | `terraformProviderVersionUpdate` | `version: ID!`, `description: String!` | `TerraformProviderVersion!` | Update an existing Terraform provider version. |
| M | `terraformProviderVersionUploadDocs` | `version: ID!`, `archive: String!` | `TerraformProviderVersion!` | Upload documentation for a Terraform provider version. |

RBAC actions and legacy fallbacks [D] rbac: `TERRAFORM_PROVIDER_CREATE`,
`TERRAFORM_PROVIDER_DELETE`, `TERRAFORM_PROVIDER_SET_VISIBILITY` and
`TERRAFORM_PROVIDER_UPDATE` fall back to Admin; `TERRAFORM_PROVIDER_VERSION_CREATE`,
`_DELETE`, `_PUBLISH`, `_REGISTER_PLATFORM`, `_REVOKE` and `_UPDATE` fall back to Writer
[G] `Action`. No action names verification or documentation upload *(unverified which
action gates them)*.

## 11. External modules

Modules from sources other than the Spacelift registry need credentials supplied through
ordinary stack configuration; Spacelift adds no registry-specific mechanism beyond the
`.terraformrc` merge [D] external-modules. The page applies to OpenTofu stacks too [D].

| Source | Mechanism | Evidence |
|---|---|---|
| S3 and GCS buckets | AWS or GCP cloud integrations, or the worker's own cloud identity on private workers. The self-hosted page names S3 only. | [D] external-modules; [D] <https://docs.spacelift.io/self-hosted/latest/vendors/terraform/external-modules> |
| Git over HTTPS | Mount a netrc file (for example `github.netrc`) and append it to `~/.netrc` with `chmod 600` in a `before_init` hook | [D] external-modules |
| Git over SSH | Mount the private key as exactly `id_ed25519` (other names fail with `Permission denied`), move it to `~/.ssh` with mode 400, and add the host key with `ssh-keyscan` in `before_init`; a trailing newline fixes libcrypto errors | [D] external-modules |
| Third-party registries (for example Terraform Cloud) | `credentials` blocks in a mounted file ending in `.terraformrc`, merged into the generated `~/.terraformrc` | [D] external-modules; [D] cli-configuration |
| Credentials without mounting | Bake them into a private runner image, or fetch them from a secret store in `before_init` | [D] external-modules |

Mounted credentials are best kept in contexts attached where needed [D] external-modules.
Terragrunt consumes the Spacelift registry with `tfr://spacelift.io/<org>/<module>/<provider>?version=<v>`
[D] <https://docs.spacelift.io/vendors/terragrunt/reference>.

## 12. Clients

### 12.1 Terraform provider

| Resource or data source | GraphQL operations | Notes |
|---|---|---|
| `spacelift_module` (resource) | `module`, `moduleCreate`, `moduleUpdateV2`, `moduleDelete`, `modulePublish`, `moduleShare` | Attributes in section 2.2; `public` and `name` force replacement [S] |
| `spacelift_version` (resource) | `module`, `versionCreate` | `module_id` (required), `commit_sha`, `version_number`, `keepers` (map; a change re-creates the version), computed `id`; every argument forces replacement [S] [tf-version](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_version.go) |
| `spacelift_terraform_provider` (resource) | `terraformProvider`, `terraformProviderCreate`, `terraformProviderUpdate`, `terraformProviderSetVisibility`, `terraformProviderDelete` | `type` and `space_id` required, `description`, `labels`, `public` (default `false`) [S] [tf-provider](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_terraform_provider.go) |
| `spacelift_module` (data source) | `module` | Reads one module by `module_id` |
| `spacelift_modules` (data source) | `searchModules` | Predicate blocks `administrative` (`equals`), and `branch`, `commit`, `labels`, `name`, `project_root`, `repository`, `worker_pool` (`any_of`) |

The operation map is from
[`terraform-operations.json`](../../../api/reference/spacelift/terraform-operations.json)
and the attributes from
[`terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json).

### 12.2 spacectl

| Command | Flags | Calls |
|---|---|---|
| `module create-version` | `--id`, `--sha`, `--version` | `versionCreate` |
| `module delete-version` | `--id`, `--versionid` | `versionDelete` |
| `module list` | `--output`, `--limit`, `--search` | `searchModules` |
| `module list-versions` | `--id`, `--output` | `module.searchModuleVersions`, ordered by `createdAt` descending, excluding `state = FAILED`, at most 50 per page [S] [search_version.go L116](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/module/search_version.go#L116) |
| `module local-preview` | `--id`, `--no-find-repository-root`, `--no-upload`, `--run-metadata`, `--disregard-gitignore`, `--with-git-dir`, `--tests`, `--no-animation` | `uploadLocalWorkspace`, `versionProposeLocalWorkspace` |
| `module logs` | `--id`, `--run`, `--phase`, `--tail` | run logs |
| `provider add-gpg-key` | `--import` or `--generate`, `--name`, `--email`, `--path` | `gpgKeyCreate` |
| `provider list-gpg-keys` | none | `gpgKeys` |
| `provider revoke-gpg-key` | `--key` | `gpgKeyRevoke` |
| `provider create-version` | `--type`, `--protocols`, `--goreleaser-dir`, `--gpg-key-id`, `--quiet` | section 10.3 |
| `provider list-versions` | `--type` | `terraformProvider.versions` |
| `provider publish-version` | `--version` (the version ID) | `terraformProviderVersionPublish` |
| `provider delete-version` | `--version` | `terraformProviderVersionDelete` |
| `provider revoke-version` | `--version` | `terraformProviderVersionRevoke` |
| `provider verify-version` | `--version` | `terraformProviderVersionVerify` |
| `provider upload-docs` | `--version`, `--docs-archive` | `terraformProviderVersionUploadDocs` |

Sources: [S] [ctl-module module.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/module/module.go),
[S] [ctl-provider provider.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/provider/provider.go).

## 13. Self-hosted differences

| Topic | Difference | Evidence |
|---|---|---|
| Tier notes | Absent | [D] sh-module-registry |
| Provider address | Your own hostname replaces `spacelift.io` | [D] sh-provider-registry |
| Module address | The self-hosted page still shows `spacelift.io`; the installation's hostname is inferred to apply as for providers [I] | [D] sh-module-registry |
| Storage | One "Modules" bucket holds module sources and provider artifacts | [D] sh-object-storage |
| `config.yml` `version` | v7.0.0 rejects values other than `1` or `2` with an explicit error | [D] sh-changelog |
| Worker pool | The Terraform provider calls `worker_pool_id` required on self-hosted | [S] [tf-module](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_module.go) |
| Upload headers | spacectl uses server-provided upload headers from self-hosted v3.0.0, to support storage other than S3 | [S] [ctl-provider provider.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/provider/provider.go) |

## What this means for infraweave

- A module is a stack variant with no state: infraweave needs a module resource sharing
  the stack's source, space, worker pool, attachment and environment machinery (reports
  07 and 13), plus a `TESTING` run type whose phases end in an unconditional destroy.
- The registry must parse `.spacelift/config.yml` module keys, run test cases in parallel
  with `depends_on` ordering, and report one commit status per test case.
- Version creation must enforce `major.minor.patch`, no reuse of a successful number and
  no skipped numbers, and model `PENDING`, `ACTIVE`, `FAILED` plus a permanent `yanked`
  flag and notes.
- Module metadata extraction (inputs, outputs, providers, resources, submodules, examples)
  feeds both the UI and the protocol's versions response.
- Sharing has three layers (space inheritance, explicit space availability, cross-account)
  plus irreversible public visibility, each with its own permission check.
- infraweave must serve Terraform remote service discovery (`login.v1`, `modules.v1`,
  `providers.v1`), the module and provider registry protocols with short-lived signed
  download URLs, and the token formats Terraform, Dependabot and Renovate send.
- Runs must receive generated registry credentials in `~/.terraformrc`, merged with
  mounted `*.terraformrc` files and readable by OpenTofu.
- The provider registry needs drafts, publish, revoke and delete with number-reuse rules,
  per-platform presigned uploads, account-level GPG keys with revocation, signature and
  checksum verification, documentation archives, and a CI publishing path compatible with
  GoReleaser output.
- Trigger policies on modules need consumer tracking per version.

## Inferred and undocumented

- The registry protocol endpoints, response bodies, status codes and host roles come
  from live captures [C], not documentation.
- Provider endpoint response fields follow HashiCorp's protocol [I]; Spacelift's exact
  bodies were not captured because the endpoints need credentials.
- The mapping of RBAC actions to mutations is by name [I].
- `moduleShare` replace semantics are inferred from the provider sending full sets [I].
- That `GPGKey.id` is the signer's long key ID is inferred from spacectl [I].
- That the protocol list and download leave out unusable versions is inferred from
  gaps in a live versions list and the no-skip rule [I].
- *(unverified)* items: negative test case outcome semantics; whether the stale-run
  cleanup applies on SaaS; `versionTrigger`'s intermediate state; `versionPush`'s
  purpose; which state (failed or yanked) produces each gap in the protocol list, and
  download of yanked versions by existing consumers; the US-region source host; public provider access
  without credentials; draft revoke and revoked delete; documentation archive
  classification; the action gating verification and docs upload.
- The changelog deprecates `ModuleCreateInput.space` (2026-10-20), but the snapshot
  carries no input-field deprecation data at all, so the schema cannot confirm it; the
  changelog is the only source.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e` |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| spacectl | `6bf167cb62a6` |
| Self-hosted documentation | `self-hosted/latest` (changelog up to v7.1.0) |
| Live registry captures | 2026-10-28 |

## Sources

- <https://docs.spacelift.io/vendors/terraform/module-registry>
- <https://docs.spacelift.io/vendors/terraform/provider-registry>
- <https://docs.spacelift.io/vendors/terraform/external-modules>
- <https://docs.spacelift.io/vendors/terraform/cli-configuration>
- <https://docs.spacelift.io/vendors/terragrunt/reference>
- <https://docs.spacelift.io/concepts/run/test-case>
- <https://docs.spacelift.io/concepts/configuration/runtime-configuration/runtime-yaml-reference>
- <https://docs.spacelift.io/concepts/authorization/rbac-system>
- <https://docs.spacelift.io/concepts/policy/push-policy>
- <https://docs.spacelift.io/concepts/policy/trigger-policy>
- <https://docs.spacelift.io/concepts/worker-pools>
- <https://docs.spacelift.io/concepts/intelligence/intent/spacelift-modules>
- <https://docs.spacelift.io/product/changelog> (entries 2025-10-17, 2025-11-10, 2025-12-10, 2026-01-09, 2026-07-16, 2026-08-04, 2026-08-21, 2026-10-12, 2026-10-20)
- <https://docs.spacelift.io/self-hosted/latest/vendors/terraform/module-registry>
- <https://docs.spacelift.io/self-hosted/latest/vendors/terraform/provider-registry>
- <https://docs.spacelift.io/self-hosted/latest/vendors/terraform/external-modules>
- <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/guides/first-setup>
- <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/reference-architecture/reference/object-storage>
- <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog>
- Live captures, 2026-10-28: <https://spacelift.io/.well-known/terraform.json>, <https://app.spacelift.io/.well-known/terraform.json>, <https://app.us.spacelift.io/.well-known/terraform.json>, `https://app.spacelift.io/registry/modules/v1/spacelift-io/spacelift-workerpool-on-ec2/aws/versions` and its `/{version}/download` (with versions `9.1.0`, `9.1`, `v9.1.0`, `2.11.0`, `99.0.0` and `abc`), `https://app.spacelift.io/registry/providers/v1/spacelift-io/foo/versions` and `/1.0.0/download/linux/amd64`
- HashiCorp protocol specifications: <https://developer.hashicorp.com/terraform/internals/remote-service-discovery>, <https://developer.hashicorp.com/terraform/internals/module-registry-protocol>, <https://developer.hashicorp.com/terraform/internals/provider-registry-protocol>
- [`introspection.json`](../../../api/reference/spacelift/introspection.json), [`terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json), [`terraform-operations.json`](../../../api/reference/spacelift/terraform-operations.json)
- <https://github.com/spacelift-io/terraform-provider-spacelift/tree/c72b9bd2f3c3/spacelift> (`resource_module.go`, `resource_version.go`, `resource_terraform_provider.go`, `data_module.go`, `data_modules.go`)
- <https://github.com/spacelift-io/spacectl/tree/6bf167cb62a6/internal/cmd/module>
- <https://github.com/spacelift-io/spacectl/tree/6bf167cb62a6/internal/cmd/provider>
