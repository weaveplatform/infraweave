# 17: Blueprints and templates

Checked against: docs.spacelift.io read 2026-10-28 (SaaS and `self-hosted/latest`, local mirror), including the blueprint and template JSON schemas embedded in the blueprint and template configuration pages; introspection `b3255fe38f5e`; spacelift-api-bruno `7cad4574954b`; terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); spacectl `6bf167cb62a6`.

Research baseline: **2026-10-28**.

This report records Spacelift's two self-service stack factories. Blueprints
render a YAML body with user inputs into one or more ordinary stacks and then
forget them. Templates add versions, deployments that keep ownership of the
stacks they create, change history and rollback. The report covers both body
schemas key by key, the input types and validations, the CEL expression
engine and its context variables, the state machines of blueprints, template
versions and deployments, the operations and their deprecations, the evidence
that templates supersede the versioned-blueprint API, the export path from a
blueprint to a template, and the version catalog queries that list the tool
versions a body may name. infraweave's blueprint and template resources and
their REST routes are designed from it. The stack settings that a body sets
are defined in report 07; schedules and dependencies in report 18; RBAC
actions in report 14; the REST routes in report 04; the Terraform resources in
[05](05-terraform-provider.md); the ServiceNow integration that consumes
blueprints in report 25; tool version resolution in report 20.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, read 2026-10-28. The JSON schemas printed on the blueprint and template configuration pages are cited as [D] |
| **[G]** | The introspection snapshot `b3255fe38f5e` ([`introspection.json`](../../../api/reference/spacelift/introspection.json)) |
| **[B]** | spacelift-api-bruno at `7cad4574954b` ([`bruno.json`](../../../api/reference/spacelift/bruno.json)) |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog, <https://docs.spacelift.io/product/changelog>, or the self-hosted changelog where named |
| **[I]** | Inferred; the text says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

Short names for the docs pages cited below:

| Short name | URL |
|---|---|
| blueprint | <https://docs.spacelift.io/concepts/blueprint> |
| export | <https://docs.spacelift.io/concepts/blueprint/export-to-template> |
| template | <https://docs.spacelift.io/concepts/template> |
| tconfig | <https://docs.spacelift.io/concepts/template/configuration> |
| deployments | <https://docs.spacelift.io/concepts/template/deployments> |
| workbench | <https://docs.spacelift.io/concepts/template/workbench> |
| rbac | <https://docs.spacelift.io/concepts/authorization/rbac-system> |
| changelog | <https://docs.spacelift.io/product/changelog> |
| sh-changelog | <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog> |
| sh-blueprint | <https://docs.spacelift.io/self-hosted/latest/concepts/blueprint> |
| versions | <https://docs.spacelift.io/vendors/terraform/version-management> |
| servicenow | <https://docs.spacelift.io/integrations/external-integrations/servicenow> |
| flows-blocks | <https://docs.spacelift.io/flows/spacelift-deploy-integration/spacelift-blocks> |
| tofu-migration | <https://docs.spacelift.io/vendors/opentofu/migration> |

The tables of body keys in sections 5 and 8 were generated with `python3 -I`
from the JSON schemas printed on the blueprint and tconfig pages; the GraphQL
tables were generated from the introspection snapshot with the same tool and
then annotated.

## 1. Two features, one engine

| Aspect | Blueprint | Template |
|---|---|---|
| Purpose | Creates stacks once; the stacks then live on their own [D] blueprint | Creates stacks through a deployment that keeps managing them [D] template |
| Link after creation | `Stack.blueprint` points to the blueprint, but the blueprint has no further effect; changing it does not touch existing stacks [G] [D] blueprint | `Stack.templateDeployment` points to the deployment; the deployment owns the stacks [G] [D] template |
| Stack editability | Fully editable like any stack [D] blueprint | Read-only, even for admins; `Stack.isLockedByTemplateDeployment` is true while an active deployment manages it [G] [D] workbench |
| Versioning | None; one body per blueprint, cloned to change a published one [D] blueprint | A template holds many versions; each deployment pins one [D] workbench |
| VCS reference | Follows the latest commit of the branch [D] blueprint | A published version is pinned to the commit its reference pointed at when published [D] tconfig |
| Body schema | `blueprintSchema` [G] | `templateSchema` [G] |
| Stacks per body | `stack`, or `stacks` with at most 5 entries [D] blueprint schema | `stacks` only; docs say at most 10 [D] tconfig, the schema sets no limit [D] tconfig schema |
| Tier | Business and above on SaaS [D] blueprint | Business and above on SaaS [D] template |
| Tier flag | `BillingTierFeature.BLUEPRINTS` [G] | No template-named `BillingTierFeature` value exists; that templates are gated by `BLUEPRINTS` is [I] from the shared `BLUEPRINT` naming of the template RBAC actions |
| RBAC | No RBAC actions; space admin only [D] rbac | `*_BLUEPRINTS_VERSIONED_GROUP` and `*_BLUEPRINT_DEPLOYMENT` actions (section 9.7) [G] [D] rbac |
| Expression engine | CEL through `celplate` [D] blueprint | The same engine [D] tconfig |
| First release noted | Earlier than templates; the ServiceNow integration built on it shipped as a beta in self-hosted v2.6.0 (the section headed "Changes between v2.5.0 and v2.6.0") [RN] sh-changelog | SaaS changelog entry of 2026-02-18 [RN] changelog; self-hosted v5.0.0 [RN] sh-changelog |

The self-hosted pages for both features carry no Business-plan notice, and
the self-hosted blueprint page lists only AWS cloud integration attachments
where the SaaS page also names GCP and Azure [D] sh-blueprint.

## 2. Supersession of the versioned-blueprint API by templates

Templates replace the versioned-blueprint API, not plain blueprints. The
GraphQL schema kept a second, versioned blueprint family
(`BlueprintVersionedGroup`, `BlueprintDeployment`, `blueprintVersion*`) that
is the same model as templates under older names; that family is deprecated
and partly removed. The single-body blueprint operations carry no deprecation.

### 2.1 Evidence

| Date | Evidence | Source |
|---|---|---|
| 2026-02-18 | Templates announced as a self-service provisioning system with versioned configurations and lifecycle control over deployed stacks | [RN] changelog |
| 2026-07-16 | First published list of GraphQL deprecations. It deprecates `blueprintDeploymentCreate`, `blueprintDeploymentDelete`, `blueprintDeploymentRollback`, `blueprintDeploymentUpdate`, `blueprintVersionCreate`, `blueprintVersionDelete`, `blueprintVersionDeprecate`, `blueprintVersionParseTemplate`, `blueprintVersionUpdate`, `blueprintVersionedGroupCreate`, `blueprintVersionedGroupDelete`, `blueprintVersionedGroupUpdate` (mutations), `blueprintDeployment`, `blueprintV2Schema`, `blueprintVersionedGroup`, `searchBlueprintDeployments`, `searchBlueprintVersionedGroups`, `searchBlueprintVersionedGroupsSuggestions` (queries), `Stack.blueprintDeployment`, the `templateVersionParseTemplate` mutation (in favour of the query of the same name), the `sensitive` field of `BlueprintDeploymentCreateInputPair`, `BlueprintStackCreation.stackID` and `runID`, and the `Action` values `UPDATE_BLUEPRINT_DEPLOYMENT` and `UPDATE_BLUEPRINT_DEPLOYMENT_INPUTS` | [RN] changelog |
| 2026-10-20 | Removed: `blueprintDeploymentRollback`, `blueprintVersionDeprecate` (mutations) and `blueprintV2Schema` (query) | [RN] changelog |
| Snapshot | Every output field and enum value in the 2026-07-16 list that was not removed still carries `isDeprecated`; the 15 blueprint root operations, `Stack.blueprintDeployment` and the `templateVersionParseTemplate` mutation name their template replacement, the two `Action` values name `UPGRADE_BLUEPRINT_DEPLOYMENT`, and `BlueprintStackCreation.stackID`/`runID` name `stackIds`/`runIds`. The three removed fields are absent | [G] |
| Snapshot | No deprecation on `blueprint`, `blueprintCreate`, `blueprintUpdate`, `blueprintDelete`, `blueprintClone`, `blueprintCreateStack`, `blueprintParseTemplate`, `blueprintSchema`, `searchBlueprints`, `searchBlueprintsSuggestions`, `blueprintWithGroupCreate`, `blueprintWithGroupUpdate`, `blueprintWithGroupDelete`, `blueprintAttachToIntegration`, `blueprintDetachIntegration` | [G] |
| Docs | The RBAC page calls blueprints "legacy" and gives them no RBAC actions | [D] rbac |
| Docs | The blueprint page points readers who need ongoing lifecycle management to templates; the export page calls templates the newer format and offers a one-way conversion | [D] blueprint; [D] export |
| Bruno | The Blueprints folder says blueprints are being superseded by templates and that new work belongs in the Templates folder | [B] |
| Coverage | The reference's REST projection excludes the 15 deprecated blueprint root fields (10 mutations, 5 queries) and the deprecated `templateVersionParseTemplate` mutation | [`coverage.md`](../../../api/reference/spacelift/coverage.md) |

