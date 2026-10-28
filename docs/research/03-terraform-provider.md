# 03: The Spacelift Terraform provider

How the reference uses
[terraform-provider-spacelift](https://github.com/spacelift-io/terraform-provider-spacelift),
and how the provider will later check infraweave's own API.

## What is extracted

| Snapshot | How | Gives |
|---|---|---|
| `terraform-schema.json` | `terraform providers schema -json` against the pinned release, in a scratch directory | 56 resources and 62 data sources: attribute types, required/optional/computed, sensitivity, deprecation, descriptions, nested blocks |
| `terraform-operations.json` | `go/ast` over `spacelift/resource_*.go` and `data_*.go` at the release tag | the GraphQL root fields each resource calls (54 resources and 56 data sources call at least one) |

Loading a provider's schema never configures the provider, so no Spacelift
credentials are involved.

The provider declares its operations as struct tags, for example
`graphql:"stackCreate(input: $input)"`. Tags that name a nested selection
rather than a root field are discarded by `RetainRootFields`.

## Attribute matching

For each resource, `specref` gathers the input objects of the mutations the
resource calls, in this order:

1. create and attach mutations;
2. update mutations;
3. all other mutations.

Within each group, it gathers the nested input objects first, then the
`<Op>Request` component, then the path parameters (`path:<op>`).

Each attribute then lands on the first input field matching one of these
candidates:

1. the camelCase name (`administrative` → `administrative`);
2. the name with a write-only `_wo` suffix dropped (`secret_wo` → `secret`);
3. `_id` replaced by `ID` or dropped (`space_id` → `space`);
4. `_ids` replaced by `s` (`worker_pool_ids` → `workerPools`).

Where the provider names a field differently, the `terraform:` section of
`rest-mapping.yaml` holds the rename; for example `spacelift_stack`
`enable_local_preview` → `localPreviewEnabled`. A rename that points at a field
that does not exist fails generation.

Matched properties carry `x-terraform: [resource.attribute]` and fall back to
the provider's description when GraphQL has none.

## What does not map

`coverage.md` lists, per resource:

- **Unmapped attributes.** For example the `spacelift_context` hook lists
  (`before_init` and the rest), which the provider sends through a separate
  `hooks` input. Another example is the `module_id` versus `stack_id` choice on
  attachment resources, which GraphQL takes as one generic ID.
- **Terraform-only attributes.** The `*_wo_version` counters of write-only
  arguments exist only to make Terraform re-send a secret, so no API field
  matches them.
- **Nested blocks.** Examples are `vendor` blocks on `spacelift_stack` and the
  VCS blocks on `spacelift_module`. The provider folds these into the GraphQL
  `vendorConfig` and `provider` fields. They are listed but not mapped yet.

## Provider-specific behaviour worth copying

- Attachments are separate resources (`spacelift_context_attachment`, …),
  which is why REST models them as sub-resources with `PUT`/`DELETE` rather
  than as fields of the stack.
- Write-only arguments (Terraform 1.11+) are carried for secrets: webhook
  secrets and VCS tokens.

## Later: provider parity in `deploy/ci`

infraweave will ship its own REST-native Terraform provider (see decision
[0001](decisions/0001-rest-first-spacelift-surface.md)). `deploy/ci` will run
it against a live infraweave server, mirroring
`weaveplatform-artifact-registry/deploy/ci`.

`terraform-schema.json` is the parity oracle. Every Spacelift resource and
attribute should have an infraweave equivalent, or a recorded reason why not.
