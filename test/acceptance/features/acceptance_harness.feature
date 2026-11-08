Feature: Acceptance harness
  Scenarios run against the infraweave binary, each with its own process,
  and name the real dependencies they need by tag. Each dependency starts
  on first use and is shared, and each @postgres scenario gets a database
  of its own in the shared container. Without Docker, a scenario that needs
  it is skipped and named, unless the run requires Docker, and a failed
  scenario leaves its evidence.
  ref: decision 0016

  # Both examples create the same table, so whichever runs second fails
  # if the two share a database, in either order and in parallel.
  @postgres
  Scenario Outline: @postgres scenario <n> gets a database of its own
    Given the scenario's PostgreSQL database
    Then it answers "SELECT count(*) FROM pg_tables WHERE tablename = 'marker'" with "0"
    And it answers "CREATE TABLE marker (n int)" with "CREATE TABLE"

    Examples:
      | n      |
      | first  |
      | second |

  @objectstore
  Scenario: An @objectstore scenario gets the shared object store
    Given the shared object store
    Then its health endpoint answers 200

  @oidc
  Scenario: An @oidc scenario gets the shared OIDC issuer
    Given the shared OIDC issuer
    Then a token it mints for subject "user:alice" verifies against its JWKS

  Scenario: Without Docker, a scenario that needs it is skipped and named
    Given the fixture features in "skip"
    When the suite runs them nested without Docker
    Then the nested run passes
    And its output says "1 scenarios were skipped"
    And its output says "A database scenario (@postgres)"

  Scenario: A run that requires Docker fails without it
    Given the fixture features in "skip"
    When the suite runs them nested without Docker requiring it
    Then the nested run fails
    And its output says "INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1, and no Docker daemon answers"

  @postgres
  Scenario: A failed scenario leaves its evidence
    Given the fixture features in "fail"
    When the suite runs them nested
    Then the nested run fails
    And it left "server.stdout" in the evidence of "A scenario that fails on purpose"
    And it left "config.yaml" in the evidence of "A scenario that fails on purpose"
    And it left "pg_dump.sql" in the evidence of "A scenario that fails on purpose"
