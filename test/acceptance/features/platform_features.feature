@postgres
Feature: Features the installation switches off
  features.disabled switches a feature off for every organization of the
  installation (decision 0021). Each operation names the capability it
  belongs to, so a request for a switched-off one answers 403
  feature-disabled, naming the feature, whatever the organization's tier
  and whether billing is on; every other operation is unaffected, and so is
  a switched-off feature no served operation belongs to.

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
