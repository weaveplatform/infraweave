@postgres @objectstore @vcs @policy-evidence
Feature: Public policy evidence enforces ownership before opening sealed inputs
  Safe evaluation metadata is readable without opening sealed objects. Raw sample,
  print and capture inputs require root-space administration, including through
  run receipt links. Every test uses real PostgreSQL, OPA and sealed RustFS data.
  ref: decisions 0003, 0007 and 0009; #662

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

    And the member key is exchanged through B for token "m"
    And the sibling key is exchanged through A for token "s"

  @op:policyEvaluationSample @op:policySearchEvaluationRecords @op:policySearchEvaluationRecordSuggestions @op:policyInputCaptures @op:policyInputCapture @op:runPolicyReceiptSample @case:success @case:denial @case:failure
  Scenario: All six evidence routes enforce current ownership root administration and expiry across replicas
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Public evidence","type":"APPROVAL","space":"legacy","body":"package infraweave\nsample := true\nprivate_source := \"evidence-private-source\"\napprove if { print(\"evidence-private-print\"); true }\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And public evidence handles for run "run" and policy "guard" are kept through B
    And token "h" reads all six evidence operations for run "run" and policy "guard" as "root" through A
    And token "m" reads all six evidence operations for run "run" and policy "guard" as "unbound" through B
    When the member key is space-admin on the space "legacy"
    Then token "m" reads all six evidence operations for run "run" and policy "guard" as "child-admin" through A
    And token "s" reads all six evidence operations for run "run" and policy "guard" as "foreign" through B
    And metadata evidence operations reject invalid typed filters through A
    When all retained public policy evidence expires
    Then token "h" reads all six evidence operations for run "run" and policy "guard" as "expired" through B
    And token "h" eventually reads run "run" with "expired" APPROVAL samples through A
    When the evidence replicas restart with policies disabled
    Then token "h" reads all six evidence operations for run "run" and policy "guard" as "feature-disabled" through A
