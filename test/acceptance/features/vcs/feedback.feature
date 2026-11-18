@postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops @docker
Feature: Repository runs report native checks and deployments
  Real launchers plan and apply the pinned Terraform and OpenTofu releases.
  The event sink and durable VCS jobs report historical run states through
  the providers' native APIs. Proposed and no-change runs create no deployment.
  ref: decision 0012
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  Scenario Outline: A recovered native deployment follows confirmation and apply while excluded runs stay silent
    Given a "<provider>" deployment stack "infra" using "<vendor>" with labels '[]'
    And the "<provider>" deployment API loses its next successful create response
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the "<provider>" deployment for run "run" on commit "seed" in environment "infraweave/infra" has statuses "<pending>"
    And a deployment report for run "run" succeeded after retry
    And the launcher "l" is killed
    And the relay holds the state backend's writes
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And deployment events for run "run" have settled
    And the "<provider>" deployment for run "run" on commit "seed" in environment "infraweave/infra" has statuses "<queued>"
    When the launcher "l" starts again and resumes its worker
    Then token "h" eventually reads the run "run" in the state "APPLYING" through B
    And the relay holds 1 write of the state of the run "run"
    And deployment events for run "run" have settled
    And the "<provider>" deployment for run "run" on commit "seed" in environment "infraweave/infra" has statuses "<applying>"
    When the relay releases the state backend's writes
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And deployment events for run "run" have settled
    And the "<provider>" deployment for run "run" on commit "seed" in environment "infraweave/infra" has statuses "<success>"
    And the "<provider>" repository has exactly 1 native deployments and 1 environments
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    Then it answers 200
    And the answer's id names "unchanged"
    And token "h" eventually reads the run "unchanged" in the state "FINISHED" through A
    And deployment events for run "unchanged" have settled
    And the "<provider>" repository has exactly 1 native deployments and 1 environments
    When the deployment repository changes its resource input to "two" as commit "changed"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{changed}"}' through A
    Then it answers 200
    And the answer's id names "proposed"
    And token "h" eventually reads the run "proposed" in the state "FINISHED" through B
    And deployment events for run "proposed" have settled
    And the "<provider>" repository has exactly 1 native deployments and 1 environments
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{changed}"}' through B
    Then it answers 200
    And the answer's id names "discarded"
    And token "h" eventually reads the run "discarded" in the state "UNCONFIRMED" through A
    And the "<provider>" deployment for run "discarded" on commit "changed" in environment "infraweave/infra" has statuses "<pending>"
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{discarded}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "discarded" in the state "DISCARDED" through B
    And deployment events for run "discarded" have settled
    And the "<provider>" deployment for run "discarded" on commit "changed" in environment "infraweave/infra" has statuses "<discarded>"
    And the "<provider>" repository has exactly 2 native deployments and 1 environments

    @terraform
    Examples:
      | provider | vendor    | pending | queued          | applying                     | success                              | discarded         |
      | GitHub   | terraform | pending | pending, queued | pending, queued, in_progress  | pending, queued, in_progress, success | pending, failure  |
      | GitLab   | terraform | running | running         | running                      | running, success                     | running, canceled |

    @tofu
    Examples:
      | provider | vendor   | pending | queued          | applying                     | success                              | discarded         |
      | GitHub   | opentofu | pending | pending, queued | pending, queued, in_progress  | pending, queued, in_progress, success | pending, failure  |
      | GitLab   | opentofu | running | running         | running                      | running, success                     | running, canceled |

  Scenario Outline: GitHub environment labels override the name or disable deployment reporting
    Given a "GitHub" deployment stack "overridden" using "<vendor>" with labels '["ghenv:prod"]'
    When token "h" sends "POST /api/v1/stacks/overridden/runs" with '{"commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "overridden"
    And token "h" eventually reads the run "overridden" in the state "UNCONFIRMED" through B
    And the "GitHub" deployment for run "overridden" on commit "seed" in environment "prod" has statuses "pending"
    When token "h" sends "POST /api/v1/stacks/overridden/runs/discard" with '{"run":"{overridden}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "overridden" in the state "DISCARDED" through B
    And deployment events for run "overridden" have settled
    And the "GitHub" deployment for run "overridden" on commit "seed" in environment "prod" has statuses "pending, failure"
    Given a "GitHub" deployment stack "disabled" using "<vendor>" with labels '["ghenv:-"]'
    When token "h" sends "POST /api/v1/stacks/disabled/runs" with '{"commitSha":"{seed}"}' through B
    Then it answers 200
    And the answer's id names "disabled"
    And token "h" eventually reads the run "disabled" in the state "UNCONFIRMED" through A
    And deployment events for run "disabled" have settled
    And the "GitHub" repository has exactly 1 native deployments and 1 environments

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |

  # ref: decisions 0011, 0012 D10/D11/DV047, 0015, 0016 and 0021
  Scenario Outline: Native individual checks follow real proposed and tracked plans and honor both options
    Given a "<provider>" check stack "infra" using "<vendor>" with mode "INDIVIDUAL" and options '{}'
    And the launcher "l" is killed
    And the check repository holds authenticated uploads
    And the check repository loses its next successful check response
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{checkCommit}"}' through A
    Then it answers 200
    And the answer's id names "checkRun"
    And the "<provider>" check "infraweave/infra" for run "checkRun" on commit "checkCommit" has states "<queued>"
    And a check report for run "checkRun" succeeded after retry
    When the launcher "l" starts again and resumes its worker
    Then the check repository has a held authenticated upload
    And the "<provider>" check "infraweave/infra" for run "checkRun" on commit "checkCommit" has states "<progress>"
    When the check repository releases authenticated uploads
    Then token "h" eventually reads the run "checkRun" in the state "FINISHED" through B
    And check events for run "checkRun" have settled
    And the "<provider>" check "infraweave/infra" for run "checkRun" on commit "checkCommit" has states "<success>"
    And the check "infraweave/infra" for run "checkRun" and stack "infra" has summary containing "1 added"
    When the launcher "l" is killed
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{checkCommit}"}' through B
    Then it answers 200
    And the answer's id names "trackedCheck"
    And the "<provider>" check "infraweave/infra/tracked" for run "trackedCheck" on commit "checkCommit" has states "<queued>"
    When the launcher "l" starts again and resumes its worker
    Then token "h" eventually reads the run "trackedCheck" in the state "UNCONFIRMED" through A
    And the "<provider>" check "infraweave/infra/tracked" for run "trackedCheck" on commit "checkCommit" has states "<progress>"
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{trackedCheck}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "trackedCheck" in the state "FINISHED" through B
    And check events for run "trackedCheck" have settled
    And the "<provider>" check "infraweave/infra/tracked" for run "trackedCheck" on commit "checkCommit" has states "<success>"
    When the check integration uses mode "INDIVIDUAL" and options '{"individualSkipUnchanged":true}'
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{checkCommit}"}' through A
    Then it answers 200
    And the answer's id names "unchangedCheck"
    And token "h" eventually reads the run "unchangedCheck" in the state "FINISHED" through B
    And check events for run "unchangedCheck" have settled
    And no native check belongs to run "unchangedCheck"
    When the check integration uses mode "INDIVIDUAL" and options '{"individualFailuresOnly":true}'
    And the check repository changes to a failing configuration as commit "checkFailure"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{checkFailure}"}' through B
    Then it answers 200
    And the answer's id names "failedCheck"
    And token "h" eventually reads the run "failedCheck" in the state "FAILED" through A
    And check events for run "failedCheck" have settled
    And the "<provider>" check "infraweave/infra" for run "failedCheck" on commit "checkFailure" has states "<failure>"

    @terraform
    Examples:
      | provider        | vendor    | queued     | progress              | success                           | failure |
      | GitHub          | terraform | queued     | queued, in_progress   | queued, in_progress, success      | failure |
      | GitLab          | terraform | pending    | pending, running      | pending, running, success         | failed  |
      | Bitbucket Cloud | terraform | INPROGRESS | INPROGRESS            | INPROGRESS, SUCCESSFUL            | FAILED  |
      | Bitbucket DC    | terraform | INPROGRESS | INPROGRESS            | INPROGRESS, SUCCESSFUL            | FAILED  |
      | Azure DevOps    | terraform | pending    | pending               | pending, succeeded                | failed  |

    @tofu
    Examples:
      | provider | vendor   | queued  | progress         | success                   | failure |
      | GitLab   | opentofu | pending | pending, running | pending, running, success | failed  |

  # ref: decisions 0012 D11/DV047, 0015 and 0016
  Scenario Outline: Aggregates summarize both real stack outcomes and conclude absent kinds without individual checks
    Given a "<provider>" check stack "healthy" using "terraform" with mode "AGGREGATED" and options '{}'
    And a "<provider>" check stack "broken" using "terraform" with mode "AGGREGATED" and options '{}'
    And the check stack "broken" fails before planning
    When token "h" sends "POST /api/v1/stacks/healthy/runs" with '{"runType":"PROPOSED","commitSha":"{checkCommit}"}' through A
    Then it answers 200
    And the answer's id names "healthyCheck"
    When token "h" sends "POST /api/v1/stacks/broken/runs" with '{"runType":"PROPOSED","commitSha":"{checkCommit}"}' through B
    Then it answers 200
    And the answer's id names "brokenCheck"
    And token "h" eventually reads the run "healthyCheck" in the state "FINISHED" through A
    And token "h" eventually reads the run "brokenCheck" in the state "FAILED" through B
    And check events for run "healthyCheck" have settled
    And check events for run "brokenCheck" have settled
    And the check repository contains a "infraweave/proposed" check in native state "<failure>" with summary containing "healthy: FINISHED"
    And the check repository contains a "infraweave/proposed" check in native state "<failure>" with summary containing "broken: FAILED"
    And the check repository contains a "infraweave/tracked" check in native state "<skipped>" with summary containing "0 runs"
    And the check repository contains a "infraweave/modules" check in native state "<skipped>" with summary containing "0 runs"
    And the check repository contains no check named "infraweave/healthy"
    And the check repository contains no check named "infraweave/broken"

    @terraform
    Examples:
      | provider        | failure | skipped       |
      | GitHub          | failure | skipped       |
      | GitLab          | failed  | skipped       |
      | Bitbucket Cloud | FAILED  | SUCCESSFUL    |
      | Bitbucket DC    | FAILED  | SUCCESSFUL    |
      | Azure DevOps    | failed  | notApplicable |

  # ref: decision 0012 D10/D11/DV047 and decision 0016
  @terraform
  Scenario: GitHub ALL exposes per-run checks and promotion actions beside shared aggregates
    Given a "GitHub" check stack "infra" using "terraform" with mode "ALL" and options '{}'
    And the launcher "l" is killed
    And the check repository holds authenticated uploads
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{checkCommit}"}' through A
    Then it answers 200
    And the answer's id names "actionCheck"
    And the GitHub check "infraweave/infra" for run "actionCheck" has actions ""
    When the check stack "infra" sets GitHub promotion to "true"
    And the launcher "l" starts again and resumes its worker
    Then the check repository has a held authenticated upload
    And the GitHub check "infraweave/infra" for run "actionCheck" has actions "preview, deploy"
    When the check repository releases authenticated uploads
    Then token "h" eventually reads the run "actionCheck" in the state "FINISHED" through B
    And check events for run "actionCheck" have settled
    And the check repository contains a "infraweave/proposed" check in native state "success" with summary containing "infra: FINISHED"
    And the GitHub check "infraweave/infra" for run "actionCheck" has actions "preview, deploy"
    When the check stack "infra" sets GitHub promotion to "false"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{checkCommit}"}' through B
    Then it answers 200
    And the answer's id names "noActions"
    And token "h" eventually reads the run "noActions" in the state "FINISHED" through A
    And check events for run "noActions" have settled
    And the GitHub check "infraweave/infra" for run "noActions" has actions ""

  # ref: decisions 0012 D9/D11/D18 and 0016
  @terraform
  Scenario: A real GitLab push posts the admitted run and failed checks for every capped stack
    Given a "GitLab" check stack "first" using "terraform" with mode "INDIVIDUAL" and options '{}'
    And a "GitLab" check stack "second" using "terraform" with mode "INDIVIDUAL" and options '{}'
    And a "GitLab" check stack "third" using "terraform" with mode "INDIVIDUAL" and options '{}'
    And token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":1}' through B
    And it answers 200
    When GitLab pushes a changed commit to the check repository
    Then the check push's tracked run is kept as "pushCheck"
    And token "h" eventually reads the run "pushCheck" in the state "UNCONFIRMED" through A
    And the check push records 2 native cap failures naming limit 1
    When token "h" sends "POST /api/v1/stacks/{checkPushStack}/runs/discard" with '{"run":"{pushCheck}"}' through B
    Then it answers 200
    And token "h" eventually reads the run "pushCheck" in the state "DISCARDED" through A
    And check events for run "pushCheck" have settled
