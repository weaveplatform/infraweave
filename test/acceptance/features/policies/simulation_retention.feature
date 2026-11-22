@postgres @objectstore @vcs @policies
Feature: Simulation preserves existing retained policy evidence and permissions
  An edited decision may request samples or administrator access, but simulation
  never applies either request or changes a previously published capture.
  ref: decision 0009; #663

  @op:policySimulatev2 @case:success
  Scenario: A simulated LOGIN grant and true sample leave real evidence and member permissions unchanged
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Retain before simulation","type":"TASK","space":"legacy","body":"package infraweave\nsample := true\ndeny contains \"seed denial\" if true\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo simulation-existing-private-command"}' through A
    And it answers a 403 problem "forbidden"
    And policy "guard" eventually has 1 retained TASK sample
    And the latest TASK capture has a sanitized command and no "simulation-existing-private-command"
    And the member key is space-reader on the space "legacy"
    And the member key is exchanged through A for token "m"
    And the simulation's evaluation and workflow state is recorded
    When token "m" sends "POST /api/v1/policies/simulate" with '{"type":"LOGIN","body":"package infraweave\nallow := true\nadmin := true\nsample := true\nflag contains \"simulation-private-print\" if { print(\"simulation-private-print\") }\n","input":"{}"}' through B
    Then it answers 200
    And token "m" still cannot create an administrative space
    And the simulation leaves evaluation, workflow and object storage unchanged
    And the simulation keeps "simulation-private-print" out of server logs
