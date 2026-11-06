@postgres
Feature: API keys while PostgreSQL is down
  Every API key operation answers 503 service-unavailable while PostgreSQL
  is unreachable. The caller's token authenticates from the replica's
  session cache (decision 0006), so each operation fails at the store. The
  server runs on a PostgreSQL of its own, which the scenario stops.

  @op:apiKeys @op:apiKeyCreate @op:apiKey @op:apiKeyUpdate @op:apiKeyReset @op:apiKeyDelete @op:apiKeyEnableNonAdminManagers @case:failure
  Scenario: Every API key operation answers 503 while PostgreSQL is down
    Given a running infraweave server on a PostgreSQL of its own
    When the home key is exchanged through A for token "admin"
    Then the viewer through A with token "admin" answers 200
    When its PostgreSQL stops
    Then "GET /api/v1/api-keys" through A with token "admin" answers a 503 problem "service-unavailable"
    And "POST /api/v1/api-keys" through A with token "admin" and '{"name":"deploy","teams":["ops"]}' answers a 503 problem "service-unavailable"
    And "GET /api/v1/api-keys/01KACME0000000000000000003" through A with token "admin" answers a 503 problem "service-unavailable"
    And "PUT /api/v1/api-keys/01KACME0000000000000000003" through A with token "admin" and '{"name":"renamed"}' answers a 503 problem "service-unavailable"
    And "POST /api/v1/api-keys/01KACME0000000000000000003/reset" through A with token "admin" answers a 503 problem "service-unavailable"
    And "DELETE /api/v1/api-keys/01KACME0000000000000000003" through A with token "admin" answers a 503 problem "service-unavailable"
    And "PUT /api/v1/account/api-key-non-admin-managers" through A with token "admin" and '{"enable":true}' answers a 503 problem "service-unavailable"
