# 25: Other integrations

Checked against: docs.spacelift.io read 2026-10-28 (SaaS and `self-hosted/latest`); introspection `b3255fe38f5e`; terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); plugins `930c4509a789`; backstage-plugins `27d06658468a`; spacelift-operator `341692ad7d56` (archived, read over the network 2026-10-28); liftoff v1.5.1 (repository metadata only, read 2026-10-28).

Research baseline: **2026-10-28**.

This report records the integrations that no other report owns: plugins and
plugin templates (the `spacelift-io/plugins` repository and its `spaceforge`
SDK), external integrations with ServiceNow as the only typed member, the
Backstage plugins, the Teleport join method, the Docker page's rules for runner
images, the archived Kubernetes operator, and `liftoff`, Spacelift's migration
tool. infraweave's plugin engine, its external-integration resource and its
client ecosystem are planned from it. Run phases and hooks are in report 08,
policies and custom policy input in [09](09-policies.md), runner images and
private workers in [10](10-workers-and-execution.md), contexts in
[13](13-configuration.md), API keys and roles in
[14](14-spaces-rbac-and-identity.md), webhooks and the audit trail in
[19](19-events-notifications-audit.md), blueprints in report 17, Infracost as a
vendor feature in report 20, and spacectl and other clients in report 06.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` ([`introspection.json`](../../../api/reference/spacelift/introspection.json)) |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog, <https://docs.spacelift.io/product/changelog>, or the self-hosted changelog |
| **[C]** | A community source or repository metadata read over the network |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

Short names for the docs pages cited below:

| Short name | URL |
|---|---|
| plugins | <https://docs.spacelift.io/integrations/plugins> |
| servicenow | <https://docs.spacelift.io/integrations/external-integrations/servicenow> |
| backstage | <https://docs.spacelift.io/integrations/external-integrations/backstage> |
| teleport | <https://docs.spacelift.io/integrations/teleport> |
| docker | <https://docs.spacelift.io/integrations/docker> |
| api-keys | <https://docs.spacelift.io/concepts/authorization/assigning-roles-api-keys> |
| liftoff | <https://docs.spacelift.io/product/migrating-to-spacelift> |
| liftoff-setup | <https://docs.spacelift.io/product/migrating-to-spacelift/setup> |
| liftoff-generate | <https://docs.spacelift.io/product/migrating-to-spacelift/generate> |
| liftoff-publish | <https://docs.spacelift.io/product/migrating-to-spacelift/publish> |
| liftoff-byo | <https://docs.spacelift.io/product/migrating-to-spacelift/publish-byo-git> |
| liftoff-mutate | <https://docs.spacelift.io/product/migrating-to-spacelift/mutate> |
| liftoff-finalize | <https://docs.spacelift.io/product/migrating-to-spacelift/finalize> |
| liftoff-transform | <https://docs.spacelift.io/product/migrating-to-spacelift/transform> |
| liftoff-discover | <https://docs.spacelift.io/product/migrating-to-spacelift/discover> |
| liftoff-batch | <https://docs.spacelift.io/product/migrating-to-spacelift/batch> |
| liftoff-audit | <https://docs.spacelift.io/product/migrating-to-spacelift/audit> |
| liftoff-model | <https://docs.spacelift.io/product/migrating-to-spacelift/model> |
| liftoff-models | <https://docs.spacelift.io/product/migrating-to-spacelift/models> |
| liftoff-errors | <https://docs.spacelift.io/product/migrating-to-spacelift/errors> |
| liftoff-restore | <https://docs.spacelift.io/product/migrating-to-spacelift/restore> |
| liftoff-ui | <https://docs.spacelift.io/product/migrating-to-spacelift/ui> |
| sh-changelog | <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog> |

## 1. Scope: the integration pages and their owners

Every page under `pages/integrations/` of the docs mirror, with the report that
owns it. The full page-to-report table for `vendors`, `integrations` and
`product` is in the RES-05 notes and is checked by script.

| Page | Owner |
|---|---|
| `integrations/plugins` | This report, section 2 |
| `integrations/external-integrations/servicenow` | This report, section 3 |
| `integrations/external-integrations/backstage` | This report, section 4 |
| `integrations/teleport` | This report, section 5 |
| `integrations/docker` | Report 10 for runner images and registries; this report, section 6, for the customisation rules |
| `integrations/api` | Reports 01 and 04 |
| `integrations/audit-trail`, `integrations/webhooks`, `integrations/chatops/*`, `integrations/observability*` | Report 19 |
| `integrations/cloud-providers*` and `integrations/cloud-providers/oidc/*` | Report 12 |
| `integrations/single-sign-on*` | Report 14 |
| `integrations/source-control*` | Report 11 |

The Kubernetes operator (section 7) and `liftoff` (section 8) have no
`integrations/` page; `liftoff` lives under `product/migrating-to-spacelift/`
and no other report owns it.

## 2. Plugins

### 2.1 Model

A plugin is an installed instance of a plugin template. Installing one creates
Spacelift resources (contexts, policies and named webhooks) from the template's
manifest; those resources reach stacks only through an `autoattach` label, the
plugin's *stack label* [D] [plugins]. Plugins appeared on SaaS on 2025-11-13
[RN] and in self-hosted v3.8.0 [RN] [sh-changelog].

| Concept | Meaning | Source |
|---|---|---|
| Plugin template | A YAML manifest plus metadata; global templates come from Spacelift, custom ones are created by users | [G] `PluginTemplate.isGlobal`; [D] [plugins] |
| Plugin | An installation of a template into a space, with parameter values and a stack label | [G] `PluginInstallInput` |
| Stack label | The `autoattach` label value; `*` attaches to every stack in the space | [D] [plugins] |
| Managed resource | A context, policy or named webhook created by the installation; read-only in the UI | [D] [plugins]; [G] `Context.plugin`, `Policy.plugin`, `NamedWebhooksIntegration.plugin` |
| Plugin output | Markdown a plugin uploads during a run; shown on the run's **Plugin Outputs** tab | [D] [plugins]; [S] `send_markdown` in [plugin.py](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/plugin.py#L268) |

### 2.2 Management lifecycle

| Step | Behaviour | Source |
|---|---|---|
| Install | Choose a template, an installation name, a stack label, a space and optional labels, then fill the template's parameters; creates the managed resources | [D] [plugins] |
| Attach | Only by adding the stack label to a stack; no direct attachment exists | [D] [plugins] |
| Change the template | **New template from this** copies a template for editing; existing installations keep the original | [D] [plugins] |
| Update | Installations are immutable: configuration and managed resources cannot be edited; uninstall and reinstall instead | [D] [plugins] |
| Uninstall | Deletes the installation and every managed resource permanently; stacks lose them from the next run | [D] [plugins] |

The schema is looser than the docs: `pluginUpdate` changes `name`,
`description` and `labels` of an installation [G]. The docs' immutability
covers parameters, template and managed resources, which `PluginUpdateInput`
cannot reach [G]. The Terraform provider treats every `spacelift_plugin`
attribute as `ForceNew`, so any change replaces the installation [S]
[resource_plugin.go](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_plugin.go#L30-L83).

### 2.3 GraphQL types

`Plugin` [G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Identifier |
| `name` | `String!` | Installation name |
| `description` | `String` | Description |
| `slug` | `String!` | URL-safe slug |
| `spaceDetails` | `Space!` | Owning space |
| `pluginTemplateDetails` | `PluginTemplate` | The template it was installed from |
| `manifest` | `String!` | The manifest of the installation |
| `labels` | `[String!]!` | User labels |
| `labelIdentifier` | `String!` | The stack label used for `autoattach` |
| `createdAt`, `updatedAt` | `Int!` | Unix seconds |

`PluginTemplate` [G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Identifier |
| `isGlobal` | `Boolean!` | Provided by Spacelift; cannot be removed |
| `name`, `description`, `slug` | `String!`, `String`, `String!` | Naming |
| `manifest` | `String!` | The YAML manifest |
| `parameters` | `[PluginTemplateParameter!]!` | Parameters parsed from the manifest |
| `labels` | `[String!]!` | User labels |
| `createdAt`, `updatedAt` | `Int!` | Unix seconds |

`PluginTemplateParameter` [G]: `id` (`String!`), `name` (`String!`), `type`
(`PluginTemplateParameterType!`), `description` (`String`), `sensitive`
(`Boolean!`), `required` (`Boolean!`), `default` (`String`).

`PluginTemplateParameterType` [G]: `NONE`, `STRING`, `BOOLEAN`, `NUMBER`. The
manifest schema allows only `string`, `number` and `boolean` [S]
[schema.json](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/schema.json#L134-L141);
when `NONE` is produced is *(unverified)*.

Inputs [G]:

| Input | Fields |
|---|---|
| `PluginInstallInput` | `name: String!`, `parameters: [PluginInstallParameterInput!]`, `pluginTemplateID: ID!`, `labels: [String!]`, `space: ID!`, `labelIdentifier: String!` |
| `PluginInstallParameterInput` | `id: String!`, `value: String!`; every value travels as a string |
| `PluginTemplateCreateInput` | `name: String!`, `description: String`, `manifest: String!`, `labels: [String!]` |
| `PluginUpdateInput` | `name: String!`, `description: String`, `labels: [String!]` |

Run fields [G]: `Run.pluginLogs` returns `[PluginLogsResponse!]!`
(`pluginName: String!`, `message: LogMessage!`), and
`Run.pluginLogsDownloadURL(pluginName: String!)` returns `LogsDownloadURL`.
Audit trail resource types `PLUGIN` and `PLUGIN_TEMPLATE` carry
`AuditTrailPluginMetadata` and `AuditTrailPluginTemplateMetadata` (`id`,
`name`) [G]; report 19 owns the audit trail.

### 2.4 Operations

| GraphQL field | Kind | REST projection | Notes |
|---|---|---|---|
| `pluginTemplates` | Query | `GET /plugin-templates` | `[PluginTemplate!]!` [G] |
| `pluginTemplate(id)` | Query | `GET /plugin-templates/{pluginTemplateId}` | [G] |
| `pluginTemplateCreate(input)` | Mutation | `POST /plugin-templates` | [G] |
| `pluginTemplateDelete(id)` | Mutation | `DELETE /plugin-templates/{pluginTemplateId}` | Global templates cannot be removed [G] |
| `plugins` | Query | `GET /plugins` | [G] |
| `plugin(id)` | Query | `GET /plugins/{pluginId}` | [G] |
| `pluginInstall(input)` | Mutation | `POST /plugins` | [G] |
| `pluginUpdate(id, input)` | Mutation | `PUT /plugins/{pluginId}` | [G] |
| `pluginDelete(id)` | Mutation | `DELETE /plugins/{pluginId}` | Removes managed resources [D] [plugins] |
| `Run.pluginLogsDownloadURL` | Field | `GET /runs/{runId}/plugin-logs-download-url` | [G]; the REST path is from the committed [OpenAPI projection](../../../api/reference/spacelift/spacelift.openapi.yaml) |

No `Action` enum value names plugins [G]; which role may install or delete
them is *(unverified)*.

### 2.5 Terraform provider

| Resource or data source | Attributes | Behaviour |
|---|---|---|
| `spacelift_plugin` | `name`, `plugin_template_id`, `stack_label` (required); `parameters` (map, sensitive), `labels`, `space_id` (optional); `id` | All attributes `ForceNew`; `space_id` defaults to `root`; unknown parameter IDs, and missing required parameters without a default, fail before the call; template defaults fill omitted parameters; import by ID [S] [resource_plugin.go](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_plugin.go#L88-L184) |
| `spacelift_plugin_template` | `name`, `manifest` (required); `description`, `labels`; computed `is_global`, `parameters`, `created_at`, `updated_at` | Calls `pluginTemplateCreate`, `pluginTemplate`, `pluginTemplateDelete` [S] [resource_plugin_template.go](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_plugin_template.go#L131) |
| data `spacelift_plugin`, `spacelift_plugin_template` | Look up by ID | `plugin(id)`, `pluginTemplate(id)` [S] [data_plugin_template.go](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/data_plugin_template.go#L116) |

The docs install global templates by slug-like IDs such as `opentofu-tracing`
through the data source [D] [plugins].

### 2.6 The manifest

A manifest is YAML validated by the JSON Schema in the plugins repository, which
the repository also exports to Go as `JSONSchema()` next to `Templates()`, the
embedded list of global manifests [S]
[templates.go](https://github.com/spacelift-io/plugins/blob/930c4509a789/templates.go#L12-L31).
That Spacelift's server loads its global templates through this Go package is
[I] from the package's shape and its `go.mod` module path.

Top level (`PluginManifest`) [S]
[schema.json](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/schema.json#L336-L433):

| Key | Type | Required | Meaning |
|---|---|---|---|
| `name` | string | yes | Plugin name |
| `version` | string | yes | Plugin version |
| `description` | string | yes | Description |
| `author` | string | yes | Author |
| `labels` | list of string | no | Labels |
| `parameters` | list of `Parameter` | no | Installation inputs; the docs require at least one [D] [plugins], the schema does not [S] |
| `contexts` | list of `Context` | no | Contexts to create |
| `webhooks` | list of `Webhook` | no | Named webhooks to create |
| `policies` | list of `Policy` | no | Policies to create |

Nested objects [S]
[schema.json](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/schema.json#L1-L339):

| Object | Keys (required in bold) | Constraints |
|---|---|---|
| `Parameter` | **`name`**, **`description`**, **`type`**, `sensitive` (default false), `required` (default false), `default` (string, integer, number, boolean or null), `id` | `type` is `string`, `number` or `boolean` |
| `Context` | **`name_prefix`**, **`description`**, `env`, `mounted_files`, `hooks`, `labels`, `priority` (integer, default 0) | `hooks` keys are the eleven phases below; values are lists of shell commands |
| `Variable` | **`key`**, `value`, `value_from_parameter`, `sensitive` (default false) | SDK validation needs `value` or `value_from_parameter` [S] [README](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/README.md) |
| `MountedFile` | **`path`**, **`content`**, `sensitive` (default false) | |
| `Policy` | **`name_prefix`**, **`type`**, **`body`**, `engine_type` (default `REGO_V1`), `labels` | `type` is `PUSH`, `PLAN`, `TRIGGER`, `APPROVAL` or `NOTIFICATION`; `engine_type` is `REGO_V0` or `REGO_V1` |
| `Webhook` | **`name_prefix`**, **`endpoint`**, `secretFromParameter`, `labels` | The secret comes from a parameter |

Hook phases a context may define [S]
[schema.json](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/schema.json#L50-L66):
`before_init`, `after_init`, `before_plan`, `after_plan`, `before_apply`,
`after_apply`, `before_perform`, `after_perform`, `before_destroy`,
`after_destroy`, `after_run`. These are the stack hook phases of report 08.

Every `name_prefix` is suffixed with a unique ID at installation [S]
[schema.json](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/schema.json#L4).
`value_from_parameter` and `secretFromParameter` reference a parameter. At
generation the SDK rewrites a variable's `value_from_parameter` from the
parameter's name to its `id` [S]
[generator.py](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/generator.py#L256-L282);
it applies no such rewrite to a webhook's `secretFromParameter`, which is
passed through as written [I] from the generator having no webhook mapping.
The server-side substitution of parameter values into variables and webhook
secrets is [I] from those keys; the SDK README also mentions a `${param.name}`
interpolation at install time, whose syntax no manifest in the repository uses
*(unverified)*.

How the managed resources pick up the stack label is not documented. That each
created context, policy and webhook carries `autoattach:<labelIdentifier>` is
[I] from the attach rule in the docs and report 13's auto-attach mechanism
*(unverified)*.

### 2.7 Execution in a run

`spaceforge` (Python, MIT, `requires-python >= 3.10`) turns a Python class
into a manifest; `python -m spaceforge generate` writes `plugin.yaml`, and
`python -m spaceforge run <hook>` runs one hook locally [S]
[spaceforge README](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/README.md).
The generated manifest makes a run do the following [S]
[generator.py](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/generator.py#L283-L335):

| Step | Mechanism |
|---|---|
| Working directory | `before_init` runs `mkdir -p /mnt/workspace/plugins/<plugin name, lower case, spaces as underscores>` [S] [L121-L125](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/generator.py#L121-L125) |
| Code delivery | The plugin's `plugin.py` and `requirements.txt` are mounted files of the first context |
| Binaries | Declared binaries download in `before_init` into `/mnt/workspace/plugins/plugin_binaries`, choosing the amd64 or arm64 URL by `arch`, unpacking `.tar.gz`, `.tar.bz2` and `.zip` [S] [binary_install.sh.j2](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/templates/binary_install.sh.j2) |
| Hook script | For each overridden Python hook, a mounted shell script creates a virtualenv with `--system-site-packages`, installs `spaceforge` and the requirements once, changes to `/mnt/workspace/source/$TF_VAR_spacelift_project_root`, and runs `python -m spaceforge run --plugin-file <file> <phase>` [S] [ensure_spaceforge_and_run.sh.j2](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/templates/ensure_spaceforge_and_run.sh.j2) |
| Context order | With several contexts, names get a five-letter prefix by `priority`: priority 0 gets `ZZZZZ`, higher priorities get letters nearer `A` [S] [L35-L68](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/generator.py#L35-L68). The schema's own description says priority 0 runs before priority 1 [S] [schema.json](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/schema.json#L4). Auto-attached contexts order by name (report 13), so the prefix is how a manifest controls ordering [I] |

The runner image must provide `python` with `venv` and `pip`; the standard
images install `spaceforge` with pip (report 10).

Run-time helpers on `SpaceforgePlugin` [S]
[plugin.py](https://github.com/spacelift-io/plugins/blob/930c4509a789/spaceforge/plugin.py#L33-L378):

| Helper | What it uses |
|---|---|
| `query_api` | `TF_VAR_spacelift_graphql_endpoint` and `SPACELIFT_API_TOKEN`; disabled unless the endpoint starts with `https://` |
| `use_user_token(id, secret)` | Exchanges an API key for a JWT through `apiKeyUser` |
| `get_plan_json`, `get_state_before_json` | `spacelift.plan.json` and `spacelift.state.before.json` in the working directory |
| `send_markdown` | `POST` `{"plugin_name": ...}` to the GraphQL endpoint with `/graphql` replaced by `/worker/plugin_logs_url`, then `PUT` the markdown (`text/markdown`) to the returned signed URL |
| `add_to_policy_input(name, data)` | Writes `<name>.custom.spacelift.json`, which plan policies read as `input.third_party_metadata.custom.<name>` (report 09) |
| Run detection | `TF_VAR_spacelift_run_id`; absent means local mode, where uploads only print |

### 2.8 Plugin catalogue at `930c4509a789`

Generated by script from every `plugins/*/plugin.yaml` [S]
[plugins/](https://github.com/spacelift-io/plugins/tree/930c4509a789/plugins):

| Directory | `name` | `version` | Parameters (`id`: type, required, sensitive) | Context hooks | Policies | Mounted files |
|---|---|---|---|---|---|---|
| `aws_sam` | AWS SAM | 1.0.2 | `sam_s3_prefix`: string; `sam_additional_args`: string | `before_init` | none | 3 |
| `checkov` | Checkov | 1.0.6 | `checkov_additional_args`: string | `after_plan`, `before_init` | `PLAN` (REGO_V0) | 3 |
| `coverage` | Module Test Coverage | 1.0.2 | `mode`: string; `min_variable_coverage`: number; `min_branch_coverage`: number; `fail_on`: string; `base_ref`: string | `after_init`, `before_init` | `PLAN` (REGO_V1) | 3 |
| `enviroment_manager` | Environment Manager | 1.0.5 | `spacelift_api_key_id`: string, required; `spacelift_api_key_secret`: string, required, sensitive | `before_init` | none | 4 |
| `envsubst` | Envsubst | 1.0.3 | none | `before_init` (two contexts) | none | 2 |
| `infracost` | Infracost | 1.0.3 | `infracost_api_key`: string, required, sensitive | `after_plan`, `before_init` | `PLAN` (REGO_V0) | 1 |
| `opentofu-tracing` | OpenTofu Tracing | 2.0.4 | `output_file`: string; `max_traces`: number; `max_memory_mb`: number; `summary_mode`: boolean | `after_apply`, `after_plan`, `before_apply`, `before_init`, `before_plan` | none | 6 |
| `sops` | Sops | 1.3.2 | `sops_config_path`: string | `before_init` | none | 4 |
| `ssm_parameter_store` | SSM Parameter Store | 1.0.2 | `aws_region`: string | `after_run`, `before_apply`, `before_destroy`, `before_init`, `before_plan`; second context `before_apply`, `before_init`, `before_perform`, `before_plan` | none | 7 |
| `terrascan` | Terrascan | 1.0.6 | `terrascan_additional_args`: string | `after_plan`, `before_init` | `PLAN` (REGO_V0) | 3 |
| `trivy` | Trivy | 1.0.7 | `trivy_additional_args`: string | `after_plan`, `before_init` | `PLAN` (REGO_V0) | 3 |
| `trufflehog` | Trufflehog | 1.0.4 | `results_filter`: string; `additional_args`: string | `before_init`, `before_plan` | none | 3 |
| `wiz` | Wiz | 3.0.3 | `default_scan_name`, `default_policies`: string; `wiz_client_id`: string, required; `wiz_client_secret`: string, required, sensitive; `wiz_additional_args`: string; `wizcli_autofail`, `is_govcloud`, `is_fedramp`: boolean | `after_plan`, `before_init` | `PLAN` (REGO_V0) | 3 |

No manifest defines a webhook. The docs and the 2025-11-13 changelog list ten
plugins [D] [plugins] [RN]; the repository has three more, `aws_sam`,
`coverage` and `ssm_parameter_store` [S]. Whether those three are global
templates on SaaS is *(unverified)*.

The Infracost plugin shows the typical shape: an `after_plan` hook runs
`infracost breakdown` into `infracost.custom.spacelift.json`, and a `PLAN`
policy reads `input.third_party_metadata.infracost` [S]
[infracost/plugin.yaml](https://github.com/spacelift-io/plugins/blob/930c4509a789/plugins/infracost/plugin.yaml).
Report 20 covers the built-in Infracost integration.

## 3. External integrations and ServiceNow

### 3.1 The external integration object

`ExternalIntegration` is a typed, space-scoped integration with its own API key
[G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Identifier |
| `deleted` | `Boolean!` | Soft-delete flag |
| `name` | `String!` | Name |
| `description` | `String` | Description |
| `type` | `String!` | Integration type, a free string |
| `createdAt` | `Int!` | Unix seconds |
| `spaceDetails` | `Space!` | Owning space; decides which blueprints may attach |
| `apiKey` | `ApiKey!` | The API key the integration uses |
| `customConfig` | `ExternalIntegrationConfig!` | Union; the only member is `ExternalIntegrationConfigServiceNow` (`baseURL`, `username`) |
| `attachedBlueprints` | `[BlueprintIntegrationAttachment!]!` | Blueprints it serves |

`ExternalIntegrationInput` [G]: `space: ID!`, `name: String!`,
`description: String`, `type: String!`, `customConfigData:
IntegrationCustomConfigData!`, whose only key is `serviceNow:
ServiceNowExtraDataInput` (`username`, `password`, `baseURL`, all
`String!`). The password is write-only: the output type has no password field
[G].

`BlueprintIntegrationAttachment` [G]: `id`, `integrationID`, `blueprintID`,
`blueprintName`, `integrationName`, `deleted`, `blueprintSpaceDetails`.
`Blueprint.externalIntegrations` lists them [G]. Templates (report 17) have no
integration field in the schema [G], so a ServiceNow catalogue item is bound to
a blueprint only.

| Operation | Kind | REST projection |
|---|---|---|
| `externalIntegrations` | Query | `GET /external-integrations` |
| `externalIntegration(id)` | Query | `GET /external-integrations/{externalIntegrationId}` |
| `attachableExternalIntegrationInSpace(id)` | Query | `GET /attachable-external-integration-in-space` |
| `externalIntegrationCreate(input)` | Mutation | `POST /external-integrations` |
| `externalIntegrationDelete(id)` | Mutation | `DELETE /external-integrations/{externalIntegrationId}` |
| `blueprintAttachToIntegration(id, input: BlueprintAttachInput!)` (input field `externalIntegration: ID!`), returns `BlueprintIntegrationAttachment!` | Mutation | `POST /blueprints/{blueprintId}/attach-to-integration` |
| `blueprintDetachIntegration(id)`, returns `BlueprintIntegrationAttachment!` | Mutation | `POST /blueprints/{blueprintId}/detach-integration` |

Sources: [G]; REST paths from the committed
[OpenAPI projection](../../../api/reference/spacelift/spacelift.openapi.yaml),
inventoried in report 04. The audit trail resource type `EXTERNAL_INTEGRATION`
carries `AuditTrailExternalIntegrationMetadata` (`id`, `name`) [G]. Billing
features `SERVICE_NOW_INTEGRATION` and `BACKSTAGE_INTEGRATION` gate the two
integrations [G]; report 26 maps tiers. The root query `integrationsCount`
returns `IntegrationsCount`, which counts `serviceNow` and `backstage`
integrations alongside the cloud, VCS, Slack, webhook and AI ones [G].

### 3.2 ServiceNow

ServiceNow is an Enterprise feature on SaaS [D] [servicenow]; the self-hosted
page carries no plan note and the feature arrived there in v2.6.0 as a beta
[RN] [sh-changelog].

| Step | Behaviour | Source |
|---|---|---|
| ServiceNow user | A dedicated user with roles `web_service_admin` (REST Messages), `business_rule_admin` (Business Rules) and `catalog_admin` (Catalog Items) | [D] [servicenow] |
| Create the integration | Name, space, optional description, base URL (`https://<id>.service-now.com`), username and password | [D] [servicenow] |
| On creation | Spacelift creates an outbound **REST Message** in ServiceNow holding the authorisation it uses to call Spacelift | [D] [servicenow] |
| Attach to a blueprint | From the blueprint's **Integrations** tab | [D] [servicenow] |
| On attachment | Spacelift creates a **Service Catalog Item** with one variable per blueprint input, and a **Business Rule** whose script maps the variables to blueprint inputs and calls the Spacelift API to create a stack from the blueprint | [D] [servicenow] |
| Ordering | Ordering the catalogue item creates a stack from the blueprint | [D] [servicenow] |
| Removal | Detach from every blueprint, then delete; Spacelift tries to delete the ServiceNow objects it created | [D] [servicenow] |

