@postgres @vcs @docker
Feature: The worker protocol end to end
  A launcher of a private pool registers a worker, polls the worker API
  for runs, opens each run's sealed specification with the pool's private
  key, and runs it in a Docker container of infraweave-runner:acceptance,
  whose root process is this build's runner. The runner reports the run's
  first phase started and then failed with its note, since this build's
  runner executes no phase. The launcher reaches the server through the
  scenario's relay, which records each call and can hold the runners'
  reports, so a run stays on its worker while the scenario kills the
  launcher. The reap, every 30 seconds, marks a worker unseen for
  worker.offline_after OFFLINE, and deletes one unseen for run.lost_after
  and fails the run it held with the note "worker lost"; a launcher that
  starts again before then resumes its worker and its run. The home key
  is space-admin on the account's top space. Stack infra lives in legacy
  and runs on the pool, in the runner image.
  ref: decision 0008
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database, with the settings '{"worker":{"poll_timeout_max":"5s","offline_after":"8s"},"run":{"lost_after":"45s"}}'
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","runnerImage":"infraweave-runner:acceptance"},"manageState":false}' through B
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @case:success
  Scenario: A launcher claims a run, opens its specification and runs it in a container that reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, FAILED"
    And the answer's last state transition's note is "This build's runner executes no phase, so the run ends here."
    And the relay passed "GET /worker/v1/runs/{run}/spec" answered 200
    And the relay passed "POST /worker/v1/runs/{run}/reports" answered 200
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @case:failure
  Scenario: A killed launcher's worker turns OFFLINE, and the run it held is lost
    Given the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And the relay holds a report of the run "run"
    And the launcher "l" is killed
    Then token "h" eventually reads the worker "l" of the pool "pool" as "OFFLINE" through A
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "worker lost"
    And token "h" eventually reads the worker "l" of the pool "pool" as "GONE" through A

  @case:success
  Scenario: A launcher killed and started again within run.lost_after keeps its worker and its run
    Given the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And the relay holds a report of the run "run"
    And the launcher "l" is killed
    And the launcher "l" starts again and resumes its worker
    And the relay releases the runners' reports
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, FAILED"
    And the answer's last state transition's note is "This build's runner executes no phase, so the run ends here."
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
