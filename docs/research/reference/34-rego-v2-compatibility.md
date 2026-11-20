# 34: Released Rego v2 and the supported policy language

Checked against: OPA v1.22.0 release notes (2026-10-29), v1.22.4 release notes (2026-11-12), the linked `github.com/open-policy-agent/opa` v1.22.4 module and its public Go APIs, official import/negation documentation read 2026-11-20, and the MIT policy example library at `6ff5edb08bc806f323eff5c48426a7762ef1fee7`. Probe sources and exact corpus bytes are committed with this report.

Research baseline: **2026-11-20**.

## 1. Released behavior and the API boundary

OPA released `import rego.v2` in v1.22.0, including logical `and`/`or`, updated
negation and the public `ast.RegoV2` Go setting. The currently linked v1.22.4
therefore implements it; it is not a main-only proposal. [S1][S2]

A policy's language version is separate from the evaluator's reported runtime
version. infraweave offers `REGO_V0` and `REGO_V1`; linking a newer OPA does not
expand that engine enum. The omitted-create V1 choice remains intentional.
[Decision 0009](../decisions/0009-policy-engine-opa.md) records the compatibility
decision: v2 is released but deliberately unsupported in the current policy API.
This report measures the trade-off; it does not propose a v2 implementation.

## 2. A concrete wildcard-import compatibility gap

OPA documents a difference for a composite negation whose intermediate value is
undefined. Legacy evaluation can fail the whole expression before negation;
`future.keywords.not` or v2 wraps that work inside the negation. The wildcard
`future.keywords` also selects the newly available keywords. [S3][S4]

The authored probe uses this policy with input `{}`:

```rego
package infraweave
import future.keywords
deny contains "missing" if not startswith(input.missing, "x")
```

The real linked library and infraweave runtime produced:

| Evaluation | Raw `deny` |
| --- | --- |
| Direct embedded OPA v1, without the wildcard | `[]` |
| Direct embedded OPA v2 | `["missing"]` |
| infraweave V0/V1 with the wildcard, before the capability fix | `["missing"]` |
| infraweave V0/V1 with the frozen supported keyword capabilities | `[]` |

The pre-fix failure is `TestUnitRegoLegacyWildcardSemantics`, independently for
both engine values. These are executed authored cases, not captured Spacelift
responses. Rejecting the literal `rego.v2` import alone did not preserve the
meaning of an existing v0/v1 wildcard policy.

The supported opt-in set is now exactly `contains`, `every`, `if` and `in`.
Explicit imports of those keywords and `rego.v1` execute under both supported
engines. `rego.v2`, the new `not`/`and`/`or` imports and unknown keywords fail with
located compile diagnostics. A wildcard cannot enable v2 bodies/operators.

Named imports also exposed a separate validation bug: the AST renders keyword
segments using brackets, for example `future.keywords["if"]`. Validation now
compares reference segments, preserving supported imports instead of relying on
a dot-prefix rendering. This keeps the existing input/data import boundaries.

## 3. Bounded embedded-runtime and corpus results

[`v2_probe_test.go`](../../../internal/platform/rego/v2_probe_test.go) uses the
actual pinned Go library. Its research-only evaluator admits released v2 language
features while retaining production built-in/network/FIPS restrictions. It does
not expose a new engine value, store or HTTP endpoint. Each prototype call has a
two-second deadline, and print uses the existing bounded private hook.

| Probe | Observed result |
| --- | --- |
| Released v2 import, logical operators and `sanitized` | Execute in the isolated prototype; supported API still rejects v2 |
| Ten policy types, v2 user module with v1 preamble | Complete raw defaults match the supported v1 baseline |
| Five exact MIT policy bodies, ten authored inputs | Complete raw decisions match across supported v0/v1 and prototype v2: 30 evaluations |
| Removed network/process/time/trace built-ins | Remain compile-time refusals in the v2 prototype |
| FIPS MD5/SHA-1 and HMAC restrictions | Remain compile-time refusals in the v2 prototype; probes also run under actual `fips140=only` |
| Print capture | Private hook truncates at the existing 64 KiB bound |
| Cached wildcard query, then narrower language capabilities | Recompiles under a distinct effective-capability cache key and returns legacy semantics |
| Repeated evaluation with different inputs | Reuses the correct prepared query without retaining prior input |

The MIT corpus covers access, approval, plan, push and trigger policies, with
positive and negative authored inputs. Its immutable source links, hashes and full
MIT notice are in [`rego-compatibility`](../../../test/fixtures/rego-compatibility/README.md).
Only the package namespace is rewritten in memory; the committed source files
remain byte-for-byte copies. These samples do not establish compatibility for
all public policies, v2 migration safety, or Spacelift's deployed v2 behavior.

All existing dialect, preamble, capability, timeout, cache, print and captured-raw-
decision tests are rerun with the probe, including race/shuffle and enforced FIPS.
No external sidecar, fake OPA evaluator, credentials or live account is involved.
The real dependency being tested is the embedded library itself.

## 4. Decision and future scope

Keep the API's v0/v1 boundary and V1 create default. Freeze wildcard keyword
capabilities so a library upgrade cannot silently opt existing policies into a
later negation meaning. Remove v2 capability/import flags from production and
retain located rejections for unsupported language choices. The linked library
version remains actual build metadata, not a language support promise.

A future proposal to offer v2 requires a separate scoped ticket and explicit
engine/storage/schema decisions, the undefined-value truth-table differences,
per-module preamble compatibility, policy migration and error behavior, effective
capabilities/cache identity, and normal/FIPS acceptance. This investigation makes
no such implementation proposal. Successful execution of the small corpus alone
would be insufficient justification for an automatic API expansion.

## 5. Reproduction

From the repository root, with the committed dependency and corpus:

```sh
GOWORK=off go test -v ./internal/platform/rego -run '^TestUnitRego' -count=1
GOWORK=off go test -race -shuffle=on ./internal/platform/rego -run '^TestUnit' -count=1
GOWORK=off GODEBUG=fips140=only go test -v ./internal/platform/rego -run '^TestUnit' -count=1
make docs-check
```

The implementation PR records the exact tested revision and final CI evidence.
No result here depends on fetching a moving upstream branch during a test.

## Sources

- [S1] [OPA v1.22.0 release, including released v2 support](https://github.com/open-policy-agent/opa/releases/tag/v1.22.0).
- [S2] [OPA v1.22.4 release](https://github.com/open-policy-agent/opa/releases/tag/v1.22.4), [pinned parser source](https://github.com/open-policy-agent/opa/blob/v1.22.4/v1/ast/parser.go), and [pinned capabilities source](https://github.com/open-policy-agent/opa/blob/v1.22.4/v1/ast/capabilities.go).
- [S3] [Official imports and v2/keyword opt-ins](https://www.openpolicyagent.org/docs/policy-reference/keywords/import).
- [S4] [Official legacy and improved negation semantics](https://www.openpolicyagent.org/docs/policy-reference/keywords/not#improved-negation-semantics).
- [S5] [MIT example library at the exact corpus revision](https://github.com/spacelift-io/spacelift-policies-example-library/tree/6ff5edb08bc806f323eff5c48426a7762ef1fee7).
