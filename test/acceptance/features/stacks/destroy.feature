@postgres @vcs
Feature: A stack deleted with its resources stays until its destroy run finishes
  A delete with destroyResources answers the stack deleting and creates a
  DESTROY run on its tracked commit. While the stack is deleting it runs
  nothing else, and a delete without destroyResources is refused; another
  delete with it answers the stack as it is. The stack is deleted when
  its destroy run finishes; a destroy run that ends any other way leaves
  it, no longer deleting. A protected stack, a disabled one and a caller
  without STACK_DELETE are refused. Here the scenario is the worker: it
  registers with the pool's key, claims the destroy run and reports its
  phases through the worker API. The release indexes serve the recorded
  fixtures, so the run's specification names a Terraform release. The
  home key is space-admin on the account's top space; the member key
  holds only the bindings a scenario gives it. Stack infra lives in legacy
  and runs on the pool.
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
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And the launcher "l" registers with the key of "csr" and the certificate "cert" through A

  @op:stackDelete @case:success
  Scenario: A stack being deleted runs nothing but its destroy run, and is deleted when that run finishes
    When token "h" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through A
    Then it answers 200
    And the answer's deleting is 'true'
    And the answer's deleted is 'false'
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then it answers 200
    And the answer's first run names "destroy"
    And token "h" eventually reads the run "destroy" in the state "READY" through A
    When token "h" sends "GET /api/v1/runs/{destroy}" through B
    Then the answer's type is '"DESTROY"'
    And the answer's commits is '["{seed}"]'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output"}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"infra"}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stacks/0/stack" as "rule"
    When token "h" sends "DELETE /api/v1/stacks/infra" through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through A
    Then it answers 200
    And the answer's deleting is 'true'
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's items are the runs "destroy"
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "destroy", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{destroy}/spec" through A
    Then it answers 200
    When token "rt" sends "POST /worker/v1/runs/{destroy}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through B
    Then it answers 200
    When token "rt" sends "POST /worker/v1/runs/{destroy}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through A
    Then it answers 200
    And the answer's state is '"DESTROYING"'
    When token "rt" sends "POST /worker/v1/runs/{destroy}/reports" with '{"event":"succeeded","state":"DESTROYING","stateVersion":5}' through B
    Then it answers 200
    And the answer's action is '"exit"'
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/{destroy}" through B
    Then it answers a 404 problem "not-found"

  @op:stackDelete @case:denial
  Scenario: A caller without STACK_DELETE is refused, and the stack stays as it was
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_DELETE"'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer's deleting is 'false'
    When token "h" sends "GET /api/v1/stacks/infra/runs" through A
    Then the answer's items are the runs ""

  @op:stackDelete @case:failure
  Scenario: A destroy run that fails leaves the stack, and a disabled or protected stack is refused
    When token "h" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's first run names "destroy"
    And token "h" eventually reads the run "destroy" in the state "READY" through A
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "destroy", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{destroy}/spec" through A
    And token "rt" sends "POST /worker/v1/runs/{destroy}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through B
    And token "rt" sends "POST /worker/v1/runs/{destroy}/reports" with '{"event":"failed","state":"INITIALIZING","stateVersion":4,"details":"The destroy failed."}' through A
    Then it answers 200
    And the answer's action is '"exit"'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer's deleting is 'false'
    When token "h" sends "GET /api/v1/runs/{destroy}" through A
    Then the answer's state is '"FAILED"'
    When token "h" sends "POST /api/v1/stacks/infra/disable" through B
    Then it answers 200
    When token "h" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "disabled"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Kept","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","protectFromDeletion":true},"manageState":false}' through B
    And token "h" sends "DELETE /api/v1/stacks/kept?destroyResources=true" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/stacks/kept/runs" through B
    Then the answer's items are the runs ""
    When token "h" sends "GET /api/v1/stacks/infra/runs" through A
    Then the answer's items are the runs "destroy"
