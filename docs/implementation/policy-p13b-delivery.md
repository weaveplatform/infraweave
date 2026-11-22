# P13b delivery verification

Delivery gate: [#666](https://github.com/weaveplatform/infraweave/issues/666).
Epic: [#636](https://github.com/weaveplatform/infraweave/issues/636).
The [P13 audit](policy-p13-delivery.md) retains the earlier phase's exact evidence.
This audit supplements it with identity, deprecated types and policy tooling.

## Current evidence boundary

The integrated implementation baseline `bf85746e20165b7ac9b6384357f15621bb63282a`
passed [CI 44982320621](https://github.com/weaveplatform/infraweave/actions/runs/44982320621).
Its six ordinary acceptance partitions contain 189 features / 1,136 scenarios.
The operation audit finds success, denial and failure evidence for all **31**
served policy, scheme, receipt-sample and contributing stack operations. No
capability record claims more than those results establish. Policies remains
`partial` as a complete reference tag because Intent attachments are later work.
The four policy Terraform resource/data-source records now match their computed
`partial` API state; each explicitly retains the absent native provider in P24.
These are baseline facts, not a claim that this final gate has completed CI.

This branch adds executable C47/C50/C52/C53/C54/C273 replay and actual V0/V1 LOGIN
language/default-sampling proof. Its final exact-head CI, combined reports and
required real-provider lab rerun must be recorded before delivery is complete.
The phase remains in progress until those gates pass.

## Operation coverage

All 31 declared operations have passing `@case:success`, `@case:denial` and
`@case:failure` scenarios in the integrated baseline reports. The audit includes
policy CRUD and merge patch, account/space searches and suggestions, explicit
and automatic attachments, candidates, contract/runtime/compiler, all six evidence
readers, simulation, the three catalogue operations, both authorization-scheme
operations and stack/receipt backedges. Scenario names and locations are recorded
in the machine-readable operation audit beside this report.

Safe capture summaries contain ID, kind and timestamp. Reading their private
input is a separate root-admin operation. C53's GraphQL list requested that
private field; its native replay exercises the corresponding protected REST GET.
A safe list/count never grants permission to open the input.

## Actual OPA/Rego workflow matrix

Every runtime row uses embedded OPA in actual infraweave processes with real
PostgreSQL, queues and the relevant blob/worker services. Authored programs and
approved contracts are distinguished from original vendor observations.

| Behavior | Executable proof | Result asserted |
| --- | --- | --- |
| V0/V1 syntax, explicit compatibility imports, dialect changes, located diagnostics, unsupported versions and disabled built-ins | `policies/check.feature`, `policies/simulation.feature`, complete pinned library corpus | Actual compiler/check/save outcomes and located errors; forbidden execution/network built-ins refused |
| V0/V1 LOGIN helpers, comprehensions, partial objects/sets and default sampling | `authz/login_language.feature` | Real key admission, exact root-writer actions, default retained input/source/dialect, non-admin sample 403, one committed LOGIN evaluation and no run effects |
| LOGIN multi-account/member/session admission, original identity facts and empty switcher | `authz/login_member.feature`, `auth/login_oauth.feature`, `authz/login_policy.feature` | Only admitted accounts appear; failed B entry hides private denial; actual derived signed credentials preserve original facts |
| LOGIN malformed V0 output and actual expensive V1 program | `authz/login_failures.feature` | No session, grant or run flag; committed RESULT/TIMEOUT evidence |
| Active/inactive scheme transitions and policy edits | `authz/authorization_scheme.feature`, `authz/authorization_scheme_controls.feature`, C50/C273 replay | Saved configuration survives round trips; peer credentials are revoked across replicas; editor/caller survives |
| Captured LOGIN flag true/false/deny programs | C50 replay | Real grants stay exactly root writer, edits revoke old sessions, deny creates no partial session/grants, three evaluations create no run-owned effects |
| TASK V0/V1 refusal and positive execution | `runs/tasks.feature`, `policies/sampling.feature`, C52 replay, `policies/initialization.feature` | Refusal precedes task/run/job creation; real Terraform commands execute in admitted cases; dated deprecation metadata replays exactly |
| Server INITIALIZATION V0/V1, invalid results and multiple denials | `policies/initialization.feature`, C52 replay | Exact READY-to-FAILED assignment note/history and owned receipts before preparation; allowed actual Terraform task and duplicate-job controls |
| Policy-free defaults, conflicts, ownership/reset and workflow snapshots | P13's `policies/workflow_*.feature`, `policies/approval*.feature`, `policies/plan*.feature`, `policies/trigger*.feature` | Committed state/history/receipts and owner-qualified flags; approval/deny/ignore and lock/priority rules; fresh trigger/replan ownership |
| Actual Terraform/OpenTofu/Terragrunt documents and `sanitized()` | `policies/plan_workers.feature`, `policies/plan_reports.feature`, C53/C273 replay | Native worker plans, last-eight SHA-256 values, no plaintext copies in retained inputs/raw storage, fresh decisions on replans |
| Deadlines, combined cancellation, bounded output and concurrent state | `policies/workflow_runtime.feature`, `policies/workflow_bounds.feature`, `policies/workflow_limits.feature`, `policies/workflow_isolation.feature`, `policies/simulation_limits.feature` | Actual expensive-program failure; caller cannot receive partial approval; independent input/helper/print state; bounded notes and private print |
| Caller rollback, queue fencing and backend failure | Native app/policy/runs PostgreSQL acceptance plus `authz/login_failures.feature` | No committed receipt/flag/outbox/job from aborted callers; failed admission grants nothing; stale ownership cannot publish effects |
| Sample false/true, budgets, sealed publication, privacy, expiry and replacement | `policies/sampling.feature`, `policies/evidence_samples.feature`, C53 replay and native storage controls | Safe metadata independent of retention; account/hour quotas atomic; raw input requires root admin; ciphertext identity, expiry and current capture fences |
| No-write edited-input simulation and captured complete decisions | `policies/simulation*.feature`, C192/C273 replay | Whole actual decision documents match recordings; evaluation/workflow/blob inventories unchanged |
| Complete MIT library | Native catalogue acceptance, C54 replay and `policies/catalogue.feature` | All 74 programs compile, 62 upstream files / 241 assertions pass; every served captured identity pages once; import preserves origin/dialect |
| Account replay isolation | `api/idempotency_accounts.feature`, real PostgreSQL substitution controls | A → B → A, changed grants, independent actors, refreshed credentials and exact owned row counts; ciphertext copied between accounts fails |
| Certified FIPS | Complete required FIPS job | All suites run in mode only with actual Go Cryptographic Module v1.0.0; no relaxed gate |

## Kind, dialect and workflow coverage

The linked OPA dependency is **v1.22.4**. `policies/runtime.feature` checks the
actual running binary's version; the final CI and lab records must confirm it.
A default-engine policy below runs V1 under the intentional ADR 0009 default.

| Kind | Actual V0 workflow | Actual V1 workflow | Owned result |
| --- | --- | --- | --- |
| LOGIN | `authz/login_language.feature`, malformed-result control | Same language outline, member/session/scheme controls and actual timeout | Account admission and exact session grants; no run-owned effects |
| TASK | `runs/tasks.feature` admitted command and invalid result; captured C52 refusal | `runs/tasks.feature` and `policies/sampling.feature` denial | Refusal creates no run/job; admitted real Terraform command runs |
| INITIALIZATION | `policies/initialization.feature` actual task, duplicate jobs, invalid result and multiple policies | Same feature's four READY-denial cases and captured C52 source | Owned receipt and exact state/note before preparation; admitted task executes |
| PLAN | `policies/plan_workers.feature` real Terraform and `workflow_plan_flags.feature` owner | Same features' real OpenTofu/Terragrunt, new decision on replan and captured C53/C273 plans | Sanitized native reports, warn/deny/allow, receipt/state and owner-qualified flags |
| APPROVAL | `workflow_approval_flags.feature` and `workflow_isolation.feature` legacy owners | Same real review/concurrent workflow features and `approval_workers.feature` | Approval/deny precedence, current-state review ownership, lock/flag history |
| GIT_PUSH | `policies/push.feature` V0 proposed event | Same feature's V1 tracked selection plus worker/range/native-provider walkthroughs | One admitted run, retained initial receipt/flags and durable no-trigger reasons |
| TRIGGER | `policies/trigger_semantics.feature` legacy high-priority rule | Same feature's conflicting low-priority rule and isolated child | Normal-priority fallback, durable errors, independently owned downstream state |
| NOTIFICATION | Pinned original V0 source/corpus, stateless tooling only | Typed contract and stateless tooling only | Execution/inbox is P18; C273 proves it has no stack attachment or used-by projection |
| INTENT / module execution / worker exec initialization | Later owning phase | Later owning phase | P25 / P17 / P14; no completed execution row is claimed |

This matrix names committed workflow assertions, rather than treating a
successful compile or simulation as workflow execution. The complete required
CI runs these features with real services and checks their nonzero reports.

## Capture provenance and executable assertions

Original responses, request documents, browser snapshots, source pins and notices
are retained. `expect.yaml` maps every observed assertion to executed REST,
upstream evidence, approved inference or an explicit deviation. Implemented
behavior has no deferred replay owner. A successful subset is not counted as a
newly observed vendor fact or a full upstream parity claim.

| Capture | New actual execution | Preserved boundary |
| --- | --- | --- |
| C47 | Retired ACCESS request rejected at `/type`; real stack/policy inventories unchanged | ACCESS is intentionally absent under ADR 0009/0021; an unavailable legacy upstream account is not invented |
| C50 | Captured true/false/deny source drives real scheme/session/grant/no-run proof and cleanup | The free-tier experiment did not establish LOGIN evaluation; fixture key login and root-space aliases map to native documented identity facts |
| C52 | Captured V1 initialization source fails assignment with exact note; real Terraform positive control; captured V0 TASK denial refuses creation | Historical free-tier TASK completion without receipts remains upstream evidence; refusal-before-creation is the approved implementation contract |
| C53 | Real marked-sensitive Terraform plans, false/true bodies, capture independence, sample hash, private input 403, raw ciphertext check, expiry and cleanup | Capture privacy uses typed placeholders; samples retain the exact sanitized evaluated input. Original timestamp and workload-specific approval count stay historical |
| C54 | All 62 captured served identities page without duplicates or omissions | All 68 original templates and 62 test files remain pinned/licensed; six LOGIN templates are corpus-only |
| C273 | Two real tracked plans produce two ALLOW samples; body/used-by/search/suggestions/sample/simulation agree; NOTIFICATION cannot attach; session controls and cleanup pass | Original browser admin/inventory is separate from native organization/account IAM; opaque REST handles and transport schema are normalized |

The frozen implementation revision `3f8249c6` passed a combined certified-FIPS
capture batch: **14 scenarios / 557 steps**, including all six P13b replayers
and the C192/C291/C46/C48/C51 baseline workflows. C47 additionally passed its
direct public `/type` rejection and unchanged-inventory assertions. The new
LOGIN language matrix passed **2 scenarios / 34 steps** during development.
These local reports establish actual execution; full exact-head CI and final
main lab evidence remain required before delivery is complete.

## Later-phase boundaries and final gates

P14 owns worker exec initialization, P17 module-version execution, P18
notification delivery/inbox, P23 a live organization IdP product and P25 Intent.
Typed inputs, catalogue source, stateless simulation or captured observations do
not establish those execution products. `notificationCount` remains zero until
its P18 inbox producer exists; generic policy outcomes are not inbox items.

The final gate must attach its exact passing revision, unchanged 95% total /
90% every-package coverage, generated/reference/parity checks, full certified
FIPS and the real four-hosted-provider infraweave-lab policy rerun on final main.
Each provider must actually run its assigned scenarios and verify cleanup; a
skipped job, empty selection or compilation-only result does not satisfy it.
