# Product vocabulary review

The project-wide review covers maintained source, filenames, tests, fixtures,
API source and baselines, generators, scripts, hidden workflow configuration and
operator documentation outside `docs/`. Planning ownership stays in the
implementation documents and their registers. Product code describes present
capabilities and the domains that own them.

## Causes and corrections

| Cause | Correction |
|---|---|
| Roadmap identifiers and “later phases” copied into handwritten comments | Describe authorization, policy evaluation, notification delivery, optional inputs, stack projections, probes and telemetry by their current domain responsibilities. Remove promises tied to a build sequence. |
| Documentation ownership copied into a generated runtime catalogue | Remove the unused ownership member from both `Feature` and the generator input/output. Regenerate all 83 entries; entitlement names, kinds, tiers, limits, requirements and tier-feature mappings are unchanged. The documentation registers retain their ownership assignments. |
| A policy acceptance selector named for delivery planning | Rename the selector to `@policies` in all 14 feature headers, the allowed-tag registry, lab batch, controller assertion and usage documentation. Scenario identities, step text and partition ownership stay unchanged. |
| Flows parity exceptions explained through a future build stage | Rewrite 45 explanations around the unimplemented Flows operations and their architecture decision. Keep every baseline member and test-scenario control decision. |
| Planning examples embedded as Go string constants | Move the exact documentation examples and expected diagnostics from three test files into [documentation testdata](testdata/README.md). Keep parsing, ordering, schema, missing-owner and error-location assertions. Golden readers normalize CRLF. |
| Fixture guidance and published schema prose using roadmap language | Describe actual policy contracts, input producers, sampling, workflow evidence and Intent attachments. Preserve raw captured requests/responses, pins, upstream policy sources and vendor documents. |
| Repository conventions lacked a boundary for planning vocabulary | Add a product-vocabulary rule to `CLAUDE.md`, alongside the existing present-tense comment rule. Record squash-only PR merging. |

No maintained source filename on the reviewed main revision contains a build
stage identifier. Policy capture files and delivery evidence documents use capability names.

## Verification boundaries

The generated catalogue is byte-identical after removing its unused ownership
member. Flows baseline membership is identical. The selector edit preserves all
27 scenario declarations and 458 step declarations in the affected feature
files. Original documentation examples are preserved byte for byte before
checkout line-ending normalization; negative assignments and diagnostics retain
the same values and locations.

Searches include numbered ownership labels, phase-based names and prose about
later implementation work. Matches reviewed as unrelated include execution
phases such as PLAN/APPLY, the P95 performance percentile, elliptic-curve names,
PKCS12 filenames, pagination cursors, local principal variables, compressed
schema data and repository names inside immutable vendor captures. These remain
valid product, framework or evidence vocabulary. Documentation processors retain
the generic schema needed to validate and render the planning documents; they
contain no concrete project-stage assignments.

## Local validation

- Repository-wide configured `golangci-lint run`: zero issues.
- Race/shuffle unit checks: documentation rendering and validation, feature
  entitlements, policy input contracts, API parity, acceptance partitions and
  harness selection all pass.
- Infraweave-lab controller checks: eight tests pass.
- The complete `@policies` batch passes on the updated main baseline with
  Docker required: 14 feature files, 51 scenarios and 902 steps; every step
  passes. It runs with `GOFIPS140=certified` and `GODEBUG=fips140=only` and completes
  in 75.748 seconds. This includes native OPA evaluation, both LOGIN Rego
  dialects, and Terraform/OpenTofu worker scenarios.
- `git diff --check` and the tracked-source vocabulary audit pass.
