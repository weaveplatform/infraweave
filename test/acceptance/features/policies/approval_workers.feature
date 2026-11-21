@postgres @objectstore @vcs @docker @policy-approval-worker
Feature: Approval policies gate real Terraform family workers
  Each state requires its own review. The actual worker uploads structural
  changes and reports its plan, then pauses while the server evaluates approval
  policies. Approving a plan permits the separate confirmation action; rejecting
  it returns the captured failure transition and releases the stack lock.
  ref: decision 0008
  ref: decision 0009

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "approval-policy"\n} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runReview @op:runConfirm @case:success
  Scenario Outline: Queued and prior plan reviews cannot approve a later state version or replace confirmation
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Each state review","type":"APPROVAL","space":"legacy","body":"package infraweave\napprove if { input.run.state == \"QUEUED\"; count(input.reviews.current.approvals) == 1 }\napprove if { input.run.state == \"UNCONFIRMED\"; count(input.reviews.current.approvals) == 1; count(input.reviews.older.approvals) >= 1; count(input.run.changes) > 0 }\nflag contains \"queued-reviewed\" if { approve; input.run.state == \"QUEUED\" }\nflag contains \"unconfirmed-reviewed\" if { approve; input.run.state == \"UNCONFIRMED\" }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","stateVersion":1}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the answer's needsApproval is 'true'
    And the answer's canConfirm is 'false'
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","stateVersion":1}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","stateVersion":6}' through B
    Then it answers 200
    And the answer's state is '"UNCONFIRMED"'
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then it answers 200
    And the answer's state is '"UNCONFIRMED"'
    And the answer's needsApproval is 'false'
    And the answer's canConfirm is 'true'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the answer's needsApproval is 'true'
    And the answer's canConfirm is 'false'
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","stateVersion":6}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","stateVersion":10}' through A
    Then it answers 200
    And the answer's stateVersion is '10'
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And the answer's flags is '["queued-reviewed","unconfirmed-reviewed"]'

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |

  @terraform @policy-task @op:runReview @op:taskCreate @case:success
  Scenario: A task needs its own review before an actual worker performs its command
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Review task command","type":"APPROVAL","space":"legacy","body":"package infraweave\napprove if { input.run.type == \"TASK\"; input.run.command == \"terraform version\"; count(input.reviews.current.approvals) == 1 }\nflag contains \"task-reviewed\" if approve\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Inspect before task creation","type":"TASK","space":"legacy","body":"package infraweave\ndeny contains \"Unexpected task command\" if { input.request.command != \"terraform version\" }\n"}' through B
    And it answers 201
    And the answer's id names "taskguard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{taskguard}" through A
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform version","skipInitialization":true}' through A
    Then it answers 201
    And the TASK answer carries its deprecation notice
    And the answer's id names "run"
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer's state is '"QUEUED"'
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","stateVersion":1}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's flags is '["task-reviewed"]'
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FINISHED"
    And token "h" reads the run "run"'s PERFORMING log through A, which holds "Terraform v{terraform.newest}" and not "Error"

  @op:runReview @case:failure
  Scenario Outline: A real plan report commits approval rejection with the captured failure note
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Reject reported plan","type":"APPROVAL","space":"legacy","body":"package infraweave\napprove if { input.run.state == \"QUEUED\"; count(input.reviews.current.approvals) == 1 }\nreject if { input.run.state == \"UNCONFIRMED\" }\nflag contains \"report-rejected\" if reject\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","stateVersion":1}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, FAILED"
    And the answer's last state transition's note is "Run rejected by approval policies"
    And the answer's flags is '["report-rejected"]'
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |
