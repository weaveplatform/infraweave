# P13 real-provider policy verification

[POL-18 (#655)](https://github.com/weaveplatform/infraweave/issues/655) extends
the existing `vcs-lab` workflow and partition. It retains the source, signed
webhook, check and comment regressions. The new cases live in
`test/acceptance/features/vcs/feedback.feature`, so the existing three-feature
lab sequence executes them. Each provider's native Git client creates actual
commits, pushes and pull requests. No direct webhook POST substitutes for
upstream delivery.

## Required provider and workflow matrix

GitHub, GitLab, Bitbucket Cloud and Azure DevOps are mandatory. Bitbucket Data
Center runs when its licence is configured; an unconfigured job's success is
not an executed provider row.

| Named scenario | Required evidence |
|---|---|
| Real upstream proposals retain both policy dialects and one native plan comment | A native branch push and opened request produce one proposed run. Stored V1 push and V0/V1 plan programs run over an actual Terraform report. Both warning messages, receipt versions/languages, exact owned flags, the original native check's success and one labelled plan comment are verified. |
| Real upstream replacements discard an OpenTofu plan and trigger an isolated target | One native tracked push produces an actual unconfirmed OpenTofu plan. Another native push cancels it in favor of its high-priority replacement, preserving the exact transition note once. A V1 terminal program creates one tracked target at the second repository's own commit, with no inherited source flags. The replacement also supplies an actual worker plan and is discarded through the public API at cleanup. |
| Real upstream edit and revert pushes preserve every touched path | One genuine two-commit push edits and restores `main.tf`. A stored V1 push policy requires both commits and the touched-file union before admitting a run. The actual worker plans and finishes, and its native check reaches success. This supplements #696/#701's deterministic truncation and recovery controls; it does not claim that every provider truncates this particular two-commit event. |

`TestUnitLivePolicyProgramsCompileWithActualOPA` extracts and compiles the
authored programs from these actual scenario definitions as preflight. It does
not count compilation as a passing upstream row. The native Git helper also
runs against real Gitea, verifying the remote's two-parent chain and identical
endpoint files while exercising the existing Azure clone fallback. That helper
test is not Azure service evidence.

## GitHub check identity correction

The baseline real-provider run
[44803036616](https://github.com/weaveplatform/infraweave/actions/runs/44803036616)
tested main `db2b8e58028970f350ff3eaa6b8ac93f73e8ed47`. Its GitHub source contract
passed, but its original webhook workflow refused duplicate or foreign native
check resources; repository destruction completed. A previous run at
`45b97b0d79d3fa929ca7f0cc7a0bced788c0a4f4` had the same class of failure.

`TestUnitAcknowledgedCheckSurvivesStaleListing` independently demonstrates a
specific defect: after a successful create, the adapter ignored the saved native
ID and created another resource when a subsequent list response omitted the
acknowledged check. The repair uses GitHub's
[single-check read endpoint](https://docs.github.com/en/rest/checks/runs#get-a-check-run),
validates the App, commit, name, stack and recovery marker, and updates that
identity. A failed lookup never falls back to another create. Unacknowledged
create recovery and the existing explicit reopen-refusal behavior remain
separate paths.

The deterministic regression and its refusal controls passing are evidence for
that defect. The repaired revision now passes the actual lab as recorded below. The
original upstream failure remains preserved; the deterministic stale-listing
regression identifies the specific adapter defect independently. The verifier retains its
duplicate/owner refusals and reports only bounded diagnostic facts.

## Execution, reports and cleanup

The protected `vcs-lab` environment and the workflow admit only main. After
required CI passes and the implementation merges, dispatch the existing lab
against that merged revision. The harness uses the committed public runner
image digests and builds the application from the tested source. Record the
source SHA, image pins and actual workflow results before claiming delivery.
Keep related corrections together in the implementation PR while it is open;
the main-only environment requires post-merge live verification as well.

Each provider's `vcs-lab-results-<provider>` artifact retains all three feature
reports, the merged Cucumber report, safe progress traces, source revision/run
metadata and cleanup outcomes. Failure in a feature still retains that
feature's report, and clears the previous report before the next feature so an
early build failure cannot reuse stale results. Cleanup outcomes and source
metadata are retained even after a partially failed Terraform apply. The
workflow restores the GitHub App's parked hook or detaches
the run-owned repository hooks, destroys the run's Terraform resources, verifies
that its state is empty, closes its tunnel with the existing terminal-outcome
check, and removes its local provider container. The infraweave stacks and
policies live in the harness's disposable database and are removed with that
database and its owned processes/containers at suite teardown.

The exact implemented revision, all four provider results, attempts and cleanup
outcomes must pass before #655 is complete. Provider configuration skips and
infrastructure failures stay visible. #656 owns the subsequent phase audit.

## Verified current main — 2026-11-21

[Native run 44934050424](https://github.com/weaveplatform/infraweave/actions/runs/44934050424) passes at `3ff32129c22db94166780230d3c637f829655401`, in one attempt. GitHub executes six scenarios / 192 steps; GitLab, Bitbucket Cloud and Azure DevOps each execute six / 193. All 24 scenarios / 771 steps pass, including the source credential contract and actual provider delivery. All twelve Terraform platform batches verify readiness, empty state, no owned resources and private-input removal. Applicable repository/hook/App/tunnel/GitLab cleanup passes. Data Center is unconfigured because its licence is absent.

The [provider proof](policy-native-provider-verification.json) retains artifact IDs, report hashes, source, attempt and raw cleanup outcomes. The [full source gate](https://github.com/weaveplatform/infraweave/actions/runs/44898323446) passes at 95.4% total coverage with every package meeting 90%, including complete certified FIPS. Historical failed lab attempts remain separate in the [delivery audit](policy-p13-delivery.md). No direct webhook POST or provider model is counted as native delivery.
