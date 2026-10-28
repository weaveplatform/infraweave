# 0002: Committed reference sources and their licensing

Status: Accepted

## Context

The Spacelift reference is generated from three upstream sources. To keep
builds offline and diffs reviewable, the extracted data is committed to
`api/reference/spacelift/`. That means redistributing material that other
parties wrote.

## Decision

| File | Source | Licence |
|---|---|---|
| `introspection.json` | introspection of `demo.app.spacelift.io/graphql`, normalized | the public schema; the same descriptions ship in the MIT-licensed provider and collection |
| `bruno.json` | extracted from spacelift-io/spacelift-api-bruno at a pinned commit (names, folders, root fields, docs text, example variables; not the `.bru` files) | MIT |
| `terraform-schema.json` | `terraform providers schema -json` of spacelift-io/spacelift at a pinned release | MIT |
| `terraform-operations.json` | GraphQL root fields named in the provider's struct tags at the same release | MIT |

- **Attribution.** `api/reference/spacelift/NOTICE` carries the MIT notices
  for both repositories.
- **Pins.** `sources.lock.yaml` pins every source: a SHA-256 for each, plus
  the Bruno commit and the provider tag. `specref fetch` writes nothing unless
  every source succeeds, and accepts only a full 40-hex commit for Bruno and an
  exact semantic version for the provider.
- **Refreshes.** A weekly `openapi-spec-update.yml` compares the pins with the
  newest upstream commits. When one is newer, it regenerates on a runner and
  opens a PR, so upstream changes arrive as reviewable diffs through the
  quality gate.

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
- No Spacelift source code is copied. The provider is read only for its
  struct tags.

## References

- <https://github.com/spacelift-io/spacelift-api-bruno>
- <https://github.com/spacelift-io/terraform-provider-spacelift>
