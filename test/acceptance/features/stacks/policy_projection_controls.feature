@postgres @policy-projection-denial
Feature: Stack projection entitlement controls
  Policy projections retain the stack's shared feature guard on both replicas.

  @op:stack @op:starSet @case:denial
  Scenario: Revoking stacks refuses a known projection and preserves its caller's star
    Given two infraweave servers, A and B, on one database, with an operator key
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And it answers 201
    And token "h" sends "PUT /api/v1/stacks/infra/star" with '{"star":true}' through B
    And it answers 200
    And the answer's starred is 'true'
    When token "op" creates the tier "No stacks" with the feature "spaces" through A
    And token "op" gives the home organization the tier "No stacks" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/stacks/infra" through A with token "h" eventually answers a 403 problem "feature-not-in-tier"
    And "GET /api/v1/stacks/infra" through B with token "h" eventually answers a 403 problem "feature-not-in-tier"
    When token "h" sends "PUT /api/v1/stacks/infra/star" with '{"star":false}' through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "PUT /api/v1/stacks/infra/star" with '{"star":false}' through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "op" grants the home organization the feature "stacks" through B
    Then "GET /api/v1/stacks/infra" through A with token "h" eventually answers 200
    And "GET /api/v1/stacks/infra" through B with token "h" eventually answers 200
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer's starred is 'true'
