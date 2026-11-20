@postgres @vcs
Feature: Historical run policy evidence
  Public run reads and run_updated expose committed receipt metadata and the
  flag ownership union. This read fixture does not execute a public policy gate;
  approval and worker-report integration are verified by POL-12/13 and POL-16.

  @op:run @case:success @case:denial @case:failure
  Scenario: Two servers expose historical evidence without leaking raw decisions or deleted policy source
    Given two infraweave servers, A and B, on one database, that run no default-queue jobs
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    And the sibling key is exchanged through A for token "sib"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through A
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    And the answer's id names "run"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Snapshot guard","type":"APPROVAL","body":"package infraweave\napprove if true\n"}' through B
    And it answers 201
    And the answer's id names "policy"
    And the run "run" has a historical receipt fixture for policy "policy"
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer's flags is '["reviewed"]'
    And the answer carries the historical receipt for policy "policy", deleted false
    And neither the answer nor a server's log names "policy-evidence-private-marker"
    When token "m" sends "GET /api/v1/runs/{run}" through A
    Then it answers a 404 problem "not-found"
    When token "sib" sends "GET /api/v1/runs/{run}" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/nope" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"name":"Renamed guard"}' through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/policies/{policy}" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then it answers 200
    And the answer carries the historical receipt for policy "policy", deleted true
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/cancel" with '{}' through B
    Then it answers 200
    And the run "run"'s latest update carries only public policy evidence
