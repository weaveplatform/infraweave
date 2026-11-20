@postgres
Feature: Linked policy runtime
  The policy runtime endpoint reports the OPA module linked into the real
  server binary. Account authentication, the policies capability and ordinary
  shared controls cover this read, including feature and caller switches.

  @op:policyRuntime @case:success
  Scenario: Both replicas report the OPA version actually linked from this build
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    When token "h" sends "GET /api/v1/policy-runtime" through A
    Then it answers 200
    And the runtime version matches the pinned OPA module
    And the policy contract answer has the shared cache and rate headers
    When token "m" sends "GET /api/v1/policy-runtime" through B
    Then it answers 200
    And the runtime version matches the pinned OPA module

  @op:policyRuntime @case:denial
  Scenario: Switching off policies refuses runtime metadata reads
    Given the installation switches off the features "policies"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "GET /api/v1/policy-runtime" through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "GET /api/v1/policy-runtime" through B
    Then it answers a 403 problem "feature-disabled"

  @op:policyRuntime @case:failure
  Scenario: Missing credentials and a disabled caller refuse runtime metadata
    Given two infraweave servers, A and B, on one database
    When the browser sends "GET /api/v1/policy-runtime" through A
    Then it answers a 401 problem "unauthenticated"
    And the response challenges for a bearer token
    When the operator disables the caller "api_key:01KACME0000000000000000003" in the table
    Then "GET /api/v1/policy-runtime" through A eventually answers a 503 problem "api-disabled"
    And "GET /api/v1/policy-runtime" through B eventually answers a 503 problem "api-disabled"
