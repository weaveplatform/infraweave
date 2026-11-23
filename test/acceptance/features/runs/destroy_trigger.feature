@terraform-plans @postgres @objectstore @vcs @docker @terraform @policy-plan-worker
Feature: A destroy run destroys a stack's resources and leaves it disabled
  A caller triggers a destroy run on a stack, which queues behind the
  stack's other runs on the commit the stack tracks. It plans the destroy
  first, under the stack's plan policies: a deny fails it, and a warning
  holds it for confirmation, after which it destroys in a new container
  from the workspace its planning saved; otherwise it destroys in the
  container that planned. Once it finishes, the stack is disabled and has
  no outputs. A stack that is being deleted, is protected from deletion,
  has a destroy run that has not ended, manages no resources, is disabled
  or is locked by another caller is refused, as is a caller without
  RUN_DESTROY_TRIGGER, and an unknown stack is not found. Each run
  executes on a real launcher of a private pool, in a container of
  {runner.terraform} with the newest Terraform the version catalogue
  lists. The repository's configuration has a terraform_data resource
  whose input changes on every plan and an output, so every tracked plan
  changes something and no provider is downloaded. The home key is
  space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it. Stacks live in legacy and keep their state
  in infraweave.
  ref: decision 0008
  ref: decision 0009

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = timestamp()\n}\n\noutput "name" {\n  value = terraform_data.this.output\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runDestroyTrigger @op:runConfirm @case:success
  Scenario: A destroy run queues behind a busy stack, plans the destroy, destroys the resources and disables the stack
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "busy"
    And token "h" eventually reads the run "busy" in the state "UNCONFIRMED" through B
    When token "h" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    Then it answers 201
    And the answer's type is '"DESTROY"'
    And the answer's state is '"QUEUED"'
    And the answer's commits is '["{seed}"]'
    And the answer's id names "destroy"
    When token "h" sends "POST /api/v1/stacks/infra/destroy-runs" through B
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "has not ended"
    When token "h" sends "GET /api/v1/runs/{destroy}" through A
    Then the answer's state is '"QUEUED"'
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{busy}"}' through B
    Then it answers 200
    And token "h" eventually reads the run "destroy" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, DESTROYING, FINISHED"
    And the outbox holds the run "destroy"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, DESTROYING, FINISHED"
    And token "h" reads the run "destroy"'s PLANNING log through B, which holds "Plan: 0 to add, 0 to change, 1 to destroy." and not "Error"
    And token "h" reads the run "destroy"'s DESTROYING log through A, which holds "Destroy complete! Resources: 1 destroyed." and not "Error"
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's isDisabled is 'true'
    And the answer's outputs is '[]'
    When token "h" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "manages no resources"

  @op:runDestroyTrigger @op:runConfirm @case:failure
  Scenario: A plan policy denies a destroy run or holds it for confirmation, and a disabled or locked stack is refused
    Given the member key is space-admin on the space "legacy"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"Guard destroys","type":"PLAN","space":"legacy","body":"package infraweave\ndeny contains \"Keep the stack\" if { some c in input.terraform.resource_changes; \"delete\" in c.change.actions }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    Then it answers 201
    And the answer's id names "denied"
    And token "h" eventually reads the run "denied" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the answer's last state transition's note is "denied by plan policy"
    And the answer's plan policy outcomes are '[{"deny":["Keep the stack"],"warn":[]}]'
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer's isDisabled is 'false'
    When token "h" sends "POST /api/v1/stacks/infra/disable" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "disabled"
    When token "h" sends "POST /api/v1/stacks/infra/enable" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/destroy-runs" through B
    Then it answers a 409 problem "resource-locked"
    And the answer's lockedBy is '"01KACME0000000000000000003"'
    When token "h" sends "POST /api/v1/stacks/infra/unlock" through A
    Then it answers 200
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"body":"package infraweave\nwarn contains \"Review the destroy\" if { some c in input.terraform.resource_changes; \"delete\" in c.change.actions }\n"}' through B
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    Then it answers 201
    And the answer's id names "destroy"
    And token "h" eventually reads the run "destroy" in the state "UNCONFIRMED" through B
    And the answer's plan policy outcomes are '[{"deny":[],"warn":["Review the destroy"]}]'
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{destroy}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "destroy" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, DESTROYING, FINISHED"
    And token "h" reads the run "destroy"'s DESTROYING log through A, which holds "Destroy complete! Resources: 1 destroyed." and not "Error"
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's isDisabled is 'true'

  @op:runDestroyTrigger @case:failure
  Scenario: A stack protected from deletion, managing no resources or being deleted is refused, and an unknown stack is not found
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Kept","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","protectFromDeletion":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/kept/destroy-runs" through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "protected from deletion"
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When token "h" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "manages no resources"
    When token "h" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "being deleted"
    And token "h" eventually finds no stack "infra" through B
    When token "h" sends "POST /api/v1/stacks/missing/destroy-runs" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/kept/runs" through B
    Then the answer's items are the runs ""

  @op:runDestroyTrigger @case:denial
  Scenario: A caller without RUN_DESTROY_TRIGGER is refused, and the stack runs nothing
    Given the member key is space-writer on the space "legacy"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "m" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_DESTROY_TRIGGER"'
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's items are the runs ""
