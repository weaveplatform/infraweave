@postgres @objectstore @vcs @docker @terraform @policy-initialization
Feature: Server initialization policies refuse assignment before tool startup
  C52 records QUEUED, READY, FAILED with the exact assignment-denial note.
  This server decision occurs before the launcher starts a run container.
  A task's skipInitialization setting changes tool initialization, not this gate.
  ref: decisions 0008 and 0009; capture C52; #660

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "initialization-policy"\n} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Reject assignment","type":"INITIALIZATION","space":"legacy","body":"package infraweave\ndeny contains \"No tool startup\" if { input.run.state == \"READY\" }\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through B
    And it answers 200

  @op:runTrigger @op:taskCreate @case:failure
  Scenario Outline: A denied <kind> fails at READY without reaching worker preparation
    When token "h" sends "POST /api/v1/stacks/infra/<operation>" with '<body>' through A
    Then it answers <status>
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, FAILED"
    And the answer's last state transition's note is "job assignment failed: denied by 1 initialization policy"
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through A

    Examples:
      | kind                         | operation | body                                      | status |
      | tracked run                  | runs      | {"commitSha":"{seed}","runType":"TRACKED"} | 200    |
      | task skipping initialization | tasks     | {"command":"terraform version","skipInitialization":true} | 201 |
