@postgres
Feature: Served API
  infraweave serve answers the API on the API listener under /api/v1
  and the probes on the admin listener alone (decision 0017). Every
  operation answers 501 until a family implements it, and every error,
  the router's own included, is a problem detail (decision 0003). Every
  route, stubbed or not, is rate-limited per caller and cost class, honours
  Idempotency-Key on POST and PATCH, and can be switched off for one caller.
  A stubbed POST answers 501, which releases its key, so an idempotent replay
  needs an operation that succeeds: TestUnitGuardedIdempotency proves it
  until a family serves a POST. Each scenario runs its own infraweave serve
  on a database of its own.

  Scenario: The admin listener answers the liveness probe
    Given a running infraweave server
    Then "GET /livez" on the admin listener answers 200
    And "GET /livez" on the API listener answers a 404 problem "not-found"

  Scenario: Scaffolded operations answer 501 until a family implements them
    Given a running infraweave server
    Then "GET /api/v1/stacks" on the API listener answers a 501 problem "not-implemented"

  Scenario: The router's own errors are problem details
    Given a running infraweave server
    Then "GET /api/v1/nope" on the API listener answers a 404 problem "not-found"
    And "PATCH /api/v1/stacks" on the API listener answers a 405 problem "method-not-allowed"

  Scenario: An exhausted budget answers 429, the expensive class first
    Given a running infraweave server whose standard burst is 4 and expensive burst is 2
    When "POST /api/v1/stacks/search" is sent 2 times on the API listener
    Then "POST /api/v1/stacks/search" on the API listener answers a 429 problem "rate-limited"
    And the response carries Retry-After and the rate-limit headers
    And "GET /api/v1/stacks" on the API listener answers a 501 problem "not-implemented"

  Scenario: A disabled caller gets 503 api-disabled
    Given a running infraweave server that disables the caller "127.0.0.1"
    Then "GET /api/v1/stacks" on the API listener answers a 503 problem "api-disabled"
    And the response carries Retry-After
    And "GET /livez" on the admin listener answers 200

  Scenario: Callers the operator has not disabled are unaffected
    Given a running infraweave server that disables the caller "192.0.2.1"
    Then "GET /api/v1/stacks" on the API listener answers a 501 problem "not-implemented"
