@postgres @objectstore @vcs @policy-plan-worker
Feature: Planning report artifact integrity and committed decision replay
  These protocol tests act as an authenticated worker rather than running a
  Terraform container. Real servers, OPA, PostgreSQL and object storage verify
  that omitting an artifact cannot bypass policy and a lost response can be
  retried without a second decision. Native tool documents are covered by
  plan_workers.feature.
  ref: decision 0008
  ref: decision 0009

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
    And the specification's sealed part opens with the key of "csr" to the run's artifact key, kept as "ak"
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through B
    And it answers 200
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through A
    And it answers 200
    And the answer's action is '"execute"'
    And the answer's state is '"PLANNING"'

    And token "h" sends "POST /api/v1/policies" with '{"name":"Require a real input","type":"PLAN","space":"legacy","body":"package infraweave\nflag contains \"protocol-evaluated\" if true\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200

  @case:failure
  Scenario: A missing current plan fails closed and its exact report can recover the committed answer
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through A
    Then it answers 200
    And the answer's action is '"exit"'
    And the answer's state is '"FAILED"'
    And token "h" sends "GET /api/v1/runs/{run}" through B
    And it answers 200
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the answer's last state transition's note is "denied by plan policy"
    And the answer's flags is '[]'
    And the answer's plan policy outcomes are '[{"deny":["Policy input facts are unavailable or invalid."],"warn":[]}]'
    And the answer's PLAN receipt outcomes are "DENY"
    And the answer contains a PLAN error receipt "INPUT"
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then it answers 200
    And the answer's action is '"exit"'
    And the answer's state is '"FAILED"'
    And the answer's stateVersion is '6'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":2,"change":0,"delete":0,"import":0,"outputs":false}}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the answer's plan policy outcomes are '[{"deny":["Policy input facts are unavailable or invalid."],"warn":[]}]'
    And the answer's PLAN receipt outcomes are "DENY"

  @case:failure
  Scenario Outline: A <fault> artifact cannot make the authenticated worker bypass PLAN evaluation
    Given token "rt" records the protocol plan "<fault>" with artifact key "ak" for run "run" in state version 5 through A
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then it answers 200
    And the answer's action is '"exit"'
    And the answer's state is '"FAILED"'
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then it answers 200
    And the answer's last state transition's note is "denied by plan policy"
    And the answer's flags is '[]'
    And the answer's PLAN receipt outcomes are "DENY"
    And the answer contains a PLAN error receipt "INPUT"
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through A
    Then it answers 200
    And the answer's action is '"exit"'
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer's PLAN receipt outcomes are "DENY"
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"

    Examples:
      | fault           |
      | malformed       |
      | unreadable      |
      | missing-changes |
      | invalid-version |

  @case:success
  Scenario: A replan cannot reuse its earlier recorded input or receipt
    Given token "rt" records the protocol plan "valid" with artifact key "ak" for run "run" in state version 5 through A
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then it answers 200
    And the answer's action is '"pause"'
    And the answer's state is '"UNCONFIRMED"'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through A
    Then it answers 200
    And the answer's action is '"pause"'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":2,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through A
    Then it answers 200
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "re"
    When token "re" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"PLANNING","stateVersion":8}' through A
    Then it answers 200
    And the answer's state is '"PLANNING"'
    When token "re" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":9,"delta":{"add":2,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then it answers 200
    And the answer's action is '"exit"'
    And the answer's state is '"FAILED"'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer's PLAN receipt outcomes are "ALLOW, DENY"
    And the run "run" committed PLAN receipts at state versions "6, 10"
    And the answer's flags is '["protocol-evaluated"]'
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, PLANNING, FAILED"

  @case:success
  Scenario: A locked stack preserves confirmation even when PLAN allows autodeploy
    Given token "h" sends "PATCH /api/v1/stacks/infra" with '{"autodeploy":true}' through A
    And it answers 200
    And token "h" sends "POST /api/v1/stacks/infra/lock" through B
    And it answers 200
    And token "rt" records the protocol plan "valid" with artifact key "ak" for run "run" in state version 5 through A
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then it answers 200
    And the answer's action is '"pause"'
    And the answer's state is '"UNCONFIRMED"'
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then it answers 200
    And the answer's PLAN receipt outcomes are "ALLOW"
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"

  @case:success
  Scenario: An on-worker allow replays only the exact committed delta
    Given token "h" sends "PATCH /api/v1/stacks/infra" with '{"autodeploy":true}' through A
    And it answers 200
    And token "rt" records the protocol plan "valid" with artifact key "ak" for run "run" in state version 5 through A
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then it answers 200
    And the answer's action is '"execute"'
    And the answer's state is '"APPLYING"'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":2,"change":0,"delete":0,"import":0,"outputs":false}}' through A
    Then it answers a 409 problem "invalid-state"
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through A
    Then it answers 200
    And the answer's action is '"execute"'
    And the answer's stateVersion is '6'
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer's PLAN receipt outcomes are "ALLOW"
    And the run "run" committed PLAN receipts at state versions "6"
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING"
