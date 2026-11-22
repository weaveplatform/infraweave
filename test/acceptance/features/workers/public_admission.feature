@postgres @vcs
Feature: Public execution admission preserves shared pool restrictions
  A run retains its selected execution pool even when its stack changes.
  Public runs use FIFO and refuse priority changes; private runs retain
  their priority controls. Authorization still precedes pool restrictions.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through B
    And the answer's id names "publicrun"
    And token "h" eventually reads the run "publicrun" in the state "READY" through A

  @op:runPrioritySet @case:failure
  Scenario: Public runs refuse priority changes without changing their FIFO priority
    When token "h" sends "POST /api/v1/stacks/infra/runs/{publicrun}/priority-set" with '{"preset":"high"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/preset" as "rule"
    When token "h" sends "GET /api/v1/runs/{publicrun}" through B
    Then the answer's priorityPreset is '"normal"'

  @op:runPrioritySet @case:denial
  Scenario: Public priority restrictions preserve authorization before validation
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs/{publicrun}/priority-set" with '{"preset":"high"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_PRIORITIZE_SET"'

  @op:runPrioritySet @case:success
  Scenario: A run's original pool controls priority after its stack changes pools
    Given a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Private workers","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "privatepool"
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"workerPool":"{privatepool}"}' through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs/{publicrun}/priority-set" with '{"preset":"low"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{publicrun}/cancel" with '{}' through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "privaterun"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{privaterun}/priority-set" with '{"preset":"high"}' through B
    Then it answers 200
    And the answer's priorityPreset is '"high"'
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"workerPool":null}' through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs/{privaterun}/priority-set" with '{"preset":"low"}' through B
    Then it answers 200
    And the answer's priorityPreset is '"low"'
