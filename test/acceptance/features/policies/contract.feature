@postgres
Feature: The typed policy contract
  Policy schema is served on the API listener at its root discovery path.
  An account credential and the policies capability are required. The same
  no-store, rate-limit and caller-disable controls cover it as other API reads.
  Its schema is generated from the actual input and decision types; the
  response must validate every recorded fixture after the namespace rewrite.

  @op:policyContract @case:success
  Scenario: Both replicas publish a complete schema through the ordinary account controls
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    When token "h" sends "GET /.well-known/policy-contract.json" through A
    Then it answers 200
    And the policy contract answer has the shared cache and rate headers
    And the policy contract validates every recorded input and decision
    When token "m" sends "GET /.well-known/policy-contract.json" through B
    Then it answers 200
    And the policy contract validates every recorded input and decision
    And "GET /.well-known/policy-contract.json" on the admin listener answers 404

  @op:policyContract @case:denial
  Scenario: The installation's policies switch refuses contract reads
    Given the installation switches off the features "policies"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "GET /.well-known/policy-contract.json" through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "GET /.well-known/policy-contract.json" through B
    Then it answers a 403 problem "feature-disabled"

  @op:policyContract @case:failure
  Scenario: Missing credentials and the database-backed caller switch refuse schema reads
    Given two infraweave servers, A and B, on one database
    When the browser sends "GET /.well-known/policy-contract.json" through A
    Then it answers a 401 problem "unauthenticated"
    And the response challenges for a bearer token
    When the operator disables the caller "api_key:01KACME0000000000000000003" in the table
    Then "GET /.well-known/policy-contract.json" through A eventually answers a 503 problem "api-disabled"
    And the response carries Retry-After
    And "GET /.well-known/policy-contract.json" through B eventually answers a 503 problem "api-disabled"
