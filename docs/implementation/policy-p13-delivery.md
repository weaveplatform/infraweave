# P13 delivery evidence audit

Audit for [POL-19 (#656)](https://github.com/weaveplatform/infraweave/issues/656), dated 2026-11-21. **P13 remains In progress.** This audit records the passing local-service contracts and the evidence still required for delivery; it does not close the phase, epic #635, or its outstanding follow-ups.

## Revision and executed results

The retained historical execution baseline is [`1120bb8a`](https://github.com/weaveplatform/infraweave/commit/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9). Its own [push quality gate](https://github.com/weaveplatform/infraweave/actions/runs/44808423418) passed, independently of [PR #706](https://github.com/weaveplatform/infraweave/pull/706)'s tested head `fcd3ab623de01a71d611a7a8603476a59beefb03`. The gate passed normal acceptance, certified FIPS, PostgreSQL-next, all three OS unit suites, race checks, generation, parity, lint, vulnerability checks, cross-compilation and aggregate Quality. Coverage was **95.5% (49,560/51,922)**; every package met 90%.

The six downloaded Cucumber reports contain **1,040 scenarios and 25,407 steps, all passed**. The adjacent [execution index](policy-p13-delivery.json) retains report hashes, artifact IDs, exact scenario names, source locations and operation tags. These are executed results, not a count of source tags. Artifacts can expire; reproduce the same partitions when refreshing this dated audit.

| Partition | Passed scenarios | Passed steps | Executed report |
|---|---:|---:|---|
| `core` | 382 | 5,810 | [Cucumber artifact](https://github.com/weaveplatform/infraweave/actions/runs/44808423418/artifacts/14667220055) |
| `integrations` | 356 | 9,419 | [Cucumber artifact](https://github.com/weaveplatform/infraweave/actions/runs/44808423418/artifacts/14665140870) |
| `runs` | 82 | 3,062 | [Cucumber artifact](https://github.com/weaveplatform/infraweave/actions/runs/44808423418/artifacts/14665700087) |
| `terraform` | 117 | 3,704 | [Cucumber artifact](https://github.com/weaveplatform/infraweave/actions/runs/44808423418/artifacts/14665520117) |
| `terraform-plans` | 41 | 1,173 | [Cucumber artifact](https://github.com/weaveplatform/infraweave/actions/runs/44808423418/artifacts/14663573650) |
| `terraform-vcs` | 62 | 2,239 | [Cucumber artifact](https://github.com/weaveplatform/infraweave/actions/runs/44808423418/artifacts/14665085875) |

The earlier capture PR #667 had a failed historical FIPS job; this audit does not relabel that run green. The current merged-source gate above executes the retained contracts. The authored `@policy-phase` workflow slice and its real PostgreSQL, Gitea, storage, queue and vendor-tool proofs are described in [POL-17's matrix](policy-phase-verification.md). Five provider API models in that slice do not replace the live-provider lab.

## Authenticated operation matrix

The exact P13 set contains **21 new or changed operations**: ten management/search, seven attachment, compiler check, typed contract, runtime and review. Existing CEL `expressionCheck` is a separate shared-registry regression, bringing the table to 22 rows. Each linked denial below actually asserts `403`; unreadable-object `404` and filtered searches are additional isolation evidence. The contract discovery path is on the ordinary API listener at its root, requires an account credential and the policies feature, and returns `404` on the admin listener. It is not a public endpoint.

Paths include their served prefix. Check uses `expressions`; runtime and schema use `policies`. Save/check share the real Rego compiler, while existing `expressionCheck` remains the CEL editor surface.

| Operation / owner | Method and served path | Success | 403 | Failure |
|---|---|---|---|---|
| `policies` / #645 | `GET /api/v1/policies` | [S6](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L35) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S7](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L55) |
| `policyCreatev2` / #645 | `POST /api/v1/policies` | [S1](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L221) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S37](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L174) |
| `policy` / #645 | `GET /api/v1/policies/{policyId}` | [S1](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L221) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S3](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L9) |
| `policyUpdatev2` / #645 | `PUT /api/v1/policies/{policyId}` | [S4](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L54) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S4](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L54) |
| `policyUpdatev2Patch` / #645 | `PATCH /api/v1/policies/{policyId}` | [S5](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L26) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S5](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L26) |
| `policyDelete` / #645 | `DELETE /api/v1/policies/{policyId}` | [S4](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L54) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S4](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L54) |
| `searchPolicies` / #645 | `POST /api/v1/policies/search` | [S6](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L35) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S7](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L55) |
| `searchPoliciesSuggestions` / #645 | `POST /api/v1/policies/search/suggestions` | [S8](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L96) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S7](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L55) |
| `searchSpacePolicies` / #645 | `POST /api/v1/space-policies/search` | [S8](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L96) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S7](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L55) |
| `searchSpacePoliciesSuggestions` / #645 | `POST /api/v1/space-policies/search/suggestions` | [S8](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L96) | [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | [S7](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L55) |
| `attachablePoliciesInSpace` / #646 | `GET /api/v1/attachable-policies-in-space` | [S9](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L17) | [S10](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L111) | [S11](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L133) |
| `autoattachmentForLabels` / #646 | `GET /api/v1/autoattachment-for-labels` | [S12](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L237) | [S10](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L111) | [S12](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L237) |
| `policyAttachedStack` / #646 | `GET /api/v1/policies/{policyId}/attached-stack` | [S13](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L294) | [S10](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L111) | [S11](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L133) |
| `policyDetach` / #646 | `DELETE /api/v1/policy-attachments/{attachmentId}` | [S13](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L294) | [S10](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L111) | [S11](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L133) |
| `policyAttach` / #646 | `PUT /api/v1/stacks/{stackId}/policies/{policyId}` | [S13](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L294) | [S10](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L111) | [S11](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L133) |
| `stackAttachablePolicies` / #646 | `GET /api/v1/stacks/{stackId}/attachable-policies` | [S9](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L17) | [S10](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L111) | [S11](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L133) |
| `stackPolicyAttachment` / #646 | `GET /api/v1/stacks/{stackId}/policy-attachment` | [S13](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L294) | [S10](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L111) | [S11](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L133) |
| `policyCheck` / #648 | `POST /api/v1/policies/check` | [S14](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/check.feature#L25) | [S15](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/check.feature#L45) | [S16](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/check.feature#L53) |
| `policyContract` / #641 | `GET /.well-known/policy-contract.json` | [S17](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/contract.feature#L10) | [S18](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/contract.feature#L24) | [S19](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/contract.feature#L34) |
| `policyRuntime` / #640 | `GET /api/v1/policy-runtime` | [S20](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/runtime.feature#L8) | [S21](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/runtime.feature#L21) | [S22](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/runtime.feature#L31) |
| `runReview` / #650 | `POST /api/v1/stacks/{stackId}/runs/review` | [S23](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/approval.feature#L30) | [S24](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/approval.feature#L41) | [S25](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/approval.feature#L49) |
| `expressionCheck` / #648 (baseline) | `POST /api/v1/expressions/check` | [S26](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/expressions/editor.feature#L17) | [S27](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/expressions/editor.feature#L88) | [S28](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/expressions/editor.feature#L100) |

The [API sources](../../api/src/api/paths-policies.yaml), [space searches](../../api/src/api/paths-spaces.yaml), [stack attachments](../../api/src/api/paths-stacks.yaml), [review route](../../api/src/api/paths-runs.yaml), [check](../../api/src/api/paths-policy-check.yaml), [schema](../../api/src/api/paths-policy-contract.yaml) and [runtime](../../api/src/api/paths-policy-runtime.yaml) determine this set. Merge patch is an infraweave-only operation. Deprecated ACCESS policies remain superseded; a future typed decision or template is not another served workflow.

### Scenario keys

All paths are under `test/acceptance/features`; the index retains the complete passed result and partition for each key. A scenario may deliberately exercise both a successful write and a later refused write.

| Key | Executed scenario |
|---|---|
| [S1](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L221) | `policies/management.feature` — A compiled policy persists across replicas with the intentional V1 default |
| [S2](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L125) | `policies/management.feature` — Revoking the policy entitlement refuses known policies on both replicas |
| [S3](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L9) | `policies/search.feature` — A sibling account cannot read or change a known policy ID |
| [S4](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L54) | `policies/management.feature` — Conditional updates and deletion refuse stale policy versions across replicas |
| [S5](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L26) | `policies/management.feature` — Merge patch keeps source and dialect while resetting nullable settings |
| [S6](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L35) | `policies/search.feature` — Both list and search cursors resume on another replica without skipped policies |
| [S7](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L55) | `policies/search.feature` — Invalid policy search requests and unknown spaces have public failures |
| [S8](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/search.feature#L96) | `policies/search.feature` — Account and inherited space searches use persistent policy projections |
| [S9](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L17) | `policies/attachments.feature` — Account-qualified attachments, candidates and preview never cross accounts |
| [S10](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L111) | `policies/attachments.feature` — Revoking the policy entitlement refuses every attachment operation |
| [S11](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L133) | `policies/attachments.feature` — Missing, malformed and mismatched attachment parents are refused |
| [S12](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L237) | `policies/attachments.feature` — Preview counts all matching targets and returns current target labels |
| [S13](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L294) | `policies/attachments.feature` — Explicit attachment persists once with distinct policy and attachment IDs |
| [S14](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/check.feature#L25) | `policies/check.feature` — Both replicas compile the default V1 and explicit V0 policy dialects |
| [S15](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/check.feature#L45) | `policies/check.feature` — Switching off expressions refuses policy checking |
| [S16](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/check.feature#L53) | `policies/check.feature` — Missing credentials and unknown dialects refuse checks |
| [S17](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/contract.feature#L10) | `policies/contract.feature` — Both replicas publish a complete schema through the ordinary account controls |
| [S18](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/contract.feature#L24) | `policies/contract.feature` — The installation's policies switch refuses contract reads |
| [S19](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/contract.feature#L34) | `policies/contract.feature` — Missing credentials and the database-backed caller switch refuse schema reads |
| [S20](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/runtime.feature#L8) | `policies/runtime.feature` — Both replicas report the OPA version actually linked from this build |
| [S21](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/runtime.feature#L21) | `policies/runtime.feature` — Switching off policies refuses runtime metadata reads |
| [S22](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/runtime.feature#L31) | `policies/runtime.feature` — Missing credentials and a disabled caller refuse runtime metadata |
| [S23](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/approval.feature#L30) | `policies/approval.feature` — An authorized queued review evaluates the real policy and releases the run |
| [S24](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/approval.feature#L41) | `policies/approval.feature` — A stack reader cannot supply a review |
| [S25](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/approval.feature#L49) | `policies/approval.feature` — Unknown runs and runs outside review states refuse a review |
| [S26](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/expressions/editor.feature#L17) | `expressions/editor.feature` — A template is checked, completed and evaluated against a stack's environment, its secret masked |
| [S27](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/expressions/editor.feature#L88) | `expressions/editor.feature` — With expressions switched off for the installation, each operation answers 403 |
| [S28](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/expressions/editor.feature#L100) | `expressions/editor.feature` — A misspelt variable is flagged with its range, and a source, cursor or surface the endpoints cannot use is refused |
| [S29](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/runs/policy_evidence.feature#L8) | `runs/policy_evidence.feature` — Two servers expose historical evidence without leaking raw decisions or deleted policy source |
| [S30](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/runs/logs.feature#L128) | `runs/logs.feature` — The run's own in-run API token reads its stack while the run plans, but neither the run nor its logs |
| [S31](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/runs/queue.feature#L136) | `runs/queue.feature` — A run's priority is read back, its stack answers, and a stack's lists page newest first |
| [S32](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/runs/queue.feature#L219) | `runs/queue.feature` — An unknown run, a cancel of a run that ended, a run named on another stack and a malformed search are refused |
| [S33](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/stacks/stacks.feature#L223) | `stacks/stacks.feature` — The runs and tasks of a stack the caller cannot read answer as a stack that does not exist |
| [S34](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/stacks/stacks.feature#L198) | `stacks/stacks.feature` — A stack with no runs lists empty pages of runs and tasks, and finds no run by ID |
| [S35](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/stacks/stacks.feature#L242) | `stacks/stacks.feature` — An altered cursor on a stack's runs or tasks answers 400 |
| [S36](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L334) | `policies/attachments.feature` — C9 forced deletion removes the policy and both attachment projections |
| [S37](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/management.feature#L174) | `policies/management.feature` — Compilation refusals preserve the stored body and engine |

## Updated run and stack projections

The projection boundary is explicit. [Run rendering](../../internal/runs/internal/adapters/http/http.go)
adds owned flag unions, safe receipt metadata, current reviews, approval guards
and retained PLAN messages to full `Run` responses. [Stack rendering](../../internal/stacks/internal/http/http.go)
reads effective policy attachments for full `Stack` responses. Their common
mappers do not establish that every route has exercised every populated field.

| Projection / actual served operations | Passing evidence at the audited revision | Audit limit |
|---|---|---|
| Full Run reads: `run`, `stackRun`, `stackRuns`, `stackRunsInRange`, `stackProposedRuns`, `stackTasks` | [S29](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/runs/policy_evidence.feature#L8) checks receipts, deleted-policy snapshots, flags, privacy and event projection through two servers. [S31](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/runs/queue.feature#L136), [S34](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/stacks/stacks.feature#L198) and [S35](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/stacks/stacks.feature#L242) execute list/read success and malformed/unknown/cursor failures. | `run` has a genuine in-run-token `403` in [S30](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/runs/logs.feature#L128). The other five routes' tagged denial is unreadable-stack `404` in [S33](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/stacks/stacks.feature#L223); the local `@policy-projection-denial` replay now supplies literal `403` for all five routes through both replicas (one scenario / 57 steps). Final CI for that repair remains required. |
| Full Run mutation responses: `runTrigger`, `runProposeLocalWorkspace`, `proposeWithOverrides`, `taskCreate`, `runRetry`, `runPromote`, `runLogsDelete`, `runCancel`, `runStop`, `runKill`, `runConfirm`, `runDiscard`, `runTargetedReplan`, `runPrioritySet` | Existing per-operation success/denial/failure scenarios are included in the execution index. Actual policy workflows verify review/worker gating, replan messages, cancellation and trigger effects; [POL-17](policy-phase-verification.md) maps their real OPA and committed-state assertions. | These routes share the full Run mapper. A baseline lifecycle success with empty policy fields is not a populated policy-evidence assertion. `runReview` instead returns its review record, not a full Run. |
| Full Stack responses: `stacks`, `searchStacks`, `stack`, `runStack`, `stackCreate`, `stackUpdate`, `stackPatch`, `stackDelete`, `stackReslug`, `stackDisable`, `stackEnable`, `stackLock`, `stackSyncCommit`, `stackSetCurrentCommit`, `stackUnlock`, `starSet` | [S13](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L294) checks populated `attachedPolicies` and detach; [S36](https://github.com/weaveplatform/infraweave/blob/1120bb8aa7e4c09e5c3468cd876567db13e9e3b9/test/acceptance/features/policies/attachments.feature#L334) verifies forced deletion clears both projections. Baseline operation scenarios remain in the index. | Each uses the same effective, account-qualified attachment reader. Ordinary unreadable entities answer `404` under [0007](../research/decisions/0007-authorization-spaces-and-roles.md); an index denial tag does not assert a literal `403`. |
| `run_updated` event | Actual public review and native report owner-reset scenarios compare public flags/receipts with committed projection and SQL. Historical deleted-policy evidence checks the safe event fields. | This is an outbox projection, not an HTTP operation. Raw decisions, input documents, private print output and policy source are not public receipt metadata. P18 owns delivery sinks. |
| Worker report and sealed plan artifacts | `workerRunReport` on `POST /worker/v1/runs/{run}/reports`; actual Terraform/OpenTofu/Terragrunt DEFAULT/SMART/NONE, replan and uploaded/decrypted artifact assertions are in the passing vendor partitions. | Worker/run JWT identity, run ownership, state/version fences and retries are the [0003 protocol controls](../research/decisions/0003-api-conventions.md), not a fabricated account-role denial. Explicit NONE stays sealed. Missing/truncated artifacts and unsupported sensitive families fail closed. |

`searchRuns` returns Run/Stack references and facets rather than full policy
receipts. `searchRunsSuggestions` and `searchStacksSuggestions` return facets,
not full Run/Stack objects. They retain their own existing isolation cases and
must not be described as populated receipt or attachment projections.

## Capture assertions and their owners

The historical baseline validates **15 authentic policy/tooling records**,
executes four complete policy REST replayers (C9/C49/C56/C57), one C48 subset
and the unrelated example replay, with ten authentic records deferred. The
local reconciliation below adds the C291 complete replay, C46/C51 subsets and
C48 workflow assertions. Final CI must refresh the complete capture census. A `/recorded` pass checks captured bytes and declared
observations; it does not exercise the equivalent infraweave workflow. The rows
below keep those distinctions and the original capture provenance intact.

| Capture | Executed proof / remaining assertion | Owner and status |
|---|---|---|
| [C49](../../test/fixtures/captures/C49/README.md) | Omitted engine produced upstream V0; real create/read/delete replay deliberately expects infraweave V1 and verifies cleanup. | #645 / DV-028; complete for this intentional documented deviation. |
| [C56](../../test/fixtures/captures/C56/README.md) | Real create/update timestamps remain within captured request bounds at Unix-second precision. | #645; real REST replay executed. |
| [C57](../../test/fixtures/captures/C57/README.md) | Upstream recorded OPA `v1.17.1`; real local runtime endpoint independently equals linked `v1.22.4` from `go.mod`. | #679 / #640; real REST replay executed. Equal historical/local versions are not asserted. |
| [C9](../../test/fixtures/captures/C9/README.md), policy assertions | Non-forced deletion refuses a relation; force deletes policy and clears both attachment projections. | #646; real REST replay executed. Context/plugin remainder retains its separate owner. |
| [C291](../../test/fixtures/captures/C291/README.md) | The executable REST replay uses the captured policy sources with real worker plans and reviews. Both UNCONFIRMED and PENDING_REVIEW become FAILED with the exact captured note; latest mixed approval receipts and verified cleanup are checked. | #656 executes the previously skipped #650/#651/#654 handoff. |
| [C51](../../test/fixtures/captures/C51/README.md) | The current-owner REST subset creates the captured policy, performs a real tracked plan, checks the historical flag receipt/current reset and stored ownership, rejects the review and verifies cleanup. | Sample-input reads remain explicitly deferred to #662. The missing legacy comparison remains an approved inference; public ID syntax never determines eligibility. |
| [C46](../../test/fixtures/captures/C46/README.md) | The REST subset creates/attaches the captured policy and verifies stack/policy deletion and restored inventories. Historical billing refusal/null and two genuine commit hashes remain upstream assertions. | #656 removes the stale #652 deferral. Ignored-reason/default-disable-label choices remain approved inferences, distinct from unobserved entitled-account behavior and #655 live execution. |
| [C48](../../test/fixtures/captures/C48/README.md) | Current REST subset creates all ten policies/eight types, attaches eight relations/six stack types, refuses NOTIFICATION attachment and restores inventory. Captured FAILED warned proposal remains handed to closed #650/#651/#654 rather than executed capture replay. | #683 subset complete; #656 must execute/reconcile PLAN warning → PENDING_REVIEW → approval rejection → FAILED. Historical tier/feature names and entitled-account comparison remain distinct. |
| C37/C40/C41 | [Console report 32](../research/reference/32-spacelift-console-survey.md), sections 16.1–16.3, and the checked-in input fixtures retain push cancellation of an unconfirmed run, queued rejection and reject precedence. Actual push/review worker scenarios assert the corresponding state/history. | Fixture-backed observations retained; these are not additional directories or newly recorded upstream captures. |
| [C192](../../test/fixtures/captures/C192/response.json) | Captured `sanitized()` results and full supported decisions execute offline through actual OPA; REST simulation remains deferred. | #663; future P13b surface, not missing P13 simulation delivery. |
| [C54](../../test/fixtures/captures/C54/library.json) | Pinned MIT library inventory, digests and captured pages; representative embedded-runtime corpus executes. Complete catalogue/import/simulation remains deferred. | #664, with #663 prerequisite; no complete catalogue claim. |
| [C47](../../test/fixtures/captures/C47/README.md), [C50](../../test/fixtures/captures/C50/README.md), [C52](../../test/fixtures/captures/C52/README.md) | Actual legacy/access/login/task/initialization observations and cleaned-up lab records are checked. LOGIN flags and TASK denial remain unobserved on the available account. | #659/#658/#660; future REST/worker consumers and approved documented inferences. |
| [C53](../../test/fixtures/captures/C53/README.md) | Actual sample/input independence, resource masking and non-admin capture refusal; upstream cadence/retention/cross-space/direct sample reads remain unobserved. | #661/#662; no P13 sealed sample storage/read claim. |
| [C273](../../test/fixtures/captures/C273/README.md) | Recorded policy-page request inventory and scheme/session controls. | #665 page API projections; deferred REST replay. |

The maintainer's dated [ADR 0009 decisions](../research/decisions/0009-policy-engine-opa.md#maintainer-approved-inferences-for-the-remaining-captures)
permit C46/C48/C51 implementation choices without claiming new upstream facts.
The 2026-11-20 P13b inference approval likewise preserves C47/C50/C52/C53's open
observations. On 2026-11-21 the maintainer separately approved #675's documented
source-pinned experimental producer. Its unchanged actual CLI output now has
positive compatibility proof; unmodified official-release production remains
unproven. This approval does not turn authored controls into upstream captures.
No authored response or owner closure substitutes for an executed replay.

## Real-provider lab and independent delivery gates

[POL-18 PR #715](https://github.com/weaveplatform/infraweave/pull/715) is open at
`202e61f5f591c5cef0c2ccbcce4a4f5733106f90`. Its new real-provider policy matrix
has **not executed**. Its local compiler/helper checks and pending required CI
are implementation evidence, not live-service acceptance. The `vcs-lab`
environment is restricted to main; this audit grants no branch-rule exception.

The earlier real [baseline run 44803036616](https://github.com/weaveplatform/infraweave/actions/runs/44803036616)
at `db2b8e58028970f350ff3eaa6b8ac93f73e8ed47` failed GitHub's native check
uniqueness/ownership assertion. GitLab, Bitbucket Cloud and Azure DevOps passed
the preceding workflow; their passes do not execute PR #715's new cases.
Bitbucket Data Center's successful job was unconfigured and supplies no native
DC evidence. Recorded cleanup completed. The GitHub known-check-ID repair in
#715 remains unproved against GitHub until the repaired lab runs.

| Provider | Prior baseline | New policy matrix and cleanup |
|---|---|---|
| GitHub | Failed native check assertion | Outstanding |
| GitLab | Passed prior workflow | Outstanding |
| Bitbucket Cloud | Passed prior workflow | Outstanding |
| Azure DevOps | Passed prior workflow | Outstanding |
| Bitbucket Data Center | Unconfigured | Conditional on configured licence; no pass claimed |

The new live rows require both dialects' PLAN warning and one native check/one
labelled comment, actual OpenTofu replacement/cancellation and terminal child,
and intermediate-edit/revert touched-path union. Preserve the actual tested
source, per-provider logs, run/commit/check identities, worker artifacts,
retry behavior and verified Terraform cleanup. Four configured providers are
mandatory; licence-less DC is the documented conditional peer.

| Exit requirement | Exact evidence / current disposition |
|---|---|
| Embedded OPA, supported dialects, typed schema and recorded fixtures | Merged source gate above; actual linked runtime, contract schema and all checked-in input/decision round trips. `rego.v2` remains released but intentionally unsupported. |
| Public review/report language, flags, rollback, isolation and runtime limits | [POL-17 matrix](policy-phase-verification.md), merged PR #706, and own merged-source run 44808423418 passed. Representative corpus is additional proof; full catalogue remains #664. |
| DEFAULT/SMART/NONE and real vendor artifacts | Native Terraform/OpenTofu/Terragrunt mode, selected-unit, decrypted upload, replan, sealing and worker-capability fence tests passed in the source gate. Modern positive action/deferred-resource fixtures retain exact vendor provenance. |
| Approved experimental `deferred_action_invocations` compatibility and positive decrypted upload | #675's source-pinned patched-development capture and strict wrapper transform now pass the complete local certified-FIPS matrix: 48 scenarios / 1,075 steps. Actual upload, selected units, masks/copies, sealing, projection, replan/resume and worker fences pass. [Proof record](policy-plan-payload-verification.json) preserves the approved evidence class; unmodified official-release production remains unproven. Final exact-head CI remains required. Rejected #708 supplies no completion proof. |
| Recovered pushed-path union | #696 / merged PR #701's real edit/revert, identity, pagination, incomplete-range and failure behavior passed. #715 still needs the new real-provider union case. |
| Runner image ownership/publication follow-up | #710/#711/#714 implement the accepted public shared-image design. #703 / PR #716 adds actual failed/canceled-work isolation, a surviving second owner and real plan/rollback/cleanup proof; native ARM64 FIPS passed. Exact-head CI 44867207346 passed normally and under certified FIPS after one scoped retry of the external registry quota failure; PR #716 is merged. Issue reconciliation remains outstanding. |
| Capture replay/assertion handoffs | C291 now executes both captured review rejection states. C51 current-owner reset and C46 cleanup execute as strict subsets; sample/upstream handoffs remain explicit. C48 checks complete source equality with C291 and executes that real worker rejection workflow in its own retained report, alongside its management subset. |
| Real-provider workflow | #655 / #715 pending, all new live rows outstanding. Baseline failure is retained. |
| Final audit head and generated evidence | Required CI for this audit, `make gate`, `make verify-parity verify-generate`, and `specref parity --product deploy --states --check` remain final gates. Current main's success does not attest to future edits. |
| Phase/capability status | P13 stays In progress. The actual merged-report parity command computes Infrastructure, Policies, Runs and Stacks as partial. Only Infrastructure's stale missing state is corrected to partial; no tag becomes implemented. #656 scoped proof does not close #675/#703 or all of epic #635. |

`runPolicyReceiptSample` remains with PWB-07 (#662); sampled-request metadata is
not a linked sealed sample. LOGIN/TASK/server INITIALIZATION, sampling, simulation,
catalogue and page projections remain #657–#666. Notification routing is P18,
launcher initialization/exec P14, dependencies P16, module-version triggers P17,
full Terragrunt changes/outputs P20 and Intent execution/attachments P25.
Typed contracts and emitted events do not deliver those workflows.

The prior P12b [#529](https://github.com/weaveplatform/infraweave/issues/529),
[#551](https://github.com/weaveplatform/infraweave/issues/551) and
[PR #634](https://github.com/weaveplatform/infraweave/pull/634) completion history
is retained. Its lab evidence does not replace the new policy lab.


## Local capture and projection reconciliation — 2026-11-21

The capture replay matrix passed six scenarios / 300 steps under certified
FIPS with actual PostgreSQL, object storage, Gitea, Terraform workers and
public review calls. C291 has two complete rejection replays; C48 verifies
identical captured programs and retains its own two workflow reports; C46
executes its cleanup subset; C51 executes current-owner reset and rejection.
Each replay requires its expected scenarios to complete and verifies public
resource deletion/inventory restoration. The execution index retains the four
report hashes and counts separately from the historical merged CI baseline.

The new projection-denial scenario exposed a real authorization gap: a stack's
implicit reader grant admitted an in-run token to full run/task projections.
The run HTTP adapter now rejects run principals before loading those views,
after the shared route guard checks stack visibility. The actual protocol-issued
in-run token still reads its stack, while all five run/task projection routes
return SPACE_READ 403 through both servers. This passed one scenario / 57 steps
under certified FIPS. Unit tests cover direct read/search refusals before any
projection dependency is used; the normal user/API-key paths remain covered by
the existing real-service matrix.

Local parity passes with the six complete reports from successful PR #715 CI
44867086361 (1,040 scenarios / 25,407 steps), plus the separately identified
projection regression. This mixed-source local check verifies the evidence
mapping; it does not attest to the audit's final revision. The exact final PR
and merged main still require their complete CI, generated checks and live lab.
Main's current policy-lab revision is e909e739b50646d32aeb754a310272d9648d514a;
[lab 44869808030](https://github.com/weaveplatform/infraweave/actions/runs/44869808030)
failed on all four required providers. Every provider verified cleanup: hook
restoration/detachment, Terraform destroy and empty-state readback, stopped tunnels
and GitLab shutdown. The failure exposed concurrent scenarios competing for the
same native webhook port and an OpenTofu merge-patch retaining Terraform settings.
PR #717 serializes only the real-provider partition and explicitly clears that
Terraform member. Local race/FIPS regressions and an actual API/OpenTofu worker
probe pass; the corrected real-provider rerun remains required. P13 remains
In progress until its delivery gates pass.
