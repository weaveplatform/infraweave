# Policy verification ownership and prerequisites

Audit for [#685](https://github.com/weaveplatform/infraweave/issues/685), dated
2026-11-20. This records verification ownership and readiness; the phase gates
retain responsibility for executing the full acceptance matrix. This is the frozen
#685 snapshot; the [P13 delivery audit](policy-p13-delivery.md) records current
executed evidence and outstanding delivery gates without rewriting this history.

## Sources and audit boundary

The repository baseline is `5968157bc067f0c65d66c7dcfd034d468acdcf8d`.
The [issue snapshot](policy-verification-ownership.json) records exact GitHub update times,
body hashes, native parents and direct blockers. The reviewed requirements are
[epic POL #635](https://github.com/weaveplatform/infraweave/issues/635) and
[epic PWB #636](https://github.com/weaveplatform/infraweave/issues/636), the
[implementation plan](plan.md), and decisions
[0004](../research/decisions/0004-repository-architecture.md),
[0008](../research/decisions/0008-run-execution-and-worker-protocol.md),
[0009](../research/decisions/0009-policy-engine-opa.md),
[0015](../research/decisions/0015-events-notifications-webhooks-audit.md) and
[0016](../research/decisions/0016-testing-and-acceptance.md).

At source inspection, evaluator [PR #686](https://github.com/weaveplatform/infraweave/pull/686)
remained in progress; its latest audited implementation is
`82148efc6683e383c2d0e0fe68e1bbd1f2df7de9`. Editor-check
[PR #684](https://github.com/weaveplatform/infraweave/pull/684) passed every
required check and merged as the baseline above. #648 is completed; other
proposed interfaces below require their own exact-head CI and merge before
being counted complete.

## Verified public and domain boundaries

The [published REST source](../../api/api.openapi.yaml) declares policy management,
searches, attachment projections, `GET /policy-runtime`, `POST /expressions/check`
and `POST /policies/check` (`policyCheck`). Completed #648 installs the policy
compiler callback through the shared expressions registry and verifies the
policy-specific HTTP adapter with the actual compiler. A route declaration alone
still does not establish a particular language surface is installed.

The baseline REST source has no `runReview`, `policySimulatev2`, policy template,
policy evaluation/sample/capture, authorization-scheme or receipt-sample
operations. Their owning tickets must add the matching REST/schema/SDK contracts
and real handlers. An upstream GraphQL register entry alone establishes neither
a current route nor passing HTTP evidence.

[Worker reports](../../api/worker.openapi.yaml) already expose
`POST /runs/{run}/reports` (`workerRunReport`). The
[runs assembly](../../internal/runs/assembly/assembly.go) supplies the scheduler,
reports, lifecycle jobs and public producer ports. #651 extends this actual
report path. [App](../../internal/app/domains.go) owns domain construction and
back-edge binding. Cross-domain acceptance composition belongs there; a bounded
runs-owned test-support package can exercise private lifecycle seams without a
served test endpoint or a constructor-rule exception.

## Requirement, owner and executable proof

“Gate” identifies the later integrated proof in addition to each implementation
PR's own acceptance and required CI. Each operation's success, 403 and failure
cases still follow decision 0016. Public/protocol exceptions use decision 0003's
documented controls. Already completed prerequisites are retained
in the exact snapshot rather than repeated as open work here.

| Ticket | Required boundary and prerequisite | Implementation verification | Integrated gate |
|---|---|---|---|
| #647 | Effective attachments and typed contracts from #646/#641; actual embedded OPA evaluator | Recorded decisions, every type's defaults/conflicts/errors, independent input/helper/print state, actual PostgreSQL rollback and safe outbox records | #654; P13b consumers in #666 |
| #648 | Shared `policyCheck` Rego surface over #645's compiler | Actual HTTP compiler success/403/failure, located diagnostics, wrong dialect and forbidden built-ins | #654 |
| #649 | Runs-owned receipts/flags, typed input snapshots and the actual #647 evaluator | Two replicas, owner-only reset, reset-before-add, previous/current snapshots, state/queue fences, caller/commit rollback, safe read/event projections | Public reviews and worker reports remain mandatory in #654 after #650/#651 |
| #650 | Public reviews and approval transitions over #647/#649 | Real public review calls and worker lifecycle; state-scoped reviews, rejection, concurrent reviewers, edits/timeouts and policy-free regression | #654 |
| #651 | Actual `workerRunReport` planning path over #642/#650 | Real Terraform/OpenTofu/Terragrunt artifacts and object storage; deny/warn/autodeploy/review, targeted replan, retry and failed upload | #654 |
| #652 | Provider event jobs over #647/#649 | All provider adapters, actual PostgreSQL/two servers and worker plans; ignore/no-trigger, priority/lock conflict, cancellations, checks and deduplication | #654 for integration; #655 for live providers |
| #653 | Durable terminal-run/task trigger jobs over #647/#649 | Real queue/database/tools and two replicas; consumer interruption, retries, own target tracked commit and fresh target flag ownership | #654 and #655 |
| #654 | #648/#651/#652/#653 and the independent CI-capacity prerequisite | Full public review/report flag matrix, workflow state/history/receipts/events, sanitization and the real OPA/Rego matrix below | #655 lab, then #656 evidence audit |
| #655 | Completed #654 workflow integration | Existing `vcs-lab` workflow and `@vcs-lab` partition; exact main revision, per-provider results, retries and cleanup | #656 |
| #656 | #655/#679/#683/#685 | Final operation/scenario, capture/scenario and phase-exit tables; generated parity states, actual lab and CI/coverage links | P13 exit and P13b readiness |
| #657 | #656/#644; account scheme storage and session revocation | Actual issued sessions on two servers/accounts; A→B→A preserves inactive configuration, keeps the caller's admin path and revokes other sessions | Actual policy admission/grants in #658/#666 |
| #658 | #657 scheme controls; access-owned login evaluator bound by app | Real identity sessions and signed tokens; multi-account admission, grants, owner escape, denial/error/timeout and revocation | #665/#666 |
| #659 | #656/#644; TASK evaluation before creation | Real public task calls, tools and PostgreSQL; refusal commits no task/run/job, with policy-free lifecycle retained | #666 |
| #660 | #656/#644; server assignment gate | Real worker polling and tools; C52's READY→FAILED assignment denial precedes launcher startup; approval scheduling regression | #666 |
| #661 | #656/#644; evaluator sampling handoff and owned sealed storage | Real PostgreSQL/blobs, atomic account/hour budgets, age-driven expiry, object/key identity, upload/rollback cleanup and receipt expiration metadata | Public sealed-content access in #662/#666 |
| #662 | #661's committed sealed objects and metadata | Actual sample/search/capture/receipt-sample HTTP operations; root-admin access, child-admin/foreign-account denial, expiration and replacement races | #665/#666 |
| #663 | #656; edited-policy simulation operation | Actual server/compiler/database/blob inventories; runtime limits and diagnostics, no receipt/sample/budget mutation | #664 import flow, #665/#666 |
| #664 | #656 and #663's public simulation operation | Pinned MIT catalogue, all 68 templates/62 upstream test files, six LOGIN exclusions from serving, source digests/notices, public import→create→simulate and searches | #665/#666 |
| #665 | #658/#662/#663/#664 and existing management/attachments | Ordinary HTTP/SDK page projections after writes, deletion/expiry, counters and authorization; C273 operation mapping | #666 |
| #666 | #659/#660/#665 | Full P13b operation and isolation matrix, real OPA/Rego/API/worker/blob behavior, generated parity and exact CI; lab rerun when shared execution changes | P13b exit |
| #675 | #642; verified modern vendor payload shapes | The maintainer-approved source-pinned experimental producer and all 48 sanitization cases now pass exact-main CI; actual decrypted uploads/masks/copies retain the explicit provenance in the [delivery audit](policy-p13-delivery.md) | Official-release deferred-action production remains unproven; no experimental runner CLI or new typed policy block |
| #683 | #646; authentic C48 partial capture and approved inference | Assertion-level observed/deviation/owner mapping and actual implemented REST replay subset, including NOTIFICATION attachment refusal | #656 |
| #685 | Current bodies, source inventory and native relationships | This ownership/prerequisite audit, exact snapshot, acyclic graph and preserved requirements; documentation checks and required CI | #656 |
| #687 | CI/test partition capacity; no runtime prerequisite | Split the near-deadline Terraform partition under 0016 while preserving every scenario, coverage artifact and parity input | Required CI and #654's complete matrix |

## Reconciled prerequisites and retained cases

1. **#649/#654 verification ownership.** The maintainer approved storage and
   transaction proof in #649 against real PostgreSQL and the actual evaluator.
   #654 retains public-review and actual worker-report proof after #650/#651:
   two flag owners, owner-only reset, reset-before-add, later and previous-run
   snapshots, trigger-created runs with fresh flags, state fencing and rollback.
   Direct SQL fixtures or ordinary policy-free runs do not replace these tests.
2. **#649 requires #647.** Its approved actual-evaluator proof depends on the
   evaluator supplied by #647. The body and native direct blocker now both name
   it. This exposes an existing prerequisite without changing storage scope or
   moving a test into a different ticket.
3. **#664 requires #663.** Its approved public import→create→simulate test requires
   #663's simulation operation. Both body and native blocker now express that
   prerequisite. Catalogue membership, licensing, complete corpus execution and
   operation acceptance remain mandatory.
4. **#657 and #661 preserve their bounded proof.** Scheme storage/revocation and
   the caller's administrative path can be verified before #658's complete
   login-grant implementation. Sealed storage/budget/expiration and existing
   receipt metadata can be verified before #662's sample-content HTTP handlers.
   #658/#662/#666 retain the actual admission and public-content access cases.
5. **#687 preserves CI evidence.** The triggering run passed all 173 Terraform
   scenarios and 5,229 steps but reached 9m58.445516312s before cleanup; the Go
   process timed out at 600.125s. Its 10-minute test deadline differs from the
   30-minute Actions job budget. The separate ticket applies 0016's partition
   split rule while keeping all tests. A timed-out job is not a passing CI gate.

The native graph is checked after reconciliation. It contains no cycle or duplicate
parent membership, and open implementation tickets' direct blocker sets agree with their reviewed
dependency sections. Completed #672 retains its original non-template dependency
format; #679 separately labels its completed supporting references. Their native
blockers and completed work are preserved. The snapshot is dated. Its ready frontier uses completed native predecessors;
stacked work records the parent revision and passes required CI after that
parent lands. Epic checkbox synchronization and executed phase evidence retain
their existing owners.

## Required real OPA and Rego phase matrix

The maintainer's 2026-11-20 requirement is explicit in #654 and #666. Both gates
must record the actual linked OPA version, exact source revision, CI run and
executed program/dialect/workflow rows.

- **Language:** actual REGO_V0 and REGO_V1 programs, compatible imports and
  rejected wrong/unsupported dialects; booleans, sets, objects, comprehensions,
  helpers and the captured `sanitized()` hash; check/save diagnostic locations.
- **Decisions:** policy-free behavior and attached defaults; one/many policies;
  approval/rejection, plan deny/warn, push ignore/no-trigger/lock/priority and
  trigger fallback. Assert committed state/history, receipts and owner flags.
- **Failures and isolation:** actual expensive-program deadlines, combined
  cancellation, configured input/message/print limits, forbidden built-ins and
  certified FIPS behavior; separate input/helper/print state across concurrent
  programs; transactional rollback of receipts/flags/outbox/jobs.
- **Compatibility:** recorded vendor OPA results, full captured decisions and
  preamble defaults; #664's complete pinned MIT corpus, with namespace rewriting
  restricted to code paths and arbitrary string values preserved.
- **Services and entrypoints:** real public reviews, worker reports, queue jobs,
  PostgreSQL, blobs and pinned Terraform-family tools. #655 independently proves
  real provider behavior through vcs-lab. P13b adds actual login, task,
  initialization, simulation and sealed sample/capture access/expiry paths.

Each implementation PR still supplies its own success/403/failure evidence and
passes required CI, including the 95% total/90% per-package coverage gate.
Compilation-only or canned evaluator results cannot complete this phase matrix.

## Evidence and future-phase limits

- P13's receipt/flag metadata and requested sampling evidence remain distinct
  from #661's linked sealed sample. Sample requests do not establish retention;
  a receipt with no linked object remains unsampled. #662 owns sealed-content
  reads and `runPolicyReceiptSample`.
- Durable `policy_evaluated`/`internal_error` production belongs to P13. P18 owns
  notification/inbox/webhook delivery; event persistence is verified independently.
- #644's cleaned-up lab and the maintainer's dated inference authorization permit
  the accepted LOGIN flag, TASK denial and sample/capture retention/privacy
  implementations. C47 legacy comparison and C50/C52/C53's missing upstream
  observations remain evidence questions. Authentic responses stay unchanged.
- #675's historical partial PR #680 did not complete the producer requirement.
  The maintainer subsequently approved the source-pinned experimental producer;
  its actual CLI output and positive decrypted-upload/mask/copy acceptance now
  pass exact-main CI. [The delivery audit](policy-p13-delivery.md) preserves the
  official-release limitation, authored negative controls and final quality gate.
- P14 owns launcher-side initialization/exec; P16 dependency execution; P17
  module-version triggers; P18 notification routing; P25 Intent execution.
  Typed schemas and pure decision tests do not deliver those workflows.

## Reproduction

Inspect the exact issue and native predecessor sets with `gh api` and compare
body hashes/update times with the adjacent snapshot. Re-read #635/#636 native
sub-issue sets before changing ownership. Follow the backlog reconciliation
rules and preserve human additions, completed work and every accepted test.

Run `make docs-check` and `make sig-check` for this report's changes. The PR must
pass its required CI. Runtime acceptance remains with the implementation and
phase-gate owners in the table; this document does not replace those executions.

## Current delivery refresh — 2026-11-21

The dated native-dependency snapshot above remains historical. The [delivery audit](policy-p13-delivery.md) now records main `3ff32129c22db94166780230d3c637f829655401`, full quality run 44898323446 and the successful four-provider lab 44934050424, with exact report hashes and cleanup. Sampling, simulation, catalogue, LOGIN/TASK/server INITIALIZATION and page projections remain with their P13b owners. No historical capture or issue-body hash is rewritten.
