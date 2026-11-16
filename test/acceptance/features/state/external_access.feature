@postgres @objectstore @vcs @docker
Feature: Callers from outside a stack's runs read its state, read only
  A stack's state is read from outside its own runs through the http
  backend: a user's terraform_remote_state with HTTP Basic credentials of
  the user api and an API key's <id>:<secret> or a token as the
  password, and another stack's run with no credentials in its code,
  since the http backend takes the run's own from the environment its
  runner sets. Each reads once the stack's external state access is on
  and the reader holds STACK_STATE_READ and STACK_STATE_DOWNLOAD on the
  stack's space, a run through its stack's role bindings; none may lock or
  write it. Every caller but a run on its own stack is answered 401 for a
  bad credential or another user name, then 404 for a stack it cannot see
  or whose state infraweave does not keep, then 403. No Terraform Cloud
  API is served. As in the backend feature, the scenario is the runs'
  worker, and its tools reach the servers through the scenario's relay,
  which records each call; a user's workspace runs the same tool in a
  workspace of its own, with the credential only in its environment.
  Stack infra keeps its state with external state access on, deploys
  automatically and runs on the pool; reader keeps its state and runs on
  the pool; closed keeps its state with access off; plain does not keep
  it. The home key is space-admin on the account's
  top space; the member key holds only the bindings a scenario gives it.
  ref: decision 0010
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n\noutput "input" {\n  value = terraform_data.this.output\n}\n\noutput "token" {\n  value     = "plain-s3cret-0510"\n  sensitive = true\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{"externalStateAccessEnabled":true}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Reader","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":true}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Closed","repository":"infra","branch":"main","space":"legacy"},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Plain","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A

  @case:success
  Scenario Outline: An API key, a token and another stack's run read a stack's outputs
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And the worker takes over the run "run" with token "rt" and the key of "csr", checking out "infra", through A
    And the worker reports the run "run" started in INITIALIZING through B
    Then <tool> "init" of the run "run" exits 0
    When the worker reports the run "run" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And <tool> "plan" of the run "run" exits 0
    When the worker reports the run "run" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":true}' through B
    Then the report's answer is to execute in APPLYING
    And <tool> "apply -auto-approve" of the run "run" exits 0
    And the run "run" wrote 1 state version

    Given the member key is space-writer on the space "legacy"
    And a workspace "laptop" reads the output "input" of the stack "infra" with the member key through A
    Then <tool> "init" of the workspace "laptop" exits 0
    And <tool> "apply -auto-approve" of the workspace "laptop" exits 0
    And <tool> "output -raw input" of the workspace "laptop" exits 0
    And the last output of the workspace "laptop" is "one"
    And the relay passed a GET of the state of the stack "infra" answered 200
    Given a workspace "cli" reads the output "input" of the stack "infra" with token "m" through A
    Then <tool> "init" of the workspace "cli" exits 0
    And <tool> "apply -auto-approve" of the workspace "cli" exits 0
    And <tool> "output -raw input" of the workspace "cli" exits 0
    And the last output of the workspace "cli" is "one"

    Given the launcher "w2" registers with the key of "csr" and the certificate "cert" through B
    When token "h" sends "POST /api/v1/stacks/reader/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "other"
    And token "h" eventually reads the run "other" in the state "READY" through B
    And token "w2" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    And the answer assigns the run "other", its run token kept as "ot"
    And the worker takes over the run "other" with token "ot" and the key of "csr", checking out "infra", through A
    And the workspace of the run "other" reads the output "input" of the stack "infra" as "upstream"
    And the worker reports the run "other" started in INITIALIZING through B
    Then <tool> "init" of the run "other" exits 0
    When the worker reports the run "other" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And <tool> "plan" of the run "other" exits 1
    And the relay passed a GET of the state of the stack "infra" answered 403
    When token "h" sends "POST /api/v1/stack-role-bindings" with '{"stackID":"reader","roleID":"space-writer","spaceID":"legacy"}' through B
    Then it answers 201
    And <tool> "plan" of the run "other" exits 0
    And the last output of the run "other" holds 'upstream = "one"'
    When "LOCK /state/v1/stacks/infra" is sent to the state backend as "run" with token "ot" through A
    Then it answers a 403 problem "forbidden"
    When "POST /state/v1/stacks/infra" is sent to the state backend as "run" with token "ot" through B
    Then it answers a 403 problem "forbidden"
    And the run "run" wrote 1 state version

    @terraform
    Examples: Terraform
      | tool      |
      | terraform |

    @tofu
    Examples: OpenTofu
      | tool |
      | tofu |

  @case:denial
  Scenario: A reader needs both state actions and external access, and never locks or writes
    Given the member key is space-reader on the space "legacy"
    When "GET /state/v1/stacks/infra" is sent to the state backend as "api" with the member key through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_STATE_READ"'
    Given token "h" sends "POST /api/v1/roles" with '{"name":"State Readers","actions":["SPACE_READ","STACK_STATE_READ"]}' through B
    And the member key is state-readers on the space "legacy"
    When "GET /state/v1/stacks/infra/workspaces/blue" is sent to the state backend as "api" with token "m" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_STATE_DOWNLOAD"'
    When "GET /state/v1/stacks/closed" is sent to the state backend as "api" with the home key through A
    Then it answers a 403 problem "forbidden"
    When "POST /state/v1/stacks/infra" is sent to the state backend as "api" with the home key through B
    Then it answers a 403 problem "forbidden"
    When "LOCK /state/v1/stacks/infra" is sent to the state backend as "api" with token "h" through A
    Then it answers a 403 problem "forbidden"
    When "UNLOCK /state/v1/stacks/infra/workspaces/blue" is sent to the state backend as "api" with the home key through B
    Then it answers a 403 problem "forbidden"
    When "GET /state/v1/stacks/infra" is sent to the state backend as "api" with the home key through A
    Then it answers 204

  @case:failure
  Scenario: A bad credential answers 401, a hidden or unkept stack 404, a revoked key stops reading, and no Terraform Cloud API is served
    When "GET /state/v1/stacks/infra" is sent to the state backend as "admin" with the home key through A
    Then it answers a 401 problem "unauthenticated"
    And the answer challenges with 'Basic realm="infraweave-state"'
    When "GET /state/v1/stacks/infra" is sent to the state backend as "api" with the home key and another secret through B
    Then it answers a 401 problem "unauthenticated"
    And the answer challenges with 'Basic realm="infraweave-state"'
    When "GET /state/v1/stacks/infra" is sent to the state backend as "api" with no password through A
    Then it answers a 401 problem "unauthenticated"
    When "GET /state/v1/stacks/infra" is sent to the state backend as "run" with token "h" through B
    Then it answers a 401 problem "unauthenticated"
    When "GET /state/v1/stacks/infra" is sent to the state backend as "api" with the member key through A
    Then it answers a 404 problem "not-found"
    When "GET /state/v1/stacks/plain" is sent to the state backend as "api" with the home key through B
    Then it answers a 404 problem "not-found"
    When "GET /state/v1/stacks/nowhere" is sent to the state backend as "api" with token "h" through A
    Then it answers a 404 problem "not-found"
    And Terraform's service discovery through A names no Terraform Cloud service

    Given the member key is space-writer on the space "legacy"
    When "GET /state/v1/stacks/infra" is sent to the state backend as "api" with the member key through A
    Then it answers 204
    Given the home account lets API keys manage API keys
    When token "h" sends "DELETE /api/v1/api-keys/01KACME000000000000000000A" through B
    Then it answers 200
    Then "GET /state/v1/stacks/infra" sent to the state backend as "api" with the member key through A answers 401 within 30 seconds
    And "GET /state/v1/stacks/infra" sent to the state backend as "api" with token "m" through B answers 401 within 30 seconds
