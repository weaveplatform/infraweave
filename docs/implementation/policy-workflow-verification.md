# Policy workflow verification

[POL-17 (#654)](https://github.com/weaveplatform/infraweave/issues/654) joins the
workflow evidence from #649–#653. Its new scenarios carry `@policies`; their
dependency tags retain the existing CI partitions. PostgreSQL, queue jobs,
object storage, Gitea and the Terraform-family executables are real services and
tools. The five provider HTTP servers model upstream APIs. Real-provider proof
remains a separate, mandatory [POL-18 (#655)](https://github.com/weaveplatform/infraweave/issues/655)
gate.

The tested revision, linked OPA version, complete Cucumber results and final CI
run belong in the implementation PR. A pending check or a later-phase handoff
does not count as a passing row. `GET /api/v1/policy-runtime` independently checks
the version linked into each server against `go.mod`.

## Public workflow and language matrix

Paths below are relative to `test/acceptance/features`. Each named scenario uses
stored programs evaluated by the actual OPA runtime in the server. No helper
inserts a policy decision, run receipt or owned flag directly into storage.

| Contract | Executed scenario source and assertion |
|---|---|
| Both dialects, compatibility imports, booleans, sets and helper rules | `policies/workflow_approval_flags.feature`, **Actual reviews reset one owner before adding flags without losing another owner**: V1 Alpha and V0 Beta with an actually used `future.keywords.in` import run through three public flag snapshots. |
| Owner-only reset, reset before add, later input and committed projections | The same review scenario checks shared ownership before and after each reset. `policies/workflow_plan_flags.feature`, **Actual terraform/opentofu reports preserve owners, reset order and previous-run snapshots**, repeats the proof through native full, alpha-only and beta-only plans. Every checkpoint compares the public API with the committed `run_updated` projection. |
| Actual plan documents, comprehensions and `sanitized()` | `policies/plan_workers.feature`, **REGO_V0/REGO_V1 validates an actual sanitized terraform/opentofu plan and applies in the same container**, checks the worker upload, vendor version and captured hash helper. **Terragrunt evaluates exactly the selected sanitized units with run-all false/true** covers selected units and both underlying tools. |
| Previous-run snapshot and trigger isolation | `policies/workflow_plan_flags.feature` discards the source through the public API, runs the downstream stack at its own tracked commit with no inherited flags, then creates a new source run whose policy reads the previous source's committed owner flags. |
| Real rollback and worker retry | The same two-vendor scenario pauses PostgreSQL at the native report's event insert, after provisional policy writes. It verifies no receipt, flag, policy event or orphaned fanout job committed; terminates only that report's backend; and lets the actual worker retry. Exact receipt counts and public projections exclude partial or duplicate effects. |
| Individual and combined execution deadlines | `policies/workflow_runtime.feature`, **The individual/combined deadline keeps a reviewed run queued**, executes a four-level expensive Rego search after a real review. Another policy's approval cannot overcome `TIMEOUT`; the expensive program's partial flag does not commit. |
| Input limit, malformed decision and runtime conflict | `policies/workflow_limits.feature`, **A review retains input overflow / a malformed boolean / conflicting booleans without partial flags**, verifies `LIMIT`, `RESULT` and `EXECUTION` receipts, QUEUED state and empty flags after successful public review writes. |
| Partial objects and concurrent input/helper state | `policies/workflow_isolation.feature`, **Concurrent programs retain their input, helper and private print state**, creates eight distinct stack owners. Concurrent pairs hit the same server and prepared-query cache, alternating replicas. Both dialects use the same partial-object helper names; their flags must contain only that program's owner and that run's ID. Public receipts, real owner rows and committed projections agree. |
| Message and print bounds | `policies/workflow_bounds.feature`, **A real review bounds oversized notes without exposing private prints**, executes 105 oversized Unicode notes and more than 64 KiB of printing. The stored decision retains 100 notes of at most 4,096 bytes without splitting UTF-8. Private print text is absent from retained decisions, public responses and process logs. The concurrent scenario separately checks private per-evaluation printing. |
| Complete GitOps walkthrough on every provider adapter | `policies/workflow_gitops.feature`, **A signed GitHub/GitLab/Bitbucket Cloud/Bitbucket DC/Azure DevOps proposal evaluates its real plan and publishes one labelled comment**, sends a verified request, a branch push and a duplicate delivery through both replicas. One PROPOSED run consumes an actual Terraform upload, retains a PLAN warning, and finishes with its native successful check and exactly one labelled plan comment. The check's SHA, run URL, identity and real change summary are verified. |
| Policy-free behavior and attached defaults | `policies/approval.feature`, **Removing the only approval policy restores policy-free scheduling**, and **Every policy must approve when V0 and V1 run together**. `policies/push.feature` retains the no-policy processing path, explicit default-disable control and selected no-trigger behavior. Existing run/vendor acceptance remains part of the full gate. |
| Review states, precedence and fencing | `policies/approval.feature` covers one rejecting policy among approvers, stale versions and idempotent replay. `policies/approval_workers.feature`, **Queued and prior plan reviews cannot approve a later state version or replace confirmation**, uses actual Terraform/OpenTofu workers. `policies/plan.feature` covers a real deny before apply, proposed PENDING_REVIEW and rejection with the retained failure history. |
| Push defaults, ignore/deny, lock and priority conflicts | `policies/push.feature`, `push_failures.feature` and `push_locks.feature` exercise stored programs through native event jobs. `push_recovery.feature` kills both replicas during child completion and recovers one complete result. #696's range-recovery proof and #655's upstream runs remain separately identified. |
| Terminal states, durable queue recovery and normal-priority fallback | `policies/trigger_terminal.feature` drives real failed apply, discard, cancellation, stopping and task completion. `trigger_recovery.feature`, **A crash at queue claim/completion recovers one complete fan-out**, verifies replica restart. `trigger_semantics.feature`, **Conflicting V0 and V1 priorities are normal with durable errors and isolated children**, verifies both dialects and the downstream fallback. |
| Wrong dialect, engine changes, located errors and disabled built-ins | `policies/check.feature`, **Editor checks and policy saves share compilation and exact diagnostics**, checks malformed modules, wrong dialect, `rego.v2` and `http.send` through public check/save operations. `policies/management.feature` covers engine updates. Runtime rows above supply the separate actual decision proof. |
| DEFAULT/SMART/NONE at the actual artifact boundary | `vendors/sanitization.feature`, **A real terraform/opentofu plan uses DEFAULT/SMART/NONE, sanitizes again on replan and applies its raw saved plan**, and **Terragrunt uploads its selected units under DEFAULT/SMART/NONE with run-all false/true**, inspect actual decrypted worker uploads. `sanitization_fence.feature` and `sanitization_unsupported.feature` retain refusal and mode-receipt controls. |

The concurrency row observes setup's event fanout, scheduling and approval jobs
finishing before it tests execution-state isolation in pairs. No helper
completes or cancels those jobs. `TestUnitPreparedIsolation` additionally runs
80 actual OPA evaluations across two cached bodies in each dialect and checks
each evaluation's exact private print output alongside its input-derived
decision. The workflow cases check that neither stdout nor stderr exposes it.
These checks do not claim
that arbitrary simultaneous serializable writers finish within ADR 0005's
three-retry budget. Earlier diagnostics, including concurrent pairs during
setup's background reevaluations, exhausted that budget. Their failed evidence
is retained separately and is not a passing matrix row.

## Capture and corpus provenance

`TestAccCaptures` checks the recorded documents before replaying the implemented
REST calls against the real server and PostgreSQL. It reports deferred replay
and assertion handoffs explicitly. Authored workflow programs above supplement
these records; they do not turn an inference into an upstream observation.

| Records | Named replay/evidence gate and P13 use |
|---|---|
| C9 | `TestAccCaptures/C9/recorded` and `TestAccCaptures/C9`: deletion behavior; policy management and attachment lifecycle. |
| C46, C48 | Corresponding `/recorded` subtests preserve entitlement refusals, observed fields and each assertion's REST/deviation/inference/future-owner classification. C48's implemented REST subset is replayed independently. Authored push tests exercise the accepted ignored/no-trigger contracts without claiming the free-tier account observed them. |
| C49, C56, C57 | Recorded omitted-engine result, timestamp request bounds and vendor runtime version. Implemented management/runtime replays retain the intentional V1 default deviation and check the actual linked local version independently. |
| C51, C291 | Recorded flag-reset and review-rejection evidence; the new public owner-reset scenarios and existing actual rejection worker scenarios exercise their implemented workflow contracts. |
| C54, C192 | Recorded catalogue/OPA decision evidence. `TestUnitRegoCapturedDecisions` and `TestUnitRecordedDecisions` compare complete captured results under supported dialects and typed decision combination. Catalogue/import completion remains with #664. |
| C47, C50, C52, C53, C273 | `TestAccCaptures/<id>/recorded` verifies the tooling documents and cleanup. Legacy comparison, actual LOGIN flags, TASK/INITIALIZATION admission and sealed sample/capture behavior remain visibly assigned to their P13b owners; skipped REST replay is not counted as implementation. |

The five MIT files in `test/fixtures/rego-compatibility` are pinned by path,
commit, license and SHA-256. `TestUnitRegoMITCompatibilityCorpus` runs their
authored positive/negative inputs through the actual linked OPA library and
compares full decisions. Only the package namespace is rewritten in code;
arbitrary strings retain their original values. This representative corpus
supplements public workflow proof. The complete catalogue corpus and import
workflow belong to #664 and the #666 gate.

P13's sample-request receipt metadata does not prove sealed sample storage,
retention, privacy or expiry. #661/#662/#666 own those paths. Authored deferred
artifact fixtures do not establish native deferred-plan capture. #675's later
maintainer-approved source-pinned experimental capture and full positive
compatibility matrix now pass exact-main CI; official-release production
remains unproven. The [delivery audit](policy-workflow-delivery.md) retains that
distinction. The [verification ownership record](policy-verification-ownership.md)
and each capture's handoffs remain authoritative for later phases.

## Reproduction and phase gate

Run the new combined workflow slice with Docker available:

```sh
GOFIPS140=certified GODEBUG=fips140=only \
INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1 \
INFRAWEAVE_ACCEPT_TAGS=@policies make accept
```

`make accept` writes the executed scenarios to `cover/accept/cucumber.json`.
The tag selects the seven new feature files; it does not select the entire
phase regression matrix. Full verification additionally runs every relevant
acceptance partition, `TestAccCaptures`, PostgreSQL evaluator/queue fencing
tests, recorded OPA/corpus checks, and `make gate`. Required CI must pass the
95% total and 90% per-package coverage thresholds and race/FIPS checks before
this issue is complete. #655's real-provider lab and #656's final audit remain
mandatory subsequent phase gates.

## Delivered source evidence

The [P13 delivery audit](policy-workflow-delivery.md) links current main `3ff32129c22db94166780230d3c637f829655401`, full quality run 44898323446 and the four-provider native run 44934050424. This matrix remains the language/workflow proof; native service delivery and the audit's own required CI are independent gates. Full catalogue and P13b claims remain excluded.
