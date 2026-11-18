@postgres @objectstore @github @gitlab @docker
Feature: Tracked runs report native deployments
  Real launchers plan and apply the pinned Terraform and OpenTofu releases.
  The event sink and durable VCS jobs report historical run states through
  the providers' native APIs. Proposed and no-change runs create no deployment.
  ref: decision 0012
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
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

  @case:plan-comments
  Scenario Outline: Labelled plans recover one masked comment per run and follow current shared heads
    Given a "<provider>" plan-comment stack "infra" using "<vendor>" with labels '["feature:add_plan_pr_comment"]'
    And the comment fixture sends shared, moved and closed requests through A
    And the comment API loses its next successful create response
    When a real "PROPOSED" plan of comment stack "infra" is run as "first" with "masked" output through A
    Then token "h" eventually reads the run "first" in the state "FINISHED" through B
    And comment events for run "first" have settled
    And a comment report for run "first" recovered after a lost create
    And run "first" has one masked native plan comment on each request "12, 13"
    And run "first" has a native plan comment result "FINISHED" on request 12
    And comment requests "14, 15" have no native comments
    When comment request 12 moves to another real commit through B
    And a real "TRACKED" plan of comment stack "infra" is run as "second" with "plain" output through B
    Then token "h" eventually reads the run "second" in the state "UNCONFIRMED" through A
    And comment events for run "second" have settled
    And run "second" has a native plan comment result "UNCONFIRMED" on request 13
    And run "second" has no native comment on request 12
    And comment request 13 has 2 comments and its previous plan is "<previous>"
    And comment requests "14, 15" have no native comments
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{second}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "second" in the state "DISCARDED" through B
    And comment events for run "second" have settled
    Given a "<provider>" plan-comment stack "silent" using "<vendor>" with labels '[]'
    When a real "PROPOSED" plan of comment stack "silent" is run as "silent" with "plain" output through A
    Then token "h" eventually reads the run "silent" in the state "FINISHED" through B
    And comment events for run "silent" have settled
    And unlabelled run "silent" has real stored planning output and no native comment
    And comment request 13 has 2 comments and its previous plan is "<previous>"

    @terraform
    Examples:
      | provider | vendor    | previous  |
      | GitHub   | terraform | outdated  |
      | GitLab   | terraform | unchanged |

    @tofu
    Examples:
      | provider | vendor   | previous  |
      | GitHub   | opentofu | outdated  |
      | GitLab   | opentofu | unchanged |

  @case:plan-comments
  Scenario Outline: An oversized stored planning log keeps its exact tail within the native comment budget
    Given a "<provider>" plan-comment stack "infra" using "<vendor>" with labels '["feature:add_plan_pr_comment"]'
    And the comment fixture opens request 12 at the current head through A
    When a real "PROPOSED" plan of comment stack "infra" is run as "large" with "oversized" output through B
    Then token "h" eventually reads the run "large" in the state "FINISHED" through A
    And comment events for run "large" have settled
    And run "large" has a native plan comment result "FINISHED" on request 12
    And run "large" has an oversized native comment tail on request 12

    @terraform
    Examples:
      | provider | vendor    |
      | GitHub   | terraform |
      | GitLab   | terraform |

    @tofu
    Examples:
      | provider | vendor   |
      | GitHub   | opentofu |
      | GitLab   | opentofu |
