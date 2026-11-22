@managed-cloud-workers @postgres
Feature: Managed cloud worker hosting beside one application control plane
  A managed cloud batch of test/terraform/lab applies each selected cloud's
  committed cluster and deployment roots under one owner, validates their
  hosting outputs and attaches them to the platform deployment's PostgreSQL.
  These scenarios borrow that attachment without replacement and run the
  current application's pool API beside it. They run only in a managed cloud
  batch; ordinary partitions exclude them, and an absent or foreign workers
  manifest fails the background rather than skipping.
  ref: decision 0016

  Background:
    Given the attached managed cloud worker hosting belongs to this batch
    And two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a certificate signing request for a P-256 key is kept as "csr"

  Scenario: The worker API creates and removes a pool beside the attached cloud hosting
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Managed cloud","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    Then it answers 201
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    When token "h" sends "DELETE /api/v1/worker-pools/{pool}" through B
    Then it answers 200
    And the answer's deleted is 'true'

  Scenario: The worker API denies pool creation to a space reader beside the attached cloud hosting
    Given the member key is space-reader on the space "{team}"
    When token "m" sends "POST /api/v1/worker-pools" with '{"name":"Denied","space":"{team}","certificateSigningRequest":"{csr}"}' through B
    Then it answers a 403 problem "forbidden"

  Scenario: The worker API refuses a malformed pool certificate request beside the attached cloud hosting
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Invalid","space":"{team}","certificateSigningRequest":"not-a-csr"}' through A
    Then it answers a 422 problem "validation-failed"
