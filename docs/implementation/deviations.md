# Deviations from Spacelift

infraweave reproduces Spacelift's feature set behind its own REST API
([0001](../research/decisions/0001-rest-first-spacelift-surface.md)). It is built
with Spacelift's design in view, and where Spacelift's interface does not fit a
self-hosted REST product, or where the research shows a limit worth avoiding,
infraweave deliberately does something else. The deviations register,
[`docs/data/deviations.json`](../data/deviations.json), lists every such
departure in one place, so a reader comparing infraweave with Spacelift does not
have to collect them from twenty decision records. This page says what counts
as a deviation and how to record one.

Every record is a design commitment of an Accepted decision record.
Availability is the [build status](plan.md#build-status) of the phase that
delivers it; phases in [plan.md](plan.md) cite the `DV-NNN` records their tasks
implement. A record that rests on Spacelift behaviour a capture has not yet
shown carries a `pending` member, described under
[deviations a capture may change](#deviations-a-capture-may-change).

## Scope

- **A deviation** is an infraweave behaviour or interface that differs from
  Spacelift on purpose: a different wire shape, a different default, a stricter
  rule, an added operation or header, or a different deployment artefact.
- **Operation-level divergences** are also recorded in the served spec: each
  operation that differs from the reference lists its differences in
  `x-infraweave-divergence`, and `specref parity` fails on a difference without
  one ([0016](../research/decisions/0016-testing-and-acceptance.md#parity-checks-against-the-specification-reference)).
  This register holds the rule; the spec holds each operation's instance of it.
- **Not deviations:** interfaces infraweave does not serve because a newer
  Spacelift interface supersedes them. They follow
  [0021](../research/decisions/0021-capability-scope-and-supersession.md)'s
  supersession test and live in the
  [supersession register](capability-roadmap.md#supersession-register). Missing
  capabilities and 501 stubs are tracked by their owning phase in the
  [capability roadmap](capability-roadmap.md), not here.
- **Choices where Spacelift is silent** are deviations only when a record names
  them as such; the record's [I] or *(unverified)* basis and its capture are in
  [report 30](../research/reference/30-open-questions.md#captures-needed).
- **Client impact** is stated against Spacelift's own clients and integrations:
  spacectl, `terraform-provider-spacelift`, direct GraphQL callers, webhook and
  audit consumers, runner images, launchers and agents. Decision 0001 already
  rules out re-pointing Spacelift's API clients unchanged, so for those records
  the impact says what a port must change.

## Adding an entry

Add a record to [`deviations.json`](../data/deviations.json) in the pull
request that introduces the divergence, with the next free `DV-NNN` number;
an ID is never reused, and a record that no longer holds keeps its ID and
gains a `withdrawn` member. Its `group` is the heading below that names the
deciding record, and a divergence whose subject has no group gets a new
heading here and a new value in the schema's `group` list. The decision
record lists the departure in its Rationale or Constraints, and the record's
`source` links back to that section.

The register's [schema](../data/schema/deviations.schema.json) says what each
member holds. In short: `spacelift` is what Spacelift does as a report
records it, saying so when the report marks it *(unverified)*; `infraweave`
is what infraweave does instead, with the exact names of its settings,
routes and fields; `why` is the limit avoided or the property gained; and
`impact` is `None` with the reason, or what a Spacelift client, an operator
or a script must change. Text members are markdown whose relative links
start from `docs/data/`.

## Register

Each heading is a value of a record's `group`: the subject, and the decisions
that own it.

### API surface: decisions 0001 and 0003

REST and OpenAPI in place of GraphQL, infraweave's own clients, idempotency
keys, rate limits per cost class, a per-caller API switch, cursor paging and
problem types named by URN.

### Architecture and persistence: decisions 0004 and 0005

The launcher and runner as process modes of the one binary, the PostgreSQL
floor, one bucket with a prefix per store, and object expiry and versioning
kept by infraweave rather than by the bucket.

### Tenancy: decisions 0006, 0007, 0014 and 0021

The root, organization, account and space hierarchy, authentication at the
organization, the account admin as a role on the account's top space, and
shares between accounts of one organization.

### Identity and access: decisions 0006 and 0007

Token claims, administrator API keys as role bindings, OIDC issuers over
HTTPS, signing key rotation, federation token refresh, session lifetimes, the
`legacy` space, system roles and disabled actions, and the OAuth server for
MCP.

### Runs, workers and policies: decisions 0008 and 0009

The worker long-poll protocol in place of MQTT, lost runs and offline
workers, runner images and launcher settings, the pool key, and the policy
engine's default version, removed builtins, fail-closed outcomes and
`sanitized` helper.

### State and queue: decisions 0010 and 0011

The state backend over `http`, proxied and sealed state, plan and run locks,
retention of state versions, `Content-MD5` under FIPS, and the PostgreSQL
work queue with retry limits, dead jobs and ordering keys.

### VCS: decision 0012

The operator's GitHub App, webhook routes, secret rotation, delivery replay,
the VCS agent's transport and allowlist, and commit check names.

### Secrets: decision 0013

Rewrapping the keyring, the KMS providers offered, the separate JWT signing
key, and masking of short values.

### Registry: decision 0014

Discovery on the installation's host, checksum checks on publishing, error
body types, and yanked versions.

### Events: decision 0015

Delivery headers and retries, retention settings, the worker usage event,
server-sent-event streams, Deploy events as Flows events, and notification
templates.

### Observability: decision 0017

OTLP as the only export, the exporter's series served at
`/metrics/accounts`, the probe routes, worker traces and the access log.

### Flows: decision 0018

Flows in the infraweave account, endpoint addresses and MCP, the API Request
block, where Flows and its agents run, REST Flows operations in the one served document, app images and
declarative widgets.

### Intelligence: decision 0019

AI tools over infraweave's operations, the operator's provided integration
and its budget, the chat endpoint, the operator's terms, and Intent's
provider checks.

### Packaging: decision 0020

The reference deployments, one version for application, modules and chart,
first-party Flows apps as images, launcher downloads, and no licence token.

### Scope, tiers and clients: decision 0021

Seats and tiers, billing as an installation feature, operator-registered apps
and catalogues, superseded enum values, and `infraweavectl` authentication.

### Names: decision 0001 and the glossary

infraweave's own names on every surface, with no Spacelift alias.

### Errors, updates and input: decision 0022

Status codes and problem types, `PATCH` with merge patch, `ETag` and
`If-Match`, unset sub-reads, strict request bodies, label rules, cron syntax
and the keys of the repository configuration file.

### Console: decision 0023

`allowedActions` on every resource the API returns.

### Expressions: decision 0024

CEL for every expression, text templates in workflow files and environment
values, a fixed render context, and expressions on output references.

## Deviations a capture may change

A record with a `pending` member rests on Spacelift behaviour that a capture
of [report 30](../research/reference/30-open-questions.md#captures-needed)
has not yet shown. The member names the captures and what they settle for
that record. When a capture lands, the record stays, changes or is withdrawn
with the owning decision, and the `pending` member goes once no capture is
left.

## Not deviations

Interfaces infraweave does not serve because Spacelift supersedes them (such as
`ACCESS` policies, `policyCreate` and `policyUpdate`, the versioned-blueprint
family, the legacy per-stack AWS role and GCP service-account integration,
`/intent/mcp`, the standalone `spacelift-intent` stdio server and
`pulumi-spacelift`) are recorded with their evidence in the
[supersession register](capability-roadmap.md#supersession-register), under
[0021](../research/decisions/0021-capability-scope-and-supersession.md)'s
supersession test. Decision
[0016](../research/decisions/0016-testing-and-acceptance.md) governs how
infraweave proves its behaviour and adds no behavioural departure.
