# 0021: Capability scope, features and tiers

Status: Accepted

## Context

The research set fixes one scope rule before any design: infraweave mirrors
everything Spacelift lets a user do, at every tier, and leaves out only an
interface that a newer Spacelift interface supersedes, with evidence
([README](../README.md#decisions-fixed-before-writing)). infraweave mirrors
capability, not interface: Spacelift is the reference foundation, and
infraweave's own surfaces use infraweave's names. Report 26 applies the rule
row by row. It gives every tag, Terraform resource and data source an
availability state, and lists the exclusions it found in its
[supersession boundaries](../reference/26-scope-and-tiers.md#7-supersession-boundaries).
Four product questions depend on the rule:

| Question | Report 30 entry |
|---|---|
| Which deprecated or superseded interfaces does infraweave serve? | P2 |
| Are capabilities known only from the schema and Bruno (scans, Origin, `BLOB`, the server side of plugin templates) built? | P3 |
| Where Spacelift's docs and its own clients disagree, which does infraweave's CLI (`infraweavectl`) follow? | P4 |
| Does infraweave gate features by Spacelift's editions and its 31 `BillingTierFeature` values? | P5 |

The maintainer's requirement for P5 is that every feature carries a tier
from the start, that billing is itself a feature, and that an installation
behaves as Enterprise until an operator turns billing on. One installation
hosts many organizations, each one agreement covering one or more accounts
([0007](0007-authorization-spaces-and-roles.md#tenancy)), so a tier is
something each organization has and its accounts share.

Five facts shape the answers:

- **Deprecation evidence is uneven.** The schema marks deprecated output
  fields and enum values, but the snapshot carries no input-field
  deprecations (P15), and some interfaces have no schema element at all: a
  worker transport, an endpoint path, a Terraform resource, a client.
  Sources sometimes disagree, such as the docs deprecating the
  `INITIALIZATION` policy type while the schema keeps it live
  ([26 section 7.3](../reference/26-scope-and-tiers.md#73-policy-types)).
- **Some operations exist only because Spacelift runs a service.** Billing
  subscriptions, the Spacelift-provided AI integration, the Origin app and
  the Azure Entra application are all backed by something Spacelift
  operates ([04 section 3.3](../reference/04-platform-api-inventory.md#33-billing),
  [12 section 3.1](../reference/12-cloud-integrations-and-oidc.md#31-behaviour)).
- **Self-hosted Spacelift is narrower than SaaS.** It has no public worker
  pool, no Azure or GCP integration and no hosted AI integration
  ([26 section 2.3](../reference/26-scope-and-tiers.md#23-self-hosted)).
- **Spacelift's gates are partly published.** Report 26 gives each tag and
  each `BillingTierFeature` value the lowest plan the docs or the pricing
  page name, and marks the rest "Not stated"
  ([26 section 3](../reference/26-scope-and-tiers.md#3-billingtierfeature),
  [section 4](../reference/26-scope-and-tiers.md#4-capability-table-api-tags)).
  A plain Starter tier is published only in fragments (C260).
- **`BillingTierFeature` is a billing artefact, not a feature list.** Some
  values name a Spacelift product (`SPACELIFT_INTELLIGENCE`), some are
  quantities or add-ons (`UNLIMITED_SEATS`, `ADDITIONAL_PUBLIC_WORKER`), one
  is deprecated (`AUDIT_WEBHOOKS`), and its pairing with operations is
  inferred by name (C14, C121, C122).

## Decision

### The scope rule

A capability is in scope when any of these names it: the specification
reference, the docs, the Terraform provider, spacectl, or another published
Spacelift client. It stays in scope unless it passes the supersession test
below. None of these, alone, excludes a capability:

| Not a reason to exclude | Example |
|---|---|
| No docs page | scans, the `BLOB` provider ([21 section 5](../reference/21-resources-scans-search.md#5-scans)) |
| Deprecated by one source while an equal or higher source keeps it live | the `INITIALIZATION` and `TASK` policy types; `TerraformWorkflowTool.OPEN_TOFU` ([20 section 3.1](../reference/20-vendors.md#31-workflow-tool)) |
| Kept "for backward compatibility" with a newer path recommended | per-stack webhooks ([19 section 7](../reference/19-events-notifications-audit.md#7-stack-and-module-webhooks)); the per-stack Slack subscription |
| Missing from self-hosted Spacelift | the public pool, the Azure integration, hosted AI |
| Backed by a Spacelift-operated service | handled by the vendor-bound rule below, not by exclusion |
| Cost, a missing UI, or a tier price | blueprints, the provider registry |
| UI support only | saved filters, search suggestions, forms |

### The supersession test

An interface is **superseded**, and infraweave does not build it, when one of
these rules holds and no source of equal or higher rank
([README](../README.md#target-version-and-sources)) contradicts it:

| Rule | Condition | Examples at the baseline |
|---|---|---|
| S1 Retired | Spacelift removed or disabled it for every account, or the schema says it is no longer used | `ACCESS` policies (disabled 2026-05-30); `RunState.PENDING`; the `STACK_WEBHOOK_*` actions marked disabled |
| S2 Schema-deprecated | The schema deprecates it and names a replacement that infraweave serves. For input fields, which the snapshot cannot mark (P15), a changelog deprecation counts | the 35 deprecated root fields of [26 section 7.1](../reference/26-scope-and-tiers.md#71-root-fields-outside-the-reference); the versioned-blueprint family; `ApiKeyInput.admin`; the flat hook fields |
| S2a Shadowed twin | The schema holds a `V2` twin of the same operation, and the reference routes the twin in its place, so both cannot share one REST route | `bitbucketCloudIntegrationCreate`, `bitbucketCloudIntegrationUpdate`, `moduleUpdate`, `terraformProviderVersionRegisterPlatform` |
| S3 Owner-deprecated | It has no schema element, and the source that owns it deprecates it and names a replacement: the docs for a protocol or path, a client's repository for that client | `/intent/mcp`; the MQTT worker transport; the `pulumi-spacelift` provider; the deprecated provider resources of [26 sections 7.6 and 7.7](../reference/26-scope-and-tiers.md#76-gcp-service-account-integration-replaced-by-oidc); the login policy `space_*` rules |

A schema element that the docs deprecate, but the schema keeps live, is not
superseded under S3. It is served, and marked deprecated as described in
the next section.

Recording a superseded interface needs four things. Report 26 section 7
holds them:
1. the replacement, which must itself be in scope;
2. the primary evidence and its date;
3. how the replacement covers the original use;
4. how stored data that uses it reaches infraweave. The Spacelift importer
   converts it to the replacement, or reports it as not imported, and never
   drops it silently.

Superseded enum values are left out of infraweave's served schemas. The
served spec lists each one with its replacement under
`x-infraweave-superseded` on the enum. A request that sends one fails input
validation as any unknown value does (decision 0022), and the problem's
`detail` names the replacement.

### Lifecycle of interfaces infraweave already serves

The weekly specification update (decision 0002) surfaces new deprecations as
a diff. When Spacelift deprecates an interface that infraweave already
serves, infraweave follows Spacelift's lifecycle rather than its own:

| Spacelift's step | infraweave's served API |
|---|---|
| Deprecates it, naming a replacement | The operation or field gets `deprecated: true`. Responses carry `Deprecation` ([RFC 9745](https://www.rfc-editor.org/rfc/rfc9745)) with Spacelift's deprecation date, and `Link: <successor>; rel="successor-version"` when the replacement is a route |
| Announces a removal date | Responses also carry `Sunset` ([RFC 8594](https://www.rfc-editor.org/rfc/rfc8594)) with that date |
| Removes or disables it | infraweave removes it as a breaking change: a `feat!` commit, so release-please cuts a major version |

Interfaces that are already superseded when infraweave would first build them
are never built. Interfaces that are deprecated but live (S3's exception)
are built, and carry `deprecated: true` from the start.

### Availability states

Report 26's states rest on these definitions. The implementation plan
carries the state of each row forward.

| State | Holds when |
|---|---|
| `implemented` | Every operation of the row is served with behaviour, and meets [0016](0016-testing-and-acceptance.md)'s evidence rule: passing scenarios against real dependencies for a success, a denial and a failure path. Its feature is in the catalogue and its gate is wired. Every capture that blocks it is resolved to a fixture, or its behaviour is recorded as infraweave's choice in a decision record |
| `partial` | Some operations of the row meet `implemented`; the others are `stub` or `missing` |
| `stub` | Every operation is routed and answers 501 from the generated server. A stub is never delivery |
| `missing` | At least one operation has no route |
| `superseded` | The supersession test above holds, with its four recorded items |

Every row also has a **basis**: `documented` when a docs page describes its
behaviour, `schema-only` when only the schema, Bruno or client source does.
A `schema-only` operation carries `x-infraweave-basis: schema-only` in the
served spec. It may reach `implemented`, but its parity claim covers shape
only; the owning decision records each behaviour infraweave chose.

### Deprecated and superseded candidates (P2)

| Candidate | Rule | Disposition |
|---|---|---|
| `ACCESS` policies and `STACK_ACCESS` | S1 | Not served. The value is absent from `PolicyType`. The importer reports access policies as not imported and points to space role bindings |
| `policyCreate`, `policyUpdate`, `policySimulate` | S2 | Not served; the `v2` forms are |
| The versioned-blueprint family | S2 | Not served; templates are. Single-body blueprints and `blueprintWithGroup*` are served ([26 section 7.2](../reference/26-scope-and-tiers.md#72-the-versioned-blueprint-api-replaced-by-templates)) |
| Legacy per-stack AWS role: provider resources `spacelift_aws_role`, `spacelift_stack_aws_role` | S3 | Not in infraweave's provider |
| Legacy per-stack AWS role: mutations `stackIntegrationAwsCreate` and `stackIntegrationAwsDelete` | live in the schema | Served and marked deprecated. `AwsIntegration.legacy` is true for integrations they create, and the importer keeps such integrations as they are |
| Legacy GCP service-account integration | S2 | Not served; GCP is reached through the OIDC federation token |
| Per-stack and per-module webhooks | in scope | Served, on the same delivery path as named webhooks ([0015](0015-events-notifications-webhooks-audit.md)) |
| Per-stack Slack subscription | in scope | Served; what it sends is unverified (report 19) |
| `/intent/mcp` | S3 | Not served; `/mcp` is ([0019](0019-ai-provider-boundary.md)) |
| The standalone `spacelift-intent` stdio server | S3 | Not mirrored: the repository is archived and points to the hosted tools. Local stdio clients use `infraweavectl mcp`, which forwards to `/mcp` (0019 answers P10) |
| `ApiKeyInput.admin` | S2, through the changelog | Not accepted. Admin keys come from a `space-admin` binding on the account's top space (decision 0007) |
| `RunState.PENDING` | S1 | Absent from the served enum |
| The stack `administrative` flag | S1 for its behaviour; the field is live in the schema | Served, marked deprecated and ignored: `StackInput.administrative` is accepted and reads back `false` (decisions 0007 and 0022) |
| `WebhooksIntegration.secret` | S1 for its value; the field is live in the schema | Served, marked deprecated, always an empty string ([0015](0015-events-notifications-webhooks-audit.md)) |
| `TerraformWorkflowTool.OPEN_TOFU` | live in the schema | Served, marked deprecated for stacks only, since modules and Terragrunt still need it |
| `INITIALIZATION` and `TASK` policy types | live in the schema | Served and marked deprecated. The aliases `TASK_RUN` (of `TASK`) and `TERRAFORM_PLAN` (of `PLAN`) are S2 and not served ([26 section 7.3](../reference/26-scope-and-tiers.md#73-policy-types)) |
| MQTT worker transport | S3 | Not served, neither the SaaS IoT Core endpoint nor a self-hosted broker: long-poll is the replacement and the self-hosted v7.1.0 release deprecates the built-in broker too ([10 section 4.2](../reference/10-workers-and-execution.md#42-transports)). Decision 0008 owns the worker protocol and answers P6 |
| `pulumi-spacelift` | S3 | No infraweave Pulumi provider. Pulumi users bridge infraweave's Terraform provider ([06 section 7.1](../reference/06-spacectl-and-clients.md#71-pulumi-provider)) |

### Schema-only capabilities (P3)

These are in scope, built to the schema's shape, with `basis: schema-only`.
Each behaviour infraweave chooses where the sources are silent is recorded in
the owning decision. When capture C11 or a docs page later describes the
behaviour, it replaces infraweave's choice through the lifecycle above.

| Capability | Built as |
|---|---|
| Scans ([21 section 5](../reference/21-resources-scans-search.md#5-scans)) | Scan configurations and scan runs executed on workers like any run, recording discovered resources against managed entities; the `scans` capability |
| `BLOB` source provider ([11 section 10](../reference/11-vcs-integrations.md#10-other-source-providers-in-the-enum)) | Commit archives read from S3 or S3-compatible storage with the schema's three authentication methods; the access key is sealed (decision 0013) |
| Origin integration (same section) | Spacelift backs it with its own global app. infraweave reads an operator-registered Origin app from `vcs.origin` configuration; without one, `originIntegrationInstallURL` answers `installation-unsupported` |
| Plugin templates, server side ([25 section 2](../reference/25-other-integrations.md#2-plugins)) | Custom templates as the schema describes. Global templates are loaded from `spacelift-io/plugins` (MIT) at a pinned commit, with its notice under decision 0002's rules, plus any directory the operator adds in `plugins.catalog_paths` |

### The feature catalogue (P5)

Every feature of infraweave is an entry in one catalogue, the register
[docs/data/features.json](../../data/features.json), its one source. The
`features` domain (decision 0004 places it) compiles it in:
`make generate` writes `internal/features/catalogue_gen.go` from the
register, and `make verify-generate` fails when that file is stale. An
entry has a name, a kind, a description, the features it requires, the
lowest seeded tier that includes it and its owning phase. The
`BillingTierFeature` values it covers for parity (the mapping section
below) are the `tier-feature:` records of
[docs/data/capabilities.json](../../data/capabilities.json), which the
generator reads with it. There are three kinds:

| Kind | Gates | Where the gate sits | Examples |
|---|---|---|---|
| Capability | A set of operations. Usually an API tag; finer where Spacelift gates part of a tag | Every operation of every served OpenAPI document carries `x-infraweave-feature` naming exactly one capability | `stacks`, `drift-detection`, `targeted-replan`, `scheduling` |
| Option | One value or mode of a field inside an operation | The schema property carries `x-infraweave-feature` naming the option and the gated value; the catalogue entry names the same field | `aggregated-vcs-checks` (`vcsChecks: AGGREGATED`), `multiple-vcs-integrations` (`isDefault: false`), each IaC vendor (`StackVendor`) |
| Limit | A quantity, as a whole number in a unit the entry states | The domain that creates the counted thing | `seats`, `private-workers`, `public-worker-slots`, `ai-budget` |

- **Coverage.** Every operation belongs to exactly one capability, and every
  option names its field. The catalogue covers Deploy; Flows, as
  `flows-engine`, `flows-apps`, `flows-agents`, `flows-data-tables`,
  `flows-forms` and `flows-mcp-tools`; the `console`; and each IaC vendor,
  as an option on `StackVendor` such as `vendor-opentofu`. Flows has no
  switch of its own: it turns off like any other feature, and every Flows
  feature requires `flows-engine`.
- **Names.** Feature names are infraweave's own: lower-case English words
  joined by hyphens, never a Spacelift product or `BillingTierFeature`
  spelling. A capability that covers the rest of an API tag takes the tag's
  name, such as `worker-pools` or `vcs-integrations`, unless that name
  collides with a glossary term: the Billing tag's capability is `usage`,
  because `billing` is the feature below. A name is a public contract,
  since `features.disabled`, the tier object and grants carry it, so
  renaming one is a breaking change.
- **Requirements.** An entry may require other features, such as
  `drift-detection` requiring `private-worker-pools`. A feature whose
  requirement is not in an organization's entitlement is not in it either.
- **Splitting.** When a release moves operations out of a capability into a
  new, finer one, every tier that held the original capability gains the new
  one, so no organization loses an operation it had.

### Tiers

A tier is data: a name, a set of features and a set of limit values. Tiers
are not an ordered ladder. A feature check is set membership, so an operator
can define a tier that holds a Business feature without a Starter+ one.

infraweave seeds five tiers from report 26. Each feature is placed at the
plan Spacelift documents for it, read from report 26's Plan column with these
rules:

| Report 26 plan | Seeded tiers |
|---|---|
| All, UI support | Every seeded tier |
| Paid | Starter and above. Starter is the lowest paid plan the pricing page describes |
| Starter+, Business, Enterprise, from a docs notice or the schema | That tier and above |
| Starter+ from the pricing page's comparison table only | Every seeded tier. The table has no Free or Starter column, so it is no evidence against them |
| Not stated | Every seeded tier: infraweave does not invent a gate. C261 corrects the placement |
| A finer gate inside a row | The finer feature at its own plan, the rest of the row at the row's plan |

Enterprise holds every feature and no limit, including features a release
adds later, and the operator cannot edit or delete it, because it is what
every organization has while billing is off. Enterprise+ is not seeded: it differs from Enterprise only in
how Spacelift installs and supports it (self-hosted, FedRAMP, air-gapped,
escalation), none of which is a feature of infraweave.

The features not in every seeded tier:

| Lowest seeded tier | Features |
|---|---|
| Starter | Capabilities `policies`, `notifications`, `webhooks`, `modules`, `module-sharing`, `oidc-federation`, `oidc-sso`, `tasks`; option `notification-policies` (policy `type: NOTIFICATION`) |
| Starter+ | Capabilities `drift-detection`, `private-worker-pools`, `ignored-runs`, `sessions` |
| Business | Capabilities `scheduling`, `targeted-replan`, `blueprints`, `templates`, `terraform-provider-registry` (with the GPG Keys tag, whose keys sign provider releases); option `aggregated-vcs-checks` |
| Enterprise | Capabilities `audit-trail`, `saml-sso`, `scim`, `mfa`, `vcs-agent-pools`, `servicenow`; options `oidc-api-keys` (API key type `OIDC`), `multiple-vcs-integrations` |

Every other feature, including all of Flows, the console, every IaC vendor,
`scans`, `ai-integrations`, `intent` and `organization` (the `Organization`
tag, below), is in every seeded tier.

Seeded limits, where no value means no limit:

| Limit | Unit | Free | Starter | Starter+ | Business | Enterprise |
|---|---|---|---|---|---|---|
| `seats` | members, each once however many accounts admit it, and API keys as [14 section 4.4](../reference/14-spaces-rbac-and-identity.md#44-api-keys) counts them | 2 | 2 | | | |
| `private-workers` | registered private workers | 0 | 0 | 2 | 3 | |
| `public-worker-slots` | runs in flight on the public worker pool | 1 | 1 | 2 | 2 | |
| `ai-budget` | the period budget of decision 0019's default AI integration | USD 10 per 30 days | | | | |

Where Spacelift publishes no value for a tier (Starter's seats and public
slots, Business's "configurable" public workers), the seed takes the value
of the tier below it, and C260 corrects it.

The seed is installed once. After that, the other four tiers are the
operator's data, and the operator can add tiers of its own. A release adds
a new feature to a seeded tier its catalogue entry names only while that
tier still matches its seed.

### Organizations, grants and entitlement

- **One tier per organization.** Every organization (decision 0007) holds
  exactly one tier, and every account of it has the organization's
  entitlement. A new organization gets the installation's default tier,
  Enterprise unless the operator changes it.
- **Grants.** The operator can grant an organization a feature its tier
  lacks, or set one of its limits to a value above or below the tier's.
  Grants belong to the organization, apply to all its accounts, and survive
  a change of tier. Nothing in the entitlement is set per account except
  the account limits below.
- **Switch-offs.** `features.disabled` stays operator configuration. It
  removes a feature from every organization of the installation:

  ```yaml
  features:
    disabled: [ai-integrations, scans]
  ```

  Strict decoding rejects an unknown name, `billing`, and Spacelift's
  spellings such as `SCANS`, so a typo cannot silently leave a feature on.
  Changes take effect at restart, like any other configuration.
- **Entitlement** is the organization's tier, plus its grants, minus the
  switched-off features, minus every feature whose requirements are not in
  that set. Limit values come from a grant if one exists, else the tier.

### Limits

- **Pooled per organization.** `seats`, `private-workers`,
  `public-worker-slots` and `ai-budget` are counted across all of an
  organization's accounts against the organization's value: one member
  admitted by two accounts holds one seat (0006), and two accounts with one
  private worker each hold two of the pool's private workers.
- **Account limits below the pool.** An organization admin may give an
  account a limit below the pool, through the organization API, in
  `account_limits(account_id, limit_name, value, updated_at)`. A thing that
  would take the account above its limit, or the organization above its
  pool, is refused, whichever is reached first. The sum of account limits
  may exceed the pool, so an account limit is a ceiling, not a
  reservation.
- **Above the pool is refused.** Setting an account limit above the
  organization's value answers `422`, problem `validation-failed`, naming
  the limit, so an account limit never promises more than the agreement
  holds. A pool lowered later below an account limit leaves the limit
  stored; the pool still applies.
- **Counts are live.** Each check counts at the moment of the write in the
  creating transaction, so two accounts racing for the last seat cannot
  both take it: the count locks the organization's entitlement row
  (`SELECT ... FOR UPDATE` on `organization_entitlements`) before counting.
- **Tables.** The `features` domain owns
  `organization_entitlements(organization_id, tier, entitlement_version)`,
  the organization's grants and `account_limits`. They carry the
  organization's and account's IDs without a foreign key, because
  `features` sits below `access`, which owns the organizations and accounts
  (0004); `access` creates and deletes the rows through `features`' `api/`
  in the transaction that creates or deletes the organization or account.

### Billing

`billing` is the feature that turns entitlement enforcement on. It belongs
to the installation, not to a tier, and it is off by default. While it is
off, no entitlement check runs and every organization, and so every
account, behaves as Enterprise: every feature not in `features.disabled`,
and no limits. Tiers, organization tiers and grants can still be edited, so an operator can prepare them
before switching billing on.

Organizations, tiers, the billing switch, organization tiers and grants
are data, managed only through the operator API: its own OpenAPI document,
`api/operator.openapi.yaml`, served under `/operator/v1`, outside the parity surface because Spacelift has
no counterpart, and reachable only by operator principals (decision 0006
issues their credentials; any other credential answers `401
unauthenticated`). `infraweavectl operator` wraps it with `organization`,
`tier`, `grant` and `billing` commands:

- **`organization create`** takes the organization's slug and name and its
  first account's slug and name, and creates both in one transaction, with
  the default tier and the break-glass credential (0006, 0007).
  **`organization delete`** deletes the organization and all its accounts.
  **`organization break-glass set`** replaces the break-glass credential.
- **`tier`** and **`grant`** edit tiers and an organization's tier and
  grants; **`billing`** sets the switch.

A change takes effect without a restart: each write bumps
`organization_entitlements.entitlement_version` for the organization it touches and
notifies `infraweave_invalidate`, and replicas rebuild their per-organization
entitlement snapshot, which every account of the organization reads, as
they do for authorization (decision 0007). Deleting a tier that an
organization holds answers `409 invalid-state`.

### The organization API

The `Organization` tag of the served deploy API is the organization admins'
API ([0007](0007-authorization-spaces-and-roles.md#tenancy)), outside the
parity surface because Spacelift has no level above the account. Its
operations take `ORGANIZATION_ADMIN` with the subject `organization`, accept
the organization principals of decision 0006, and belong to the
`organization` capability:

| Resource | Operations |
|---|---|
| The organization | Read; rename; MFA enforcement and the session lifetime (0006) |
| Accounts | List, create (naming the first account admin), claim an unclaimed account, rename, delete |
| Members | List, deactivate, open a link between two members (0006), grant and revoke `organization-admin` |
| Identity providers | List every provider and its kind; create, update and delete them; choose the default per kind |
| Organization keys | List, create, disable, delete |
| Limits and usage | Read the pooled limits and their use; set and clear account limits; usage across the organization's accounts, with no stacks, runs, state or secrets |
| Audit | The organization's audit trail ([0015](0015-events-notifications-webhooks-audit.md#audit-trail)) |

An account route never accepts an organization token, and an organization
route never reveals an account's data.

### What callers see

One chi middleware in `internal/features` runs after authentication and
before the handler. It checks the operation's capability; the handler's
input validation checks options; the domain that creates a counted thing
checks limits.

| Slug (decision [0022](0022-api-errors-and-update-semantics.md)) | When | Body members |
|---|---|---|
| `feature-disabled` (403) | The feature is in `features.disabled` | `feature` |
| `feature-not-in-tier` (403) | Billing is on and the organization's entitlement lacks the feature | `feature`; `tiers`, the names of the tiers that include it, smallest first (fewest features, then name) |
| `limit-reached` (403) | Billing is on and creating the thing would take the account above its account limit or the organization above its pool | `limit`; `scope`, `organization` or `account`, whichever was reached; `value`, that scope's limit |

- `feature-disabled` wins over `feature-not-in-tier`: the operator's
  switch-off is the cause a caller can do nothing about.
- An option is checked only when a write changes the field to the gated
  value, so a `PATCH` of another field of an aggregated-checks integration
  still succeeds after a downgrade.
- `public-worker-slots` is a concurrency limit: a run beyond it waits in the
  queue rather than failing (decision 0008 schedules the public pool).
- `ai-budget` counts spend in its period and resets when the period ends.
  A request that would spend beyond it answers `limit-reached` like any
  other limit, so a caller sees one problem type for every tier limit.
- A worker that reconnects under its existing registration is not a new
  private worker.

### Downgrade and upgrade

A change of tier, a removed grant or a new switch-off deletes nothing.

- **Gated background work pauses.** A schedule of a feature outside the
  entitlement does not fire, and a queued job of such a feature is completed
  as skipped at dequeue (decision 0011), logged and counted in
  `infraweave.features.skipped`, with no retry.
- **Stored option values stay.** While the organization lacks the option,
  infraweave behaves as if the field held its default.
- **Over a limit, nothing stops and nothing is added.** Existing users,
  keys and workers keep working; creating another answers `limit-reached`
  until the count is below the limit.
- **Upgrading resumes.** Schedules fire again from their next occurrence;
  missed firings are not caught up, so an upgrade cannot start a burst of
  runs.

A Business organization with five schedules and four private workers, all
in one account, moves to Starter+:

| Item | On Starter+ | Back on Business |
|---|---|---|
| The five schedules | Kept; none fires. Reading or changing them answers `feature-not-in-tier` with `feature: scheduling` and `tiers: [Business, Enterprise]` | Fire again from their next occurrence |
| The four private workers | All four stay registered and take runs. A fifth registration answers `limit-reached` with `limit: private-workers`, `scope: organization`, `value: 2`, and so does a new one after two leave, since two is not below two | The limit is 3, so a new registration succeeds once fewer than three are registered |

### Vendor-bound operations

An operation backed by a service only Spacelift operates never pretends to
succeed. infraweave offers an operator-supplied equivalent where the
protocol allows one, answers from its own data where the operation is a read
with an honest local meaning, and otherwise refuses with
`installation-unsupported` (403); decision 0022 lists it.

| Operation or service | infraweave's answer |
|---|---|
| `tier` | A tier object in place of the `BillingTier` enum: `name`, `features` (sorted names) and `limits` (a map from limit name to value; an absent limit has no limit). With billing off, the Enterprise tier |
| `tierFeatures` | The organization's entitlement, as infraweave feature names |
| `seats` | The organization's pooled `seats` limit, or no limit, with the organization's count in use; the account's limit when one is set, with the account's count |
| `usage` and the usage CSV export | Computed from infraweave's own run and worker records over a trailing 30-day window, which is the period `usage` covers on Spacelift; there is no billing period ([19 section 11](../reference/19-events-notifications-audit.md#11-usage-data-and-usage-export)) |
| `billingSubscription`, `onTrialUntil` | `null` |
| `billedExternally` | `true`: no Stripe subscription exists |
| `availableBillingAddons`, `availableSelfServicePlans` | Empty lists |
| `billingSubscriptionCreate`, `billingSubscriptionUpdateV2`, `billingSubscriptionDelete`, `billingSubscriptionUpdateInfo`, `billingSubscriptionUpdateTier` | `installation-unsupported`. An organization's tier changes through the operator API |
| `debugInfo` | infraweave's commit and version; an empty licence |
| Usage reporting to Spacelift | `installation-unsupported`: there is no recipient ([0015](0015-events-notifications-webhooks-audit.md)) |
| The Spacelift-provided AI integration | Decided in [0019](0019-ai-provider-boundary.md) |
| The GitHub Marketplace app | An operator-registered GitHub App, as self-hosted Spacelift requires (decision 0012) |
| The Azure integration's managed Entra applications | Created through an operator-registered Entra application in `cloud.azure`, which needs permission to create applications in its tenant. Without one, `azureIntegrationCreate` answers `installation-unsupported` |
| The public worker pool | An optional pool the operator runs for every organization of the installation, with `public-worker-slots` pooled per organization ([0008](0008-run-execution-and-worker-protocol.md#pools), [0020](0020-reference-deployments-and-packaging.md)) |

### Mapping from `BillingTierFeature`

Each value maps to the infraweave features that cover it, so parity stays
traceable. The `tier-feature:` records of
[capabilities.json](../../data/capabilities.json) carry the same mapping for
the catalogue, and a test checks that every live value has one.

| `BillingTierFeature` | infraweave feature | Kind |
|---|---|---|
| `ACCESS_POLICIES` | none: superseded (S1), space role bindings replace it | |
| `ADDITIONAL_PUBLIC_WORKER` | a grant raising `public-worker-slots` | limit |
| `ADVANCED_SCHEDULING` | `scheduling` | capability |
| `AGGREGATED_VCS_CHECKS` | `aggregated-vcs-checks` | option |
| `API_KEYS_OIDC` | `oidc-api-keys` | option |
| `AUDITING` | `audit-trail` | capability |
| `AUDIT_WEBHOOKS` | `audit-trail`: superseded (S2) by `AUDITING` | capability |
| `BLUEPRINTS` | `blueprints`, `templates` | capability |
| `CAN_UPDATE_SSO_SAML` | `saml-sso` | capability |
| `CLOUD_CREDENTIAL_GENERATION` | `cloud-integrations` | capability |
| `CUSTOM_SSO_SAML` | `saml-sso` | capability |
| `DRIFT_DETECTION` | `drift-detection` | capability |
| `MFA` | `mfa` | capability |
| `MULTIPLE_VCS` | `multiple-vcs-integrations` | option |
| `NOTIFICATION_POLICIES` | `notification-policies`, `notifications` | option, capability |
| `PRIVATE_MODULE_SHARING` | `module-sharing` | capability |
| `PRIVATE_VCS` | `vcs-agent-pools` | capability |
| `PRIVATE_WORKERS` | `private-worker-pools`, `private-workers` | capability, limit |
| `RUN_STATE_CHANGE_WEBHOOKS` | `webhooks` | capability |
| `TARGETED_REPLAN` | `targeted-replan` | capability |
| `TERRAFORM_PROVIDER_VERSIONS` | `terraform-provider-registry` | capability |
| `UNLIMITED_SEATS` | no `seats` value | limit |
| `WORKFLOW_POLICIES` | `policies` | capability |
| `OIDC_FEDERATION_TOKEN_GENERATION` | `oidc-federation` | capability |
| `RUN_IGNORED_TRIGGERS` | `ignored-runs` | capability |
| `ACCOUNT_AI_CONTROL` | `ai-account-controls` | capability |
| `SPACELIFT_INTELLIGENCE` | `ai-integrations`, `intent` | capability |
| `SERVICE_NOW_INTEGRATION` | `servicenow` | capability |
| `BACKSTAGE_INTEGRATION` | `backstage` | capability |
| `SCIM` | `scim` | capability |
| `SCANS` | `scans` | capability |

The first assignment of operations to capabilities starts from report 26's
pairing, which is by name [I], and captures C14, C121 and C122 correct it.

### Docs against clients (P4)

Where Spacelift's docs and its released clients disagree, a behaviour the
server enforces, or that the user relies on for security, follows the docs.
infraweave's own clients replace Spacelift's (decision 0001), so for
client-local behaviour infraweave chooses on merit. It prefers the strict
option and never picks silently between conflicting inputs.

| Case | Docs | Client source | infraweave |
|---|---|---|---|
| CLI credential order when several are set ([06 section 2.3](../reference/06-spacectl-and-clients.md#23-credentials-and-sessions)) | token, GitHub, API key | token, API key, GitHub | Two methods set without `INFRAWEAVE_API_PREFERRED_METHOD` is an error naming both; one method set is used as is |
| CLI profile storage (same section) | a file per alias and a `current` link | one `config.json` | One strict YAML file, `$XDG_CONFIG_HOME/infraweave/config.yaml` (`%AppData%` on Windows), with `current_profile` and a `profiles` map, written `0600` in a `0700` directory |
| VCS agent request validation ([11 section 14.6](../reference/11-vcs-integrations.md#146-what-it-proxies-request-validation)) | a strict allowlist | no validation at the pin | The allowlist is on by default; decision 0012 records the agent's flags |

### Self-hosted limits are not inherited

infraweave is self-hosted, but it mirrors SaaS. It keeps the Azure
integration, the public pool as an option, and hosted-style AI defaults
where [0019](0019-ai-provider-boundary.md) allows them. FedRAMP shows the
inverse constraint: no capability may depend on the public worker pool
([26 section 2.4](../reference/26-scope-and-tiers.md#24-fedramp)). Every
acceptance feature that runs work therefore runs it on a private pool.

## Rationale

- **P2, option (b) with a lifecycle, not (a) or (c).** Building an interface
  that is superseded before infraweave reaches it is cost with no user,
  because infraweave's clients are its own (decision 0001). Removing an
  interface that infraweave already serves breaks those clients, though, so
  it follows Spacelift's own deprecation and removal dates rather than
  infraweave's. Option (c), storing `ACCESS` policies without acting on
  them, is rejected: a stored access policy reads as protection it does not
  give.
- **The rule-based test, not "deprecated means excluded".** Deprecation is
  advisory where the schema still lists an element, and the docs and schema
  rank equally, so a disagreement keeps the element live. S3 exists because
  a transport, a path or a client has no schema element, and the owner of
  the interface is the only authority on it.
- **P3, option (a), not stubs.** A 501 is not delivery, and the scope rule
  has no "until documented" clause. The `schema-only` basis keeps the
  weaker evidence visible instead of hiding it behind a stub.
- **P5, tiers as data with per-feature switches, not (a) alone or a fixed
  edition setting.** A gate added after clients and data exist is a breaking
  change, so every feature carries a tier from the start. Tiers as data let
  an operator who hosts infraweave for others package it as Spacelift does,
  or differently, without a release; a fixed edition setting could do
  neither, and could not express a single policy switch such as "no AI".
  Per-feature switches keep that policy switch, and give clients and tests
  one real refusal path.
- **Billing off by default.** Most installations serve one company, with
  no commercial relationship between operator and organizations. With billing off
  they run no entitlement checks and see every feature, which is what the
  maintainer asked for, while the gates exist and are tested for those who
  turn billing on.
- **Seeded from Spacelift's documented plans.** Capability parity covers the
  gates too: an operator who switches billing on gets Spacelift's packaging
  without writing it. Where Spacelift publishes nothing, a gate infraweave
  invented would be a restriction with no source, so the feature goes in
  every tier until a capture says otherwise.
- **infraweave's names, with a mapping.** `BillingTierFeature` mixes
  products, quantities, add-ons and a deprecated value, and its spellings
  name Spacelift. Kinds and names of infraweave's own make every gate one of
  three checks; the mapping table keeps each Spacelift value traceable.
- **Every operation in exactly one capability.** A test can then prove that
  nothing is ungated, and an operator can predict what a switch-off does.
- **Downgrade deletes nothing.** A tier change is often temporary or a
  mistake. Pausing work and refusing growth keeps data and running
  infrastructure intact, and an upgrade needs no restore.
- **403 for both new slugs.** Neither is transient: retrying does not help
  until an operator changes the organization, which 429 or 409 would not
  convey.
- **The tier on the organization, the pool across its accounts.** The tier
  is the agreement, and one agreement covers several business entities, so
  entitlement belongs where the agreement is. Pooling the limits charges
  the agreement once for a member who works in two entities, and account
  limits let the organization divide its pool without the operator.
- **An organization API in the served API, an operator API apart.** What
  an organization admin manages is the customer's own data and belongs on
  the customer's surface, under a capability of its own; what the operator
  manages spans customers and stays in a document no customer credential
  reaches.
- **Refusal over simulation.** A fake billing or Entra success looks like
  progress in a test and fails in production. `installation-unsupported`
  is explicit and testable.
- **P4, option (c) with a default.** Following the docs everywhere would
  copy a profile layout that the released CLI no longer uses. Following the
  clients everywhere would copy an agent that validates nothing. A rule
  plus a recorded case keeps both kinds of choice reviewable.
- **Departure from Spacelift:** `tier` returns a tier object instead of a
  `BillingTier` value, and `tierFeatures` returns infraweave feature names.
  The enum names Spacelift's plan generations and cannot express a tier an
  operator defines.
- **Departure from Spacelift:** billing is off by default, so every
  organization behaves as Enterprise; tiers and grants are managed through an operator
  API that has no Spacelift counterpart.
- **Departure from Spacelift:** the tier, grants and limits belong to the
  organization, above the account, and the `Organization` tag has no
  Spacelift counterpart.
- **Departure from Spacelift:** the billing mutations refuse; reads answer
  with local meaning.
- **Departure from Spacelift:** the Azure integration, Origin and global
  plugin templates depend on operator-registered apps and catalogues
  instead of Spacelift's own.
- **Departure from Spacelift:** a superseded enum value is rejected outright
  rather than accepted in a disabled state.
- **Departure from Spacelift:** the CLI refuses ambiguous credentials, and
  stores its profiles as one YAML file.

## Constraints

- **Inferred gate pairing.** The pairing of operations with
  `BillingTierFeature` values, and so the first assignment of operations to
  capabilities, is inferred by name. Until captures C14, C121 and C122
  resolve, some operations may sit in the wrong capability; the effect is
  limited to installations that switch billing on or disable features.
- **Seed gaps.** A plain Starter tier is published only in fragments, and
  the pricing page gives Starter one private worker while the docs place
  private worker pools from Starter+; the seed follows the docs, which rank
  higher. Free's separate API key count is unpublished and not seeded. C260
  and C261 correct the seed; a correction changes data, not the design.
- **Input deprecations.** Input deprecations rest on the changelog until
  the snapshot fetcher requests them (P15).
- **Unverified operator equivalents.** Whether Origin supports
  customer-registered apps is unverified (C11). If it does not, Origin is
  refused on every installation, and the gap is recorded in report 26.
- **Superseded data in imports.** Data that uses superseded interfaces
  (legacy AWS integrations, the `legacy` space, access-policy residue)
  reaches infraweave only through the importer. The importer's design is
  outside this record.

## Verification

- **`TestUnitServedDeprecations`** (`api/`) fails when an operation in the
  served spec matches a reference operation that `coverage.md` excludes, or
  that the schema deprecates, without `deprecated: true` in the served spec.
  The weekly update's pull request therefore cannot merge a new deprecation
  silently.
- **`TestUnitSupersededEnums`** fails when a served enum contains a value
  that the schema marks deprecated or no longer used, unless the value is
  listed as live-but-deprecated in this record.
- **`TestUnitFeatureCatalogue`** (`internal/features`) fails when:
  - an operation of a served OpenAPI document has no `x-infraweave-feature`,
    or one that names anything but a capability;
  - a gated schema property names anything but an option, or an option's
    catalogue field does not exist;
  - a live `BillingTierFeature` value of the snapshot has no mapping;
  - the seeded Enterprise tier lacks a feature or holds a limit.
- **`TestUnitEntitlement`** (`internal/features`), table-driven: tier plus
  grants minus switch-offs, requirements, limit grants above and below the
  tier, and billing off; every account of an organization reads the same
  entitlement.
- **`TestAccPooledLimits`** (`internal/features`, PostgreSQL): two accounts
  of one organization share the `seats` and `private-workers` pools, a
  member admitted by both holds one seat, an account limit refuses before
  the pool does with `scope: account`, an account limit above the pool
  answers `422`, and two concurrent creates for the last seat let one
  through.
- **`TestUnitFeaturesConfig`** (`internal/features`) covers strict decoding
  of `features.disabled`, and the rejection of an unknown name, of
  `billing`, and of `SCANS`.
- **`test/acceptance/features/platform_features.feature`** (`@postgres`),
  billing off:
  - `tier` answers the Enterprise tier object and `tierFeatures` every
    feature;
  - a disabled `ai-integrations` answers 403 `feature-disabled` on an AI
    route and leaves other routes untouched;
  - a disabled `flows-engine` answers `feature-disabled` on Flows routes;
  - a skipped background job.
- **`test/acceptance/features/platform_tiers.feature`**, billing on:
  - an account whose organization is on Starter+ creating a schedule answers
    `feature-not-in-tier` naming Business and Enterprise;
  - a seat beyond the limit answers `limit-reached`;
  - the Business to Starter+ downgrade above, then the upgrade back;
  - a tier change applies to a running server without a restart, in every
    account of the organization;
  - an account credential on the operator API answers 401;
  - `organization create` creates the organization with its first account,
    unclaimed, and the default tier; an organization token reaches the
    `Organization` tag and no account route.
- **`test/acceptance/features/platform_billing.feature`**: every Billing
  operation answers as the vendor-bound table says, and each mutation
  answers 403 `installation-unsupported`.
- **`test/acceptance/features/platform_deprecation.feature`**: a
  deprecated-but-live operation, such as creating an `INITIALIZATION`
  policy, carries `Deprecation`; a superseded value, such as policy type
  `ACCESS`, fails validation with the replacement named.
- **The availability-state check.** `specref parity --states --check`
  ([0016](0016-testing-and-acceptance.md)) fails when a record of
  [capabilities.json](../../data/capabilities.json) claims a state the tests
  do not show. It also fails on:
  - a `superseded` row without its four recorded items;
  - a `schema-only` operation without `x-infraweave-basis`.

## References

- [README: decisions fixed before writing](../README.md#decisions-fixed-before-writing),
  [sources and ranking](../README.md#target-version-and-sources).
- [26: availability states](../reference/26-scope-and-tiers.md#12-availability-states),
  [plan vocabulary](../reference/26-scope-and-tiers.md#13-plan-vocabulary),
  [editions](../reference/26-scope-and-tiers.md#2-editions),
  [`BillingTierFeature`](../reference/26-scope-and-tiers.md#3-billingtierfeature),
  [API tags](../reference/26-scope-and-tiers.md#4-capability-table-api-tags),
  [supersession boundaries](../reference/26-scope-and-tiers.md#7-supersession-boundaries).
- [30: product decisions](../reference/30-open-questions.md#product-decisions)
  P2, P3, P4, P5 and P15; captures C11, C14, C121, C122, C260 and C261.
- [01: what is out of scope](../reference/01-spacelift-landscape.md#13-what-is-out-of-scope).
- [09: deprecated policy types](../reference/09-policies.md#12-deprecated-types-and-their-replacements).
- [14: API key fields](../reference/14-spaces-rbac-and-identity.md#81-types-and-fields).
- [22: the MCP endpoint](../reference/22-intelligence.md#71-endpoint-and-transport).
- [GLOSSARY](../../../GLOSSARY.md): feature, capability, option, limit,
  tier, grant, entitlement, billing, installation, root, organization,
  account, member, operator.
- Decisions 0001, 0002 and 0003; decisions 0004, 0006, 0007, 0008, 0011,
  0012, 0013, 0019 and 0022, written alongside this one.
- [RFC 9745](https://www.rfc-editor.org/rfc/rfc9745) (`Deprecation`),
  [RFC 8594](https://www.rfc-editor.org/rfc/rfc8594) (`Sunset`).
