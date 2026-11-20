@postgres @objectstore @vcs @docker @terraform @policy-trigger-worker
Feature: Failed, discarded, canceled and task terminal sources trigger real downstream workers
  A trigger is decided from a committed terminal source, including one that
  never reached a worker. Downstream execution still uses real Terraform and
  the target's independent tracked commit.
  ref: decision 0009
  ref: decision 0011

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "source" {\n  input = "source-653"\n}\n |
    And a Gitea repository "downstream" with the files:
      | main.tf | resource "terraform_data" "target" {\n  input = "target-653"\n}\n |
    And the head of "main" of "infra" is kept as "sourceSeed"
    And the head of "main" of "downstream" is kept as "targetSeed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Target","repository":"downstream","branch":"main","provider":"GIT","repositoryURL":"{downstream.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "sourceSeed" through A
    And token "h" eventually reads the stack "target" tracking the commit kept as "targetSeed" through B

    And token "h" sends "POST /api/v1/policies" with '{"name":"Every terminal source","type":"TRIGGER","space":"legacy","body":"package infraweave\ntrigger contains \"target\" if { valid_source; input.run.state in {\"FINISHED\", \"FAILED\", \"CANCELED\", \"DISCARDED\", \"STOPPED\"}; some target in input.stacks; target.id == \"target\"; target.queued_runs == 0 }\nflag contains \"source-terminal\" if { count(trigger) > 0 }\nvalid_source if { input.run.type == \"TRACKED\" }\nvalid_source if { input.run.type == \"TASK\"; input.run.command in {\"echo actual-task-653\", \"exit 7\"} }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200

  @case:success
  Scenario: An actual failed apply still triggers its downstream tracked run
    Given token "h" sends "PATCH /api/v1/stacks/infra" with '{"beforeApply":["exit 17"]}' through A
    And it answers 200
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    And token "h" eventually reads the run "source" in the state "FAILED" through B
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through A
    And token "h" eventually reads the run "child" in the state "FINISHED" through B
    And the answer's commit hash is "{targetSeed}"
    And the answer's triggeredBy is '"policy/infra/{source}"'
    And the answer's flags is '[]'
    When token "h" sends "GET /api/v1/runs/{source}" through A
    Then it answers 200
    And the answer's flags is '["source-terminal"]'
    And the answer's TRIGGER receipt outcomes are "TRIGGER"

  @case:success
  Scenario: Discarding an actual unconfirmed plan triggers the downstream run
    Given token "h" sends "PATCH /api/v1/stacks/infra" with '{"autodeploy":false}' through A
    And it answers 200
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    And token "h" eventually reads the run "source" in the state "UNCONFIRMED" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{source}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "source" in the state "DISCARDED" through B
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through A
    And token "h" eventually reads the run "child" in the state "FINISHED" through B
    And the answer's commit hash is "{targetSeed}"
    And the answer's triggeredBy is '"policy/infra/{source}"'
    And the answer's flags is '[]'
    When token "h" sends "GET /api/v1/runs/{source}" through A
    Then it answers 200
    And the answer's flags is '["source-terminal"]'
    And the answer's TRIGGER receipt outcomes are "TRIGGER"

  @case:success
  Scenario: A source canceled before worker claim still commits its downstream job
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads the run "source" in the state "CANCELED" through A
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through B
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" eventually reads the run "child" in the state "FINISHED" through A
    And the answer's commit hash is "{targetSeed}"
    And the answer's flags is '[]'
    When token "h" sends "GET /api/v1/runs/{source}" through B
    Then it answers 200
    And the answer's flags is '["source-terminal"]'
    And the answer's TRIGGER receipt outcomes are "TRIGGER"

  @case:success
  Scenario Outline: A real task ending <state> triggers a tracked downstream deployment
    Given token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"<command>","skipInitialization":true}' through A
    Then it answers 201
    And the answer's id names "source"
    And token "h" eventually reads the run "source" in the state "<state>" through B
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through A
    And token "h" eventually reads the run "child" in the state "FINISHED" through B
    And the answer's commit hash is "{targetSeed}"
    And the answer's triggeredBy is '"policy/infra/{source}"'
    And the answer's flags is '[]'
    When token "h" sends "GET /api/v1/runs/{source}" through A
    Then it answers 200
    And the answer's flags is '["source-terminal"]'
    And the answer's TRIGGER receipt outcomes are "TRIGGER"

    Examples:
      | command              | state    |
      | echo actual-task-653 | FINISHED |
      | exit 7               | FAILED   |

  @case:success
  Scenario: Stopping an actual planning worker still triggers downstream execution
    Given token "h" sends "PATCH /api/v1/stacks/infra" with '{"beforePlan":["echo waiting-for-stop-653; sleep 60"]}' through A
    And it answers 200
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    And token "h" eventually reads the run "source" in the state "PLANNING" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/stop" with '{"note":"Trigger after worker stop","state":"PLANNING"}' through A
    Then it answers 200
    And token "h" eventually reads the run "source" in the state "STOPPED" through B
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through A
    And token "h" eventually reads the run "child" in the state "FINISHED" through B
    And the answer's commit hash is "{targetSeed}"
    And the answer's flags is '[]'
    When token "h" sends "GET /api/v1/runs/{source}" through A
    Then it answers 200
    And the answer's flags is '["source-terminal"]'
    And the answer's TRIGGER receipt outcomes are "TRIGGER"
