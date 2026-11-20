# C48: accessible free-tier observations

This partial capture records V5_FREE advertising no WORKFLOW_POLICIES,
ACCESS_POLICIES, NOTIFICATION_POLICIES or RUN_IGNORED_TRIGGERS. It still permits
creating PLAN, APPROVAL, GIT_PUSH, TRIGGER, INITIALIZATION, TASK, NOTIFICATION and
INTENT policies. All except NOTIFICATION could be attached to the upstream test
stack. Real receipts establish PLAN, APPROVAL and INITIALIZATION evaluation on
this account. Creation or attachment does not prove evaluation or a feature gate.

A real GitHub push with an attached ignore=true GIT_PUSH policy nevertheless
created a tracked run. No GIT_PUSH receipt/sample was observed. An entitled-account
comparison remains required to observe the exact feature-to-type mapping. The
policies page's warning in console.json names Access, Push, Trigger and
Notification as unevaluated on this account, without isolating each billing
feature. LOGIN, ACCESS, STACK_ACCESS and legacy aliases were not mutated.

The maintainer approved ADR 0009's WORKFLOW_POLICIES mapping to GIT_PUSH/TRIGGER
evaluation as **inference** on 2026-11-19. This unblocks implementation without
resolving the missing upstream observation or imposing a blanket CRUD gate.
Raw meta.yaml, request.graphql, response.json and console.json retain their
historical bytes. No current infraweave tier response is synthesized from them.

## Assertion handoff

`expect.yaml` maps every recorded assertion to an owner. `TestAccCaptures/C48`
executes `replaySubset: policy-free-tier-management` against the shared
authenticated API and real PostgreSQL. Its success is reported as one executed
REST subset, never as a completed C48 workflow replay. Recorded JSON Pointer
checks continue to verify the original upstream values independently.

| Recorded assertion | Classification and infraweave proof | Remaining owner |
|---|---|---|
| `/exchanges/0/response/data/tier` = `V5_FREE` | Historical upstream account inventory; infraweave's feature model differs and exposes no equivalent tier response | Open C48 evidence question; #656 audits the distinction |
| `/exchanges/0/response/data/tierFeatures` | Historical feature names remain unchanged; the WORKFLOW_POLICIES mapping is approved inference, not observed parity | Entitled-account comparison remains open; ADR 0009 and #656 |
| `/exchanges/18/response/errors/0/message` | Executed equivalent refusal: `PUT /api/v1/stacks/{stack}/policies/{notification}` returns 422 `validation-failed`, `/policyId` / `rule`, and no attachment. REST problem wording intentionally differs from GraphQL | #646 implemented the contract; #683 executes this subset |
| `/exchanges/20/response/data/stack/run/state` = `FAILED` | Deferred: the recorded history enters PENDING_REVIEW with PLAN warnings, then fails with `Run rejected by approval policies`. Management requests do not prove that transition | #650 approval/review behavior, #651 PLAN warnings, #654 real workflow integration |
| `/exchanges/23/response/data/stacks` = `[]` | Executed local cleanup: temporary stack deleted through REST, GET returns 404, and complete stack inventory returns to its initial set | #683; #656 records final tested revision and CI |
| `/exchanges/23/response/data/policies` = `[]` | Executed local setup/cleanup: all ten captured sources across eight types compile and persist; all temporary policies are deleted, GET returns 404, and inventory returns to its initial set | #683; #656 records final tested revision and CI |

The local replay translates only `package spacelift` to `package infraweave`,
resource names to a C48 test prefix, and root space to the seeded `acme` space.
It reads back the stored body, type and REGO_V1 dialect. Eight relations across
PLAN, APPROVAL, GIT_PUSH, TRIGGER, INITIALIZATION and TASK are created and read.
INTENT is created but its upstream stack attachment is not replayed: infraweave
reserves INTENT attachments for Intent projects in P25 (ADR 0009). The existing
attachment acceptance suite verifies that chosen stack-attachment refusal.
Inventory comparison preserves unrelated seeded objects; it does not assert
that the entire infraweave account started empty. Bounded fallback cleanup also
runs when any intermediate assertion fails.

## Verification and phase exit

`captures_policy_management_test.go` and `captures_policy_attachments_test.go`
retain the other stateful replayers. The real policy management and attachment
features retain their success, 403, missing-parent and atomic-failure cases:

- `features/policies/management.feature`: persisted bodies/dialects across
  replicas, compile refusal preserving the previous version, space-reader write
  denial, feature and entitlement revocation.
- `features/policies/attachments.feature`: persisted distinct relation IDs,
  reader mutation denial, missing/mismatched parents, type-specific 422 refusal,
  and a failed durable reconcile rolling back the space change.

#656 must record the tested revision, passing CI and this subset's actual setup,
refusal and cleanup evidence. Complete workflow behavior still needs #650/#651
and the varied real OPA/Rego public-path acceptance matrix in #654. The entitled
upstream comparison stays visibly unresolved. A green subset, recorded-value
check or approved inference establishes none of those missing observations.
