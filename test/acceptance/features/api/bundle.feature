Feature: Served API specification
  infraweave serves its own API, authored as fragments in api/src/api and
  bundled into api/api.openapi.yaml. Families start as scaffolds of the
  Spacelift reference, and every scaffolded operation follows the API
  conventions: cursor pagination, idempotency keys and rate limits.
  ref: decision 0003

  Background:
    Given the bundled API

  Scenario: The bundle is what its fragments say
    Then the bundle is up to date with api/src/api
    And the API validates as OpenAPI 3.0.3

  Scenario: The Stacks family is served in full
    Then every reference operation tagged "Stacks" is in the API with the same method, path and operationId

  Scenario: Scaffolded Stacks operations follow the API conventions
    Then "GET /stacks" is cursor-paginated with page schema "StackPage"
    And "POST /stacks" accepts an Idempotency-Key
    And "POST /stacks/search" costs "expensive" and takes no Idempotency-Key
    And every operation tagged "Stacks" can answer 429 and records its conventions
