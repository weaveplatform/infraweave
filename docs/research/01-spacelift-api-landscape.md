# 01: The Spacelift API landscape

What Spacelift exposes, where each piece is published, and what infraweave
takes from it. Numbers are from the snapshots pinned in
`api/reference/spacelift/sources.lock.yaml`.

## One GraphQL endpoint

Spacelift has no REST API. Everything the web UI, `spacectl` and the
Terraform provider do goes through one GraphQL endpoint per account:
`https://<account>.app.spacelift.io/graphql`.

| Kind | Count |
|---|---|
| Root queries | 285 |
| Root mutations | 380 |
| Object types | 576 |
| Input objects | 164 |
| Enums | 92 |
| Unions | 17 |
| Interfaces | 3 |
| Scalars | 5 (built-ins only: `ID`, `String`, `Int`, `Float`, `Boolean`) |

Subscriptions are not part of the schema. Log streaming is done by polling
(`runLogs` with a token), which maps naturally onto REST.

### Introspection without an account

`https://demo.app.spacelift.io/graphql` answers the standard introspection
query without credentials. That is the schema `specref fetch` snapshots.

Caveats:

- The demo account runs the current SaaS release. Self-hosted Spacelift lags
  it, and feature-flagged fields can appear on a tenant before they reach the
  demo schema.
- Introspection gives types, nullability, deprecations and descriptions. It
  gives neither authorization rules nor error semantics. Those come from the
  Bruno docs and the provider behaviour.
- The schema has no commit or version of its own. It is pinned by the
  SHA-256 of its normalized JSON, and the generated spec is versioned
  `schema-<sha12>`.

## Authentication

1. Create an API key in the UI (an ID and a secret), or use an OIDC-backed
   key.
2. Call the `apiKeyUser(id, secret)` mutation. It returns a short-lived JWT.
3. Send `Authorization: Bearer <jwt>` on every later request.

The reference maps step 2 to `POST /auth/token` (public), and every other route
requires `bearerAuth`.

## spacelift-api-bruno

[spacelift-io/spacelift-api-bruno](https://github.com/spacelift-io/spacelift-api-bruno)
(MIT) is Spacelift's maintained request collection: 673 requests in 91
folders and sub-folders. Each `.bru` file holds one GraphQL operation, its
example variables and, in most cases, a `docs` block.

What the reference takes from it:

- **Tags.** The top-level folder (Stacks, Runs, Contexts, …) becomes the
  OpenAPI tag. Advanced uses its sub-folder; Cookbook and Danger Zone fall back
  to the resource's tag.
- **Examples.** Example variables become request examples. An example the
  generated schema rejects is dropped and listed in `coverage.md`; today these
  are `stackScheduledTaskCreate` and `stackScheduledTaskUpdate`, because
  GraphQL coerces a single value into a list.
- **Docs.** The docs text up to the `<!-- notes` marker becomes the operation
  description when introspection has none.

Not every root field has a request. `coverage.md` lists the ones no request
calls.

## terraform-provider-spacelift

[spacelift-io/terraform-provider-spacelift](https://github.com/spacelift-io/terraform-provider-spacelift)
(MIT) has 56 resources and 62 data sources. See
[03-terraform-provider.md](03-terraform-provider.md).

## What is out of scope

- **UI analytics mutations** (`identifyUserEvent`, `trackUserEvent`, …). These
  are excluded in `rest-mapping.yaml`.
- **Deprecated fields and superseded `V1` twins.** These are excluded
  automatically, and each is listed with its upstream reason.
- **Billing and Spacelift-internal SaaS operations.** These are kept in the
  reference so its coverage stays complete. Whether infraweave implements them
  is a later decision.
