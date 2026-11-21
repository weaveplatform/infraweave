@postgres @objectstore @vcs @docker @terraform
Feature: Every run type, action and timer through the run lifecycle
  Each run type takes its path through the lifecycle end to end, on a real
  launcher of a private pool that runs each run in a container of
  {runner.terraform} with the newest Terraform the
  version catalogue lists. Each scenario reads the run's history and the
  run_updated events the outbox holds of it, which follow the path the
  run's type takes, or the choice made where the reference leaves a gap
  open until its capture is recorded: a proposed run plans and finishes;
  a failed run retries on its commit with its recorded hooks (C10), while
  a finished run cannot retry; a tracked run
  waits in UNCONFIRMED until confirmed, and on an autodeploy stack applies
  straight from PLANNING; a task performs in PERFORMING, and one that
  skips initialization still passes through INITIALIZING, running no init
  command or hook and logging the skip (C42); a destroy run on a deleted
  stack goes from INITIALIZING straight to DESTROYING (C34); a targeted
  replan loops back to UNCONFIRMED, and one whose handover stalls in
  PREPARING_REPLAN fails on the preparing timeout (C44); a stop ends the
  run STOPPED, and a kill FAILED with the
  note "killed" (C36); and a launcher's no-log timeout fails the run with
  the note "no log output" (C39). A killed launcher's worker turns OFFLINE
  and keeps its run, which its restart resumes, and a worker unseen for
  run.lost_after is deleted and its run fails as lost. The timers end the
  runs nobody moves, a task queued behind its stack's lock included, each
  with a note naming its rule and the rule on its run_updated event, and
  leave a run short of its boundary as it is. Time passes for the timers
  and the reap on the run or the worker, whose timestamps the scenario
  moves back by the span; the reap and run.timers then fall due, so no
  scenario waits for a boundary. The release indexes serve the recorded
  fixtures; the home key is space-admin on the account's top space. Stacks
  live in legacy, keep their state in infraweave and run on the pool; the
  repository's configuration has a terraform_data resource and an output
  alone, so no provider is downloaded.
  ref: decision 0008
  ref: decision 0016
  ref: report 08

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n\noutput "name" {\n  value = terraform_data.this.output\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTrigger @op:runRetry @case:success
  Scenario: A failed proposed run retries on its commit with its recorded hooks
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}},"beforePlan":["echo hook: old","exit 1"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the run "run" wrote 0 state versions
    And the answer's runtime configuration includes '{"beforePlan":["echo hook: old","exit 1"],"terraformVersion":"{terraform.newest}"}'
    Given token "h" sends "PATCH /api/v1/stacks/infra" with '{"beforePlan":["echo hook: new"]}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/retry" through B
    Then it answers 200
    And the answer's type is '"PROPOSED"'
    And the answer's commits is '["{seed}"]'
    And the answer's last state transition's note is "retry of {run}"
    And the answer's runtime configuration includes '{"beforePlan":["echo hook: old","exit 1"],"terraformVersion":"{terraform.newest}"}'
    And the answer's id names "retry"
    And token "h" eventually reads the run "retry" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the outbox holds the run "retry"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And token "h" reads the run "retry"'s PLANNING log through B, which holds "hook: old" and not "hook: new"
    And token "h" records the run "retry"'s retry planning evidence through B

  @op:runTrigger @op:runRetry @case:failure
  Scenario: A proposed run that finishes cannot be retried
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's canRetry is 'false'
    And the answer's retryBlocker is '"A retry needs a run that ended other than in FINISHED."'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/retry" through A
    Then it answers a 409 problem "invalid-state"

  @op:runTrigger @op:runConfirm @case:success
  Scenario: A tracked run waits in UNCONFIRMED, and applies once confirmed
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And the run "run" wrote 1 state version

  @op:runTrigger @case:success
  Scenario: A tracked run on an autodeploy stack applies straight from PLANNING
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And the run "run" wrote 1 state version

  @op:taskCreate @case:success
  Scenario: A task performs in PERFORMING, and one that skips initialization logs the skip and runs no init hook or command
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}},"beforeInit":["echo hook: before init"],"afterInit":["echo hook: after init"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo \"name: $(terraform output -raw name)\""}' through A
    Then it answers 201
    And the answer's id names "task"
    And token "h" eventually reads the run "task" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FINISHED"
    And the outbox holds the run "task"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FINISHED"
    And token "h" reads the run "task"'s PERFORMING log through A, which holds "name: one" and not "Error"
    And token "h" reads the run "task"'s INITIALIZING log through A, which holds "hook: before init" and not "Error"
    And token "h" reads the run "task"'s INITIALIZING log through B, which holds "hook: after init" and not "Error"
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo skipped","skipInitialization":true}' through B
    Then it answers 201
    And the answer's id names "skipped"
    And token "h" eventually reads the run "skipped" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FINISHED"
    And the outbox holds the run "skipped"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FINISHED"
    And token "h" reads the run "skipped"'s INITIALIZING log through B, which holds "infraweave: initialization is skipped" and not "Initializing the backend"
    And token "h" reads the run "skipped"'s INITIALIZING log through A, which holds "infraweave: initialization is skipped" and not "hook: before init"
    And token "h" reads the run "skipped"'s INITIALIZING log through B, which holds "infraweave: initialization is skipped" and not "hook: after init"
    And the run "skipped" wrote 0 state versions

  @op:stackDelete @case:success
  Scenario: A stack deleted with its resources destroys them in a run that goes from INITIALIZING straight to DESTROYING
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    When token "h" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's first run names "destroy"
    And token "h" eventually finds no stack "infra" through A
    And the outbox holds the run "destroy"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, DESTROYING, FINISHED"

  @op:runTargetedReplan @op:runConfirm @case:success
  Scenario: A targeted replan loops back to UNCONFIRMED, and its confirmation applies
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through A
    Then it answers 200
    And the answer's state is '"REPLAN_REQUESTED"'
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"

  @op:runTargetedReplan @case:failure
  Scenario: A replan whose handover stalls in PREPARING_REPLAN fails on the preparing timeout
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through A
    Then it answers 200
    And the relay holds a report of the run "run"
    And token "h" eventually reads the run "run" in the state "PREPARING_REPLAN" through B
    When 15 minutes pass for the run "run"
    And the "worker.reap" periodic task falls due
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, FAILED"
    And the answer's last state transition's note is "preparing timeout"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, FAILED"

  @op:runStop @case:success
  Scenario: A stop ends a plan a hook holds, and the run becomes STOPPED
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}},"beforePlan":["echo holding the plan","sleep 600"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "PLANNING" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/stop" with '{"note":"Stopping run"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "STOPPED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, STOPPED"
    And the answer's last state transition's note is "Stopping run"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, STOPPED"

  @op:runKill @case:success
  Scenario: A kill ends a plan whose hook ignores the stop, and the run fails with the note "killed"
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}},"beforePlan":["trap \"\" INT","echo ignoring SIGINT","while :; do sleep 1; done"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "PLANNING" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/stop" with '{}' through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/kill" with '{"state":"PLANNING"}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the answer's last state transition's note is "killed"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"

  @case:failure
  Scenario: A run whose container goes quiet for the launcher's no-log timeout fails with the note "no log output"
    Given a certificate signing request for a P-256 key is kept as "quietcsr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Quiet","space":"legacy","certificateSigningRequest":"{quietcsr}"}' through A
    And the answer's id names "quiet"
    And token "h" starts the launcher "q" of the pool "quiet" with the key of "quietcsr", whose no-log timeout is 20s, through A
    And token "h" eventually reads the worker "q" of the pool "quiet" as "IDLE" through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{quiet}","vendorConfig":{"terraform":{}},"beforePlan":["echo going quiet","sleep 600"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the answer's last state transition's note is "no log output"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"

  @case:success
  Scenario: A killed launcher's worker turns OFFLINE and keeps its run, which the launcher resumes once started again
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And the relay holds a report of the run "run"
    And the launcher "l" is killed
    And 3 minutes pass for the worker "l"
    And the "worker.reap" periodic task falls due
    Then token "h" eventually reads the worker "l" of the pool "pool" as "OFFLINE" through A
    And token "h" eventually reads the run "run" in the state "PREPARING" through B
    When the launcher "l" starts again and resumes its worker
    And the relay releases the runners' reports
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"

  @case:failure
  Scenario: A worker unseen for run.lost_after is deleted, and the run it held fails as lost
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And the relay holds a report of the run "run"
    And the launcher "l" is killed
    And 1 hour passes for the worker "l"
    And the "worker.reap" periodic task falls due
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "worker lost"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, FAILED"
    And token "h" eventually reads the worker "l" of the pool "pool" as "GONE" through B

  @case:success
  Scenario: The timers end the runs nobody moves, each by its rule, and leave a run short of its boundary
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Second","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Third","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "unconfirmed"
    And token "h" eventually reads the run "unconfirmed" in the state "UNCONFIRMED" through B
    And token "h" sends "POST /api/v1/stacks/second/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "confirmed"
    And token "h" eventually reads the run "confirmed" in the state "UNCONFIRMED" through B
    And the launcher "l" is killed
    And token "h" sends "POST /api/v1/stacks/second/runs/confirm" with '{"run":"{confirmed}"}' through A
    And token "h" eventually reads the run "confirmed" in the state "CONFIRMED" through B
    And token "h" sends "POST /api/v1/stacks/third/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "ready"
    And token "h" eventually reads the run "ready" in the state "READY" through B
    And token "h" eventually reads the stack "third" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/stacks/third/tasks" with '{"command":"echo queued"}' through B
    And the answer's id names "queued"
    And token "h" eventually reads the run "queued" in the state "QUEUED" through A
    And token "h" sends "POST /api/v1/stacks/third/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through B
    And the answer's id names "proposed"
    And token "h" eventually reads the run "proposed" in the state "READY" through A
    And token "h" sends "POST /api/v1/stacks/second/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "young"
    And token "h" eventually reads the run "young" in the state "READY" through B
    When 30 days pass for the run "queued"
    And the "run.timers" periodic task falls due
    Then token "h" eventually reads the run "queued" in the state "CANCELED" through A
    And the answer's history is the states "QUEUED, CANCELED"
    And the answer's last state transition's note is "not ended within 30 days of its creation"
    And the outbox's run_updated event of the run "queued" in the state "CANCELED" names the timer "max_age"
    When 7 days pass for the run "ready"
    And 7 days pass for the run "unconfirmed"
    And 7 days pass for the run "confirmed"
    And 2 days pass for the run "proposed"
    And the "run.timers" periodic task falls due
    Then token "h" eventually reads the run "ready" in the state "CANCELED" through B
    And the answer's history is the states "QUEUED, READY, CANCELED"
    And the answer's last state transition's note is "not picked up within 7 days"
    And the outbox's run_updated event of the run "ready" in the state "CANCELED" names the timer "pickup_blocking"
    And token "h" eventually reads the run "unconfirmed" in the state "DISCARDED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, DISCARDED"
    And the answer's last state transition's note is "unconfirmed for 7 days"
    And the outbox's run_updated event of the run "unconfirmed" in the state "DISCARDED" names the timer "unconfirmed"
    And token "h" eventually reads the run "confirmed" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, FAILED"
    And the answer's last state transition's note is "not applied within 7 days"
    And the outbox's run_updated event of the run "confirmed" in the state "FAILED" names the timer "confirmed"
    And token "h" eventually reads the run "proposed" in the state "CANCELED" through A
    And the answer's history is the states "QUEUED, READY, CANCELED"
    And the answer's last state transition's note is "not picked up within 2 days"
    And the outbox's run_updated event of the run "proposed" in the state "CANCELED" names the timer "pickup_proposed"
    And token "h" eventually reads the run "young" in the state "READY" through B
    And the outbox holds the run "young"'s run_updated events of the states "QUEUED, READY"
