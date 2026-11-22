@postgres
Feature: Organization administrators bound account public capacity
  Public worker slots are an int64 organization pool. An account ceiling
  can reduce its share, and does not create additional organization capacity.

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the operator key is exchanged through A for token "op"
    And the home account's member has a browser session, admitted to home alone
    And token "op" sends "POST /operator/v1/tiers" with '{"name":"Public capacity","features":["organization"],"limits":{"public-worker-slots":3}}' through A
    And it answers 201
    And token "op" gives the home organization the tier "Public capacity" through A
    And token "op" turns billing on through A

  @op:organizationAccountLimitSet @op:organizationAccountLimitClear @case:success
  Scenario: An organization administrator sets and clears a lower account ceiling
    Given the home account's member holds organization-admin
    When the browser sends "PUT /api/v1/organization/accounts/acme-dev/limits/public-worker-slots" with '{"value":2}' through A
    Then the answer sets the account's limit "public-worker-slots" to 2
    When the browser sends "DELETE /api/v1/organization/accounts/acme-dev/limits/public-worker-slots" through B
    Then it answers 200

  @op:organizationAccountLimitSet @case:denial
  Scenario: An account administrator cannot grant public execution capacity
    When the browser sends "PUT /api/v1/organization/accounts/acme-dev/limits/public-worker-slots" with '{"value":2}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ORGANIZATION_ADMIN"'

  @op:organizationAccountLimitSet @case:failure
  Scenario: An account ceiling cannot exceed the organization pool
    Given the home account's member holds organization-admin
    When the browser sends "PUT /api/v1/organization/accounts/acme-dev/limits/public-worker-slots" with '{"value":4}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "public-worker-slots"
