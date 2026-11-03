Feature: Served deploy API
  infraweave serve answers the deploy API on the API listener under /api/v1
  and the probes on the admin listener alone (decision 0017). Every
  operation answers 501 until a family implements it, and every error,
  the router's own included, is a problem detail (decision 0003).

  Background:
    Given a running infraweave server

  Scenario: The admin listener answers the liveness probe
    Then "GET /livez" on the admin listener answers 200
    And "GET /livez" on the API listener answers a 404 problem

  Scenario: Scaffolded operations answer 501 until a family implements them
    Then "GET /api/v1/stacks" on the API listener answers a 501 problem

  Scenario: The router's own errors are problem details
    Then "GET /api/v1/nope" on the API listener answers a 404 problem
    And "PATCH /api/v1/stacks" on the API listener answers a 405 problem
