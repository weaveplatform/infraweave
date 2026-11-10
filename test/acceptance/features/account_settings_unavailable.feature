@postgres
Feature: Account settings while PostgreSQL is down
  The account settings a member reads, and the request to enable personal
  API keys, answer 503 service-unavailable while PostgreSQL is
  unreachable. The caller's token authenticates from the replica's session
  cache, so each operation fails at the store. The server runs on a
  PostgreSQL of its own, which the scenario stops.
  ref: decision 0006

  @op:personalApiKeysEnabled @op:accountRequestPersonalApiKeysEnabling @op:awarenessSourceSurvey @case:failure
  Scenario: The settings a member reads answer 503 while PostgreSQL is down
    Given a running infraweave server on a PostgreSQL of its own
    When the home key is exchanged through A for token "admin"
    Then the viewer through A with token "admin" answers 200
    When its PostgreSQL stops
    Then "GET /api/v1/personal-api-keys-enabled" through A with token "admin" answers a 503 problem "service-unavailable"
    And "POST /api/v1/account/request-personal-api-keys-enabling" through A with token "admin" answers a 503 problem "service-unavailable"
    And "GET /api/v1/awareness-source-survey" through A with token "admin" answers a 503 problem "service-unavailable"

  @op:apiKeysManageableByNonHumans @op:apiKeysManagedByNonAdmins @op:accountToggleAPIKeyManagementFromNonHumans @case:failure
  Scenario: Who manages API keys answers 503 while PostgreSQL is down
    Given a running infraweave server on a PostgreSQL of its own
    And the home account's member has a browser session, admitted to home alone
    When the home key is exchanged through A for token "admin"
    Then the viewer through A with token "admin" answers 200
    And the viewer through A in the browser answers 200
    When its PostgreSQL stops
    Then "GET /api/v1/api-keys-manageable-by-non-humans" through A with token "admin" answers a 503 problem "service-unavailable"
    And "GET /api/v1/api-keys-managed-by-non-admins" through A with token "admin" answers a 503 problem "service-unavailable"
    And "POST /api/v1/account/toggle-api-key-management-from-non-humans" through A in the browser with the public URL's Origin answers a 503 problem "service-unavailable"
