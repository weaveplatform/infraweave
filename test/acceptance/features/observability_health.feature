@postgres
Feature: Observability and health
  Every infraweave process logs JSON lines to stdout with decision 0017's
  standard fields, gives every request an ID it echoes and logs, and
  answers /livez, /startupz and /readyz on the admin listener alone. On
  SIGTERM, /readyz fails at once, the listeners keep accepting for
  server.shutdown.ready_delay, in-flight requests drain and the process
  exits 0; a second signal exits 1 at once. A request that needs
  PostgreSQL while it is unreachable answers 503 service-unavailable with
  Retry-After, which pkg/sdk retries. Each scenario runs its own infraweave
  all, or infraweave scheduler, on a database of its own.

  Scenario: The probes pass once the schema is migrated
    Given a running infraweave server
    Then "GET /startupz?verbose" on the admin listener eventually reports the check "schema" as ok
    And "GET /readyz?verbose" on the admin listener eventually reports the check "postgres" as ok
    And "GET /livez" on the admin listener answers 200

  Scenario: Readiness follows PostgreSQL while liveness does not
    Given a running infraweave server on a PostgreSQL of its own
    When its PostgreSQL stops answering
    Then "GET /readyz?verbose" on the admin listener eventually reports the check "postgres" as fail
    And "GET /readyz" on the admin listener eventually answers 503
    And "GET /livez" on the admin listener answers 200
    When its PostgreSQL answers again
    Then "GET /readyz" on the admin listener eventually answers 200

  Scenario: Log lines carry the standard fields and the request ID
    Given a running infraweave server
    When "GET /api/v1/stacks" is sent on the API listener with X-Request-Id "scenario-request-1"
    Then the response carries X-Request-Id "scenario-request-1"
    And the server logs a "request" line with request_id "scenario-request-1", route "/api/v1/stacks" and status 501
    And every line the server logged has the standard fields with mode "serve,scheduler"
    When "GET /api/v1/stacks" is sent on the API listener with X-Request-Id "not a valid id"
    Then the response carries a generated X-Request-Id

  Scenario: SIGTERM drains a request in flight
    Given a running infraweave server whose shutdown ready delay is 3s
    And "POST /api/v1/stacks" with an Idempotency-Key is in flight with half its body
    When the server receives SIGTERM
    Then "GET /readyz" on the admin listener eventually answers 503
    And the API listener eventually refuses connections
    When the rest of the body arrives
    Then the request in flight is answered 501
    And the server exits 0

  Scenario: A second signal exits at once
    Given a running infraweave server whose shutdown ready delay is 10m
    When the server receives SIGTERM
    Then "GET /readyz" on the admin listener eventually answers 503
    When the server receives SIGTERM
    Then the server exits 1

  Scenario: infraweave scheduler serves only the admin listener
    Given a running infraweave scheduler
    Then it bound no API listener
    And "GET /readyz" on the admin listener eventually answers 200
    And every line the server logged has the standard fields with mode "scheduler"

  Scenario: A request answers 503 while PostgreSQL is down and succeeds once it is back
    Given a running infraweave server on a PostgreSQL of its own
    When its PostgreSQL stops
    Then "POST /api/v1/stacks" with an Idempotency-Key answers a 503 problem "service-unavailable" with Retry-After
    When its PostgreSQL starts again
    Then "POST /api/v1/stacks" with an Idempotency-Key eventually answers a 501 problem "not-implemented"

  Scenario: pkg/sdk's default retries ride out a short PostgreSQL outage
    Given a running infraweave server on a PostgreSQL of its own
    When its PostgreSQL stops
    And "POST /api/v1/stacks" with an Idempotency-Key is sent with the SDK's default retries
    And the server logs a 503 for a POST
    And its PostgreSQL starts again
    Then the request sent with the default retries is answered a 501 problem "not-implemented"
