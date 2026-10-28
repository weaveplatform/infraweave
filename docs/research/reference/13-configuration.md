# 13: Configuration: contexts, environment, mounted files and precedence

Checked against: docs.spacelift.io read 2026-10-28 (SaaS pages, and self-hosted v7.1.0 pages where noted); introspection `b3255fe38f5e`; terraform-provider-spacelift v1.57.0 at `c72b9bd2f3c3`; spacectl at `6bf167cb62a6`.

Research baseline: **2026-10-28**.

This report records how configuration reaches a Spacelift run: the
configuration elements (environment variables and mounted files) set on a
stack or module, the contexts that bundle elements and hooks for reuse, how
contexts attach (explicitly with a priority, or automatically through
`autoattach:` labels), the values Spacelift computes and injects into every
run, how secrets are hidden and masked, and the order that decides which value
wins when the same name is defined more than once. infraweave's run engine and
its configuration API are built from it. Stack settings and the
`.spacelift/config.yml` keys themselves are in report 07; run phases and the
hooks they run are in report 08; the worker launcher, which can pass its own
environment into runs, is in report 10; cloud integrations, which inject
credentials, are in report 12; spaces and roles are in report 14; stack
dependency outputs are in report 18.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

## 1. The configuration element

A configuration element is either an environment variable or a file mounted
into the workspace. Stacks, modules and contexts all hold them, and every
element has the same shape [G].

### 1.1 `ConfigType`

| Value | Meaning |
|---|---|
| `ENVIRONMENT_VARIABLE` | An environment variable set for the run's processes [G] |
| `FILE_MOUNT` | A file written into the run's workspace [G] |

### 1.2 `ConfigInput` (create and update)

