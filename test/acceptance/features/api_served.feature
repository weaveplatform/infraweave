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
  until a family serves a POST. Every problem names the request it answers
  in its instance, urn:infraweave:request: and the X-Request-Id. Every
  route but POST /auth/token needs a bearer token (decision 0006): the
  scenario's client holds one of a seeded API key, whose caller,
  api_key:<key>, the controls count against, and a request without one
  answers 401 unauthenticated with WWW-Authenticate: Bearer. Each scenario
  runs its own infraweave all on a database of its own.

  Scenario: The admin listener answers the liveness probe
    Given a running infraweave server
    Then "GET /livez" on the admin listener answers 200
    And "GET /livez" on the API listener answers a 404 problem "not-found"

  Scenario: Scaffolded operations answer 501 until a family implements them
    Given a running infraweave server
    Then "GET /api/v1/stacks" on the API listener answers a 501 problem "not-implemented"
    And the problem names the request in its instance

  Scenario: The router's own errors are problem details
    Given a running infraweave server
    Then "GET /api/v1/nope" on the API listener answers a 404 problem "not-found"
    And the problem names the request in its instance
    And "PATCH /api/v1/stacks" on the API listener answers a 405 problem "method-not-allowed"

  Scenario: An exhausted budget answers 429, the expensive class first
    Given a running infraweave server whose standard burst is 4 and expensive burst is 2
    When "POST /api/v1/stacks/search" is sent 2 times on the API listener
    Then "POST /api/v1/stacks/search" on the API listener answers a 429 problem "rate-limited"
    And the response carries Retry-After and the rate-limit headers
    And the problem names the request in its instance
    And "GET /api/v1/stacks" on the API listener answers a 501 problem "not-implemented"

  Scenario: A disabled caller gets 503 api-disabled
    Given a running infraweave server that disables the caller "api_key:01KACME0000000000000000003"
    Then "GET /api/v1/stacks" on the API listener answers a 503 problem "api-disabled"
    And the response carries Retry-After
    And "GET /livez" on the admin listener answers 200

  Scenario: Callers the operator has not disabled are unaffected
    Given a running infraweave server that disables the caller "192.0.2.1"
    Then "GET /api/v1/stacks" on the API listener answers a 501 problem "not-implemented"

  Scenario: A route that needs a credential refuses a request without one
    Given a running infraweave server
    Then "GET /api/v1/stacks" without a credential answers a 401 problem "unauthenticated"
    And the response challenges for a bearer token
    And "GET /api/v1/nope" without a credential answers a 401 problem "unauthenticated"
    And "GET /livez" on the admin listener answers 200
