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