| Field | Type | Meaning | Source |
|---|---|---|---|
| `id` | `ID!` | The variable name, or the mounted file's path relative to `/mnt/workspace/` | [G]; [D](https://docs.spacelift.io/concepts/configuration/environment) |
| `type` | `ConfigType!` | Variable or file | [G] |
| `value` | `String!` | The value. For a `FILE_MOUNT`, the Terraform provider sends the file content Base64-encoded, as it receives it, so the API value of a file is Base64 [I] from [`resource_mounted_file.go#L113-L127`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_mounted_file.go#L113-L127) and the provider's `content` description [S] | [G] |
| `description` | `String` | Free-form, Markdown in the UI | [G]; [D](https://docs.spacelift.io/concepts/configuration/environment) |
| `writeOnly` | `Boolean!` | Secret flag: when true the value is never returned by the API or UI | [G] |
| `fileMode` | `String` | File permission bits (user, group, other) for a mounted file; the provider does not expose it | [G]; [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_mounted_file.go#L119-L127) |

### 1.3 `ConfigElement` (read)

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Variable name or relative file path [G] |
| `checksum` | `String!` | SHA-256 of the value, always returned, secret or not [G]; [D](https://docs.spacelift.io/concepts/configuration/environment) |
| `createdAt` | `Int` | Unix time of creation; empty for elements computed at run time. Elements are immutable, so a value change resets it [G] |
| `runtime` | `Boolean!` | True when the value is only known at run time (computed) [G] |
| `type` | `ConfigType!` | Variable or file [G] |
| `value` | `String` | The value, null when `writeOnly` is true [G] |
| `description` | `String!` | Description [G] |
| `writeOnly` | `Boolean!` | Secret flag [G] |
| `fileMode` | `String` | File mode of a mount [G] |
| `category` | `ConfigElementCategory` | Present for Spacelift-managed elements [G] |

`ConfigElementCategory` has one value, `SPACELIFT_ENVIRONMENT`: an element
Spacelift manages itself, such as API tokens, OIDC tokens and the
`TF_VAR_spacelift_*` metadata [G].

`ConfigElementWithSource` wraps an element with where it came from: `context`
(a `StackContextAttachment`) or `stackDependency` (a `StackDependency`), both
nullable, and the `element` itself [G]. `Stack.runtimeConfig` and
`Module.runtimeConfig` return `[ConfigElementWithSource!]!`, which the schema
describes as the computed set drawn from contexts, the stack and integrations
[G]. This is the API form of the "Environment" view, which shows what a run
will see [D](https://docs.spacelift.io/concepts/configuration/environment).

### 1.4 Operations

| Operation | Arguments | Returns | Notes |
|---|---|---|---|
| `stackConfigAdd` | `stack: ID!`, `config: ConfigInput!` | `ConfigElement!` | Also used for modules, whose IDs are stack IDs [G]; [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go#L170-L190) |
| `stackConfigDelete` | `stack: ID!`, `id: ID!` | `ConfigElement` | [G] |
| `contextConfigAdd` | `context: ID!`, `config: ConfigInput!` | `ConfigElement!` | Needs `Context Update` on the context's space [G] |
| `contextConfigDelete` | `context: ID!`, `id: ID!` | `ConfigElement` | Needs `Context Update` on the context's space [G] |
| `Stack.config`, `Stack.configElement(id)` | | `[ConfigElement!]!`, `ConfigElement` | Elements set directly on the stack [G] |
| `Stack.runtimeConfig` | | `[ConfigElementWithSource!]!` | The merged view [G] |
| `Context.config`, `Context.configElement(id)` | | `[ConfigElement!]!`, `ConfigElement` | [G] |

There is no update mutation. The UI edits a value in place
[D](https://docs.spacelift.io/concepts/configuration/environment), and the
provider marks every stored attribute of `spacelift_environment_variable` and
`spacelift_mounted_file` `ForceNew`, so a change is a delete and a re-add. The
write-only `value_wo` and `content_wo` cannot be `ForceNew`; a change to them
takes effect only through the `ForceNew` `value_wo_version` or
`content_wo_version`
[S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go#L46-L110);
[S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_mounted_file.go#L42-L109).
Whether `stackConfigAdd` with an existing `id` replaces the element in place
is *(unverified)*; the UI's edit-in-place behaviour suggests it does [I].

## 2. Environment variables

| Rule | Detail | Source |
|---|---|---|
| Scope | Available to every process in the run | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Input variables | A `TF_VAR_` prefix makes a variable an OpenTofu/Terraform input variable; Spacelift has no separate input-variable mechanism | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Name | Cannot be renamed after creation; only the value changes | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Newlines | Not supported in values; use a mounted file or Base64 | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Interpolation | A value may reference another variable as `${NAME}`. The worker resolves it at run time, and it works between variables defined in different places, contexts included; the page does not say whether computed values take part | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Persistence across phases | Variables exported by a hook are visible to the phase in the same shell session, and environment variables persist across phases | [D](https://docs.spacelift.io/concepts/stack/stack-settings) |

### 2.1 Read and write stage prefixes

A variable is passed to every stage by default. Two name prefixes narrow that
[D](https://docs.spacelift.io/concepts/configuration/environment):

| Prefix | Passed to | Not passed to |
|---|---|---|
| `ro_` | Read stages: every stage except applying | The applying stage |
| `wo_` | The write stage: applying | Every read stage |

Tasks receive `ro_` variables during preparation since 2026-05-21
[RN](https://docs.spacelift.io/product/changelog). The prefixes do not help
with `-backend-config` passed through `ro_`/`wo_TF_CLI_ARGS_init`: OpenTofu and
Terraform record backend configuration in the plan file, so the apply reuses
the planner's backend settings. The documented workaround is `ro_`/`wo_`
cloud credentials that the SDK reads at run time, such as `AWS_ROLE_ARN` with
`AWS_WEB_IDENTITY_TOKEN_FILE`
[D](https://docs.spacelift.io/concepts/configuration/environment). Whether the
prefix is stripped from the name the process sees is not stated on that page
*(unverified)*; the docs' `GITHUB_TOKEN` example implies the provider reads the
unprefixed name [I].

The private-worker launcher has the same prefixes for variables it forwards
from its own environment with `SPACELIFT_WHITELIST_ENVS` (report 10)
[D](https://docs.spacelift.io/concepts/worker-pools).

### 2.2 User-tunable `SPACELIFT_*` variables

Users set these on a stack or context to change Spacelift's own behaviour.

| Variable | Effect | Source |
|---|---|---|
| `SPACELIFT_STATE_ANALYTICS_UPLOAD_TIMEOUT` | Timeout for uploading state analytics; Go-style duration (`ns`, `us`/`µs`, `ms`, `s`, `m`), at most 10 minutes | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| `SPACELIFT_STATE_OUTPUTS_UPLOAD_TIMEOUT` | Timeout for uploading state outputs; same format and cap | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| `SPACELIFT_MODULE_METADATA_UPLOAD_TIMEOUT` | Timeout for uploading module metadata; same format and cap | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| `SPACELIFT_SKIP_APPLYING` | `true` skips the applying phase without running it | [D](https://docs.spacelift.io/concepts/run/tracked) |
| `SPACELIFT_SKIP_PLANNING` | `true` skips planning, Ansible stacks only | [D](https://docs.spacelift.io/concepts/run/proposed); [D](https://docs.spacelift.io/vendors/ansible/reference) |
| `SPACELIFT_SKIP_DESTROYING` | `true` skips the destroying phase of module test cases | [D](https://docs.spacelift.io/concepts/run/test-case) |
| `SPACELIFT_DISABLE_STATE_LOCK` | Stops `terraform plan` taking a state lock (report 16) | [D](https://docs.spacelift.io/vendors/terraform/state-management) |
| `SPACELIFT_IGNORE_SKIPPED_TASK_CHANGES` | `true` removes skipped Ansible tasks from the change count, so autodeploy can confirm | [D](https://docs.spacelift.io/vendors/ansible/reference) |
| `SPACELIFT_ANSIBLE_CLI_ARGS`, `SPACELIFT_ANSIBLE_CLI_ARGS_plan`, `SPACELIFT_ANSIBLE_CLI_ARGS_apply` | Extra Ansible arguments for both phases, planning only, or applying only; generic and phase-specific both apply | [D](https://docs.spacelift.io/vendors/ansible/reference) |
| `SPACELIFT_ANSIBLE_REQUIREMENTS_FILE` | Path of a non-default `requirements.yml` | [D](https://docs.spacelift.io/vendors/ansible/reference) |
| `SPACELIFT_PULUMI_CLI_ARGS_preview`, `_up`, `_destroy` | Extra Pulumi arguments per command | [D](https://docs.spacelift.io/vendors/pulumi) |
| `SPACELIFT_KUBERNETES_DEBUG` | `1` adds debug output on Kubernetes stacks | [D](https://docs.spacelift.io/vendors/kubernetes/workflow-tool) |
| `TG_CLI_ARGS_init`, `_plan`, `_apply`, `_destroy` | Extra Terragrunt arguments per phase | [D](https://docs.spacelift.io/vendors/terragrunt/reference) |

`TF_CLI_ARGS_*` are OpenTofu/Terraform's own variables, and Spacelift relies
on them too: spacectl's local preview turns `--target` flags into a
`TF_CLI_ARGS_plan` override
[S](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/local_preview.go#L191-L208).

## 3. Mounted files

| Rule | Detail | Source |
|---|---|---|
| Root | Every run has `/mnt/workspace/`; file paths are relative to it, and `/` nests directories (`a/b/c/d/e.json` is valid) | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Source checkout | The repository is cloned to `/mnt/workspace/source/`, the working directory unless a project root is set | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Clashes | A mounted file under `source/` overwrites a repository file with the same path | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Size | 2 MB per file | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Download | Plain files can be downloaded from the UI and API; secret files cannot | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Provider path | `relative_path` excludes the `/mnt/workspace/` prefix; `content` is Base64 | [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_mounted_file.go#L42-L110) |

Spacelift itself writes files into the same tree, such as the OIDC token at
`/mnt/workspace/spacelift.oidc`
[D](https://docs.spacelift.io/integrations/cloud-providers/oidc).

## 4. Secrets: write-only values and masking

### 4.1 Plain and secret

| | Plain | Secret (`writeOnly: true`) |
|---|---|---|
| Value in UI and API | Shown | Hidden; `value` is null [G] |
| Checksum | Shown | Shown, so a holder of the value can compare SHA-256 [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Mounted file download | Allowed, from the UI and the API | Refused [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Visible to | Users, API, runs | Runs and tasks only [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Storage | Encrypted at rest and in transit | The same, plus two extra protection layers the docs do not detail [D](https://docs.spacelift.io/concepts/configuration/environment) |

A stack's `SPACELIFT_API_TOKEN` cannot read secret values either, and unlike a
user token it cannot be used to print a secret by running `env` in a task and
reading the logs back
[D](https://docs.spacelift.io/vendors/terraform/terraform-provider).

### 4.2 Defaults differ by client

| Client | Default for secret |
|---|---|
| UI | Off ("Mark as secret" is an opt-in toggle) [D](https://docs.spacelift.io/concepts/configuration/environment) |
| GraphQL | No default: `writeOnly: Boolean!` is required [G] |
| Terraform provider | `write_only` defaults to `true` for both variables and files [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go#L99-L105) |

### 4.3 Three meanings of "write-only"

| Term | Meaning | Source |
|---|---|---|
| `writeOnly` / `write_only` | Secret: the value is write-only from the API's point of view | [G]; [S] |
| `wo_` name prefix | Passed only to the applying (write) stage | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Terraform write-only arguments `value_wo`, `content_wo` | Terraform 1.11+ / OpenTofu arguments kept out of Terraform state; changed by bumping `value_wo_version` or `content_wo_version` | [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go#L72-L98) |

When `write_only` is set and no `*_wo_version` is used, the provider stores the
checksum in its state in place of the hidden value, to detect drift
[S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_mounted_file.go#L227-L240).

### 4.4 Log masking

| Mechanism | Behaviour | Source |
|---|---|---|
| Secret elements | Secret values are masked in run output | [D](https://docs.spacelift.io/concepts/run/task) |
| `::add-mask` runtime command | A hook or phase prints `::add-mask v1 v2`; each whitespace-separated word is masked from then on and replaced by five `*` characters | [D](https://docs.spacelift.io/concepts/stack/stack-settings) |
| Well-known secret masking | Stack setting `enableWellKnownSecretMasking` (report 07) redacts recognised patterns: AWS access key IDs, Entra ID client secrets, Azure connection strings, GitHub PATs (classic and fine-grained), GitHub App, refresh and OAuth tokens, JWTs, Slack tokens, PGP and RSA private keys, PEM `BEGIN PRIVATE KEY` blocks | [D](https://docs.spacelift.io/concepts/stack/stack-settings); [G] |
| `SPACELIFT_MASK_ENVS` (private worker) | Variables the launcher forwards that must never appear in logs | [D](https://docs.spacelift.io/concepts/worker-pools) |
| Limit | Masking works one log line at a time, so a value spanning several lines (a PEM key, for example) is not masked by any mechanism | [D](https://docs.spacelift.io/concepts/stack/stack-settings) |

`proposeWithOverrides` accepts `environmentVarsOverrides`, and the schema warns
that these values are not treated as secrets [G].

## 5. Contexts

A context is a named bundle of environment variables, mounted files and hooks,
managed apart from any stack and attached to any number of stacks and modules
[D](https://docs.spacelift.io/concepts/configuration/context). The self-hosted
v7.1.0 page defines a context as variables and mounted files only, yet its own
priority section ranks context hooks too
[D](https://docs.spacelift.io/self-hosted/latest/concepts/configuration/context);
the SaaS schema has `Context.hooks` [G].

### 5.1 `Context` fields

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Immutable slug derived from the first name, unique in the account [G]; [D](https://docs.spacelift.io/concepts/configuration/context) |
| `name` | `String!` | Mutable display name; the docs discourage diverging far from the slug [G]; [D](https://docs.spacelift.io/concepts/configuration/context) |
| `description` | `String` | Markdown [G] |
| `labels` | `[String!]!` | Free labels, including `autoattach:` [G] |
| `space`, `spaceDetails` | `ID!`, `Space!` | Owning space [G] |
| `hooks` | `Hooks!` | The context's hooks [G] |
| `config`, `configElement(id)` | `[ConfigElement!]!`, `ConfigElement` | Elements [G] |
| `attachedStacks`, `attachedStack(id)` | `[ContextStackAttachment!]!` | Attachments, explicit and automatic [G] |
| `hasAttachedStacks` | `Boolean!` | True when any stack is attached, explicitly or automatically. The schema's description says "policy", a copy error [G] |
| `createdAt`, `updatedAt` | `Int!` | Unix times [G] |
| `plugin` | `Plugin` | Set when a plugin manages the context, which restricts edits [G]; plugin-managed contexts are locked in the UI [RN](https://docs.spacelift.io/product/changelog) (2025-11-13) |
| `allowedActions` | `[Action!]!` | What the caller may do [G] |

### 5.2 `ContextInput` and its mutations

| `ContextInput` field | Type | Notes |
|---|---|---|
| `name` | `String!` | |
| `description` | `String` | |
| `hooks` | `HooksInput` | Eleven lists, each `[String!]!`: `beforeInit`, `afterInit`, `beforePlan`, `afterPlan`, `beforeApply`, `afterApply`, `beforePerform`, `afterPerform`, `beforeDestroy`, `afterDestroy`, `afterRun` [G] |
| `labels` | `[String!]` | |
| `space` | `ID` | |
| `stackAttachments` | `[ContextStackAttachmentInput!]` | Each `{stack: ID!, priority: Int!}` [G] |
| `configAttachments` | `[ConfigInput!]` | Elements created with the context [G] |

| Mutation | Arguments | Notes |
|---|---|---|
| `contextCreateV2` | `input: ContextInput!` | [G] |
| `contextUpdateV2` | `id`, `input`, `replaceConfigElements: Boolean` | Without `replaceConfigElements: true`, `input.configAttachments` is ignored. With it, the context's elements become exactly the list sent, and omitted ones are deleted [G]. The provider never sets it [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_context.go#L245-L250) |
| `contextCreate`, `contextUpdate` | Flat arguments, no hooks or elements | Deprecated in favour of the V2 forms [G]; listed in the changelog's deprecations on 2026-07-16 [RN](https://docs.spacelift.io/product/changelog) |
| `contextDelete` | `id`, `force: Boolean` | Deleting detaches the context from every project and erases its data with no history kept [D](https://docs.spacelift.io/concepts/configuration/context). What `force` changes is undocumented *(unverified)* |
| `contextAttach` | `id`, `stack`, `priority: Int!` | Returns `ContextStackAttachment!` [G] |
| `contextDetach` | `id` (the attachment ID) | [G]; [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_context_attachment.go#L126-L130) |
| `context`, `contexts`, `attachableContextsInSpace(id)`, `searchContexts`, `searchSpaceContexts` | | Reads [G] |

The `Action` enum keeps `STACK_CONTEXT_ATTACH` and `STACK_CONTEXT_DETACH`
marked "Disabled. Do not use." beside `CONTEXT_CREATE`, `CONTEXT_UPDATE` and
`CONTEXT_DELETE` [G].

### 5.3 Attachments

| `ContextStackAttachment` (from the context) | `StackContextAttachment` (from the stack) |
|---|---|
| `id`, `stackId`, `stackName`, `stackSpaceDetails`, `isModule`, `createdAt`, `priority`, `isAutoattached` [G] | `id`, `contextId`, `contextName`, `contextDescription`, `contextLabels`, `contextHooks`, `config`, `priority`, `isAutoattached` [G] |

Rules:

| Rule | Source |
|---|---|
| A context attaches to a given project at most once | [D](https://docs.spacelift.io/concepts/configuration/context) |
| Priority is an integer on the attachment, not on the context; values need not be unique | [D](https://docs.spacelift.io/concepts/configuration/context) |
| Attachments sort by priority, lowest first | [D](https://docs.spacelift.io/concepts/configuration/context); [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_context_attachment.go#L48-L54) |
| On a conflict the lower number wins: Spacelift's migration tooling attaches the overriding context at `0` and others at `1` for that reason | [D](https://docs.spacelift.io/product/migrating-to-spacelift/generate) |
| The provider defaults `priority` to `0` and replaces the attachment when it changes | [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_context_attachment.go#L48-L54) |
| The UI can edit an attachment's priority in place | [D](https://docs.spacelift.io/concepts/configuration/context) |
| Which contexts a stack may attach: those in its own space, and those in ancestor spaces reached through an unbroken chain of spaces with inheritance enabled | [D](https://docs.spacelift.io/concepts/spaces/how-access-works) |

The documents disagree on wording only: "lowest values go first" and "evaluated
starting with the lowest priority" read as ordering, while the migration page
states the effect, that a lower number takes precedence. The schema has no
description to settle it [G].

### 5.4 Auto-attachment

| Rule | Source |
|---|---|
| A context labelled `autoattach:<label>` attaches to every stack and module carrying `<label>`; the project needs only the bare label | [D](https://docs.spacelift.io/concepts/configuration/context) |
| `autoattach:*` attaches to every project in the context's space and its child spaces | [D](https://docs.spacelift.io/concepts/configuration/context) |
| Auto-attached contexts have no priority picker; among themselves they order alphabetically by name | [D](https://docs.spacelift.io/concepts/configuration/context) |
| Attachments made this way report `isAutoattached: true` | [G] |
| The same label mechanism attaches policies (except login policies) and AWS and Azure integrations | [D](https://docs.spacelift.io/concepts/policy); [D](https://docs.spacelift.io/concepts/configuration/context) |
| For AWS and Azure integrations only, `autoattach_read:` and `autoattach_write:` restrict the attachment to read or write; combined with `autoattach:` both are granted | [D](https://docs.spacelift.io/concepts/configuration/context); [RN](https://docs.spacelift.io/product/changelog) (2026-06-10) |

What `priority` an auto-attached attachment reports, and whether the
`autoattach:*` reach into child spaces depends on their inheritance toggle (as
the spaces page says for policies,
[D](https://docs.spacelift.io/concepts/spaces)), are *(unverified)*.

### 5.5 Hook order with contexts

Hooks from the stack, explicitly attached contexts and auto-attached contexts
run in a fixed order [D](https://docs.spacelift.io/concepts/stack/stack-settings):

| Position | Before-phase hooks | After-phase hooks |
|---|---|---|
| 1 | Explicit contexts, ascending priority | Stack hooks |
| 2 | Auto-attached contexts, reverse alphabetical | Auto-attached contexts, alphabetical |
| 3 | Stack hooks | Explicit contexts, descending priority |

The order nests: the outermost wrapper runs first before the phase and last
after it. The docs' worked example, with auto-attached `context_a` and
`context_b`, `context_c` at priority 0 and `context_d` at priority 5, runs
`c, d, b, a, stack` before and `stack, a, b, d, c` after. Within a list, hooks
run in the order shown, and context hooks can only be edited from the context
[D](https://docs.spacelift.io/concepts/stack/stack-settings). Hook execution
semantics (`&&` chaining, `;`, failure behaviour, `after_run` and
`TF_VAR_spacelift_final_run_state`) are in report 08.

## 6. The computed environment

Computed elements are shown as `<computed>` in the Environment view and are not
set on the stack; integrations and Spacelift itself add them at run time
[D](https://docs.spacelift.io/concepts/configuration/environment). In the API
they carry `runtime: true` and, for Spacelift's own, `category:
SPACELIFT_ENVIRONMENT` [G].

### 6.1 Spacelift metadata (`TF_VAR_spacelift_*`)

Every run gets these, named so they double as OpenTofu/Terraform input
variables [D](https://docs.spacelift.io/concepts/configuration/environment):

| Variable | Type | Content |
|---|---|---|
| `TF_VAR_spacelift_account_name` | string | The account host, `<account>.app.spacelift.io` |
| `TF_VAR_spacelift_commit_branch` | string | Branch of the run's commit |
| `TF_VAR_spacelift_commit_sha` | string | 40-character commit SHA |
| `TF_VAR_spacelift_local_preview` | boolean | Whether local preview is enabled on the stack |
| `TF_VAR_spacelift_project_root` | string | The stack's project root, relative |
| `TF_VAR_spacelift_repository` | string | `owner/repo` |
| `TF_VAR_spacelift_run_id` | string | The run's ULID |
| `TF_VAR_spacelift_run_state` | enum | Current `RunState` (report 08) |
| `TF_VAR_spacelift_run_trigger` | string | Who or what triggered the run: a user name, `vcs/commit`, `git` (fallback when no trigger is recorded, as for pull-request proposed runs), trigger policy, schedule, API key, reconciliation, modules; examples, not a closed list |
| `TF_VAR_spacelift_run_type` | enum | `PROPOSED`, `TRACKED`, `TASK`, `TESTING`, `DESTROY`, `PARSE` |
| `TF_VAR_spacelift_space_id` | string | The stack's space, such as `root` |
| `TF_VAR_spacelift_stack_branch` | string | The stack's tracked branch |
| `TF_VAR_spacelift_stack_id` | string | Stack slug |
| `TF_VAR_spacelift_stack_labels` | string | Labels, comma-separated |
| `TF_VAR_spacelift_stack_labels_list` | string | Labels as an HCL list literal |
| `TF_VAR_spacelift_workspace_root` | string | Absolute workspace root, such as `/mnt/workspace` |
| `TF_VAR_spacelift_final_run_state` | | Only in `after_run` hooks: the run's final state [D](https://docs.spacelift.io/concepts/stack/stack-settings) |

The docs advise against overriding these
[D](https://docs.spacelift.io/concepts/configuration/environment). The docs'
`TF_VAR_spacelift_run_state` value list includes the deprecated `PENDING`, and
matches the schema's `RunState` without `UNKNOWN` [G].

### 6.2 Other injected values

| Value | Content | Source |
|---|---|---|
| `SPACELIFT_API_TOKEN` | Bearer token carrying the API endpoint; configures the Terraform provider and spacectl inside a run. It is valid for 3 hours, and only while its run is in `PLANNING`, `APPLYING` or `PERFORMING`. Stacks without role attachments get a token that cannot modify Spacelift resources; with roles, a broader token still narrower than a user's: it can manage stacks, contexts, integrations and configuration, but cannot read Terraform state, runs, tasks or their logs | [D](https://docs.spacelift.io/vendors/terraform/terraform-provider); [D](https://docs.spacelift.io/concepts/spacectl) |
| `SPACELIFT_OIDC_TOKEN` and `/mnt/workspace/spacelift.oidc` | Spacelift-signed JWT valid for one hour, in every run on paid accounts (claims in report 12) | [D](https://docs.spacelift.io/integrations/cloud-providers/oidc) |
| Cloud credentials | Set by attached cloud integrations, such as `AWS_ACCESS_KEY_ID` (report 12) | [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Stack dependency inputs | Outputs of upstream stacks, under the input name each reference chooses, often `TF_VAR_*`; a missing output fails assignment with "the following inputs are missing" (report 18) | [D](https://docs.spacelift.io/concepts/stack/stack-dependencies) |
| Launcher-forwarded variables | Variables named in `SPACELIFT_WHITELIST_ENVS` on a private worker (report 10) | [D](https://docs.spacelift.io/concepts/worker-pools) |

One sentence of the Terraform provider page says the API token goes to runs
of stacks that have roles attached, while the same page later describes the
limited token that role-less stacks receive, and the environment page says
every run gets one
[D](https://docs.spacelift.io/vendors/terraform/terraform-provider);
[D](https://docs.spacelift.io/concepts/configuration/environment). Every run
receiving a token, narrowed by the stack's roles, is the reading both pages
support [I].

The schema's description of `SPACELIFT_ENVIRONMENT` names API tokens, OIDC
tokens and the `TF_VAR_spacelift_*` metadata as Spacelift-managed [G]. A
search of runner-terraform, runner-opentofu, runner-pulumi, runner-ansible
and spacelift-worker-image at their pinned commits finds only launcher and
worker-pool variables (`SPACELIFT_TOKEN`, `SPACELIFT_POOL_PRIVATE_KEY`,
`SPACELIFT_METADATA_*` and similar, report 10), and no run-time variable the
docs omit [S]. The runner side that injects the variables is closed source, so
this list cannot be checked for completeness *(unverified)*.

### 6.3 Overriding a computed value

A computed value cannot be set, but a stack variable of the same name overrides
it, becoming an ordinary editable variable. Deleting that variable restores
the computed value; removing a computed variable altogether requires disabling
the integration that adds it. A context-supplied value is overridden on the
stack the same way, and is restored when the stack variable is deleted
[D](https://docs.spacelift.io/concepts/configuration/environment).

## 7. Precedence

### 7.1 The documented order

Highest first [D](https://docs.spacelift.io/concepts/configuration):

| Rank | Source | Notes |
|---|---|---|
| 1 | `.spacelift/config.yml`, the stack's own block under `stacks.<slug>` | Highest in both file versions |
| 2 | `.spacelift/config.yml`, `stack_defaults` | Version 2 files; in version 1 files rank 3 outranks it [D](https://docs.spacelift.io/concepts/configuration/runtime-configuration) |
| 3 | The stack: its environment and its settings | Report 07 for settings |
| 4 | Explicitly attached contexts, by priority, lowest number first | |
| 5 | Auto-attached contexts, alphabetically | [D](https://docs.spacelift.io/concepts/configuration/context) |

The runtime configuration page restates ranks 1 to 3, and says the runtime
file's `environment` map overrides both context and stack variables on a
conflict [D](https://docs.spacelift.io/concepts/configuration/runtime-configuration).
The context page gives ranks 3 to 5 for variables, mounted files and hooks
[D](https://docs.spacelift.io/concepts/configuration/context); the
configuration page itself has one context rank and does not separate
auto-attached contexts [D](https://docs.spacelift.io/concepts/configuration).
A stack labelled `feature:ignore_runtime_config` ignores the runtime file, so
ranks 1 and 2 drop out for it
[D](https://docs.spacelift.io/concepts/stack/stack-settings).

### 7.2 Where the sources disagree or are silent

| Point | What the sources say | Status |
|---|---|---|
| Runtime file version | Version 2 puts the file above stack settings. Version 1 put stack settings above `stack_defaults` [D](https://docs.spacelift.io/concepts/configuration/runtime-configuration). That page says `version` defaults to `1`; the YAML reference says `version` is optional and ignored [D](https://docs.spacelift.io/concepts/configuration/runtime-configuration/runtime-yaml-reference) | Contradictory; which applies to a file without `version` is *(unverified)* |
| No stack block | When the file has no block for the stack, the page lists the stack's settings first and `stack_defaults` second [D](https://docs.spacelift.io/concepts/configuration/runtime-configuration) | Read literally, that reverses rank 2 and 3 for such stacks *(unverified)* |
| Hooks from the runtime file | The file's hooks "can override" stack hooks [D](https://docs.spacelift.io/concepts/stack/stack-settings) | Whether they replace or extend stack hooks, and what happens to context hooks, is *(unverified)* |
| Computed values | A stack variable overrides a computed one [D](https://docs.spacelift.io/concepts/configuration/environment) | Where computed values rank against contexts is *(unverified)* |
| Stack dependency inputs | Not placed in any order | *(unverified)* |
| Launcher-forwarded variables | Not placed in any order | *(unverified)* |
| Per-run overrides | `runTrigger(runtimeConfig: RuntimeConfigInput)` carries either a `yaml` document or the fields `environment` (`[EnvVarInput!]`), `projectRoot`, `runnerImage`, `gitSparseCheckoutPaths` and the eleven hook lists, not both; `proposeWithOverrides` and `runProposeLocalWorkspace` carry `environmentVarsOverrides` [G]; spacectl exposes `--env-var-override` and `--tf-env-var-override`, the latter adding a `TF_` prefix [S](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/local_preview.go#L150-L172) | Their rank is undocumented; that they win over every stored source is *(unverified)* [I] from their purpose |
| Interpolation and precedence | `${NAME}` resolves on the worker across sources [D](https://docs.spacelift.io/concepts/configuration/environment) | Whether it resolves against the winning value only is *(unverified)* |

### 7.3 Settings, not only variables

The precedence applies to "settings" in general: any key the runtime file
can carry (runner image, project root, tool versions, hooks, environment) is
resolved with ranks 1 to 3 [D](https://docs.spacelift.io/concepts/configuration),
and contexts contribute only variables, files and hooks
[D](https://docs.spacelift.io/concepts/configuration/context). The full key
list is in report 07.

## 8. Typing traps

| Trap | Detail |
|---|---|
| Base64 file content | A mounted file's value travels Base64-encoded through the provider, while an environment variable's value is raw [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_mounted_file.go#L42-L50) |
| Secret default | UI off, provider on, GraphQL required (section 4.2) |
| Element `id` | The name or relative path is the element's ID; renaming is a delete and create [G]; [D](https://docs.spacelift.io/concepts/configuration/environment) |
| Provider resource IDs | `stack/<id>/<name>`, `module/<id>/<name>`, `context/<id>/<name>` (or `<relative_path>`) [S](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go#L160-L195) |
| `createdAt` | Nullable; null for computed elements and reset when the value changes [G] |
| `value` | Null both for secrets and, for computed elements, before the run [G] |
| Priority direction | Lower number, higher precedence (section 5.3) |
| `contextUpdateV2` | Silently ignores `configAttachments` unless `replaceConfigElements` is true [G] |
| Three "write-only"s | Section 4.3 |

## What this means for infraweave

- A configuration element model with two types, a secret flag whose value is
  never readable through the API, an always-readable SHA-256 checksum, file
  modes and a 2 MB file limit, held by stacks, modules and contexts.
- Contexts with variables, files and the eleven hook lists, explicit
  attachments with integer priority, and label-driven auto-attachment
  (`autoattach:<label>`, `autoattach:*`) scoped by space and inheritance.
- A merged, per-run environment view that records each value's source
  (stack, context attachment, stack dependency, integration, Spacelift), as
  `runtimeConfig` does.
- Injection of the `TF_VAR_spacelift_*` metadata, an API token scoped by the
  stack's roles, an OIDC token in variable and file form, and the user-tunable
  `SPACELIFT_*` switches.
- `ro_`/`wo_` stage filtering, `${NAME}` interpolation on the worker, and log
  masking for secrets, `::add-mask` and well-known patterns, with the
  single-line limit.
- A precedence order that has to be fixed where Spacelift's is silent
  (computed values, dependency inputs, launcher variables, per-run overrides)
  and documented as a divergence where it is chosen.

## Inferred and undocumented

- Mounted file values are Base64 at the API, inferred from the provider passing
  its Base64 `content` straight into `ConfigInput.value`.
- `stackConfigAdd` on an existing ID updating in place, inferred from the UI.
- The rank of per-run overrides, inferred from their purpose.
- The `ro_`/`wo_` prefix being stripped before the process sees the variable,
  inferred from the documented example.
- Version 1 versus 2 precedence for files without `version`, the order when
  the file has no stack block, how runtime-file hooks combine with stack and
  context hooks, where computed, dependency and launcher values rank, the
  priority of auto-attached attachments, inheritance and `autoattach:*`,
  and `contextDelete(force)`: all undocumented and *(unverified)*.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e`, fetched 2026-10-28 |
| docs.spacelift.io | read 2026-10-28; self-hosted pages at v7.1.0 |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| spacectl | `6bf167cb62a6` |

## Sources

- [D] <https://docs.spacelift.io/concepts/configuration>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/configuration/context>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/configuration/environment>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/configuration/runtime-configuration>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/configuration/runtime-configuration/runtime-yaml-reference>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/stack/stack-settings>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/stack/stack-dependencies>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/spaces>, <https://docs.spacelift.io/concepts/spaces/how-access-works>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/policy>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/run/task>, <https://docs.spacelift.io/concepts/run/tracked>, <https://docs.spacelift.io/concepts/run/proposed>, <https://docs.spacelift.io/concepts/run/test-case>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/worker-pools>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/spacectl>, read 2026-10-28.
- [D] <https://docs.spacelift.io/integrations/cloud-providers/oidc>, read 2026-10-28.
- [D] <https://docs.spacelift.io/vendors/terraform/terraform-provider>, <https://docs.spacelift.io/vendors/terraform/state-management>, read 2026-10-28.
- [D] <https://docs.spacelift.io/vendors/ansible/reference>, <https://docs.spacelift.io/vendors/pulumi>, <https://docs.spacelift.io/vendors/kubernetes/workflow-tool>, <https://docs.spacelift.io/vendors/terragrunt/reference>, read 2026-10-28.
- [D] <https://docs.spacelift.io/product/migrating-to-spacelift/generate>, read 2026-10-28.
- [D] <https://docs.spacelift.io/self-hosted/latest/concepts/configuration/context>, read 2026-10-28.
- [RN] <https://docs.spacelift.io/product/changelog>, entries of 2025-11-13, 2026-05-21, 2026-06-10 and 2026-07-16.
- [G] [`api/reference/spacelift/introspection.json`](../../../api/reference/spacelift/introspection.json): `ConfigType`, `ConfigInput`, `ConfigElement`, `ConfigElementCategory`, `ConfigElementWithSource`, `Context`, `ContextInput`, `ContextStackAttachment`, `ContextStackAttachmentInput`, `StackContextAttachment`, `HooksInput`, `EnvironmentVariable`, `EnvVarInput`, `RuntimeConfigInput`, `Action`, and the root fields named above. Tables generated by script from the snapshot.
- [S] terraform-provider-spacelift at `c72b9bd2f3c3` (MIT): [`resource_context.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_context.go), [`resource_context_attachment.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_context_attachment.go), [`resource_environment_variable.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_environment_variable.go), [`resource_mounted_file.go`](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_mounted_file.go).
- [S] spacectl at `6bf167cb62a6` (MIT): [`internal/cmd/stack/local_preview.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/local_preview.go), [`internal/cmd/stack/flags.go`](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/stack/flags.go#L146-L154).
- [`api/reference/spacelift/terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json): `spacelift_context`, `spacelift_context_attachment`, `spacelift_environment_variable`, `spacelift_mounted_file`.