The objects created in ServiceNow may be edited by the customer afterwards [D]
[servicenow]. That the Business Rule calls `blueprintCreateStack` with the
integration's API key is [I] from `ExternalIntegration.apiKey` and report 17's
stack-from-blueprint operation.

## 4. Backstage

### 4.1 Setup

| Step | Behaviour | Source |
|---|---|---|
| Integration key | **Integrations > Backstage > Set up integration**, pick a space, create; a file with the secret downloads | [D] [backstage] |
| Key semantics | An API key with the admin role on the chosen space; consumes no API key seat; issues short-lived tokens | [D] [backstage] |
| Install | `@spacelift-io/backstage-integration-backend` and `@spacelift-io/backstage-integration-frontend`, Backstage 1.17.0 or later | [D] [backstage]; [S] [backend README](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-backend/README.md) |
| Configure | `spacelift.hostUrl` (no scheme), `spacelift.apiKey`, `spacelift.apiSecret` (secret), `spacelift.readOnly` (default `false`) in `app-config.yaml` | [S] [config.d.ts](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-backend/config.d.ts) |
| Remove | Delete the integration key; the packages can then be removed | [D] [backstage] |

No schema type or `APIKeyType` value marks an integration key: `APIKeyType` is
`SECRET` or `OIDC` [G]. `IntegrationsCount.backstage` shows Spacelift counts
Backstage integrations as records [G], and `ExternalIntegration` carries its
own `apiKey`, but its `customConfig` union has no Backstage member [G]. How the
key is created through the API, and how "no seat" is represented, is
*(unverified)*.

