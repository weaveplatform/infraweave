@postgres @vcs
Feature: A task runs a command in its stack's workspace
  A task is a TASK run of one command on the stack's tracked commit. It
  takes the stack's lock as a tracked run does, and every task creates a
  run, since a task is asked for to run each time. Its run answers the
  command, and its specification carries the command and whether it
  skips the tool's initialization. A space writer creates tasks; a space
  reader is refused TASK_CREATE, a stack the caller cannot read answers as
  one that does not exist, and a tier without the tasks feature refuses
  every task. A blank command, one holding a NUL byte and a disabled stack
  are refused. The release indexes serve the recorded fixtures. The home
  key is space-admin on the account's top space; the member key holds
  only the bindings a scenario gives it. Stack infra lives in legacy and
  runs on the pool; stack hidden lives in team.
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
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Hidden","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}"},"manageState":false}' through A
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A

  @op:taskCreate @case:success
  Scenario: A task runs on the tracked commit, carries its command to its specification, and each task is a run of its own
    Given the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output -json","skipInitialization":true}' through A
    Then it answers 201
    And the answer drew on the expensive budget
    And the answer's type is '"TASK"'
    And the answer's state is '"QUEUED"'
    And the answer's command is '"terraform output -json"'
    And the answer's commits is '["{seed}"]'
    And the answer's id names "task"
    And token "h" eventually reads the run "task" in the state "READY" through B
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output -json","skipInitialization":true}' through B
    Then it answers 201
    And the answer's id names "again"
    And the answer's state is '"QUEUED"'
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "task", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{task}/spec" through A
    Then it answers 200
    And the answer's task is '{"command":"terraform output -json","skipInitialization":true}'
    When token "h" sends "GET /api/v1/runs/{task}" through B
    Then it answers 200
    And the answer's command is '"terraform output -json"'
    When token "h" sends "GET /api/v1/stacks/infra/tasks" through A
    Then the answer's items are the runs "again, task"

  @op:taskCreate @case:denial
  Scenario: A space reader is refused, a stack the caller cannot read does not exist, and a tier without tasks refuses them
    Given the member key is space-reader on the space "legacy"
    And the sibling key is exchanged through A for token "sib"
    When token "m" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"TASK_CREATE"'
    When token "m" sends "POST /api/v1/stacks/hidden/tasks" with '{"command":"terraform output"}' through B
    Then it answers a 404 problem "not-found"
    When token "sib" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output"}' through A
    Then it answers a 404 problem "not-found"
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output"}' through B
    Then it answers 201
    And the answer's type is '"TASK"'
    And the answer's id names "mine"
    When token "op" creates the tier "Gold" with the feature "stacks" through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/tier" through B with token "h" eventually answers a 403 problem "feature-not-in-tier"
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output"}' through B
    Then it answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "tasks"
    When token "h" sends "GET /api/v1/stacks/infra/tasks" through B
    Then the answer's items are the runs "mine"

  @op:taskCreate @case:failure
  Scenario: A blank command, an empty one, one holding a NUL byte, a body without a command and a disabled stack are refused
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"   "}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/command" as "required"
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"ls\u0000-l"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/command" as "rule"
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":""}' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"skipInitialization":true}' through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/stacks/infra/disable" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output"}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "GET /api/v1/stacks/infra/tasks" through A
    Then the answer's items are the runs ""

  @policy-task @op:taskCreate @case:denial
  Scenario: Real V1 TASK decisions refuse the command before any run or scheduling effect on either replica
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Command guard","type":"TASK","space":"legacy","body":"package infraweave\ndeny contains \"Command refused by TASK policy\" if { input.request.command == \"terraform output -json\" }\nflag contains \"no-run-flag\" if { true }\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output -json","skipInitialization":true}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's detail is '"Command refused by TASK policy"'
    And the TASK answer carries its deprecation notice
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output -json"}' through B
    Then it answers a 403 problem "forbidden"
    And policy "guard" has 2 committed TASK refusals and no task creation effects

  @policy-task @op:taskCreate @case:failure
  Scenario: A stored V0 policy with an invalid TASK decision fails closed without creating a run
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Invalid TASK output","type":"TASK","engineType":"REGO_V0","space":"legacy","body":"package infraweave\ndeny[17] { true }\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through A
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output -json"}' through B
    Then it answers a 403 problem "forbidden"
    And the TASK answer carries its deprecation notice
    And policy "guard" has 1 committed TASK refusals and no task creation effects

  @policy-task @op:taskCreate @case:success
  Scenario: A real V0 TASK policy permits the existing task lifecycle while its deprecated API remains explicit
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Permit inspected command","type":"TASK","engineType":"REGO_V0","space":"legacy","body":"package infraweave\ndeny[msg] { input.request.command != \"terraform output -json\"; msg := \"Unexpected command\" }\n"}' through B
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through A
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform output -json"}' through A
    Then it answers 201
    And the answer's type is '"TASK"'
    And the answer's command is '"terraform output -json"'
    And the TASK answer carries its deprecation notice
    When token "h" sends "GET /api/v1/policies/{guard}" through B
    Then it answers 200
    And the TASK answer carries its deprecation notice
