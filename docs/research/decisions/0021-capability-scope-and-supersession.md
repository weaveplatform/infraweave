# 0021: Capability scope and supersession

Status: Proposed

## Context

The research set fixes one scope rule before any design: infraweave mirrors
everything Spacelift offers, at every tier, and leaves out only an interface
that a newer Spacelift interface supersedes, with evidence
([README](../README.md#decisions-fixed-before-writing)). Report 26 applies the
rule row by row. It gives every tag, Terraform resource and data source an
availability state, and lists the exclusions it found in its
[supersession boundaries](../reference/26-scope-and-tiers.md#7-supersession-boundaries).
The rule itself, and four product questions that depend on it, are still
open:

| Question | Report 30 entry |
|---|---|
| Which deprecated or superseded interfaces does infraweave serve? | P2 |
| Are capabilities known only from the schema and Bruno (scans, Origin, `BLOB`, the server side of plugin templates) built? | P3 |
| Where Spacelift's docs and its own clients disagree, which does infraweave's CLI (`infraweavectl`) follow? | P4 |
| Does infraweave gate features by Spacelift's editions and its 31 `BillingTierFeature` values? | P5 |

Four facts shape the answers:

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
- **Clients read tier data.** `tier` and `tierFeatures` are part of the
  surface, and what they return on a self-hosted install is undocumented
  (C122).

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
| S1 Retired | Spacelift removed or disabled it for every account, or the schema says it is no longer used | `ACCESS` policies (disabled 2026-05-30); the stack `administrative` flag; `RunState.PENDING`; `WebhooksIntegration.secret`; the `STACK_WEBHOOK_*` actions marked disabled |
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
| `implemented` | Every operation of the row is served with behaviour, and meets [0016](0016-testing-and-acceptance.md)'s evidence rule: passing scenarios against real dependencies for a success, a denial and a failure path. Its feature gate is wired. Every capture that blocks it is resolved to a fixture, or its behaviour is recorded as infraweave's choice in a decision record |
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
| `ApiKeyInput.admin` | S2, through the changelog | Not accepted. Admin keys come from a root-space admin role binding (decision 0007) |
| `RunState.PENDING` | S1 | Absent from the served enum |
| `TerraformWorkflowTool.OPEN_TOFU` | live in the schema | Served, marked deprecated for stacks only, since modules and Terragrunt still need it |
| `INITIALIZATION` and `TASK` policy types | live in the schema | Served and marked deprecated. The aliases `TASK_RUN` (of `TASK`) and `TERRAFORM_PLAN` (of `PLAN`) are S2 and not served ([26 section 7.3](../reference/26-scope-and-tiers.md#73-policy-types)) |
| MQTT worker transport | S3 | Not served: long-poll is the replacement and the self-hosted v7.1.0 release deprecates the built-in broker too ([10 section 4.2](../reference/10-workers-and-execution.md#42-transports)). Decision 0008 owns the worker protocol and answers P6 |
| `pulumi-spacelift` | S3 | No infraweave Pulumi provider. Pulumi users bridge infraweave's Terraform provider ([06 section 7.1](../reference/06-spacectl-and-clients.md#71-pulumi-provider)) |

### Schema-only capabilities (P3)

These are in scope, built to the schema's shape, with `basis: schema-only`.
Each behaviour infraweave chooses where the sources are silent is recorded in
the owning decision. When capture C11 or a docs page later describes the
behaviour, it replaces infraweave's choice through the lifecycle above.

| Capability | Built as |
|---|---|
| Scans ([21 section 5](../reference/21-resources-scans-search.md#5-scans)) | Scan configurations and scan runs executed on workers like any run, recording discovered resources against managed entities; gated by `SCANS` |
| `BLOB` source provider ([11 section 10](../reference/11-vcs-integrations.md#10-other-source-providers-in-the-enum)) | Commit archives read from S3 or S3-compatible storage with the schema's three authentication methods; the access key is sealed (decision 0013) |
| Origin integration (same section) | Spacelift backs it with its own global app. infraweave reads an operator-registered Origin app from `vcs.origin` configuration; without one, `originIntegrationInstallURL` answers `installation-unsupported` |
| Plugin templates, server side ([25 section 2](../reference/25-other-integrations.md#2-plugins)) | Custom templates as the schema describes. Global templates are loaded from `spacelift-io/plugins` (MIT) at a pinned commit, with its notice under decision 0002's rules, plus any directory the operator adds in `plugins.catalog_paths` |

### Vendor-bound operations

An operation backed by a service only Spacelift operates never pretends to
succeed. infraweave offers an operator-supplied equivalent where the
protocol allows one, answers from its own data where the operation is a read
with an honest local meaning, and otherwise refuses with
`installation-unsupported` (403); decision 0022 lists it.

| Operation or service | infraweave's answer |
|---|---|
| `tier` | `V5_ENTERPRISE_PLUS`, the edition that offers self-hosting ([26 section 2.1](../reference/26-scope-and-tiers.md#21-saas-plans)) |
| `tierFeatures` | Every `BillingTierFeature` value that is not superseded and not in `features.disabled` |
| `seats` | No limit, with the count of users in use |
| `usage` and the usage CSV export | Computed from infraweave's own run and worker records over a trailing 30-day window, which is the period `usage` covers on Spacelift; there is no billing period ([19 section 11](../reference/19-events-notifications-audit.md#11-usage-data-and-usage-export)) |
| `billingSubscription`, `onTrialUntil` | `null` |
| `billedExternally` | `true`: no Stripe subscription exists |
| `availableBillingAddons`, `availableSelfServicePlans` | Empty lists |
| `billingSubscriptionCreate`, `billingSubscriptionUpdateV2`, `billingSubscriptionDelete`, `billingSubscriptionUpdateInfo`, `billingSubscriptionUpdateTier` | `installation-unsupported` |
| `debugInfo` | infraweave's commit and version; an empty licence |
| Usage reporting to Spacelift | `installation-unsupported`: there is no recipient ([0015](0015-events-notifications-webhooks-audit.md)) |
| The Spacelift-provided AI integration | Decided in [0019](0019-ai-provider-boundary.md) |
| The GitHub Marketplace app | An operator-registered GitHub App, as self-hosted Spacelift requires (decision 0012) |
| The Azure integration's managed Entra applications | Created through an operator-registered Entra application in `cloud.azure`, which needs permission to create applications in its tenant. Without one, `azureIntegrationCreate` answers `installation-unsupported` |
| The public worker pool | An optional, operator-run shared pool ([0020](0020-reference-deployments-and-packaging.md)) |

### Features and tiers (P5)

infraweave has no editions and no commercial gates. Every capability is
available on every install. The operator can turn features off:

```yaml
features:
  disabled: [SPACELIFT_INTELLIGENCE, SCANS]
```

- **Values.** `features.disabled` takes `BillingTierFeature` names. Strict
  decoding rejects an unknown or superseded name (`ACCESS_POLICIES`,
  `AUDIT_WEBHOOKS`), so a typo cannot silently leave a feature on. The
  default is empty.
- **Which operations a value gates.** The served spec names the gating
  value on each operation as `x-infraweave-feature`. The first assignment
  comes from the pairing in [26 section 3](../reference/26-scope-and-tiers.md#3-billingtierfeature),
  which is by name [I], and captures C14, C121 and C122 correct it.
- **Enforcement.** One chi middleware, in `internal/features` (decision
  0004 owns placement), runs after authentication and before the handler.
  An operation gated by a disabled value answers 403 `feature-disabled`, a
  slug decision 0022 lists, with the value in the problem's `feature`
  member. Background work of a disabled feature stops: the queue (decision
  0011) discards a gated job at dequeue, logs it, and counts it in
  `infraweave.features.skipped`, with no retry.
- **Data is kept.** Disabling a feature deletes nothing. Re-enabling it
  resumes from the stored configuration. Changes take effect at restart,
  like any other configuration.
- **Flows.** Flows has no `BillingTierFeature`, so it is switched off with
  `flows.enabled: false` ([0018](0018-flows-runtime.md)), the one switch
  outside `features.disabled`. A disabled Flows answers `feature-disabled`
  on its own API routes in the same way, with no `feature` member, and the
  Deploy-side `Flows` reads answer an empty integration.
- **What the switch is for.** It serves operators with a policy reason to
  keep a feature off, such as AI or VCS agent pools, and it gives clients
  and tests one real code path for "feature not available".

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
inverse constraint: no capability may depend on a shared pool
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
- **P5, options (a) and (c) combined, not (b).** An edition setting would
  fix artificial limits into an open product, and it could not express a
  single policy switch such as "no AI". Pure option (a) leaves clients
  without a tier answer, and tests without the refusal path. Per-feature
  switches, all on by default, give both.
- **`V5_ENTERPRISE_PLUS` for `tier`.** Self-hosted Spacelift is sold at that
  level and carries the Enterprise tier's functionality. Returning a value
  that Spacelift's enum lacks would break the enum contract for one
  constant.
- **P4, option (c) with a default.** Following the docs everywhere would
  copy a profile layout that the released CLI no longer uses. Following the
  clients everywhere would copy an agent that validates nothing. A rule
  plus a recorded case keeps both kinds of choice reviewable.
- **Refusal over simulation.** A fake billing or Entra success looks like
  progress in a test and fails in production. `installation-unsupported`
  is explicit and testable.
- **Departure from Spacelift:** `tier` is a constant and `tierFeatures` is
  operator configuration, not a commercial plan.
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

- **Inferred gate pairing.** The `BillingTierFeature` pairing with
  operations is inferred by name. Until captures C14, C121 and C122
  resolve, some `x-infraweave-feature` annotations may gate the wrong
  operation; the effect is limited to installs that disable features.
- **Report 26 cells to amend.** Report 26 section 7.8 keeps the MQTT
  transport in scope for self-hosted. Under S3 it is superseded, and
  decision 0008 reaches the same answer for P6. That cell, and section
  7.7's open question on the legacy AWS mutations, are amended when this
  record is accepted.
- **Input deprecations.** Input deprecations rest on the changelog until
  the snapshot fetcher requests them (P15).
- **Unverified operator equivalents.** Whether Origin supports
  customer-registered apps is unverified (C11). If it does not, Origin is
  refused on every install, and the gap is recorded in report 26.
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
- **`TestUnitFeatureAnnotations`** fails when an `x-infraweave-feature`
  names an unknown or superseded `BillingTierFeature`, or when a tag that
  report 26 pairs with a value has no annotated operation.
- **`TestUnitFeaturesConfig`** (`internal/features`) covers strict decoding
  of `features.disabled`, and the rejection of `ACCESS_POLICIES` and of an
  unknown name.
- **`test/acceptance/features/platform_features.feature`** (`@postgres`):
  - `tier` and `tierFeatures` with nothing disabled;
  - a disabled `SPACELIFT_INTELLIGENCE` answering 403 `feature-disabled`
    on an AI route and leaving other routes untouched;
  - a skipped background job.
- **`test/acceptance/features/platform_billing.feature`**: every Billing
  operation answers as the vendor-bound table says, and each mutation
  answers 403 `installation-unsupported`.
- **`test/acceptance/features/platform_deprecation.feature`**: a
  deprecated-but-live operation, such as creating an `INITIALIZATION`
  policy, carries `Deprecation`; a superseded value, such as policy type
  `ACCESS`, fails validation with the replacement named.
- **The availability-state check.** `specref parity --states --check`
  ([0016](0016-testing-and-acceptance.md)) fails when report 26 claims a
  state the tests do not show. It also fails on:
  - a `superseded` row without its four recorded items;
  - a `schema-only` operation without `x-infraweave-basis`.

## References

- [README: decisions fixed before writing](../README.md#decisions-fixed-before-writing),
  [sources and ranking](../README.md#target-version-and-sources).
- [26: availability states](../reference/26-scope-and-tiers.md#12-availability-states),
  [editions](../reference/26-scope-and-tiers.md#2-editions),
  [`BillingTierFeature`](../reference/26-scope-and-tiers.md#3-billingtierfeature),
  [supersession boundaries](../reference/26-scope-and-tiers.md#7-supersession-boundaries).
- [30: product decisions](../reference/30-open-questions.md#product-decisions)
  P2, P3, P4, P5 and P15; captures C11, C14, C121, C122.
- [01: what is out of scope](../reference/01-spacelift-landscape.md#13-what-is-out-of-scope).
- [09: deprecated policy types](../reference/09-policies.md#12-deprecated-types-and-their-replacements).
- [14: API key fields](../reference/14-spaces-rbac-and-identity.md#81-types-and-fields).
- [22: the MCP endpoint](../reference/22-intelligence.md#71-endpoint-and-transport).
- Decisions 0001, 0002 and 0003; decisions 0007, 0008, 0011, 0012, 0013 and
  0022, written alongside this one.
- [RFC 9745](https://www.rfc-editor.org/rfc/rfc9745) (`Deprecation`),
  [RFC 8594](https://www.rfc-editor.org/rfc/rfc8594) (`Sunset`).
