# Complete pushed-path ranges

Issue: [#696](https://github.com/weaveplatform/infraweave/issues/696). This extends
[#652](https://github.com/weaveplatform/infraweave/issues/652)'s complete native
commit handling. Real-provider phase evidence remains with
[#655](https://github.com/weaveplatform/infraweave/issues/655).

## Selection and provenance

[Spacelift's push policy documentation](https://docs.spacelift.io/concepts/policy/push-policy#ignoring-certain-paths)
includes paths touched by intermediate commits, even when a later commit
reverts or deletes their changes. Comparing only the delivered before and head
trees loses these paths.

Source preparation uses a complete signed native collection when available.
Otherwise the original account-bound integration reads the immutable native
history range: commits reachable from head but not before. Each recovered
commit carries its actual metadata and complete first-parent touched paths.
The final sorted path union also includes the existing full before/head delta,
preserving force-push removals when no new commit becomes reachable. Stack
counts, default selection and typed policy input use that final union.

The normalized event retains its signed `commits` source, items and omission
reason. Repeated commit identities cannot establish completeness. REST-recovered facts have a separate `recoveredCommits` field in the
owned prepared document. Recovery never relabels a truncated signed subset as
complete. Typed policy input uses the recovered collection when present; known
empty commits and empty ranges are arrays. Unknown paths are not fabricated as
empty arrays.

For a new branch, recovery freezes the merge base with the repository's default
branch and returns that immutable anchor to source preparation. The later
endpoint comparison uses the anchor, rather than rereading a mutable default
branch. Deleted branches and tag events retain their existing separate paths.

## Native APIs and limits

The investigation uses the providers' primary REST contracts, not third-party
examples. The repository pins `go-github/v93` at `v93.0.0` and GitLab's Go client
at `v1.46.0`; the other adapters use their existing explicit REST clients.

| Provider | Complete range | Complete per-commit paths |
| --- | --- | --- |
| GitHub | Page compare commits with `page`/`per_page`; require a stable `total_commits` and progressing pages. For existing unrelated histories, page both immutable histories with `sha` and subtract before ancestors. | Read complete commit trees; compare each commit with its first parent. Recursive-tree truncation falls back to complete single-level trees. |
| GitLab | Page `repository/commits` with the immutable `before..head` revision range. A new branch first resolves `repository/merge_base`. | Page recursive repository trees, retaining object identity, type and mode; compare each commit's first-parent trees. |
| Bitbucket Cloud | Follow the native `next` links for `commits?include=head&exclude=before`. | Follow every page of single-commit `diffstat/SHA`, whose documented comparison is against its first parent. Preserve both old and new names. |
| Bitbucket Data Center | Page native commits with exclusive `since=before` and inclusive `until=head`, following `nextPageStart`. | Enumerate complete file trees and read each candidate path's diff. A root commit uses its complete paged file list. The global changes endpoint's unpageable hard cap cannot hide paths. |
| Azure DevOps | Page each immutable `searchCriteria.itemVersion` history using `$skip`/`$top`, then subtract before ancestors from head ancestors. | Page `commits/SHA/changes` with `skip`/`top`; verify its change counts. Retain `originalPath`, `sourceServerItem` and the current file path; folder entries are not files. |

GitHub's compare file list is capped at 300, independently of commit pagination;
its default unpaged commit response is capped at 250. Recovery uses neither
file list as a complete path source. GitLab documents that comparison diffs
can be incomplete. Azure documents `fromCommitId`/`toCommitId` as alphabetical
bounds, so they are not used as ancestry ranges. The Azure implementation
deliberately reads both histories rather than inferring an undocumented
direction for a two-version query. This can require more reads on large
repositories; the existing timeout and rate-limit boundaries still apply.

Primary references:

- [GitHub compare and commit pagination](https://docs.github.com/en/rest/commits/commits#compare-two-commits)
  and [Git trees](https://docs.github.com/en/rest/git/trees#get-a-tree).
- [GitLab commit revision ranges](https://docs.gitlab.com/api/commits/#list-repository-commits),
  [repository trees](https://docs.gitlab.com/api/repositories/#list-all-repository-trees-in-a-project),
  [merge bases](https://docs.gitlab.com/api/repositories/#merge-base) and
  [comparison limits](https://docs.gitlab.com/api/repositories/#compare-branches-tags-or-commits).
- [Bitbucket Cloud commits and diffstat](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-commits/).
- [Data Center repository REST](https://developer.atlassian.com/server/bitbucket/rest/v900/api-group-repository/)
  and the [official since/until parameter contract](https://docs.atlassian.com/bitbucket-server/rest/7.17.0/bitbucket-rest.html).
- [Azure immutable commit histories and pagination](https://learn.microsoft.com/en-us/rest/api/azure/devops/git/commits/get-commits?view=azure-devops-rest-7.1),
  [query criteria semantics](https://learn.microsoft.com/en-us/javascript/api/azure-devops-extension-api/gitquerycommitscriteria)
  and [paged commit changes](https://learn.microsoft.com/en-us/rest/api/azure/devops/git/commits/get-changes?view=azure-devops-rest-7.1).

## Failure and transaction boundaries

All external reads occur during source preparation before the dispatcher holds
its creation transaction. Recovery uses the original delivery's integration,
repository identity guards and deadline. The recovery path requires its signed
immutable repository ID and reads native metadata before any source object or
history. It refuses an ID mismatch or unreconciled coordinate change; a rename
already reconciled by the owner uses the current coordinates and the same ID.
Missing identity, metadata access failure and identity mismatch remain distinct
private causes, while intake publishes only its fixed safe failure. Complete
native collections retain their existing path. Provider recovery operations also
retain the adapters' existing 30-second bound. No body or credential leaves
the source owner through the prepared facts.

A refused or failed read, missing immutable ref, inconsistent count, repeated
page or commit, missing required metadata or incomplete file result cannot
become a successful empty range. Native rate-limit errors retain their reset
for non-consuming queue snoozes. Other operational errors retain their cause;
existing intake failure handling publishes safe diagnostics. Parent dispatch,
prepared storage, child evaluation, initial receipts, flags, locks, cancellation,
once-per-commit admission and feedback retain their existing transaction and
generation fences.

## Recorded implementation evidence

These are local real-service checks, not upstream captures. They run actual
server processes, PostgreSQL, object storage and embedded OPA against native
HTTP fakes backed by actual Git objects. The worker cases also run actual
pinned Terraform/OpenTofu tools. Docker is required explicitly.

The initial signed GitHub A→C edit/revert case failed against the previous
covered binary: 11 setup/assertion steps passed, then zero runs were created
instead of one. With the recovery implementation, the same case passed all
12 steps under certified FIPS in 9.705 seconds, seed `696002`.

The native adapter matrix also passes first-parent edit/revert, rename/delete,
new-branch merge-base freezing, divergent/backward force pushes, unrelated root
histories, missing integration/ref, 105-commit pagination and 205-file
pagination for all five providers. Each adapter preserves real HTTP rate-limit,
server-failure and context-deadline errors.

The expanded server matrix passes 12 scenarios / 265 steps under certified FIPS
in 11.361 seconds, seed `696004`. It includes all five absent-native forms,
GitHub's 2,048 boundary, GitLab's 21 commits versus newest 20, Cloud's six
commits versus newest five and absent per-commit paths, and Azure's shared
multi-ref collection. Assertions cover recovered provenance, path union,
affected-stack count, typed OPA facts, public receipts/flags and deduplication.
The first expanded run also passed both actual Terraform and OpenTofu default
selection worker cases; its policy receipt fixture used a different flag from
the shared assertion and was corrected before the green 12-case rerun.

The source-fault recovery and expiry matrix passes 15 scenarios / 410 steps
under certified FIPS in 31.164 seconds, seed `696005`, on integrated revision
`d817bd19b8d5087d7d5eb090a6bea23ac403187d`. It proves non-consuming rate-limit
snoozes, provider-error retries, no provisional preparation/children/locks or
cancellation, successful single retry admission, duplicate refusal and expiry.

The signed identity reproducer on that pre-fix binary passes the five missing-ID
refusals and fails all five different-ID cases because native source reads
occurred. Native identity checks also cover bound rename success and actual
rename-plus-path-reuse refusal before reading the old ref.

The combined implementation matrix passes **44 scenarios / 1,041 steps** under
certified FIPS in **181.057 seconds**, seed `696008`, at revision
`c4fbd234c099a37f595743524d1a74c787204c6a`. The covered app's SHA-256 is
`c3f2c65563ad610d7984694f68ff7dab8bbd5ca231b602ea0c73084614f07af0`.
It includes 12 native-boundary cases, 15 source-fault/retry/expiry cases, ten
signed repository identity refusals, both actual worker cases and five cases
that kill both server processes during child completion and recover once.
All 1,041 recorded steps pass; no scenario is skipped.

Documentation, signature, reference and generated-code checks also pass. The
remaining affected regressions, race/shuffle, lint, merged coverage and required
CI/FIPS/parity checks are final merge gates, not inferred from this focused
matrix. The CI run on the final PR head remains authoritative.
