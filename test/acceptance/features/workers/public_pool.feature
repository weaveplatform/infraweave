@postgres
Feature: Installation public pool credentials
  The operator issues a public pool certificate from the host's P-256 CSR.
  Account credentials cannot administer it. Rotation preserves installation
  identity and requires an explicit credential precondition.

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a certificate signing request for a P-256 key is kept as "csr2"

  @op:publicWorkerPoolCreate @op:publicWorkerPoolCredentials @op:publicWorkerPoolReset @case:success
  Scenario: Operator credentials create read and rotate the same installation pool across replicas
    When token "op" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"{csr}"}' through A
    Then it answers 201
    And the answer's id names "public"
    And the answer's version is '1'
    And the answer's ETag is kept as "first"
    And the answer's pool token names the pool "public" and chains to the pool CA, its serial kept as "first"
    When token "op" sends "GET /operator/v1/public-worker-pool" through B
    Then it answers 200
    And the answer's id is '"{public}"'
    And the answer's version is '1'
    When token "op" sends "POST /operator/v1/public-worker-pool/reset" with '{"certificateSigningRequest":"{csr2}"}' if matching the ETag "first" through B
    Then it answers 200
    And the answer's id is '"{public}"'
    And the answer's version is '2'
    And the answer's ETag differs from "first"
    When token "op" sends "POST /operator/v1/public-worker-pool/reset" with '{"certificateSigningRequest":"{csr}"}' if matching the ETag "first" through A
    Then it answers a 412 problem "precondition-failed"
    When token "op" sends "GET /operator/v1/public-worker-pool" through A
    Then it answers 200
    And the answer's version is '2'
    When token "h" sends "GET /api/v1/worker-pools/{public}" through A
    Then it answers a 404 problem "not-found"

  @op:publicWorkerPoolCreate @op:publicWorkerPoolCredentials @op:publicWorkerPoolReset @case:denial
  Scenario: An account credential cannot read create or reset installation credentials
    When token "h" sends "GET /operator/v1/public-worker-pool" through B
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"{csr}"}' through A
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "POST /operator/v1/public-worker-pool/reset" with '{"certificateSigningRequest":"{csr2}"}' through B
    Then it answers a 401 problem "unauthenticated"

  @op:publicWorkerPoolCreate @op:publicWorkerPoolCredentials @op:publicWorkerPoolReset @case:failure
  Scenario: Missing credentials invalid requests and unconditional rotation fail without replacement
    When token "op" sends "GET /operator/v1/public-worker-pool" through A
    Then it answers a 404 problem "not-found"
    When token "op" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"invalid"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/certificateSigningRequest" as "rule"
    When token "op" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"{csr}"}' through A
    Then it answers 201
    And the answer's ETag is kept as "first"
    When token "op" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"{csr2}"}' through B
    Then it answers a 409 problem "invalid-state"
    When token "op" sends "POST /operator/v1/public-worker-pool/reset" with '{"certificateSigningRequest":"{csr2}"}' through A
    Then it answers a 428 problem "precondition-required"
    When token "op" sends "POST /operator/v1/public-worker-pool/reset" with '{"certificateSigningRequest":"invalid"}' if matching the ETag "first" through B
    Then it answers a 422 problem "validation-failed"
    When token "op" sends "GET /operator/v1/public-worker-pool" through A
    Then it answers 200
    And the answer's version is '1'

  @op:workerRegister @case:denial
  Scenario: Disabled public capacity cannot register even with valid operator-issued credentials
    Given token "op" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "public"
    And the answer's pool token names the pool "public" and chains to the pool CA, its serial kept as "first"
    When the public launcher "l" registers with the key of "csr" and the certificate "first" through B
    Then it answers a 401 problem "unauthenticated"

  @op:publicWorkerPoolReset @op:publicWorkerPoolCredentials @case:failure
  Scenario: A real credential persistence failure rolls back and can be retried
    Given token "op" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "public"
    And the answer's ETag is kept as "first"
    And public pool credential persistence is refused
    When token "op" sends "POST /operator/v1/public-worker-pool/reset" with '{"certificateSigningRequest":"{csr2}"}' if matching the ETag "first" through B
    Then it answers a 500 problem "internal-error"
    When token "op" sends "GET /operator/v1/public-worker-pool" through A
    Then it answers 200
    And the answer's id is '"{public}"'
    And the answer's version is '1'
    When public pool credential persistence is restored
    And token "op" sends "POST /operator/v1/public-worker-pool/reset" with '{"certificateSigningRequest":"{csr2}"}' if matching the ETag "first" through A
    Then it answers 200
    And the answer's id is '"{public}"'
    And the answer's version is '2'
