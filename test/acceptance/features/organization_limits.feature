@postgres
Feature: Organization account limits and usage
  An organization admin sets an account's limit within the organization's
  pool: a value above the pool answers 422, and a value below it refuses the
  account a seat while the pool has room, naming the account's limit. Usage
  counts each account of the organization and no account of another: the
  home account's seats are its member's and the home key's, which every
  launch uses. The member, an account admin, holds the organization role
  only where a scenario gives it.
  ref: decision 0021

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the operator key is exchanged through A for token "op"
    And the home account's member has a browser session, admitted to home alone
    And token "op" creates the tier "Gold" with the feature "organization" and 5 seats through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A

  @op:organizationAccountLimitSet @case:failure
  Scenario: An account limit above the pool, or of no limit, is refused
    Given the home account's member holds organization-admin
    When the browser sends "PUT /api/v1/organization/accounts/acme-dev/limits/seats" with '{"value":6}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "seats"
    When the browser sends "PUT /api/v1/organization/accounts/acme-dev/limits/no-such" with '{"value":1}' through A
    Then it answers a 422 problem "validation-failed"

  @op:organizationUsage @op:organizationLimits @op:organizationAccountLimitSet @op:organizationAccountLimitClear @case:success
  Scenario: Usage counts both accounts' seats, and an account limit is set and cleared
    Given the home account's member holds organization-admin
    Then the usage read through A in the browser counts 2 seats in "acme" and 0 in "acme-dev"
    When the browser sends "PUT /api/v1/organization/accounts/acme-dev/limits/seats" with '{"value":1}' through A
    Then the answer sets the account's limit "seats" to 1
    When the browser sends "GET /api/v1/organization/limits" through B
    Then it answers 200
    When the browser sends "DELETE /api/v1/organization/accounts/acme-dev/limits/seats" through A
    Then it answers 200

  @op:organizationAccountLimitSet @case:denial
  Scenario: An account limit below the pool refuses a seat while the pool has room
    Given the home account's member holds organization-admin
    When the browser sends "PUT /api/v1/organization/accounts/acme-dev/limits/seats" with '{"value":0}' through A
    Then it answers 200
    And the sibling key's first use through A answers a 403 problem "limit-reached"
    And the problem names the limit "seats" of the account at 0

  @op:organizationAccounts @op:organizationUsage @op:organizationAccountUpdate @op:organizationAccountLimitSet @case:denial
  Scenario: A member sees nothing of another account, and an admin nothing of another organization
    Given the stranger key is exchanged through A for token "x"
    When the browser sends "GET /api/v1/organization/accounts" through A
    Then it answers a 403 problem "forbidden"
    Given the home account's member holds organization-admin
    When the browser sends "PATCH /api/v1/organization/accounts/globex" with '{"name":"Mine"}' through B
    Then it answers a 404 problem "not-found"
    When the browser sends "PUT /api/v1/organization/accounts/globex/limits/seats" with '{"value":1}' through A
    Then it answers a 404 problem "not-found"
    When the browser sends "GET /api/v1/organization/usage" through B
    Then the answer leaves out "globex"
    When token "x" sends "GET /api/v1/organization/usage" through A
    Then it answers a 401 problem "unauthenticated"

  @op:organizationLimits @op:organizationAccountLimitClear @case:denial
  Scenario: An account admin without the organization role reads no limit and clears none
    When the browser sends "PUT /api/v1/organization/accounts/acme-dev/limits/seats" with '{"value":1}' through A
    Then it answers a 403 problem "forbidden"
    When the browser sends "GET /api/v1/organization/limits" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ORGANIZATION_ADMIN"'
    When the browser sends "DELETE /api/v1/organization/accounts/acme-dev/limits/seats" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ORGANIZATION_ADMIN"'

  @op:organizationAccountLimitClear @case:failure
  Scenario: Clearing a limit no account holds, or of an account the organization lacks, is refused
    Given the home account's member holds organization-admin
    When the browser sends "DELETE /api/v1/organization/accounts/acme-dev/limits/no-such" through A
    Then it answers a 422 problem "validation-failed"
    When the browser sends "DELETE /api/v1/organization/accounts/no-such/limits/seats" through B
    Then it answers a 404 problem "not-found"
