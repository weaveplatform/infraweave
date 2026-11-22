@kubernetes-lab @kubernetes @postgres
Feature: Terraform-owned local Kubernetes lab
  Terraform owns the cluster, namespace, network and storage. The normal
  harness borrows it without replacement and runs the current application.
  This proves infrastructure readiness and existing worker APIs, not the
  Kubernetes worker controller or executor, which are separate delivery.
  ref: decision 0016

  Background:
    Given the owned Terraform Kubernetes API and namespace are ready
    And two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a certificate signing request for a P-256 key is kept as "csr"

  @op:workerPoolCreate @op:workerPoolDelete @case:success
  Scenario: The current worker API creates and removes a pool beside the ready cluster
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Local cluster","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    Then it answers 201
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    When token "h" sends "DELETE /api/v1/worker-pools/{pool}" through B
    Then it answers 200
    And the answer's deleted is 'true'

  @op:workerPoolCreate @case:denial
  Scenario: The existing API denies pool creation to a space reader
    Given the member key is space-reader on the space "{team}"
    When token "m" sends "POST /api/v1/worker-pools" with '{"name":"Denied","space":"{team}","certificateSigningRequest":"{csr}"}' through B
    Then it answers a 403 problem "forbidden"

  @op:workerPoolCreate @case:failure
  Scenario: The existing API refuses a malformed pool certificate request
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Invalid","space":"{team}","certificateSigningRequest":"not-a-csr"}' through A
    Then it answers a 422 problem "validation-failed"