The plugins add no user-level authorisation; Backstage permissions must guard
the page, and Spacelift access is whatever the key's space grants [D]
[backstage].

### 4.2 What the plugins call

| Layer | Behaviour | Source |
|---|---|---|
| Backend plugin ID | `spacelift-io` | [S] [plugin.ts](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-backend/src/plugin.ts#L14) |
| Backend routes | `GET /health`; `GET /stacks`; `POST /stacks/:stackId/trigger`, which answers `403` in read-only mode | [S] [router.ts](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-backend/src/router.ts#L41-L73) |
| Authentication | `apiKeyUser(id, secret)` at `https://<hostUrl>/graphql`; the JWT is cached for one minute | [S] [Spacelift.service.ts](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-backend/src/services/Spacelift/Spacelift.service.ts#L27-L47) |
| Stack list | `stacks { id name description labels state branch spaceDetails { id name } }` | [S] [L66-L79](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-backend/src/services/Spacelift/Spacelift.service.ts#L66-L79) |
| Trigger | `runTrigger(stack)` returning `id` and `state` | [S] [L99-L105](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-backend/src/services/Spacelift/Spacelift.service.ts#L99-L105) |
| Error mapping | A GraphQL client error returns its HTTP status and first error message | [S] [router.ts](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-backend/src/router.ts#L8-L26) |
| Frontend | Page `SpaceliftIoPage`, polled every 10 s; the trigger button shows for stack states `DISCARDED`, `FAILED`, `STOPPED`, `FINISHED` and `NONE` | [S] [constants.ts](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-frontend/src/constants.ts) |

The backend validates sixteen stack states with zod: `APPLYING`, `CONFIRMED`,
`DESTROYING`, `DISCARDED`, `FAILED`, `FINISHED`, `INITIALIZING`, `NONE`,
`PLANNING`, `PREPARING`, `PREPARING_APPLY`, `PREPARING_REPLAN`, `READY`,
`REPLAN_REQUESTED`, `STOPPED`, `UNCONFIRMED` [S]
[types/index.ts](https://github.com/spacelift-io/backstage-plugins/blob/27d06658468a/packages/spacelift-io-backend/src/types/index.ts#L8-L25).
These are exactly the sixteen `StackState` values at `b3255fe38f5e` [G]
(compared by script), so validation fails only if Spacelift adds a stack state
later [I] from the closed enum.

## 5. Teleport

Teleport's Terraform provider can authenticate from a Spacelift run with
Teleport's `spacelift` join method, using an ID token Spacelift signs, so no
long-lived secret is stored [D] [teleport]. Report 12 owns Spacelift's OIDC
token.

| Item | Value | Source |
|---|---|---|
| Spacelift requirement | A paid account; the `spacelift` join method needs it | [D] [teleport] |
| Teleport requirement | Enterprise Teleport; `tctl` and `tsh` 17.3.4 or later | [D] [teleport] |
| Join token | `kind: token`, `roles: [Bot]`, `join_method: spacelift`, `bot_name`, and `spacelift.hostname` (the tenant host) | [D] [teleport] |
| Allow rule keys | `space_id`, `caller_type` (`stack` or `module`), `caller_id` (the stack or module ID) | [D] [teleport] |
| Bot | `tctl bots add <name> --roles=terraform-provider --token=<token>` | [D] [teleport] |
| Provider block | `join_method = "spacelift"`, `join_token = "<token>"`, `addr` of the Teleport proxy | [D] [teleport] |

Spacelift has no Teleport-specific object; the integration is entirely
Teleport-side configuration that trusts Spacelift's token claims [I] from the
absence of any Teleport type in the schema [G].

## 6. Docker: runner image rules

Report 10 tabulates the runner images, their flavours, allowed public-pool
registries, `SPACELIFT_DOCKER_CONFIG_DIR` and the account default images. The
Docker page adds these rules [D] [docker]:

| Rule | Detail |
|---|---|
| Isolation | Every job runs in a new container |
| No pinned versions | Runner images are deliberately unversioned; previews use a `future` tag |
| Binary mounting | Neither standard image bundles Terraform or OpenTofu; the chosen version is downloaded and mounted at run time |
| Architecture | Any AMD64 image works as a custom runner image when the Spacelift provider is not used with Terraform 0.12 |
| Required user | `spacelift` with UID 1983 |
| Required command | `ps`, used by the worker to poll the container |
| Entrypoint | `ENTRYPOINT` and `CMD` are ignored; the worker binary must be the root process; shell customisation goes in a `before_init` hook |
| Custom providers, Terraform 0.12 and below | Binaries next to the Terraform binary (for example `/bin/terraform-provider-<name>`) are used |
| Custom providers, Terraform 0.13 and later | Binaries under `/plugins/<host>/<namespace>/<name>/<version>/linux_amd64/`, matched by a `required_providers` source of the same address |
| Private images | Only on private workers, after `docker login` and with `SPACELIFT_DOCKER_CONFIG_DIR`; public workers refuse them so cached images cannot leak between customers |
| ECR | The launcher mints an ECR token from the host's credentials on every job; no `docker login` is needed |

## 7. Kubernetes operator

`spacelift-io/spacelift-operator` (MIT) is archived; its README says it gained
no traction and remains only as an example [S]
[README](https://github.com/spacelift-io/spacelift-operator/blob/341692ad7d56/README.md).
Its last release is v0.2.0 (2024-11-14) and its last push 2025-06-11 [C]
(GitHub API, read 2026-10-28). Spacelift's live Kubernetes path is the worker
pool controller (report 10), and `WorkerPool.managedByK8sController` marks
pools it owns [G].

The operator's API group is `app.spacelift.io`, version `v1beta1`, with five
custom resources [S]
[api/v1beta1](https://github.com/spacelift-io/spacelift-operator/tree/341692ad7d56/api/v1beta1):

| Kind | Spec | Status | Constraints |
|---|---|---|---|
| `Stack` | `repository` (required), `commitSHA`, `name`, `spaceName` or `spaceId`, hook lists for the eleven phases, `autodeploy`, `autoretry`, `administrative`, `branch`, `labels`, `projectRoot`, `provider`, `runnerImage`, `terraformVersion`, `vcsIntegrationId`, `workerPool`, `vendorConfig`, `awsIntegration`, `managesStateFile` and other stack settings | `id` only | Exactly one of `spaceName` and `spaceId` [S] [stack_types.go](https://github.com/spacelift-io/spacelift-operator/blob/341692ad7d56/api/v1beta1/stack_types.go#L26-L67) |
| `Run` | `stackName` (required), `createSecretFromStackOutput` | `state`, `id`, `stackId` | Terminal states `CANCELED`, `FAILED`, `FINISHED`, `DISCARDED`, `STOPPED`, `SKIPPED`; also `QUEUED` and `UNCONFIRMED` [S] [run_types.go](https://github.com/spacelift-io/spacelift-operator/blob/341692ad7d56/api/v1beta1/run_types.go#L26-L61) |
| `Space` | `parentSpace` (required), `name`, `description`, `inheritEntities`, `labels` | `id` | [S] [space_types.go](https://github.com/spacelift-io/spacelift-operator/blob/341692ad7d56/api/v1beta1/space_types.go#L26-L60) |
| `Context` | `name`, `spaceId` or `spaceName`, `description`, `labels`, `attachments` (`stackId`, `stackName` or `moduleId`, with `priority`), `hooks`, `environment`, `mountedFiles` | `id` | Each variable or file takes `value` or `valueFromSecret` (a Kubernetes secret key), not both [S] [context_types.go](https://github.com/spacelift-io/spacelift-operator/blob/341692ad7d56/api/v1beta1/context_types.go#L26-L92) |
| `Policy` | `name`, `body` (required), `type` (required), `description`, `labels`, `spaceName` or `spaceId`, `attachedStacksNames`, `attachedStacksIds` | `id` | `type` is one of `ACCESS`, `APPROVAL`, `GIT_PUSH`, `INITIALIZATION`, `LOGIN`, `PLAN`, `TASK`, `TRIGGER`, `NOTIFICATION` [S] [policy_types.go](https://github.com/spacelift-io/spacelift-operator/blob/341692ad7d56/api/v1beta1/policy_types.go#L26-L54) |

A login policy example in the docs grants a role to an API key session whose
login is `k8s-spacelift-operator` [D] [api-keys]. The name sits beside other
illustrative key names (`terraform-cloud-integration`, `ansible-automation`),
so it is an example a user chooses, not an identity the operator fixes [I]
from the example's context. Report 06 owns the operator as a client.

## 8. Migration tooling: `liftoff`

`liftoff` migrates an estate from Terraform Cloud or Terraform Enterprise into
Spacelift [D] [liftoff]. It lives in `spacelift-solutions/liftoff`, which states
no licence [C] (GitHub API, read 2026-10-28), so it is cited and never quoted.
Its latest release at the baseline is v1.5.1, 2026-10-06 [C].

### 8.1 Shape

| Aspect | Behaviour | Source |
|---|---|---|
| Distribution | One static binary; Homebrew tap `spacelift-solutions/tap/liftoff`, an install script, or a Windows zip | [D] [liftoff-setup] |
| Workspace | `./.liftoff` at the project root, an encrypted local store plus `config.yaml`; commands walk up to find it | [D] [liftoff] |
| Prerequisites | A Spacelift API key with root-space admin, source credentials, a VCS integration reaching every repository, at least one worker, and a choice between the Spacelift-managed repository and the customer's git | [D] [liftoff-setup] |
| Sources | `terraform` (Terraform Cloud and Enterprise; `api_endpoint` defaults to `https://app.terraform.io`) | [D] [liftoff-setup] |
| Interfaces | CLI by hand, CLI driven by an AI agent (recommended), and `liftoff ui --beta` | [D] [liftoff] |
| Output formats | Styled text on a terminal, TOON when piped, JSON with `--output json` | [D] [liftoff] |
| Errors | One structured error object (`code`, `message`, `remediation`, optional `entity`, `next`, `details`; `schema_version: 2`), non-zero exit, never a prompt; codes are stable and grouped by the surface that reported them; `unset_error_code` always means a bug | [D] [liftoff] [liftoff-errors] |
| Agent rules | Agents read a page with `liftoff skills <topic>` and pass its proof token back; source-changing steps need an approval only a person in their own terminal can give | [D] [liftoff] |
| Model choice | The docs set a behavioural floor for an agent model, because a weak one produces a migration that only looks complete | [D] [liftoff-models] |
| Idempotence | Every step may be re-run safely | [D] [liftoff] |

### 8.2 Pipeline

Only `configure validate`, `discover` and `mutate` touch the source; everything
else reads the local store [D] [liftoff].

| Step | Command | Writes |
|---|---|---|
| 1 | `liftoff init` | The encrypted workspace |
| 2 | `liftoff sources` | Nothing |
| 3 | `liftoff configure --source <id> --set source.<key>=<value>` | `config.yaml` |
| 4 | `liftoff configure validate` | Nothing; proves both credentials |
| 5 | `liftoff discover` | The whole estate, read-only, into the store; resumable, keeps staging choices; organisations and projects become spaces, workspaces become stacks, and the Terraform version picks each stack's workflow tool [D] [liftoff-discover] |
| 6 | `liftoff batch stage <kind>:<id>...` or `--all` | Staging choices; staging a stack cascades to its spaces and attached contexts; `batch list` and `batch unstage` complete the set [D] [liftoff-batch] |
| optional | `liftoff transform workflow-tool`, `transform space mv`, `transform stack-labels` | Staged entities only |
| 7 | `liftoff audit [--repair]` | The store, only with `--repair`; findings have a severity (`warning`, `error`), a repairable flag and a remediation, and are recomputed on every run [D] [liftoff-audit] |
| 8 | `liftoff generate` | An OpenTofu module |
| 9 | `liftoff publish` | A Spacelift-managed repository and an admin stack |
| 10 | `liftoff mutate --allow-mutation <name>` | Captured secrets; the source is reverted |
| 11 | `liftoff finalize staged` | The batch is marked migrated |

`status`, `restore`, `ui --beta` and `skills <topic>` work at any point [D]
[liftoff]. `liftoff model` reads and writes single entities in the store:
`model set` is the only way to change a populated value, while `--repair`
only fills empty ones, and a later `discover` overwrites `model set` edits
[D] [liftoff-model]. `mutate` records a restore point before each source
change; an interrupted run leaves pending restore points that block the next
`mutate` until `liftoff restore` reverts them [D] [liftoff-restore]. The
browser UI runs the same commands, listens only on the local machine, treats
its URL as a credential and refuses agents [D] [liftoff-ui].

Terraform source configuration keys [D] [liftoff-setup]:
`api_endpoint`, `api_token` (an admin user token; team and organisation tokens
fail), `requests_per_second` (default 30), `workspace_concurrency` (default 8),
`treat_all_as_sensitive`, `vcs_integration_id`. Repair keys:
`vcs_integration_id`, `module_workflow_tool` (`TERRAFORM_FOSS`, `OPEN_TOFU` or
`CUSTOM`), `default_branch` (default `main`), `custom_runner_image`,
`worker_pool_id`, `repository_map`. Mutations: `secrets`, `context-secrets`,
`state`, `module-git-versions`.

### 8.3 What it produces in Spacelift

| Output | Behaviour | Source |
|---|---|---|
| Module tree | One directory per space mirroring the space tree; root `providers.tf` (Spacelift provider, `SPACELIFT_API_KEY_*`), `versions.tf` per module, `liftoff.tf` wiring, `space.tf` per created space, `moved.tf` after a remap, one `<kind>_<slug>.tf` per stack, module, context or provider; no backend block | [D] [liftoff-generate] |
| Annotations | Audit findings ignored for generation appear as greppable comments in the files | [D] [liftoff-generate] |
| Provider registry | Providers migrate as definitions only; published versions do not | [D] [liftoff-generate] |
| Publish | Commits to a Spacelift-managed repository `liftoff`, creates the admin stack `liftoff-admin` (provider `SPACELIFT`, own state, autodeploy off), binds it a space-admin role, and stops at the `UNCONFIRMED` plan | [D] [liftoff-publish] |
| Ownership | Both carry the label `liftoff:managed`; liftoff refuses to touch same-named entities without it | [D] [liftoff-publish] |
| Own git | The module under `.liftoff/data/generated/` may instead be pushed to the customer's repository and an admin stack created by hand | [D] [liftoff-byo] |
| Secrets | `finalize sensitive` writes captured values as write-only variables and mounted files; multi-line values become mounted files plus export hooks | [D] [liftoff-finalize] [liftoff-mutate] |
| State | `finalize state` uploads each captured state and imports it into its stack, locking briefly; five stacks at a time by default (`spacelift.push_concurrency`, 1 to 50) | [D] [liftoff-finalize] |
| Workflow tool | `transform workflow-tool` moves staged stacks and modules to OpenTofu at one chosen version | [D] [liftoff-transform] |
| Name to ID | Targets are resolved by deriving the Spacelift ID from the name: lower case, ASCII transliteration, punctuation and spaces collapsed to dashes | [D] [liftoff-finalize] |

The name-to-ID rule is a fact about Spacelift's own slug derivation, which an
API-compatible implementation must reproduce [I] from the docs saying
`liftoff` matches it.

## What this means for infraweave

- A plugin engine needs plugin templates (global and custom), installations
  with typed parameters, managed contexts, policies and named webhooks that are
  read-only to users and removed with the installation, and attachment by
  `autoattach` label only.
- The `spaceforge` manifest schema and the eleven hook phases are the contract
  for the global plugins; running them needs `python`, `venv` and `pip` in the
  runner image, mounted files under `/mnt/workspace/plugins`, and the run
  variables `TF_VAR_spacelift_run_id`, `TF_VAR_spacelift_graphql_endpoint`,
  `TF_VAR_spacelift_project_root` and `SPACELIFT_API_TOKEN`.
- Plugin outputs need a worker-facing endpoint that issues a signed upload URL
  per plugin (`/worker/plugin_logs_url` in Spacelift), plus run fields to list
  and download those logs.
- Custom policy input files (`*.custom.spacelift.json`) are the bridge from
  plugins to plan policies (report 09).
- External integrations need a typed config union with write-only secrets, an
  owning space, an API key, and attachment to blueprints; ServiceNow
  additionally creates and deletes objects in the customer's ServiceNow.
- Backstage support needs only `apiKeyUser`, a stack list and `runTrigger`
  behind a space-scoped key; the "integration key" without a seat is a product
  concept the plan must model or decline.
- Teleport's `spacelift` join method depends on the OIDC token's claims
  (`space_id`, caller type and caller ID) and the tenant host name (report 12).
- Custom runner images must work with the UID 1983 user, an ignored
  entrypoint, a `ps` binary and the Terraform 0.13 `/plugins` layout.
- The operator is archived and needs no parity; `liftoff`'s output is plain
  Spacelift provider code and API calls, so a compatible provider and API
  surface would accept it, including state import and write-only secrets.

## Inferred and undocumented

| Claim | Basis |
|---|---|
| Spacelift loads global plugin templates from the `spacelift-io/plugins` Go package | [I] from `templates.go` exporting embedded manifests and the JSON Schema |
| Managed plugin resources carry `autoattach:<labelIdentifier>` | [I] from the docs' attach rule; *(unverified)* |
| Parameter values substitute into `value_from_parameter` and `secretFromParameter` at install | [I] from the manifest keys; the `${param.name}` syntax in the SDK README is *(unverified)* |
| `PluginTemplateParameterType.NONE` | Not documented; *(unverified)* |
| Which role may manage plugins, templates and external integrations | No `Action` value names them; *(unverified)* |
| `aws_sam`, `coverage` and `ssm_parameter_store` are global templates on SaaS | In the repository, absent from the docs list; *(unverified)* |
| The integration key's API representation and seat exemption | Not in the schema; *(unverified)* |
| The ServiceNow Business Rule creates the stack with the integration's API key | [I] from `ExternalIntegration.apiKey` |
| A stack state added after `b3255fe38f5e` would fail the Backstage plugin's validation | [I] from the zod enum matching today's sixteen values |
| `k8s-spacelift-operator` in the login policy docs is an example key name, not a fixed operator identity | [I] from the surrounding examples |
| A webhook's `secretFromParameter` is not rewritten to a parameter ID by the SDK | [I] from `generator.py` mapping only variables |

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS schema | introspection `b3255fe38f5e` |
| Self-hosted | plugins from v3.8.0; ServiceNow beta from v2.6.0 [RN] |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| plugins (`spaceforge`) | `930c4509a789`; package version from setuptools-scm, Python 3.10 or later |
| backstage-plugins | `27d06658468a`; Backstage 1.17.0 or later |
| spacelift-operator | `341692ad7d56`, archived; last release v0.2.0 |
| liftoff | v1.5.1 (2026-10-06) |
| Teleport | `tctl` and `tsh` 17.3.4 or later, per the docs |

## Sources

- [D] <https://docs.spacelift.io/integrations/plugins>, read 2026-10-28.
- [D] <https://docs.spacelift.io/integrations/external-integrations/servicenow>, read 2026-10-28.
- [D] <https://docs.spacelift.io/integrations/external-integrations/backstage>, read 2026-10-28.
- [D] <https://docs.spacelift.io/integrations/teleport>, read 2026-10-28.
- [D] <https://docs.spacelift.io/integrations/docker>, read 2026-10-28.
- [D] <https://docs.spacelift.io/concepts/authorization/assigning-roles-api-keys>, read 2026-10-28.
- [D] <https://docs.spacelift.io/product/migrating-to-spacelift> and its subpages `setup`, `discover`, `batch`, `audit`, `model`, `models`, `generate`, `publish`, `publish-byo-git`, `mutate`, `finalize`, `transform`, `restore`, `errors`, `ui`, read 2026-10-28.
- [D] The self-hosted copies <https://docs.spacelift.io/self-hosted/latest/integrations/plugins>, <https://docs.spacelift.io/self-hosted/latest/integrations/external-integrations/servicenow>, <https://docs.spacelift.io/self-hosted/latest/integrations/external-integrations/backstage>, <https://docs.spacelift.io/self-hosted/latest/integrations/teleport> and <https://docs.spacelift.io/self-hosted/latest/integrations/docker>, read 2026-10-28.
- [RN] <https://docs.spacelift.io/product/changelog> (2025-11-13) and <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog> (v3.8.0, v2.6.0).
- [G] [`api/reference/spacelift/introspection.json`](../../../api/reference/spacelift/introspection.json), snapshot `b3255fe38f5e`; tables generated by script.
- [S] <https://github.com/spacelift-io/plugins/tree/930c4509a789>
- [S] <https://github.com/spacelift-io/backstage-plugins/tree/27d06658468a>
- [S] <https://github.com/spacelift-io/terraform-provider-spacelift/tree/c72b9bd2f3c3>
- [S] [C] <https://github.com/spacelift-io/spacelift-operator/tree/341692ad7d56>, read over the network 2026-10-28.
- [C] <https://github.com/spacelift-solutions/liftoff>, repository metadata and latest release read through the GitHub API 2026-10-28.
