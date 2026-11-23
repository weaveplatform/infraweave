@postgres
Feature: Public pool account reads while PostgreSQL is down
  The public pool's projection, the account's default public runner image
  and the declared outgoing addresses each answer 503 service-unavailable
  while PostgreSQL is unreachable. The projection and the runner image read
  the account's rows; the outgoing addresses come from the server's
  configuration, so they fail where the caller's credential or capability
  must be read from the store. The server runs on a PostgreSQL of its own,
  which the scenario stops.
  ref: decision 0008
  ref: decision 0016

  @op:publicWorkerPool @op:defaultPublicWorkerPoolRunnerImage @op:outgoingIPAddresses @case:failure
  Scenario: The public pool's account reads answer 503 while PostgreSQL is down
    Given a running infraweave server on a PostgreSQL of its own
    When the home key is exchanged through A for token "seen"
    And the home key is exchanged through A for token "unseen"
    Then the viewer through A with token "seen" answers 200
    When its PostgreSQL stops
    Then "GET /api/v1/public-worker-pool" through A with token "seen" answers a 503 problem "service-unavailable"
    And "GET /api/v1/default-public-worker-pool-runner-image" through A with token "seen" answers a 503 problem "service-unavailable"
    And "GET /api/v1/outgoing-ip-addresses" through A with token "unseen" answers a 503 problem "service-unavailable"
