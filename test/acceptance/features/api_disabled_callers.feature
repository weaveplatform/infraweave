@postgres
Feature: The operator's switch across replicas
  A caller in api_disabled_callers gets 503 api-disabled from every replica
  once the change is announced on infraweave_invalidate, until its until
  passes or the row goes. Other callers, and the probes, are unaffected.
  ref: decisions 0003 and 0005

  Background:
    Given two infraweave servers, A and B, on one database

  Scenario: A caller the table disables is turned away by both replicas
    When the operator disables the caller "api_key:01KACME0000000000000000003" in the table
    Then "GET /api/v1/stacks" through A eventually answers a 503 problem "api-disabled"
    And the response carries Retry-After
    And "GET /api/v1/stacks" through B eventually answers a 503 problem "api-disabled"
    And "GET /livez" on the admin listener answers 200
    When the operator removes the caller "api_key:01KACME0000000000000000003" from the table
    Then "GET /api/v1/stacks" through A eventually answers a 501 problem "not-implemented"
    And "GET /api/v1/stacks" through B eventually answers a 501 problem "not-implemented"

  Scenario: Callers the table does not name are unaffected
    When the operator disables the caller "192.0.2.1" in the table
    Then "GET /api/v1/stacks" through A answers a 501 problem "not-implemented"
    And "GET /api/v1/stacks" through B answers a 501 problem "not-implemented"

  Scenario: A switch whose until has passed is off
    When the operator disables the caller "api_key:01KACME0000000000000000003" in the table until a minute ago
    Then "GET /api/v1/stacks" through A answers a 501 problem "not-implemented"
    And "GET /api/v1/stacks" through B answers a 501 problem "not-implemented"
