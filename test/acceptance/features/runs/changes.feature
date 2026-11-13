@postgres @objectstore @vcs
Feature: A run's changes and delta are read after it plans
  After planning, a run's worker seals changes.json, the changes of its
  plan, with the run's artifact key, uploads it through a signed URL and
  commits it through the worker API, then reports the plan's delta. A
  caller who can read the run reads its changes: changes-v3 as one set
  of resources and changes-v2 as entities a page at a time, in the
  plan's order, each read of a phase and change type the input selects.
  The run answers every count of its delta, and so does its stack,
  which the run blocks, while the run waits for the plan's
  confirmation. Before the plan both reads answer none, and neither the
  run nor the stack a delta. Here the scenario is the worker: it builds
  the document's key, as a runner does, from the account the run's
  specification names. The changes are those of internal/iac's recorded
  Terraform plan of one change of every type. The run's in-run API token
  reads none of it. The release indexes serve the recorded fixtures, so
  the run's specification names a Terraform release. The home key is
  space-admin on the account's top space; the member key holds no
  binding. Stack infra lives in legacy and runs on the pool.
  ref: decision 0007
  ref: decision 0008
  ref: decision 0013
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
    And the specification names the run "run" of the home account, kept as "acct"
    And the specification's sealed part opens with the key of "csr" to an in-run API token, kept as "it"
    And the specification's sealed part opens with the key of "csr" to the run's artifact key, kept as "ak"
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through B
    And it answers 200
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through A
    And the answer's state is '"PLANNING"'

  @op:runChangesV2 @op:runChangesV3 @op:run @op:stack @op:runStack @case:success
  Scenario: Once the run plans, both reads answer the plan's changes, and the run and its stack its delta
    When token "h" sends "GET /api/v1/runs/{run}/changes-v3?input=%7B%7D" through A
    Then it answers 200
    And the answer is '[]'
    When token "h" sends "GET /api/v1/runs/{run}/changes-v2?input=%7B%7D" through B
    Then it answers 200
    And the answer's edges is '[]'
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then it answers 200
    And the answer leaves out "delta"
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer's blocker is '{"id":"{run}"}'
    And the answer leaves out "delta"
    When token "rt" commits the changes of the recorded terraform plan "all", sealed with the artifact key "ak", for the run "run" of the account "acct" in state version 5 through B
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":3,"change":1,"delete":3,"import":1,"move":1,"forget":1,"resources":7,"outputs":true}}' through A
    Then the answer's state is '"UNCONFIRMED"'
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer's delta is '{"addCount":3,"changeCount":1,"deleteCount":3,"importCount":1,"moveCount":1,"forgetCount":1,"resources":7,"okCount":0,"skipCount":0,"rescueCount":0,"ignoreCount":0,"unreachableCount":0,"failedCount":0}'
    When token "h" sends "GET /api/v1/stacks/infra/run?id={run}" through A
    Then the answer's delta is '{"addCount":3,"changeCount":1,"deleteCount":3,"importCount":1,"moveCount":1,"forgetCount":1,"resources":7,"okCount":0,"skipCount":0,"rescueCount":0,"ignoreCount":0,"unreachableCount":0,"failedCount":0}'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer's delta is '{"addCount":3,"changeCount":1,"deleteCount":3,"importCount":1,"moveCount":1,"forgetCount":1,"resources":7,"okCount":0,"skipCount":0,"rescueCount":0,"ignoreCount":0,"unreachableCount":0,"failedCount":0}'
    When token "h" sends "GET /api/v1/runs/{run}/stack" through A
    Then it answers 200
    And the answer's delta is '{"addCount":3,"changeCount":1,"deleteCount":3,"importCount":1,"moveCount":1,"forgetCount":1,"resources":7,"okCount":0,"skipCount":0,"rescueCount":0,"ignoreCount":0,"unreachableCount":0,"failedCount":0}'
    When token "h" sends "GET /api/v1/runs/{run}/changes-v3?input=%7B%7D" through A
    Then the answer lists the changes of the recorded terraform plan "all"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v3?input=%7B%22changePhase%22%3A%22PLAN%22%7D" through B
    Then the answer lists the changes of the recorded terraform plan "all"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v2?input=%7B%7D" through A
    Then the answer lists the changes of the recorded terraform plan "all"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v2?input=%7B%22first%22%3A4%7D" through B
    Then the answer's edges are the changes "data.terraform_remote_state.read, terraform_data.add, terraform_data.change, terraform_data.delete"
    And the answer's endCursor is kept as "page"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v2?input=%7B%22first%22%3A4%2C%22after%22%3A%22{page}%22%7D" through A
    Then the answer's edges are the changes "terraform_data.forget, terraform_data.imported, terraform_data.keep, terraform_data.new"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v2?input=%7B%22changeType%22%3A%22DELETE%22%7D" through B
    Then the answer's edges are the changes "terraform_data.delete"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v3?input=%7B%22changePhase%22%3A%22APPLY%22%7D" through A
    Then the answer is '[]'

  @op:runChangesV2 @op:runChangesV3 @case:denial
  Scenario: The run's own in-run API token reads neither of its changes, and a caller who cannot read the run finds none
    When token "it" sends "GET /api/v1/runs/{run}/changes-v2?input=%7B%7D" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_READ"'
    When token "it" sends "GET /api/v1/runs/{run}/changes-v3?input=%7B%7D" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_READ"'
    When token "m" sends "GET /api/v1/runs/{run}/changes-v2?input=%7B%7D" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/runs/{run}/changes-v3?input=%7B%7D" through B
    Then it answers a 404 problem "not-found"

  @op:runChangesV2 @op:runChangesV3 @case:failure
  Scenario: An unknown run, a missing or invalid input, and a cursor that is not this list's are refused
    When token "h" sends "GET /api/v1/runs/01JBQ6Q4V2ZK5Y3W8N7M6P5R4T/changes-v2?input=%7B%7D" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/01JBQ6Q4V2ZK5Y3W8N7M6P5R4T/changes-v3?input=%7B%7D" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v3" through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v2?input=%7B%22changePhase%22%3A%22LATER%22%7D" through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v3?input=%7B%22phase%22%3A%22PLAN%22%7D" through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v2?input=%7B%22after%22%3A%22garbage%22%7D" through B
    Then it answers a 400 problem "invalid-cursor"
