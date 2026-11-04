# 0002: Committed reference sources and their licensing

Status: Accepted

## Context

Each Spacelift product has a reference generated from upstream sources:
Deploy's in `api/reference/spacelift/`, Flows' in
`api/reference/spacelift-flows/`. To keep builds offline and diffs reviewable,
the extracted data is committed beside each reference. That means
redistributing material that other parties wrote.

## Decision

| File | Source | Licence |
|---|---|---|
| `introspection.json` | introspection of `demo.app.spacelift.io/graphql`, normalized | the public schema; the same descriptions ship in the MIT-licensed provider and collection |
| `bruno.json` | extracted from spacelift-io/spacelift-api-bruno at a pinned commit (names, folders, root fields, docs text, example variables; not the `.bru` files) | MIT |
| `terraform-schema.json` | `terraform providers schema -json` of spacelift-io/spacelift at a pinned release | MIT |
| `terraform-operations.json` | GraphQL root fields named in the provider's struct tags at the same release | MIT |
| `spacelift-flows/provider-rpcs.json` | the RPC paths and request and response field names of spacelift-io/terraform-provider-flows at a pinned commit, read from its `CallFlowsAPI` calls | MIT |
| `spacelift-flows/flowctl-rpcs.json` | the RPC paths and field names of spacelift-io/flowctl's `src/api.ts` at a pinned commit | MIT |
| `spacelift-flows/sdk-identifiers.json` | the declaration and member names of the `@slflows/sdk` npm package at a pinned version; no types, comments or code | none declared, so identifiers only |
| `spacelift-flows/schemas.yaml` | written by hand from the Flows documentation; checked against the published templates and the core app registry, of which only SHA-256 digests are committed | ours |

Files without a directory are in `api/reference/spacelift/`.

- **Attribution.** Each reference directory's `NOTICE` carries the MIT
  notices for the repositories it draws on.
- **Pins.** Each `sources.lock.yaml` pins every source with a SHA-256.
  Deploy's also pins the Bruno commit and the provider tag; Flows' pins the
  provider and flowctl commits, the SDK version, and the digest of each
  template it validated. `specref fetch` writes nothing unless every source
  succeeds, and accepts only a full 40-hex commit for a repository pinned by
  commit and an exact semantic version for the Deploy provider.
- **Refreshes.** A weekly `openapi-spec-update.yml` compares each product's
  pins with the newest upstream versions: the Bruno head and the latest
  provider release for Deploy; the heads of the Flows provider and flowctl
  and the latest SDK for Flows. When one is newer, it re-fetches that
  product, regenerates both references on a runner and opens one PR, so
  upstream changes arrive as reviewable diffs through the quality gate.

## Rationale

- **Reproducible builds.** Committed snapshots make `make gate`
  reproducible without network access or Terraform, and turn upstream drift
  into a reviewable diff.
- **Smaller footprint.** Extracting only what generation needs, rather than
  vendoring the repositories, keeps the footprint and the licensing surface
  small.

## Constraints

- The live schema has no commit hash. A schema change arrives with the next
  Bruno commit or provider release, or with a manual `force` run of the
  update workflow.
- No Spacelift source code is copied. The providers are read only for their
  struct tags and call sites, and flowctl only for its call sites and type
  declarations.
- The SDK states no licence, so nothing beyond its identifiers is committed.

## References

- <https://github.com/spacelift-io/spacelift-api-bruno>
- <https://github.com/spacelift-io/terraform-provider-spacelift>
- <https://github.com/spacelift-io/terraform-provider-flows>
- <https://github.com/spacelift-io/flowctl>
- <https://www.npmjs.com/package/@slflows/sdk>