The snapshot records no deprecation flags on input fields at all (none of its
input fields carries `isDeprecated`), so the deprecation of
`BlueprintDeploymentCreateInputPair.sensitive` rests on the changelog alone
[G] [RN]. `TemplateDeploymentCreateInputPair` has no `sensitive` field; the
input's declared type decides secrecy [G] [RN] changelog.

### 2.2 Name mapping

| Versioned-blueprint name | Template name |
|---|---|
| `BlueprintVersionedGroup`, `blueprintVersionedGroup`, `blueprintVersionedGroupCreate/Update/Delete` | `Template`, `template`, `templateCreate/Update/Delete` |
| `Blueprint` used as a version, `blueprintVersionCreate/Update/Delete` | `TemplateVersion`, `templateVersionCreate/Update/Delete` |
| `blueprintVersionDeprecate` (removed) | `templateVersionDeprecate` |
| `blueprintVersionParseTemplate` (mutation) | `templateVersionParseTemplate` (query) |
| `BlueprintDeployment`, `blueprintDeployment`, `blueprintDeploymentCreate/Update/Delete` | `TemplateDeployment`, `templateDeployment`, `templateDeploymentCreate/Update/Delete` |
| `blueprintDeploymentRollback` (removed) | `templateDeploymentRollback` |
| `blueprintV2Schema` (removed) | `templateSchema` |
| `searchBlueprintDeployments`, `searchBlueprintVersionedGroups(Suggestions)` | `searchTemplateDeployments`, `searchTemplates(Suggestions)` |
| `Stack.blueprintDeployment` | `Stack.templateDeployment` |
| `BlueprintVersionedGroup.blueprintVersion(id)`, `.searchBlueprintVersions(Suggestions)`, `.compatibleVersions` | `Template.templateVersion(id)`, `.searchTemplateVersions(Suggestions)`, `.compatibleVersions` |
| `BlueprintVersionedGroup.latestVersion` (`BlueprintVersionSummary`, latest by creation time) | `Template.latestVersion` (`TemplateVersionSummary`, highest published non-deprecated version by semver) |
| `blueprintWithGroupCreate/Update/Delete` (not deprecated; create takes the group ID) | `templateVersionCreate/Update/Delete` |
| `BlueprintDeploymentChangeRecord`, `BlueprintDeployment.searchChangeHistory` | `TemplateDeploymentChangeRecord`, `TemplateDeployment.searchChangeHistory` |
| `BlueprintDeploymentCreateInputPair.sensitive` | none; the input's declared type decides secrecy |

Sources for the table: the deprecation reasons in [G] and the field-by-field
comparison of the two families' types in the snapshot [G]. The template side
adds what the versioned family never had: `templateVersionByNumber`,
`TemplateDeploymentCreateInput.deploymentType` (`TEST` deployments),
and `templateDeploymentDelete(skipStackDeletion)` [G].

Template-side types still reuse blueprint names: `TemplateVersion.state` is a
`BlueprintState`, `TemplateVersion.inputs` are `BlueprintInput`,
`templateVersionParseTemplate` returns `BlueprintParseResult`, and the RBAC
actions keep `BLUEPRINT` in their names [G] [D] rbac. The provider describes
`spacelift_template` as a "versioned blueprint" [S]
[resource_template.go](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template.go).

The `BlueprintWithGroup*` mutations and the `Blueprint` fields `version`,
`groupDetails`, `deprecatedAt`, `deprecationNote` and `deploymentsCount` belong
to the versioned family but carry no deprecation [G]. Whether they remain
supported is *(unverified)*.

### 2.3 What templates replace and what they leave alone

| Workflow | Blueprint surface | Template surface | Status |
|---|---|---|---|
| Author a versioned definition, publish, deprecate | `blueprintVersionedGroup*`, `blueprintVersion*`, `blueprintWithGroup*` | `template*`, `templateVersion*` | Versioned family deprecated, except `blueprintWithGroup*`, which carry no deprecation [G] [RN] |
| Deploy, upgrade, roll back and delete managed stacks | `blueprintDeployment*` | `templateDeployment*` | Versioned family deprecated or removed [G] [RN] |
| Create stacks once from a single-body blueprint | `blueprintCreate`, `blueprintCreateStack`, `blueprintClone` | none | Not deprecated; a template always creates its stacks under a deployment that keeps managing them [G] [D] blueprint [D] template |
| Attach a blueprint to ServiceNow | `blueprintAttachToIntegration`, `blueprintDetachIntegration` | none | Not deprecated; the ServiceNow integration is built on blueprints and its page names no template support [G] [D] servicenow |
| Move a blueprint body into the template model | `Blueprint.template` | `templateCreate` | One-way export (section 10) [D] export |

The docs present templates as the choice for ongoing lifecycle management and
blueprints as the choice for one-off creation [D] blueprint [D] template;
neither page announces a removal date for plain blueprints.

## 3. Entities and identifiers

| Entity | GraphQL type | Identity | Parent | Notes |
|---|---|---|---|---|
| Blueprint | `Blueprint` | `id` (slug), `ulid` | Space | One body, one state [G] |
| Template | `Template` | `id` (slug), `ulid` | Space | Metadata only; bodies live in versions [G] |
| Template version | `TemplateVersion` | `id` (slug), `ulid`; `version` is the free-form number | Template (`templateDetails`) | `templateVersion(id)` accepts slug or ULID [G] |
| Template deployment | `TemplateDeployment` | `id` (slug) | Template; addressed with `templateID` | `name` can change without changing the slug [G] |
| Change record | `TemplateDeploymentChangeRecord` | `id` (ULID) | Deployment | Read through `searchChangeHistory` [G] |
| Deployment stack | `TemplateDeploymentStack` | stack `id` | Deployment | Carries `state` (`StackState`) and `outputs` [G] |

