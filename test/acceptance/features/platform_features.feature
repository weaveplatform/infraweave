@postgres
Feature: Features the installation switches off
  features.disabled switches a feature off for every organization of the
  installation (decision 0021). Each operation names the capability it
  belongs to, so a request for a switched-off one answers 403
  feature-disabled, naming the feature, whatever the organization's tier
  and whether billing is on; every other operation is unaffected, and so is
  a switched-off feature no served operation belongs to. With billing on,
  seats are counted as Spacelift counts them: an API key takes a seat on
  its first use in 30 days, so the exchange that would take one past the
  limit answers 403 limit-reached and issues no token.

  Scenario: A switched-off capability refuses its operations alone
    Given a running infraweave server whose features.disabled is "stacks, ai-integrations"
    Then "GET /api/v1/stacks" on the API listener answers a 403 problem "feature-disabled"
    And the problem names the feature "stacks"
    And "POST /api/v1/stacks/search" on the API listener answers a 403 problem "feature-disabled"
    And the problem names the feature "stacks"
    And "GET /api/v1/name" on the API listener answers 200

  Scenario: With nothing switched off the operation runs
    Given a running infraweave server
    Then "GET /api/v1/stacks" on the API listener answers a 501 problem "not-implemented"

  Scenario: A key's first use beyond the seats limit is refused
    Given two infraweave servers, A and B, on one database
    And the home organization is on the Free tier with billing on, its 2 seats taken
    Then the sibling key's first use through B answers a 403 problem "limit-reached"
    And the problem names the limit "seats" of the organization at 2
    And the home key is exchanged through A for token "h"

  @op:tierFeatures @case:success
  Scenario: The entitlement lists the organization's tier and grants
    Given two infraweave servers, A and B, on one database, with an operator key
    When the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"
    And token "op" creates the tier "Gold" with the feature "usage" through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" grants the home organization the feature "stacks" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/tier-features" through B with token "h" eventually answers '{"items":["stacks","usage"]}'

  @op:tierFeatures @op:evaluateFeatureFlags @case:success
  Scenario: A switched-off feature leaves the entitlement, and no Spacelift flag is set
    Given a running infraweave server whose features.disabled is "stacks"
    Then the tier features read on the API listener lack "stacks"
    And the tier features read on the API listener hold "usage"
    And "GET /api/v1/evaluate-feature-flags?input=%7B%22flags%22%3A%5B%5D%7D" on the API listener answers '{"items":[]}'

  @op:tierFeatures @case:success
  Scenario: With nothing switched off the entitlement holds every feature
    Given a running infraweave server
    Then the tier features read on the API listener hold "stacks"

  @op:evaluateFeatureFlags @case:denial
  Scenario: With the console switched off its flags are refused
    Given a running infraweave server whose features.disabled is "console"
    Then "GET /api/v1/evaluate-feature-flags?input=%7B%22flags%22%3A%5B%5D%7D" on the API listener answers a 403 problem "feature-disabled"
    And the problem names the feature "console"

  @op:evaluateFeatureFlags @case:failure
  Scenario: A cursor of another list is refused
    Given a running infraweave server
    Then "GET /api/v1/evaluate-feature-flags?input=%7B%22flags%22%3A%5B%5D%7D&cursor=nope" on the API listener answers a 400 problem "invalid-cursor"
