# Capability roadmap

This is the feature catalogue's view of the plan: every feature of
infraweave, with its kind, the lowest seeded tier that holds it and the
phase of the [implementation plan](plan.md) that builds it. Every one of
the 53 tags of the Spacelift (Deploy) reference and its 8 untagged
operations, each of the 5 tags of the Flows reference, each
of the 56 resources and 62 data sources of the Terraform provider, each
gated option and each of the 31 `BillingTierFeature` values maps to a
feature. Flows, the console and every IaC vendor are features like any
other. Scope and availability states come from report
[26](../research/reference/26-scope-and-tiers.md); the scope rule, the
catalogue and the seeded tiers are decision
[0021](../research/decisions/0021-capability-scope-and-supersession.md)'s.
What is not built, and why, is the [supersession register](#supersession-register)
at the end.

The data is in three registers, and this page says how to read them:

- [docs/data/features.json](../data/features.json), the feature catalogue:
  one record per feature, its id the feature's name.
- [docs/data/capabilities.json](../data/capabilities.json): one record per
  reference row, with report 26's research in its `spacelift` member and
  this roadmap's columns in its `infraweave` member.
- [docs/data/supersessions.json](../data/supersessions.json): what is left
  out as superseded, and what stays served though deprecated.

[tiers.md](tiers.md), which `make generate` writes from the first two, sets
the features against the seeded tiers and counts the rows each phase owns.

## How to read this roadmap

- **Feature** names the catalogue entry that gates the row, by the id of
  its record in features.json. A feature is a capability (a set of
  operations), an option (one value of a field) or a limit (a quantity).
  The register is the catalogue's one source: `make generate` writes
  `internal/features/catalogue.gen.go` from it, `make verify-generate`
  fails when that file is stale, and `TestUnitFeatureCatalogue` fails when
  an operation, a gated property or a live `BillingTierFeature` value has
  no entry.
- **Lowest seeded tier** (`lowestTier`) is the smallest of the five seeded
  tiers (Free, Starter, Starter+, Business, Enterprise) that holds the
  feature, placed from report 26's plan column by decision 0021's rules.
  Free means every seeded tier. A tier belongs to an organization, and every
  account of the organization inherits it with the organization's grants.
  Tiers apply only while billing is on; with billing off, the default, every
  organization behaves as Enterprise. An operator can change every seeded
  tier except Enterprise, add tiers, grant a feature to one organization,
  and switch a feature off for the installation with `features.disabled`.
- **Phase** (`phase`) is the owning phase: the phase that delivers the
  row's core and whose exit makes the row `implemented` under decision
  [0016](../research/decisions/0016-testing-and-acceptance.md)'s evidence rule.
  **Also** (`also`) names the phases that deliver some of a tag's
  operations, each with the subject that owns them (a setting, an
  attachment, a search).
- **API phase** and **Provider phase** (`apiPhase`, `providerPhase`) split
  each Terraform row: the API phase serves the core operations the resource
  or data source calls, and P24 builds the resource in infraweave's own
  provider, named with the `infraweave_` prefix. Where a row also calls an
  operation of a later phase, its notes name that phase. A Terraform row is
  `implemented` only when all of them are done; P24 depends on every one.
- **State** (`state`) is the availability state the record claims; at the
  research baseline, 2026-10-28, it is report 26's computed state.
  `specref parity --states` computes the current state, and its `--check`
  fails when a record claims more than the passing scenarios show.
- Every phase a record of the three registers names, outside a capability's
  `spacelift` member, is a phase of [plan.md](plan.md); `make docs-check`
  fails otherwise.
- A record moves phase only in the pull request that changes the plan.

## Feature catalogue

The catalogue is [features.json](../data/features.json), and
[tiers.md](tiers.md#features-by-lowest-seeded-tier) lists it by lowest
seeded tier.

### Capabilities

Every operation of every served document carries `x-infraweave-feature`
naming exactly one capability. A capability that covers the rest of an API
tag takes the tag's name. Search suggestion operations belong to the
capability of the search each completes. A capability record's `gates`
says what it covers, and `requires` names the features it needs: an
organization whose entitlement lacks a required feature lacks the
capability too.

### Options

An option gates one value of a field; its schema property carries
`x-infraweave-feature`, and a write that sets the gated value outside the
entitlement answers `403 feature-not-in-tier`. An option record's `field`
names the field and the gated value.

### Limits

A limit is checked by the domain that creates the counted thing and answers
`403 limit-reached` beyond it. Every limit is pooled: it counts its thing
across all of an organization's accounts, and an organization admin may set
an account limit below the pool for any account, which answers the same
refusal while the pool still has room. A limit record's `values` holds its
value in each seeded tier, and a tier with no value has no limit
([tiers.md](tiers.md#seeded-limits)).

### The installation's switch

`billing` is a feature of the installation, not of a tier, and is off by
default. It turns entitlement enforcement on, and the operator API sets it
(P3b). `features.disabled` rejects it, as it rejects any unknown name.

## Summary by phase

[tiers.md](tiers.md#summary-by-phase) counts the reference rows each phase
owns, both products' tags together in its Tags column. The phases that own no row (P0 to P2c, P7, P8b, P13b, P18d, P20, P24,
P24b, P24c and P28) build foundations, behaviour behind other rows, features
the reference has no row for, or the provider and clients. Superseded counts
the Terraform rows that no phase builds; the two superseded
`BillingTierFeature` values count under P3b, which leaves them out of the
catalogue.

## Reference rows

[capabilities.json](../data/capabilities.json) holds one record per row of
the references, and the record's id says which kind of row it is. Each
record names its `product`: `flows` for the tags of the Flows reference,
`deploy` for every other row, including the Deploy-side `Flows` tag that
links a Deploy account to Flows.

- `tag:<Tag>`: one per tag of `api/reference/spacelift/spacelift.openapi.yaml`
  (`deploy`) and of `api/reference/spacelift-flows/flows.openapi.yaml`
  (`flows`, every tag starting `Flows `), in name order. `ops` is the tag's
  operation count in its reference. Where a tag holds operations of more
  than one feature, `feature` names each and `lowestTier` names the tier of
  each finer one. `make docs-check` fails when a tag of a reference has no
  record of its product, or a record no tag in its product's reference.
- `operation:<graphqlField>`: the 8 reference operations that carry no tag.
- `resource:<spacelift_x>` and `data-source:<spacelift_x>`: one per
  resource and data source of terraform-provider-spacelift v1.57.0, by its
  Spacelift name; infraweave's provider serves each as `infraweave_<name>`.
  Flows resources, named `infraweave_flows_<name>`, follow
  `terraform-provider-flows` and land in P24 with the rest.
- `tier-feature:<VALUE>`: one per value of the `BillingTierFeature` enum,
  with the infraweave features that cover it (decision 0021's mapping,
  which the catalogue carries and `TestUnitFeatureCatalogue` checks). The
  phase is where the feature is delivered; P3b delivers the catalogue, the
  `x-infraweave-feature` annotations and `tierFeatures` for all of them.
  `features.disabled` takes infraweave's names only and rejects these
  spellings.

The served `Organization` tag has no reference counterpart, so it has no
record: the `organization` capability gates it, P3b serves it, P18b adds
run and worker usage per account, and P23 adds identity providers and
identity linking.

## Supersession register

Decision [0021](../research/decisions/0021-capability-scope-and-supersession.md)
leaves an interface out only when a supersession rule holds: S1 retired, S2
schema-deprecated with a replacement infraweave serves, S2a a shadowed `V1`
twin, or S3 deprecated, with a named replacement, by the source that owns an
interface the schema does not hold. The `SUP-` records of
[supersessions.json](../data/supersessions.json) are those interfaces, and
report [26](../research/reference/26-scope-and-tiers.md#7-supersession-boundaries)
section 7 holds the evidence for each. No phase builds them; a record's
`replacementPhase` names the phase that serves the need instead, so a
reader can find it. How the importer from Spacelift treats data that uses
them is outside decision 0021 and is not planned here.

Four root fields are left out without being superseded: `groupUserEvent`,
`identifyUserEvent`, `pageUserEvent` and `trackUserEvent` are product
analytics Spacelift's web interface sends, not an API of the platform
(report 26 section 7.1).

Operations backed by a service only Spacelift operates (billing, usage
reporting to Spacelift, the GitHub Marketplace app) are not superseded: they
are served, and answer from infraweave's own data or with
`installation-unsupported`, as decision 0021's vendor-bound table sets out.
Their phases are those of their tags.

### Deprecated but served

The `DEP-` records of [supersessions.json](../data/supersessions.json) stay
served and carry `deprecated: true`, because the schema or a decision keeps
them live (decision 0021, "Deprecated and superseded candidates"). Each
names the phase that serves it and why it stays.
