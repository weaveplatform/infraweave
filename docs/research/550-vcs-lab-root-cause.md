# VCS lab root-cause investigation: #550

Status: working research scratchpad; the ticket is not complete.

Research baseline: main `37bc072c5accd67583df8f2d36857e9ecb550f3d`.
Checked against: the source at that commit and the native workflow evidence
linked below. Unknowns remain explicit; passing unit tests do not establish a
passing real-provider scenario.

## Completion contract

[Issue #550](https://github.com/weaveplatform/infraweave/issues/550) requires
**one main workflow run** that passes the source, signed webhook and native
feedback scenarios on GitHub, GitLab, Bitbucket Cloud and Azure DevOps:

- A real signed tracked push creates one tracked run and moves the tracked head.
- A push on another branch creates one proposed run; opening its pull request
  creates none.
- Genuine workers finish and their native checks reach success.
- The enabled feature publishes one plan comment per run; GitHub minimizes the
  earlier comment.
- An ordinary pull request comment is stored and starts no run.
- Hooks, tunnels and disposable repositories are cleaned up on success or failure.

Only Bitbucket Data Center's absent licence is an acceptable optional-provider
skip for this completion proof. Existing environment secrets and run-owned
credentials suffice. Public research must contain no identifying lab accounts,
cloud account IDs, credentials, private configuration or database dumps.

The governing sources are [decision 0016](decisions/0016-testing-and-acceptance.md),
[decision 0012](decisions/0012-vcs-integration-model.md), the
[implementation plan](../implementation/plan.md) and
[repository layout](../implementation/repo-layout.md).

## Native evidence ledger

### Earlier baseline

[Run 44512669320](https://github.com/weaveplatform/infraweave/actions/runs/44512669320)
tested `cea924c56afb5387a2d56cbc504e219f7b3eeacc`.
All four required source contracts and source features passed. None reached the
feedback feature. All applicable cleanup passed.

| Provider | First observed failure | What the evidence does not establish |
| --- | --- | --- |
| GitHub | Tracked run and pending check appeared; FINISHED wait timed out | Worker failure reason or preparation stage |
| GitLab | Tracked run and pending check appeared; FINISHED wait timed out | Worker failure reason or preparation stage |
| Bitbucket Cloud | Tracked worker/check completed; pull request head retention timed out | Failure of comment publication, which was not reached |
| Azure DevOps | Lab's native Git clone returned HTTP 400 before the tracked push | Failure of production worker checkout or an authentication cause |

### Current merged-fix validation

[Run 44523399115](https://github.com/weaveplatform/infraweave/actions/runs/44523399115)
tests the research baseline, including
[PR #625](https://github.com/weaveplatform/infraweave/pull/625) and
[PR #626](https://github.com/weaveplatform/infraweave/pull/626).

| Provider | Executed evidence | Current conclusion |
| --- | --- | --- |
| GitHub | Source contract PASS; sources 76.151s, webhooks 101.251s, feedback 44.505s PASS; no skip markers; App restoration, destruction and tunnel closure PASS | Complete GitHub path passes on this main commit |
| Bitbucket Cloud | Sources 75.159s and entire webhooks feature 97.217s PASS; feedback setup times out waiting for the new stack's initial tracked head; cleanup PASS | Prior PR-head failure is gone; first new failure precedes comment-specific assertions |
| Azure DevOps | Source contract and sources feature PASS; native clone still HTTP 400; cleanup PASS | Original clone failure remains unresolved |
| GitLab | Job still running at the initial research checkpoint | Await terminal evidence; no success or root-cause claim |
| Bitbucket Data Center | Missing-licence configuration skip | Optional; contributes no execution proof |

GitHub's native source contract took 4.71s. Successful nonverbose acceptance
output does not expose individual Cucumber row counts; none are invented here.

## Proven coverage mismatch

The source checks do not drive the same path as a real worker:

```text
source checks: REST metadata + nonempty URL + authenticated ref advertisement
real run: integration URL + credential -> Git pack fetch -> checkout/ownership
          -> tool/container -> worker reports -> run state -> native feedback
```

- [The shared source contract](../../internal/vcs/internal/core/port/sourcetest/sourcetest.go)
  checks that SourceURL is nonempty.
- [Credential reach](../../internal/vcs/internal/core/port/sourcetest/lab.go)
  checks the HTTP status and MIME type of `info/refs`, with Git protocol v2. It
  does not fetch a pack, check out a commit or start a worker.
- [GitHub's native source test](../../internal/vcs/internal/adapters/github/lab_test.go)
  independently constructs its canonical clone URL. It therefore bypassed the
  live integration URL defect corrected by #626.
- [Actual worker Git fetch](../../internal/agent/source/git.go) performs an
  exact-SHA shallow fetch, with a full-branch fallback for the explicit
  unsupported-exact-SHA error.
- [The lab's native push](../../test/acceptance/internal/vcslab/git.go) first
  performs a separate full clone. Azure fails here before a tracked push exists.
- [The pending-check step](../../test/acceptance/internal/steps/vcs/lab_feedback.go)
  observes a native check and releases reports; observing the check alone does
  not prove a worker has fetched source or started a container.

This explains why earlier green checks provided insufficient coverage. It does
**not** establish one runtime defect shared by every provider.

## Experiments and falsifiable hypotheses

Experiments change one variable at a time. A fix requires a failing baseline,
a controlled success, and a regression at the actual failing seam.

| Experiment | Prediction and controlled comparison | Status |
| --- | --- | --- |
| GitHub integration checkout host | Supplying the web host rather than defaulting it to the API host corrects the worker URL | Real handler/service/provider replay RED to GREEN; #626 merged; current native three-phase run PASS |
| Bitbucket abbreviated PR head | Expanding the native immutable prefix through the named integration restores head association | Documented-shape regression RED to GREEN; #625 merged; current full native webhook feature PASS |
| Azure ACK negotiation | If the pinned client's excluded ACK capabilities cause the 400, stock clone fails while upstream's documented fresh-clone compatibility mode succeeds against the same repository | Candidate; native A/B comparison not executed |
| GitLab execution | The first divergence in assignment, specification, fetch, ownership, tool, container, report or transition identifies the failing subsystem | No retained worker outcome yet; do not change timeouts based on the timeout alone |
| Bitbucket feedback setup | The second scenario's new stack must resolve the repository after the webhook scenario changes main | Investigate repository history, stack synchronization and the actual returned tracked head; do not infer a comment API defect |

### Azure comparison

The pinned `go-git` version is v5.19.3. Its
[unsupported capability list](https://github.com/go-git/go-git/blob/v5.19.3/plumbing/transport/common.go)
excludes `multi_ack` and `multi_ack_detailed`. Its own
[Azure example](https://github.com/go-git/go-git/blob/v5.19.3/_examples/azure_devops/main.go)
documents a fresh-clone workaround and warns about subsequent fetches.

Compare stock and compatibility clones in separate fresh processes, using the
same disposable repository, credential and immutable source. Both operations are
read-only on the provider. Keep credentials in the environment or private stdin,
never process arguments or public output. Record only phase, status, selected
capabilities and whether the expected commit was checked out. A workaround in
this experiment does not authorize changing global production Git settings.

### GitLab execution evidence

The [existing wait](../../test/acceptance/internal/steps/runs/runs.go) discards
history and failure notes already present in the run response.
[The poll limit](../../test/deps/eventually.go) is two minutes, while
[source fetch](../../internal/agent/source/source.go) may last ten minutes.
That mismatch makes the timeout ambiguous; it does not prove that increasing
the limit fixes anything.

Obtain the existing run history and launcher outcome at the first failed
boundary, retaining private data privately. Reproduce that boundary with the
same immutable inputs before selecting a code or timeout change. Any necessary
observation must answer a named hypothesis rather than add generic logging.

## Separate required-CI failure

[PR #627](https://github.com/weaveplatform/infraweave/pull/627) adds diagnostic
HTTP phase text; it does not fix Azure's clone. Its
[quality run](https://github.com/weaveplatform/infraweave/actions/runs/44522986570)
failed the existing ended-run log-deletion scenario with HTTP 500.

Private artifact correlation shows deletion overlapping log compaction, retained
chunks and an uncommitted deletion. Source inspection identifies a precise
candidate: deleting a pending compacted blob with a null checksum changes its
state to deleting, conflicting with the checksum/state database constraint.
The actual failing SQLSTATE was not retained. A blocked-upload PostgreSQL
reproduction is required to confirm the candidate. This is a separate CI defect,
not evidence explaining the native provider failures. No blind retry is a fix.

## Remaining completion evidence

- [x] GitHub native source, webhook and feedback path passes on the current main run.
- [x] Bitbucket Cloud's prior PR-head failure no longer occurs in that run.
- [ ] Reproduce and fix Azure's actual clone failure.
- [ ] Locate and reproduce GitLab's first failing execution boundary.
- [ ] Locate and reproduce Bitbucket feedback setup's tracked-head failure.
- [ ] Resolve required-CI failures through a confirmed reproduction.
- [ ] One final main run passes every required provider's complete scenarios,
  with cleanup and no required-provider skips.

This checklist records intermediate evidence. It does not replace #550's
single-run acceptance criterion or close that issue early.
