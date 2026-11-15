@postgres @vcs
Feature: A tracked run waiting for confirmation is replanned against chosen addresses
  A caller replans a tracked run that waits for confirmation against
  resource or module addresses: the run moves to REPLAN_REQUESTED, still
  holding its stack's run lock, and the next poll of its pool's worker is
  assigned it in PREPARING_REPLAN, its specification naming the targets.
  The worker restores the run's saved workspace and plans again, and the
  plan decision leaves the run waiting for confirmation again. A restore
  that fails fails the run in PREPARING_REPLAN. Here the scenario is the
  worker: it registers with the pool's key, claims the run, opens its
  specification for the in-run API token, and reports its phases through
  the worker API. A space reader is refused RUN_TARGETED_REPLAN, as is the
  run's in-run API token, and a tier without the targeted-replan feature
  refuses every replan. A run in another state, no address, an address
  that is not one and a run that does not exist are refused. The release
  indexes serve the recorded fixtures, so the run's specification names a
  Terraform release. The home key is space-admin on the account's top
  space; the member key holds only the bindings a scenario gives it. Stack
  infra lives in legacy and runs on the pool.
  ref: decision 0008
  ref: decision 0016
  ref: decision 0021

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database, with an operator key
    And the operator key is exchanged through A for token "op"
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
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through A
    And the answer's state is '"PLANNING"'

  @op:runTargetedReplan @case:success
  Scenario: A replanned run is claimed with its targets, plans again and waits for confirmation again
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":2,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then the answer's state is '"UNCONFIRMED"'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this","module.app[\"a\"].aws_instance.web[0]"]}' through A
    Then it answers 200
    And the answer drew on the expensive budget
    And the answer's state is '"REPLAN_REQUESTED"'
    And the answer's allowedActions is '["RUN_PRIORITIZE_SET","RUN_COMMENT"]'
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "re"
    When token "re" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers 200
    And the answer's targets is '["terraform_data.this","module.app[\"a\"].aws_instance.web[0]"]'
    When token "re" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"PLANNING","stateVersion":8}' through B
    Then it answers 200
    And the answer's state is '"PLANNING"'
    When token "re" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":9,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through A
    Then it answers 200
    And the answer's action is '"pause"'
    And the answer's state is '"UNCONFIRMED"'
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, PLANNING, UNCONFIRMED"
    And the answer's delta is '{"addCount":1,"changeCount":0,"deleteCount":0,"importCount":0,"moveCount":0,"forgetCount":0,"resources":0,"okCount":0,"skipCount":0,"rescueCount":0,"ignoreCount":0,"unreachableCount":0,"failedCount":0}'

  @op:runTargetedReplan @case:denial
  Scenario: A space reader and the run's in-run API token are refused, and a tier without targeted-replan refuses every replan
    Given the member key is space-reader on the space "legacy"
    When token "it" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_TARGETED_REPLAN"'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then the answer's state is '"UNCONFIRMED"'
    When token "m" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_TARGETED_REPLAN"'
    When token "op" creates the tier "Gold" with the feature "stacks" through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/tier" through B with token "h" eventually answers a 403 problem "feature-not-in-tier"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through B
    Then it answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "targeted-replan"

  @op:runTargetedReplan @case:failure
  Scenario: A run that does not wait, no address, an address that is not one and an unknown run are refused, and a restore that fails fails the run
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":[]}' through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this","terraform_data this"]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/targets/1" as "rule"
    When token "h" sends "POST /api/v1/stacks/infra/runs/01JBQ6Q4V2ZK5Y3W8N7M6P5R4T/targeted-replan" with '{"targets":["terraform_data.this"]}' through B
    Then it answers a 404 problem "not-found"
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then the answer's state is '"UNCONFIRMED"'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through A
    Then it answers 200
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "re"
    When token "re" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"failed","state":"PREPARING_REPLAN","stateVersion":8,"details":"The run saved no workspace to apply or replan from."}' through A
    Then it answers 200
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then the answer's state is '"FAILED"'
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, FAILED"
    And the answer's last state transition's note is "The run saved no workspace to apply or replan from."
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "next"
    Then token "h" eventually reads the run "next" in the state "READY" through B
