# Bounded policy compatibility corpus

Five exact public MIT policy files from
[`spacelift-policies-example-library`](https://github.com/spacelift-io/spacelift-policies-example-library/tree/6ff5edb08bc806f323eff5c48426a7762ef1fee7),
commit `6ff5edb08bc806f323eff5c48426a7762ef1fee7`, fetched 2026-11-20.
`manifest.json` pins each original path and SHA-256, including the complete
upstream `LICENSE`. The corpus is representative, not the whole example library.

`TestUnitRegoMITCompatibilityCorpus` verifies every file against that manifest,
rewrites only `package spacelift` to `package infraweave` in memory, and evaluates
ten authored positive/negative inputs through the actual linked OPA module.
Complete raw decisions are compared under supported v0/v1 and a research-only v2
prototype with the per-type v1 preamble and restricted capabilities. Inputs are
not upstream captures and do not establish live account behavior.

The upstream plan example's hash comment is retained verbatim. infraweave's
`sanitized` implementation follows the captured final-eight-hex SHA-256 contract
in ADR 0009; the example calls that helper instead of hard-coding a digest.

[Research, commands and limitations](../../../docs/research/reference/34-rego-v2-compatibility.md).
