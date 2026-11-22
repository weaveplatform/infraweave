# Policy page projection verification

Issue: [#665](https://github.com/weaveplatform/infraweave/issues/665).
Parent: [#636](https://github.com/weaveplatform/infraweave/issues/636).

## Public operation mapping for P24c

[C273](../../test/fixtures/captures/C273/README.md) records actual browser requests
and their responses. The mapping below uses the served REST/SDK operations;
there is no console-specific backend or new operation in this ticket. Paths
are relative to `/api/v1`.

| Captured view or action | Served operation and path | Public evidence |
| --- | --- | --- |
| Policy list and names (`SearchPolicies`, `GetPolicyNames`) | `searchPolicies`: GET `/policies/search`; `policies`: GET `/policies` | `policies/management.feature`, `policies/search.feature` |
| List filters (`SearchPoliciesSuggestions`) | `searchPoliciesSuggestions`: GET `/policies/search/suggestions`; space-scoped `searchSpacePoliciesSuggestions` | `policies/search.feature` |
| Body, identity, name, description, engine, type, labels, timestamps, template origin (`GetPolicyForDetails`) | `policy`: GET `/policies/{policyId}`; same projection in create/update/delete/list/search responses | `policies/management.feature`, `policies/catalogue.feature`, `policies/projections.feature` |
| Used by, `hasAttachedStacks`, attachment identity/name/space | `policy` and `policyAttachedStack`: GET `/policies/{policyId}/attached-stack?id=...`; stack's `stack`/`stackPolicyAttachment` backedges reuse the policy-owned port | `policies/attachments.feature`, `policies/projections.feature` |
| Attach, detach and automatic selection | `policyAttach`: PUT `/stacks/{stackId}/policies/{policyId}`; `policyDetach`: DELETE `/policy-attachments/{attachmentId}`; existing candidates/label preview | `policies/attachments.feature`, `policies/projections.feature` |
| Safe newest-first evaluation preview (`GetPolicyEvaluationRecords`) | `policy.evaluationRecords`; stack/attachment previews bind the actual stack target | `policies/projections.feature` and native PostgreSQL preview/search comparison |
| Retained sample badge (`sampleCount`) | `policy.sampleCount`; current owner publication/expiry metadata, separately from evaluation history | `policies/projections.feature`, `policies/sampling.feature` |
| Evaluation search (`SearchEvaluationRecords`) | `policySearchEvaluationRecords`: GET `/policies/{policyId}/search-evaluation-records?input=...` | `policies/evidence_api.feature`, `policies/evidence_samples.feature` |
| Per-type sample filters (`SearchEvaluationRecordSuggestions`) | `policySearchEvaluationRecordSuggestions`: GET `/policies/{policyId}/search-evaluation-record-suggestions?input=...` | `policies/evidence_api.feature`, `policies/evidence_samples.feature` |
| Read-only sample JSON/results/source/print (`GetPolicyEvaluationSample`) | `policyEvaluationSample`: GET `/policies/{policyId}/evaluation-sample?key=...` | `policies/evidence_samples.feature`, `policies/projections.feature` |
| Edited body/input simulation (`SimulatePolicyV2`) | `policySimulatev2`: POST `/policies/simulate` | `policies/simulation.feature`, `policies/simulation_limits.feature`, `policies/simulation_retention.feature` |
| Workbench captured inputs | `policyInputCaptures`: GET `/policy-input-captures`; `policyInputCapture`: GET `/policy-input-captures/{policyInputCaptureId}` | `policies/evidence_api.feature`, `policies/evidence_samples.feature` |
| Import from the catalogue | `searchPolicyTemplates`, `searchPolicyTemplatesSuggestions`, `policyTemplate`; `policyCreatev2` persists the validated origin | `policies/catalogue.feature` |
| Account LOGIN settings | Root/account-scoped policy management, existing authorization-scheme operations | `authz/authorization_scheme.feature`, `authz/login_member.feature`, `authz/login_policy.feature` |

C273's original GraphQL bodies and timestamps remain unchanged. Its string
simulation result maps to the existing typed REST response. REST replay and the
combined operation/capture exit matrix are enforced by #666 after integration;
the recorded assertions alone are not reported as a completed REST replay.

## Projection ownership and visibility

All history and count queries fix account, policy identity and the policy's
current readable space. Attachment views additionally fix the actual stack
target. Counts do not grant raw-sample access: a child space reader sees safe
metadata/counts, but a sample read still requires root-space administration.
Used-by excludes unreadable targets and ineffective/stale automatic relations.
Subsequent reads resolve current names, body, space and attachment selection.

Safe previews contain only opaque evaluation ID, lower-case outcome and Unix
seconds. They include committed unsampled evaluations and pending uploads. Each
policy has at most 100 preview records within seven days, ordered by evaluation
time descending then ID ascending, matching shared evaluation search. This
bound is an infraweave implementation rule, not an inferred vendor limit.

Sample counts independently traverse every 500-row candidate batch within one
database snapshot. Published, live, unexpired sample inputs contribute only if
the blob owner also reports live/unexpired lifecycle metadata. Captures,
staging, failed uploads, expired or retiring inputs and missing/retiring blob
metadata contribute zero. No input/source/print, sealed payload or bucket is
opened for these projections; no signed URL is generated. The blob owner's
bounded availability port owns its own SQL. Availability is owner metadata,
not an external bucket probe.

LOGIN has no stack used-by view. NOTIFICATION has no stack attachment view, as
observed in C273. Intent attachments/execution remain P25. `notificationCount`
remains zero: C273 never requests that field, and ADR 0009 assigns dismissible
notification-inbox production to P18. Counting NOTIFICATION outcomes or generic
event deliveries would invent a different fact. No UI route is delivered here.

## Real verification

Before implementation, the public retained-APPROVAL test reaches actual OPA
samples and fails because the policy returns hardcoded zero/empty projections.
After implementation, the expanded public feature passes **2 scenarios / 102
steps** against real server replicas, PostgreSQL, Gitea, OPA and sealed RustFS.
It verifies policy/stack/attachment identity and history, name updates,
child-reader metadata versus raw-sample denial, foreign-account hiding, expiry
without erasing safe history, detach/delete, automatic labels, hidden targets
and current-space moves. The automatic-selection scenario explicitly enables
inheritance on the fixture's non-inheriting legacy space.

Native race/FIPS PostgreSQL controls pass with a one-connection pool. They
execute **501 actual OPA evaluations and sealed filesystem publications**, model
one prior UTC quota hour in fixture metadata without changing production
budgets, and prove all 501 samples count while the preview contains 100. The
preview agrees record-for-record with the first search page at equal frozen
clock timestamps. Physically unavailable test payloads do not cause a private
read during projection. Additional controls cover unsampled and pending
history, strict input/blob expiry, failed/deleting/missing metadata,
foreign/moved/unreadable/deleted parents, independent stack targets and actual
PostgreSQL availability errors. The blob-owner batch test separately proves
store isolation, strict expiry, pending/deleting states, duplicates, empty and
oversized batches and cancelled reads.

The final combined capture replay, real Terraform/OPA workflow matrix, coverage,
certified FIPS and required main lab rerun remain #666's delivery gate.
