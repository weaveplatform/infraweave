@postgres @objectstore @vcs @policy-sampling
Feature: Real policy decisions retain requested evaluation samples
  A denied TASK creates no run, but its committed policy evaluation can request
  a sealed sample independently of run creation. Samples belong to the policy
  owner and are retained only after the object reference commits.
  ref: decision 0009; #661

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

  @op:taskCreate @case:denial
  Scenario: A sampled V1 TASK denial survives across replicas without creating a run
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Sample denied command","type":"TASK","space":"legacy","body":"package infraweave\nsample := true\ndeny contains \"Sampling denied command\" if { input.request.command == \"echo sampling-private-command\"; print(\"sampling-private-print\") }\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo sampling-private-command","skipInitialization":true}' through A
    Then it answers a 403 problem "forbidden"
    And policy "guard" has 1 committed TASK refusals and no task creation effects
    And policy "guard" eventually has 1 retained TASK sample
    And policy "guard" keeps "sampling-private-command" and "sampling-private-print" out of raw storage and logs

  @op:taskCreate @case:denial
  Scenario: Capture retention is independent of a false sample decision
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Capture without sampling","type":"TASK","space":"legacy","body":"package infraweave\nsample := false\ndeny contains \"Capture refused command\" if { input.request.command == \"echo capture-sensitive-command\" }\n"}' through B
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through A
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo capture-sensitive-command"}' through B
    Then it answers a 403 problem "forbidden"
    And policy "guard" has 1 committed TASK refusals and no task creation effects
    And policy "guard" eventually has 0 retained TASK sample
    And the latest TASK capture has a sanitized command and no "capture-sensitive-command"

  @op:runRead @case:success
  Scenario: Public approval receipts distinguish retained evidence from expired samples before and after cleanup
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Sample approval","type":"APPROVAL","space":"legacy","body":"package infraweave\nsample := true\napprove if true\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And token "h" eventually reads run "run" with "retained" APPROVAL samples through A
    When the retained inputs for run "run" expire
    Then token "h" eventually reads run "run" with "expired" APPROVAL samples through B
    When the input cleanup job removes the expired private data for run "run"
    Then token "h" eventually reads run "run" with "expired" APPROVAL samples through A