The Terraform provider stores a deployment as `<template_id>/<deployment_id>`
and imports a version as `<template_id>-<version_ulid>` [S]
[resource_template_deployment.go#L323](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_deployment.go#L323)
[S] [resource_template_version.go#L232](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_version.go#L232).

## 4. Blueprints

### 4.1 States and transitions

`BlueprintState` [G]:

| Value | Meaning |
|---|---|
| `UNKNOWN` | Invalid zero value [G] |
| `DRAFT` | Being written; not validated; no stacks can be created from it [D] blueprint |
| `PUBLISHED` | Validated against the schema; stacks can be created from it [D] blueprint |
| `DELETED` | Deleted [G] |

| From | To | Trigger | Rule |
|---|---|---|---|
| (none) | `DRAFT` or `PUBLISHED` | `blueprintCreate` with `state` | `template` is required when publishing [S] [resource_blueprint.go#L67](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_blueprint.go#L67) |
| `DRAFT` | `DRAFT` | `blueprintUpdate` | Any body accepted; drafts are not validated [D] blueprint |
| `DRAFT` | `PUBLISHED` | `blueprintUpdate` with `state: PUBLISHED`, or the UI **Publish** button | Body validated; fields holding `${{ }}` expressions are validated only when stacks are created [D] blueprint |
| `PUBLISHED` | `DRAFT` | none | Not allowed; clone, edit and publish the clone instead [D] blueprint |
| any | `DELETED` | `blueprintDelete` | [G] |

`blueprintUpdate` takes the full `BlueprintCreateInput`, not a patch [G]. The
provider forces replacement when `template` changes and accepts only `DRAFT`
and `PUBLISHED` for `state` [S]
[resource_blueprint.go#L67-L80](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_blueprint.go#L67-L80).

### 4.2 Permissions and sharing

- Create, update and delete need admin on the blueprint's space; read needs
  read on that space [D] blueprint.
- Creating a stack from a published blueprint needs read on the blueprint's
  space and admin on the space where the stack goes [D] blueprint.
- A published blueprint has a **Share** link that opens only the input form,
  without the editor, for anyone with the permissions above [D] blueprint.
- The RBAC system defines no blueprint actions [D] rbac.

### 4.3 The `Blueprint` object

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Slug |
| `ulid` | `String!` | ULID |
| `space` | `Space!` | Owning space |
| `deleted` | `Boolean!` | Soft-deletion flag |
| `deprecatedAt`, `deprecationNote` | `Int`, `String` | Versioned-family deprecation marker |
| `name`, `description` | `String!`, `String` | |
| `instructions` | `String` | Per-version instructions (versioned family) |
| `rawTemplate` | `String!` | The YAML body |
| `state` | `BlueprintState!` | Section 4.1 |
| `inputs` | `[BlueprintInput!]!` | Parsed from the body |
| `labels` | `[String!]!` | |
| `stackKeys` | `[String!]!` | The `key` values in the body |
| `createdAt`, `updatedAt`, `publishedAt` | `Int!`, `Int!`, `Int` | Unix seconds |
| `externalIntegrations` | `[BlueprintIntegrationAttachment!]!` | External integrations (ServiceNow) attached to the blueprint (report 25) |
| `groupDetails` | `BlueprintVersionedGroup!` | Versioned family |
| `version` | `String` | Versioned family |
| `canDelete` | `Boolean!` | |
| `deploymentsCount` | `Int!` | Versioned family |
| `template` | `BlueprintTemplate!` | The body converted to the template schema, with `template`, `warnings` and `errors`; used by the export (section 10) |

`BlueprintInput` [G]: `id: ID!`, `name: String!`, `description: String!`,
`default: String!`, `type: BlueprintInputType!`, `options: [String!]!`,
`validations: BlueprintInputValidations`. Defaults and options are strings on
the wire whatever the input type.

### 4.4 Operations

| Operation | Kind | Arguments | Returns | Notes |
|---|---|---|---|---|
| `blueprint` | Q | `id` | `Blueprint` | [G] |
| `searchBlueprints`, `searchBlueprintsSuggestions` | Q | `SearchInput`, `SearchSuggestionsInput` | outputs | Search mechanics in report 21 [G] |
| `blueprintSchema` | Q | none | `String!` | The JSON schema as a string [G] [D] blueprint |
| `blueprintCreate` | M | `BlueprintCreateInput` (`space`, `name`, `state`, `description`, `labels`, `template`) | `Blueprint!` | [G] |
| `blueprintUpdate` | M | `id`, `BlueprintCreateInput` | `Blueprint!` | Full replacement [G] |
| `blueprintDelete` | M | `id` | `Blueprint` | [G] |
| `blueprintClone` | M | `id`, `BlueprintCloneInput` (`space`, `name`, `description`, `labels`) | `Blueprint!` | The route back to an editable copy of a published blueprint [G] [D] blueprint |
| `blueprintParseTemplate` | M | `template` | `BlueprintParseResult` (`inputs`, `errors`) | Validates a body and extracts its inputs [G] |
| `blueprintCreateStack` | M | `id`, `BlueprintStackCreateInput` (`templateInputs: [{id, value}]`) | `BlueprintStackCreation` (`stackIds`, `runIds`; `stackID`, `runID` deprecated) | Section 4.5 [G] |
| `blueprintAttachToIntegration`, `blueprintDetachIntegration` | M | `id`, `BlueprintAttachInput.externalIntegration` | `BlueprintIntegrationAttachment` | ServiceNow (report 25) [G] [D] servicenow |
| `blueprintWithGroupCreate/Update/Delete` | M | `BlueprintWithGroup*Input` | `Blueprint` | Versioned family; `version` must be set when publishing [G] |

### 4.5 Creating stacks from a blueprint

1. The caller sends one `{id, value}` pair per input; values are strings
   [G]. spacectl prompts per input type, or reads a JSON object keyed by
   input ID, rejects IDs the blueprint does not define, fills omitted inputs
   from their defaults and fails on any other omission [S]
   [deploy.go#L225-L268](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/blueprint/deploy.go#L225-L268).
2. The body is rendered with the inputs, `maps` and `context` (section 6),
   and the rendered YAML is fully validated, including fields that held
   expressions [D] blueprint.
3. Each stack in `stack` or `stacks` is created with its settings,
   attachments, environment, hooks, schedules and dependencies, unless the
   stack's `options.do_not_create` is true [D] blueprint.
4. If `options.trigger_run` is true, a tracked run starts after creation [D]
   blueprint. The response lists every created stack and every started run
   [G].

Dependencies inside one body name stack `key` values, not names; cycles and
self-dependencies are rejected; `depends_on` and
`stack_dependency_references` may be mixed and the reference wins when both
name the same stack [D] blueprint. Keys need not be unique but should be [D]
blueprint.

## 5. The blueprint body

The body is YAML validated against a draft-07 JSON schema served by
`blueprintSchema` and printed on the blueprint page [G] [D] blueprint. Every
object in it rejects unknown keys (`additionalProperties: false`) [D]
blueprint schema.

### 5.1 Top level

| Key | Type | Required | Constraints |
|---|---|---|---|
| `inputs` | list of input | no | Section 5.2 |
| `options` | object | no | `trigger_run: boolean`, `do_not_create: boolean` |
| `maps` | map of name to map of key to string, number or boolean | no | Section 6.3 |
| `stack` | stack | no (schema) | The docs offer `stacks` as the alternative to `stack` [D] blueprint |
| `stacks` | list of stack | no (schema) | At most 5 items; each must have `key` |

The blueprint schema requires neither `stack` nor `stacks` and does not forbid
both [D] blueprint schema; whether the service rejects a body with neither or
with both is *(unverified)*.

### 5.2 Inputs and input types

An input has `id` and `name` (always required), `description`, `type`,
`default` (string, number or boolean), `validations` and `options` [D]
blueprint schema. The schema has four input shapes:

| YAML `type` | GraphQL `BlueprintInputType` | Shape | Extra keys | Validations |
|---|---|---|---|---|
| omitted or `short_text` | `SHORT_TEXT` | text; `type` optional, defaults to `short_text` | none | string |
| `long_text` | `LONG_TEXT` | text, shown as a text area | none | string |
| `secret` | `SECRET` | text, masked in the UI | none | string |
| `number` | `NUMBER` | numeric; `type` required | none | number |
| `float` | `FLOAT` | numeric; `type` required | none | number |
| `boolean` | `BOOLEAN` | `type` required | none | none |
| `select` | `SELECT` | `type` and `options` required | `options`: list of strings, at least 1 | none in the schema |
| none | `AVAILABLE_SPACES` | Shows the list of spaces the user may use in the UI [G] | | |

`AVAILABLE_SPACES` exists only in the GraphQL enum; neither JSON schema
accepts it as a YAML `type`, so how a body declares it is *(unverified)*.
The template docs list `type` as required for every input [D] tconfig, while
the template schema, like the blueprint schema, lets the text shape omit it
[D] tconfig schema; the schema wins.

String validations (`short_text`, `long_text`, `secret`) [D] blueprint:

| Key | Type | Meaning |
|---|---|---|
| `required` | boolean | Must be filled |
| `min_length` | integer ≥ 0 | Minimum characters |
| `max_length` | integer ≥ 0 | Maximum characters |
| `length_equal` | integer ≥ 0 | Exact character count |
| `pattern` | string | Regular expression the value must match |

Number validations (`number`, `float`) [D] blueprint:

| Key | Type | Meaning |
|---|---|---|
| `required` | boolean | Must be filled |
| `greater_than`, `greater_than_or_equal` | number | Lower bound, exclusive or inclusive |
| `less_than`, `less_than_or_equal` | number | Upper bound, exclusive or inclusive |
| `not_equal` | number | Excluded value |
| `step` | integer | Value must be a multiple of it |

The GraphQL `BlueprintInputValidations` carries the same rules in camel case
(`required: Boolean!`, `pattern`, `minLength`, `maxLength`, `lengthEqual`,
`greaterThan`, `greaterThanOrEqual`, `lessThan`, `lessThanOrEqual`,
`notEqual`, `step`), with the numeric bounds typed `Float` and `step` `Int`
[G]. The template docs add `required` for `boolean` and `select` inputs [D]
tconfig, which the template JSON schema does not allow (the boolean and select
shapes have no `validations` key) [D] tconfig schema; which one the service
enforces is *(unverified)*.

### 5.3 Stack

Required: `name`, `space`, `vcs`, `vendor`; `key` is also required inside
`stacks` [D] blueprint schema. Each key sets the stack setting of the same
meaning in report 07.

| Key | Type | Constraints | Stack setting (report 07) |
|---|---|---|---|
| `key` | string | Required in `stacks`; the target of `depends_on` and `from_stack` | none (body-local) |
| `name` | string | min length 1 | `name` |
| `description` | string | | `description` |
| `labels` | list of string | | `labels` |
| `space` | string | min length 1; space ID | `space` |
| `administrative` | boolean | Docs call it deprecated in favour of `attachments.roles` [D] blueprint | `administrative` |
| `allow_promotion` | boolean | | `githubActionDeploy` |
| `auto_deploy` | boolean | | `autodeploy` |
| `auto_retry` | boolean | | `autoretry` |
| `is_disabled` | boolean | | `isDisabled` |
| `local_preview_enabled` | boolean | | `localPreviewEnabled` |
| `protect_from_deletion` | boolean | | `protectFromDeletion` |
| `secret_masking_enabled` | boolean | | `enableWellKnownSecretMasking` |
| `runner_image` | string | | `runnerImage` |
| `worker_pool` | string | worker pool ID | `workerPool` |
| `attachments` | object | Section 5.4 | contexts, roles, policies, cloud integrations |
| `environment` | object | Section 5.5 | environment variables, mounted files, dependency references |
| `hooks` | object | Section 5.6 | before and after hooks |
| `schedules` | object | Section 5.7 | drift detection, scheduled tasks, scheduled delete (report 18) |
| `vcs` | object | Section 5.8 | VCS settings |
| `vendor` | object | Section 5.9 | vendor configuration |
| `options` | object | `trigger_run`, `do_not_create`, per stack | none |
| `depends_on` | list of string | stack keys in the same body | stack dependencies (report 18) |

The mapping of `allow_promotion` to `githubActionDeploy` and of
`secret_masking_enabled` to `enableWellKnownSecretMasking` is [I] from the
setting names in report 07; the docs give no mapping.

### 5.4 Attachments

| Key | Shape | Required keys |
|---|---|---|
| `contexts` | list of `{id, priority}` | `id`; `priority` integer ≥ 0 |
| `roles` | list of `{role_id, space_id}` | both |
| `policies` | list of policy IDs | |
| `clouds.aws` | `{id, read, write}` | all three |
| `clouds.azure` | `{id, read, write, subscription_id}` | all four |

All attachments name existing objects by ID [D] blueprint. The schema has no
`clouds.gcp` key although the SaaS page lists GCP integrations among the
attachable resources [D] blueprint; the schema wins, so GCP attachment from a
body is *(unverified)*.

### 5.5 Environment

| Key | Item shape | Required |
|---|---|---|
| `variables` | `{name, value, description, secret}` | `name`, `value`; `value` is a string |
| `mounted_files` | `{path, content, description, secret}` | `path`, `content` |
| `stack_dependency_references` | `{name, from_stack, output, trigger_always}` | `name`, `from_stack` (a stack key), `output` |

### 5.6 Hooks

`apply`, `init`, `plan`, `perform` and `destroy` each take `before` and
`after`; `run` takes only `after`. Each hook is a list of strings [D]
blueprint schema. The docs example writes each list as one command split into
words, such as `["sh", "-c", "echo …"]` [D] blueprint.

### 5.7 Schedules

| Key | Shape | Required | Constraints |
|---|---|---|---|
| `drift` | `{cron, reconcile, ignore_state, timezone}` | `cron`, `reconcile` | `cron` is a list of cron strings |
| `tasks` | list of `{command, cron, timestamp_unix, timezone}` | `command` plus one of `cron` or `timestamp_unix` | `command` min length 1; `timestamp_unix` ≥ 1600000000 |
| `delete` | `{delete_resources, timestamp_unix}` | `timestamp_unix` | ≥ 1600000000 |

Each blueprint cron string must match five space-separated fields, each `*`,
`N`, `N-M` or `N/M` [D] blueprint schema. The template schema also accepts
`*/N` and comma lists (section 8.4). Cron semantics and timezones are report
18.

### 5.8 VCS

The schema has two shapes [D] blueprint schema:

| Key | Provider shape | `RAW_GIT` shape |
|---|---|---|
| `branch` | required, min length 1 | required, min length 1 |
| `provider` | required; `GITHUB`, `GITLAB`, `BITBUCKET_DATACENTER`, `BITBUCKET_CLOUD`, `GITHUB_ENTERPRISE`, `SHOWCASE`, `AZURE_DEVOPS` | required; `RAW_GIT` |
| `repository` | required, min length 1; name only | optional; derived from `repository_url` when absent |
| `namespace` | optional in the schema; docs require it for all providers except `GITHUB` | optional; derived from `repository_url` |
| `repository_url` | not allowed | required |
| `id` | optional; a space-level VCS integration ID | not allowed |
| `project_root` | optional | optional |
| `project_globs` | optional list | optional list |

`SHOWCASE` appears only in the schema [D] blueprint schema; the prose list of
providers omits it [D] blueprint. `GITHUB` is the built-in integration and
`GITHUB_ENTERPRISE` covers any additional GitHub installation [D] blueprint.
The export page says blueprints take `branch`, `tag` or `sha` [D] export, but
the blueprint schema has only `branch` [D] blueprint schema; the schema wins.

### 5.9 Vendor

One vendor block per stack [D] tconfig. Each block's keys [D] blueprint schema:

| Block | Keys | Required |
|---|---|---|
| `terraform` | `version`, `workspace`, `use_smart_sanitization`, `manage_state`, `workflow_tool` (`TERRAFORM_FOSS`, `CUSTOM`, `OPEN_TOFU`) | `manage_state` |
| `opentofu` | `version`, `workspace`, `use_smart_sanitization`, `external_state_access_enabled`, `concise`, `enable_cpu_profiling`, `manage_state`, `workflow_tool` (`OPENTOFU`, `CUSTOM`) | `manage_state` |
| `terragrunt` | `terraform_version`, `terragrunt_version`, `use_run_all`, `use_smart_sanitization`, `terragrunt_tool` (`TERRAFORM_FOSS`, `OPEN_TOFU`, `MANUALLY_PROVISIONED`) | none |
| `pulumi` | `stack_name`, `login_url` | both, min length 1 |
| `cloudformation` | `entry_template_file`, `template_bucket`, `stack_name`, `region` | all, min length 1 |
| `kubernetes` | `namespace` | `namespace`, min length 1 |
| `ansible` | `playbook` | `playbook`, min length 1 |

The docs mark `OPEN_TOFU` under `terraform.workflow_tool` as deprecated in
favour of the `opentofu` block [D] blueprint. Vendor semantics are report 20.

## 6. The expression engine

### 6.1 Syntax and functions

- An expression is written `${{ … }}` anywhere a scalar is allowed; a field
  whose whole value is one expression takes the expression's type, so
  `${{ inputs.env == 'prod' }}` yields a boolean [D] blueprint. Quoting the
  expression forces a string [D] blueprint.
- The engine is Spacelift's `celplate`, built on Google CEL [D] blueprint.
  CEL's standard functions (`contains`, `startsWith`, `endsWith`, `matches`,
  `size` and others), operators (`* / - +`, comparisons, `&& || !`, `?:`,
  `in`), and cel-go's string extensions (`replace`, `lowerAscii`,
  `upperAscii`, `split`, `join` and others) are available [D] blueprint
  [D] tconfig. Type conversions include `string()`, `int()`, `bool()`,
  `timestamp()` and `duration()` [D] tconfig [D] blueprint.
- The docs link cel-spec v0.7.1 and cel-go v0.21.0 string extensions [D]
  blueprint; the exact cel-go version `celplate` uses is *(unverified)*.
- YAML reserved characters inside an expression (`:`, `?`, `>`, `|`) need the
  whole value quoted [D] blueprint.
- A body generated by Terraform's `templatefile` must escape the delimiter as
  `$${{ … }}` [D] blueprint.

### 6.2 Context variables

| Variable | Type | Blueprint | Template |
|---|---|---|---|
| `context.time` | timestamp, UTC, at evaluation | yes | yes |
| `context.random_string` | 6 alphanumeric characters | yes | yes |
| `context.random_number` | integer 0 to 1000000 | yes | yes |
| `context.random_uuid` | UUID string | yes | yes |
| `context.user.login`, `context.user.name` | strings from the identity provider | yes | yes |
| `context.user.account` | account subdomain | yes | yes |
| `context.blueprint.name`, `.space`, `.labels` | string, space ID, list | yes | no |
| `context.blueprint.created_at`, `.updated_at`, `.published_at` | timestamps | yes | no |
| `context.deployment.name`, `.slug` | strings | no | yes |
| `context.deployment.created_at` | timestamp | no | yes |

Sources: [D] blueprint; [D] tconfig. Because the `context.random_*` values and
`context.time` differ per evaluation, the docs qualify the determinism of
templates with the condition that no random input is used [D] template.

### 6.3 Maps

`maps` is a two-level lookup table: first-level keys name a variant, second-
level keys hold strings, numbers or booleans [D] blueprint schema. Bodies read
it as `${{ maps[inputs.env].key }}`. Map values may not contain expressions
and input definitions may not read maps [D] blueprint [D] tconfig.

## 7. Templates and template versions

### 7.1 The `Template` object

| Field | Type | Meaning [G] |
|---|---|---|
| `id`, `ulid` | `ID!`, `String!` | Slug and ULID |
| `space` | `Space!` | Owning space |
| `name`, `description`, `labels` | | Name is unique within the space [D] workbench |
| `createdAt`, `updatedAt` | `Int!` | Unix seconds |
| `canDelete` | `Boolean!` | True when the template has no versions |
| `deploymentsCount` | `Int!` | |
| `latestVersion` | `TemplateVersionSummary` | Highest published, non-deprecated version, compared as semantic versions; creation time decides when a number is not semver |
| `templateVersion(id)` | `TemplateVersion` | By slug or ULID |
| `templateVersionByNumber(versionNumber)` | `TemplateVersion` | By number |
| `compatibleVersions(versionId)` | `[TemplateVersion!]!` | Published versions a deployment on `versionId` may move to |
| `searchTemplateVersions`, `searchTemplateVersionsSuggestions` | search outputs | Report 21 |
| `allowedActions` | `[Action!]!` | |

`TemplateVersionSummary` has `id`, `ulid`, `version`, `deprecatedAt` and
`deprecationNote` [G].

### 7.2 The `TemplateVersion` object

| Field | Type | Meaning [G] |
|---|---|---|
| `id`, `ulid` | `ID!`, `String!` | |
| `space` | `Space!` | |
| `deleted` | `Boolean!` | |
| `deprecatedAt`, `deprecationNote` | `Int`, `String` | Set by `templateVersionDeprecate` |
| `name` | `String!` | The template's name |
| `instructions` | `String` | Markdown deployment instructions shown to users [D] workbench |
| `rawTemplate` | `String!` | YAML body |
| `state` | `BlueprintState!` | `DRAFT` or `PUBLISHED` |
| `inputs` | `[BlueprintInput!]!` | |
| `labels`, `stackKeys` | `[String!]!` | |
| `createdAt`, `updatedAt`, `publishedAt` | `Int!`, `Int!`, `Int` | Unix seconds |
| `templateDetails` | `Template!` | Parent |
| `version` | `String` | Version number |
| `canDelete` | `Boolean!` | False while deployments exist [D] workbench |
| `deploymentsCount` | `Int!` | |
| `template` | `BlueprintTemplate!` | The body in template-schema form; for a body in the older schema it is the converted body with warnings |

### 7.3 Version numbers

The number is free text; Spacelift imposes no convention [D] workbench. The
provider calls `version_number` a string such as `1.0.0` [S]
[resource_template_version.go](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_version.go).
Only `latestVersion` interprets it, as a semantic version with creation time
as the fallback [G]. Whether two versions of one template may share a number
is *(unverified)*.

### 7.4 Version states and transitions

| From | To | Trigger | Rule |
|---|---|---|---|
| (none) | `DRAFT` | `templateVersionCreate` with `state: DRAFT`, or `templateCreate.initialVersion` | Optional clone of an existing version's body in the UI [D] workbench. `InitialTemplateVersionInput` has no `state` field [G]; that the initial version starts as `DRAFT` is [I] from the export, which creates a `DRAFT` version [D] export |
| (none) | `PUBLISHED` | `templateVersionCreate` with `state: PUBLISHED` | Body required [S] [resource_template_version.go#L88](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_version.go#L88) |
| `DRAFT` | `DRAFT` | `templateVersionUpdate` | Body, instructions, labels and number editable [D] workbench [G] |
| `DRAFT` | `PUBLISHED` | `templateVersionUpdate` with `state: PUBLISHED`, or **Publish** | Body parsed and validated; the VCS reference is pinned to its current commit SHA; the version becomes read-only and appears in the Templates List [D] workbench [D] tconfig |
| `PUBLISHED` | `DRAFT` | none | One-way; create a new version instead [D] workbench |
| `PUBLISHED` | `PUBLISHED`, deprecated | `templateVersionDeprecate(id, {note})` | Sets `deprecatedAt` and `deprecationNote`; excluded from `latestVersion` [G] |
| any | deleted | `templateVersionDelete` | Only when the version has no deployments [D] workbench |

The provider validates a body before publishing by calling
`templateVersionParseTemplate`, and refuses to change the state of, or the
body of, a published version [S]
[resource_template_version.go#L27-L57](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_version.go#L27-L57).
The docs do not describe deprecation. Bruno's Templates/Versions folder says
deprecation discourages new use without breaking deployments already on the
version [B]; whether the service refuses new deployments from a deprecated
version is *(unverified)*.

### 7.5 Template and version operations

| Operation | Kind | Arguments | Returns | RBAC action [G] [D] rbac |
|---|---|---|---|---|
| `template` | Q | `id` | `Template` | space read |
| `searchTemplates`, `searchTemplatesSuggestions` | Q | search inputs | outputs | space read |
| `templateSchema` | Q | none | `String!` (JSON schema) | |
| `templateVersionParseTemplate` | Q | `template` | `BlueprintParseResult` (`inputs`, `errors`) | |
| `templateCreate` | M | `TemplateCreateInput` (`space`, `name`, `description`, `labels`, `initialVersion: {number, description, rawTemplate}`) | `Template!` | `CREATE_BLUEPRINTS_VERSIONED_GROUP` |
| `templateUpdate` | M | `id`, `TemplateUpdateInput` (`space`, `name`, `description`, `labels`) | `Template!` | `UPDATE_BLUEPRINTS_VERSIONED_GROUP` |
| `templateDelete` | M | `id` | `Template` | `DELETE_BLUEPRINTS_VERSIONED_GROUP`; only with no versions [D] workbench |
| `templateVersionCreate` | M | `TemplateVersionCreateInput` (`templateID`, `state`, `instructions`, `labels`, `template`, `versionNumber`) | `TemplateVersion!` | `UPDATE_BLUEPRINTS_VERSIONED_GROUP` |
| `templateVersionUpdate` | M | `id`, `TemplateVersionUpdateInput` (`state`, `instructions`, `labels`, `template`, `versionNumber`) | `TemplateVersion!` | `UPDATE_BLUEPRINTS_VERSIONED_GROUP` |
| `templateVersionDelete` | M | `id` | `TemplateVersion` | `UPDATE_BLUEPRINTS_VERSIONED_GROUP`; only with no deployments |
| `templateVersionDeprecate` | M | `id`, `TemplateVersionDeprecateInput` (`note`) | `TemplateVersion!` | not documented |

`templateUpdate` and `templateVersionUpdate` take full objects, with
`labels` non-null lists, so a client must resend every field [G].

## 8. The template body

`templateSchema` serves a draft-07 schema, printed on the tconfig page, whose
title is still `Blueprint` [G] [D] tconfig schema. It reuses the blueprint
definitions (inputs, validations, maps, attachments, environment items, hooks,
vendor blocks) unchanged, except as below [D] tconfig schema, compared by
script.

### 8.1 Top level

| Key | Type | Required | Notes |
|---|---|---|---|
| `space` | string, min length 1 | no | Space slug for the deployment and all its stacks; overrides the space chosen at deploy time; absent means the template's space [D] tconfig |
| `inputs` | list of input | no | As section 5.2 |
| `maps` | as section 6.3 | no | |
| `stacks` | list of stack | yes | No `maxItems` in the schema; the docs give 10 [D] tconfig; which limit the service enforces is *(unverified)* |
| `scheduled_delete` | `{timestamp_unix, delete_stacks}` | no | Section 9.5 |

`stack` (singular) and `options` do not exist in the template schema [D]
tconfig schema.

### 8.2 Stack keys that differ

| Blueprint key | Template key | Change |
|---|---|---|
| `key` (required only in `stacks`) | `key` | Always required; must be unique; may not be an expression [D] tconfig |
| `space` (required) | none | Moved to top-level `space` |
| `auto_deploy`, `auto_retry` | `autodeploy`, `autoretry` | Renamed |
| `allow_promotion`, `is_disabled`, `local_preview_enabled`, `protect_from_deletion` | none | Removed |
| `options` | none | Removed; a deployment always creates its stacks and starts tracked runs (section 9.4) [I] from the update behaviour in [D] workbench |
| `schedules.delete` | none | Replaced by top-level `scheduled_delete` |
| `vcs.branch` | `vcs.reference` `{value, type}` | `type` is `branch`, `tag` or `sha`; both keys required |
| none | `external_depends_on` | List of existing stack slugs; ordering only |
| none | `environment.external_stack_dependency_references` | `{name, from_stack, output, trigger_always}` with `from_stack` an existing stack slug |

Unchanged stack keys: `name`, `description`, `labels`, `administrative`,
`runner_image`, `secret_masking_enabled`, `worker_pool`, `attachments`,
`environment` (`variables`, `mounted_files`, `stack_dependency_references`),
`hooks`, `schedules.drift`, `schedules.tasks`, `vcs` (other keys), `vendor`,
`depends_on` [D] tconfig schema. Every `value` of an environment variable must
be a string, so numeric and boolean inputs need quoting [D] tconfig.

### 8.3 Where expressions are not allowed

Expressions may not appear in stack `key`, `depends_on` entries, any key under
`vcs`, or any key under `inputs` (including `default`, `options` and
`validations`) [D] tconfig. They are allowed in `external_depends_on`,
`name`, `description`, `labels`, booleans such as `autodeploy`, `environment`,
`vendor`, `hooks`, `schedules`, `attachments` and `scheduled_delete` [D]
tconfig. The restriction exists so that the parse step can determine stacks,
dependencies and repositories without inputs [D] tconfig.

### 8.4 Cron strings

The template cron pattern adds `*/N` and comma lists (`N,M,…`) to the
blueprint pattern's `*`, `N`, `N-M` and `N/M` per field [D] tconfig schema.
The template `delete_schedule` drops the `timestamp_unix` minimum of
1600000000 that the blueprint schema sets [D] tconfig schema.

### 8.5 External dependencies

`external_depends_on` and `external_stack_dependency_references` refer to
stacks outside the template by slug. The referenced stacks must already exist
in the account, and the deploying user needs read on their spaces. A stack
named in both lists yields one dependency carrying the references [D] tconfig.
Dependency semantics are report 18.

## 9. Template deployments

### 9.1 The `TemplateDeployment` object

| Field | Type | Meaning [G] |
|---|---|---|
| `id` | `ID!` | Slug, fixed at creation |
| `createdAt` | `Int!` | Unix seconds |
| `space` | `Space!` | Where the deployment and its stacks live |
| `template` | `TemplateDeploymentTemplate!` | `{id}` |
| `templateVersion` | `TemplateDeploymentVersion!` | `{id, version}` |
| `name`, `description` | `String!`, `String` | Renaming keeps the slug |
| `deploymentType` | `TemplateDeploymentType!` | `REGULAR` or `TEST` |
| `state` | `TemplateDeploymentState!` | Computed from the states of the managed stacks |
| `stacks` | `[TemplateDeploymentStack!]!` | `{id, name, state: StackState, outputs: [StackOutput]}` |
| `inputs` | `[TemplateDeploymentInput!]!` | `{id, value, secret, checksum}`; described as the non-secret inputs |
| `allowedActions` | `[Action!]!` | |
| `searchChangeHistory(input)` | `SearchTemplateDeploymentChangeHistoryOutput!` | Section 9.6 |
| `scheduledDeleteTimestamp` | `Int` | From `scheduled_delete.timestamp_unix` |
| `scheduledDeleteStacks` | `Boolean` | From `scheduled_delete.delete_stacks`; true by default |

`checksum` is the SHA-256 of the value [G]. The provider compares checksums
to detect changes to secret inputs it cannot read back [S]
[resource_template_deployment.go#L424-L428](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_deployment.go#L424-L428).
The `inputs` description says it lists non-secret inputs while each item has a
`secret` flag; what a secret item's `value` holds is *(unverified)*.

### 9.2 Deployment types

| Value | Meaning |
|---|---|
| `REGULAR` | Default; deploys a published version [G] |
| `TEST` | May deploy a draft version [G]; needs `TEST_BLUEPRINT_DEPLOYMENT` [G] [D] rbac |

Other differences between test and regular deployments are *(unverified)*.

### 9.3 Deployment states

`TemplateDeploymentState` (and the identical deprecated
`BlueprintDeploymentState`) [G], with the docs' meaning and allowed actions:

| Value | UI label | Meaning | Update | Revert | Delete |
|---|---|---|---|---|---|
| `NONE` | None | Initial, before processing [D] deployments | *(unverified)* | *(unverified)* | *(unverified)* |
| `IN_PROGRESS` | InProgress | Stacks being created or updated [D] workbench | no | no | no |
| `FINISHED` | Finished | All runs succeeded; outputs available | yes | yes | yes |
| `FAILURE` | Failure | Creation or update failed | yes | yes | yes |
| `UNCONFIRMED` | Unconfirmed | Waiting for confirmation by the template creator | no | no | no |
| `DESTROYING` | Destroying | Resources being destroyed on delete | no | no | no |
| `DESTROYING_FAILED` | DestroyingFailed | Destruction failed | no [D] workbench | *(unverified)* | yes |
| `DISCARDED` | Discarded | The deployment's run was discarded [I] from the run state of the same name in report 08 | yes | *(unverified)* | yes |

Sources: the workbench state table and the warnings that block update, revert
and delete in `IN_PROGRESS`, `UNCONFIRMED` and `DESTROYING` [D] workbench
[D] deployments. The end-user page omits `DISCARDED` [D] deployments.

The state is derived from the stacks' states [G]; the derivation rule is
*(unverified)*. `UNCONFIRMED` matches a tracked run that stopped for
confirmation [I] from the run state `UNCONFIRMED` (report 08). The provider
waits while the state is `NONE`, `IN_PROGRESS` or `DESTROYING` and treats
`FINISHED` and `UNCONFIRMED` as settled [S]
[resource_template_deployment.go#L374-L389](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_deployment.go#L374-L389).

### 9.4 Lifecycle operations

| Action | Operation | Arguments | Effect | RBAC [G] [D] rbac |
|---|---|---|---|---|
| Deploy | `templateDeploymentCreate` | `id` (the template version ID), `TemplateDeploymentCreateInput` (`space`, `name`, `description`, `inputs: [{id, value}]`, `deploymentType`) | Creates the deployment, its stacks, and starts provisioning [D] deployments | `CREATE_BLUEPRINT_DEPLOYMENT`, or `TEST_BLUEPRINT_DEPLOYMENT` for `TEST` |
| Read | `templateDeployment` | `id`, `templateID` | | space read |
| Search | `searchTemplateDeployments` | `SearchInput` | | space read |
| Update inputs or metadata | `templateDeploymentUpdate` | `id`, `templateID`, `TemplateDeploymentUpdateInput` (`name`, `description`, `inputs`) | Stacks updated in place, then a new tracked run [D] workbench | `UPGRADE_BLUEPRINT_DEPLOYMENT` |
| Upgrade version | `templateDeploymentUpdate` with `versionId` | as above | The deployment moves to the version; stacks updated, not recreated; a tracked run starts [D] workbench | `UPGRADE_BLUEPRINT_DEPLOYMENT` |
| Revert | `templateDeploymentRollback` | `id`, `templateID`, `changeRecordID` | Restores the version and inputs recorded in that change record and redeploys [D] workbench [G] | `ROLLBACK_BLUEPRINT_DEPLOYMENT` |
| Delete | `templateDeploymentDelete` | `id`, `templateID`, `skipStackDeletion` | Destroys the stacks and their resources; with `skipStackDeletion: true`, removes only the deployment and detaches the stacks as standalone stacks [G] | `DELETE_BLUEPRINT_DEPLOYMENT`; the end-user page also lets the deployment owner delete [D] deployments |

Rules:
- `inputs` on update is a full replacement; an empty list clears all
  inputs; `keepOldValue: true` keeps a stored value, which is how a client
  leaves a secret unchanged without knowing it [G].
- An upgrade target must have exactly the same stack keys as the current
  version and no new required inputs [D] workbench; `compatibleVersions`
  returns the published versions that qualify [G].
- The upgrade action covers both deprecated actions
  `UPDATE_BLUEPRINT_DEPLOYMENT` and `UPDATE_BLUEPRINT_DEPLOYMENT_INPUTS` [D]
  rbac [G].
- `TemplateDeploymentUpgradeInput` (`versionId`) exists in the schema but no
  operation takes it [G].
- Stacks of a deployment cannot be edited by anyone; they become editable
  again once their deployment is deleted, so that failures can be repaired by
  hand [D] workbench.
- The provider waits 3 minutes on create and 30 minutes on update and
  delete by default; after an update that changes the version or inputs it
  requires three consecutive settled reads, because the state may still show
  the previous `FINISHED` [S]
  [resource_template_deployment.go#L36-L40](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_deployment.go#L36-L40)
  [S] [#L276-L289](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_deployment.go#L276-L289).
  On delete it waits for the deployment to disappear or reach `UNCONFIRMED`
  [S] [#L311-L316](https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_deployment.go#L311-L316).

### 9.5 Scheduled deletion

| Key | Type | Required | Default |
|---|---|---|---|
| `scheduled_delete.timestamp_unix` | number, Unix seconds; may be an expression | yes | none |
| `scheduled_delete.delete_stacks` | boolean | no | `true` |

Source: [D] tconfig. When the time arrives [D] tconfig:
- with `delete_stacks: true`, the deployment and its stacks are destroyed; while
  any stack still has a non-terminal run the deletion waits, a
  notification is sent, and Spacelift retries periodically (the retry
  interval is *(unverified)*);
- with `delete_stacks: false`, the stacks are detached and kept, and only the
  deployment is removed.

Updating a deployment reconciles the schedule: the new version's
`scheduled_delete` replaces the old one, and a version without the block
removes it [D] tconfig.

### 9.6 Change history

Each applied change produces a `TemplateDeploymentChangeRecord` with `id`
(ULID), `createdAt`, `version` (`{id, number, rawTemplate}`), `inputs`,
`previousVersion` and `previousInputs` (null on the first record) [G]. The UI
groups entries by date and shows version movement, input changes with secrets
masked, and a body diff when the version changed [D] workbench. Every entry
except the newest offers **Revert to**; the newest cannot be a revert target
[D] workbench. Whether a revert itself adds a change record is [I] likely
from "the newest entry reflects the current state" but *(unverified)*.

### 9.7 Permissions

| Action | `Action` value | Legacy fallback |
|---|---|---|
| Create template | `CREATE_BLUEPRINTS_VERSIONED_GROUP` | Space admin |
| Update template, create, publish or delete a version | `UPDATE_BLUEPRINTS_VERSIONED_GROUP` | Space admin |
| Delete template | `DELETE_BLUEPRINTS_VERSIONED_GROUP` | Space admin |
| Create deployment | `CREATE_BLUEPRINT_DEPLOYMENT` | Space admin |
| Test deployment | `TEST_BLUEPRINT_DEPLOYMENT` | Space admin |
| Upgrade or update deployment | `UPGRADE_BLUEPRINT_DEPLOYMENT` (`UPDATE_BLUEPRINT_DEPLOYMENT` and `UPDATE_BLUEPRINT_DEPLOYMENT_INPUTS` deprecated) | Space admin |
| Revert deployment | `ROLLBACK_BLUEPRINT_DEPLOYMENT` | Space admin |
| Delete deployment | `DELETE_BLUEPRINT_DEPLOYMENT` | Space admin |
| View templates and deployments | none | Space read |

Sources: [G] `Action`; [D] rbac; [D] workbench. The rbac page gives Admin as
the legacy fallback for every template action, and the workbench page phrases
each permission as the action "or Space Admin"; the end-user deployments page
also lets the deployment owner delete it [D] deployments. Report 14 owns the
RBAC model.

### 9.8 Audit trail

`AuditTrailResourceType` has `BLUEPRINT`, `BLUEPRINT_VERSIONED_GROUP` and
`BLUEPRINT_DEPLOYMENT`, with metadata types carrying `id` and `name` [G]. No
template-named values exist; template events appear to use the blueprint
names [I] from the shared RBAC naming. Report 19 owns the audit trail.

## 10. Exporting a blueprint to a template

| Step | Behaviour |
|---|---|
| Precondition | Blueprint `PUBLISHED`; the user may create templates in the target space [D] export |
| Preview | A side-by-side diff of the blueprint body and the converted body, with warnings (partly compatible fields converted on a best-effort basis) and errors (incompatible fields omitted) [D] export |
| Result | A new template named after the blueprint, with its description, space and labels, and a `DRAFT` version `0.1` holding the converted body [D] export |
| Source blueprint | Left unchanged [D] export |
| API | `Blueprint.template` returns the converted body, `warnings` and `errors` [G]; the create step uses `templateCreate` with `initialVersion` [I] from the inputs available |

The docs' summary of what changes lists the `stacks`-only form, required
unique keys, `reference` for VCS, the top-level `space`, the 5-to-10 stack
limit and the expression restrictions [D] export.

## 11. Clients

### 11.1 Terraform provider (v1.57.0)

| Resource or data source | Key attributes | Notes |
|---|---|---|
| `spacelift_blueprint` | `name`, `space`, `state` (required); `description`, `labels`, `template` | Replacing `template` forces a new resource [S] |
| `spacelift_template` | `name`, `space` (required); `description`, `labels`; computed `ulid`, `created_at`, `updated_at` | [S] |
| `spacelift_template_version` | `template_id` (forces new), `version_number`, `state` (required); `instructions`, `template`; computed `ulid`, `created_at`, `updated_at`, `published_at` | Validates and guards published versions (section 7.4) [S] |
| `spacelift_template_deployment` | `template_version_id`, `name`, `space` (forces new) (required); `description`; `input` blocks (`id`, `value` sensitive; computed `secret`, `checksum`); computed `deployment_id`, `template_id`, `state`, `stacks`, `created_at` | Changing `template_version_id` upgrades in place [S] |
| data `spacelift_template`, `spacelift_template_version` (by `version_id` or `version_number`), `spacelift_template_deployment` | | [S] |

Source: [`terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json)
and the provider files under
<https://github.com/spacelift-io/terraform-provider-spacelift/tree/c72b9bd2f3c3/spacelift>.
The provider has no blueprint data source and no blueprint stack-creation
resource [S]. Report [05](05-terraform-provider.md) covers the extraction.

### 11.2 spacectl

| Command | Calls | Notes |
|---|---|---|
| `spacectl blueprint list` | `searchBlueprints` | [S] [list.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/blueprint/list.go) |
| `spacectl blueprint show --blueprint-id` | `blueprint` | [S] [show.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/blueprint/show.go) |
| `spacectl blueprint deploy --blueprint-id [--input-file FILE]` | `blueprintCreateStack` | Prompts per input type or reads a JSON file [S] [deploy.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/blueprint/deploy.go) |
| `spacectl template list` | `searchTemplates` | [S] [list.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/template/list.go) |
| `spacectl template show --template-id` | `template` | [S] [show.go](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/template/show.go) |

spacectl's MCP server also exposes `list_blueprints` (`searchBlueprints`) and
`get_blueprint` (`blueprint`) tools [S]
[mcp.go#L23](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/blueprint/mcp.go#L23)
[S] [#L83](https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/blueprint/mcp.go#L83).
spacectl cannot create template deployments [S]. Report 06 owns spacectl.

### 11.3 Other consumers

| Consumer | Uses | Source |
|---|---|---|
| ServiceNow integration | Blueprints, through `blueprintAttachToIntegration` | [D] servicenow; report 25 |
| Flows | A **Deploy Template** block that deploys a template and reports progress, and a **Deploy Blueprint** block that creates stacks from a blueprint; each exposes the form's inputs as block fields | [D] flows-blocks; report 23 |
| Native OpenTofu migration | Blueprints that create OpenTofu stacks should move to the `opentofu` vendor block so new stacks are created natively | [D] tofu-migration; report 20 |

## 12. The version catalog

The Bruno collection groups seven queries under **Version Catalog**: the tool
versions Spacelift offers, and the version a constraint resolves to, so a
client can check what a stack would use before a run [B]. Template and
blueprint vendor blocks name versions or constraints that these queries
validate (`version`, `terraform_version`, `terragrunt_version`) [I].

| Query | Arguments | Returns | Meaning [G] |
|---|---|---|---|
| `terraformVersions` | none | `[String!]!` | Supported Terraform versions |
| `openTofuVersions` | none | `[String!]!` | Supported OpenTofu versions |
| `terragruntVersions` | none | `[String!]!` | Supported Terragrunt versions |
| `kubectlVersions` | none | `[String!]!` | Supported kubectl versions |
| `terraformEffectiveVersion` | `constraints: String!` | `String!` | Newest supported version satisfying the constraint |
| `openTofuEffectiveVersion` | `constraints: String!` | `String!` | Same, for OpenTofu |
| `terragruntEffectiveVersion` | `terragruntConstraints: String!`, `terraformConstraints: String!`, `tool: TerragruntTool` | `EffectiveTerragruntVersionResult!` | `effectiveTerragruntVersion`, `effectiveTerraformVersion` (both null for `MANUALLY_PROVISIONED`), `tool` |

`tool` accepts only `TERRAFORM_FOSS` or `OPEN_TOFU` and defaults to
`TERRAFORM_FOSS` [G]. Bruno's examples use constraints such as `>= 1.5.0` and
`>= 0.50.0` [B]. Constraint syntax follows the blang/semver range grammar,
the newest supported version is used when a stack sets none, Spacelift may
withhold a faulty release, and a stack's version is raised to the one last
applied [D] versions. OpenTofu constraints are validated against the lowest
version they admit [D] versions. Resolution at run time is report 20; the
REST routes are report 04.

## What this means for infraweave

- Two body schemas must be served and validated: the blueprint schema
  (`stack` or up to 5 `stacks`) and the template schema (`stacks`, top-level
  `space` and `scheduled_delete`, `reference`, external dependencies), each
  rejecting unknown keys, each exposed as a JSON string.
- A CEL-based renderer with `${{ }}` delimiters, the string extensions, typed
  substitution, `maps`, and the blueprint and template context variables is
  needed; template parse must reject expressions in keys, `depends_on`, VCS
  and input definitions.
- Blueprint state (`DRAFT`, `PUBLISHED`, one-way), clone, stack creation with
  `trigger_run` and `do_not_create`, and share links.
- Templates need versions with one-way publication, commit pinning on
  publish, deprecation, semver-aware latest-version selection, and
  compatibility checks (same stack keys, no new required inputs).
- Deployments need the eight-value state computed from stack and run states,
  `REGULAR` and `TEST` types, full-replacement input updates with
  `keepOldValue`, SHA-256 checksums of inputs, change records with rollback,
  delete with or without stack destruction, scheduled deletion with
  postponement, and a lock that makes managed stacks read-only.
- The RBAC actions keep `BLUEPRINT` names for template operations; any
  wire-compatible surface must accept them.
- The deprecated versioned-blueprint operations and the removed ones are the
  supersession boundary for report 26.
- The version catalog's list and effective-version queries.

## Inferred and undocumented

- The stack-setting mapping of `allow_promotion` and
  `secret_masking_enabled` is inferred from setting names (section 5.3).
- That the template schema's lack of `options` means deployments always
  create stacks and start runs is inferred from the documented update
  behaviour (section 8.2).
- `DISCARDED` and `UNCONFIRMED` deployment states are inferred to mirror the
  run states of the same names (section 9.3).
- That the export uses `templateCreate` with `initialVersion` is inferred
  (section 10).
- That template audit events use the `BLUEPRINT_*` resource types is inferred
  (section 9.8).
- That version catalog queries validate body versions is inferred (section
  12).
- That templates share the `BLUEPRINTS` tier flag, and that
  `templateCreate.initialVersion` creates a `DRAFT` version, are inferred
  (sections 1 and 7.4).
- Undocumented in the docs, present in the schema: `AVAILABLE_SPACES` input
  type, `TEST` deployments, `templateVersionDeprecate`, `latestVersion`
  semantics, `compatibleVersions`, `keepOldValue`, input checksums,
  `skipStackDeletion`, `TemplateDeploymentUpgradeInput` with no operation
  using it.
- Unverified: a blueprint body with neither or both of `stack` and `stacks`;
  `AVAILABLE_SPACES` in YAML; `validations` on boolean and select
  inputs; the template stack limit; GCP attachments; whether
  `BlueprintWithGroup*` remain supported; duplicate version numbers;
  deployments from deprecated versions; secret input values on read; test
  deployment differences; the state derivation; allowed actions in `NONE`,
  `DESTROYING_FAILED` and `DISCARDED`; scheduled-delete retry interval;
  whether a revert adds a change record; `celplate`'s cel-go version.

## Versions at the research baseline

| Component | Version |
|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e` |
| spacelift-api-bruno | `7cad4574954b` |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) |
| spacectl | `6bf167cb62a6` |
| Self-hosted Spacelift | `latest` docs; templates since v5.0.0 |
| CEL references named by the docs | cel-spec v0.7.1; cel-go string extensions v0.21.0 |

## Sources

- [D] <https://docs.spacelift.io/concepts/blueprint>, read 2026-10-28
- [D] <https://docs.spacelift.io/concepts/blueprint/export-to-template>, read 2026-10-28
- [D] <https://docs.spacelift.io/concepts/template>, read 2026-10-28
- [D] <https://docs.spacelift.io/concepts/template/configuration>, read 2026-10-28
- [D] <https://docs.spacelift.io/concepts/template/deployments>, read 2026-10-28
- [D] <https://docs.spacelift.io/concepts/template/workbench>, read 2026-10-28
- [D] <https://docs.spacelift.io/concepts/authorization/rbac-system>, read 2026-10-28
- [D] <https://docs.spacelift.io/vendors/terraform/version-management>, read 2026-10-28
- [D] <https://docs.spacelift.io/integrations/external-integrations/servicenow>, read 2026-10-28
- [D] <https://docs.spacelift.io/self-hosted/latest/concepts/blueprint>, read 2026-10-28
- [D] <https://docs.spacelift.io/flows/spacelift-deploy-integration/spacelift-blocks>, read 2026-10-28
- [D] <https://docs.spacelift.io/vendors/opentofu/migration>, read 2026-10-28
- [RN] <https://docs.spacelift.io/product/changelog> (entries 2026-02-18, 2026-07-16, 2026-10-20), read 2026-10-28
- [RN] <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog> (v2.6.0, v5.0.0), read 2026-10-28
- [G] [`introspection.json`](../../../api/reference/spacelift/introspection.json), `b3255fe38f5e`
- [B] [`bruno.json`](../../../api/reference/spacelift/bruno.json), folders Blueprints, Templates and Version Catalog
- [`coverage.md`](../../../api/reference/spacelift/coverage.md) and [`terraform-schema.json`](../../../api/reference/spacelift/terraform-schema.json)
- [S] <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_blueprint.go>
- [S] <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template.go>
- [S] <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_version.go>
- [S] <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_template_deployment.go>
- [S] <https://github.com/spacelift-io/spacectl/tree/6bf167cb62a6/internal/cmd/blueprint>
- [S] <https://github.com/spacelift-io/spacectl/tree/6bf167cb62a6/internal/cmd/template>
- [S] <https://github.com/spacelift-io/spacectl/blob/6bf167cb62a6/internal/cmd/blueprint/mcp.go>
