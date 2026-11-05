Feature: A scenario that fails

  @postgres
  Scenario: A scenario that fails on purpose
    Given a running infraweave server
    Then the scenario fails on purpose
