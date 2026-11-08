@postgres
Feature: API tokens and sessions while PostgreSQL is down
  The exchange and every session operation answer 503 service-unavailable
  while PostgreSQL is unreachable. A token whose session a replica holds in
  its cache still authenticates for up to a minute, so the
  operations it reaches fail at the store; a token the replica has not seen
  fails at authentication. The server runs on a PostgreSQL of its own, which
  the scenario stops.
  ref: decision 0006

  @op:apiKeyUser @op:viewer @op:sessions @op:sessionDelete @op:sessionDeleteAll @op:logout @case:failure
  Scenario: Every token and session operation answers 503 while PostgreSQL is down
    Given a running infraweave server on a PostgreSQL of its own
    When the home key is exchanged through A for token "t1"
    And the home key is exchanged through A for token "t2"
    Then the viewer through A with token "t1" answers 200
    When its PostgreSQL stops
    Then "GET /api/v1/sessions" through A with token "t1" answers a 503 problem "service-unavailable"
    And deleting token "t2"'s session through A with token "t1" answers a 503 problem "service-unavailable"
    And "DELETE /api/v1/sessions" through A with token "t1" answers a 503 problem "service-unavailable"
    And "POST /api/v1/auth/logout" through A with token "t1" answers a 503 problem "service-unavailable"
    And "GET /api/v1/viewer" through A with token "t2" answers a 503 problem "service-unavailable"
    And exchanging the home key through A answers a 503 problem "service-unavailable"
