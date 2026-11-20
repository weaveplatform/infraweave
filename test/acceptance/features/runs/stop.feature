@postgres @objectstore @vcs @docker @terraform
Feature: Stopping and killing a run through its worker's poll
  A caller stops a run that initializes, or a proposed or tracked run that
  plans. The server records the stop and wakes the pool's held polls; the
  busy worker's poll answers it once; the launcher sends the runner
  SIGINT, which the runner passes to the running command's process group;
  and once the command ends the runner reports stopped, so the run becomes
  STOPPED with the stop's note. A command that ignores SIGINT keeps the
  run planning until the caller kills it: the launcher kills the
  container, and the run fails with the note "killed", which releases its
  stack's lock. Stopping or killing the run that holds its stack's lock
  needs the blocking variant as well. A run whose state takes neither, and
  a state the run has left, answer 409 with the run's state. A real
  launcher of a private pool runs each run in a container of
  {runner.terraform} with the newest Terraform the
  version catalogue lists. The release indexes serve the recorded
  fixtures; the home key is space-admin on the account's top space, and
  the member key holds only the bindings a scenario gives it. Stacks live
  in legacy; the repository's configuration has a terraform_data resource
  alone, so no provider is downloaded.
  ref: decision 0008
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runStop @case:success
  Scenario: A stop ends a plan a hook holds, once, and the run becomes STOPPED with the stop's note
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}},"beforePlan":["echo holding the plan","sleep 600"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "PLANNING" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/stop" with '{"note":"Stopping run","state":"PLANNING"}' through A
    Then it answers 200
    And the answer's allowedActions is '["RUN_KILL_BLOCKING","RUN_PRIORITIZE_SET","RUN_COMMENT"]'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/stop" with '{"note":"Again"}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "STOPPED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, STOPPED"
    And the answer's last state transition's note is "Stopping run"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "infraweave: the run stopped on request" and not "Terraform will perform the following actions"
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then it answers 200
    And the answer leaves out "blocker"

  @op:runStop @op:runKill @case:success
  Scenario: A kill ends a plan whose hook ignores the stop, and the run fails with the note "killed"
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}},"beforePlan":["trap \"\" INT","echo ignoring SIGINT","while :; do sleep 1; done"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "PLANNING" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/stop" with '{}' through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/kill" with '{"state":"PLANNING"}' through B
    Then it answers 200
    And the answer's allowedActions is '["RUN_PRIORITIZE_SET","RUN_COMMENT"]'
    And token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the answer's last state transition's note is "killed"
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer leaves out "blocker"

  @op:runStop @op:runKill @case:denial
  Scenario: A space reader stops and kills no run that holds its stack's lock
    Given the member key is space-reader on the space "legacy"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}},"beforePlan":["trap \"\" INT","while :; do sleep 1; done"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "PLANNING" through A
    When token "m" sends "POST /api/v1/stacks/infra/runs/{run}/stop" with '{}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_STOP_BLOCKING"'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/stop" with '{}' through B
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/runs/{run}/kill" with '{}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_KILL_BLOCKING"'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/kill" with '{}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FAILED" through A

  @op:runStop @op:runKill @case:failure
  Scenario: A run whose state takes neither, a state the run has left and an unknown run are refused
    The stack has no private pool, so its run waits in READY for a worker.
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Parked","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/parked/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    When token "h" sends "POST /api/v1/stacks/parked/runs/{run}/stop" with '{}' through A
    Then it answers a 409 problem "invalid-state"
    And the answer's detail is '"The run is READY; a stop needs a run that initializes, or a proposed or tracked run that plans."'
    When token "h" sends "POST /api/v1/stacks/parked/runs/{run}/stop" with '{"state":"PLANNING"}' through B
    Then it answers a 409 problem "invalid-state"
    And the answer's detail is '"The run is READY; the request names PLANNING."'
    When token "h" sends "POST /api/v1/stacks/parked/runs/{run}/kill" with '{}' through A
    Then it answers a 409 problem "invalid-state"
    And the answer's detail is '"The run is READY; a kill needs a run on a worker that a stop was asked of."'
    When token "h" sends "POST /api/v1/stacks/parked/runs/01JBQ6Q4V2ZK5Y3W8N7M6P5R4T/stop" with '{}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/parked/runs/{run}/kill" with '{"note":3}' through A
    Then it answers a 400 problem "invalid-request"
