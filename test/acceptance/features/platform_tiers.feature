@postgres
Feature: Tiers, organization tiers, grants and billing through the operator API
  Tiers, each organization's tier and grants, and the billing switch are
  data the operator edits under /operator/v1. Every edit bumps the
  entitlement version of each organization it touches and notifies
  infraweave_invalidate, so every replica applies it on a later request
  without a restart. Enterprise, what every organization holds while billing
  is off, cannot be edited, and a tier an organization holds cannot be
  deleted.
  ref: decision 0021

  @case:success
  Scenario: A new tier, given to an organization with billing on, applies on another replica
    Given two infraweave servers, A and B, on one database, with an operator key
    When the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"
    Then "GET /api/v1/viewer" through B with token "h" eventually answers 200
    When token "op" creates the tier "Gold" with the feature "stacks" through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/viewer" through B with token "h" eventually answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "viewer"
    And "GET /api/v1/stacks" through B with token "h" answers a 501 problem "not-implemented"
    When token "op" grants the home organization the feature "viewer" through A
    Then "GET /api/v1/viewer" through B with token "h" eventually answers 200
    And "DELETE /operator/v1/organizations/acme/grants/features/viewer" through B with token "op" answers '{"tier":"Gold","version":3,"features":["stacks"],"limits":{},"grants":{"features":[],"limits":[]}}'
    And "GET /api/v1/viewer" through A with token "h" eventually answers a 403 problem "feature-not-in-tier"

  @case:failure
  Scenario: Enterprise cannot be edited, and a tier in use cannot be deleted
    Given two infraweave servers, A and B, on one database, with an operator key
    When the operator key is exchanged through A for token "op"
    And token "op" creates the tier "Gold" with the feature "stacks" through A
    And token "op" gives the home organization the tier "Gold" through B
    Then "DELETE /operator/v1/tiers/Gold" through B with token "op" answers a 409 problem "invalid-state"
    And "PUT /operator/v1/tiers/Enterprise" through A with token "op" and '{"features":[],"limits":{}}' answers a 409 problem "invalid-state"
    And "DELETE /operator/v1/tiers/Enterprise" through B with token "op" answers a 409 problem "invalid-state"
    And "POST /operator/v1/tiers" through A with token "op" and '{"name":"Gold","features":[],"limits":{}}' answers a 409 problem "already-exists"
    When token "op" gives the home organization the tier "Free" through A
    Then "DELETE /operator/v1/tiers/Gold" through B with token "op" answers '{"name":"Gold","features":["stacks"],"limits":{},"organizations":0,"editable":true}'

  @case:denial
  Scenario: An account's credential cannot reach the operator API
    Given two infraweave servers, A and B, on one database, with an operator key
    When the home key is exchanged through A for token "h"
    Then "GET /operator/v1/tiers" through A with token "h" answers a 401 problem "unauthenticated"
    And "PUT /operator/v1/billing" through B with token "h" and '{"enabled":true}' answers a 401 problem "unauthenticated"
    And "PUT /operator/v1/organizations/acme/tier" through B with token "h" and '{"tier":"Free"}' answers a 401 problem "unauthenticated"
