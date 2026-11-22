@postgres @objectstore @vcs @policies
Feature: Policy page projections compose served public operations
  Safe evaluation metadata is readable without opening sealed objects. Raw sample,
  print and capture inputs require root-space administration, including through
  run receipt links. Every test uses real PostgreSQL, OPA and sealed RustFS data.
  ref: decisions 0003, 0007 and 0009; #665

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

  @op:policy @case:success
  Scenario: Policy views project real retained counts and safe committed metadata
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Projection evidence","type":"APPROVAL","space":"legacy","body":"package infraweave\nsample := true\napprove if true\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{guard}" through B
    And it answers 200
    And the answer's id names "attachment"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And token "h" eventually reads run "run" with "retained" APPROVAL samples through A
    Then token "h" eventually reads policy "guard" with "retained" evidence projections and 1 visible attachments through B

    And token "h" reads stack "infra" and attachment "attachment" for policy "guard" with committed history through A
    When token "h" sends "PATCH /api/v1/policies/{guard}" with '{"name":"Current projection name","description":"Current projection description"}' through A
    Then it answers 200
    When token "h" sends "GET /api/v1/policies/{guard}" through B
    Then it answers 200
    And the answer's name is '"Current projection name"'
    And the answer's description is '"Current projection description"'
    And token "h" reads stack "infra" and attachment "attachment" for policy "guard" with committed history through B
    Given the member key is space-reader on the space "legacy"
    Then token "m" eventually reads policy "guard" with "retained" evidence projections and 1 visible attachments through A
    And token "m" reads stack "infra" and attachment "attachment" for policy "guard" with committed history through B
    And token "m" cannot open the raw sample of policy "guard" through A
    When token "s" sends "GET /api/v1/policies/{guard}" through B
    Then it answers a 404 problem "not-found"
    Given the retained inputs for run "run" expire
    Then token "h" eventually reads policy "guard" with "expired" evidence projections and 1 visible attachments through A
    And token "m" eventually reads policy "guard" with "expired" evidence projections and 1 visible attachments through B
    And token "m" reads stack "infra" and attachment "attachment" for policy "guard" with committed history through A
    When token "h" sends "DELETE /api/v1/policy-attachments/{attachment}" through B
    Then it answers 200
    And token "h" eventually reads policy "guard" with "expired" evidence projections and 0 visible attachments through A
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer's attachedPolicies is '[]'
    When token "h" sends "DELETE /api/v1/policies/{guard}" through A
    Then it answers 200
    And the answer's id is '"{guard}"'
    When token "h" sends "GET /api/v1/policies/{guard}" through B
    Then it answers a 404 problem "not-found"


  @op:policy @op:policyUpdatev2Patch @op:policyDelete @case:success @case:failure
  Scenario: Current projections follow automatic labels and space moves without exposing hidden targets
    Given token "h" sends "PATCH /api/v1/spaces/legacy" with '{"inheritEntities":true}' through A
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Automatic view","type":"PLAN","body":"package infraweave\nsample := false\n","labels":["autoattach:*"]}' through A
    And it answers 201
    And the answer's id names "guard"
    Then token "h" eventually reads policy "guard" with 2 visible attachments and empty evidence through B
    Given the member key is space-reader on the space "legacy"
    Then token "m" eventually reads policy "guard" with 1 visible attachments and empty evidence through A
    When token "h" sends "PATCH /api/v1/policies/{guard}" with '{"space":"{team}"}' through B
    Then it answers 200
    And token "h" eventually reads policy "guard" with 1 visible attachments and empty evidence through A
    When token "m" sends "GET /api/v1/policies/{guard}" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "PATCH /api/v1/policies/{guard}" with '{"labels":[]}' through A
    Then it answers 200
    And token "h" eventually reads policy "guard" with 0 visible attachments and empty evidence through B
    When token "h" sends "DELETE /api/v1/policies/{guard}" through A
    Then it answers 200
    And the answer's hasAttachedStacks is 'false'
    And the answer's sampleCount is '0'
    And the answer's evaluationRecords is '[]'
    When token "h" sends "GET /api/v1/policies/{guard}" through B
    Then it answers a 404 problem "not-found"
