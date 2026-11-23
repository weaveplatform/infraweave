@postgres
Feature: Worker deregistration while PostgreSQL is down
  A worker's deregistration removes its row, so the worker API answers 503
  service-unavailable while PostgreSQL is unreachable and the worker stays
  registered. Once PostgreSQL is back the same session deregisters it. The
  server runs on a PostgreSQL of its own, which the scenario stops.
  ref: decision 0008

  @op:workerDeregister @case:failure
  Scenario: A worker's deregistration answers 503 while PostgreSQL is down and succeeds once it is back
    Given a running infraweave server on a PostgreSQL of its own
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    And the launcher "l" registers with the key of "csr" and the certificate "first" through A
    And it answers 200
    When its PostgreSQL stops
    And token "l" sends "POST /worker/v1/deregister" through A
    Then it answers a 503 problem "service-unavailable"
    When its PostgreSQL starts again
    Then "GET /api/v1/worker-pools" through A with token "h" eventually answers 200
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through A
    Then the answer's workersCount is '1'
    When token "l" sends "POST /worker/v1/deregister" through A
    Then it answers 204
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through A
    Then the answer's workersCount is '0'
