@postgres @authorization-scheme
Feature: Authorization scheme reads and writes retain shared API controls

  @op:authorizationScheme @op:accountUpdateAuthorizationScheme @case:denial
  Scenario: Both operations answer a literal 403 when the feature is disabled
    Given the installation switches off the features "account-settings"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "GET /api/v1/authorization-scheme" through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    Then it answers a 403 problem "feature-disabled"

  @op:authorizationScheme @case:failure
  Scenario: The read answers 503 when its real PostgreSQL is unavailable
    Given a running infraweave server on a PostgreSQL of its own
    And the home key is exchanged through A for token "h"
    And the viewer through A with token "h" answers 200
    When token "h" sends "GET /api/v1/authorization-scheme" through A
    Then it answers 200
    When its PostgreSQL stops
    Then "GET /api/v1/authorization-scheme" through A with token "h" answers a 503 problem "service-unavailable"
