@postgres
Feature: infraweavectl operator
  infraweavectl is infraweave's client CLI over pkg/sdk (decision 0001). A
  profile names an endpoint and a credential; infraweavectl operator wraps
  the operator API's tiers, organizations, grants and billing (decision
  0021), printing each answer as JSON and, for an error, its problem.

  @case:success
  Scenario: The operator manages a tier, an organization and billing with infraweavectl
    Given two infraweave servers, A and B, on one database, with an operator key
    And infraweavectl's profile "ops" holds A's endpoint and the operator key
    Then infraweavectl whoami prints the claims of an operator token
    And infraweavectl version names infraweavectl
    And infraweavectl "operator tier create Gold --feature stacks --limit seats=3" prints JSON holding '{"name":"Gold","features":["stacks"],"limits":{"seats":3},"editable":true}'
    And infraweavectl "operator billing set --enabled --default-tier Gold" prints JSON holding '{"enabled":true,"defaultTier":"Gold"}'
    And infraweavectl "operator organization create initech --name Initech --account-slug initech --account-name Main" prints JSON holding '{"slug":"initech","name":"Initech"}'
    And infraweavectl "operator grant list initech" prints JSON holding '{"tier":"Gold","features":["stacks"],"limits":{"seats":3}}'
    And infraweavectl "operator grant feature set initech viewer" prints JSON holding '{"features":["stacks","viewer"]}'
    And infraweavectl "operator grant limit set initech seats unlimited" prints JSON holding '{"limits":{}}'
    And infraweavectl "operator organization tier set initech Free" prints JSON holding '{"tier":"Free"}'
    And infraweavectl "operator tier get Gold" prints JSON holding '{"organizations":0}'
    And infraweavectl "api GET /operator/v1/billing" prints JSON holding '{"enabled":true,"defaultTier":"Gold"}'

  @case:failure @case:denial
  Scenario: infraweavectl prints the problem of a refused change
    Given two infraweave servers, A and B, on one database, with an operator key
    And infraweavectl's profile "ops" holds B's endpoint and the operator key
    Then infraweavectl "operator tier update Enterprise" fails with a 409 problem "invalid-state"
    And infraweavectl "operator tier delete Missing" fails with a 404 problem "not-found"
    And infraweavectl "operator tier list" with the home key fails with a 401 problem "unauthenticated"
