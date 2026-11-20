# GIT_PUSH workflow verification

[POL-15 (#652)](https://github.com/weaveplatform/infraweave/issues/652) evaluates
stored, verified deliveries through the real policy selector and embedded OPA.
Webhook intake does not evaluate a policy. The child holds the account, space,
integration, stack and queue generation while committing the new run, initial
receipts, owned flags, policy lock and eligible cancellations. A duplicate or
refused replacement performs no lock or cancellation effects.

With no selected policy, the existing default processing remains active. A
selected explicit ignore records `CUSTOM_POLICY`; other selected no-run results
record `CUSTOM_POLICY_NO_TRIGGER`. The intentional default-disable label is
`feature:disable_default_push_policy`. Invalid results, runtime errors and
conflicting priority or lock decisions create no run and commit a failed native
check request together with safe `policy.internal_error` metadata. Existing
notification options still govern successful ignored checks. External dependency
and module-version directives remain in typed decisions; P16/P17 own execution.

## Native input completeness

Native request preparation uses the original account-bound target integration,
outside database transactions. A persisted snapshot includes native source and
target identities, creator, action actor, current comment, base and head commits,
full comparison paths and optional native aggregate facts. Unknown approval,
mergeability or ancestry stays absent. A headless comment resolves its native
request first. A policy attached after a no-policy preparation hint causes a
bounded preparation retry outside the child transaction, then rechecks every
authoritative fence. Concurrent children reuse the first committed snapshot.
Native failure strings do not enter durable queue errors.

The normalized event's `commits` object records `source`, native `items` and an
optional `omission` reason. This provenance object differs from the typed policy
input's `push.commits` array. Only a proven complete collection with all required
per-commit metadata and file lists is projected. Missing arrays stay absent;
known empty complete arrays stay empty. No missing file list becomes `[]`.

| Provider | Native source and completeness boundary | Policy projection |
|---|---|---|
| GitHub | `commits`, below the documented 2,048-commit cap; boundary-sized arrays conservatively use `provider-commit-limit` | Native author, message, timestamp, hash, URL and complete added/modified/removed paths, in native order |
| GitLab | `commits`, with `total_commits_count` equal to the array length and at most 20 entries | Same required native facts; missing totals use `unknown-commit-count`, truncated arrays use `provider-commit-limit` |
| Bitbucket Cloud | `push.changes[].commits`, with explicit `truncated: false` and at most five entries | Metadata is retained; native commit objects lack required per-commit paths, so nonempty collections use `missing-commit-files` unless an earlier completeness refusal applies |
| Azure DevOps | `resource.commits` | Native metadata is retained; unavailable per-commit paths use `missing-commit-files`. A shared array on multiple ref updates uses `multiple-ref-updates` because it cannot be attributed to one ref |
| Bitbucket Data Center | The consumed `repo:refs_changed` event supplies ref changes, without a native commit collection | `push.commits` stays absent |

Missing timestamps or invalid hashes use `incomplete-commit-metadata`. A damaged
or legacy stored collection is checked again at typed projection. When a complete native collection exists, its touched-path union augments the
immutable before/head diff before affected-stack counts and selection. Thus an
intermediate edit and revert remains consistent with the retained commits. For
unavailable or truncated collections the existing before/head diff remains;
full provider range recovery remains with [#696](https://github.com/weaveplatform/infraweave/issues/696). No extra commit-list HTTP
crawl or new provider credential is introduced.

Primary provider documentation, checked 2026-11-20:

- [GitHub push webhook payload and 2,048 limit](https://docs.github.com/en/webhooks/webhook-events-and-payloads#push).
- [GitLab push event, total count and newest 20 entries](https://docs.gitlab.com/user/project/integrations/webhook_events/#push-events).
- [Bitbucket Cloud push payload and explicit five-entry truncation](https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/#Push).
- [Azure DevOps code-pushed service-hook payload](https://learn.microsoft.com/en-us/azure/devops/service-hooks/events?view=azure-devops#git.push).
- [Bitbucket Data Center native event payloads](https://confluence.atlassian.com/bitbucketserver/event-payload-938025882.html).

## Acceptance boundaries

All listed features use two real server processes, PostgreSQL, the real selector
and embedded OPA. Local provider fakes serve documented native APIs and signed
deliveries; these are synthetic fixtures, not upstream captures.

| Feature under `test/acceptance/features/policies` | Evidence |
|---|---|
| `push.feature` | Explicit ignore, no match, default-disable label, mixed V0/V1 decisions, zero affected stacks, initial receipts/flags, deduplication and actual GitLab failed feedback |
| `push_requests.feature` | All five providers: native PR selection, no-match before matching comment actor/text, and branch/PR once-per-commit behavior |
| `push_cancellation.feature` | All five providers: queued/ready cancellation, unrelated blocking-run preservation and exact replacement notes |
| `push_workers.feature` | All five providers with real Terraform and OpenTofu containers: unconfirmed T27 replacement, private-pool priority and new real plans |
| `push_forks.feature` | All five providers: distinct readable source identity rejected until explicit `allow_fork`, then one evidenced run |
| `push_recovery.feature` | All five providers: native branch/PR race, kill both actual servers after provisional creation/evidence/cancellation but before queue completion, verify rollback, restart servers and recover one atomic replacement |
| `push_preparation.feature` | A late policy on a previously headless GitHub comment commits its failed native check and no run |
| `push_failures.feature` | All five providers: real runtime evaluation conflict, invalid output, conflicting locks/priorities, durable native failure plus internal-error metadata and no escaped effects |
| `push_commits.feature` | Real Git objects supply complete GitHub/GitLab commit metadata; a real 21-commit GitLab history retains the newest 20 but omits the incomplete policy array |
| `push_locks.feature` | All five providers: a new run and lock commit together; matching unlock admits the next run and clears the policy-owned lock |

The recovery fixture changes only timing: PostgreSQL pauses the actual child
completion transaction, process signals kill its owners, and dead-worker leases
are advanced before fresh servers execute production recovery. The native fork
fixtures share a real seed commit with their parent and deliberately do not
claim unique fork-tip worker checkout. Actual PostgreSQL tests separately check
receipt/cancellation rollback, account/stack/type qualifications, replay and
generation fences, concurrent immutable-snapshot winners and backend failures.

Mandatory real-provider `vcs-lab` evidence remains with
[POL-18 (#655)](https://github.com/weaveplatform/infraweave/issues/655), alongside
the phase's combined walkthrough. A local provider fake does not satisfy that
phase-exit gate. Final tested revision, counts, commands and authoritative CI
results belong in the implementation PR.
