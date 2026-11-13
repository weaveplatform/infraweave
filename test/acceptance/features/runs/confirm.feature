@postgres @vcs
Feature: A tracked run waiting for confirmation is confirmed or discarded
  A tracked run whose plan changes something, on a stack that does not
  deploy automatically, waits in UNCONFIRMED holding its stack's run lock.
  A caller confirms it, after which the next poll of its pool's worker is
  assigned it to apply, or discards it, which ends it and releases the
  lock. Each names the run, or by default acts on the run that holds the
  stack's lock. Here the scenario is the worker: it registers with the
  pool's key, claims the run, opens its specification for the in-run API
  token, and reports its phases through the worker API. The in-run API
  token authenticates only while its run plans, applies or performs, and
  takes no RUN_ action. The release indexes serve the recorded fixtures,
  so the run's specification names a Terraform release. The home key is
  space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it. Stack infra lives in legacy and runs on
  the pool.
  ref: decision 0008
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And the member key is exchanged through A for token "m"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":false}' through B
    And the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    And the specification's sealed part opens with the key of "csr" to an in-run API token, kept as "it"
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through B
    And it answers 200
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through A
    And it answers 200
    And the answer's action is '"execute"'
    And the answer's state is '"PLANNING"'

  @op:runConfirm @op:run @case:success
  Scenario: Confirming the run that holds the lock assigns it to a worker to apply
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then it answers 200
    And the answer's action is '"pause"'
    And the answer's state is '"UNCONFIRMED"'
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then the answer's allowedActions is '["RUN_CONFIRM","RUN_DISCARD","RUN_TARGETED_REPLAN","RUN_PRIORITIZE_SET"]'
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"note":"Looks good"}' through B
    Then it answers 200
    And the answer's id is '"{run}"'
    And the answer's state is '"CONFIRMED"'
    And the answer's allowedActions is '["RUN_PRIORITIZE_SET"]'
    And the answer's last state transition's note is "Looks good"
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    Then the answer assigns the run "run", its run token kept as "apply"
    And token "h" eventually reads the run "run" in the state "PREPARING_APPLY" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY"

  @op:runDiscard @op:runTrigger @case:success
  Scenario: Discarding the run named ends it and releases the stack's lock
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":0,"change":1,"delete":0,"import":0,"outputs":false}}' through A
    Then the answer's state is '"UNCONFIRMED"'
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{run}","note":"Not this one"}' through B
    Then it answers 200
    And the answer's state is '"DISCARDED"'
    And the answer's allowedActions is '[]'
    And the answer's last state transition's note is "Not this one"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "next"
    Then token "h" eventually reads the run "next" in the state "READY" through B

  @op:runConfirm @op:runDiscard @case:denial
  Scenario: A space reader neither confirms nor discards the run
    Given the member key is space-reader on the space "legacy"
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":0,"change":0,"delete":1,"import":0,"outputs":false}}' through A
    Then the answer's state is '"UNCONFIRMED"'
    When token "m" sends "POST /api/v1/stacks/infra/runs/confirm" with '{}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_CONFIRM"'
    When token "m" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{run}"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_DISCARD"'
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then the answer's state is '"UNCONFIRMED"'

  @op:runConfirm @op:runDiscard @op:stack @case:denial
  Scenario: The run's own in-run API token reads its stack while the run plans, confirms and discards nothing, and ends when the run waits
    When token "it" sends "GET /api/v1/stacks/infra" through A
    Then it answers 200
    When token "it" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_CONFIRM"'
    When token "it" sends "POST /api/v1/stacks/infra/runs/discard" with '{}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_DISCARD"'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":2,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then the answer's state is '"UNCONFIRMED"'
    When token "it" sends "GET /api/v1/stacks/infra" through A
    Then it answers a 401 problem "unauthenticated"

  @op:runConfirm @op:runDiscard @case:failure
  Scenario: A run that does not wait for confirmation, a run of another stack, an unknown run and a stack with no blocking run are refused
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}"},"manageState":false}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{run}"}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/other/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"01JBQ6Q4V2ZK5Y3W8N7M6P5R4T"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/other/runs/discard" with '{}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}","approve":true}' through B
    Then it answers a 400 problem "invalid-request"
