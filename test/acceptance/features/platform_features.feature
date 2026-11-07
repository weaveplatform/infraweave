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
